package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"prstk/internal/gh"
	"prstk/internal/plan"
	"prstk/internal/render"
)

// repoPath is where repo-level git commands run (worktree add/remove).
func (a *App) repoPath(in *plan.Initiative) (string, error) {
	if in.Meta.RepoPath != "" {
		if _, err := os.Stat(in.Meta.RepoPath); err == nil {
			return in.Meta.RepoPath, nil
		}
	}
	top, err := a.Git.MainWorktree(a.Cwd)
	if err != nil {
		return "", fmt.Errorf("can't find the repo (PLAN.md repo_path %q missing, and cwd isn't a repo)", in.Meta.RepoPath)
	}
	return top, nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func cmdAssign(a *App, id, worker string, ignoreConflicts bool) error {
	worker = strings.TrimSpace(worker)
	if worker == "" {
		return usageError{"--worker is required"}
	}
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		// Idempotent re-run.
		already := (p.Status == plan.InProgress || p.Status == plan.Fixing) && p.Worker == worker
		target := plan.InProgress
		switch {
		case already:
			target = p.Status
		case p.Status == plan.FixPending:
			target = plan.Fixing
		case in.IsReady(p):
		default:
			why := string(p.Status)
			if p.Status == plan.Planned {
				if par := in.PR(p.Parent); par != nil {
					why = fmt.Sprintf("not ready: parent %s is %s", p.Parent, par.Status)
				} else {
					why = fmt.Sprintf("parent %q doesn't exist", p.Parent)
				}
			}
			return "", fmt.Errorf("%s can't be assigned (%s); needs to be ready or fix-pending", id, why)
		}
		if !already {
			if in.SlotsUsed() >= in.MaxWorkers() {
				return "", fmt.Errorf("no free slot (%d/%d used)", in.SlotsUsed(), in.MaxWorkers())
			}
			if b := in.ConflictBlocker(p); b != "" && !ignoreConflicts {
				return "", fmt.Errorf("%s conflicts_with %s, which is active (%s); wait, or pass --ignore-conflicts", id, b, in.PR(b).Status)
			}
		}
		repo, err := a.repoPath(in)
		if err != nil {
			return "", err
		}
		wt := in.Worktree(id)
		created := false
		if !exists(wt) {
			if err := a.ensureBranch(in, repo, p); err != nil {
				return "", err
			}
			if _, err := a.Git.Run(repo, "worktree", "add", wt, p.Branch); err != nil {
				return "", err
			}
			created = true
		}
		if !already && in.Config.Setup != "" {
			if err := a.runSetup(in, wt, in.Config.Setup); err != nil {
				return "", fmt.Errorf("setup %q failed in %s: %w (re-run assign to retry)", in.Config.Setup, wt, err)
			}
		}
		if already {
			return fmt.Sprintf("%s already %s by %s · %s", id, p.Status, worker, wt), nil
		}
		now := a.Now()
		p.SetStatus(target, now)
		p.Worker, p.BlockedReason, p.BlockedFrom = worker, "", ""
		if err := p.Save(); err != nil {
			return "", err
		}
		note := ""
		if created {
			note = " (new worktree)"
		}
		if err := in.AppendLog(now, "assign", fmt.Sprintf("%s → %s (%s, %s)", id, worker, target, p.Model)); err != nil {
			return "", err
		}
		return fmt.Sprintf("assigned %s → %s · %s · %s · worktree %s%s", id, worker, target, p.Model, wt, note), nil
	})
}

// ensureBranch creates p's local branch from its parent's branch, or from
// origin/<base> when it has no unmerged parent.
func (a *App) ensureBranch(in *plan.Initiative, repo string, p *plan.Spec) error {
	if a.Git.LocalBranchExists(repo, p.Branch) {
		return nil
	}
	if a.Git.RemoteBranchExists(repo, p.Branch) {
		_, err := a.Git.Run(repo, "fetch", "origin", p.Branch+":"+p.Branch)
		return err
	}
	var start string
	par := in.PR(p.Parent)
	if par != nil && par.Status != plan.Merged {
		if !a.Git.LocalBranchExists(repo, par.Branch) {
			if !a.Git.RemoteBranchExists(repo, par.Branch) {
				return fmt.Errorf("parent branch %s doesn't exist yet", par.Branch)
			}
			if _, err := a.Git.Run(repo, "fetch", "origin", par.Branch+":"+par.Branch); err != nil {
				return err
			}
		}
		start = par.Branch
	} else {
		base := in.Meta.BaseBranch
		if _, err := a.Git.Run(repo, "fetch", "origin", base); err != nil {
			return err
		}
		start = "origin/" + base
	}
	_, err := a.Git.Run(repo, "branch", "--no-track", p.Branch, start)
	return err
}

func (a *App) runSetup(in *plan.Initiative, dir, script string) error {
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	in.Verbose("setup %q in %s → err=%v\n%s", script, dir, err, out.String())
	if err != nil {
		tail := strings.TrimSpace(out.String())
		if len(tail) > 300 {
			tail = "…" + tail[len(tail)-300:]
		}
		return fmt.Errorf("%v: %s", err, tail)
	}
	return nil
}

// updateStackNav rewrites the stack-nav block in every open PR whose body
// changed. Returns the PR numbers touched.
func (a *App) updateStackNav(in *plan.Initiative) ([]int, error) {
	var touched []int
	var errs []string
	for _, q := range in.PRs {
		if q.PRNumber <= 0 || q.Status == plan.Merged || q.Status == plan.Abandoned {
			continue
		}
		info, err := a.GH.View(in.Meta.Repo, q.PRNumber)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if info.State != "OPEN" {
			continue
		}
		body := render.ReplaceBlock(info.Body, render.StackBegin, render.StackEnd, render.StackNav(in, q.ID))
		if body == info.Body {
			continue
		}
		if err := a.GH.EditBody(in.Meta.Repo, q.PRNumber, body); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		touched = append(touched, q.PRNumber)
	}
	if len(errs) > 0 {
		return touched, fmt.Errorf("stack nav update: %s", strings.Join(errs, "; "))
	}
	return touched, nil
}

func cmdOpen(a *App, id string) error {
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		switch p.Status {
		case plan.InProgress, plan.Fixing, plan.ChangesRequested, plan.DirectorReview:
		default:
			return "", fmt.Errorf("%s is %s; open needs in-progress, fixing, or changes-requested", id, p.Status)
		}
		wt := in.Worktree(id)
		if !exists(wt) {
			return "", fmt.Errorf("no worktree at %s (assign first)", wt)
		}
		if dirty, err := a.Git.Dirty(wt); err != nil {
			return "", err
		} else if dirty {
			return "", fmt.Errorf("worktree %s has uncommitted changes; the worker must commit first", wt)
		}
		if _, err := a.Git.Run(wt, "push", "-u", "origin", p.Branch); err != nil {
			return "", err
		}
		repo := in.Meta.Repo
		base := in.BaseFor(p)
		created := false
		if p.PRNumber <= 0 {
			n, err := a.GH.FindOpenPR(repo, p.Branch)
			if err != nil {
				return "", err
			}
			if n == 0 {
				desc, _ := plan.ReadDescription(p.NotesPath())
				n, err = a.GH.CreatePR(repo, ghCreate(base, p, render.PRBody(desc, render.StackNav(in, id))))
				if err != nil {
					return "", err
				}
				created = true
			}
			p.PRNumber = n
			if err := p.Save(); err != nil { // record immediately for idempotency
				return "", err
			}
		}
		now := a.Now()
		prev := p.Status
		p.SetStatus(plan.DirectorReview, now)
		p.BlockedReason, p.BlockedFrom = "", ""
		if err := p.Save(); err != nil {
			return "", err
		}
		_, navErr := a.updateStackNav(in)
		verb := "pushed"
		if created {
			verb = "opened draft"
		}
		if prev != plan.DirectorReview {
			if err := in.AppendLog(now, "open", fmt.Sprintf("%s #%d → %s (%s)", id, p.PRNumber, base, verb)); err != nil {
				return "", err
			}
		}
		msg := fmt.Sprintf("%s #%d %s → %s · director-review", verb, p.PRNumber, id, base)
		if navErr != nil {
			return msg, navErr
		}
		return msg, nil
	})
}

func cmdReady(a *App, id string) error {
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		if p.Status == plan.AwaitingYou {
			return fmt.Sprintf("%s already awaiting-you (#%d)", id, p.PRNumber), nil
		}
		if p.Status != plan.DirectorReview {
			return "", fmt.Errorf("%s is %s; ready needs director-review", id, p.Status)
		}
		info, err := a.GH.View(in.Meta.Repo, p.PRNumber)
		if err != nil {
			return "", err
		}
		if info.IsDraft {
			if err := a.GH.Ready(in.Meta.Repo, p.PRNumber); err != nil {
				return "", err
			}
		}
		now := a.Now()
		worker := p.Worker
		p.SetStatus(plan.AwaitingYou, now)
		p.Worker = ""
		if err := p.Save(); err != nil {
			return "", err
		}
		if err := in.AppendLog(now, "ready", fmt.Sprintf("%s #%d ready for review; %s released", id, p.PRNumber, orDash(worker))); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s #%d ready for review · awaiting-you · released %s", id, p.PRNumber, orDash(worker)), nil
	})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdPropagate(a *App, id string) error {
	return a.mutate(func(in *plan.Initiative) (string, error) {
		root := in.PR(id)
		if root == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		now := a.Now()
		skip := map[string]bool{}
		var results []string
		var conflicts int
		for _, c := range in.Descendants(id) {
			if skip[c.Parent] {
				skip[c.ID] = true
				continue
			}
			res := func(s string) { results = append(results, c.ID+" "+s) }
			if c.Status == plan.Merged || c.Status == plan.Abandoned {
				skip[c.ID] = true
				res(string(c.Status) + ", skipped")
				continue
			}
			wt := in.Worktree(c.ID)
			if !exists(wt) {
				res("not started")
				continue
			}
			if dirty, err := a.Git.Dirty(wt); err != nil || dirty {
				skip[c.ID] = true
				res("dirty worktree, skipped subtree")
				continue
			}
			parentBranch := in.PR(c.Parent).Branch
			before, _ := a.Git.Run(wt, "rev-parse", "HEAD")
			_, err := a.Git.Run(wt, "merge", "--no-ff", "--no-edit", parentBranch)
			if err != nil {
				files := a.Git.ConflictedFiles(wt)
				a.Git.Run(wt, "merge", "--abort")
				skip[c.ID] = true
				if len(files) == 0 {
					res("merge failed: " + err.Error())
					continue
				}
				conflicts++
				c.SetStatus(plan.FixPending, now)
				c.BlockedReason = fmt.Sprintf("merge conflict with %s: %s", c.Parent, strings.Join(files, ", "))
				c.BlockedFrom, c.Worker = "", ""
				if err := c.Save(); err != nil {
					return "", err
				}
				res("conflict → fix-pending (" + strings.Join(files, ", ") + ")")
				continue
			}
			after, _ := a.Git.Run(wt, "rev-parse", "HEAD")
			if before == after && !a.Git.Unpushed(wt, c.Branch) {
				res("up to date")
				continue
			}
			if _, err := a.Git.Run(wt, "push", "-u", "origin", c.Branch); err != nil {
				res("merged, push failed")
				continue
			}
			res("merged+pushed")
		}
		summary := "nothing to propagate"
		if len(results) > 0 {
			summary = strings.Join(results, ", ")
		}
		if err := in.AppendLog(now, "propagate", fmt.Sprintf("from %s: %s", id, summary)); err != nil {
			return "", err
		}
		return fmt.Sprintf("propagate %s: %s", id, summary), nil
	})
}

func cmdMerged(a *App, id string) error {
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		if p.PRNumber <= 0 {
			return "", fmt.Errorf("%s has no PR", id)
		}
		if par := in.PR(p.Parent); par != nil && par.Status != plan.Merged {
			return "", fmt.Errorf("parent %s isn't merged yet (%s); merge bottom-up", par.ID, par.Status)
		}
		info, err := a.GH.View(in.Meta.Repo, p.PRNumber)
		if err != nil {
			return "", err
		}
		if info.State != "MERGED" || info.MergedAt == "" {
			return "", fmt.Errorf("#%d is not merged on GitHub (state %s)", p.PRNumber, info.State)
		}
		now := a.Now()
		first := p.Status != plan.Merged
		p.SetStatus(plan.Merged, now)
		p.Worker, p.BlockedReason, p.BlockedFrom = "", "", ""
		if err := p.Save(); err != nil {
			return "", err
		}
		parts := []string{fmt.Sprintf("merged %s (#%d)", id, p.PRNumber)}
		var retargeted []string
		for _, c := range in.Children(id) {
			if c.PRNumber <= 0 || c.Status == plan.Merged || c.Status == plan.Abandoned {
				continue
			}
			ci, err := a.GH.View(in.Meta.Repo, c.PRNumber)
			if err != nil {
				return strings.Join(parts, " · "), err
			}
			if ci.State == "OPEN" && ci.BaseRef != in.Meta.BaseBranch {
				if err := a.GH.EditBase(in.Meta.Repo, c.PRNumber, in.Meta.BaseBranch); err != nil {
					return strings.Join(parts, " · "), err
				}
				retargeted = append(retargeted, fmt.Sprintf("#%d", c.PRNumber))
			}
		}
		if len(retargeted) > 0 {
			parts = append(parts, "retargeted "+strings.Join(retargeted, ", ")+" → "+in.Meta.BaseBranch)
		}
		if _, err := a.updateStackNav(in); err != nil {
			return strings.Join(parts, " · "), err
		}
		if first {
			if err := in.AppendLog(now, "merged", strings.Join(parts, " · ")); err != nil {
				return "", err
			}
		}
		wt := in.Worktree(id)
		if exists(wt) {
			if dirty, err := a.Git.Dirty(wt); err != nil {
				return strings.Join(parts, " · "), err
			} else if dirty {
				return strings.Join(parts, " · "), fmt.Errorf("recorded merged, but worktree %s is dirty; not removed (clean it and re-run)", wt)
			}
			repo, err := a.repoPath(in)
			if err != nil {
				return strings.Join(parts, " · "), err
			}
			if _, err := a.Git.Run(repo, "worktree", "remove", wt); err != nil {
				return strings.Join(parts, " · "), err
			}
			parts = append(parts, "worktree removed")
		}
		return strings.Join(parts, " · "), nil
	})
}

func ghCreate(base string, p *plan.Spec, body string) gh.CreateArgs {
	return gh.CreateArgs{Base: base, Head: p.Branch, Title: p.Title, Body: body, Draft: true}
}
