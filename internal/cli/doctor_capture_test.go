package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capEnv describes a fake machine for doctor's capture-tool lines: which tools are
// on PATH, the environment, and the home directory and OS the default cache uses.
type capEnv struct {
	npx, vhs   bool
	vhsVersion string
	getenv     map[string]string
	userHome   string
	goos       string
}

func (c capEnv) doctor(t *testing.T, e *env, cwd string) (string, int) {
	t.Helper()
	oldLP, oldV, oldGOOS := lookPath, vhsVersion, goos
	t.Cleanup(func() { lookPath, vhsVersion, goos = oldLP, oldV, oldGOOS })
	lookPath = func(name string) (string, error) {
		switch name {
		case "npx":
			if !c.npx {
				return "", errors.New("not found")
			}
		case "vhs":
			if !c.vhs {
				return "", errors.New("not found")
			}
		}
		return "/usr/bin/" + name, nil
	}
	vhsVersion = func(string) string { return c.vhsVersion }
	if c.goos != "" {
		goos = c.goos
	}
	var o, er bytes.Buffer
	a := e.app(cwd)
	a.Stdout, a.Stderr = &o, &er
	a.Getenv = func(k string) string {
		if v, ok := c.getenv[k]; ok {
			return v
		}
		if strings.HasPrefix(k, "CLAUDE_CODE") {
			return "1"
		}
		return ""
	}
	if c.userHome != "" {
		a.UserHome = c.userHome
	}
	code := a.Run([]string{"doctor"})
	return o.String(), code
}

func mkBrowsers(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDoctorPlaywrightBrowsersPathMatching(t *testing.T) {
	e := newEnv(t)
	e.run("init", "ev")
	e.run("add", "01", "shots", "--title", "Shots", "--evidence", "screenshots")
	dir := t.TempDir()
	mkBrowsers(t, dir, "chromium-"+playwrightChromiumRevision, "ffmpeg-1011")
	out, code := capEnv{npx: true, vhs: true, vhsVersion: "vhs version 0.12.1",
		getenv: map[string]string{"PLAYWRIGHT_BROWSERS_PATH": dir}}.doctor(t, e, e.repo)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "✓ playwright     npx; chromium 1248 installed (playwright@1.64.1)") {
		t.Fatalf("playwright line:\n%s", out)
	}
	if !strings.Contains(out, "✓ vhs            vhs version 0.12.1") {
		t.Fatalf("vhs line:\n%s", out)
	}
	// The headless shell directory counts too.
	dir2 := t.TempDir()
	mkBrowsers(t, dir2, "chromium_headless_shell-"+playwrightChromiumRevision)
	out, _ = capEnv{npx: true, vhs: true, getenv: map[string]string{"PLAYWRIGHT_BROWSERS_PATH": dir2}}.doctor(t, e, e.repo)
	if !strings.Contains(out, "✓ playwright") || !strings.Contains(out, "✓ vhs            found on PATH") {
		t.Fatalf("headless shell:\n%s", out)
	}
}

func TestDoctorPlaywrightWrongRevisionInDefaultCache(t *testing.T) {
	e := newEnv(t)
	e.run("init", "ev")
	e.run("add", "01", "shots", "--title", "Shots", "--evidence", "screenshots")
	home := t.TempDir()
	cases := map[string]string{
		"darwin": filepath.Join(home, "Library", "Caches", "ms-playwright"),
		"linux":  filepath.Join(home, ".cache", "ms-playwright"),
	}
	for goosName, dir := range cases {
		mkBrowsers(t, dir, "chromium-1234", "chromium_headless_shell-1234")
		out, code := capEnv{npx: true, vhs: true, userHome: home, goos: goosName}.doctor(t, e, e.repo)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", goosName, code, out)
		}
		for _, want := range []string{"⚠ playwright", "found chromium 1234, need 1248", dir,
			"install: npx -y playwright@1.64.1 install chromium", "PLAYWRIGHT_BROWSERS_PATH=<scratch dir>"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s: missing %q:\n%s", goosName, want, out)
			}
		}
		if strings.Contains(out, "only needed for PRs") {
			t.Fatalf("%s: evidence PR is live, no note expected:\n%s", goosName, out)
		}
	}
	// Windows default: %LOCALAPPDATA%\ms-playwright.
	local := t.TempDir()
	mkBrowsers(t, filepath.Join(local, "ms-playwright"), "chromium-"+playwrightChromiumRevision)
	out, _ := capEnv{npx: true, vhs: true, goos: "windows", getenv: map[string]string{"LOCALAPPDATA": local}}.doctor(t, e, e.repo)
	if !strings.Contains(out, "✓ playwright") {
		t.Fatalf("windows:\n%s", out)
	}
	// Nothing installed at all.
	out, _ = capEnv{npx: true, vhs: true, userHome: t.TempDir(), goos: "linux"}.doctor(t, e, e.repo)
	if !strings.Contains(out, "chromium 1248 not found in") || !strings.Contains(out, "install: npx -y playwright@1.64.1 install chromium") {
		t.Fatalf("empty cache:\n%s", out)
	}
}

func TestDoctorPlaywrightNpxMissing(t *testing.T) {
	e := newEnv(t)
	e.run("init", "ev")
	e.run("add", "01", "shots", "--title", "Shots", "--evidence", "screenshots")
	out, code := capEnv{vhs: true}.doctor(t, e, e.repo)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "⚠ playwright     npx not found (install Node") {
		t.Fatalf("npx missing:\n%s", out)
	}
}

func TestDoctorPlaywrightBrowsersPathZero(t *testing.T) {
	e := newEnv(t)
	e.run("init", "ev")
	out, code := capEnv{npx: true, vhs: true, getenv: map[string]string{"PLAYWRIGHT_BROWSERS_PATH": "0"}}.doctor(t, e, e.repo)
	if code != 0 || !strings.Contains(out, "⚠ playwright") || !strings.Contains(out, "PLAYWRIGHT_BROWSERS_PATH=0") {
		t.Fatalf("path=0: %d\n%s", code, out)
	}
}

func TestDoctorCaptureLinesAlwaysPrintAndNeverFail(t *testing.T) {
	e := newEnv(t)
	// No initiative at all (run from the repo before init): both lines still print.
	none := capEnv{userHome: t.TempDir(), goos: "linux"}
	out, _ := none.doctor(t, e, e.repo)
	for _, want := range []string{"⚠ playwright", "⚠ vhs", "(only needed for PRs with audit evidence)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no initiative: missing %q:\n%s", want, out)
		}
	}
	// An initiative with no evidence PR: same.
	e.run("init", "ev")
	e.run("add", "01", "plain", "--title", "Plain")
	out, code := none.doctor(t, e, e.repo)
	if code != 0 || strings.Count(out, "(only needed for PRs with audit evidence)") != 2 {
		t.Fatalf("no evidence PR: %d\n%s", code, out)
	}
	// A live evidence PR: lines print, no "only needed" note, still exit 0 with every tool missing.
	e.run("add", "02", "shots", "--title", "Shots", "--evidence", "screenshots")
	out, code = none.doctor(t, e, e.repo)
	if code != 0 {
		t.Fatalf("doctor failed on missing tools: %d\n%s", code, out)
	}
	if !strings.Contains(out, "⚠ playwright") || !strings.Contains(out, "⚠ vhs") || !strings.Contains(out, "brew install vhs") || strings.Contains(out, "only needed") {
		t.Fatalf("live evidence PR:\n%s", out)
	}
	// Once the evidence PR is done, the note comes back, and the lines stay.
	e.run("status", "02", "abandoned")
	out, _ = none.doctor(t, e, e.repo)
	if !strings.Contains(out, "⚠ playwright") || !strings.Contains(out, "only needed") {
		t.Fatalf("abandoned PR:\n%s", out)
	}
}

// The pin lives in three places: the script header, docs/evidence/README.md and the
// doctor constants. Bump them together.
func TestPlaywrightPinMatchesTemplateAndDocs(t *testing.T) {
	want := "playwright@" + playwrightVersion
	for _, f := range []string{"playwright-capture.mjs", "README.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", "evidence", f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("docs/evidence/%s does not mention %s; update it with the doctor constants", f, want)
		}
	}
}
