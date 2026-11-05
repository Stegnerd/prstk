package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/spf13/cobra"
)

// lookPath finds an executable on PATH; tests replace it.
var lookPath = exec.LookPath

// evidenceSkeleton is the placeholder `## Evidence` section for a spec added
// with --evidence. It's an HTML comment so plan.Validate still treats the
// section as empty until the director fills it in.
const evidenceSkeleton = `## Evidence

<!-- List the screens and flows to capture, one numbered step each, and say which ones need video. -->

`

func validEvidenceLevel(s string) bool {
	return s == plan.EvidenceNone || s == plan.EvidenceScreenshots || s == plan.EvidenceVideo
}

// skipReason returns the --skip-evidence reason when the flag was given, or
// nil. An empty reason is a usage error.
func skipReason(c *cobra.Command, val string) (*string, error) {
	if !c.Flags().Changed("skip-evidence") {
		return nil, nil
	}
	if strings.TrimSpace(val) == "" {
		return nil, usageError{"--skip-evidence needs a reason"}
	}
	val = strings.TrimSpace(val)
	return &val, nil
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// evidenceStart is the ref a PR's branch started from.
func evidenceStart(in *plan.Initiative, p *plan.Spec) string {
	base := in.BaseFor(p)
	if base == in.Meta.BaseBranch {
		return "origin/" + base
	}
	return base
}

func evidenceCounts(m plan.EvidenceManifest) (before, after int) {
	for _, it := range m.Items {
		switch it.Phase {
		case plan.PhaseBefore:
			before++
		case plan.PhaseAfter:
			after++
		}
	}
	return
}

// worktreeHead is the HEAD sha of a PR's worktree.
func (a *App) worktreeHead(in *plan.Initiative, id string) (string, error) {
	wt := in.Worktree(id)
	if !exists(wt) {
		return "", fmt.Errorf("no worktree at %s (assign first)", wt)
	}
	return a.Git.Run(wt, "rev-parse", "HEAD")
}

// checkPushed requires the worktree HEAD to be what's on origin/<branch> (the
// last remote-tracking state; no fetch), i.e. the code `open` pushed and the
// director reviewed.
func (a *App) checkPushed(in *plan.Initiative, p *plan.Spec, head string) error {
	remote, err := a.Git.Run(in.Worktree(p.ID), "rev-parse", "origin/"+p.Branch)
	if err != nil || remote != head {
		got := "origin/" + p.Branch + " doesn't exist"
		if err == nil {
			got = fmt.Sprintf("origin/%s is @%s", p.Branch, shortSHA(remote))
		}
		return fmt.Errorf("worktree HEAD @%s is not what was pushed (%s); run prstk open %s to push it and get it re-reviewed first", shortSHA(head), got, p.ID)
	}
	return nil
}

// ---- gates ----

// evidenceGate runs the open/ready gate for p. at is "open" or "ready". With
// no evidence level it does nothing. When skip is non-nil, a failing gate is
// bypassed and the bypass is logged.
func (a *App) evidenceGate(in *plan.Initiative, p *plan.Spec, at string, skip *string) error {
	if p.Evidence == plan.EvidenceNone {
		return nil
	}
	var gerr error
	if at == "open" {
		gerr = a.openGate(in, p)
	} else {
		gerr = a.readyGate(in, p)
	}
	if gerr == nil {
		return nil
	}
	if skip == nil {
		return fmt.Errorf("%w\nor bypass with --skip-evidence \"<reason>\"", gerr)
	}
	return in.AppendLog(a.Now(), "evidence", fmt.Sprintf("%s gate skipped at %s: %s", p.ID, at, *skip))
}

func (a *App) openGate(in *plan.Initiative, p *plan.Spec) error {
	m, err := plan.LoadEvidence(p)
	if err != nil {
		return err
	}
	var missing []string
	if !m.HasKind(plan.PhaseBefore, plan.KindImage) && !m.HasKind(plan.PhaseBefore, plan.KindVideo) {
		missing = append(missing, "no before evidence")
	} else if p.Evidence == plan.EvidenceVideo && !m.HasKind(plan.PhaseBefore, plan.KindVideo) {
		missing = append(missing, "no before video")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s needs audit evidence (%s) before it can open: %s.\nBefore evidence must be captured before any code changes: prstk evidence add %s --phase before --file <path>",
		p.ID, p.Evidence, strings.Join(missing, ", "), p.ID)
}

func (a *App) readyGate(in *plan.Initiative, p *plan.Spec) error {
	head, err := a.worktreeHead(in, p.ID)
	if err != nil {
		return fmt.Errorf("can't verify audit evidence: %w", err)
	}
	if err := a.checkPushed(in, p, head); err != nil {
		return err
	}
	m, err := plan.LoadEvidence(p)
	if err != nil {
		return err
	}
	var problems []string
	for _, pr := range m.Pairs() {
		if pr.Before == nil {
			continue
		}
		switch {
		case pr.After == nil:
			problems = append(problems, fmt.Sprintf("step %d: no after evidence", pr.Step))
		case pr.After.Commit != head:
			problems = append(problems, fmt.Sprintf("step %d: after evidence is stale (@%s, HEAD is @%s)", pr.Step, shortSHA(pr.After.Commit), shortSHA(head)))
		}
	}
	afterAtHead, videoAtHead := 0, false
	for _, it := range m.Items {
		if it.Phase == plan.PhaseAfter && it.Commit == head {
			afterAtHead++
			videoAtHead = videoAtHead || it.Kind == plan.KindVideo
		}
	}
	if afterAtHead == 0 && len(problems) == 0 {
		problems = append(problems, "no after evidence at HEAD")
	}
	if p.Evidence == plan.EvidenceVideo {
		if !videoAtHead {
			problems = append(problems, "no after video at HEAD")
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s audit evidence (%s) is incomplete for the current code:\n  %s\nRe-run the capture script on the approved code and record each step: prstk evidence add %s --phase after --file <path> [--step N]",
		p.ID, p.Evidence, strings.Join(problems, "\n  "), p.ID)
}

// evidenceDescriptionLine is the line open appends to a new PR's description.
func evidenceDescriptionLine(p *plan.Spec) string {
	if p.Evidence == plan.EvidenceNone {
		return ""
	}
	return fmt.Sprintf("📷 This PR has audit evidence (%s); see its card on the prstk board.", p.Evidence)
}

// ---- evidence add ----

type evidenceAddOpts struct {
	id, phase, file, caption string
	step                     int
}

func cmdEvidenceAdd(a *App, o evidenceAddOpts) error {
	phase := plan.Phase(o.phase)
	if phase != plan.PhaseBefore && phase != plan.PhaseAfter {
		return usageError{fmt.Sprintf("--phase must be before or after, got %q", o.phase)}
	}
	if o.file == "" {
		return usageError{"--file is required"}
	}
	if o.step < 0 {
		return usageError{"--step must be 1 or more"}
	}
	src := o.file
	if !filepath.IsAbs(src) {
		src = filepath.Join(a.Cwd, src)
	}
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(o.id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", o.id)
		}
		head, err := a.worktreeHead(in, o.id)
		if err != nil {
			return "", err
		}
		wt := in.Worktree(o.id)
		step := o.step
		switch phase {
		case plan.PhaseBefore:
			if err := a.checkBeforeAllowed(in, p, wt); err != nil {
				return "", err
			}
		case plan.PhaseAfter:
			if p.Status != plan.DirectorReview {
				return "", fmt.Errorf("%s is %s; after evidence is captured from approved code (director-review, approved). Wait for prstk review %s --file <path> --verdict approved", o.id, p.Status, o.id)
			}
			if _, verdict := p.LatestDirectorVerdict(); !strings.HasPrefix(verdict, "approved") {
				return "", fmt.Errorf("the director hasn't approved %s yet; wait for prstk review %s --file <path> --verdict approved, then capture after evidence", o.id, o.id)
			}
			if out, err := a.Git.Run(wt, "status", "--porcelain", "--untracked-files=no"); err != nil {
				return "", err
			} else if out != "" {
				return "", fmt.Errorf("worktree %s has uncommitted changes; after evidence must come from the committed, approved code", wt)
			}
			if err := a.checkPushed(in, p, head); err != nil {
				return "", err
			}
			if step == 0 {
				m, err := plan.LoadEvidence(p)
				if err != nil {
					return "", err
				}
				for _, pr := range m.Pairs() {
					if pr.Before != nil && (pr.After == nil || pr.After.Commit != head) {
						step = pr.Step
						break
					}
				}
				if step == 0 {
					return "", fmt.Errorf("no before step is missing an after item at HEAD %s; pass --step N to record another", shortSHA(head))
				}
			}
		}
		it, err := p.AddEvidence(src, plan.EvidenceItem{Phase: phase, Step: step, Caption: o.caption, Commit: head})
		if err != nil {
			return "", err
		}
		if err := in.AppendLog(a.Now(), "evidence", fmt.Sprintf("%s %s step %d %s %s @ %s", o.id, phase, it.Step, it.Kind, it.File, shortSHA(head))); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s step %d: %s (%s) @ %s", o.id, phase, it.Step, it.File, it.Kind, shortSHA(head)), nil
	})
}

// checkBeforeAllowed enforces that before evidence comes from the untouched
// worktree: in-progress, no tracked changes, no commits past the start.
func (a *App) checkBeforeAllowed(in *plan.Initiative, p *plan.Spec, wt string) error {
	const why = "before evidence must be captured before any code changes"
	if p.Status != plan.InProgress {
		return fmt.Errorf("%s is %s; %s (only an in-progress PR can take before evidence)", p.ID, p.Status, why)
	}
	out, err := a.Git.Run(wt, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("worktree %s has tracked changes; %s", wt, why)
	}
	start := evidenceStart(in, p)
	n, err := a.Git.Run(wt, "rev-list", "--count", start+"..HEAD")
	if err != nil {
		return err
	}
	if n != "0" {
		return fmt.Errorf("%s has %s commit(s) beyond %s; %s", p.Branch, n, start, why)
	}
	return nil
}

// ---- evidence script ----

func cmdEvidenceScript(a *App, id, file string) error {
	if file == "" {
		return usageError{"--file is required"}
	}
	src := file
	if !filepath.IsAbs(src) {
		src = filepath.Join(a.Cwd, src)
	}
	return a.mutate(func(in *plan.Initiative) (string, error) {
		p := in.PR(id)
		if p == nil {
			return "", fmt.Errorf("no PR %q", id)
		}
		if p.Status == plan.Merged || p.Status == plan.Abandoned {
			return "", fmt.Errorf("%s is %s", id, p.Status)
		}
		name, err := p.SetEvidenceScript(src)
		if err != nil {
			return "", err
		}
		if err := in.AppendLog(a.Now(), "evidence", fmt.Sprintf("%s script %s", id, name)); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s capture script: %s", id, name), nil
	})
}

// ---- evidence ls ----

func cmdEvidenceLs(a *App, id string) error {
	in, err := a.load()
	if err != nil {
		return err
	}
	p := in.PR(id)
	if p == nil {
		return fmt.Errorf("no PR %q", id)
	}
	m, err := plan.LoadEvidence(p)
	if err != nil {
		return err
	}
	if a.jsonOut {
		return a.printJSON(struct {
			Level string `json:"level"`
			plan.EvidenceManifest
		}{p.Evidence, m})
	}
	head := ""
	if h, err := a.worktreeHead(in, id); err == nil {
		head = h
	}
	level := p.Evidence
	if level == "" {
		level = "none"
	}
	a.out("evidence: %s", level)
	a.out("script: %s", orDashStr(m.Script))
	for _, pr := range m.Pairs() {
		before, after := "—", "—"
		if pr.Before != nil {
			before = pr.Before.File
		}
		line := ""
		if pr.After != nil {
			after = pr.After.File
			tag := "@" + shortSHA(pr.After.Commit)
			if head != "" {
				if pr.After.Commit == head {
					tag += ", current"
				} else {
					tag += ", stale"
				}
			}
			line = " [" + tag + "]"
		}
		a.out("step %d  before: %s  after: %s%s", pr.Step, before, after, line)
	}
	return nil
}

func orDashStr(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ---- show / spawn-prompt / doctor helpers ----

// evidenceSummary is "<level> (<n> before / <m> after)".
func evidenceSummary(p *plan.Spec) string {
	m, _ := plan.LoadEvidence(p)
	b, af := evidenceCounts(m)
	return fmt.Sprintf("%s (%d before / %d after)", p.Evidence, b, af)
}

func evidenceSpawnParagraph(p *plan.Spec) string {
	if p.Evidence == plan.EvidenceNone {
		return ""
	}
	switch p.Status {
	case plan.Fixing, plan.FixPending, plan.ChangesRequested:
		return fmt.Sprintf("This PR has audit evidence (%s). Once the director approves your new code, the after evidence must be re-captured: it will ask you to re-run the registered capture script and record it with `prstk evidence add %s --phase after`.\n", p.Evidence, p.ID)
	}
	return fmt.Sprintf("This PR needs audit evidence (%s). Before changing any code, follow `## Evidence` in spec.md: write the capture script, register it with `prstk evidence script %s --file <path>`, capture each step and record it with `prstk evidence add %s --phase before --file <path>`. After the director approves, it will ask you to re-run the same script and record `--phase after`.\n", p.Evidence, p.ID, p.ID)
}

// doctorEvidence prints the capture-tool lines when any live PR uses evidence.
// A missing tool only warns: a PR needs just one of them.
func (a *App) doctorEvidence(in *plan.Initiative) {
	used := false
	for _, p := range in.PRs {
		if p.Evidence != plan.EvidenceNone && p.Status != plan.Merged && p.Status != plan.Abandoned {
			used = true
		}
	}
	if !used {
		return
	}
	tool := func(name, bin, ok, hint string) {
		if _, err := lookPath(bin); err != nil {
			a.out("⚠ %-14s %s", name, hint)
		} else {
			a.out("✓ %-14s %s", name, ok)
		}
	}
	tool("playwright", "npx", "via npx", "npx not found (install Node, https://nodejs.org); screenshots need Playwright or vhs")
	tool("vhs", "vhs", "found on PATH", "vhs not found (brew install vhs); video needs vhs or Playwright")
}
