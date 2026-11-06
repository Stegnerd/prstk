package cli

import (
	"strings"
	"testing"
)

// A worktree made by assign must be able to `git push` with no arguments.
func TestAssignSetsUpstream(t *testing.T) {
	e := newEnv(t)
	e.run("init", "up")
	e.run("add", "01", "first", "--title", "First")
	e.run("assign", "01", "--worker", "w1")

	in := e.initiative()
	p := in.PR("01")
	wt := in.Worktree("01")

	if got := git(t, wt, "config", "branch."+p.Branch+".remote"); got != "origin" {
		t.Fatalf("remote = %q", got)
	}
	if got := git(t, wt, "config", "branch."+p.Branch+".merge"); got != "refs/heads/"+p.Branch {
		t.Fatalf("merge = %q", got)
	}

	e.work("01", "a.txt", "hello\n")
	git(t, wt, "push") // plain push: must not need -u
	if out := git(t, e.remote, "branch", "--list", p.Branch); !strings.Contains(out, p.Branch) {
		t.Fatalf("origin missing branch %s: %q", p.Branch, out)
	}

	// idempotent: re-running ensureBranch on an existing branch is harmless
	a := e.app(e.repo)
	if err := a.ensureBranch(in, e.repo, p); err != nil {
		t.Fatal(err)
	}
	if got := git(t, wt, "config", "branch."+p.Branch+".merge"); got != "refs/heads/"+p.Branch {
		t.Fatalf("merge after re-run = %q", got)
	}
}
