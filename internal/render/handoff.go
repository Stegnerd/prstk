package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

// HandoffRecordLine is the last line of every handoff summary.
const HandoffRecordLine = "The plan files are the record; this file is only a summary."

const (
	handoffNotesHeading = "## Director notes"
	handoffReadHeading  = "## Read first"
)

// HandoffOpts configures Handoff.
type HandoffOpts struct {
	// Point is the allowed point a HANDOFF.md is written at. Empty builds the
	// live summary that --print and --hook show.
	Point plan.HandoffPointKind
	Now   time.Time
	// Notes are the director's notes, copied verbatim under "## Director
	// notes". The section is left out when Notes is blank.
	Notes string
	// Saved is the line about the saved HANDOFF.md ("no handoff written", or
	// "notes written <time>; N LOG entries since"). Live summaries only.
	Saved string
}

// Handoff builds the handoff summary markdown: the header, the PR table,
// what waits on the human, what comes next, the director's notes and the
// files to read first. It is built from the initiative's current state, so
// the same builder serves HANDOFF.md and the live views.
func Handoff(in *plan.Initiative, o HandoffOpts) string {
	var b strings.Builder
	now := o.Now
	fmt.Fprintf(&b, "# Handoff: %s\n\n", in.Name)
	fmt.Fprintf(&b, "- Initiative: %s (%s) · base `%s` · max_workers %d\n", in.Meta.Initiative, in.Meta.Repo, in.Meta.BaseBranch, in.MaxWorkers())
	if o.Point != "" {
		fmt.Fprintf(&b, "- Point: %s · written %s\n", o.Point, now.Format("2006-01-02 15:04"))
	} else {
		fmt.Fprintf(&b, "- Live state as of %s\n", now.Format("2006-01-02 15:04"))
	}
	if o.Saved != "" {
		fmt.Fprintf(&b, "- Handoff: %s\n", o.Saved)
	}

	// ---- PRs ----
	b.WriteString("\n## PRs\n\n")
	order, _ := in.TreeOrder(false)
	var merged, abandoned []string
	var rows []*plan.Spec
	for _, p := range order {
		if p.Status == plan.Merged {
			merged = append(merged, prLabel(p))
		} else {
			rows = append(rows, p)
		}
	}
	for _, p := range in.PRs {
		if p.Status == plan.Abandoned {
			abandoned = append(abandoned, prLabel(p))
		}
	}
	if len(rows) > 0 {
		b.WriteString("| PR | Title | Parent | Status | GitHub | Verdict | Worker |\n|---|---|---|---|---|---|---|\n")
		for _, p := range rows {
			parent := p.Parent
			if parent == "" {
				parent = in.Meta.BaseBranch
			}
			st := DisplayStatus(in, p)
			if p.Status == plan.Blocked && p.BlockedReason != "" {
				st += ": " + p.BlockedReason
			}
			st += " (" + plan.Ago(now, p.StatusSince) + ")"
			gh := ""
			if p.PRNumber > 0 {
				gh = fmt.Sprintf("#%d", p.PRNumber)
			}
			verdict := ""
			if n, v := p.LatestDirectorVerdict(); n > 0 {
				verdict = fmt.Sprintf("r%d %s", n, v)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", p.ID, esc(p.Title), parent, esc(st), gh, esc(verdict), p.Worker)
		}
	}
	if len(merged) > 0 {
		fmt.Fprintf(&b, "\nmerged: %s\n", strings.Join(merged, ", "))
	}
	if len(abandoned) > 0 {
		fmt.Fprintf(&b, "\nabandoned: %s\n", strings.Join(abandoned, ", "))
	}

	// ---- Waiting on the human ----
	b.WriteString("\n## Waiting on the human\n\n")
	waiting := 0
	for _, p := range order {
		if p.Status != plan.AwaitingYou {
			continue
		}
		waiting++
		item := prLabel(p) + " " + p.Title
		if after := MergeAfter(in, p); after != "" {
			item += " · ⏳ merge after " + after
		}
		fmt.Fprintf(&b, "- %s\n", item)
	}
	if waiting == 0 {
		b.WriteString("nothing\n")
	}

	// ---- Next ----
	b.WriteString("\n## Next\n\n")
	b.WriteString("`prstk next` prints:\n\n")
	for _, l := range nextLines(in) {
		fmt.Fprintf(&b, "- %s\n", l)
	}
	var fixes []string
	for _, p := range order {
		if p.Status != plan.FixPending {
			continue
		}
		why := "feedback: see ## Review: " + in.Config.ReviewerName()
		if strings.HasPrefix(p.BlockedReason, "merge conflict") {
			why = p.BlockedReason
		} else if p.BlockedReason != "" {
			why += " (" + p.BlockedReason + ")"
		}
		fixes = append(fixes, fmt.Sprintf("- fix-pending %s %s: %s\n", p.ID, p.Title, why))
	}
	if len(fixes) > 0 {
		b.WriteString("\n")
		for _, f := range fixes {
			b.WriteString(f)
		}
	}

	// ---- Director notes ----
	if n := trimNotes(o.Notes); n != "" {
		fmt.Fprintf(&b, "\n%s\n\n%s\n", handoffNotesHeading, n)
	}

	// ---- Read first ----
	fmt.Fprintf(&b, "\n%s\n\n", handoffReadHeading)
	fmt.Fprintf(&b, "- %s\n- %s\n", in.PlanPath(), in.LogPath())
	for _, p := range in.PRs {
		if p.Status != plan.Merged && p.Status != plan.Abandoned {
			fmt.Fprintf(&b, "- %s\n", p.SpecPath())
		}
	}
	fmt.Fprintf(&b, "\n%s\n", HandoffRecordLine)
	return b.String()
}

// HandoffNotes extracts the "## Director notes" body from a saved
// HANDOFF.md, or "" when it has none. The notes are verbatim text that may
// itself hold "## " headings, so the section runs to the "## Read first"
// heading prstk writes after it.
func HandoffNotes(saved string) string {
	saved = strings.ReplaceAll(saved, "\r\n", "\n")
	start := strings.Index(saved, "\n"+handoffNotesHeading+"\n")
	if start < 0 {
		return ""
	}
	rest := saved[start+len(handoffNotesHeading)+2:]
	end := strings.LastIndex(rest, "\n"+handoffReadHeading+"\n")
	if end < 0 {
		return ""
	}
	return trimNotes(rest[:end])
}

// trimNotes drops leading blank lines and trailing whitespace, leaving the
// text between them as written (indentation included).
func trimNotes(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return strings.TrimRight(strings.TrimLeft(s, "\n"), " \t\n")
}

// prLabel is "01 #23" for a PR with a GitHub number, else just the id.
func prLabel(p *plan.Spec) string {
	if p.PRNumber > 0 {
		return fmt.Sprintf("%s #%d", p.ID, p.PRNumber)
	}
	return p.ID
}

// nextLines is what `prstk next` prints now, one entry per line.
func nextLines(in *plan.Initiative) []string {
	cands, free := in.Next()
	if len(cands) == 0 {
		why := "nothing ready"
		if free == 0 {
			why = "no free slots"
		}
		return []string{fmt.Sprintf("none · %s (%d/%d used)", why, in.SlotsUsed(), in.MaxWorkers())}
	}
	var out []string
	for _, c := range cands {
		out = append(out, fmt.Sprintf("%s %s · %s · %s · critical path %d", c.ID, in.PR(c.ID).Slug, c.Kind, c.Model, c.Critical))
	}
	return out
}
