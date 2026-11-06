package cli

import (
	"bytes"
	"os"
	"path/filepath"
)

// doctorAgents prints one line per installed agent definition and returns how
// many failed (missing files). Out-of-date copies only warn.
func (a *App) doctorAgents() (failed int) {
	dir := filepath.Join(a.UserHome, ".claude", "agents")
	for _, n := range agentNames {
		raw, err := embeddedAgent(n)
		if err != nil {
			a.out("✗ %-14s %s: %v", "agents", n, err)
			failed++
			continue
		}
		want, err := markedAgent(raw)
		if err != nil {
			a.out("✗ %-14s %s: %v", "agents", n, err)
			failed++
			continue
		}
		path := filepath.Join(dir, n)
		st := inspectAgent(path)
		switch {
		case st.err != nil:
			a.out("✗ %-14s %s: %v", "agents", n, st.err)
			failed++
		case st.kind == agentMissing:
			a.out("✗ %-14s %s: not found in %s; run prstk setup", "agents", n, dir)
			failed++
		case st.kind == agentMarked && bytes.Equal(st.content, want):
			a.out("✓ %-14s %s: installed by prstk setup, up to date", "agents", n)
		case st.kind == agentMarked:
			a.out("⚠ %-14s %s: out of date with this prstk binary; run prstk setup", "agents", n)
		case st.kind == agentPlain:
			a.out("⚠ %-14s %s: not installed by prstk setup; run prstk setup --force to manage it", "agents", n)
		case st.kind == agentSymlink:
			a.out("✓ %-14s %s: linked to %s", "agents", n, st.target)
			t := st.target
			if !filepath.IsAbs(t) {
				t = filepath.Join(dir, t)
			}
			if b, err := os.ReadFile(t); err != nil {
				a.out("⚠ %-14s %s: link target can't be read (%v)", "agents", n, err)
			} else if !bytes.Equal(b, raw) {
				a.out("⚠ %-14s %s: your checkout differs from this binary's version", "agents", n)
			}
		}
	}
	return failed
}
