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
	// PointWaiting is right before the human merges: every open PR waits on
	// the human, with no worker assigned and nothing left to assign.
	PointWaiting HandoffPointKind = "waiting"
)

// HandoffAllowed says when a handoff is allowed. Every refusal ends with it.
const HandoffAllowed = "handoff is allowed right after planning (before the first assign) or when every open PR is waiting on the human (no worker assigned, nothing to assign)"

// HandoffPoint is the handoff gate. It decides from the PR specs, LOG.md
// entries and the validation issues whether a handoff may be written now,
// and at which point. There is no override: the only way to a handoff is to
// reach a point.
//
//   - refused while any open PR has a worker recorded or a status that holds
//     a worker slot (the worker field is checked as well as the status, so a
//     status edited to look idle doesn't open the gate);
//   - refused for a PR with an unknown status;
//   - refused when every PR is merged or abandoned (nothing left to hand off);
//   - PointPlanning: no PR has started and validation has no errors;
//   - PointWaiting: at least one PR is awaiting-you, `prstk next` would list
//     nothing, and a `ready` entry follows the latest `handoff` entry (any
//     `ready` entry when there is no handoff entry yet).
//
// When both could hold, PointPlanning is reported.
func HandoffPoint(in *Initiative, log []LogEntry, issues []Issue) (HandoffPointKind, error) {
	if len(in.PRs) == 0 {
		return "", fmt.Errorf("the plan has no PRs yet; nothing to hand off")
	}
	var live, unknown []string
	open, awaiting := 0, 0
	for _, p := range in.PRs {
		if p.Status == Merged || p.Status == Abandoned {
			continue
		}
		open++
		if p.Status == AwaitingYou {
			awaiting++
		}
		switch {
		case p.Worker != "" || p.Status.HoldsSlot():
			w := p.Worker
			if w == "" {
				w = "no worker recorded"
			}
			live = append(live, fmt.Sprintf("%s (%s, %s)", p.ID, w, p.Status))
		case !p.Status.Known():
			unknown = append(unknown, fmt.Sprintf("%s has an unknown status %q", p.ID, p.Status))
		}
	}
	if len(live) > 0 {
		return "", fmt.Errorf("refusing: workers are live: %s; %s", strings.Join(live, ", "), HandoffAllowed)
	}
	if len(unknown) > 0 {
		return "", fmt.Errorf("refusing: %s; fix its spec with prstk status", strings.Join(unknown, ", "))
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

	if cands, _ := in.Next(); len(cands) > 0 {
		ids := make([]string, len(cands))
		for i, c := range cands {
			ids[i] = c.ID
		}
		return "", fmt.Errorf("refusing: prstk next lists work (%s), so a handoff would stall it; %s", strings.Join(ids, ", "), HandoffAllowed)
	}
	if awaiting == 0 {
		return "", fmt.Errorf("refusing: no PR is awaiting-you; %s", HandoffAllowed)
	}
	last := latestIndex(log, "handoff")
	for _, e := range log[last+1:] {
		if e.Type == "ready" {
			return PointWaiting, nil
		}
	}
	return "", fmt.Errorf("refusing: no PR was marked ready since the last handoff; %s", HandoffAllowed)
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
