package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/gh"
	"github.com/Stegnerd/prstk/internal/plan"
)

type env struct {
	t      *testing.T
	home   string // plans root
	repo   string // main working clone
	remote string
	gh     *gh.Fake
	now    time.Time
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	gcfg := filepath.Join(root, "gitconfig")
	os.WriteFile(gcfg, []byte("[user]\n\tname = Test\n\temail = t@example.com\n[init]\n\tdefaultBranch = develop\n[advice]\n\tdetachedHead = false\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gcfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	remote := filepath.Join(root, "remotes", "dalton", "sample_proj.git")
	os.MkdirAll(remote, 0o755)
	git(t, remote, "init", "--bare", "-b", "develop")
	repo := filepath.Join(root, "sample_proj")
	git(t, root, "clone", remote, repo)
	os.WriteFile(filepath.Join(repo, "README.md"), []byte("# sample\n"), 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	git(t, repo, "push", "-u", "origin", "develop")

	e := &env{t: t, home: filepath.Join(root, "plans"), repo: repo, remote: remote, gh: gh.NewFake(),
		now: time.Date(2026, 11, 3, 14, 15, 0, 0, time.Local)}
	return e
}

func (e *env) app(cwd string) *App {
	return &App{
		Cwd: cwd, Home: e.home, Now: func() time.Time { return e.now }, GH: e.gh,
		Getenv: func(string) string { return "" }, UserHome: e.t.TempDir(),
	}
}

// run executes prstk from the main repo and fails the test on non-zero exit.
func (e *env) run(args ...string) string {
	e.t.Helper()
	out, errOut, code := e.try(args...)
	if code != 0 {
		e.t.Fatalf("prstk %v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

func (e *env) try(args ...string) (string, string, int) {
	return e.tryIn(e.repo, args...)
}

func (e *env) tryIn(cwd string, args ...string) (string, string, int) {
	var o, er bytes.Buffer
	a := e.app(cwd)
	a.Stdout, a.Stderr = &o, &er
	code := a.Run(args)
	return strings.TrimSpace(o.String()), strings.TrimSpace(er.String()), code
}

func (e *env) initiative() *plan.Initiative {
	e.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(e.home, "dalton-sample_proj", "2026-*"))
	if len(dirs) != 1 {
		e.t.Fatalf("want 1 initiative, got %v", dirs)
	}
	in, err := plan.LoadInitiative(dirs[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return in
}

func (e *env) status(id string) plan.Status { return e.initiative().PR(id).Status }

// work simulates a worker: writes a file in the PR's worktree, commits,
// pushes, and fills notes.md.
func (e *env) work(id, file, content string) {
	e.t.Helper()
	in := e.initiative()
	wt := in.Worktree(id)
	os.MkdirAll(filepath.Dir(filepath.Join(wt, file)), 0o755)
	if err := os.WriteFile(filepath.Join(wt, file), []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	git(e.t, wt, "add", ".")
	git(e.t, wt, "commit", "-m", "work on "+id)
	p := in.PR(id)
	os.WriteFile(p.NotesPath(), []byte("## Description\nImplements "+id+".\n\n## Worker notes\nnone\n"), 0o644)
}

func (e *env) addPilot() {
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("add", "02", "db-layer", "--title", "DB layer", "--parent", "01", "--model", "opus")
	e.run("add", "03", "read-endpoints", "--title", "Read endpoints", "--parent", "02")
	e.run("add", "04", "types-endpoints", "--title", "Types endpoints", "--parent", "02")
	e.run("add", "05", "write-endpoints", "--title", "Write endpoints", "--parent", "03")
	e.run("add", "06", "middleware", "--title", "Middleware", "--parent", "01", "--conflicts-with", "02")
}

func TestPilotLifecycle(t *testing.T) {
	e := newEnv(t)
	out := e.run("init", "pokemon-api")
	if !strings.Contains(out, "created") || !strings.Contains(out, "base develop") {
		t.Fatalf("init: %s", out)
	}
	// idempotent
	if out := e.run("init", "pokemon-api"); !strings.Contains(out, "reused") {
		t.Fatalf("re-init: %s", out)
	}
	e.addPilot()

	in := e.initiative()
	if got := in.PR("02").ConflictsWith; len(got) != 1 || got[0] != "06" {
		t.Fatalf("conflicts not symmetric: %v", got)
	}
	if in.PR("02").Model != "opus" || in.PR("02").Branch != "prstk/pokemon-api/02-db-layer" {
		t.Fatalf("02: %+v", in.PR("02"))
	}
	if out := e.run("validate"); !strings.HasPrefix(out, "ok · 6 PRs") {
		t.Fatalf("validate: %s", out)
	}
	plan, _ := os.ReadFile(in.PlanPath())
	if !strings.Contains(string(plan), "```mermaid") || !strings.Contains(string(plan), "## Split rationale") {
		t.Fatalf("PLAN.md:\n%s", plan)
	}
	if _, err := os.Stat(in.BoardPath()); err != nil {
		t.Fatal("board.html not written")
	}

	if out := e.run("next"); !strings.HasPrefix(out, "01 scaffold · new") || strings.Count(out, "\n") != 0 {
		t.Fatalf("next: %q", out)
	}
	if _, _, code := e.try("assign", "02", "--worker", "w1"); code == 0 {
		t.Fatal("assigning 02 before 01 passes review should fail")
	}

	// 01: assign → work → open → changes requested → open → ready
	e.run("assign", "01", "--worker", "w1")
	if out := e.run("assign", "01", "--worker", "w1"); !strings.Contains(out, "already") {
		t.Fatalf("assign not idempotent: %s", out)
	}
	wt := e.initiative().Worktree("01")
	if br := git(t, wt, "branch", "--show-current"); br != "prstk/pokemon-api/01-scaffold" {
		t.Fatalf("worktree branch %s", br)
	}
	e.work("01", "main.go", "package main\n")

	// open is idempotent across a gh failure after push
	e.gh.FailOn["CreatePR"] = true
	if _, _, code := e.try("open", "01"); code == 0 {
		t.Fatal("open should fail when CreatePR fails")
	}
	if e.status("01") != "in-progress" {
		t.Fatalf("status after failed open: %s", e.status("01"))
	}
	out = e.run("open", "01")
	if !strings.Contains(out, "opened draft #101") {
		t.Fatalf("open: %s", out)
	}
	pr := e.gh.PRs[101]
	if !pr.IsDraft || pr.BaseRef != "develop" || !strings.Contains(pr.Body, "Implements 01.") || !strings.Contains(pr.Body, "**#101 01 scaffold ← this PR**") {
		t.Fatalf("PR 101: %+v", pr)
	}
	if git(t, e.repo, "ls-remote", "--heads", "origin", "prstk/pokemon-api/01-scaffold") == "" {
		t.Fatal("branch not pushed")
	}
	if out := e.run("next"); !strings.HasPrefix(out, "none") {
		t.Fatalf("next while 01 in review: %s", out)
	}
	e.run("status", "01", "changes-requested")
	e.work("01", "Makefile", "all:\n")
	e.run("open", "01")
	if e.status("01") != "director-review" {
		t.Fatal("re-open should return to director-review")
	}
	e.run("ready", "01")
	if e.gh.PRs[101].IsDraft || e.status("01") != "awaiting-you" || e.initiative().PR("01").Worker != "" {
		t.Fatal("ready didn't flip draft / release worker")
	}

	// 02 is next; 06 conflicts with 02 so it's deferred while 02 is active.
	if out := e.run("next"); !strings.HasPrefix(out, "02 db-layer · new · opus · critical path 3") || strings.Contains(out, "06") {
		t.Fatalf("next after 01 ready: %q", out)
	}
	e.run("assign", "02", "--worker", "w1")
	if out := e.run("next"); strings.Contains(out, "06") {
		t.Fatalf("06 should be deferred while 02 active: %q", out)
	}
	if _, se, code := e.try("assign", "06", "--worker", "w2"); code == 0 || !strings.Contains(se, "conflicts_with 02") {
		t.Fatalf("assign 06 should be refused: %s", se)
	}
	e.work("02", "db/db.go", "package db\n\n// v1\n")
	e.run("open", "02")
	if e.gh.PRs[102].BaseRef != "prstk/pokemon-api/01-scaffold" {
		t.Fatalf("02 base = %s", e.gh.PRs[102].BaseRef)
	}
	if !strings.Contains(e.gh.PRs[101].Body, "#102 02 db layer") {
		t.Fatalf("stack nav not updated on #101:\n%s", e.gh.PRs[101].Body)
	}
	e.run("ready", "02")

	// 03 (critical 1) then 04 (tie with 06 broken by id).
	out = e.run("next")
	if lines := strings.Split(out, "\n"); len(lines) != 2 || !strings.HasPrefix(lines[0], "03") || !strings.HasPrefix(lines[1], "04") {
		t.Fatalf("next: %q", out)
	}
	e.run("assign", "03", "--worker", "w1")
	e.run("assign", "04", "--worker", "w2")
	if _, se, code := e.try("assign", "06", "--worker", "w3"); code == 0 || !strings.Contains(se, "no free slot") {
		t.Fatalf("third assign should fail on slots: %s", se)
	}
	// 03 branched from 02
	if !strings.Contains(git(t, e.initiative().Worktree("03"), "log", "--oneline"), "work on 02") {
		t.Fatal("03 not branched from 02")
	}
	e.work("03", "api/read.go", "package api\n")
	e.work("04", "db/db.go", "package db\n\n// types change\n") // will conflict

	// Dalton feedback on 02 → fix worker → new commit → propagate.
	e.run("status", "02", "fix-pending", "--reason", "rename table")
	if out := e.run("next"); out != "none · no free slots (2/2 used)" {
		t.Fatalf("next with full slots: %q", out)
	}
	e.run("open", "03") // 03 → director-review (still holds slot)
	e.run("status", "03", "changes-requested")
	e.run("status", "03", "blocked", "--reason", "waiting on 02 fix")
	if e.initiative().PR("03").BlockedFrom != "changes-requested" {
		t.Fatal("blocked_from not recorded")
	}
	e.run("status", "03", "in-progress") // unblock → restores changes-requested
	if e.status("03") != "changes-requested" {
		t.Fatalf("unblock restored %s", e.status("03"))
	}
	e.run("open", "03")
	e.run("ready", "03")
	e.run("assign", "02", "--worker", "fixer")
	if e.status("02") != "fixing" {
		t.Fatal("assign from fix-pending should set fixing")
	}
	e.work("02", "db/db.go", "package db\n\n// v2 renamed\n")
	git(t, e.initiative().Worktree("02"), "push")
	e.run("open", "02")
	e.run("ready", "02")

	out = e.run("propagate", "02")
	if !strings.Contains(out, "03 merged+pushed") || !strings.Contains(out, "05 not started") || !strings.Contains(out, "04 conflict → fix-pending (db/db.go)") {
		t.Fatalf("propagate: %s", out)
	}
	in = e.initiative()
	if in.PR("04").Status != "fix-pending" || !strings.Contains(in.PR("04").BlockedReason, "db/db.go") {
		t.Fatalf("04: %+v", in.PR("04"))
	}
	if !strings.Contains(git(t, in.Worktree("03"), "log", "--oneline", "-3"), "Merge branch") {
		t.Fatal("03 didn't get a merge commit")
	}
	if dirty, _ := (&App{}).Git.Dirty(in.Worktree("04")); dirty {
		t.Fatal("merge --abort left 04 dirty")
	}
	// re-run is a no-op for 03
	if out := e.run("propagate", "02"); !strings.Contains(out, "03 up to date") {
		t.Fatalf("propagate re-run: %s", out)
	}

	// Merge 01: verify, retarget 02 and (no PR) 06, remove worktree.
	if _, se, code := e.try("merged", "01"); code == 0 || !strings.Contains(se, "not merged on GitHub") {
		t.Fatalf("merged before GitHub merge: %s", se)
	}
	e.gh.Merge(101)
	out = e.run("merged", "01")
	if !strings.Contains(out, "retargeted #102 → develop") || !strings.Contains(out, "worktree removed") {
		t.Fatalf("merged: %s", out)
	}
	if e.gh.PRs[102].BaseRef != "develop" {
		t.Fatal("02 not retargeted")
	}
	if !strings.Contains(e.gh.PRs[103].Body, "#101 01 scaffold ✅ merged") {
		t.Fatalf("stack nav missing merged mark:\n%s", e.gh.PRs[103].Body)
	}
	if _, err := os.Stat(e.initiative().Worktree("01")); err == nil {
		t.Fatal("01 worktree still exists")
	}
	if out := e.run("merged", "01"); !strings.Contains(out, "merged 01") {
		t.Fatalf("merged not idempotent: %s", out)
	}
	if _, se, code := e.try("merged", "03"); code == 0 || !strings.Contains(se, "parent 02 isn't merged") {
		t.Fatalf("merged 03 before 02: %s", se)
	}

	// Worker commands run from inside a worktree resolve the initiative by cwd.
	wt4 := e.initiative().Worktree("04")
	if o, se, code := e.tryIn(wt4, "show", "04"); code != 0 || !strings.Contains(o, "fix-pending") {
		t.Fatalf("show from worktree: %s %s", o, se)
	}

	if out := e.run("validate"); !strings.HasPrefix(out, "ok") {
		t.Fatalf("final validate: %s", out)
	}
	if _, se, code := e.try("teardown"); code == 0 || !strings.Contains(se, "refusing") {
		t.Fatalf("teardown should refuse: %s", se)
	}
	log, _ := os.ReadFile(e.initiative().LogPath())
	for _, typ := range []string{"· init ·", "· add ·", "· assign ·", "· open ·", "· ready ·", "· review ·", "· feedback ·", "· blocked ·", "· propagate ·", "· merged ·"} {
		if !strings.Contains(string(log), typ) {
			t.Errorf("LOG.md missing %q", typ)
		}
	}
}

func TestValidationErrors(t *testing.T) {
	e := newEnv(t)
	e.run("init", "x")
	e.run("add", "01", "a", "--title", "A")
	e.run("add", "02", "b", "--title", "B", "--parent", "01")
	in := e.initiative()
	// Hand-corrupt: child started before parent, asymmetric conflict, bad status.
	p2 := in.PR("02")
	p2.Status = "in-progress"
	p2.ConflictsWith = []string{"01"}
	p2.Save()
	p1 := in.PR("01")
	p1.Status = "bogus"
	p1.Save()
	out, se, code := e.try("validate")
	if code == 0 {
		t.Fatalf("validate should fail: %s", out)
	}
	for _, want := range []string{"unknown status", "not symmetric", "hasn't passed director review"} {
		if !strings.Contains(se, want) {
			t.Errorf("missing %q in:\n%s", want, se)
		}
	}
	// mutating commands still succeed but report validation errors
	out, _, code = e.try("add", "03", "c", "--title", "C")
	if code != 0 || !strings.Contains(out, "⚠ validate") {
		t.Fatalf("add with invalid plan: %d %s", code, out)
	}
}

func TestInitRequiresRemoteBase(t *testing.T) {
	e := newEnv(t)
	if _, se, code := e.try("init", "x", "--base", "nope"); code == 0 || !strings.Contains(se, "doesn't exist on origin") {
		t.Fatalf("init with missing base: %s", se)
	}
}

func TestResolutionMultiple(t *testing.T) {
	e := newEnv(t)
	e.run("init", "one")
	e.now = e.now.Add(time.Minute)
	e.run("init", "two")
	repoDir := filepath.Join(e.home, "dalton-sample_proj")
	if cur := plan.ReadCurrent(repoDir); !strings.HasSuffix(cur, "-two") {
		t.Fatalf("current = %s", cur)
	}
	plan.SetCurrent(repoDir, "")
	if _, se, code := e.try("next"); code == 0 || !strings.Contains(se, "multiple initiatives") {
		t.Fatalf("want ambiguity error: %s", se)
	}
	names, _ := plan.ListInitiatives(repoDir)
	e.run("use", names[0])
	if cur := plan.ReadCurrent(repoDir); cur != names[0] {
		t.Fatalf("use: current = %s", cur)
	}
	if out := e.run("next", "--initiative", names[1]); !strings.HasPrefix(out, "none") {
		t.Fatalf("--initiative: %s", out)
	}
}

func TestUsageAndHelp(t *testing.T) {
	e := newEnv(t)
	e.run("init", "u")
	for _, tc := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{}, 0, "Available Commands"},
		{[]string{"assign", "--help"}, 0, "--ignore-conflicts"},
		{[]string{"bogus"}, 2, "unknown command"},
		{[]string{"show"}, 2, "accepts 1 arg"},
		{[]string{"add", "01", "x"}, 2, `required flag(s) "title" not set`},
		{[]string{"assign", "01"}, 2, `required flag(s) "worker" not set`},
		{[]string{"next", "--nope"}, 2, "unknown flag: --nope"},
		{[]string{"board", "--port", "abc"}, 2, "invalid argument"},
		{[]string{"show", "99"}, 1, "no PR"},
	} {
		out, se, code := e.try(tc.args...)
		if code != tc.code || !strings.Contains(out+se, tc.want) {
			t.Errorf("%v: code %d (want %d), output:\n%s\n%s", tc.args, code, tc.code, out, se)
		}
	}
	// comma-separated and repeated --conflicts-with both work
	e.run("add", "01", "a", "--title", "A")
	e.run("add", "02", "b", "--title", "B")
	e.run("add", "03", "c", "--title", "C", "--conflicts-with", "01,02")
	e.run("add", "04", "d", "--title", "D", "--conflicts-with", "01", "--conflicts-with", "03")
	in := e.initiative()
	if got := strings.Join(in.PR("01").ConflictsWith, ","); got != "03,04" {
		t.Fatalf("01 conflicts: %s", got)
	}
	// flag state doesn't leak between runs on the same App
	a := e.app(e.repo)
	var o bytes.Buffer
	a.Stdout, a.Stderr = &o, &o
	a.Run([]string{"next", "--json"})
	o.Reset()
	a.Run([]string{"next"})
	if strings.HasPrefix(strings.TrimSpace(o.String()), "{") {
		t.Fatalf("--json leaked into next run: %s", o.String())
	}
}
