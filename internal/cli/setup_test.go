package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stegnerd/prstk/internal/plan"
)

// installTestAgents writes marked, up-to-date agent copies under home, so
// doctor tests that don't care about agents stay green.
func installTestAgents(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range agentNames {
		raw, err := embeddedAgent(n)
		if err != nil {
			t.Fatal(err)
		}
		m, err := markedAgent(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), m, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// setupRun runs a command with an explicit UserHome and environment.
func (e *env) setupRun(cwd, userHome string, getenv map[string]string, args ...string) (string, string, int) {
	e.t.Helper()
	var o, er bytes.Buffer
	a := e.app(cwd)
	a.UserHome = userHome
	a.Getenv = func(k string) string { return getenv[k] }
	a.Stdout, a.Stderr = &o, &er
	code := a.Run(args)
	return o.String(), er.String(), code
}

var teamsOn = map[string]string{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"}

func wantMarked(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := embeddedAgent(name)
	if err != nil {
		t.Fatal(err)
	}
	m, err := markedAgent(raw)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMarkedAgentKeepsFrontmatter(t *testing.T) {
	for _, n := range agentNames {
		m := wantMarked(t, n)
		if !bytes.HasPrefix(m, []byte("---\n")) {
			t.Fatalf("%s must still start with ---", n)
		}
		parts := strings.SplitN(string(m), "\n---\n", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], agentMarker+"\n") {
			t.Fatalf("%s: marker not right after frontmatter:\n%.300s", n, m)
		}
		if strings.Contains(parts[0], "installed by prstk") {
			t.Fatalf("%s: marker leaked into frontmatter", n)
		}
	}
}

func TestSetupFreshInstallAndRerun(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	out, _, code := e.setupRun(t.TempDir(), uh, teamsOn, "setup")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	dir := filepath.Join(uh, ".claude", "agents")
	for _, n := range agentNames {
		if !strings.Contains(out, "✓ agents  wrote "+filepath.Join(dir, n)) {
			t.Errorf("missing wrote line for %s:\n%s", n, out)
		}
		got, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil || !bytes.Equal(got, wantMarked(t, n)) {
			t.Errorf("%s content wrong: %v", n, err)
		}
		if fi, _ := os.Stat(filepath.Join(dir, n)); fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode %v", n, fi.Mode())
		}
	}
	if !strings.Contains(out, "claude --add-dir ~/.pr-plans") || !strings.Contains(out, "fresh session") {
		t.Errorf("missing reminder:\n%s", out)
	}
	out, _, code = e.setupRun(t.TempDir(), uh, teamsOn, "setup")
	if code != 0 || strings.Count(out, "up to date") != 2 {
		t.Fatalf("rerun: exit %d\n%s", code, out)
	}
}

func TestSetupUpdatesMarkerCopy(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	installTestAgents(t, uh)
	p := filepath.Join(uh, ".claude", "agents", "prstk-worker.md")
	os.WriteFile(p, append(wantMarked(t, "prstk-worker.md"), []byte("\nlocal edit\n")...), 0o644)
	out, _, code := e.setupRun(t.TempDir(), uh, teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "✓ agents  updated "+p) || !strings.Contains(out, "prstk-director.md up to date") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, wantMarked(t, "prstk-worker.md")) {
		t.Fatal("not restored")
	}
}

func TestSetupRefusesUnmarkedAndSymlinkThenForce(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	dir := filepath.Join(uh, ".claude", "agents")
	os.MkdirAll(dir, 0o755)
	plain := filepath.Join(dir, "prstk-director.md")
	os.WriteFile(plain, []byte("my own copy\n"), 0o644)
	target := filepath.Join(t.TempDir(), "elsewhere.md")
	os.WriteFile(target, []byte("checkout copy\n"), 0o644)
	link := filepath.Join(dir, "prstk-worker.md")
	os.Symlink(target, link)

	out, errOut, code := e.setupRun(t.TempDir(), uh, teamsOn, "setup")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "refused "+plain+": not written by prstk setup") || !strings.Contains(out, "refused "+link+": symlink to "+target) {
		t.Fatalf("refusals:\n%s", out)
	}
	if !strings.Contains(errOut, "prstk setup:") {
		t.Fatalf("stderr: %q", errOut)
	}
	if b, _ := os.ReadFile(plain); string(b) != "my own copy\n" {
		t.Fatal("plain file touched")
	}
	if l, _ := os.Readlink(link); l != target {
		t.Fatal("symlink touched")
	}

	out, _, code = e.setupRun(t.TempDir(), uh, teamsOn, "setup", "--force")
	if code != 0 || strings.Count(out, "replaced") != 2 {
		t.Fatalf("force: exit %d\n%s", code, out)
	}
	for _, n := range agentNames {
		fi, err := os.Lstat(filepath.Join(dir, n))
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s should be a regular file now", n)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, n)); !bytes.Equal(b, wantMarked(t, n)) {
			t.Fatalf("%s content", n)
		}
	}
	// --force replaced the link, not the file it pointed at.
	if b, _ := os.ReadFile(target); string(b) != "checkout copy\n" {
		t.Fatal("wrote through the symlink")
	}
}

func fakeCheckout(t *testing.T) string {
	t.Helper()
	co := t.TempDir()
	os.MkdirAll(filepath.Join(co, "agents"), 0o755)
	for _, n := range agentNames {
		raw, _ := embeddedAgent(n)
		os.WriteFile(filepath.Join(co, "agents", n), raw, 0o644)
	}
	return co
}

func TestSetupLink(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	co := fakeCheckout(t)
	out, _, code := e.setupRun(t.TempDir(), uh, teamsOn, "setup", "--link", co)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	dir := filepath.Join(uh, ".claude", "agents")
	for _, n := range agentNames {
		if l, err := os.Readlink(filepath.Join(dir, n)); err != nil || l != filepath.Join(co, "agents", n) {
			t.Errorf("%s -> %q %v", n, l, err)
		}
	}
	out, _, code = e.setupRun(t.TempDir(), uh, teamsOn, "setup", "--link", co)
	if code != 0 || strings.Count(out, "up to date") != 2 {
		t.Fatalf("rerun: exit %d\n%s", code, out)
	}
	// A different checkout needs --force.
	co2 := fakeCheckout(t)
	out, _, code = e.setupRun(t.TempDir(), uh, teamsOn, "setup", "--link", co2)
	if code != 1 || !strings.Contains(out, "refused") {
		t.Fatalf("other target: exit %d\n%s", code, out)
	}
	if _, _, code = e.setupRun(t.TempDir(), uh, teamsOn, "setup", "--link", co2, "--force"); code != 0 {
		t.Fatal("force relink failed")
	}
	if l, _ := os.Readlink(filepath.Join(dir, agentNames[0])); l != filepath.Join(co2, "agents", agentNames[0]) {
		t.Fatalf("relinked to %q", l)
	}
	// A bad checkout fails before touching anything.
	uh2 := t.TempDir()
	if out, _, code := e.setupRun(t.TempDir(), uh2, teamsOn, "setup", "--link", t.TempDir()); code == 0 {
		t.Fatalf("empty checkout accepted:\n%s", out)
	}
	if _, err := os.Lstat(filepath.Join(uh2, ".claude")); err == nil {
		t.Fatal("created agents dir despite bad checkout")
	}
}

func TestSetupAgentsDirFlag(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	custom := filepath.Join(t.TempDir(), "my", "agents")
	out, _, code := e.setupRun(t.TempDir(), uh, teamsOn, "setup", "--agents-dir", custom)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(custom, "prstk-worker.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(uh, ".claude")); err == nil {
		t.Fatal("wrote to UserHome despite --agents-dir")
	}
}

func TestSetupCreatesConfig(t *testing.T) {
	e := newEnv(t)
	git(t, e.repo, "remote", "set-head", "origin", "develop")
	git(t, e.repo, "config", "user.name", "Ada Lovelace")
	out, _, code := e.setupRun(e.repo, t.TempDir(), teamsOn, "setup")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	dir := filepath.Join(e.home, "dalton-sample_proj")
	if !strings.Contains(out, "✓ config  wrote "+filepath.Join(dir, "config.yaml")) || strings.Contains(out, "assumed") {
		t.Fatalf("output:\n%s", out)
	}
	cfg, err := plan.LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultBase != "develop" || cfg.MaxWorkers != 2 || cfg.Reviewer != "Ada" || cfg.Setup != "" || len(cfg.Check) != 0 || cfg.EvidenceBranch != "" || cfg.EvidencePublish != nil {
		t.Fatalf("config: %+v", cfg)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	for _, k := range []string{"# setup:", "# check:", "# evidence_branch:", "# evidence_publish:"} {
		if !strings.Contains(string(b), k) {
			t.Errorf("template lacks %q:\n%s", k, b)
		}
	}
}

func TestSetupConfigFallbackBaseAndNoReviewer(t *testing.T) {
	e := newEnv(t)
	git(t, e.repo, "remote", "set-head", "origin", "--delete")
	// An empty user.name in the repo config overrides the global one.
	git(t, e.repo, "config", "user.name", "")
	out, _, code := e.setupRun(e.repo, t.TempDir(), teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "assumed main") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	cfg, err := plan.LoadConfig(filepath.Join(e.home, "dalton-sample_proj"))
	if err != nil || cfg.DefaultBase != "main" || cfg.Reviewer != "" {
		t.Fatalf("config: %+v %v", cfg, err)
	}
}

func TestSetupConfigExistingLeftAlone(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.home, "dalton-sample_proj")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "config.yaml")
	os.WriteFile(p, []byte("default_base: trunk\n"), 0o644)
	for _, args := range [][]string{{"setup"}, {"setup", "--force"}} {
		out, _, code := e.setupRun(e.repo, t.TempDir(), teamsOn, args...)
		if code != 0 || !strings.Contains(out, "✓ config  "+p+" (exists, left alone)") {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
		if b, _ := os.ReadFile(p); string(b) != "default_base: trunk\n" {
			t.Fatalf("%v rewrote config: %q", args, b)
		}
	}
}

func TestSetupOutsideRepoSkipsConfig(t *testing.T) {
	e := newEnv(t)
	out, _, code := e.setupRun(t.TempDir(), t.TempDir(), teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "– config  skipped: not inside a git repo with an origin") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if _, err := os.Stat(e.home); err == nil {
		t.Fatal("created the plans home outside a repo")
	}
}

func TestSetupAgentTeamsWarningLeavesSettingsAlone(t *testing.T) {
	e := newEnv(t)
	uh := t.TempDir()
	settings := filepath.Join(uh, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	os.WriteFile(settings, []byte(`{"theme":"dark"}`), 0o644)
	out, _, code := e.setupRun(t.TempDir(), uh, nil, "setup")
	if code != 0 || !strings.Contains(out, "⚠ agent teams  CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS is not set; add it to the env block of ~/.claude/settings.json") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if b, _ := os.ReadFile(settings); string(b) != `{"theme":"dark"}` {
		t.Fatalf("settings.json changed: %s", b)
	}
	out, _, _ = e.setupRun(t.TempDir(), uh, teamsOn, "setup")
	if strings.Contains(out, "agent teams") {
		t.Fatalf("warned although set:\n%s", out)
	}
}

func TestSetupWarnsOnActiveInitiative(t *testing.T) {
	e := newEnv(t)
	out, _, _ := e.setupRun(e.repo, t.TempDir(), teamsOn, "setup")
	if strings.Contains(out, "active initiative") {
		t.Fatalf("no initiative yet:\n%s", out)
	}
	e.run("init", "mid", "--base", "develop")
	out, _, code := e.setupRun(e.repo, t.TempDir(), teamsOn, "setup")
	if code != 0 || !strings.Contains(out, "⚠ initiative") || !strings.Contains(out, "already-loaded agent definitions") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func doctorAgentsOut(t *testing.T, e *env, uh string) (string, int) {
	t.Helper()
	out, _, code := e.setupRun(e.repo, uh, teamsOn, "doctor")
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "agents  ") {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n"), code
}

func TestDoctorAgents(t *testing.T) {
	e := newEnv(t)
	e.run("init", "dd") // so the base-branch check passes
	dirOf := func(uh string) string { return filepath.Join(uh, ".claude", "agents") }

	// missing: fails doctor
	uh := t.TempDir()
	out, code := doctorAgentsOut(t, e, uh)
	if code == 0 || strings.Count(out, "✗") != 2 || !strings.Contains(out, "run prstk setup") {
		t.Fatalf("missing: exit %d\n%s", code, out)
	}

	// marked and identical
	installTestAgents(t, uh)
	out, code = doctorAgentsOut(t, e, uh)
	if code != 0 || strings.Count(out, "✓") != 2 || strings.Contains(out, "⚠") {
		t.Fatalf("up to date: exit %d\n%s", code, out)
	}

	// marked but differing: warn only
	p := filepath.Join(dirOf(uh), "prstk-worker.md")
	os.WriteFile(p, append(wantMarked(t, "prstk-worker.md"), 'x'), 0o644)
	out, code = doctorAgentsOut(t, e, uh)
	if code != 0 || !strings.Contains(out, "⚠ agents         prstk-worker.md: out of date with this prstk binary; run prstk setup") {
		t.Fatalf("stale: exit %d\n%s", code, out)
	}

	// plain file without the marker: warn only
	os.WriteFile(p, []byte("old copy\n"), 0o644)
	out, code = doctorAgentsOut(t, e, uh)
	if code != 0 || !strings.Contains(out, "not installed by prstk setup; run prstk setup --force to manage it") {
		t.Fatalf("plain: exit %d\n%s", code, out)
	}

	// symlink to an identical checkout: linked, no warning
	co := fakeCheckout(t)
	uh2 := t.TempDir()
	if _, _, code := e.setupRun(t.TempDir(), uh2, teamsOn, "setup", "--link", co); code != 0 {
		t.Fatal("link failed")
	}
	out, code = doctorAgentsOut(t, e, uh2)
	if code != 0 || strings.Count(out, "✓ agents         prstk-") != 2 || !strings.Contains(out, "linked to "+filepath.Join(co, "agents", "prstk-worker.md")) || strings.Contains(out, "⚠") {
		t.Fatalf("link: exit %d\n%s", code, out)
	}

	// symlink to a differing checkout: linked plus a warning, still passing
	os.WriteFile(filepath.Join(co, "agents", "prstk-worker.md"), []byte("dev edit\n"), 0o644)
	out, code = doctorAgentsOut(t, e, uh2)
	if code != 0 || strings.Count(out, "✓") != 2 || strings.Count(out, "⚠") != 1 || !strings.Contains(out, "your checkout differs from this binary's version") {
		t.Fatalf("differing link: exit %d\n%s", code, out)
	}
}
