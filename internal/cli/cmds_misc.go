package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"prstk/internal/plan"
	"prstk/internal/server"
)

func cmdBoard(a *App, serve bool, port int) error {
	if !serve {
		return a.mutate(func(in *plan.Initiative) (string, error) {
			return "wrote " + in.BoardPath(), nil
		})
	}
	if port <= 0 || port > 65535 {
		return usageError{"--port must be 1–65535"}
	}
	ln, err := server.Listen(port)
	if err != nil {
		return err
	}
	srv := server.New(a.Home)
	srv.Now = a.Now
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a.out("board live at http://localhost:%d/ (watching %s; Ctrl-C to stop)", port, a.Home)
	return srv.Serve(ctx, ln)
}

func cmdDoctor(a *App) error {
	failed := 0
	check := func(name string, err error, okDetail string) {
		if err != nil {
			failed++
			a.out("✗ %-14s %v", name, err)
		} else {
			a.out("✓ %-14s %s", name, okDetail)
		}
	}
	gitPath, err := exec.LookPath("git")
	check("git", err, gitPath)
	ghPath, ghErr := exec.LookPath("gh")
	check("gh", ghErr, ghPath)
	if ghErr == nil {
		check("gh auth", a.GH.AuthStatus(), "logged in")
	} else {
		check("gh auth", fmt.Errorf("skipped (gh not installed)"), "")
	}

	in, lerr := a.load()
	switch {
	case lerr != nil:
		// Not fatal: doctor is useful before init. Check the repo's default base instead.
		if rc, err := a.repoFromCwd(); err == nil {
			cfg, _ := plan.LoadConfig(filepath.Join(a.Home, rc.key))
			base := cfg.DefaultBase
			if base == "" {
				base = "develop"
			}
			var e error
			if !a.Git.RemoteBranchExists(rc.path, base) {
				e = fmt.Errorf("%q not found on origin", base)
			}
			check("base branch", e, base+" exists on origin (default; no initiative yet)")
		} else {
			check("base branch", fmt.Errorf("skipped: %v", err), "")
		}
	default:
		repo, err := a.repoPath(in)
		if err == nil && !a.Git.RemoteBranchExists(repo, in.Meta.BaseBranch) {
			err = fmt.Errorf("%q not found on origin", in.Meta.BaseBranch)
		}
		check("base branch", err, in.Meta.BaseBranch+" exists on origin ("+in.Name+")")
	}
	check("agent teams", a.agentTeamsEnabled(), "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1")
	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	return nil
}

func (a *App) agentTeamsEnabled() error {
	const key = "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS"
	if v := a.Getenv(key); v != "" && v != "0" && v != "false" {
		return nil
	}
	p := filepath.Join(a.UserHome, ".claude", "settings.json")
	b, err := os.ReadFile(p)
	if err == nil {
		var s struct {
			Env map[string]any `json:"env"`
		}
		if json.Unmarshal(b, &s) == nil {
			if v, ok := s.Env[key]; ok {
				if str := fmt.Sprint(v); str != "" && str != "0" && str != "false" {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("%s not set in env or %s", key, p)
}

func cmdTeardown(a *App) error {
	dir, err := a.resolve()
	if err != nil {
		return err
	}
	lock, err := plan.AcquireLock(filepath.Join(dir, ".lock"), 30*time.Second)
	if err != nil {
		return err
	}
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		lock.Release()
		return err
	}
	a.attach(in)
	var open []string
	for _, p := range in.PRs {
		if p.Status != plan.Merged && p.Status != plan.Abandoned {
			open = append(open, p.ID+" ("+string(p.Status)+")")
		}
	}
	if len(open) > 0 {
		lock.Release()
		return fmt.Errorf("refusing: not merged/abandoned: %s", strings.Join(open, ", "))
	}
	wts, _ := os.ReadDir(filepath.Join(dir, "worktrees"))
	for _, w := range wts {
		wt := filepath.Join(dir, "worktrees", w.Name())
		if dirty, err := a.Git.Dirty(wt); err == nil && dirty {
			lock.Release()
			return fmt.Errorf("refusing: worktree %s has uncommitted changes", wt)
		}
	}
	now := a.Now()
	if err := in.AppendLog(now, "teardown", fmt.Sprintf("archived to _archive/%s.md", in.Name)); err != nil {
		lock.Release()
		return err
	}
	if _, err := a.regenerate(dir); err != nil { // final generated block
		lock.Release()
		return err
	}
	planB, err := os.ReadFile(in.PlanPath())
	if err != nil {
		lock.Release()
		return err
	}
	logB, err := os.ReadFile(in.LogPath())
	if err != nil {
		lock.Release()
		return err
	}
	archDir := filepath.Join(in.RepoDir(), "_archive")
	if err := os.MkdirAll(archDir, 0o755); err != nil {
		lock.Release()
		return err
	}
	archive := filepath.Join(archDir, in.Name+".md")
	content := string(planB) + "\n\n---\n\n" + string(logB)
	if err := plan.WriteFileAtomic(archive, []byte(content)); err != nil {
		lock.Release()
		return err
	}
	// Only after the archive is written: remove worktrees and the initiative.
	repo, rerr := a.repoPath(in)
	for _, w := range wts {
		wt := filepath.Join(dir, "worktrees", w.Name())
		if rerr == nil {
			if _, err := a.Git.Run(repo, "worktree", "remove", wt); err != nil {
				lock.Release()
				return fmt.Errorf("archived to %s, but removing worktree failed: %w (re-run teardown)", archive, err)
			}
		}
	}
	if rerr == nil {
		a.Git.Run(repo, "worktree", "prune")
	}
	lock.Release()
	a.in = nil // its .prstk.log is about to vanish
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("archived to %s, but deleting %s failed: %w", archive, dir, err)
	}
	if plan.ReadCurrent(in.RepoDir()) == in.Name {
		if err := plan.SetCurrent(in.RepoDir(), ""); err != nil {
			return err
		}
	}
	a.out("archived %s → %s · removed %d worktree(s) · current cleared", in.Name, archive, len(wts))
	return nil
}
