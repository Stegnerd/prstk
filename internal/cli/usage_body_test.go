package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// usageBodyEnv is an env whose director (session sess-1) has a transcript,
// and whose commands run as that director.
type usageBodyEnv struct {
	*env
	claude string
	base   map[string]int // body-edit counts already seen, by PR number
}

func (u *usageBodyEnv) do(args ...string) string {
	u.t.Helper()
	return u.runWith(directorEnv("sess-1"), u.claude, args...)
}

// tryDo is do without the exit-code check.
func (u *usageBodyEnv) tryDo(args ...string) (string, int) {
	var o, er bytes.Buffer
	a := u.app(u.repo)
	a.Stdout, a.Stderr = &o, &er
	a.Getenv = func(k string) string { return directorEnv("sess-1")[k] }
	a.ClaudeDir = u.claude
	code := a.Run(args)
	return er.String(), code
}

func (u *usageBodyEnv) transcripts(workerOut int) {
	u.t.Helper()
	now := u.now
	writeTranscript(u.t, filepath.Join(u.claude, "projects", "-repo", "sess-1.jsonl"),
		transcriptLine("m1", now.Add(-time.Minute), 100, "prstk init vis"),
		transcriptLine("m2", now.Add(time.Minute), 10, "prstk assign 01 --worker w-01"),
		transcriptLine("m3", now.Add(2*time.Minute), 20, "prstk open 01"),
	)
	writeTranscript(u.t, filepath.Join(u.claude, "projects", "-repo", "sess-1", "subagents", "agent-aw-01-0123456789abcdef.jsonl"),
		transcriptLine("w1", now.Add(3*time.Minute), workerOut),
	)
}

// rawBodyCalls counts all EditBody calls on PR n so far.
func (u *usageBodyEnv) rawBodyCalls(n string) int {
	c := 0
	for _, call := range u.gh.Calls {
		if call == "body #"+n {
			c++
		}
	}
	return c
}

// bodyCalls counts EditBody calls on PR n since the last mark.
func (u *usageBodyEnv) bodyCalls(n string) int { return u.rawBodyCalls(n) - u.base[n] }

func (u *usageBodyEnv) mark() {
	u.base = map[string]int{}
	for _, n := range []string{"101", "102"} {
		u.base[n] = u.rawBodyCalls(n)
	}
}

// openedWithUsage runs init/add/assign/open for PR 01 with transcripts in place.
func openedWithUsage(t *testing.T) *usageBodyEnv {
	u := &usageBodyEnv{env: newEnv(t), claude: t.TempDir()}
	u.transcripts(7)
	u.do("init", "vis")
	u.do("add", "01", "scaffold", "--title", "Scaffold")
	u.do("assign", "01", "--worker", "w-01")
	u.work("01", "main.go", "package main\n")
	u.do("open", "01")
	u.mark()
	return u
}

func TestOpenBodyHasNavAndUsage(t *testing.T) {
	u := openedWithUsage(t)
	body := u.gh.PRs[101].Body
	for _, want := range []string{"Implements 01.", "<!-- prstk:stack:begin -->", "<!-- prstk:stack:end -->",
		"<!-- prstk:usage:begin -->", "Token usage (est.):", "| Director |", "| Worker w-01 |", "| **Total** |",
		"<!-- prstk:usage:end -->"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "prstk:usage:begin") < strings.Index(body, "prstk:stack:end") {
		t.Errorf("usage block should follow the nav:\n%s", body)
	}
}

func TestOpenAgainUnchangedMakesNoEdit(t *testing.T) {
	u := openedWithUsage(t)
	u.do("open", "01")
	u.do("open", "01")
	if n := u.bodyCalls("101"); n != 0 {
		t.Errorf("unchanged body edited %d times", n)
	}
}

// The body is built inside the command, before regenerate refreshes
// usage.json, so the command refreshes the snapshot itself: new tokens show
// up in the same command's edit, in one EditBody call.
func TestBodyRefreshInOneEdit(t *testing.T) {
	u := openedWithUsage(t)
	before := u.gh.PRs[101].Body
	u.transcripts(9000)
	u.do("open", "01")
	if n := u.bodyCalls("101"); n != 1 {
		t.Fatalf("EditBody calls = %d, want 1", n)
	}
	after := u.gh.PRs[101].Body
	if after == before || !strings.Contains(after, "| Worker w-01 | 0 | 0 | 0 | 9.0k |") {
		t.Errorf("block not refreshed:\n%s", after)
	}
}

func TestHumanTextOutsideMarkersSurvives(t *testing.T) {
	u := openedWithUsage(t)
	u.gh.PRs[101].Body = "Human intro.\n\n" + u.gh.PRs[101].Body + "\nHuman footer.\n"
	u.transcripts(500)
	u.do("open", "01")
	body := u.gh.PRs[101].Body
	if !strings.HasPrefix(body, "Human intro.\n\nImplements 01.") || !strings.HasSuffix(body, "\nHuman footer.\n") || !strings.Contains(body, "| Worker w-01 | 0 | 0 | 0 | 500 |") {
		t.Errorf("body:\n%s", body)
	}
	if strings.Count(body, "prstk:usage:begin") != 1 || strings.Count(body, "prstk:stack:begin") != 1 {
		t.Errorf("blocks duplicated:\n%s", body)
	}
}

func TestMissingMarkersAppended(t *testing.T) {
	u := openedWithUsage(t)
	// An old body: nav only (opened before the usage block existed).
	b := u.gh.PRs[101].Body
	i, j := strings.Index(b, "<!-- prstk:usage:begin -->"), strings.Index(b, "<!-- prstk:usage:end -->")
	u.gh.PRs[101].Body = b[:i] + b[j+len("<!-- prstk:usage:end -->"):] + "\nhand-written tail\n"
	u.do("open", "01")
	body := u.gh.PRs[101].Body
	if !strings.HasPrefix(body, "Implements 01.") || !strings.Contains(body, "hand-written tail\n\n<!-- prstk:usage:begin -->") || !strings.HasSuffix(body, "<!-- prstk:usage:end -->\n") {
		t.Errorf("block not appended at the end:\n%s", body)
	}
	if n := u.bodyCalls("101"); n != 1 {
		t.Errorf("EditBody calls = %d", n)
	}
}

func TestUsagePublishOffRemovesBlock(t *testing.T) {
	u := openedWithUsage(t)
	u.gh.PRs[101].Body += "\nhuman tail\n"
	os.WriteFile(filepath.Join(u.initiative().RepoDir(), "config.yaml"), []byte("usage_publish: false\n"), 0o644)
	u.do("open", "01")
	body := u.gh.PRs[101].Body
	if strings.Contains(body, "prstk:usage") || strings.Contains(body, "Token usage") {
		t.Errorf("usage block still there:\n%s", body)
	}
	if !strings.Contains(body, "human tail") || !strings.Contains(body, "prstk:stack:end") {
		t.Errorf("other text lost:\n%s", body)
	}
	if n := u.bodyCalls("101"); n != 1 {
		t.Errorf("EditBody calls = %d", n)
	}
	// And a new PR gets no block at all while it's off.
	u.do("add", "02", "next", "--title", "Next")
	u.do("assign", "02", "--worker", "w-02")
	u.work("02", "b.go", "package main\n")
	u.do("open", "02")
	if strings.Contains(u.gh.PRs[102].Body, "prstk:usage") {
		t.Errorf("new PR has a usage block while off:\n%s", u.gh.PRs[102].Body)
	}
}

func TestNoUsageNoBlock(t *testing.T) {
	// No transcripts at all.
	u := &usageBodyEnv{env: newEnv(t), claude: t.TempDir()}
	u.do("init", "vis")
	u.do("add", "01", "scaffold", "--title", "Scaffold")
	u.do("assign", "01", "--worker", "w-01")
	u.work("01", "main.go", "package main\n")
	u.do("open", "01")
	if body := u.gh.PRs[101].Body; strings.Contains(body, "prstk:usage") || !strings.Contains(body, "prstk:stack:begin") {
		t.Errorf("body:\n%s", body)
	}
	// A previous block is left alone while publishing is on and a PR has no usage.
	u.gh.PRs[101].Body += "\n<!-- prstk:usage:begin -->\nold block\n<!-- prstk:usage:end -->\n"
	u.do("open", "01")
	if !strings.Contains(u.gh.PRs[101].Body, "old block") {
		t.Errorf("stale block removed:\n%s", u.gh.PRs[101].Body)
	}
}

func TestPartialCostBody(t *testing.T) {
	u := &usageBodyEnv{env: newEnv(t), claude: t.TempDir()}
	u.transcripts(7)
	// Make the worker's model unpriced.
	p := filepath.Join(u.claude, "projects", "-repo", "sess-1", "subagents", "agent-aw-01-0123456789abcdef.jsonl")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "claude-opus-5-5", "mystery-model-1")), 0o644)
	u.do("init", "vis")
	u.do("add", "01", "scaffold", "--title", "Scaffold")
	u.do("assign", "01", "--worker", "w-01")
	u.work("01", "main.go", "package main\n")
	u.do("open", "01")
	body := u.gh.PRs[101].Body
	if !strings.Contains(body, "(partial)</summary>") || !strings.Contains(body, "≥ $") {
		t.Errorf("partial cost not marked:\n%s", body)
	}
}

// ready and merged refresh bodies too (merged via updateStackNav).
func TestMergedRefreshesOtherBodiesInOneEdit(t *testing.T) {
	u := openedWithUsage(t)
	u.do("add", "02", "next", "--title", "Next", "--parent", "01")
	u.approveID("01")
	u.do("ready", "01")
	u.do("assign", "02", "--worker", "w-02")
	u.work("02", "b.go", "package main\n")
	u.do("open", "02")
	u.gh.MergeAt(101, "2026-11-03T20:00:00Z")
	u.gh.PRs[101].BaseRef = "develop"
	u.mark()
	u.transcripts(4242)
	writeTranscript(t, filepath.Join(u.claude, "projects", "-repo", "sess-1", "subagents", "agent-aw-02-0123456789abcdef.jsonl"),
		transcriptLine("w2", u.now.Add(4*time.Minute), 321))
	u.do("merged", "01")
	if n := u.bodyCalls("102"); n != 1 {
		t.Errorf("102 EditBody calls = %d, want 1", n)
	}
	if body := u.gh.PRs[102].Body; !strings.Contains(body, "| Worker w-02 | 0 | 0 | 0 | 321 |") {
		t.Errorf("102 body:\n%s", body)
	}
	// The merged PR's own body isn't edited again.
	if n := u.bodyCalls("101"); n > 1 {
		t.Errorf("101 edited %d times", n)
	}
}

func TestReadyRefreshesBodyInOneEdit(t *testing.T) {
	u := openedWithUsage(t)
	u.approveID("01")
	u.transcripts(8888)
	u.do("ready", "01")
	if n := u.bodyCalls("101"); n != 1 {
		t.Errorf("EditBody calls = %d, want 1", n)
	}
	if body := u.gh.PRs[101].Body; !strings.Contains(body, "| Worker w-01 | 0 | 0 | 0 | 8.9k |") {
		t.Errorf("body:\n%s", body)
	}
}

// Body-edit failures: open reports them (non-zero, state already saved, as the
// nav always did); ready only warns, because it has already happened.
func TestBodyEditFailureHandling(t *testing.T) {
	u := openedWithUsage(t)
	u.transcripts(1234)
	u.gh.FailOn["EditBody"] = true
	if se, code := u.tryDo("open", "01"); code == 0 || !strings.Contains(se, "stack nav update") {
		t.Fatalf("open: code %d, %s", code, se)
	}
	u.approveID("01")
	u.transcripts(2345)
	u.gh.FailOn["EditBody"] = true
	out := u.do("ready", "01")
	if !strings.Contains(out, "⚠ stack nav update") || u.status("01") != "awaiting-you" {
		t.Errorf("ready: %s (status %s)", out, u.status("01"))
	}
}
