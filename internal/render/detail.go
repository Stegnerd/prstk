package render

import (
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"prstk/internal/plan"
)

// LogEntry is one LOG.md entry (§4 LOG.md).
type LogEntry struct {
	Time string   `json:"time"` // as written, "2006-01-02 15:04"
	Type string   `json:"type"`
	By   string   `json:"by"`   // "prstk" | "director" | other
	Body string   `json:"body"` // markdown, may be multi-line
	PRs  []string `json:"prs"`  // known PR ids mentioned as standalone tokens; never nil
}

// logHeadRe matches an entry header: "## <time> · <type> · <by>". The time
// must look like "2006-01-02 15:04" so a stray "## a · b · c" heading inside
// a director's entry body doesn't start a new entry.
var logHeadRe = regexp.MustCompile(`^##[ \t]+(\d{4}-\d{2}-\d{2}[ \t]+\d{2}:\d{2})[ \t]*·[ \t]*([^·]*?)[ \t]*·[ \t]*(.*?)[ \t]*$`)

// ParseLog splits LOG.md into entries, in file order (no sorting by time).
// Text before the first entry header is ignored. Each entry's PRs lists the
// ids from knownIDs that its type or body mention as standalone tokens (see
// mentions), in knownIDs order.
func ParseLog(text string, knownIDs []string) []LogEntry {
	out := []LogEntry{}
	var cur *LogEntry
	var body []string
	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.TrimSpace(strings.Join(body, "\n"))
		cur.PRs = mentions(cur.Type+"\n"+cur.Body, knownIDs)
		out = append(out, *cur)
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := logHeadRe.FindStringSubmatch(line); m != nil {
			flush()
			cur = &LogEntry{Time: strings.Join(strings.Fields(m[1]), " "), Type: m[2], By: m[3]}
			body = body[:0]
			continue
		}
		if cur != nil {
			body = append(body, line)
		}
	}
	flush()
	return out
}

// mentions returns the ids in known that appear in s as a whole token, in
// known's order. A token is a maximal run of letters, digits, '_' and '-',
// where '.' and ':' also join when they sit between two letters/digits. So
// "02" is found in "02 → v-1", "on 02.", "(02)" and "02/03", but not in
// "102", "2026-11-02", "v-02", "02-board", "02a" or "15:02".
func mentions(s string, known []string) []string {
	toks := map[string]bool{}
	isWord := func(r rune) bool { return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	isAlnum := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	start := -1
	prev := utf8.RuneError
	for i, r := range s {
		in := isWord(r)
		if !in && (r == '.' || r == ':') && start >= 0 && isAlnum(prev) {
			next, _ := utf8.DecodeRuneInString(s[i+utf8.RuneLen(r):])
			in = isAlnum(next)
		}
		if in && start < 0 {
			start = i
		} else if !in && start >= 0 {
			toks[s[start:i]] = true
			start = -1
		}
		prev = r
	}
	if start >= 0 {
		toks[s[start:]] = true
	}
	out := []string{}
	for _, id := range known {
		if id != "" && toks[id] {
			out = append(out, id)
			delete(toks, id) // dedupe if known repeats an id
		}
	}
	return out
}

// ReviewRound is one "### ..." round under "## Review: Director".
type ReviewRound struct {
	Heading string `json:"heading"` // the "### ..." line without the hashes
	Body    string `json:"body"`
}

// SpecParts is a spec body split into the director-written spec and the
// review sections (§4 spec.md body sections).
type SpecParts struct {
	Spec           string        `json:"spec"`            // body up to "## Review: Director"
	DirectorRounds []ReviewRound `json:"director_rounds"` // each "### " under "## Review: Director"; never nil
	Dalton         string        `json:"dalton"`          // everything under "## Review: Dalton", trimmed
}

const (
	headDirector = "## Review: Director"
	headDalton   = "## Review: Dalton"
)

// SplitSpec splits a spec body. Headings are recognised only at the start of
// a line and outside fenced code blocks. Spec is everything before the first
// review heading. The Director section runs to the Dalton heading (or the
// end); the Dalton section runs to the Director heading (or the end), so
// either order works. Text under "## Review: Director" before its first
// "### " is kept as a round with an empty Heading rather than dropped.
func SplitSpec(body string) SpecParts {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	const (
		inSpec = iota
		inDirector
		inDalton
	)
	sect := inSpec
	var spec, dalton []string
	rounds := []ReviewRound{}
	var pre []string // director text before its first ###
	var cur *ReviewRound
	var curBody []string
	endRound := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(curBody, "\n"))
			rounds = append(rounds, *cur)
			cur, curBody = nil, nil
		}
	}
	fence := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fence == "" {
			switch strings.TrimRight(line, " \t") {
			case headDirector:
				endRound()
				sect = inDirector
				continue
			case headDalton:
				endRound()
				sect = inDalton
				continue
			}
		}
		if f := fenceMarker(trimmed); f != "" {
			if fence == "" {
				fence = f
			} else if strings.HasPrefix(trimmed, fence) && strings.TrimLeft(trimmed, fence[:1]) == "" {
				fence = ""
			}
		}
		switch sect {
		case inSpec:
			spec = append(spec, line)
		case inDalton:
			dalton = append(dalton, line)
		case inDirector:
			if fence == "" && strings.HasPrefix(line, "### ") {
				endRound()
				cur = &ReviewRound{Heading: strings.TrimSpace(strings.TrimPrefix(line, "### "))}
				continue
			}
			if cur != nil {
				curBody = append(curBody, line)
			} else {
				pre = append(pre, line)
			}
		}
	}
	endRound()
	if p := strings.TrimSpace(strings.Join(pre, "\n")); p != "" {
		rounds = append([]ReviewRound{{Body: p}}, rounds...)
	}
	return SpecParts{
		Spec:           strings.TrimSpace(strings.Join(spec, "\n")),
		DirectorRounds: rounds,
		Dalton:         strings.TrimSpace(strings.Join(dalton, "\n")),
	}
}

// fenceMarker returns the run of ``` or ~~~ (3+) a line starts with, or "".
func fenceMarker(trimmed string) string {
	for _, c := range []string{"`", "~"} {
		n := len(trimmed) - len(strings.TrimLeft(trimmed, c))
		if n >= 3 {
			return trimmed[:n]
		}
	}
	return ""
}

// PRDetail is the PR detail page payload.
type PRDetail struct {
	Card              // embed the existing board Card (id, title, status, PR link, …)
	Branch string     `json:"branch"`
	Worker string     `json:"worker"`
	Model  string     `json:"model"`
	Parent string     `json:"parent"` // "" = base branch
	Spec   SpecParts  `json:"spec"`
	Notes  string     `json:"notes"` // raw notes.md markdown
	Log    []LogEntry `json:"log"`   // entries whose PRs include this id
}

// BuildPRDetail builds the detail payload for PR id; false if id is not a PR
// of in. Files are read only through paths of the loaded initiative (the PR's
// own notes.md and the initiative's LOG.md); id never becomes a path.
// Missing notes.md or LOG.md read as empty.
func BuildPRDetail(in *plan.Initiative, id string, now time.Time) (PRDetail, bool) {
	p := in.PR(id)
	if p == nil {
		return PRDetail{}, false
	}
	_ = now // reserved: the card's "since" is rendered relative on the client
	d := PRDetail{
		Card: buildCard(in, p), Branch: p.Branch, Worker: p.Worker, Model: p.Model, Parent: p.Parent,
		Spec: SplitSpec(p.Body()), Log: []LogEntry{},
	}
	if b, err := os.ReadFile(p.NotesPath()); err == nil {
		d.Notes = string(b)
	}
	for _, e := range InitiativeLog(in) {
		for _, x := range e.PRs {
			if x == p.ID {
				d.Log = append(d.Log, e)
				break
			}
		}
	}
	return d, true
}

// InitiativeLog parses in's LOG.md with in's PR ids as the known ids. A
// missing LOG.md yields an empty (non-nil) list.
func InitiativeLog(in *plan.Initiative) []LogEntry {
	b, err := os.ReadFile(in.LogPath())
	if err != nil {
		return []LogEntry{}
	}
	ids := make([]string, 0, len(in.PRs))
	for _, p := range in.PRs {
		ids = append(ids, p.ID)
	}
	return ParseLog(string(b), ids)
}
