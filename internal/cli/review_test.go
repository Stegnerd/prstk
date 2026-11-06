package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (e *env) readFile(path string) string {
	e.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) writeTemp(name, content string) string {
	e.t.Helper()
	p := filepath.Join(e.t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func frontmatter(s string) string {
	i := strings.Index(s[4:], "\n---\n")
	return s[:4+i+5]
}

// approveID records a plain director approval for id without posting it, so
// ready's verdict gate passes.
func (e *env) approveID(id string) {
	e.t.Helper()
	e.run("review", id, "--file", e.writeTemp("approve-"+id+".md", "Looks good.\n"), "--verdict", "approved", "--no-post")
}

// openedPR returns an env with initiative + PR 01 opened as #101.
func openedPR(t *testing.T) *env {
	e := newEnv(t)
	e.run("init", "vis")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("assign", "01", "--worker", "w1")
	e.work("01", "main.go", "package main\n")
	e.run("open", "01")
	return e
}

func TestLogTypes(t *testing.T) {
	e := newEnv(t)
	e.run("init", "vis")
	for _, typ := range []string{"review", "feedback", "replan"} {
		out := e.run("log", typ, "hello", "world")
		if out != "logged "+typ+" · 2026-11-03 14:15" {
			t.Fatalf("out: %q", out)
		}
		if got := e.readFile(e.initiative().LogPath()); !strings.Contains(got, "## 2026-11-03 14:15 · "+typ+" · director\nhello world\n") {
			t.Fatalf("LOG.md:\n%s", got)
		}
	}
	_, errOut, code := e.try("log", "bogus", "x")
	if code != 2 || !strings.Contains(errOut, "unknown log type") {
		t.Fatalf("invalid type: code %d, %s", code, errOut)
	}
	if _, _, code := e.try("log", "review"); code != 2 {
		t.Fatalf("no message source should be a usage error, got %d", code)
	}
}

func TestLogSources(t *testing.T) {
	e := newEnv(t)
	e.run("init", "vis")
	f := e.writeTemp("m.md", "line one\n\nline three\n\n")
	e.run("log", "replan", "--file", f)
	got := e.readFile(e.initiative().LogPath())
	if !strings.HasSuffix(got, "## 2026-11-03 14:15 · replan · director\nline one\n\nline three\n") {
		t.Fatalf("multi-line file not preserved:\n%s", got)
	}
	if _, _, code := e.try("log", "replan", "inline", "--file", f); code != 2 {
		t.Fatalf("both sources: code %d", code)
	}
	if _, _, code := e.try("log", "replan", "--file", "/nonexistent/x"); code != 1 {
		t.Fatalf("missing file: code %d", code)
	}
}

func TestReviewRounds(t *testing.T) {
	e := openedPR(t)
	spec := e.initiative().PR("01").SpecPath()
	fmBefore := frontmatter(e.readFile(spec))

	f1 := e.writeTemp("r1.md", "# Review\n\nTests are missing for the parser.\n\n- fix it\n")
	out := e.run("review", "01", "--file", f1, "--verdict", "changes-requested")
	if out != "review 01 round 1 · changes-requested · posted #101" {
		t.Fatalf("out: %q", out)
	}
	e.now = e.now.Add(time.Hour)
	f2 := e.writeTemp("r2.md", "All findings addressed.\n")
	out = e.run("review", "01", "--file", f2, "--verdict", "approved", "--summary", "looks good")
	if out != "review 01 round 2 · approved · posted #101" {
		t.Fatalf("out: %q", out)
	}

	if n := len(e.gh.Reviews); n != 2 || e.gh.Reviews[0].Kind != "review" || e.gh.Reviews[0].N != 101 ||
		e.gh.Reviews[0].Repo != "dalton/sample_proj" || e.gh.Reviews[0].Body != "# Review\n\nTests are missing for the parser.\n\n- fix it\n\n<!-- prstk:director -->\n" {
		t.Fatalf("gh reviews: %+v", e.gh.Reviews)
	}
	got := e.readFile(spec)
	if frontmatter(got) != fmBefore {
		t.Fatalf("frontmatter changed:\n%s", frontmatter(got))
	}
	if !strings.Contains(got, "## Review: Director\n\n### Round 1 (2026-11-03): changes requested\n\n#### Review\n\nTests are missing") ||
		!strings.Contains(got, "### Round 2 (2026-11-03): approved\n\nAll findings addressed.\n\n## Review: Human") {
		t.Fatalf("spec body:\n%s", got)
	}
	log := e.readFile(e.initiative().LogPath())
	if !strings.Contains(log, "· review · director\n01 round 1: changes-requested. Tests are missing for the parser.\n") ||
		!strings.Contains(log, "· review · director\n01 round 2: approved. looks good\n") {
		t.Fatalf("LOG.md:\n%s", log)
	}
	if e.status("01") != "director-review" {
		t.Fatalf("review must not change status, got %s", e.status("01"))
	}
}

func TestReviewNumberingWithoutHumanSection(t *testing.T) {
	e := openedPR(t)
	spec := e.initiative().PR("01").SpecPath()
	s := e.readFile(spec)
	i := strings.Index(s, "## Review: Human")
	if i < 0 {
		t.Fatalf("expected a human review section in:\n%s", s)
	}
	os.WriteFile(spec, []byte(strings.TrimRight(s[:i], "\n")+"\n"), 0o644)

	f := e.writeTemp("r.md", "ok\n")
	e.run("review", "01", "--file", f, "--verdict", "approved")
	out := e.run("review", "01", "--file", f, "--verdict", "approved")
	if !strings.Contains(out, "round 2") {
		t.Fatalf("out: %s", out)
	}
	got := e.readFile(spec)
	if !strings.HasSuffix(got, "### Round 1 (2026-11-03): approved\n\nok\n\n### Round 2 (2026-11-03): approved\n\nok\n") {
		t.Fatalf("spec:\n%s", got)
	}
}

// The human section's own rounds must not count, whatever the reviewer is
// called. "Dalton" is the legacy hard-coded name: back-compat fixture.
func TestReviewNumberingWithHumanSection(t *testing.T) {
	for _, name := range []string{"Human", "Dalton", "Ada"} {
		t.Run(name, func(t *testing.T) {
			e := openedPR(t)
			spec := e.initiative().PR("01").SpecPath()
			s := strings.Replace(e.readFile(spec), "## Review: Human", "## Review: "+name, 1) +
				"\n### Round 1 (2026-11-02)\n\nverbatim feedback\n"
			os.WriteFile(spec, []byte(s), 0o644)
			f := e.writeTemp("r.md", "ok\n")
			if out := e.run("review", "01", "--file", f, "--verdict", "approved"); !strings.Contains(out, "round 1") {
				t.Fatalf("the reviewer's rounds must not count: %s", out)
			}
			got := e.readFile(spec)
			if !strings.Contains(got, "### Round 1 (2026-11-03): approved\n\nok\n\n## Review: "+name+"\n\n### Round 1 (2026-11-02)\n\nverbatim feedback\n") {
				t.Fatalf("spec:\n%s", got)
			}
		})
	}
}

func TestAddWritesReviewerHeading(t *testing.T) {
	// default: no config.yaml
	e := newEnv(t)
	e.run("init", "vis")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	got := e.readFile(e.initiative().PR("01").SpecPath())
	if !strings.HasSuffix(got, "## Review: Director\n\n## Review: Human\n") {
		t.Fatalf("default spec:\n%s", got)
	}

	// configured reviewer
	e = newEnv(t)
	e.run("init", "vis")
	cfg := filepath.Join(e.home, "dalton-sample_proj", "config.yaml")
	if err := os.WriteFile(cfg, []byte("reviewer: Ada\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	got = e.readFile(e.initiative().PR("01").SpecPath())
	if !strings.HasSuffix(got, "## Review: Director\n\n## Review: Ada\n") || strings.Contains(got, "Human") {
		t.Fatalf("configured spec:\n%s", got)
	}
}

func TestReviewBeforePROpened(t *testing.T) {
	e := newEnv(t)
	e.run("init", "vis")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	f := e.writeTemp("r.md", "Spec looks fine.\n")
	out := e.run("review", "01", "--file", f, "--verdict", "approved")
	if out != "review 01 round 1 · approved · not posted" {
		t.Fatalf("out: %q", out)
	}
	if len(e.gh.Reviews) != 0 {
		t.Fatalf("must not post without a PR: %+v", e.gh.Reviews)
	}
	if got := e.readFile(e.initiative().PR("01").SpecPath()); !strings.Contains(got, "### Round 1 (2026-11-03): approved (before PR opened)\n") {
		t.Fatalf("spec:\n%s", got)
	}
}

func TestReviewNoPost(t *testing.T) {
	e := openedPR(t)
	f := e.writeTemp("r.md", "Fine.\n")
	out := e.run("review", "01", "--file", f, "--verdict", "approved", "--no-post")
	if out != "review 01 round 1 · approved · not posted" || len(e.gh.Reviews) != 0 {
		t.Fatalf("out %q, reviews %+v", out, e.gh.Reviews)
	}
}

func TestReviewFallbackToComment(t *testing.T) {
	e := openedPR(t)
	e.gh.FailOn["Review"] = true
	f := e.writeTemp("r.md", "Fine.\n")
	out := e.run("review", "01", "--file", f, "--verdict", "approved")
	if out != "review 01 round 1 · approved · posted #101" {
		t.Fatalf("out: %q", out)
	}
	if len(e.gh.Reviews) != 1 || e.gh.Reviews[0].Kind != "comment" {
		t.Fatalf("reviews: %+v", e.gh.Reviews)
	}
}

func TestReviewDoublePostFailureWritesNothing(t *testing.T) {
	e := openedPR(t)
	in := e.initiative()
	specBefore := e.readFile(in.PR("01").SpecPath())
	logBefore := e.readFile(in.LogPath())
	e.gh.FailOn["Review"], e.gh.FailOn["Comment"] = true, true
	f := e.writeTemp("r.md", "Fine.\n")
	_, errOut, code := e.try("review", "01", "--file", f, "--verdict", "approved")
	if code != 1 || !strings.Contains(errOut, "nothing recorded") {
		t.Fatalf("code %d, %s", code, errOut)
	}
	if e.readFile(in.PR("01").SpecPath()) != specBefore || e.readFile(in.LogPath()) != logBefore {
		t.Fatal("spec or LOG changed after a failed post")
	}
}

func TestReviewUsageErrors(t *testing.T) {
	e := openedPR(t)
	f := e.writeTemp("r.md", "Fine.\n")
	for name, args := range map[string][]string{
		"bad verdict":     {"review", "01", "--file", f, "--verdict", "meh"},
		"missing verdict": {"review", "01", "--file", f},
		"missing file":    {"review", "01", "--verdict", "approved"},
	} {
		if _, _, code := e.try(args...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
	if _, _, code := e.try("review", "99", "--file", f, "--verdict", "approved"); code != 1 {
		t.Errorf("unknown id: want exit 1")
	}
}

func TestReviewApprovedWithNits(t *testing.T) {
	e := openedPR(t)
	f := e.writeTemp("r.md", "Approved; two nits.\n\n- rename x\n- drop the debug print\n")
	out := e.run("review", "01", "--file", f, "--verdict", "approved-with-nits")
	if out != "review 01 round 1 · approved-with-nits · posted #101" {
		t.Fatalf("out: %q", out)
	}
	if len(e.gh.Reviews) != 1 || !strings.Contains(e.gh.Reviews[0].Body, "two nits") {
		t.Fatalf("reviews: %+v", e.gh.Reviews)
	}
	p := e.initiative().PR("01")
	if got := e.readFile(p.SpecPath()); !strings.Contains(got, "### Round 1 (2026-11-03): approved with nits\n\nApproved; two nits.") {
		t.Fatalf("spec:\n%s", got)
	}
	if !strings.Contains(e.readFile(e.initiative().LogPath()), "01 round 1: approved-with-nits. Approved; two nits.\n") {
		t.Fatal("LOG entry missing")
	}
	if p.Status != "director-review" || p.Worker != "w1" {
		t.Fatalf("nits must keep status and worker: %s %q", p.Status, p.Worker)
	}

	// Before the PR is opened the heading gets the usual suffix.
	e2 := newEnv(t)
	e2.run("init", "vis")
	e2.run("add", "01", "scaffold", "--title", "Scaffold")
	e2.run("review", "01", "--file", f, "--verdict", "approved-with-nits")
	if got := e2.readFile(e2.initiative().PR("01").SpecPath()); !strings.Contains(got, "### Round 1 (2026-11-03): approved with nits (before PR opened)\n") {
		t.Fatalf("spec:\n%s", got)
	}

	// The usage error names every verdict.
	if _, se, code := e.try("review", "01", "--file", f, "--verdict", "nits"); code != 2 ||
		!strings.Contains(se, "approved, approved-with-nits or changes-requested") {
		t.Fatalf("usage: %d %s", code, se)
	}
}

func TestReadyVerdictGate(t *testing.T) {
	refused := func(e *env, want string, args ...string) {
		t.Helper()
		_, se, code := e.try(append([]string{"ready", "01"}, args...)...)
		if code != 1 || !strings.Contains(se, want) {
			t.Fatalf("ready %v: want refusal containing %q, got %d %s", args, want, code, se)
		}
		if e.status("01") != "director-review" || e.gh.PRs[101].IsDraft != true {
			t.Fatal("refused ready changed state")
		}
	}

	// No rounds at all.
	e := openedPR(t)
	refused(e, "01 has no director review yet")

	// Changes requested.
	e.run("review", "01", "--file", e.writeTemp("c.md", "Fix the parser.\n"), "--verdict", "changes-requested", "--no-post")
	refused(e, "01's latest review (round 1) is changes-requested")

	// Nits: refused, and neither --skip-evidence nor an evidence-free PR gets around it.
	e.run("review", "01", "--file", e.writeTemp("n.md", "Two nits.\n"), "--verdict", "approved-with-nits", "--no-post")
	refused(e, "01's latest review (round 2) is approved-with-nits; once the worker has pushed the fixes, run prstk open 01 and prstk review 01 --verdict approved")
	refused(e, "approved-with-nits", "--skip-evidence", "just ship it")

	// The nits flow: fix, push via open, re-approve, ready.
	e.work("01", "nit.go", "package main\n")
	e.run("open", "01")
	if e.status("01") != "director-review" {
		t.Fatalf("open after nits: %s", e.status("01"))
	}
	refused(e, "approved-with-nits") // still the latest round
	e.approveID("01")
	e.run("ready", "01")
	if e.status("01") != "awaiting-you" || e.gh.PRs[101].IsDraft {
		t.Fatal("ready after re-approval didn't go through")
	}

	// Hand-edited heading: not a verdict prstk knows, so refused.
	e = openedPR(t)
	p := e.initiative().PR("01")
	if _, err := p.AppendDirectorRound(e.now, "looks fine to me", "x"); err != nil {
		t.Fatal(err)
	}
	refused(e, "01's latest review (round 1) isn't a recognized verdict")

	// Approval recorded before the PR opened still counts.
	e = newEnv(t)
	e.run("init", "vis")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("assign", "01", "--worker", "w1")
	e.work("01", "main.go", "package main\n")
	e.approveID("01")
	if !strings.Contains(e.readFile(e.initiative().PR("01").SpecPath()), "approved (before PR opened)") {
		t.Fatal("expected a before-opened approval")
	}
	e.run("open", "01")
	e.run("ready", "01")
}

func TestReadyRefusesUnpushedCommits(t *testing.T) {
	e := openedPR(t)
	e.approveID("01")
	// A commit after the approval that open never pushed.
	e.work("01", "late.go", "package main\n")
	_, se, code := e.try("ready", "01", "--skip-evidence", "irrelevant")
	if code != 1 || !strings.Contains(se, "commits not on origin/prstk/vis/01-scaffold") || !strings.Contains(se, "run prstk open 01") {
		t.Fatalf("unpushed: %d %s", code, se)
	}
	if e.status("01") != "director-review" {
		t.Fatal("refused ready changed status")
	}
	e.run("open", "01")
	e.approveID("01")
	e.run("ready", "01")
}
