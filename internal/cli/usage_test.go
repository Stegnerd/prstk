package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/usage"
)

// runWith runs prstk from the main repo with the given environment and
// Claude dir, failing the test on a non-zero exit.
func (e *env) runWith(vars map[string]string, claude string, args ...string) string {
	e.t.Helper()
	var o, er bytes.Buffer
	a := e.app(e.repo)
	a.Stdout, a.Stderr = &o, &er
	a.Getenv = func(k string) string { return vars[k] }
	a.ClaudeDir = claude
	if code := a.Run(args); code != 0 {
		e.t.Fatalf("prstk %v: exit %d\nstdout: %s\nstderr: %s", args, code, o.String(), er.String())
	}
	return strings.TrimSpace(o.String())
}

func directorEnv(session string) map[string]string {
	return map[string]string{"CLAUDE_CODE_SESSION_ID": session, "CLAUDE_CODE_AGENT": "prstk-director"}
}

func TestSessionRecording(t *testing.T) {
	e := newEnv(t)
	claude := t.TempDir()
	e.runWith(directorEnv("sess-1"), claude, "init", "tiny")
	if got := e.initiative().Meta.Sessions; len(got) != 1 || got[0] != "sess-1" {
		t.Fatalf("after init: sessions = %v", got)
	}
	// Recorded once, however many commands run.
	e.runWith(directorEnv("sess-1"), claude, "add", "01", "one", "--title", "One")
	e.runWith(directorEnv("sess-1"), claude, "board")
	// A worker, and no session at all: nothing recorded.
	e.runWith(map[string]string{"CLAUDE_CODE_SESSION_ID": "worker-sess", "CLAUDE_CODE_AGENT": "prstk-worker"}, claude, "board")
	e.run("board")
	if got := e.initiative().Meta.Sessions; len(got) != 1 {
		t.Fatalf("sessions = %v, want just sess-1", got)
	}
	// A restarted director: its new session is added by a mutating command.
	e.runWith(directorEnv("sess-2"), claude, "add", "02", "two", "--title", "Two")
	in := e.initiative()
	if got := strings.Join(in.Meta.Sessions, ","); got != "sess-1,sess-2" {
		t.Fatalf("sessions = %s", got)
	}
	b, _ := os.ReadFile(in.PlanPath())
	if !strings.Contains(string(b), `sessions: ["sess-1", "sess-2"]`) {
		t.Errorf("PLAN.md frontmatter:\n%s", b)
	}
}

func TestSessionNotRecordedWithoutEnv(t *testing.T) {
	e := newEnv(t)
	e.run("init", "tiny")
	before, _ := os.ReadFile(e.initiative().PlanPath())
	e.run("add", "01", "one", "--title", "One")
	in := e.initiative()
	if len(in.Meta.Sessions) != 0 {
		t.Fatalf("sessions = %v", in.Meta.Sessions)
	}
	after, _ := os.ReadFile(in.PlanPath())
	if strings.Contains(string(after), "sessions:") || !strings.HasPrefix(string(after), string(before[:strings.Index(string(before), "\n---\n")])) {
		t.Errorf("PLAN.md frontmatter changed:\n%s", after)
	}
}

// transcriptLine is a minimal assistant line running cmds through Bash.
func transcriptLine(id string, at time.Time, out int, cmds ...string) string {
	content := []any{}
	for i, c := range cmds {
		content = append(content, map[string]any{"type": "tool_use", "id": fmt.Sprintf("%s-%d", id, i), "name": "Bash", "input": map[string]any{"command": c}})
	}
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "timestamp": at.UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"id": id, "model": "claude-opus-5-5", "content": content,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": out}},
	})
	return string(b)
}

func writeTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegenerateWritesUsage(t *testing.T) {
	e := newEnv(t)
	claude := t.TempDir()
	now := e.now
	writeTranscript(t, filepath.Join(claude, "projects", "-repo", "sess-1.jsonl"),
		transcriptLine("m1", now.Add(-time.Minute), 100, "prstk init tiny"),
		transcriptLine("m2", now.Add(time.Minute), 10),
		transcriptLine("m3", now.Add(2*time.Minute), 20, "prstk show 01"),
	)
	writeTranscript(t, filepath.Join(claude, "projects", "-repo", "sess-1", "subagents", "agent-aw-01-0123456789abcdef.jsonl"),
		transcriptLine("w1", now.Add(3*time.Minute), 7),
	)
	e.runWith(directorEnv("sess-1"), claude, "init", "tiny")
	e.runWith(directorEnv("sess-1"), claude, "add", "01", "one", "--title", "One")
	e.runWith(directorEnv("sess-1"), claude, "assign", "01", "--worker", "w-01")

	in := e.initiative()
	r, err := usage.Load(in.UsagePath())
	if err != nil || r == nil {
		t.Fatalf("usage.json: %v %v", r, err)
	}
	if got := r.Overhead.Tokens.Output; got != 100 {
		t.Errorf("overhead output = %d, want 100", got)
	}
	if got := r.PRs["01"].Director.Tokens.Output; got != 30 {
		t.Errorf("01 director output = %d, want 30", got)
	}
	if got := r.PRs["01"].Workers["w-01"].Tokens.Output; got != 7 {
		t.Errorf("01 w-01 output = %d, want 7", got)
	}
	if r.Total.CostUSD <= 0 || r.Total.Tokens.Output != 137 {
		t.Errorf("total = %+v", r.Total)
	}
}

func TestRegenerateWithoutClaudeDir(t *testing.T) {
	e := newEnv(t)
	e.run("init", "tiny") // e.app's UserHome has no .claude
	r, err := usage.Load(e.initiative().UsagePath())
	if err != nil || r == nil || len(r.Warnings) == 0 || r.Total.Messages != 0 {
		t.Fatalf("usage.json = %+v, %v", r, err)
	}
}

func TestUsageErrorDoesNotFailCommand(t *testing.T) {
	e := newEnv(t)
	e.run("init", "tiny")
	in := e.initiative()
	const junk = "{not json"
	if err := os.WriteFile(in.UsagePath(), []byte(junk), 0o644); err != nil {
		t.Fatal(err)
	}
	e.run("add", "01", "one", "--title", "One")
	if b, _ := os.ReadFile(in.UsagePath()); string(b) != junk {
		t.Errorf("usage.json was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(in.VerbosePath()); !strings.Contains(string(b), "usage: ") {
		t.Errorf("verbose log lacks the usage error:\n%s", b)
	}
}

func TestTeardownArchivesUsage(t *testing.T) {
	e, in := teardownSetup(t)
	claude := t.TempDir()
	now := e.now
	writeTranscript(t, filepath.Join(claude, "projects", "-repo", "sess-1.jsonl"),
		transcriptLine("m1", now.Add(time.Minute), 10, "prstk show 01"),
		transcriptLine("m2", now.Add(2*time.Minute), 20, "prstk teardown"),
		transcriptLine("m3", now.Add(3*time.Minute), 40), // after teardown
	)
	e.runWith(directorEnv("sess-1"), claude, "board")
	e.runWith(directorEnv("sess-1"), claude, "teardown")

	r, err := usage.Load(filepath.Join(plan.ArchivePath(in.RepoDir(), in.Name), "usage.json"))
	if err != nil || r == nil {
		t.Fatalf("archived usage.json: %v %v", r, err)
	}
	if r.PRs["01"].Director.Tokens.Output != 10 || r.Overhead.Tokens.Output != 20 || r.Total.Tokens.Output != 30 {
		t.Errorf("archived usage = 01 %+v, overhead %+v, total %+v", r.PRs["01"].Director.Tokens, r.Overhead.Tokens, r.Total.Tokens)
	}
}
