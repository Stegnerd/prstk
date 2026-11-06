package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

// The Playwright version the evidence template pins, and the Chromium build that
// version downloads. Update both together with docs/evidence/playwright-capture.mjs
// and docs/evidence/README.md (a test checks the script header against the version).
const (
	playwrightVersion          = "1.64.1"
	playwrightChromiumRevision = "1248"
)

// goos and readDir are replaced by tests.
var (
	goos    = runtime.GOOS
	readDir = os.ReadDir
	// vhsVersion returns the output of `vhs --version`, or "" if it can't be had.
	vhsVersion = func(path string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
)

var chromiumDirRe = regexp.MustCompile(`^chromium(?:_headless_shell)?-(\d+)$`)

// playwrightBrowsersDir is where Playwright keeps its downloaded browsers:
// $PLAYWRIGHT_BROWSERS_PATH if set, else the per-OS default cache.
func (a *App) playwrightBrowsersDir() string {
	if p := a.Getenv("PLAYWRIGHT_BROWSERS_PATH"); p != "" {
		return p
	}
	switch goos {
	case "darwin":
		return filepath.Join(a.UserHome, "Library", "Caches", "ms-playwright")
	case "windows":
		if l := a.Getenv("LOCALAPPDATA"); l != "" {
			return filepath.Join(l, "ms-playwright")
		}
		return filepath.Join(a.UserHome, "AppData", "Local", "ms-playwright")
	default:
		return filepath.Join(a.UserHome, ".cache", "ms-playwright")
	}
}

// installedChromiumRevisions lists the Chromium revisions found in dir.
func installedChromiumRevisions(dir string) []string {
	entries, _ := readDir(dir)
	seen := map[string]bool{}
	var revs []string
	for _, e := range entries {
		if m := chromiumDirRe.FindStringSubmatch(e.Name()); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			revs = append(revs, m[1])
		}
	}
	sort.Strings(revs)
	return revs
}

// doctorEvidence prints the capture-tool lines (playwright, vhs). They always
// print, and they only ever warn: a PR needs just one of the two tools, and most
// PRs need neither. `in` may be nil (no initiative); when no live PR uses audit
// evidence, the warnings say the tool is only needed for those.
func (a *App) doctorEvidence(in *plan.Initiative) {
	note := ""
	if !usesEvidence(in) {
		note = " (only needed for PRs with audit evidence)"
	}
	warn := func(name, msg string, more ...string) {
		a.out("⚠ %-14s %s%s", name, msg, note)
		for _, m := range more {
			a.out("  %-14s %s", "", m)
		}
	}
	ok := func(name, msg string) { a.out("✓ %-14s %s", name, msg) }

	install := "npx -y playwright@" + playwrightVersion + " install chromium"
	scratch := "(PLAYWRIGHT_BROWSERS_PATH=<scratch dir> for a scratch install, see docs/evidence/README.md)"
	switch {
	case lookPathErr("npx"):
		warn("playwright", "npx not found (install Node, https://nodejs.org); screenshots need Playwright or vhs")
	case a.Getenv("PLAYWRIGHT_BROWSERS_PATH") == "0":
		warn("playwright", "PLAYWRIGHT_BROWSERS_PATH=0 (browsers live in node_modules): can't check for chromium "+playwrightChromiumRevision,
			"to check, unset it or point it at a browsers directory")
	default:
		// Whether the playwright@<version> npm package is installed can't be known: it
		// lives in a scratch directory (docs/evidence/README.md), and doctor never runs
		// npx or touches the network. The Chromium build that version pins is the
		// useful proxy, and it is on disk in one known place.
		dir := a.playwrightBrowsersDir()
		revs := installedChromiumRevisions(dir)
		found := false
		for _, r := range revs {
			if r == playwrightChromiumRevision {
				found = true
			}
		}
		switch {
		case found:
			ok("playwright", "npx; chromium "+playwrightChromiumRevision+" installed (playwright@"+playwrightVersion+")")
		case len(revs) > 0:
			warn("playwright", "found chromium "+strings.Join(revs, ", ")+", need "+playwrightChromiumRevision+" (playwright@"+playwrightVersion+") in "+dir,
				"install: "+install, scratch)
		default:
			warn("playwright", "chromium "+playwrightChromiumRevision+" not found in "+dir+" (playwright@"+playwrightVersion+")",
				"install: "+install, scratch)
		}
	}

	if path, err := lookPath("vhs"); err != nil {
		warn("vhs", "vhs not found (brew install vhs); video needs vhs or Playwright")
	} else if v := vhsVersion(path); v != "" {
		ok("vhs", v)
	} else {
		ok("vhs", "found on PATH")
	}
}

func lookPathErr(bin string) bool {
	_, err := lookPath(bin)
	return err != nil
}

// usesEvidence reports whether any live PR of in has audit evidence.
func usesEvidence(in *plan.Initiative) bool {
	if in == nil {
		return false
	}
	for _, p := range in.PRs {
		if p.Evidence != plan.EvidenceNone && p.Status != plan.Merged && p.Status != plan.Abandoned {
			return true
		}
	}
	return false
}
