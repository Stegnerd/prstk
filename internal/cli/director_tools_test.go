package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/Stegnerd/prstk/internal/gh"
)

func TestFeedbackFromGH(t *testing.T) {
	e := openedPR(t)
	e.gh.FeedbackItems[101] = []gh.FeedbackItem{
		{Kind: "review", ID: 1, Author: "dalton", Body: "Looks fine.\n\n<!-- prstk:director -->\n", State: "COMMENTED", At: "2026-11-03T20:00:00Z"},
		{Kind: "review", ID: 2, Author: "dalton", Body: "", State: "COMMENTED", At: "2026-11-03T20:01:00Z"},
		{Kind: "inline", ID: 10, Author: "dalton", Body: "## not a heading\n```go\nx := 1\n```", Path: "main.go", Line: 3, At: "2026-11-03T20:01:00Z"},
		{Kind: "review", ID: 3, Author: "dalton", Body: "Please rename the package.", State: "CHANGES_REQUESTED", At: "2026-11-03T20:02:00Z"},
		{Kind: "comment", ID: 20, Author: "someone", Body: "drive-by", At: "2026-11-03T20:03:00Z"},
	}
	out := e.run("feedback", "01", "--from-gh", "--author", "dalton")
	if !strings.HasPrefix(out, "imported 2 item(s) from #101 into 01") {
		t.Fatalf("out: %s", out)
	}
	p := e.initiative().PR("01")
	spec := e.readFile(p.SpecPath())
	for _, want := range []string{
		"## Review: Human\n\n### 2026-11-03 14:15 · GitHub #101\n\n<!-- prstk:gh:inline:10 -->\n**`main.go:3`** · @dalton",
		"> ## not a heading\n> ```go\n> x := 1\n> ```",
		"<!-- prstk:gh:review:3 -->\n**review** (changes requested) · @dalton",
		"> Please rename the package.",
	} {
		if !strings.Contains(spec, want) {
			t.Fatalf("spec missing %q:\n%s", want, spec)
		}
	}
	for _, bad := range []string{"Looks fine.", "drive-by", "prstk:gh:review:2"} {
		if strings.Contains(spec, bad) {
			t.Fatalf("spec has %q:\n%s", bad, spec)
		}
	}
	if !strings.Contains(e.readFile(e.initiative().LogPath()), "· feedback · director\n01: imported 2 item(s) of Human's feedback from #101 into spec.md.") {
		t.Fatalf("LOG:\n%s", e.readFile(e.initiative().LogPath()))
	}

	// Re-running imports nothing; without --author the drive-by comes in.
	if out := e.run("feedback", "01", "--from-gh", "--author", "dalton"); out != "no new feedback on #101" {
		t.Fatalf("re-run: %s", out)
	}
	e.gh.FeedbackItems[101] = append(e.gh.FeedbackItems[101], gh.FeedbackItem{Kind: "comment", ID: 21, Author: "dalton", Body: "One more thing.", At: "2026-11-03T20:09:00Z"})
	if out := e.run("feedback", "01", "--from-gh"); !strings.HasPrefix(out, "imported 2 item(s)") {
		t.Fatalf("second import: %s", out)
	}
	spec = e.readFile(p.SpecPath())
	if strings.Count(spec, "## Review: Human") != 1 || strings.Count(spec, "### 2026-11-03 14:15 · GitHub #101") != 2 || !strings.HasSuffix(spec, "> One more thing.\n") {
		t.Fatalf("second import layout:\n%s", spec)
	}
}

func TestFeedbackFromFile(t *testing.T) {
	e := openedPR(t)
	f := e.writeTemp("fb.md", "# Rename\n\nUse `plan` not `pl`.\n")
	out := e.run("feedback", "01", "--file", f)
	if !strings.HasPrefix(out, "recorded Human's feedback on 01") || !strings.Contains(out, "prstk status 01 fix-pending") {
		t.Fatalf("out: %s", out)
	}
	spec := e.readFile(e.initiative().PR("01").SpecPath())
	if !strings.Contains(spec, "## Review: Human\n\n### 2026-11-03 14:15 · chat\n\n#### Rename\n\nUse `plan` not `pl`.\n") {
		t.Fatalf("spec:\n%s", spec)
	}
	for _, args := range [][]string{{"feedback", "01"}, {"feedback", "01", "--from-gh", "--file", f}, {"feedback", "01", "--file", f, "--author", "x"}} {
		if _, _, code := e.try(args...); code != 2 {
			t.Fatalf("%v: want usage error, got %d", args, code)
		}
	}
}

func TestReviewPostsDirectorMarkerAndFeedbackSkipsIt(t *testing.T) {
	e := openedPR(t)
	f := e.writeTemp("r.md", "Fine.\n")
	e.run("review", "01", "--file", f, "--verdict", "approved")
	posted := e.gh.Reviews[0]
	e.gh.FeedbackItems[101] = []gh.FeedbackItem{{Kind: "review", ID: 5, Author: "dalton", Body: posted.Body, State: "COMMENTED"}}
	if out := e.run("feedback", "01", "--from-gh"); out != "no new feedback on #101" {
		t.Fatalf("out: %s", out)
	}
}

func TestFeedbackSkipsUnmarkedDirectorRound(t *testing.T) {
	e := openedPR(t)
	f := e.writeTemp("r.md", "Director review, round 1: approved.\n\nNo blocking findings.\n")
	e.run("review", "01", "--file", f, "--verdict", "approved", "--no-post")
	e.gh.FeedbackItems[101] = []gh.FeedbackItem{{Kind: "review", ID: 6, Author: "dalton", Body: "Director review, round 1: approved.\n\nNo blocking findings.", State: "COMMENTED"}}
	if out := e.run("feedback", "01", "--from-gh"); out != "no new feedback on #101" {
		t.Fatalf("out: %s", out)
	}
}

func TestReadyTellsDirectorToStopWorker(t *testing.T) {
	e := openedPR(t)
	if out := e.run("ready", "01"); !strings.HasSuffix(out, "released w1 · stop teammate w1 now (TaskStop)") {
		t.Fatalf("out: %s", out)
	}
}

func TestSpawnPrompt(t *testing.T) {
	e := newEnv(t)
	e.run("init", "vis")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("add", "02", "db-layer", "--title", "DB layer", "--parent", "01", "--model", "opus")
	if _, se, code := e.try("spawn-prompt", "01"); code != 1 || !strings.Contains(se, "has no worker") {
		t.Fatalf("unassigned: %d %s", code, se)
	}
	e.run("assign", "01", "--worker", "w-01")
	in := e.initiative()
	out := e.run("spawn-prompt", "01")
	for _, want := range []string{
		"Spawn a teammate named `w-01` using the prstk-worker agent type. Use Sonnet.",
		"You own PR 01 (Scaffold) of initiative vis.",
		"Worktree: " + in.Worktree("01"),
		"Spec: " + in.PR("01").SpecPath(),
		"Notes: " + in.PR("01").NotesPath(),
		"Read the spec first.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Parent notes") || strings.Contains(out, "fix worker") {
		t.Fatalf("unexpected:\n%s", out)
	}

	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.run("ready", "01")
	out = e.run("spawn-prompt", "02", "--worker", "w-02")
	if !strings.Contains(out, "named `w-02`") || !strings.Contains(out, "Use Opus.") || !strings.Contains(out, "Parent notes: "+in.PR("01").NotesPath()+" (PR 01, awaiting-you)") {
		t.Fatalf("child:\n%s", out)
	}

	// A fix worker after reviewer feedback says why it exists.
	e.run("status", "01", "fix-pending")
	e.run("assign", "01", "--worker", "w-01b")
	if out := e.run("spawn-prompt", "01"); !strings.Contains(out, "fix worker for Human's review feedback") || !strings.Contains(out, "## Review: Human") {
		t.Fatalf("fix:\n%s", out)
	}
}

func TestSpawnPromptMergeConflictKeepsReason(t *testing.T) {
	e := openedPR(t)
	e.run("ready", "01")
	e.run("status", "01", "fix-pending")
	// Simulate propagate's conflict record.
	p := e.initiative().PR("01")
	p.BlockedReason = "merge conflict with 00: a.go, b.go"
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	e.run("assign", "01", "--worker", "w-fix")
	if got := e.initiative().PR("01").BlockedReason; got != "merge conflict with 00: a.go, b.go" {
		t.Fatalf("reason cleared on assign: %q", got)
	}
	out := e.run("spawn-prompt", "01")
	if !strings.Contains(out, "fix worker for a merge conflict (merge conflict with 00: a.go, b.go)") || !strings.Contains(out, "git merge --no-ff develop") {
		t.Fatalf("conflict:\n%s", out)
	}
	e.work("01", "fix.go", "package main\n")
	e.run("open", "01")
	if got := e.initiative().PR("01").BlockedReason; got != "" {
		t.Fatalf("reason kept after open: %q", got)
	}
}

func TestCheck(t *testing.T) {
	e := openedPR(t)
	// Advance develop after the PR branched: check must merge it in.
	os.WriteFile(e.repo+"/later.txt", []byte("x\n"), 0o644)
	git(t, e.repo, "add", ".")
	git(t, e.repo, "commit", "-m", "later on develop")
	git(t, e.repo, "push", "origin", "develop")

	before := git(t, e.repo, "worktree", "list")
	out := e.run("check", "01", "--cmd", "test -f main.go", "--cmd", "test -f later.txt")
	if !strings.Contains(out, "✓ merge origin/develop") || !strings.Contains(out, "✓ test -f later.txt") || !strings.Contains(out, "01 ok · 2 check(s) passed") {
		t.Fatalf("out: %s", out)
	}
	if after := git(t, e.repo, "worktree", "list"); after != before {
		t.Fatalf("throwaway worktree left behind:\n%s", after)
	}
	// The worker's branch is untouched.
	if strings.Contains(git(t, e.initiative().Worktree("01"), "log", "--oneline"), "later on develop") {
		t.Fatal("check merged into the worker's branch")
	}

	o, se, code := e.try("check", "01", "--cmd", "echo boom; false", "--cmd", "true")
	if code != 1 || !strings.Contains(o, "✗ echo boom; false") || !strings.Contains(o, "    boom") || !strings.Contains(se, "1 of 2 check(s) failed") {
		t.Fatalf("failing: %d\n%s\n%s", code, o, se)
	}
	if _, se, code := e.try("check", "01"); code != 1 || !strings.Contains(se, "no checks to run") {
		t.Fatalf("no checks: %d %s", code, se)
	}
}

func TestCheckReportsConflicts(t *testing.T) {
	e := openedPR(t)
	os.WriteFile(e.repo+"/main.go", []byte("package other\n"), 0o644)
	git(t, e.repo, "add", ".")
	git(t, e.repo, "commit", "-m", "conflicting main.go")
	git(t, e.repo, "push", "origin", "develop")
	o, se, code := e.try("check", "01", "--cmd", "true")
	if code != 1 || !strings.Contains(o, "✗ merge origin/develop: conflicts in main.go") || !strings.Contains(se, "doesn't merge cleanly") {
		t.Fatalf("%d\n%s\n%s", code, o, se)
	}
}

// stackOf2 opens 01 (#101) and its child 02 (#102, based on 01's branch).
func stackOf2(t *testing.T) *env {
	e := openedPR(t)
	e.run("add", "02", "db-layer", "--title", "DB layer", "--parent", "01")
	e.run("ready", "01")
	e.run("assign", "02", "--worker", "w2")
	e.work("02", "db.go", "package main\n")
	e.run("open", "02")
	e.run("ready", "02")
	return e
}

func TestStackNavShowsMergeAfter(t *testing.T) {
	e := stackOf2(t)
	if !strings.Contains(e.gh.PRs[102].Body, "**#102 02 db layer ← this PR** · ⏳ merge after #101") {
		t.Fatalf("body:\n%s", e.gh.PRs[102].Body)
	}
	if strings.Contains(e.gh.PRs[101].Body, "#101 01 scaffold · ⏳") {
		t.Fatalf("root has a hint:\n%s", e.gh.PRs[101].Body)
	}
}

func TestMergedChildIntoParentBranchFirst(t *testing.T) {
	e := stackOf2(t)
	parentBranch := e.initiative().PR("01").Branch
	e.gh.MergeAt(102, "2026-11-03T20:00:00Z") // merged into 01's branch
	if _, se, code := e.try("merged", "02"); code != 1 || !strings.Contains(se, "#102 was merged into "+parentBranch) || !strings.Contains(se, "Run merged 01 first") {
		t.Fatalf("child first: %d %s", code, se)
	}
	e.gh.MergeAt(101, "2026-11-03T20:01:00Z")
	e.run("merged", "01")
	out := e.run("merged", "02")
	if !strings.Contains(out, "carried to develop by #101") {
		t.Fatalf("out: %s", out)
	}
}

func TestMergedChildAfterParentIsLost(t *testing.T) {
	e := stackOf2(t)
	e.gh.MergeAt(101, "2026-11-03T20:00:00Z")
	// Simulate GitHub not retargeting: 02 still targets 01's branch.
	e.gh.MergeAt(102, "2026-11-03T20:05:00Z")
	e.gh.PRs[102].BaseRef = e.initiative().PR("01").Branch
	// merged 01 would retarget 02 if it were open; it's already merged.
	e.run("merged", "01")
	o, se, code := e.try("merged", "02")
	if code != 1 || !strings.Contains(o, "merged 02 (#102)") || !strings.Contains(se, "NOT in develop") {
		t.Fatalf("%d\n%s\n%s", code, o, se)
	}
	if e.status("02") != "merged" {
		t.Fatalf("status: %s", e.status("02"))
	}
	if !strings.Contains(e.readFile(e.initiative().LogPath()), "NOT in develop") {
		t.Fatal("warning not logged")
	}
}
