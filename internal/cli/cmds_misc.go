package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/server"
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
	gitPath, err := lookPath("git")
	check("git", err, gitPath)
	ghPath, ghErr := lookPath("gh")
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
	if lerr == nil {
		a.doctorEvidence(in)
	}
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
	locked := true
	defer func() {
		if locked {
			lock.Release()
		}
	}()
	in, err := plan.LoadInitiative(dir)
	if err != nil {
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
		return fmt.Errorf("refusing: not merged/abandoned: %s", strings.Join(open, ", "))
	}
	wtDir := filepath.Join(dir, "worktrees")
	wts, _ := os.ReadDir(wtDir)
	for _, w := range wts {
		wt := filepath.Join(wtDir, w.Name())
		if dirty, err := a.Git.Dirty(wt); err == nil && dirty {
			return fmt.Errorf("refusing: worktree %s has uncommitted changes", wt)
		}
	}
	archive := plan.ArchivePath(in.RepoDir(), in.Name)
	if _, err := os.Lstat(archive); err == nil {
		return fmt.Errorf("refusing: %s already exists", archive)
	} else if !os.IsNotExist(err) {
		return err
	}
	now := a.Now()
	if err := in.AppendLog(now, "teardown", fmt.Sprintf("archived to _archive/%s/", in.Name)); err != nil {
		return err
	}
	if _, err := a.regenerate(dir); err != nil { // final generated block
		return err
	}
	// Remove worktrees first; until the rename below, the initiative stays
	// in place and a re-run picks up where this one stopped.
	repo, rerr := a.repoPath(in)
	for _, w := range wts {
		wt := filepath.Join(wtDir, w.Name())
		if rerr == nil {
			if _, err := a.Git.Run(repo, "worktree", "remove", wt); err != nil {
				return fmt.Errorf("removing worktree failed: %w (initiative left at %s; re-run teardown)", err, dir)
			}
		}
	}
	if rerr == nil {
		a.Git.Run(repo, "worktree", "prune")
	}
	// Drop the runtime-only files so the archive holds just the plan data.
	// Detach first so verbose logging doesn't recreate .prstk.log.
	a.in = nil
	a.Git.Log = nil
	lock.Release()
	locked = false
	for _, p := range []string{in.LockPath(), in.VerbosePath(), wtDir} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing %s failed: %w (initiative left at %s; re-run teardown)", p, err, dir)
		}
	}
	if err := os.MkdirAll(plan.ArchiveRoot(in.RepoDir()), 0o755); err != nil {
		return fmt.Errorf("%w (initiative left at %s; re-run teardown)", err, dir)
	}
	if err := os.Rename(dir, archive); err != nil {
		return fmt.Errorf("archiving failed: %w (initiative left at %s; re-run teardown)", err, dir)
	}
	if plan.ReadCurrent(in.RepoDir()) == in.Name {
		if err := plan.SetCurrent(in.RepoDir(), ""); err != nil {
			return err
		}
	}
	a.out("archived %s → %s/ · removed %d worktree(s) · current cleared", in.Name, archive, len(wts))
	return nil
}
