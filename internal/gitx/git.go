// Package gitx runs git commands.
package gitx

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Logger receives a line per command for .prstk.log.
type Logger func(format string, args ...any)

type Git struct {
	Log Logger
}

// Run runs git in dir and returns trimmed stdout.
func (g Git) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if g.Log != nil {
		g.Log("git %s (in %s) → err=%v\n%s%s", strings.Join(args, " "), dir, err, out.String(), errb.String())
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// OK runs git and reports only success.
func (g Git) OK(dir string, args ...string) bool {
	_, err := g.Run(dir, args...)
	return err == nil
}

func (g Git) Toplevel(dir string) (string, error) {
	return g.Run(dir, "rev-parse", "--show-toplevel")
}

// MainWorktree returns the primary worktree of the repo containing dir.
func (g Git) MainWorktree(dir string) (string, error) {
	common, err := g.Run(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	top, err := g.Run(common, "rev-parse", "--show-toplevel")
	if err == nil {
		return top, nil
	}
	// common dir is <repo>/.git
	return strings.TrimSuffix(strings.TrimSuffix(common, "/"), "/.git"), nil
}

func (g Git) Origin(dir string) (string, error) {
	return g.Run(dir, "remote", "get-url", "origin")
}

func (g Git) LocalBranchExists(dir, branch string) bool {
	return g.OK(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
}

func (g Git) RemoteBranchExists(dir, branch string) bool {
	return g.OK(dir, "ls-remote", "--exit-code", "--heads", "origin", branch)
}

// Dirty reports uncommitted changes (tracked or untracked) in a worktree.
func (g Git) Dirty(dir string) (bool, error) {
	out, err := g.Run(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Unpushed reports whether branch has commits not on origin/branch (or no
// upstream at all).
func (g Git) Unpushed(dir, branch string) bool {
	out, err := g.Run(dir, "rev-list", "--count", "origin/"+branch+".."+branch)
	if err != nil {
		return true
	}
	return out != "0"
}

// ConflictedFiles lists unmerged paths during a conflicted merge.
func (g Git) ConflictedFiles(dir string) []string {
	out, _ := g.Run(dir, "diff", "--name-only", "--diff-filter=U")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}
