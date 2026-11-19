package usage

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Assign is one LOG.md `assign` entry: PR handed to a worker at a time.
type Assign struct {
	At     time.Time `json:"at"`
	PR     string    `json:"pr"`
	Worker string    `json:"worker"`
}

var (
	logHeadRe    = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2} \d{2}:\d{2}) · ([^·]+?) · `)
	assignBodyRe = regexp.MustCompile(`^(\S+) → (\S+)(?:\s|$)`)
)

// ParseAssigns reads the assign entries from LOG.md text
// ("## <ts> · assign · prstk" then "NN → <worker> (…)"), with timestamps
// in loc (LOG.md is written in local time). Sorted by time.
func ParseAssigns(log string, loc *time.Location) []Assign {
	var out []Assign
	lines := strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		m := logHeadRe.FindStringSubmatch(lines[i])
		if m == nil || strings.TrimSpace(m[2]) != "assign" {
			continue
		}
		at, err := time.ParseInLocation("2006-01-02 15:04", m[1], loc)
		if err != nil {
			continue
		}
		for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "## "); j++ {
			if b := assignBodyRe.FindStringSubmatch(strings.TrimSpace(lines[j])); b != nil {
				out = append(out, Assign{At: at, PR: b[1], Worker: b[2]})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// prFor returns the PR a worker was working on at t: the PR whose assign of
// that name most recently precedes t (or, failing that, its first assign).
func prFor(assigns []Assign, worker string, t time.Time) string {
	pr := ""
	for _, a := range assigns {
		if a.Worker != worker {
			continue
		}
		if pr == "" || !a.At.After(t) {
			pr = a.PR
		}
		if a.At.After(t) {
			break
		}
	}
	return pr
}
