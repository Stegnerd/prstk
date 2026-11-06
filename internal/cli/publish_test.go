package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/gh"
	"github.com/Stegnerd/prstk/internal/plan"
)

// readyEv returns an env with PR 01 (screenshots) opened as #101, approved,
// with befores and afters for steps 1 and 2 at HEAD: ready would pass.
func readyEv(t *testing.T) *env {
	t.Helper()
	e := evPR(t, "screenshots")
	e.evAdd("before", "a.png", "--caption", "Home | page")
	e.evAdd("before", "b.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a2.png"), "--caption", "Home | page")
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b2.png"))
	return e
}

const evBranch = "prstk-evidence"

// remoteGit runs git in the bare origin.
func (e *env) remoteGit(args ...string) string { return git(e.t, e.remote, args...) }

func (e *env) remoteTip() string { return e.remoteGit("rev-parse", evBranch) }

func (e *env) remoteFiles(rev string) []string {
	return strings.Split(e.remoteGit("ls-tree", "-r", "--name-only", rev), "\n")
}

func (e *env) evComments(n int) []string {
	var out []string
	for _, c := range e.gh.IssueComments[n] {
		out = append(out, c.Body)
	}
	return out
}

// repoState is HEAD, branch and the raw index of a checkout.
func repoState(t *testing.T, dir string) string {
	t.Helper()
	idx := git(t, dir, "rev-parse", "--path-format=absolute", "--git-path", "index")
	b, err := os.ReadFile(idx)
	if err != nil {
		t.Fatal(err)
	}
	return git(t, dir, "rev-parse", "HEAD") + " " + git(t, dir, "branch", "--show-current") + " " + string(b)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestEvidencePublishFirstIsOrphan(t *testing.T) {
	e := readyEv(t)
	in := e.initiative()
	wt := in.Worktree("01")
	mainBefore, wtBefore := repoState(t, e.repo), repoState(t, wt)

	out := e.run("evidence", "publish", "01")
	tip := e.remoteTip()
	if !strings.Contains(out, "01 evidence published to prstk-evidence@"+tip[:7]) || !strings.Contains(out, "comment 5001 created on #101") {
		t.Fatalf("out: %s", out)
	}
	// Orphan: no parent, no history shared with the base branch.
	if parents := strings.Fields(e.remoteGit("rev-list", "--parents", "-n", "1", tip)); len(parents) != 1 {
		t.Fatalf("first evidence commit has parents: %v", parents)
	}
	if err := exec.Command("git", "-C", e.remote, "merge-base", tip, "develop").Run(); err == nil {
		t.Fatal("evidence branch shares history with develop")
	}
	pre := in.Name + "/01/"
	files := e.remoteFiles(tip)
	for _, f := range []string{"before/a.png", "before/b.png", "after/a2.png", "after/b2.png"} {
		if !contains(files, pre+f) {
			t.Fatalf("missing %s in %v", pre+f, files)
		}
	}
	if len(files) != 4 {
		t.Fatalf("files: %v", files)
	}
	if got := e.remoteGit("show", tip+":"+pre+"after/a2.png"); got != "bytes of a2.png" {
		t.Fatalf("content: %q", got)
	}
	// Nothing about the checkouts changed.
	if repoState(t, e.repo) != mainBefore || repoState(t, wt) != wtBefore {
		t.Fatal("publish changed HEAD, branch or index of a checkout")
	}
	if st := git(t, e.repo, "status", "--porcelain"); st != "" {
		t.Fatalf("main checkout dirty: %s", st)
	}

	cs := e.evComments(101)
	if len(cs) != 1 {
		t.Fatalf("comments: %v", cs)
	}
	body := cs[0]
	head := e.head()
	url := "https://github.com/" + in.Meta.Repo + "/blob/" + tip + "/" + pre
	for _, want := range []string{
		EvidenceCommentMarker + "\n### Audit evidence\n",
		"**Level:** screenshots · **After captures at:** `" + head[:7] + "`",
		"| Step | Before | After |",
		"| 1 | ![Home \\| page](" + url + "before/a.png?raw=true)<br><sub>Home \\| page · `",
		"`</sub> | ![Home \\| page](" + url + "after/a2.png?raw=true)<br><sub>Home \\| page · `" + head[:7] + "`</sub> |",
		"| 2 | ![b.png](" + url + "before/b.png?raw=true)",
		plan.DirectorMarker,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("comment missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "stale") {
		t.Fatalf("nothing is stale:\n%s", body)
	}
	if lg := e.logText(); !strings.Contains(lg, "· evidence · prstk\nevidence publish 01 → prstk-evidence@"+tip[:7]+" (comment #5001 created)") {
		t.Fatalf("LOG:\n%s", lg)
	}
	// feedback --from-gh skips it (it carries the director marker).
	if fresh := newFeedback([]gh.FeedbackItem{{Kind: "comment", ID: 5001, Body: body}}, "", ""); len(fresh) != 0 {
		t.Fatalf("feedback would import the evidence comment: %v", fresh)
	}
}

func TestEvidencePublishIdempotentAndRepublish(t *testing.T) {
	e := readyEv(t)
	pre := e.initiative().Name + "/01/"
	e.run("evidence", "publish", "01")
	first := e.remoteTip()

	// No changes: no new commit, the same comment is edited.
	out := e.run("evidence", "publish", "01")
	if e.remoteTip() != first || !strings.Contains(out, "(unchanged)") || !strings.Contains(out, "comment 5001 updated") {
		t.Fatalf("republish: %s (tip %s, was %s)", out, e.remoteTip(), first)
	}
	if n := e.remoteGit("rev-list", "--count", evBranch); n != "1" {
		t.Fatalf("commits: %s", n)
	}
	if len(e.evComments(101)) != 1 || !contains(e.gh.Calls, "comment-edit #101 5001") {
		t.Fatalf("comments %v calls %v", e.evComments(101), e.gh.Calls)
	}
	if lg := e.logText(); !strings.Contains(lg, "evidence publish 01 → prstk-evidence@"+first[:7]+" (comment #5001 updated)") {
		t.Fatalf("LOG:\n%s", lg)
	}

	// A re-capture: a new commit on top; the old commit's files stay reachable.
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a3.png"), "--step", "1")
	e.run("evidence", "publish", "01")
	second := e.remoteTip()
	if second == first || e.remoteGit("rev-parse", second+"^") != first {
		t.Fatalf("second publish should sit on the first: %s", e.remoteGit("log", "--oneline", evBranch))
	}
	files := e.remoteFiles(second)
	if contains(files, pre+"after/a2.png") || !contains(files, pre+"after/a3.png") {
		t.Fatalf("tip files: %v", files)
	}
	e.remoteGit("cat-file", "-e", first+":"+pre+"after/a2.png")
	cs := e.evComments(101)
	if len(cs) != 1 || !strings.Contains(cs[0], "/blob/"+second+"/"+pre+"after/a3.png") || strings.Contains(cs[0], "/blob/"+first+"/") {
		t.Fatalf("comment not repointed:\n%v", cs)
	}

	// Other comments on the PR are left alone, and the marker one is found among them.
	e.gh.IssueComments[101] = append([]gh.FakeComment{{ID: 1, Body: "hello"}}, e.gh.IssueComments[101]...)
	e.run("evidence", "publish", "01")
	if cs := e.evComments(101); len(cs) != 2 || cs[0] != "hello" {
		t.Fatalf("comments: %v", cs)
	}
}

func TestEvidencePublishKeepsOtherPRs(t *testing.T) {
	e := readyEv(t)
	e.run("add", "02", "other", "--title", "Other", "--evidence", "screenshots")
	e.run("assign", "02", "--worker", "w2")
	e.run("evidence", "add", "02", "--phase", "before", "--file", e.shot("x.png"))
	e.work("02", "other.go", "package other\n")
	e.run("open", "02")

	e.run("evidence", "publish", "01")
	first := e.remoteTip()
	out := e.run("evidence", "publish", "02")
	if !strings.Contains(out, "on #102") {
		t.Fatalf("out: %s", out)
	}
	tip := e.remoteTip()
	if e.remoteGit("rev-parse", tip+"^") != first {
		t.Fatal("02's commit should sit on 01's")
	}
	name := e.initiative().Name
	files := e.remoteFiles(tip)
	if len(files) != 5 || !contains(files, name+"/01/after/b2.png") || !contains(files, name+"/02/before/x.png") {
		t.Fatalf("files: %v", files)
	}
	if body := e.evComments(102)[0]; !strings.Contains(body, "No after captures yet.") {
		t.Fatalf("02 comment:\n%s", body)
	}
	// Republishing 01 doesn't drop 02.
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("b3.png"), "--step", "2")
	e.run("evidence", "publish", "01")
	if files := e.remoteFiles(evBranch); !contains(files, name+"/02/before/x.png") || !contains(files, name+"/01/after/b3.png") {
		t.Fatalf("files: %v", files)
	}
}

// pushOther pushes a commit with file to the evidence branch from another
// clone, as a concurrent publisher would.
func (e *env) pushOther(file string) string {
	t := e.t
	t.Helper()
	dir := filepath.Join(t.TempDir(), "other")
	git(t, filepath.Dir(dir), "clone", "-q", e.remote, dir)
	if git(t, dir, "ls-remote", "origin", "refs/heads/"+evBranch) != "" {
		git(t, dir, "fetch", "-q", "origin", evBranch)
		git(t, dir, "checkout", "-q", "-b", evBranch, "FETCH_HEAD")
	} else {
		git(t, dir, "checkout", "-q", "--orphan", evBranch)
		git(t, dir, "rm", "-rqf", ".")
	}
	os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755)
	os.WriteFile(filepath.Join(dir, file), []byte("other"), 0o644)
	git(t, dir, "add", file)
	git(t, dir, "commit", "-qm", "other publish")
	git(t, dir, "push", "-q", "origin", evBranch)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestEvidencePublishRetriesRejectedPushOnce(t *testing.T) {
	e := readyEv(t)
	e.run("evidence", "publish", "01") // the branch exists
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a3.png"), "--step", "1")

	var other string
	beforeEvidencePush = func(attempt int) {
		if attempt == 0 {
			other = e.pushOther("other-init/09/before/z.png")
		}
	}
	defer func() { beforeEvidencePush = func(int) {} }()
	e.run("evidence", "publish", "01")
	tip := e.remoteTip()
	if e.remoteGit("rev-parse", tip+"^") != other {
		t.Fatalf("not rebuilt on the other publish: %s", e.remoteGit("log", "--oneline", evBranch))
	}
	files := e.remoteFiles(tip)
	if !contains(files, "other-init/09/before/z.png") || !contains(files, e.initiative().Name+"/01/after/a3.png") {
		t.Fatalf("files: %v", files)
	}

	// Rejected twice: fail clearly, and never force.
	e.run("evidence", "add", "01", "--phase", "after", "--file", e.shot("a4.png"), "--step", "1")
	beforeEvidencePush = func(int) {
		other = e.pushOther("other-init/09/before/z" + time.Now().Format("150405.000000000") + ".png")
	}
	_, se, code := e.try("evidence", "publish", "01")
	if code != 1 || !strings.Contains(se, "rejected again after rebuilding") {
		t.Fatalf("double rejection: %d %s", code, se)
	}
	if e.remoteTip() != other {
		t.Fatal("the other publisher's commit was overwritten")
	}
}

func TestEvidencePublishFirstPushRace(t *testing.T) {
	// Someone creates the branch between our ls-remote and push.
	e := readyEv(t)
	var other string
	beforeEvidencePush = func(attempt int) {
		if attempt == 0 {
			other = e.pushOther("x/1/before/y.png")
		}
	}
	defer func() { beforeEvidencePush = func(int) {} }()
	e.run("evidence", "publish", "01")
	if e.remoteGit("rev-parse", evBranch+"^") != other {
		t.Fatal("first publish not rebuilt on the racing branch")
	}
}

func TestEvidencePublishRefusals(t *testing.T) {
	// No PR yet.
	e := evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	if _, se, code := e.try("evidence", "publish", "01"); code != 1 || !strings.Contains(se, "no GitHub PR yet") {
		t.Fatalf("no PR: %d %s", code, se)
	}
	// No evidence level.
	e = evPR(t, "")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	if _, se, code := e.try("evidence", "publish", "01"); code != 1 || !strings.Contains(se, "no audit evidence level") {
		t.Fatalf("no level: %d %s", code, se)
	}
	// Files over the size limit.
	e = readyEv(t)
	maxPublishBytes = 10
	defer func() { maxPublishBytes = 50 << 20 }()
	_, se, code := e.try("evidence", "publish", "01")
	if code != 1 || !strings.Contains(se, "a.png is 14 bytes, over the 10-byte publish limit") {
		t.Fatalf("size: %d %s", code, se)
	}
	if git(t, e.repo, "ls-remote", "origin", evBranch) != "" || len(e.evComments(101)) != 0 {
		t.Fatal("refused publish pushed or commented")
	}
	// ready fails too (re-runnable) and leaves the PR in director-review.
	if _, se, code := e.try("ready", "01"); code != 1 || !strings.Contains(se, "publishing audit evidence failed") || !strings.Contains(se, "--no-publish") {
		t.Fatalf("ready: %d %s", code, se)
	}
	if e.status("01") != plan.DirectorReview || e.gh.PRs[101].IsDraft == false {
		t.Fatal("failed ready changed state")
	}
}

func TestReadyPublishes(t *testing.T) {
	e := readyEv(t)
	out := e.run("ready", "01")
	tip := e.remoteTip()
	if !strings.Contains(out, "evidence published (prstk-evidence@"+tip[:7]+")") || e.status("01") != plan.AwaitingYou {
		t.Fatalf("ready: %s", out)
	}
	if len(e.evComments(101)) != 1 || !strings.Contains(e.logText(), "evidence publish 01 → prstk-evidence@"+tip[:7]+" (comment #5001 created)") {
		t.Fatalf("comments %v\nLOG:\n%s", e.evComments(101), e.logText())
	}
	// Publish runs before gh pr ready.
	var iPub, iReady = -1, -1
	for i, c := range e.gh.Calls {
		switch c {
		case "comment-create #101 → 5001":
			iPub = i
		case "ready #101":
			iReady = i
		}
	}
	if iPub < 0 || iReady < iPub {
		t.Fatalf("calls: %v", e.gh.Calls)
	}
}

func TestReadyNoPublish(t *testing.T) {
	e := readyEv(t)
	out := e.run("ready", "01", "--no-publish")
	if !strings.Contains(out, "evidence not published (--no-publish)") || e.status("01") != plan.AwaitingYou {
		t.Fatalf("ready: %s", out)
	}
	if git(t, e.repo, "ls-remote", "origin", evBranch) != "" || len(e.evComments(101)) != 0 {
		t.Fatal("--no-publish published")
	}
	if !strings.Contains(e.logText(), "· evidence · prstk\n01 publish skipped at ready (--no-publish)") {
		t.Fatalf("LOG:\n%s", e.logText())
	}
}

func TestEvidencePublishDisabled(t *testing.T) {
	e := readyEv(t)
	in := e.initiative()
	os.WriteFile(filepath.Join(in.RepoDir(), "config.yaml"), []byte("evidence_publish: false\n"), 0o644)
	if _, se, code := e.try("evidence", "publish", "01"); code != 1 || !strings.Contains(se, "evidence_publish: false") {
		t.Fatalf("command: %d %s", code, se)
	}
	out := e.run("ready", "01")
	if strings.Contains(out, "evidence") || e.status("01") != plan.AwaitingYou {
		t.Fatalf("ready: %s", out)
	}
	if git(t, e.repo, "ls-remote", "origin", evBranch) != "" || len(e.evComments(101)) != 0 || strings.Contains(e.logText(), "publish") {
		t.Fatal("disabled publish still published")
	}
}

func TestEvidencePublishCustomBranch(t *testing.T) {
	e := readyEv(t)
	os.WriteFile(filepath.Join(e.initiative().RepoDir(), "config.yaml"), []byte("evidence_branch: audit/captures\n"), 0o644)
	e.run("evidence", "publish", "01")
	tip := e.remoteGit("rev-parse", "audit/captures")
	if !strings.Contains(e.evComments(101)[0], "[`audit/captures@"+tip[:7]+"`]") {
		t.Fatalf("comment:\n%s", e.evComments(101)[0])
	}
	os.WriteFile(filepath.Join(e.initiative().RepoDir(), "config.yaml"), []byte("evidence_branch: \"bad..name\"\n"), 0o644)
	if _, se, code := e.try("evidence", "publish", "01"); code != 1 || !strings.Contains(se, "not a valid branch name") {
		t.Fatalf("bad branch: %d %s", code, se)
	}
}

func TestReadySkipEvidencePublishesBefores(t *testing.T) {
	e := evPR(t, "screenshots")
	e.evAdd("before", "a.png")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	e.approve()
	e.run("ready", "01", "--skip-evidence", "copy-only change")
	cs := e.evComments(101)
	if len(cs) != 1 || !strings.Contains(cs[0], "No after captures (skipped: copy-only change).") || !strings.Contains(cs[0], "| 1 | ![a.png](") || !strings.Contains(cs[0], " | — |") {
		t.Fatalf("comment: %v", cs)
	}
	if strings.Contains(cs[0], "After captures at") {
		t.Fatalf("no after commit to show:\n%s", cs[0])
	}

	// --skip-evidence with no captures at all: nothing to publish, not an error.
	e = evPR(t, "screenshots")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01", "--skip-evidence", "new screen")
	e.approve()
	out := e.run("ready", "01", "--skip-evidence", "new screen")
	if strings.Contains(out, "published") || len(e.evComments(101)) != 0 {
		t.Fatalf("ready: %s", out)
	}
}

func TestEvidenceCommentStaleAndVideo(t *testing.T) {
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	before := plan.EvidenceItem{Phase: plan.PhaseBefore, Step: 1, File: "flow.webm", Kind: plan.KindVideo, Caption: "Flow", Commit: "1111111aaaa"}
	after := plan.EvidenceItem{Phase: plan.PhaseAfter, Step: 1, File: "flow.mp4", Kind: plan.KindVideo, Caption: "Flow", Commit: "2222222bbbb", CapturedAt: at}
	gif := plan.EvidenceItem{Phase: plan.PhaseAfter, Step: 2, File: "spin.gif", Kind: plan.KindImage, Commit: "3333333cccc", CapturedAt: at.Add(time.Minute)}
	body := evidenceComment(evidenceCommentOpts{
		Repo: "o/r", Level: "video", Branch: "prstk-evidence", SHA: "abcdef0123456", Prefix: "init/01",
		Pairs: []plan.EvidencePair{{Step: 1, Before: &before, After: &after}, {Step: 2, After: &gif}},
		Head:  "3333333cccc",
	})
	for _, want := range []string{
		"**Level:** video · **After captures at:** `3333333` · **Files:** [`prstk-evidence@abcdef0`](https://github.com/o/r/tree/abcdef0123456/init/01)",
		"| 1 | [▶ Flow (video)](https://github.com/o/r/blob/abcdef0123456/init/01/before/flow.webm?raw=true)<br><sub>Flow · `1111111`</sub> | [▶ Flow (video)](https://github.com/o/r/blob/abcdef0123456/init/01/after/flow.mp4?raw=true)<br><sub>Flow · `2222222` (stale)</sub> |",
		"| 2 | — | ![spin.gif](https://github.com/o/r/blob/abcdef0123456/init/01/after/spin.gif?raw=true)<br><sub>`3333333`</sub> |",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
}
