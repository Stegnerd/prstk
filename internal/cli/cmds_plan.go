package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"prstk/internal/plan"
	"prstk/internal/render"
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	idRe   = regexp.MustCompile(`^[0-9A-Za-z]+$`)
)

const specSkeleton = `## Goal

## Scope

### In

### Out

## Owns

## Provides

## Consumes

## Definition of done

## Review: Director

## Review: Dalton
`

const notesSkeleton = `## Description

## Worker notes
`

func titleCase(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// repoContext is the git repo the command runs in.
type repoContext struct {
	path, owner, name, key string
}

func (a *App) repoFromCwd() (repoContext, error) {
	var rc repoContext
	top, err := a.Git.MainWorktree(a.Cwd)
	if err != nil {
		return rc, fmt.Errorf("not inside a git repo: %w", err)
	}
	url, err := a.Git.Origin(top)
	if err != nil {
		return rc, fmt.Errorf("no origin remote: %w", err)
	}
	rc.owner, rc.name, err = plan.ParseRemote(url)
	if err != nil {
		return rc, err
	}
	rc.path, rc.key = top, plan.RepoKey(rc.owner, rc.name)
	return rc, nil
}

func cmdInit(a *App, slug, base string) error {
	if !slugRe.MatchString(slug) {
		return usageError{fmt.Sprintf("slug %q must be lowercase words joined by '-'", slug)}
	}
	rc, err := a.repoFromCwd()
	if err != nil {
		return err
	}
	repoDir := filepath.Join(a.Home, rc.key)
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		return err
	}
	cfg, err := plan.LoadConfig(repoDir)
	if err != nil {
		return err
	}
	if base == "" {
		base = cfg.DefaultBase
	}
	if base == "" {
		base = "develop"
	}
	if !a.Git.RemoteBranchExists(rc.path, base) {
		return fmt.Errorf("base branch %q doesn't exist on origin", base)
	}

	// Idempotent: reuse an active initiative with the same slug.
	var dir string
	names, _ := plan.ListInitiatives(repoDir)
	for _, n := range names {
		if strings.HasSuffix(n, "-"+slug) && len(n) == len("2006-01-02-1504-")+len(slug) {
			dir = filepath.Join(repoDir, n)
		}
	}
	reused := dir != ""
	now := a.Now()
	if dir == "" {
		dir = filepath.Join(repoDir, now.Format("2006-01-02-1504")+"-"+slug)
	}
	for _, d := range []string{dir, filepath.Join(dir, "prs"), filepath.Join(dir, "worktrees")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	planPath := filepath.Join(dir, "PLAN.md")
	if _, err := os.Stat(planPath); errors.Is(err, os.ErrNotExist) {
		maxW := cfg.MaxWorkers
		if maxW <= 0 {
			maxW = 2
		}
		in := &plan.Initiative{Dir: dir, Meta: plan.PlanMeta{
			Initiative: slug, Repo: rc.owner + "/" + rc.name, BaseBranch: base,
			Created: now, MaxWorkers: maxW, RepoPath: rc.path,
		}}
		body := fmt.Sprintf("# %s\n\n## Goal\n\n## Non-goals\n\n## Split rationale\n\n%s\n%s\n", titleCase(slug), render.PlanBegin, render.PlanEnd)
		if err := in.SavePlan(func(string) string { return body }); err != nil {
			return err
		}
	}
	logPath := filepath.Join(dir, "LOG.md")
	if _, err := os.Stat(logPath); errors.Is(err, os.ErrNotExist) {
		if err := plan.WriteFileAtomic(logPath, []byte("# Log · "+slug+"\n")); err != nil {
			return err
		}
		in := &plan.Initiative{Dir: dir}
		if err := in.AppendLog(now, "init", fmt.Sprintf("initiative %s on %s/%s, base %s", slug, rc.owner, rc.name, base)); err != nil {
			return err
		}
	}
	if err := plan.SetCurrent(repoDir, filepath.Base(dir)); err != nil {
		return err
	}
	verb := "created"
	if reused {
		verb = "reused"
	}
	return a.mutateDir(dir, func(in *plan.Initiative) (string, error) {
		return fmt.Sprintf("%s %s (base %s) · current", verb, dir, base), nil
	})
}

func cmdUse(a *App, name string) error {
	a.initiativeFlag = name
	dir, err := a.resolve()
	if err != nil {
		return err
	}
	if err := plan.SetCurrent(filepath.Dir(dir), filepath.Base(dir)); err != nil {
		return err
	}
	a.out("current → %s", filepath.Base(dir))
	return nil
}

type addOpts struct {
	id, slug, title, parent, model string
	conflicts                      []string
	earlyStart                     bool
}

func cmdAdd(a *App, o addOpts) error {
	id, slug, title, parent, model := o.id, o.slug, strings.TrimSpace(o.title), o.parent, o.model
	var conflicts []string
	for _, c := range o.conflicts {
		if c = strings.TrimSpace(c); c != "" {
			conflicts = append(conflicts, c)
		}
	}
	if !idRe.MatchString(id) {
		return usageError{fmt.Sprintf("id %q must be alphanumeric (e.g. 03, 02a)", id)}
	}
	if !slugRe.MatchString(slug) {
		return usageError{fmt.Sprintf("slug %q must be lowercase words joined by '-'", slug)}
	}
	if title == "" {
		return usageError{"--title is required"}
	}
	if model != "sonnet" && model != "opus" {
		return usageError{"--model must be sonnet or opus"}
	}

	return a.mutate(func(in *plan.Initiative) (string, error) {
		if parent != "" && in.PR(parent) == nil {
			return "", fmt.Errorf("parent %q doesn't exist", parent)
		}
		for _, c := range conflicts {
			if in.PR(c) == nil {
				return "", fmt.Errorf("conflicts-with %q doesn't exist", c)
			}
			if c == id {
				return "", fmt.Errorf("a PR can't conflict with itself")
			}
		}
		now := a.Now()
		dir := filepath.Join(in.PRsDir(), id+"-"+slug)
		existing := in.PR(id)
		if existing != nil && existing.Slug != slug {
			return "", fmt.Errorf("id %s already exists as %s", id, existing.Slug)
		}
		if existing == nil {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
			s := &plan.Spec{
				ID: id, Slug: slug, Title: title, Parent: parent, ConflictsWith: conflicts,
				Status: plan.Planned, StatusSince: now, EarlyStart: o.earlyStart, Model: model,
				Branch: fmt.Sprintf("prstk/%s/%s-%s", in.Meta.Initiative, id, slug), Dir: dir,
			}
			s.InitBody(specSkeleton)
			if err := s.Save(); err != nil {
				return "", err
			}
			existing = s
		}
		notes := existing.NotesPath()
		if _, err := os.Stat(notes); errors.Is(err, os.ErrNotExist) {
			if err := plan.WriteFileAtomic(notes, []byte(notesSkeleton)); err != nil {
				return "", err
			}
		}
		// symmetric conflicts_with
		for _, c := range conflicts {
			o := in.PR(c)
			if !slices.Contains(o.ConflictsWith, id) {
				o.ConflictsWith = append(o.ConflictsWith, id)
				if err := o.Save(); err != nil {
					return "", err
				}
			}
			if !slices.Contains(existing.ConflictsWith, c) {
				existing.ConflictsWith = append(existing.ConflictsWith, c)
				if err := existing.Save(); err != nil {
					return "", err
				}
			}
		}
		on := in.Meta.BaseBranch
		if parent != "" {
			on = parent
		}
		extra := ""
		if model == "opus" {
			extra += " · opus"
		}
		if len(conflicts) > 0 {
			extra += " · conflicts " + strings.Join(conflicts, ",")
		}
		if err := in.AppendLog(now, "add", fmt.Sprintf("%s %s (%s) on %s%s", id, slug, title, on, extra)); err != nil {
			return "", err
		}
		return fmt.Sprintf("added %s %s on %s%s · %s", id, slug, on, extra, existing.SpecPath()), nil
	})
}

func cmdValidate(a *App) error {
	dir, err := a.resolve()
	if err != nil {
		return err
	}
	lock, err := plan.AcquireLock(dir+"/.lock", 30*time.Second)
	if err != nil {
		return err
	}
	defer lock.Release()
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		return err
	}
	a.attach(in)
	issues, err := a.regenerate(dir)
	if err != nil {
		return err
	}
	errs, warns := plan.CountErrors(issues)
	if a.jsonOut {
		if issues == nil {
			issues = []plan.Issue{}
		}
		if err := a.printJSON(map[string]any{"errors": errs, "warnings": warns, "issues": issues}); err != nil {
			return err
		}
	} else {
		for _, i := range issues {
			fmt.Fprintln(a.Stderr, i.String())
		}
		a.out("%s · %d PRs · %d error(s) · %d warning(s)", map[bool]string{true: "ok", false: "invalid"}[errs == 0], len(in.PRs), errs, warns)
	}
	if errs > 0 {
		return validationFailed{errs}
	}
	return nil
}

func cmdNext(a *App) error {
	in, err := a.load()
	if err != nil {
		return err
	}
	cands, free := in.Next()
	if a.jsonOut {
		if cands == nil {
			cands = []plan.Candidate{}
		}
		return a.printJSON(map[string]any{"free_slots": free, "candidates": cands})
	}
	if len(cands) == 0 {
		why := "nothing ready"
		if free == 0 {
			why = "no free slots"
		}
		a.out("none · %s (%d/%d used)", why, in.SlotsUsed(), in.MaxWorkers())
		return nil
	}
	for _, c := range cands {
		a.out("%s %s · %s · %s · critical path %d", c.ID, in.PR(c.ID).Slug, c.Kind, c.Model, c.Critical)
	}
	return nil
}

func cmdShow(a *App, id string) error {
	in, err := a.load()
	if err != nil {
		return err
	}
	p := in.PR(id)
	if p == nil {
		return fmt.Errorf("no PR %q", id)
	}
	wt := in.Worktree(p.ID)
	if _, err := os.Stat(wt); err != nil {
		wt = ""
	}
	v := map[string]any{
		"id": p.ID, "slug": p.Slug, "title": p.Title, "parent": p.Parent, "conflicts_with": p.ConflictsWith,
		"status": render.DisplayStatus(in, p), "status_since": p.StatusSince, "blocked_reason": p.BlockedReason,
		"early_start": p.EarlyStart, "model": p.Model, "worker": p.Worker, "branch": p.Branch,
		"pr_number": p.PRNumber, "base": in.BaseFor(p),
		"spec": p.SpecPath(), "notes": p.NotesPath(), "worktree": wt,
	}
	if a.jsonOut {
		return a.printJSON(v)
	}
	parent := p.Parent
	if parent == "" {
		parent = "(base " + in.Meta.BaseBranch + ")"
	}
	lines := [][2]string{
		{"id", p.ID}, {"title", p.Title}, {"status", fmt.Sprintf("%s (%s)", render.DisplayStatus(in, p), plan.Ago(a.Now(), p.StatusSince))},
		{"parent", parent}, {"model", p.Model}, {"branch", p.Branch}, {"base", in.BaseFor(p)},
	}
	if p.BlockedReason != "" {
		lines = append(lines, [2]string{"reason", p.BlockedReason})
	}
	if len(p.ConflictsWith) > 0 {
		lines = append(lines, [2]string{"conflicts", strings.Join(p.ConflictsWith, ", ")})
	}
	if p.EarlyStart {
		lines = append(lines, [2]string{"early_start", "true"})
	}
	if p.Worker != "" {
		lines = append(lines, [2]string{"worker", p.Worker})
	}
	if p.PRNumber > 0 {
		lines = append(lines, [2]string{"pr", fmt.Sprintf("#%d https://github.com/%s/pull/%d", p.PRNumber, in.Meta.Repo, p.PRNumber)})
	}
	lines = append(lines, [2]string{"spec", p.SpecPath()}, [2]string{"notes", p.NotesPath()})
	if wt != "" {
		lines = append(lines, [2]string{"worktree", wt})
	}
	for _, l := range lines {
		a.out("%-10s %s", l[0], l[1])
	}
	return nil
}

func cmdStatus(a *App, id, status, reason string) error {
	to := plan.Status(status)
	reason = strings.TrimSpace(reason)
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		from := p.Status
		target, err := plan.CheckTransition(from, to, p.BlockedFrom)
		if err != nil {
			return "", err
		}
		if target == plan.Blocked && reason == "" && p.BlockedReason == "" {
			return "", usageError{"blocked requires --reason"}
		}
		if from == target && (reason == "" || reason == p.BlockedReason) {
			return fmt.Sprintf("%s already %s", id, target), nil
		}
		now := a.Now()
		switch {
		case target == plan.Blocked:
			if from != plan.Blocked {
				p.BlockedFrom = from
			}
			if reason != "" {
				p.BlockedReason = reason
			}
		case target == plan.FixPending:
			p.BlockedReason, p.BlockedFrom = reason, ""
			p.Worker = ""
		default:
			p.BlockedReason, p.BlockedFrom = "", ""
		}
		if target == plan.Abandoned || target == plan.Planned {
			p.Worker = ""
		}
		p.SetStatus(target, now)
		if err := p.Save(); err != nil {
			return "", err
		}
		typ := map[plan.Status]string{
			plan.Blocked: "blocked", plan.ChangesRequested: "review", plan.FixPending: "feedback",
			plan.Abandoned: "replan", plan.Planned: "replan",
		}[target]
		if from == plan.Blocked {
			typ = "blocked"
		}
		if typ == "" {
			typ = "status"
		}
		msg := fmt.Sprintf("%s %s → %s", id, from, target)
		if p.BlockedReason != "" {
			msg += ": " + p.BlockedReason
		}
		if err := in.AppendLog(now, typ, msg); err != nil {
			return "", err
		}
		return msg, nil
	})
}
