package cli

import (
	"fmt"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
)

// cmdSpawnPrompt prints the prompt the director uses to spawn a PR's worker:
// teammate name, agent type, model, absolute paths, and, for a fix worker,
// why it exists.
func cmdSpawnPrompt(a *App, id, worker string) error {
	in, err := a.load()
	if err != nil {
		return err
	}
	p := in.PR(id)
	if p == nil {
		return fmt.Errorf("no PR %q", id)
	}
	if worker = strings.TrimSpace(worker); worker == "" {
		worker = p.Worker
	}
	if worker == "" {
		return fmt.Errorf("%s has no worker; run prstk assign %s --worker <name> first, or pass --worker", id, id)
	}
	model := "Sonnet"
	if p.Model == "opus" {
		model = "Opus"
	}
	wt := in.Worktree(id)
	var b strings.Builder
	fmt.Fprintf(&b, "Spawn a teammate named `%s` using the prstk-worker agent type. Use %s.\n\n", worker, model)
	fmt.Fprintf(&b, "You own PR %s (%s) of initiative %s.\n", p.ID, p.Title, in.Meta.Initiative)
	fmt.Fprintf(&b, "Worktree: %s\n", wt)
	fmt.Fprintf(&b, "Branch: %s (PR base: %s)\n", p.Branch, in.BaseFor(p))
	fmt.Fprintf(&b, "Spec: %s\n", p.SpecPath())
	fmt.Fprintf(&b, "Notes: %s\n", p.NotesPath())
	if par := in.PR(p.Parent); par != nil {
		fmt.Fprintf(&b, "Parent notes: %s (PR %s, %s)\n", par.NotesPath(), par.ID, par.Status)
	}
	if p.PRNumber > 0 {
		fmt.Fprintf(&b, "GitHub PR: #%d\n", p.PRNumber)
	}
	b.WriteString("\n")

	reviewer := in.Config.ReviewerName()
	switch {
	case (p.Status == plan.Fixing || p.Status == plan.FixPending) && strings.HasPrefix(p.BlockedReason, "merge conflict"):
		par := in.PR(p.Parent)
		branch := in.BaseFor(p)
		if par != nil {
			branch = par.Branch
		}
		fmt.Fprintf(&b, "You are a fix worker for a merge conflict (%s). In the worktree, run `git merge --no-ff %s`, resolve the conflicts keeping both sides' intent, then vet, test, commit, and update notes.md.\n", p.BlockedReason, branch)
	case p.Status == plan.Fixing || p.Status == plan.FixPending:
		fmt.Fprintf(&b, "You are a fix worker for %s's review feedback. It is recorded verbatim under `## Review: %s` in spec.md; address the newest entry (and anything still open under `## Review: Director`). Record what you changed in notes.md.\n", reviewer, reviewer)
	case p.Status == plan.ChangesRequested:
		b.WriteString("The director requested changes: address the newest `### Round` under `## Review: Director` in spec.md, and record what you changed in notes.md.\n")
	}
	if para := evidenceSpawnParagraph(p); para != "" {
		b.WriteString(para)
	}
	if line := prstkBinSpawnLine(); line != "" {
		b.WriteString(line)
	}
	b.WriteString("Read the spec first.\n")
	fmt.Fprint(a.Stdout, b.String())
	return nil
}
