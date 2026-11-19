package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Version is the usage.json format version.
const Version = 1

// Tokens is one bucket's counts by kind.
type Tokens struct {
	Input        int64 `json:"input"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	CacheRead    int64 `json:"cache_read"`
	Output       int64 `json:"output"`
}

// Total is the sum of all kinds.
func (t Tokens) Total() int64 {
	return t.Input + t.CacheWrite5m + t.CacheWrite1h + t.CacheRead + t.Output
}

func (t *Tokens) add(o Tokens) {
	t.Input += o.Input
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
	t.CacheRead += o.CacheRead
	t.Output += o.Output
}

func (t *Tokens) max(o Tokens) {
	t.Input = max(t.Input, o.Input)
	t.CacheWrite5m = max(t.CacheWrite5m, o.CacheWrite5m)
	t.CacheWrite1h = max(t.CacheWrite1h, o.CacheWrite1h)
	t.CacheRead = max(t.CacheRead, o.CacheRead)
	t.Output = max(t.Output, o.Output)
}

// split divides t into n parts; the remainder of each kind goes to part 0.
func (t Tokens) split(n int) []Tokens {
	out := make([]Tokens, n)
	d := func(v int64, set func(*Tokens, int64)) {
		q, r := v/int64(n), v%int64(n)
		for i := range out {
			x := q
			if i == 0 {
				x += r
			}
			set(&out[i], x)
		}
	}
	d(t.Input, func(p *Tokens, v int64) { p.Input = v })
	d(t.CacheWrite5m, func(p *Tokens, v int64) { p.CacheWrite5m = v })
	d(t.CacheWrite1h, func(p *Tokens, v int64) { p.CacheWrite1h = v })
	d(t.CacheRead, func(p *Tokens, v int64) { p.CacheRead = v })
	d(t.Output, func(p *Tokens, v int64) { p.Output = v })
	return out
}

// Bucket is tokens plus estimated cost for one slice of usage.
type Bucket struct {
	Messages int               `json:"messages"`
	Tokens   Tokens            `json:"tokens"`
	ByModel  map[string]Tokens `json:"by_model"`
	CostUSD  float64           `json:"cost_usd"`           // priced models only
	Unpriced []string          `json:"unpriced,omitempty"` // models with no price; cost is partial
}

// Empty reports whether the bucket counts nothing.
func (b Bucket) Empty() bool { return b.Messages == 0 && b.Tokens.Total() == 0 }

// addTokens counts one message's tokens for model.
func (b *Bucket) addTokens(model string, t Tokens, messages int) {
	b.Messages += messages
	b.Tokens.add(t)
	if b.ByModel == nil {
		b.ByModel = map[string]Tokens{}
	}
	m := b.ByModel[model]
	m.add(t)
	b.ByModel[model] = m
}

// Add merges o into b (counts only; call price to refresh the cost).
func (b *Bucket) Add(o Bucket) {
	b.Messages += o.Messages
	b.Tokens.add(o.Tokens)
	for model, t := range o.ByModel {
		if b.ByModel == nil {
			b.ByModel = map[string]Tokens{}
		}
		m := b.ByModel[model]
		m.add(t)
		b.ByModel[model] = m
	}
}

// split divides b into n parts (tokens per model and the message count),
// with each remainder going to part 0, so the parts sum to b exactly.
func (b Bucket) split(n int) []Bucket {
	out := make([]Bucket, n)
	q, r := b.Messages/n, b.Messages%n
	for i := range out {
		out[i].Messages = q
		if i == 0 {
			out[i].Messages += r
		}
	}
	for model, t := range b.ByModel {
		for i, part := range t.split(n) {
			out[i].addTokens(model, part, 0)
		}
	}
	return out
}

// price recomputes CostUSD and Unpriced from ByModel.
func (b *Bucket) price(p Prices) {
	b.CostUSD, b.Unpriced = 0, nil
	for model, t := range b.ByModel {
		if t.Total() == 0 {
			continue
		}
		pr, ok := p.Lookup(model)
		if !ok {
			b.Unpriced = append(b.Unpriced, model)
			continue
		}
		b.CostUSD += pr.Cost(t)
	}
	sort.Strings(b.Unpriced)
}

// PRUsage is one PR's usage.
type PRUsage struct {
	Director Bucket            `json:"director"`
	Workers  map[string]Bucket `json:"workers"` // by worker name, fix workers included (w-03, w-03b)
	Total    Bucket            `json:"total"`
}

// Report is usage.json.
type Report struct {
	Version   int                 `json:"version"` // 1
	UpdatedAt time.Time           `json:"updated_at"`
	Total     Bucket              `json:"total"`    // director + workers
	Director  Bucket              `json:"director"` // all director usage, overhead included
	Workers   Bucket              `json:"workers"`  // all worker usage
	Overhead  Bucket              `json:"overhead"` // director usage not attributed to a PR (planning, other subagents)
	PRs       map[string]*PRUsage `json:"prs"`      // by PR id
	Warnings  []string            `json:"warnings,omitempty"`
	Sources   []Source            `json:"sources"` // per transcript file, for merging; renderers ignore it
}

// Source kinds.
const (
	KindDirector = "director" // a director session's main transcript
	KindWorker   = "worker"   // a subagent that is an assigned worker
	KindAgent    = "agent"    // any other subagent ("other agents": director overhead)
)

// Source is one transcript file and what it contributed. Stored so that a
// refresh can skip unchanged files and keep the numbers of deleted ones.
type Source struct {
	Path    string    `json:"path"`
	Kind    string    `json:"kind"`
	Session string    `json:"session"`
	Agent   string    `json:"agent,omitempty"` // subagent name (worker or other)
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
	Missing bool      `json:"missing,omitempty"` // file deleted; contribution kept from an earlier refresh
	// Context fingerprints the inputs the contribution was computed with
	// (window, assigns, PR ids); a change forces a re-parse.
	Context string `json:"context"`
	// Teardowns are the successful `prstk teardown` calls in a director
	// transcript (kept so the window survives the file being deleted).
	Teardowns []Teardown `json:"teardowns,omitempty"`
	// Contributions are this file's attributed usage.
	Contributions []Contribution `json:"contributions"`
}

// Teardown is one `prstk teardown` call found in a director transcript.
type Teardown struct {
	At         time.Time `json:"at"`
	Initiative string    `json:"initiative,omitempty"` // its --initiative, if any
}

// Contribution is one bucket of a source's usage: a PR's director share
// (Worker ""), a worker's share of a PR, or overhead (PR "" and Worker "").
type Contribution struct {
	PR     string `json:"pr,omitempty"`
	Worker string `json:"worker,omitempty"`
	Usage  Bucket `json:"usage"`
}

// Load reads usage.json. A missing file is (nil, nil).
func Load(path string) (*Report, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

// JSON renders the report as indented JSON.
func (r *Report) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// FormatTokens renders a token count compactly: 950, 12.3k, 4.56M.
func FormatTokens(n int64) string {
	neg := ""
	if n < 0 {
		neg, n = "-", -n
	}
	f := float64(n)
	switch {
	case n < 1000:
		return neg + strconv.FormatInt(n, 10)
	case f < 999_950:
		return neg + strconv.FormatFloat(f/1e3, 'f', 1, 64) + "k"
	case f < 999_995_000:
		return neg + strconv.FormatFloat(f/1e6, 'f', 2, 64) + "M"
	default:
		return neg + strconv.FormatFloat(f/1e9, 'f', 2, 64) + "B"
	}
}

// FormatUSD renders a dollar amount: $0.42, $12.30, $1,234.
func FormatUSD(f float64) string {
	neg := ""
	if f < 0 {
		neg, f = "-", -f
	}
	if f < 999.995 {
		return neg + "$" + strconv.FormatFloat(f, 'f', 2, 64)
	}
	s := strconv.FormatInt(int64(math.Round(f)), 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return neg + "$" + b.String()
}
