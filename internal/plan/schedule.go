package plan

import (
	"slices"
	"sort"
)

// IsReady is the derived *ready* status (§5).
func (in *Initiative) IsReady(p *Spec) bool {
	if p.Status != Planned {
		return false
	}
	return in.parentAllowsStart(p)
}

// parentAllowsStart: no parent, parent passed review, or early_start with
// parent ≥ in-progress.
func (in *Initiative) parentAllowsStart(p *Spec) bool {
	if p.Parent == "" {
		return true
	}
	par := in.PR(p.Parent)
	if par == nil {
		return false
	}
	if par.Status.PassedReview() {
		return true
	}
	return p.EarlyStart && par.Status.Started()
}

// Descendants returns all descendants of id in parent-first (preorder) order.
func (in *Initiative) Descendants(id string) []*Spec {
	var out []*Spec
	var walk func(string, map[string]bool)
	walk = func(pid string, seen map[string]bool) {
		for _, c := range in.Children(pid) {
			if seen[c.ID] {
				continue // cycle guard
			}
			seen[c.ID] = true
			out = append(out, c)
			walk(c.ID, seen)
		}
	}
	walk(id, map[string]bool{id: true})
	return out
}

// CriticalPath is the number of non-merged, non-abandoned descendants.
func (in *Initiative) CriticalPath(id string) int {
	n := 0
	for _, d := range in.Descendants(id) {
		if d.Status != Merged && d.Status != Abandoned {
			n++
		}
	}
	return n
}

// Roots are PRs with no parent or a missing parent, by id.
func (in *Initiative) Roots() []*Spec {
	var out []*Spec
	for _, p := range in.PRs {
		if p.Parent == "" || in.PR(p.Parent) == nil {
			out = append(out, p)
		}
	}
	return out
}

func (in *Initiative) SlotsUsed() int {
	n := 0
	for _, p := range in.PRs {
		if p.Status.HoldsSlot() {
			n++
		}
	}
	return n
}

// Candidate is one line of `prstk next`.
type Candidate struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Kind     string `json:"kind"` // "fix" or "new"
	Model    string `json:"model"`
	Critical int    `json:"critical_path"`
}

// Next implements §7. A candidate that conflicts_with a PR currently holding a
// slot (or one already picked in this round) is deferred so siblings touching
// the same files never run at the same time.
func (in *Initiative) Next() (cands []Candidate, free int) {
	free = in.MaxWorkers() - in.SlotsUsed()
	if free <= 0 {
		return nil, max(free, 0)
	}
	active := map[string]bool{}
	for _, p := range in.PRs {
		if p.Status.HoldsSlot() {
			active[p.ID] = true
		}
	}
	var fixes, ready []*Spec
	for _, p := range in.PRs {
		switch {
		case p.Status == FixPending:
			fixes = append(fixes, p)
		case in.IsReady(p):
			ready = append(ready, p)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		ci, cj := in.CriticalPath(ready[i].ID), in.CriticalPath(ready[j].ID)
		if ci != cj {
			return ci > cj
		}
		return ready[i].ID < ready[j].ID
	})
	pick := func(p *Spec, kind string) {
		if len(cands) >= free {
			return
		}
		for _, c := range p.ConflictsWith {
			if active[c] {
				return
			}
		}
		active[p.ID] = true
		cands = append(cands, Candidate{ID: p.ID, Title: p.Title, Kind: kind, Model: p.Model, Critical: in.CriticalPath(p.ID)})
	}
	for _, p := range fixes {
		pick(p, "fix")
	}
	for _, p := range ready {
		pick(p, "new")
	}
	return cands, free
}

// ConflictBlocker returns the id of an active PR that p conflicts with, if any.
func (in *Initiative) ConflictBlocker(p *Spec) string {
	for _, c := range p.ConflictsWith {
		if o := in.PR(c); o != nil && o.Status.HoldsSlot() && o.ID != p.ID {
			return o.ID
		}
	}
	return ""
}

// BaseFor is the GitHub base branch for p: its parent's branch, or the
// initiative base when it has no parent or the parent is merged.
func (in *Initiative) BaseFor(p *Spec) string {
	if p.Parent != "" {
		if par := in.PR(p.Parent); par != nil && par.Status != Merged {
			return par.Branch
		}
	}
	return in.Meta.BaseBranch
}

// TreeOrder returns PRs in preorder (roots by id, children by id), with depth.
func (in *Initiative) TreeOrder(includeAbandoned bool) (order []*Spec, depth map[string]int) {
	depth = map[string]int{}
	seen := map[string]bool{}
	var walk func(p *Spec, d int)
	walk = func(p *Spec, d int) {
		if seen[p.ID] || (!includeAbandoned && p.Status == Abandoned) {
			return
		}
		seen[p.ID] = true
		order = append(order, p)
		depth[p.ID] = d
		for _, c := range in.Children(p.ID) {
			walk(c, d+1)
		}
	}
	for _, r := range in.Roots() {
		walk(r, 0)
	}
	// anything unreachable (cycles) goes last at depth 0
	for _, p := range in.PRs {
		if !seen[p.ID] && (includeAbandoned || p.Status != Abandoned) {
			seen[p.ID] = true
			order = append(order, p)
			depth[p.ID] = 0
		}
	}
	return order, depth
}

// Paths returns every root-to-leaf path (non-abandoned), in tree order.
func (in *Initiative) Paths() [][]*Spec {
	var out [][]*Spec
	var walk func(p *Spec, path []*Spec, seen map[string]bool)
	walk = func(p *Spec, path []*Spec, seen map[string]bool) {
		if seen[p.ID] {
			return
		}
		seen[p.ID] = true
		path = append(slices.Clone(path), p)
		var kids []*Spec
		for _, c := range in.Children(p.ID) {
			if c.Status != Abandoned {
				kids = append(kids, c)
			}
		}
		if len(kids) == 0 {
			out = append(out, path)
		}
		for _, c := range kids {
			walk(c, path, seen)
		}
		delete(seen, p.ID)
	}
	for _, r := range in.Roots() {
		if r.Status != Abandoned {
			walk(r, nil, map[string]bool{})
		}
	}
	return out
}
