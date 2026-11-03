// Package render produces prstk's generated content: the PLAN.md block, the
// PR stack-nav block, and board data/HTML.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

const (
	PlanBegin  = "<!-- prstk:generated:begin -->"
	PlanEnd    = "<!-- prstk:generated:end -->"
	StackBegin = "<!-- prstk:stack:begin -->"
	StackEnd   = "<!-- prstk:stack:end -->"
)

// ReplaceBlock replaces the content between begin/end markers (inclusive of
// markers) with begin+content+end. If the markers are absent the block is
// appended. Everything outside the markers is untouched.
func ReplaceBlock(doc, begin, end, content string) string {
	block := begin + "\n" + strings.TrimRight(content, "\n") + "\n" + end
	i := strings.Index(doc, begin)
	if i >= 0 {
		j := strings.Index(doc[i:], end)
		if j >= 0 {
			return doc[:i] + block + doc[i+j+len(end):]
		}
	}
	sep := ""
	if doc != "" && !strings.HasSuffix(doc, "\n\n") {
		if strings.HasSuffix(doc, "\n") {
			sep = "\n"
		} else {
			sep = "\n\n"
		}
	}
	return doc + sep + block + "\n"
}

// mermaid node colors by status, shared by PLAN.md and the board.
var statusColor = map[plan.Status]string{
	plan.Planned:          "#e5e7eb",
	plan.InProgress:       "#bfdbfe",
	plan.Blocked:          "#fecaca",
	plan.DirectorReview:   "#ddd6fe",
	plan.ChangesRequested: "#fed7aa",
	plan.AwaitingYou:      "#fde68a",
	plan.FixPending:       "#fdba74",
	plan.Fixing:           "#93c5fd",
	plan.Merged:           "#bbf7d0",
	plan.Abandoned:        "#f3f4f6",
}

func mermaidID(id string) string { return "pr_" + id }

func mermaidLabel(s string) string {
	return strings.NewReplacer(`"`, "#quot;", "<", "#lt;", ">", "#gt;").Replace(s)
}

// Mermaid renders the stack tree.
func Mermaid(in *plan.Initiative) string {
	var b strings.Builder
	b.WriteString("graph TD\n")
	fmt.Fprintf(&b, "  base[\"%s\"]\n", mermaidLabel(in.Meta.BaseBranch))
	order, _ := in.TreeOrder(true)
	used := map[plan.Status]bool{}
	for _, p := range order {
		label := p.ID + " " + p.Name()
		if p.PRNumber > 0 {
			label = fmt.Sprintf("#%d · %s", p.PRNumber, label)
		}
		label += "<br/><small>" + string(p.Status) + "</small>"
		fmt.Fprintf(&b, "  %s[\"%s\"]\n", mermaidID(p.ID), strings.ReplaceAll(label, `"`, "#quot;"))
		from := "base"
		if p.Parent != "" && in.PR(p.Parent) != nil {
			from = mermaidID(p.Parent)
		}
		arrow := "-->"
		if p.Status == plan.Abandoned {
			arrow = "-.->"
		}
		fmt.Fprintf(&b, "  %s %s %s\n", from, arrow, mermaidID(p.ID))
		used[p.Status] = true
	}
	for _, p := range in.PRs {
		for _, c := range p.ConflictsWith {
			if p.ID < c && in.PR(c) != nil {
				fmt.Fprintf(&b, "  %s -. conflicts .- %s\n", mermaidID(p.ID), mermaidID(c))
			}
		}
	}
	for _, st := range plan.AllStatuses {
		if !used[st] {
			continue
		}
		cls := strings.ReplaceAll(string(st), "-", "_")
		fmt.Fprintf(&b, "  classDef %s fill:%s,stroke:#475569,color:#111827\n", cls, statusColor[st])
		var ids []string
		for _, p := range in.PRs {
			if p.Status == st {
				ids = append(ids, mermaidID(p.ID))
			}
		}
		fmt.Fprintf(&b, "  class %s %s\n", strings.Join(ids, ","), cls)
	}
	return b.String()
}

// DisplayStatus is status with derived *ready* applied.
func DisplayStatus(in *plan.Initiative, p *plan.Spec) string {
	if in.IsReady(p) {
		return "ready"
	}
	return string(p.Status)
}

// PlanBlock is the PLAN.md generated block content.
func PlanBlock(in *plan.Initiative, now time.Time) string {
	var b strings.Builder
	b.WriteString("## Stack\n\n```mermaid\n")
	b.WriteString(Mermaid(in))
	b.WriteString("```\n\n")
	b.WriteString("| PR | Title | Parent | Status | Since | Worker | GitHub | Model |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	order, depth := in.TreeOrder(true)
	for _, p := range order {
		parent := p.Parent
		if parent == "" {
			parent = in.Meta.BaseBranch
		}
		gh := ""
		if p.PRNumber > 0 {
			gh = fmt.Sprintf("[#%d](https://github.com/%s/pull/%d)", p.PRNumber, in.Meta.Repo, p.PRNumber)
		}
		st := DisplayStatus(in, p)
		if p.Status == plan.Blocked || p.Status == plan.FixPending {
			if p.BlockedReason != "" {
				st += ": " + p.BlockedReason
			}
		}
		indent := strings.Repeat("· ", depth[p.ID])
		fmt.Fprintf(&b, "| %s%s | %s | %s | %s | %s | %s | %s | %s |\n",
			indent, p.ID, esc(p.Title), parent, esc(st), plan.Ago(now, p.StatusSince), p.Worker, gh, p.Model)
	}
	cands, free := in.Next()
	fmt.Fprintf(&b, "\nSlots: %d/%d used", in.SlotsUsed(), in.MaxWorkers())
	if free > 0 && len(cands) > 0 {
		var ids []string
		for _, c := range cands {
			ids = append(ids, c.ID)
		}
		fmt.Fprintf(&b, " · next: %s", strings.Join(ids, ", "))
	}
	fmt.Fprintf(&b, " · updated %s\n", now.Format("2006-01-02 15:04"))
	return b.String()
}

func esc(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// StackNav renders the stack-nav block content for PR thisID.
func StackNav(in *plan.Initiative, thisID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Stack** (%s)\n", in.Meta.Initiative)
	order, _ := in.TreeOrder(false)
	// Merged ancestors don't indent: their children were retargeted to the
	// base branch, so they're effectively roots now (matches §8 example).
	depth := func(p *plan.Spec) int {
		d := 0
		seen := map[string]bool{}
		for cur := in.PR(p.Parent); cur != nil && !seen[cur.ID]; cur = in.PR(cur.Parent) {
			seen[cur.ID] = true
			if cur.Status != plan.Merged {
				d++
			}
		}
		return d
	}
	for _, p := range order {
		item := p.ID + " " + p.Name()
		if p.PRNumber > 0 {
			item = fmt.Sprintf("#%d %s", p.PRNumber, item)
		}
		if p.ID == thisID {
			item = "**" + item + " ← this PR**"
		}
		if p.Status == plan.Merged {
			item += " ✅ merged"
		}
		fmt.Fprintf(&b, "%s- %s\n", strings.Repeat("  ", depth(p)), item)
	}
	return b.String()
}

// PRBody builds a new PR body from the notes Description plus stack nav.
func PRBody(description, nav string) string {
	d := strings.TrimSpace(description)
	if d == "" {
		d = "_(no description yet)_"
	}
	return ReplaceBlock(d+"\n\n", StackBegin, StackEnd, nav)
}
