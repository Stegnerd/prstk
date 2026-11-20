package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/Stegnerd/prstk/internal/plan"
)

// markMerged records a merge the way `prstk merged` leaves it: status, PR
// number and the `merged` LOG entry.
func (e *env) markMerged(id string, prNumber int) {
	e.t.Helper()
	in := e.initiative()
	p := in.PR(id)
	p.PRNumber = prNumber
	p.SetStatus(plan.Merged, e.now)
	if err := p.Save(); err != nil {
		e.t.Fatal(err)
	}
	if err := in.AppendLog(e.now, "merged", "merged "+id); err != nil {
		e.t.Fatal(err)
	}
}

// markReady puts a PR where `prstk ready` leaves it: awaiting-you, with a PR
// number, no worker and a `ready` LOG entry.
func (e *env) markReady(id string, prNumber int) {
	e.t.Helper()
	in := e.initiative()
	p := in.PR(id)
	p.PRNumber, p.Worker = prNumber, ""
	p.SetStatus(plan.AwaitingYou, e.now)
	if err := p.Save(); err != nil {
		e.t.Fatal(err)
	}
	if err := in.AppendLog(e.now, "ready", "#"+id+" ready for review"); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) handoffFile() string { return e.initiative().HandoffPath() }

func (e *env) planned() *env {
	e.run("init", "pokemon-api")
	e.addPilot()
	return e
}

func TestHandoffAtPlanning(t *testing.T) {
	e := newEnv(t).planned()
	out := e.run("handoff")
	path := e.handoffFile()
	if !strings.HasPrefix(out, "wrote "+path+" (planning)\n") || !strings.Contains(out, "prstk feedback") || !strings.Contains(out, "prstk log") {
		t.Fatalf("output:\n%s", out)
	}
	md := e.readFile(path)
	for _, want := range []string{"# Handoff: ", "- Point: planning · written 2026-11-03 14:15", "max_workers 2", "| 01 | Scaffold | develop | ready (", "`prstk next` prints:", "- 01 scaffold · new · sonnet · critical path 5", handoffRecordText} {
		if !strings.Contains(md, want) {
			t.Fatalf("HANDOFF.md lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "## Director notes") {
		t.Fatalf("notes section without --file:\n%s", md)
	}
	log := e.readFile(e.initiative().LogPath())
	if !strings.Contains(log, "· handoff · prstk\npoint=planning · 6 open PRs\n") {
		t.Fatalf("LOG:\n%s", log)
	}
	// A second planning handoff, before anything starts, is fine.
	if out := e.run("handoff"); !strings.Contains(out, "(planning)") {
		t.Fatalf("second handoff: %s", out)
	}
}

const handoffRecordText = "The plan files are the record; this file is only a summary."

func TestHandoffNotesFile(t *testing.T) {
	e := newEnv(t).planned()
	notes := "Pending: which DB?\n\n## Promised\n- demo on Friday\n"
	out := e.run("handoff", "--file", e.writeTemp("notes.md", notes))
	if !strings.Contains(out, "(planning)") {
		t.Fatal(out)
	}
	md := e.readFile(e.handoffFile())
	if !strings.Contains(md, "\n## Director notes\n\nPending: which DB?\n\n## Promised\n- demo on Friday\n\n## Read first\n") {
		t.Fatalf("notes not copied verbatim:\n%s", md)
	}

	for name, content := range map[string]string{"empty": "", "blank": " \n\n"} {
		_, errOut, code := e.try("handoff", "--file", e.writeTemp(name+".md", content))
		if code != 1 || !strings.Contains(errOut, "prstk handoff:") || !strings.Contains(errOut, "is empty") {
			t.Fatalf("%s file: exit %d, %s", name, code, errOut)
		}
	}
	_, errOut, code := e.try("handoff", "--file", "/nonexistent/notes.md")
	if code != 1 || !strings.HasPrefix(errOut, "prstk handoff:") {
		t.Fatalf("missing file: exit %d, %s", code, errOut)
	}
	// The failed attempts left the earlier HANDOFF.md alone.
	if e.readFile(e.handoffFile()) != md {
		t.Fatal("HANDOFF.md changed by a failed handoff")
	}
}

func TestHandoffRefusesWhileWorkersAreLive(t *testing.T) {
	e := newEnv(t).planned()
	e.run("assign", "01", "--worker", "w1")
	_, errOut, code := e.try("handoff")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"prstk handoff: ", "01 (w1, in-progress)", plan.HandoffAllowed} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("stderr lacks %q:\n%s", want, errOut)
		}
	}
	if _, err := os.Stat(e.handoffFile()); err == nil {
		t.Fatal("HANDOFF.md written by a refused handoff")
	}
	if strings.Contains(e.readFile(e.initiative().LogPath()), "· handoff ·") {
		t.Fatal("a refused handoff was logged")
	}

	// Editing the status to look idle doesn't help: the worker is still recorded.
	e.run("status", "01", "abandoned")
	in := e.initiative()
	p := in.PR("01")
	p.Status = plan.Planned
	p.Worker = "w1"
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	_, errOut, code = e.try("handoff")
	if code != 1 || !strings.Contains(errOut, "01 (w1, planned)") {
		t.Fatalf("planned with a worker: exit %d, %s", code, errOut)
	}
}

func TestHandoffRefusesWhenValidationFails(t *testing.T) {
	e := newEnv(t).planned()
	in := e.initiative()
	p := in.PR("02")
	p.Parent = "99" // missing parent: a validation error
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	_, errOut, code := e.try("handoff")
	if code != 1 || !strings.Contains(errOut, "prstk validate") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestHandoffWhenEveryPRIsWaiting(t *testing.T) {
	e := newEnv(t)
	e.run("init", "pokemon-api")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("add", "02", "db-layer", "--title", "DB layer")
	e.markReady("01", 101)

	// 02 is still schedulable: prstk next would list it, so the handoff is refused.
	_, errOut, code := e.try("handoff")
	if code != 1 || !strings.Contains(errOut, "prstk next lists work (02)") || !strings.Contains(errOut, plan.HandoffAllowed) {
		t.Fatalf("with work left: exit %d, %s", code, errOut)
	}
	if _, err := os.Stat(e.handoffFile()); err == nil {
		t.Fatal("HANDOFF.md written by a refused handoff")
	}

	e.markReady("02", 102)
	out := e.run("handoff")
	if !strings.Contains(out, "(waiting)") {
		t.Fatalf("out: %s", out)
	}
	md := e.readFile(e.handoffFile())
	if !strings.Contains(md, "- Point: waiting") || !strings.Contains(md, "## Waiting on the human\n\n- 01 #101 Scaffold\n- 02 #102 DB layer\n") {
		t.Fatalf("HANDOFF.md:\n%s", md)
	}
	if !strings.Contains(e.readFile(e.initiative().LogPath()), "point=waiting · 2 open PRs") {
		t.Fatal("LOG lacks the waiting handoff entry")
	}

	// Nothing marked ready since: refused, whatever the file times say.
	_, errOut, code = e.try("handoff")
	if code != 1 || !strings.Contains(errOut, "no PR was marked ready since the last handoff") || !strings.Contains(errOut, plan.HandoffAllowed) {
		t.Fatalf("second handoff: exit %d, %s", code, errOut)
	}
	// A new PR reaching the human reopens the gate.
	e.run("add", "03", "more", "--title", "More")
	e.markReady("03", 103)
	if out := e.run("handoff"); !strings.Contains(out, "(waiting)") {
		t.Fatalf("after the next ready: %s", out)
	}
}

func TestHandoffRefusesFixPending(t *testing.T) {
	e := newEnv(t)
	e.run("init", "pokemon-api")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("add", "02", "db-layer", "--title", "DB layer")
	e.markReady("01", 101)
	e.markReady("02", 102)
	e.run("status", "02", "fix-pending", "--reason", "needs a rename")
	_, errOut, code := e.try("handoff")
	if code != 1 || !strings.Contains(errOut, "prstk next lists work (02)") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestHandoffRefusesFinishedInitiative(t *testing.T) {
	e := newEnv(t)
	e.run("init", "pokemon-api")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.markMerged("01", 101)
	_, errOut, code := e.try("handoff")
	if code != 1 || !strings.Contains(errOut, "nothing left to hand off") || !strings.Contains(errOut, "prstk teardown") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

func TestHandoffHasNoOverrideFlag(t *testing.T) {
	a := &App{}
	root := a.rootCmd()
	c, _, err := root.Find([]string{"handoff"})
	if err != nil || c.Name() != "handoff" {
		t.Fatalf("find: %v", err)
	}
	if c.Short == "" {
		t.Fatal("handoff has no Short")
	}
	var names []string
	c.Flags().VisitAll(func(f *pflag.Flag) { names = append(names, f.Name) })
	for _, n := range names {
		if n != "file" && n != "print" && n != "hook" && n != "help" && n != "initiative" {
			t.Fatalf("handoff defines flag --%s; there must be no way around the gate", n)
		}
		if strings.Contains(n, "force") || strings.HasPrefix(n, "skip") || strings.Contains(n, "ignore") || strings.Contains(n, "override") {
			t.Fatalf("override-style flag --%s", n)
		}
	}
	out, _, code := newEnv(t).try("--help")
	if code != 0 || !strings.Contains(out, "handoff") {
		t.Fatalf("prstk --help doesn't list handoff (exit %d):\n%s", code, out)
	}
}

func TestHandoffFlagCombinations(t *testing.T) {
	e := newEnv(t).planned()
	for _, args := range [][]string{
		{"handoff", "--print", "--hook"},
		{"handoff", "--print", "--file", "x.md"},
		{"handoff", "--hook", "--file", "x.md"},
	} {
		if _, _, code := e.try(args...); code != 2 {
			t.Fatalf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestHandoffPrint(t *testing.T) {
	e := newEnv(t).planned()
	logBefore := e.readFile(e.initiative().LogPath())
	planBefore := e.readFile(e.initiative().PlanPath())
	verboseBefore := e.readFile(e.initiative().VerbosePath())

	out := e.run("handoff", "--print")
	if !strings.Contains(out, "- Live state as of ") || !strings.Contains(out, "- Handoff: no handoff written") || !strings.Contains(out, "## PRs") {
		t.Fatalf("print without a handoff:\n%s", out)
	}
	if _, err := os.Stat(e.handoffFile()); err == nil {
		t.Fatal("--print wrote HANDOFF.md")
	}
	if e.readFile(e.initiative().LogPath()) != logBefore || e.readFile(e.initiative().PlanPath()) != planBefore {
		t.Fatal("--print changed LOG.md or PLAN.md")
	}
	if e.readFile(e.initiative().VerbosePath()) != verboseBefore {
		t.Fatal("--print wrote .prstk.log")
	}

	// With a handoff: the saved notes come back after the live state.
	e.run("handoff", "--file", e.writeTemp("n.md", "Ask the human about auth.\n"))
	e.run("assign", "01", "--worker", "w1") // state moved on since the notes
	out = e.run("handoff", "--print")
	for _, want := range []string{"| 01 | Scaffold | develop | in-progress (", "## Director notes\n\nAsk the human about auth.\n", "notes written 2026-11-03 14:15; 1 LOG entries since"} {
		if !strings.Contains(out, want) {
			t.Fatalf("print lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(planning)") || strings.Contains(out, "- Point:") {
		t.Fatalf("print replays the old point:\n%s", out)
	}
	// Nothing new since: no staleness count.
	e2 := newEnv(t).planned()
	e2.run("handoff")
	if out := e2.run("handoff", "--print"); !strings.Contains(out, "- Handoff: notes written 2026-11-03 14:15\n") || strings.Contains(out, "LOG entries since") {
		t.Fatalf("fresh handoff:\n%s", out)
	}
}

// ---- --hook ----

func (e *env) hook(stdin string, getenv map[string]string, cwd string) (string, string, int) {
	e.t.Helper()
	old := handoffStdin
	e.t.Cleanup(func() { handoffStdin = old })
	handoffStdin = func() io.Reader {
		if stdin == "<terminal>" {
			return nil
		}
		return strings.NewReader(stdin)
	}
	return e.setupRun(cwd, e.t.TempDir(), getenv, "handoff", "--hook")
}

func (e *env) sessions() []string { return e.initiative().Meta.Sessions }

func TestHandoffHookRecordsSessionAndPrints(t *testing.T) {
	e := newEnv(t).planned()
	e.run("handoff", "--file", e.writeTemp("n.md", "remember the schema\n"))
	in := `{"session_id":"sess-1","source":"clear","cwd":"` + e.repo + `","transcript_path":"/x","hook_event_name":"SessionStart","extra":{"a":1}}`
	out, errOut, code := e.hook(in, nil, e.repo)
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if !strings.HasPrefix(out, "prstk: resuming initiative pokemon-api from a handoff\n") || !strings.Contains(out, "remember the schema") || !strings.Contains(out, "## PRs") {
		t.Fatalf("stdout:\n%s", out)
	}
	if got := e.sessions(); len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("sessions: %v", got)
	}
	// Run twice: recorded once.
	if _, _, code := e.hook(in, nil, e.repo); code != 0 {
		t.Fatal("second run failed")
	}
	if got := e.sessions(); len(got) != 1 {
		t.Fatalf("sessions after a second run: %v", got)
	}
	// A different session is added.
	e.hook(`{"session_id":"sess-2"}`, nil, e.repo)
	if got := e.sessions(); len(got) != 2 || got[1] != "sess-2" {
		t.Fatalf("sessions: %v", got)
	}
}

func TestHandoffHookBadInput(t *testing.T) {
	e := newEnv(t).planned()
	for name, stdin := range map[string]string{
		"empty":     "",
		"garbage":   "\x00\x01 not json {",
		"array":     `[1,2,3]`,
		"truncated": `{"session_id":"s`,
		"terminal":  "<terminal>",
	} {
		out, errOut, code := e.hook(stdin, nil, e.repo)
		if code != 0 || errOut != "" {
			t.Fatalf("%s: exit %d, stderr %q", name, code, errOut)
		}
		if !strings.HasPrefix(out, "prstk: resuming initiative pokemon-api from a handoff\n") {
			t.Fatalf("%s: stdout:\n%s", name, out)
		}
		if got := e.sessions(); len(got) != 0 {
			t.Fatalf("%s: recorded a session from bad input: %v", name, got)
		}
	}
	// JSON without a session id prints and records nothing.
	if out, _, code := e.hook(`{"source":"clear"}`, nil, e.repo); code != 0 || out == "" || len(e.sessions()) != 0 {
		t.Fatalf("no session id: exit %d, sessions %v", code, e.sessions())
	}
}

func TestHandoffHookStaysSilent(t *testing.T) {
	// No initiative for this repo yet.
	e := newEnv(t)
	for _, stdin := range []string{`{"session_id":"s1"}`, "garbage", ""} {
		out, errOut, code := e.hook(stdin, nil, e.repo)
		if code != 0 || out != "" || errOut != "" {
			t.Fatalf("no initiative: exit %d, stdout %q, stderr %q", code, out, errOut)
		}
	}
	// Not even a git repo.
	if out, errOut, code := e.hook(`{"session_id":"s1"}`, nil, t.TempDir()); code != 0 || out != "" || errOut != "" {
		t.Fatalf("outside a repo: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	// A worker session prints nothing and records nothing.
	e.run("init", "pokemon-api")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	out, errOut, code := e.hook(`{"session_id":"w-sess"}`, map[string]string{"CLAUDE_CODE_AGENT": "prstk-worker"}, e.repo)
	if code != 0 || out != "" || errOut != "" || len(e.sessions()) != 0 {
		t.Fatalf("worker: exit %d, stdout %q, stderr %q, sessions %v", code, out, errOut, e.sessions())
	}
	// The same hook in the director's session does print.
	if out, _, _ := e.hook(`{"session_id":"d-sess"}`, map[string]string{"CLAUDE_CODE_AGENT": "prstk-director"}, e.repo); out == "" {
		t.Fatal("director session printed nothing")
	}
}

func TestHandoffHookNeverFails(t *testing.T) {
	e := newEnv(t).planned()
	// A broken PLAN.md must not fail the session start.
	if err := os.WriteFile(e.initiative().PlanPath(), []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, errOut, code := e.hook(`{"session_id":"s"}`, nil, e.repo)
	if code != 0 || out != "" || errOut != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestHandoffHookUsesCwdFromJSON(t *testing.T) {
	e := newEnv(t).planned()
	in := `{"session_id":"s","cwd":"` + e.repo + `"}`
	out, _, code := e.hook(in, nil, t.TempDir()) // process cwd is elsewhere
	if code != 0 || !strings.Contains(out, "resuming initiative pokemon-api") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if !strings.HasSuffix(strings.TrimSpace(strings.Join(e.sessions(), ",")), "s") {
		t.Fatalf("sessions %v", e.sessions())
	}
}
