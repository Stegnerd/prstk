package plan

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

type Issue struct {
	Error bool   `json:"error"`
	PR    string `json:"pr,omitempty"`
	Msg   string `json:"msg"`
}

func (i Issue) String() string {
	lvl := "warning"
	if i.Error {
		lvl = "error"
	}
	if i.PR != "" {
		return fmt.Sprintf("%s: %s: %s", lvl, i.PR, i.Msg)
	}
	return fmt.Sprintf("%s: %s", lvl, i.Msg)
}

func CountErrors(issues []Issue) (errs, warns int) {
	for _, i := range issues {
		if i.Error {
			errs++
		} else {
			warns++
		}
	}
	return
}

// Validate runs the §9 checks.
func (in *Initiative) Validate(now time.Time) []Issue {
	var out []Issue
	e := func(pr, f string, a ...any) { out = append(out, Issue{true, pr, fmt.Sprintf(f, a...)}) }
	w := func(pr, f string, a ...any) { out = append(out, Issue{false, pr, fmt.Sprintf(f, a...)}) }

	for _, le := range in.LoadErrors {
		e("", "%s", le)
	}
	for _, p := range in.PRs {
		if !p.Status.Known() {
			e(p.ID, "unknown status %q", p.Status)
		}
		if p.Status == Blocked && strings.TrimSpace(p.BlockedReason) == "" {
			e(p.ID, "blocked without blocked_reason")
		}
		if p.Model != "sonnet" && p.Model != "opus" {
			e(p.ID, "model must be sonnet or opus, got %q", p.Model)
		}
		var par *Spec
		if p.Parent != "" {
			par = in.PR(p.Parent)
			if par == nil {
				e(p.ID, "parent %q does not exist", p.Parent)
			}
		}
		for _, c := range p.ConflictsWith {
			o := in.PR(c)
			switch {
			case o == nil:
				e(p.ID, "conflicts_with %q does not exist", c)
			case c == p.ID:
				e(p.ID, "conflicts_with itself")
			case !slices.Contains(o.ConflictsWith, p.ID):
				e(p.ID, "conflicts_with %s is not symmetric (%s doesn't list %s)", c, c, p.ID)
			}
		}
		if par != nil && p.Status != Abandoned {
			if par.Status == Abandoned {
				e(p.ID, "parent %s is abandoned (needs re-plan)", par.ID)
			} else if p.Status.Started() && !in.parentAllowsStart(p) {
				e(p.ID, "is %s but parent %s (%s) hasn't passed director review", p.Status, par.ID, par.Status)
			}
			if p.Status == Merged && par.Status != Merged {
				e(p.ID, "merged but parent %s is %s", par.ID, par.Status)
			}
		}
		if p.Status.NeedsPR() && p.PRNumber <= 0 {
			e(p.ID, "status %s with pr_number unset", p.Status)
		}
		if p.Status.HoldsSlot() && !p.StatusSince.IsZero() && now.Sub(p.StatusSince) > 2*time.Hour {
			w(p.ID, "%s for %s", p.Status, Ago(now, p.StatusSince))
		}
		if p.Status == DirectorReview {
			if d, err := ReadDescription(p.NotesPath()); err == nil && strings.TrimSpace(d) == "" {
				w(p.ID, "notes.md Description is empty")
			}
		}
	}
	// cycles
	for _, p := range in.PRs {
		seen := map[string]bool{}
		for cur := p; cur != nil && cur.Parent != ""; cur = in.PR(cur.Parent) {
			if seen[cur.ID] {
				e(p.ID, "cycle in parent chain")
				break
			}
			seen[cur.ID] = true
		}
	}
	if n := in.SlotsUsed(); n > in.MaxWorkers() {
		e("", "%d PRs hold worker slots, max_workers is %d", n, in.MaxWorkers())
	}
	return out
}

// ReadDescription returns the body of notes.md's "## Description" section.
func ReadDescription(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return Section(string(b), "Description"), nil
}

// Section extracts the text under "## <name>" up to the next "## " heading.
func Section(md, name string) string {
	lines := strings.Split(md, "\n")
	var out []string
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			if in {
				break
			}
			in = strings.TrimSpace(l[3:]) == name
			continue
		}
		if in {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Ago formats a duration compactly: 40m, 3h12m, 2d4h.
func Ago(now, t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}
