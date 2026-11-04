package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// goChecks are the default checks for a Go repo when config.yaml sets none.
var goChecks = []string{
	"go vet ./...",
	`out="$(gofmt -l .)"; [ -z "$out" ] || { echo "gofmt needed:"; echo "$out"; exit 1; }`,
	"go test ./...",
}

// cmdCheck verifies a PR the way the director would: in a throwaway detached
// worktree of the PR branch, merge its latest base, run setup, then run each
// check command. Nothing is pushed and no plan state changes.
func cmdCheck(a *App, id string, cmds []string) error {
	in, err := a.load()
	if err != nil {
		return err
	}
	p := in.PR(id)
	if p == nil {
		return fmt.Errorf("no PR %q", id)
	}
	repo, err := a.repoPath(in)
	if err != nil {
		return err
	}
	if !a.Git.LocalBranchExists(repo, p.Branch) {
		return fmt.Errorf("branch %s doesn't exist locally (assign %s first)", p.Branch, id)
	}
	if wt := in.Worktree(id); exists(wt) {
		if dirty, err := a.Git.Dirty(wt); err == nil && dirty {
			a.out("⚠ %s has uncommitted changes; only committed work is checked", wt)
		}
	}

	base := in.BaseFor(p)
	baseRef := base
	if base == in.Meta.BaseBranch {
		if _, err := a.Git.Run(repo, "fetch", "origin", base); err != nil {
			return err
		}
		baseRef = "origin/" + base
	}

	tmp, err := os.MkdirTemp("", "prstk-check-"+id+"-")
	if err != nil {
		return err
	}
	wt := filepath.Join(tmp, "wt")
	defer func() {
		a.Git.Run(repo, "worktree", "remove", "--force", wt)
		os.RemoveAll(tmp)
		a.Git.Run(repo, "worktree", "prune")
	}()
	if _, err := a.Git.Run(repo, "worktree", "add", "--detach", wt, p.Branch); err != nil {
		return err
	}

	if _, err := a.Git.Run(wt, "merge", "--no-ff", "--no-edit", baseRef); err != nil {
		files := a.Git.ConflictedFiles(wt)
		a.Git.Run(wt, "merge", "--abort")
		if len(files) > 0 {
			a.out("✗ merge %s: conflicts in %s", baseRef, strings.Join(files, ", "))
			return fmt.Errorf("%s doesn't merge cleanly with %s", p.Branch, baseRef)
		}
		return fmt.Errorf("merge %s: %w", baseRef, err)
	}
	a.out("✓ merge %s", baseRef)

	if in.Config.Setup != "" {
		if err := a.runSetup(in, wt, in.Config.Setup); err != nil {
			a.out("✗ setup %s", in.Config.Setup)
			return fmt.Errorf("setup failed: %w", err)
		}
		a.out("✓ setup %s", in.Config.Setup)
	}

	if len(cmds) == 0 {
		cmds = in.Config.Check
	}
	if len(cmds) == 0 {
		if !exists(filepath.Join(wt, "go.mod")) {
			return fmt.Errorf("no checks to run: set check: in config.yaml or pass --cmd")
		}
		cmds = goChecks
	}
	failed := 0
	for _, c := range cmds {
		start := time.Now()
		out, err := runShell(wt, c)
		in.Verbose("check %s %q → err=%v\n%s", id, c, err, out)
		took := time.Since(start).Round(100 * time.Millisecond)
		if err == nil {
			a.out("✓ %s (%s)", c, took)
			continue
		}
		failed++
		a.out("✗ %s (%s)", c, took)
		for _, l := range lastLines(out, 40) {
			a.out("    %s", l)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d check(s) failed for %s", failed, len(cmds), id)
	}
	a.out("%s ok · %d check(s) passed on %s merged with %s", id, len(cmds), p.Branch, baseRef)
	return nil
}

func runShell(dir, script string) (string, error) {
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{"…"}, lines[len(lines)-n:]...)
	}
	return lines
}
