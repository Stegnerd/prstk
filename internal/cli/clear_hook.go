package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Stegnerd/prstk/internal/plan"
)

// handoffHookCommand is the command the SessionStart hook runs. The shell
// expands ${PRSTK_BIN:-prstk} when the hook fires, so a dev binary is used
// when PRSTK_BIN is set.
const handoffHookCommand = `"${PRSTK_BIN:-prstk}" handoff --hook`

// handoffHookMarker is what identifies our hook inside a settings file.
const handoffHookMarker = "handoff --hook"

// handoffHookSnippet is the hand-copyable form of the hook.
const handoffHookSnippet = `{"hooks": {"SessionStart": [{"matcher": "clear", "hooks": [{"type": "command", "command": "\"${PRSTK_BIN:-prstk}\" handoff --hook"}]}]}}`

// readSettings parses a Claude Code settings file. A missing or blank file is
// an empty object; invalid JSON, or JSON that isn't an object, is an error.
func readSettings(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber() // keep numbers exactly as written
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if dec.More() || m == nil {
		return nil, errors.New("not a single JSON object")
	}
	return m, nil
}

// sessionStartEntries returns hooks.SessionStart, or an error when the shape
// is not what Claude Code expects (so we never overwrite something odd).
func sessionStartEntries(s map[string]any) ([]any, error) {
	h, ok := s["hooks"]
	if !ok {
		return nil, nil
	}
	hm, ok := h.(map[string]any)
	if !ok {
		return nil, errors.New(`"hooks" is not an object`)
	}
	ss, ok := hm["SessionStart"]
	if !ok {
		return nil, nil
	}
	arr, ok := ss.([]any)
	if !ok {
		return nil, errors.New(`"hooks.SessionStart" is not an array`)
	}
	return arr, nil
}

// hasHandoffHook reports whether any SessionStart hook command contains
// "handoff --hook".
func hasHandoffHook(s map[string]any) bool {
	arr, err := sessionStartEntries(s)
	if err != nil {
		return false
	}
	for _, e := range arr {
		em, _ := e.(map[string]any)
		hooks, _ := em["hooks"].([]any)
		for _, h := range hooks {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, handoffHookMarker) {
				return true
			}
		}
	}
	return false
}

// settingsHaveHandoffHook reads path and reports whether it has the hook.
// Unreadable or malformed files count as "no".
func settingsHaveHandoffHook(path string) bool {
	s, err := readSettings(path)
	return err == nil && hasHandoffHook(s)
}

// addHandoffHook returns s with the clear hook appended, everything else kept.
func addHandoffHook(s map[string]any) (map[string]any, error) {
	arr, err := sessionStartEntries(s)
	if err != nil {
		return nil, err
	}
	entry := map[string]any{
		"matcher": "clear",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": handoffHookCommand,
		}},
	}
	arr = append(arr, entry)
	hm, _ := s["hooks"].(map[string]any)
	if hm == nil {
		hm = map[string]any{}
		s["hooks"] = hm
	}
	hm["SessionStart"] = arr
	return s, nil
}

// marshalSettings renders with 2-space indent, a trailing newline, and no
// HTML escaping (the hook command has quotes and braces).
func marshalSettings(s map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil // Encode ends with a newline
}

// setupClearHook merges the /clear handoff hook into the repo's
// .claude/settings.json. It returns false when this counts as a setup problem.
func (a *App) setupClearHook() bool {
	top, err := a.Git.MainWorktree(a.Cwd)
	if err != nil {
		a.out("– clear hook  skipped: not inside a git repo (run prstk setup from the repo to install it)")
		return true
	}
	path := filepath.Join(top, ".claude", "settings.json")
	s, err := readSettings(path)
	if err != nil {
		a.out("✗ clear hook  %s is not valid JSON; fix it or add the hook by hand", path)
		a.out("  %s", handoffHookSnippet)
		return false
	}
	if hasHandoffHook(s) {
		a.out("✓ clear hook  already in %s", path)
		return true
	}
	s, err = addHandoffHook(s)
	if err != nil {
		a.out("✗ clear hook  %s: %v; fix it or add the hook by hand", path, err)
		a.out("  %s", handoffHookSnippet)
		return false
	}
	b, err := marshalSettings(s)
	if err == nil {
		if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			err = plan.WriteFileAtomic(path, b)
		}
	}
	if err != nil {
		a.out("✗ clear hook  %s: %v", path, err)
		return false
	}
	a.out("✓ clear hook  added to %s (commit it so it's reviewable)", path)
	return true
}

// doctorClearHook prints the warning-only clear-hook line. It looks in the
// project's settings, its local settings, then the user's.
func (a *App) doctorClearHook() {
	root := a.Cwd
	if top, err := a.Git.MainWorktree(a.Cwd); err == nil {
		root = top
	}
	paths := []string{
		filepath.Join(root, ".claude", "settings.json"),
		filepath.Join(root, ".claude", "settings.local.json"),
	}
	if a.UserHome != "" {
		paths = append(paths, filepath.Join(a.UserHome, ".claude", "settings.json"))
	}
	for _, p := range paths {
		if settingsHaveHandoffHook(p) {
			a.out("✓ clear hook  %s", p)
			return
		}
	}
	a.out("⚠ clear hook  not installed; run prstk setup")
}
