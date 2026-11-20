package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the repo's resolved path (git reports it with symlinks resolved,
// e.g. /private/var on macOS).
func repoRoot(e *env) string {
	r, err := filepath.EvalSymlinks(e.repo)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func settingsPath(e *env) string { return filepath.Join(repoRoot(e), ".claude", "settings.json") }

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v\n%s", path, err, b)
	}
	return m
}

func countHandoffHooks(t *testing.T, path string) int {
	t.Helper()
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), "handoff --hook")
}

func TestSetupInstallsClearHookInNewFile(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	out, _, code := e.setupRun(e.repo, uh, teamsOn, "setup")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	p := settingsPath(e)
	if !strings.Contains(out, "✓ clear hook  added to "+p+" (commit it so it's reviewable)") {
		t.Fatalf("missing added line:\n%s", out)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasSuffix(string(b), "}\n") || !strings.Contains(string(b), "\n  \"hooks\": {") {
		t.Errorf("want 2-space indent and trailing newline:\n%s", b)
	}
	m := readJSON(t, p)
	entries := m["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries: %v", entries)
	}
	ent := entries[0].(map[string]any)
	h := ent["hooks"].([]any)[0].(map[string]any)
	if ent["matcher"] != "clear" || h["type"] != "command" || h["command"] != `"${PRSTK_BIN:-prstk}" handoff --hook` {
		t.Errorf("wrong entry: %v", ent)
	}
}

func TestSetupClearHookPreservesOtherSettingsAndIsIdempotent(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	p := settingsPath(e)
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(`{
  "permissions": {"allow": ["Bash(go test:*)"]},
  "retries": 3,
  "hooks": {
    "SessionStart": [{"matcher": "startup", "hooks": [{"type": "command", "command": "echo hi & echo <ok>"}]}],
    "PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "true"}]}]
  }
}
`), 0o644)
	out, _, code := e.setupRun(e.repo, uh, teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "✓ clear hook  added to "+p) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	m := readJSON(t, p)
	if m["permissions"] == nil || m["retries"].(float64) != 3 || m["hooks"].(map[string]any)["PreToolUse"] == nil {
		t.Errorf("other keys lost: %v", m)
	}
	ss := m["hooks"].(map[string]any)["SessionStart"].([]any)
	if len(ss) != 2 || ss[0].(map[string]any)["matcher"] != "startup" || ss[1].(map[string]any)["matcher"] != "clear" {
		t.Errorf("SessionStart: %v", ss)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "echo hi & echo <ok>") {
		t.Errorf("existing command was HTML-escaped:\n%s", b)
	}

	before, _ := os.ReadFile(p)
	out, _, code = e.setupRun(e.repo, uh, teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "✓ clear hook  already in "+p) {
		t.Fatalf("rerun: exit %d\n%s", code, out)
	}
	after, _ := os.ReadFile(p)
	if string(before) != string(after) || countHandoffHooks(t, p) != 1 {
		t.Errorf("rerun changed the file:\n%s", after)
	}
}

func TestSetupClearHookMalformedJSONLeftUntouched(t *testing.T) {
	e := newEnv(t)
	p := settingsPath(e)
	os.MkdirAll(filepath.Dir(p), 0o755)
	bad := "{ not json,\n"
	os.WriteFile(p, []byte(bad), 0o644)
	out, _, code := e.setupRun(e.repo, t.TempDir(), teamsOn, "setup")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (a setup problem)\n%s", code, out)
	}
	if !strings.Contains(out, "✗ clear hook  "+p+" is not valid JSON; fix it or add the hook by hand") ||
		!strings.Contains(out, `"matcher": "clear"`) {
		t.Errorf("missing error or snippet:\n%s", out)
	}
	if got, _ := os.ReadFile(p); string(got) != bad {
		t.Errorf("file was modified: %q", got)
	}
}

func TestSetupClearHookWrongShapeLeftUntouched(t *testing.T) {
	e := newEnv(t)
	p := settingsPath(e)
	os.MkdirAll(filepath.Dir(p), 0o755)
	odd := `{"hooks": {"SessionStart": "nope"}}`
	os.WriteFile(p, []byte(odd), 0o644)
	out, _, code := e.setupRun(e.repo, t.TempDir(), teamsOn, "setup")
	if code != 1 || !strings.Contains(out, "✗ clear hook") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if got, _ := os.ReadFile(p); string(got) != odd {
		t.Errorf("file was modified: %q", got)
	}
}

func TestSetupClearHookAddedEvenWhenUserSettingsHaveIt(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	os.MkdirAll(filepath.Join(uh, ".claude"), 0o755)
	os.WriteFile(filepath.Join(uh, ".claude", "settings.json"),
		[]byte(`{"hooks":{"SessionStart":[{"matcher":"clear","hooks":[{"type":"command","command":"prstk handoff --hook"}]}]}}`), 0o644)
	out, _, _ := e.setupRun(e.repo, uh, teamsOn, "setup")
	if !strings.Contains(out, "✓ clear hook  added to "+settingsPath(e)) {
		t.Fatalf("project hook should still be added:\n%s", out)
	}
}

func TestSetupClearHookSkippedOutsideRepo(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.setupRun(t.TempDir(), t.TempDir(), teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "– clear hook  skipped") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestDoctorClearHook(t *testing.T) {
	hook := `{"hooks":{"SessionStart":[{"matcher":"clear","hooks":[{"type":"command","command":"\"${PRSTK_BIN:-prstk}\" handoff --hook"}]}]}}`
	write := func(path, body string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(body), 0o644)
	}
	run := func(e *env, uh string) (string, int) {
		out, _, code := e.setupRun(e.repo, uh, teamsOn, "doctor")
		return out, code
	}

	t.Run("missing warns without failing", func(t *testing.T) {
		e := newEnv(t)
		uh := t.TempDir()
		installTestAgents(t, uh)
		out, _ := run(e, uh)
		if !strings.Contains(out, "⚠ clear hook  not installed; run prstk setup") {
			t.Fatalf("missing warning:\n%s", out)
		}
	})
	for _, c := range []struct{ name, rel string }{
		{"project", ".claude/settings.json"},
		{"local", ".claude/settings.local.json"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			uh := t.TempDir()
			installTestAgents(t, uh)
			p := filepath.Join(repoRoot(e), c.rel)
			write(p, hook)
			out, _ := run(e, uh)
			if !strings.Contains(out, "✓ clear hook  "+p) {
				t.Fatalf("not detected:\n%s", out)
			}
		})
	}
	t.Run("user", func(t *testing.T) {
		e := newEnv(t)
		uh := t.TempDir()
		installTestAgents(t, uh)
		p := filepath.Join(uh, ".claude", "settings.json")
		write(p, hook)
		out, _ := run(e, uh)
		if !strings.Contains(out, "✓ clear hook  "+p) {
			t.Fatalf("not detected:\n%s", out)
		}
	})
	t.Run("warning does not change exit code", func(t *testing.T) {
		e := newEnv(t)
		uh := t.TempDir()
		installTestAgents(t, uh)
		_, without := run(e, uh)
		write(filepath.Join(e.repo, ".claude/settings.json"), hook)
		_, with := run(e, uh)
		if without != with {
			t.Errorf("exit code differs: %d without hook, %d with", without, with)
		}
	})
	t.Run("malformed file is not installed", func(t *testing.T) {
		e := newEnv(t)
		uh := t.TempDir()
		installTestAgents(t, uh)
		write(filepath.Join(e.repo, ".claude/settings.json"), "{ nope")
		out, _ := run(e, uh)
		if !strings.Contains(out, "⚠ clear hook  not installed") {
			t.Fatalf("out:\n%s", out)
		}
	})
}
