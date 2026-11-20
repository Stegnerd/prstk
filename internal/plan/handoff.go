package plan

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// LogEntry is one LOG.md entry: "## <time> · <type> · <by>" plus its body.
type LogEntry struct {
	At   time.Time // zero when the heading's time doesn't parse
	Type string
	By   string
	Body string
}

var logHeadingRe = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) · ([^·]+?) · (.*)$`)

// ParseLog reads LOG.md text into entries, in file order. Timestamps are in
// loc (LOG.md is written in local time). Text before the first entry heading
// (the "# Log" title) is ignored.
func ParseLog(text string, loc *time.Location) []LogEntry {
	var out []LogEntry
	var body []string
	flush := func() {
		if len(out) > 0 {
			out[len(out)-1].Body = strings.TrimSpace(strings.Join(body, "\n"))
		}
		body = body[:0]
	}
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if m := logHeadingRe.FindStringSubmatch(strings.TrimRight(l, " \t")); m != nil {
			flush()
			at, _ := time.ParseInLocation("2006-01-02 15:04", m[1], loc)
			out = append(out, LogEntry{At: at, Type: strings.TrimSpace(m[2]), By: strings.TrimSpace(m[3])})
			continue
		}
		body = append(body, l)
	}
	flush()
	return out
}

// ReadLog returns the initiative's LOG.md entries (none when the file is
// missing).
func (in *Initiative) ReadLog() ([]LogEntry, error) {
	b, err := os.ReadFile(in.LogPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ParseLog(string(b), time.Local), nil
}

// LogSinceHandoff finds the latest `handoff` entry and counts the entries
// after it that are not themselves handoff entries. found is false when the
// log has no handoff entry. It goes by entry order, never by timestamps or
// file times.
func LogSinceHandoff(log []LogEntry) (last LogEntry, since int, found bool) {
	idx := latestIndex(log, "handoff")
	if idx < 0 {
		return LogEntry{}, 0, false
	}
	for _, e := range log[idx+1:] {
		if e.Type != "handoff" {
			since++
		}
	}
	return log[idx], since, true
}

func latestIndex(log []LogEntry, typ string) int {
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].Type == typ {
			return i
		}
	}
	return -1
}

// HandoffPointKind is the moment at which a handoff was allowed.
type HandoffPointKind string

const (
	// PointPlanning is right after planning, before the first assign.
	PointPlanning HandoffPointKind = "planning"
	// PointMerges is after a wave of merges, with no worker assigned.
	PointMerges HandoffPointKind = "merges"
)

// HandoffAllowed says when a handoff is allowed. Every refusal ends with it.
const HandoffAllowed = "handoff is allowed right after planning (before the first assign) or after a wave of merges (no worker assigned)"

// HandoffPoint is the handoff gate. It decides from the PR specs, LOG.md
// entries and the validation issues whether a handoff may be written now,
// and at which point. There is no override: the only way to a handoff is to
// reach a point.
//
//   - refused while any open PR has a worker recorded or a status that holds
//     a worker slot (the worker field is checked as well as the status, so a
//     status edited to look idle doesn't open the gate);
//   - refused when every PR is merged or abandoned (nothing left to hand off);
//   - PointPlanning: no PR has started and validation has no errors;
//   - PointMerges: at least one `merged` entry after the latest `handoff`
//     entry (any `merged` entry when there is no handoff entry yet).
//
// When both could hold, PointPlanning is reported.
func HandoffPoint(in *Initiative, log []LogEntry, issues []Issue) (HandoffPointKind, error) {
	if len(in.PRs) == 0 {
		return "", fmt.Errorf("the plan has no PRs yet; nothing to hand off")
	}
	var live []string
	open := 0
	for _, p := range in.PRs {
		if p.Status == Merged || p.Status == Abandoned {
			continue
		}
		open++
		switch {
		case p.Worker != "" || p.Status.HoldsSlot():
			w := p.Worker
			if w == "" {
				w = "no worker recorded"
			}
			live = append(live, fmt.Sprintf("%s (%s, %s)", p.ID, w, p.Status))
		case !p.Status.Known():
			live = append(live, fmt.Sprintf("%s (unknown status %q)", p.ID, p.Status))
		}
	}
	if len(live) > 0 {
		return "", fmt.Errorf("refusing: workers are live: %s; %s", strings.Join(live, ", "), HandoffAllowed)
	}
	if open == 0 {
		return "", fmt.Errorf("refusing: every PR is merged or abandoned; nothing left to hand off; run prstk teardown")
	}

	started := false
	for _, p := range in.PRs {
		if p.Status.Started() {
			started = true
			break
		}
	}
	if !started {
		if errs, _ := CountErrors(issues); errs > 0 {
			return "", fmt.Errorf("refusing: the plan has %d validation error(s); run prstk validate and fix them first", errs)
		}
		return PointPlanning, nil
	}

	last := latestIndex(log, "handoff")
	for _, e := range log[last+1:] {
		if e.Type == "merged" {
			return PointMerges, nil
		}
	}
	return "", fmt.Errorf("refusing: no merge since the last handoff and work has started; %s", HandoffAllowed)
}

// OpenPRs counts the PRs that are neither merged nor abandoned.
func (in *Initiative) OpenPRs() int {
	n := 0
	for _, p := range in.PRs {
		if p.Status != Merged && p.Status != Abandoned {
			n++
		}
	}
	return n
}
