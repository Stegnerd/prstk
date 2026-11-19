package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// parserVersion is part of every source's context: bump it when parsing or
// attribution changes so stored contributions are recomputed.
const parserVersion = "1"

// Input is what Collect needs to know about the initiative.
type Input struct {
	// ClaudeDir is Claude Code's config dir ($CLAUDE_CONFIG_DIR or ~/.claude).
	ClaudeDir string
	// Initiative is the initiative's directory name, matched against a
	// prstk --initiative flag.
	Initiative string
	// Created is PLAN.md's created time.
	Created time.Time
	// Sessions are the director's recorded session ids.
	Sessions []string
	// PRs are the initiative's PR ids.
	PRs []string
	// Assigns are LOG.md's assign entries (ParseAssigns).
	Assigns []Assign
	// Prices are config.yaml overrides of DefaultPrices.
	Prices map[string]Price
	// Now stamps UpdatedAt.
	Now time.Time
}

// collector carries one Collect run.
type collector struct {
	in       Input
	prices   Prices
	prs      map[string]bool
	workers  map[string]bool
	prev     map[string]Source
	parsed   map[string]*transcript
	warnings []string
	end      time.Time            // zero = unbounded
	starts   map[string]time.Time // director session → exclusive window start (zero = none)
}

// Collect refreshes the report from the transcripts, merging with prev (the
// previous usage.json, or nil). It doesn't fail: problems become Warnings.
func Collect(in Input, prev *Report) *Report {
	c := &collector{
		in: in, prices: WithOverrides(in.Prices),
		prs: map[string]bool{}, workers: map[string]bool{},
		prev: map[string]Source{}, parsed: map[string]*transcript{},
		starts: map[string]time.Time{},
	}
	for _, id := range in.PRs {
		c.prs[id] = true
	}
	for _, a := range in.Assigns {
		c.workers[a.Worker] = true
	}
	if prev != nil {
		for _, s := range prev.Sources {
			c.prev[s.Path] = s
		}
	}

	found := c.discover()

	// Pass 1: director transcripts give the teardown calls, which set the
	// window for everything else.
	var sources []Source
	for _, f := range found {
		if f.Kind == KindDirector {
			sources = append(sources, c.directorTeardowns(f))
		}
	}
	for _, s := range c.prev {
		if s.Kind == KindDirector && !containsPath(sources, s.Path) {
			s.Missing = true
			sources = append(sources, s)
		}
	}
	c.window(sources)

	// Pass 2: attribute every file (or reuse what's stored).
	for i := range sources {
		c.attribute(&sources[i])
	}
	for _, f := range found {
		if f.Kind != KindDirector {
			c.attribute(&f)
			sources = append(sources, f)
		}
	}
	for _, s := range c.prev {
		if s.Kind != KindDirector && !containsPath(sources, s.Path) {
			s.Missing = true
			sources = append(sources, s)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })

	r := c.derive(sources)
	r.UpdatedAt = in.Now
	r.Warnings = c.warnings
	return r
}

func containsPath(ss []Source, p string) bool {
	for _, s := range ss {
		if s.Path == p {
			return true
		}
	}
	return false
}

func (c *collector) warn(format string, args ...any) {
	c.warnings = append(c.warnings, fmt.Sprintf(format, args...))
}

// discover lists the transcript files of the recorded sessions, with their
// size and mtime.
func (c *collector) discover() []Source {
	if c.in.ClaudeDir == "" {
		c.warn("no Claude Code directory")
		return nil
	}
	if _, err := os.Stat(c.in.ClaudeDir); err != nil {
		c.warn("no Claude Code directory at %s", c.in.ClaudeDir)
		return nil
	}
	if len(c.in.Sessions) == 0 {
		c.warn("no director sessions recorded yet")
		return nil
	}
	projects := filepath.Join(c.in.ClaudeDir, "projects")
	var out []Source
	for _, id := range c.in.Sessions {
		mains, _ := filepath.Glob(filepath.Join(projects, "*", id+".jsonl"))
		if len(mains) == 0 {
			if _, kept := c.prevForSession(id); !kept {
				c.warn("no transcript for director session %s", id)
			}
		}
		for _, p := range mains {
			if s, ok := stat(p); ok {
				s.Kind, s.Session = KindDirector, id
				out = append(out, s)
			}
		}
		subs, _ := filepath.Glob(filepath.Join(projects, "*", id, "subagents", "agent-*.jsonl"))
		for _, p := range subs {
			s, ok := stat(p)
			if !ok {
				continue
			}
			s.Session, s.Agent, s.Kind = id, subagentName(p), KindAgent
			if s.Agent != "" && c.workers[s.Agent] {
				s.Kind = KindWorker
			}
			out = append(out, s)
		}
	}
	return out
}

func (c *collector) prevForSession(id string) (Source, bool) {
	for _, s := range c.prev {
		if s.Kind == KindDirector && s.Session == id {
			return s, true
		}
	}
	return Source{}, false
}

func stat(p string) (Source, bool) {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return Source{}, false
	}
	return Source{Path: p, Size: fi.Size(), ModTime: fi.ModTime().UTC()}, true
}

// unchanged reports whether the stored source for s's path has the same
// size and mtime, so its parse results can be reused.
func (c *collector) unchanged(s Source) (Source, bool) {
	p, ok := c.prev[s.Path]
	if !ok || p.Missing || p.Size != s.Size || !p.ModTime.Equal(s.ModTime) {
		return Source{}, false
	}
	return p, true
}

func (c *collector) parse(path string) *transcript {
	if t, ok := c.parsed[path]; ok {
		return t
	}
	t, err := parseTranscript(path)
	if err != nil {
		c.warn("read %s: %v", path, err)
		t = &transcript{errored: map[string]bool{}}
	}
	if t.malformed > 0 {
		c.warn("%s: skipped %d malformed line(s)", path, t.malformed)
	}
	c.parsed[path] = t
	return t
}

// directorTeardowns fills s.Teardowns, from the stored source if the file
// hasn't changed, else by parsing it.
func (c *collector) directorTeardowns(s Source) Source {
	if p, ok := c.unchanged(s); ok {
		s.Teardowns = p.Teardowns
		return s
	}
	t := c.parse(s.Path)
	s.Teardowns = nil
	for _, m := range t.msgs {
		if m.sidechain {
			continue
		}
		for _, b := range m.bash {
			if t.errored[b.id] {
				continue // refused or failed: the initiative wasn't torn down
			}
			for _, inv := range parseInvocations(b.command) {
				if inv.Sub == "teardown" {
					s.Teardowns = append(s.Teardowns, Teardown{At: m.at, Initiative: inv.Initiative})
				}
			}
		}
	}
	return s
}

// window sets the initiative's end (the first teardown of this initiative at
// or after Created) and each director session's start (its last teardown
// before Created, of any initiative).
func (c *collector) window(directors []Source) {
	for _, s := range directors {
		for _, td := range s.Teardowns {
			if td.At.Before(c.in.Created) {
				if td.At.After(c.starts[s.Session]) {
					c.starts[s.Session] = td.At
				}
				continue
			}
			if td.Initiative != "" && td.Initiative != c.in.Initiative {
				continue
			}
			if c.end.IsZero() || td.At.Before(c.end) {
				c.end = td.At
			}
		}
	}
}

// inEnd reports whether t is at or before the window end.
func (c *collector) inEnd(t time.Time) bool { return c.end.IsZero() || !t.After(c.end) }

// context fingerprints what a source's contribution depends on.
func (c *collector) context(s Source) string {
	var b strings.Builder
	fmt.Fprintf(&b, "v%s|%s|%s|%s|%s|%s|%s|", parserVersion, s.Kind, s.Agent, c.in.Initiative,
		c.in.Created.UTC().Format(time.RFC3339Nano), c.end.UTC().Format(time.RFC3339Nano),
		c.starts[s.Session].UTC().Format(time.RFC3339Nano))
	ids := append([]string(nil), c.in.PRs...)
	sort.Strings(ids)
	b.WriteString(strings.Join(ids, ","))
	for _, a := range c.in.Assigns {
		fmt.Fprintf(&b, "|%s=%s@%d", a.PR, a.Worker, a.At.Unix())
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// attribute fills s.Contributions, reusing the stored ones when the file and
// the context are unchanged. Missing sources keep what they have.
func (c *collector) attribute(s *Source) {
	if s.Missing {
		return
	}
	ctx := c.context(*s)
	if p, ok := c.unchanged(*s); ok && p.Context == ctx {
		s.Context, s.Contributions = ctx, p.Contributions
		return
	}
	t := c.parse(s.Path)
	acc := newAccum()
	switch s.Kind {
	case KindDirector:
		c.segmentDirector(t, s.Session, acc)
	case KindWorker:
		for _, m := range t.msgs {
			if !m.counted() || m.at.Before(c.in.Created) || !c.inEnd(m.at) {
				continue
			}
			pr := prFor(c.in.Assigns, s.Agent, m.at)
			if pr == "" {
				acc.add("", "", m) // not on any PR: director overhead
			} else {
				acc.add(pr, s.Agent, m)
			}
		}
	default: // other subagents: director overhead within the session window
		start := c.starts[s.Session]
		for _, m := range t.msgs {
			if m.counted() && m.at.After(start) && c.inEnd(m.at) {
				acc.add("", "", m)
			}
		}
	}
	s.Context, s.Contributions = ctx, acc.list()
}

// segmentDirector attributes a director transcript's in-window messages to
// PRs by the prstk commands they run (see the package doc).
func (c *collector) segmentDirector(t *transcript, session string, acc *accum) {
	start := c.starts[session]
	var msgs []*message
	for _, m := range t.msgs {
		if !m.at.After(start) || !c.inEnd(m.at) {
			continue
		}
		if m.sidechain {
			if m.counted() {
				acc.add("", "", m)
			}
			continue
		}
		msgs = append(msgs, m)
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].at.Before(msgs[j].at) })

	var pending Bucket
	for _, m := range msgs {
		if m.counted() {
			pending.addTokens(m.model, m.tokens, 1)
		}
		ids, planning := c.classify(m)
		switch {
		case len(ids) > 0:
			for i, part := range pending.split(len(ids)) {
				acc.addBucket(ids[i], "", part)
			}
			pending = Bucket{}
		case planning:
			acc.addBucket("", "", pending)
			pending = Bucket{}
		}
	}
	acc.addBucket("", "", pending)
}

// classify returns the distinct PR ids a message's prstk calls name (in
// order of appearance), and whether it ran any other prstk command.
func (c *collector) classify(m *message) (ids []string, planning bool) {
	for _, b := range m.bash {
		for _, inv := range parseInvocations(b.command) {
			if inv.Initiative != "" && inv.Initiative != c.in.Initiative {
				continue
			}
			if id := inv.prID(); id != "" && c.prs[id] {
				if !slices.Contains(ids, id) {
					ids = append(ids, id)
				}
				continue
			}
			planning = true
		}
	}
	return ids, planning
}

// accum groups contributions by (PR, worker).
type accum struct {
	m map[[2]string]*Bucket
}

func newAccum() *accum { return &accum{m: map[[2]string]*Bucket{}} }

func (a *accum) bucket(pr, worker string) *Bucket {
	k := [2]string{pr, worker}
	b := a.m[k]
	if b == nil {
		b = &Bucket{}
		a.m[k] = b
	}
	return b
}

func (a *accum) add(pr, worker string, m *message) {
	a.bucket(pr, worker).addTokens(m.model, m.tokens, 1)
}

func (a *accum) addBucket(pr, worker string, b Bucket) {
	if b.Empty() {
		return
	}
	a.bucket(pr, worker).Add(b)
}

func (a *accum) list() []Contribution {
	out := []Contribution{}
	for k, b := range a.m {
		out = append(out, Contribution{PR: k[0], Worker: k[1], Usage: *b})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PR != out[j].PR {
			return out[i].PR < out[j].PR
		}
		return out[i].Worker < out[j].Worker
	})
	return out
}

// derive sums the sources into the report's buckets and prices them.
func (c *collector) derive(sources []Source) *Report {
	r := &Report{Version: Version, PRs: map[string]*PRUsage{}, Sources: sources}
	pr := func(id string) *PRUsage {
		p := r.PRs[id]
		if p == nil {
			p = &PRUsage{Workers: map[string]Bucket{}}
			r.PRs[id] = p
		}
		return p
	}
	for _, id := range c.in.PRs {
		pr(id)
	}
	for i := range sources {
		for j := range sources[i].Contributions {
			k := &sources[i].Contributions[j]
			k.Usage.price(c.prices)
			switch {
			case k.Worker != "":
				p := pr(k.PR)
				w := p.Workers[k.Worker]
				w.Add(k.Usage)
				p.Workers[k.Worker] = w
			case k.PR == "":
				r.Overhead.Add(k.Usage)
			default:
				pr(k.PR).Director.Add(k.Usage)
			}
		}
	}
	r.Director.Add(r.Overhead)
	for _, p := range r.PRs {
		p.Total.Add(p.Director)
		r.Director.Add(p.Director)
		for name, w := range p.Workers {
			p.Total.Add(w)
			r.Workers.Add(w)
			w.price(c.prices)
			p.Workers[name] = w
		}
		p.Director.price(c.prices)
		p.Total.price(c.prices)
	}
	r.Total.Add(r.Director)
	r.Total.Add(r.Workers)
	for _, b := range []*Bucket{&r.Overhead, &r.Director, &r.Workers, &r.Total} {
		b.price(c.prices)
	}
	return r
}
