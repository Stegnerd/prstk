package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stegnerd/prstk/internal/plan"
)

// evPR returns an env with PR 01 added at the given evidence level and
// assigned (worktree clean, no commits).
func evPR(t *testing.T, level string) *env {
	t.Helper()
	e := newEnv(t)
	e.run("init", "ev")
	args := []string{"add", "01", "scaffold", "--title", "Scaffold"}
	if level != "" {
		args = append(args, "--evidence", level)
	}
	e.run(args...)
	e.run("assign", "01", "--worker", "w1")
	return e
}

func (e *env) shot(name string) string { return e.writeTemp(name, "bytes of "+name) }

func (e *env) evAdd(phase, name string, extra ...string) (string, string, int) {
	args := append([]string{"evidence", "add", "01", "--phase", phase, "--file", e.shot(name)}, extra...)
	return e.try(args...)
}

func (e *env) approve() {
	e.t.Helper()
	e.run("review", "01", "--file", e.writeTemp("r.md", "Looks good.\n"), "--verdict", "approved", "--no-post")
}

func (e *env) logText() string { return e.readFile(e.initiative().LogPath()) }

func (e *env) head() string { return git(e.t, e.initiative().Worktree("01"), "rev-parse", "HEAD") }

func TestAddEvidenceSkeleton(t *testing.T) {
	e := newEnv(t)
	e.run("init", "ev")
	e.run("add", "01", "plain", "--title", "Plain")
	e.run("add", "02", "shots", "--title", "Shots", "--evidence", "video")
	plain := e.readFile(e.initiative().PR("01").SpecPath())
	if strings.Contains(plain, "## Evidence") || strings.Contains(plain, "evidence: video") {
		t.Fatalf("plain spec has evidence:\n%s", plain)
	}
	p := e.initiative().PR("02")
	if p.Evidence != "video" {
		t.Fatalf("evidence = %q", p.Evidence)
	}
	spec := e.readFile(p.SpecPath())
	d, ev, r := strings.Index(spec, "## Definition of done"), strings.Index(spec, "## Evidence\n"), strings.Index(spec, "## Review: Director")
	if d < 0 || !(d < ev && ev < r) {
		t.Fatalf("section order wrong (%d %d %d):\n%s", d, ev, r, spec)
	}
	if !strings.Contains(spec, "<!-- List the screens and flows to capture, one numbered step each") {
		t.Fatalf("placeholder missing:\n%s", spec)
	}
	// The placeholder is a comment, so validate still warns about an empty section.
	if _, se, _ := e.try("validate"); !strings.Contains(se, "Evidence") {
		t.Fatalf("validate should warn about the empty Evidence section: %q", se)
	}
	// Re-running add leaves the body alone.
	e.run("add", "02", "shots", "--title", "Shots", "--evidence", "video")
	if got := e.readFile(p.SpecPath()); !strings.Contains(got, "## Evidence") || strings.Count(got, "## Evidence") != 1 {
		t.Fatalf("re-add changed body:\n%s", got)
	}
	if _, se, code := e.try("add", "03", "bad", "--title", "Bad", "--evidence", "gif"); code != 2 || !strings.Contains(se, "--evidence must be") {
		t.Fatalf("bad level: %d %s", code, se)
	}
}

func TestEvidenceBeforeRules(t *testing.T) {
	e := evPR(t, "screenshots")
	wt := e.initiative().Worktree("01")

	// Untracked files don't count as changes.
	os.WriteFile(filepath.Join(wt, "scratch.txt"), []byte("x"), 0o644)
	out, se, code := e.evAdd("before", "home.png", "--caption", "Home")
	if code != 0 || out != "01 before step 1: home.png (image) @ "+e.head()[:7] {
		t.Fatalf("clean fresh branch: %d %q %s", code, out, se)
	}
	if lg := e.logText(); !strings.Contains(lg, "· evidence · prstk\n01 before step 1 image home.png @ "+e.head()[:7]) {
		t.Fatalf("LOG:\n%s", lg)
	}
	m, err := plan.LoadEvidence(e.initiative().PR("01"))
	if err != nil || len(m.Items) != 1 || m.Items[0].Commit != e.head() || m.Items[0].Caption != "Home" {
		t.Fatalf("manifest: %+v %v", m, err)
	}

	// A tracked change blocks it.
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("changed\n"), 0o644)
	if _, se, code := e.evAdd("before", "two.png"); code != 1 || !strings.Contains(se, "before any code changes") {
		t.Fatalf("dirty: %d %s", code, se)
	}
	git(t, wt, "checkout", "README.md")
	if _, _, code := e.evAdd("before", "two.png"); code != 0 {
		t.Fatal("clean again should pass")
	}

	// A commit beyond the start blocks it.
	e.work("01", "main.go", "package main\n")
	if _, se, code := e.evAdd("before", "three.png"); code != 1 || !strings.Contains(se, "commit(s) beyond origin/develop") {
		t.Fatalf("commits: %d %s", code, se)
	}
	// And so does a status other than in-progress.
	e.run("open", "01", "--skip-evidence", "test")
	if _, se, code := e.evAdd("before", "three.png"); code != 1 || !strings.Contains(se, "director-review") {
		t.Fatalf("status: %d %s", code, se)
	}
}

func TestEvidenceBeforeOnStackedPR(t *testing.T) {
	e := newEnv(t)
	e.run("init", "ev")
	e.run("add", "01", "base", "--title", "Base")
	e.run("add", "02", "child", "--title", "Child", "--parent", "01", "--evidence", "screenshots")
	e.run("assign", "01", "--worker", "w1")
	e.work("01", "a.go", "package a\n")
	e.run("open", "01")
	e.run("ready", "01")
	e.run("assign", "02", "--worker", "w2")
	// The child's branch already holds the parent's commit; that isn't "beyond the start".
	out := e.run("evidence", "add", "02", "--phase", "before", "--file", e.shot("x.png"))
	if !strings.Contains(out, "02 before step 1") {
		t.Fatalf("out: %s", out)
	}
}

func TestEvidenceAfterRules(t *testing.T) {
	e := evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	e.evAdd("before", "b.png")
	e.work("01", "main.go", "package main\n")

	// in-progress: refused.
	if _, se, code := e.evAdd("after", "a2.png"); code != 1 || !strings.Contains(se, "director-review") {
		t.Fatalf("in-progress: %d %s", code, se)
	}
	e.run("open", "01", "--skip-evidence", "test")
	// director-review but no verdict yet: refused.
	if _, se, code := e.evAdd("after", "a2.png"); code != 1 || !strings.Contains(se, "--verdict approved") {
		t.Fatalf("no verdict: %d %s", code, se)
	}
	// changes-requested verdict: refused.
	e.run("review", "01", "--file", e.writeTemp("r.md", "Fix it.\n"), "--verdict", "changes-requested", "--no-post")
	if _, se, code := e.evAdd("after", "a2.png"); code != 1 || !strings.Contains(se, "--verdict approved") {
		t.Fatalf("changes requested: %d %s", code, se)
	}
	e.run("status", "01", "changes-requested")
	e.work("01", "fix.go", "package main\n")
	e.run("open", "01", "--skip-evidence", "test")
	e.approve()

	// Dirty tracked file: refused.
	wt := e.initiative().Worktree("01")
	os.WriteFile(filepath.Join(wt, "main.go"), []byte("package changed\n"), 0o644)
	if _, se, code := e.evAdd("after", "a2.png"); code != 1 || !strings.Contains(se, "uncommitted") {
		t.Fatalf("dirty: %d %s", code, se)
	}
	git(t, wt, "checkout", "main.go")

	// Steps auto-pick: lowest before step without an after at HEAD.
	if out := e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png")); !strings.Contains(out, "01 after step 1: a2.png (image)") {
		t.Fatalf("first after: %s", out)
	}
	if out := e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b2.png")); !strings.Contains(out, "01 after step 2: b2.png (image)") {
		t.Fatalf("second after: %s", out)
	}
	if _, se, code := e.evAdd("after", "c2.png"); code != 1 || !strings.Contains(se, "pass --step") {
		t.Fatalf("all covered: %d %s", code, se)
	}
	// An explicit step still works (re-capture of step 2).
	if out := e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b3.png"), "--step", "2"); !strings.Contains(out, "step 2: b3.png") {
		t.Fatalf("explicit: %s", out)
	}
}

func TestEvidenceOpenGate(t *testing.T) {
	e := evPR(t, "screenshots")
	e.work("01", "main.go", "package main\n")
	_, se, code := e.try("open", "01")
	if code != 1 || !strings.Contains(se, "no before evidence") || !strings.Contains(se, "prstk evidence add 01 --phase before") {
		t.Fatalf("open w/o before: %d %s", code, se)
	}
	if e.status("01") != "in-progress" || len(e.gh.PRs) != 0 {
		t.Fatal("refused open must not push or change anything")
	}
	if git(t, e.repo, "ls-remote", "--heads", "origin", "prstk/ev/01-scaffold") != "" {
		t.Fatal("refused open pushed the branch")
	}

	// screenshots level: an image before is enough, and PR gets the evidence line.
	e = evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	if body := e.gh.PRs[101].Body; !strings.Contains(body, "📷 This PR has audit evidence (screenshots); see its card on the prstk board.") || !strings.Contains(body, "Implements 01.") {
		t.Fatalf("PR body:\n%s", body)
	}

	// video level: needs a before video.
	e = evPR(t, "video")
	e.evAdd("before", "a.png")
	e.work("01", "main.go", "package main\n")
	if _, se, code := e.try("open", "01"); code != 1 || !strings.Contains(se, "no before video") {
		t.Fatalf("open w/o video: %d %s", code, se)
	}
	// With a before video recorded as well, open passes.
	e2 := evPR(t, "video")
	e2.evAdd("before", "a.png")
	e2.evAdd("before", "flow.webm")
	e2.work("01", "main.go", "package main\n")
	e2.run("open", "01")
}

func TestEvidenceReadyGate(t *testing.T) {
	e := evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	e.evAdd("before", "b.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()

	_, se, code := e.try("ready", "01")
	if code != 1 || !strings.Contains(se, "step 1: no after evidence") || !strings.Contains(se, "step 2: no after evidence") {
		t.Fatalf("no after: %d %s", code, se)
	}
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png"))
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b2.png"))

	// New commit after the captures: they're stale.
	e.work("01", "more.go", "package main\n")
	e.run("open", "01") // push it, so only the staleness is in question
	_, se, code = e.try("ready", "01")
	if code != 1 || !strings.Contains(se, "step 1: after evidence is stale") || !strings.Contains(se, "step 2: after evidence is stale") {
		t.Fatalf("stale: %d %s", code, se)
	}
	if out, _, _ := e.try("evidence", "ls", "01"); !strings.Contains(out, "stale") {
		t.Fatalf("ls should say stale:\n%s", out)
	}
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a3.png"))
	_, se, code = e.try("ready", "01")
	if code != 1 || strings.Contains(se, "step 1") || !strings.Contains(se, "step 2: after evidence is stale") {
		t.Fatalf("only step 2 stale: %d %s", code, se)
	}
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b3.png"))
	e.run("ready", "01")
	if e.status("01") != "awaiting-you" {
		t.Fatalf("status %s", e.status("01"))
	}
}

func TestEvidenceReadyGateVideo(t *testing.T) {
	e := evPR(t, "video")
	e.evAdd("before", "a.png")
	e.evAdd("before", "flow.webm")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png"))
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("flow2.mp4"))
	// Both steps covered at HEAD, with a video.
	e.run("ready", "01")

	e = evPR(t, "video")
	e.evAdd("before", "a.png")
	e.evAdd("before", "flow.webm")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png"))
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b2.png"))
	if _, se, code := e.try("ready", "01"); code != 1 || !strings.Contains(se, "no after video at HEAD") {
		t.Fatalf("no after video: %d %s", code, se)
	}
}

func TestEvidenceSkip(t *testing.T) {
	e := evPR(t, "screenshots")
	e.work("01", "main.go", "package main\n")
	if _, se, code := e.try("open", "01", "--skip-evidence", ""); code != 2 || !strings.Contains(se, "--skip-evidence needs a reason") {
		t.Fatalf("empty reason: %d %s", code, se)
	}
	if _, se, code := e.try("ready", "01", "--skip-evidence", "  "); code != 2 || !strings.Contains(se, "needs a reason") {
		t.Fatalf("blank reason: %d %s", code, se)
	}
	e.run("open", "01", "--skip-evidence", "no UI change")
	if lg := e.logText(); !strings.Contains(lg, "· evidence · prstk\n01 gate skipped at open: no UI change") {
		t.Fatalf("LOG:\n%s", lg)
	}

	// ready: a before step with no after is a real failure that the skip bypasses.
	e = evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	if _, _, code := e.try("ready", "01"); code != 1 {
		t.Fatal("ready should refuse")
	}
	e.run("ready", "01", "--skip-evidence", "docs only")
	if lg := e.logText(); !strings.Contains(lg, "· evidence · prstk\n01 gate skipped at ready: docs only") || e.status("01") != "awaiting-you" {
		t.Fatalf("ready skip: %s", lg)
	}
}

func TestEvidenceGatesIgnoreUnsetLevel(t *testing.T) {
	e := evPR(t, "")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.run("ready", "01", "--skip-evidence", "habit") // harmless, and nothing was skipped
	if strings.Contains(e.logText(), "gate skipped") {
		t.Fatalf("logged a skip for a PR without evidence:\n%s", e.logText())
	}
	if _, se, code := e.try("open", "01", "--skip-evidence", ""); code != 2 {
		t.Fatalf("empty reason is a usage error regardless: %d %s", code, se)
	}
	if body := e.gh.PRs[101].Body; strings.Contains(body, "Audit evidence") {
		t.Fatalf("PR body mentions evidence:\n%s", body)
	}
	out := e.run("show", "01")
	if strings.Contains(out, "evidence") {
		t.Fatalf("show:\n%s", out)
	}
	e.run("spawn-prompt", "01", "--worker", "w1")
}

func TestEvidenceScriptAndLs(t *testing.T) {
	e := evPR(t, "screenshots")
	in := e.initiative()
	if out := e.run("evidence", "ls", "01"); out != "evidence: screenshots\nscript: —" {
		t.Fatalf("empty ls: %q", out)
	}
	script := e.writeTemp("capture.mjs", "console.log('hi')\n")
	if out := e.run("evidence", "script", "01", "--file", script); !strings.Contains(out, "capture.mjs") {
		t.Fatalf("script: %s", out)
	}
	if lg := e.logText(); !strings.Contains(lg, "· evidence · prstk\n01 script capture.mjs") {
		t.Fatalf("LOG:\n%s", lg)
	}
	if _, se, code := e.try("evidence", "script", "01", "--file", e.shot("x.png")); code != 1 || !strings.Contains(se, "unsupported script type") {
		t.Fatalf("bad script: %d %s", code, se)
	}
	e.evAdd("before", "a.png")
	e.evAdd("before", "b.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png"))
	head := e.head()[:7]
	out := e.run("evidence", "ls", "01")
	want := "evidence: screenshots\nscript: capture.mjs\nstep 1  before: a.png  after: a2.png [@" + head + ", current]\nstep 2  before: b.png  after: —"
	if out != want {
		t.Fatalf("ls:\n%s\nwant:\n%s", out, want)
	}
	var js struct {
		Level  string `json:"level"`
		Script string `json:"script"`
		Items  []struct {
			Phase string `json:"phase"`
			Step  int    `json:"step"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(e.run("evidence", "ls", "01", "--json")), &js); err != nil || js.Level != "screenshots" || js.Script != "capture.mjs" || len(js.Items) != 3 {
		t.Fatalf("json: %+v %v", js, err)
	}

	// show
	if out := e.run("show", "01"); !strings.Contains(out, "evidence   screenshots (2 before / 1 after)") || !strings.Contains(out, "evidence_dir "+in.PR("01").EvidenceDir()) {
		t.Fatalf("show:\n%s", out)
	}
	var sj map[string]any
	if err := json.Unmarshal([]byte(e.run("show", "01", "--json")), &sj); err != nil || sj["evidence"] != "screenshots" || sj["evidence_dir"] != in.PR("01").EvidenceDir() {
		t.Fatalf("show json: %v %v", sj, err)
	}

	// With the worktree gone, staleness is unknown.
	e.run("ready", "01", "--skip-evidence", "step 2 not applicable")
	os.RemoveAll(in.Worktree("01"))
	if out := e.run("evidence", "ls", "01"); !strings.Contains(out, "after: a2.png [@"+head+"]") {
		t.Fatalf("no-worktree ls:\n%s", out)
	}
	if _, se, code := e.evAdd("after", "z.png"); code != 1 || !strings.Contains(se, "no worktree") {
		t.Fatalf("add w/o worktree: %d %s", code, se)
	}
	// A finished PR can't take a script.
	e.run("status", "01", "abandoned")
	if _, se, code := e.try("evidence", "script", "01", "--file", script); code != 1 || !strings.Contains(se, "abandoned") {
		t.Fatalf("script on abandoned: %d %s", code, se)
	}
}

func TestEvidenceSpawnPrompt(t *testing.T) {
	e := evPR(t, "video")
	out := e.run("spawn-prompt", "01")
	for _, want := range []string{
		"This PR needs audit evidence (video). Before changing any code, follow `## Evidence` in spec.md: write the capture script, register it with `prstk evidence script 01 --file <path>`",
		"`prstk evidence add 01 --phase before --file <path>`",
		"After the director approves, it will ask you to re-run the same script and record `--phase after`.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	e.evAdd("before", "a.png")
	e.evAdd("before", "f.webm")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.run("status", "01", "changes-requested")
	out = e.run("spawn-prompt", "01")
	if strings.Contains(out, "Before changing any code") || !strings.Contains(out, "after evidence must be re-captured") {
		t.Fatalf("changes-requested reminder:\n%s", out)
	}
}

func TestEvidenceHelpListsCommand(t *testing.T) {
	e := newEnv(t)
	out := e.run("--help")
	if !strings.Contains(out, "evidence") {
		t.Fatalf("help:\n%s", out)
	}
	if out := e.run("evidence", "--help"); !strings.Contains(out, "add") || !strings.Contains(out, "script") || !strings.Contains(out, "ls") {
		t.Fatalf("evidence help:\n%s", out)
	}
	if _, se, code := e.try("evidence", "add", "01"); code != 2 {
		t.Fatalf("missing flags: %d %s", code, se)
	}
	if _, se, code := e.try("evidence", "add", "01", "--phase", "during", "--file", "x.png"); code != 2 || !strings.Contains(se, "prstk evidence add:") {
		t.Fatalf("bad phase: %d %s", code, se)
	}
}

func TestEvidenceReadyNeedsAnAfter(t *testing.T) {
	e := evPR(t, "screenshots")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01", "--skip-evidence", "brand-new screen, nothing to capture before")
	e.approve()
	// No items at all: ready refuses.
	if _, se, code := e.try("ready", "01"); code != 1 || !strings.Contains(se, "no after evidence at HEAD") {
		t.Fatalf("empty ready: %d %s", code, se)
	}
	// An after at HEAD (no before step needed) is enough.
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("new.png"), "--step", "1")
	e.run("ready", "01")
	if e.status("01") != "awaiting-you" {
		t.Fatalf("status %s", e.status("01"))
	}
}

func TestEvidenceAfterNeedsPushedHead(t *testing.T) {
	e := evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()
	// Commit after the approval without running open: HEAD isn't what was pushed.
	e.work("01", "sneaky.go", "package main\n")
	_, se, code := e.evAdd("after", "a2.png")
	if code != 1 || !strings.Contains(se, "not what was pushed") || !strings.Contains(se, "prstk open 01") {
		t.Fatalf("add after at unpushed HEAD: %d %s", code, se)
	}
	// An after recorded at the pushed commit, then a new unpushed commit: ready refuses too.
	git(t, e.initiative().Worktree("01"), "reset", "--hard", "HEAD~1")
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png"))
	e.work("01", "sneaky.go", "package main\n")
	_, se, code = e.try("ready", "01")
	if code != 1 || !strings.Contains(se, "not what was pushed") || !strings.Contains(se, "prstk open 01") {
		t.Fatalf("ready at unpushed HEAD: %d %s", code, se)
	}
	e.run("open", "01")
	_, se, code = e.try("ready", "01")
	if code != 1 || !strings.Contains(se, "stale") {
		t.Fatalf("after open the old capture is stale: %d %s", code, se)
	}
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a3.png"))
	e.run("ready", "01")
}
