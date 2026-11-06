package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Stegnerd/prstk"
	"github.com/Stegnerd/prstk/internal/plan"
)

// agentMarker is placed right after an installed agent file's frontmatter. It
// marks the file as ours: setup overwrites marked files and doctor compares
// them with the embedded copy.
const agentMarker = "<!-- installed by prstk setup: re-run prstk setup after upgrading prstk; local edits are overwritten -->"

// agentNames are the installed agent definitions (all embedded in the binary).
var agentNames = []string{"prstk-director.md", "prstk-worker.md"}

// embeddedAgent returns the raw embedded definition.
func embeddedAgent(name string) ([]byte, error) {
	return prstk.Agents.ReadFile("agents/" + name)
}

// markedAgent returns raw with the marker line inserted after the closing
// "---" of the frontmatter, so the file still starts with "---".
func markedAgent(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return nil, errors.New("agent definition has no frontmatter")
	}
	i := bytes.Index(raw[3:], []byte("\n---\n"))
	if i < 0 {
		return nil, errors.New("agent definition has unterminated frontmatter")
	}
	end := 3 + i + len("\n---\n")
	var b bytes.Buffer
	b.Write(raw[:end])
	b.WriteString(agentMarker + "\n")
	b.Write(raw[end:])
	return b.Bytes(), nil
}

func hasAgentMarker(b []byte) bool { return bytes.Contains(b, []byte(agentMarker)) }

type setupOpts struct {
	link      string
	force     bool
	agentsDir string
}

func cmdSetup(a *App, o setupOpts) error {
	dir := o.agentsDir
	switch {
	case dir == "":
		if a.UserHome == "" {
			return errors.New("can't find your home directory; pass --agents-dir")
		}
		dir = filepath.Join(a.UserHome, ".claude", "agents")
	case !filepath.IsAbs(dir):
		dir = filepath.Join(a.Cwd, dir)
	}

	bad := 0
	var err error
	if o.link != "" {
		bad, err = a.setupLink(dir, o)
	} else {
		bad, err = a.setupCopy(dir, o)
	}
	if err != nil {
		return err
	}
	if !a.setupConfig() {
		bad++
	}

	if _, err := a.resolve(); err == nil {
		a.out("⚠ initiative  this repo has an active initiative: a running director and its workers keep their already-loaded agent definitions until their next session")
	}
	if a.agentTeamsEnabled() != nil {
		a.out("⚠ agent teams  CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS is not set; add it to the env block of ~/.claude/settings.json")
	}
	a.out("next: start Claude with `claude --add-dir ~/.pr-plans`, in a fresh session so the agent definitions load")

	if bad > 0 {
		return fmt.Errorf("%d problem(s); see above", bad)
	}
	return nil
}

// setupCopy installs the embedded definitions as marked copies. It returns
// the number of refused or failed files.
func (a *App) setupCopy(dir string, o setupOpts) (int, error) {
	want := map[string][]byte{}
	for _, n := range agentNames {
		raw, err := embeddedAgent(n)
		if err != nil {
			return 0, err
		}
		if want[n], err = markedAgent(raw); err != nil {
			return 0, fmt.Errorf("%s: %w", n, err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	bad := 0
	for _, n := range agentNames {
		path := filepath.Join(dir, n)
		st := inspectAgent(path)
		verb := ""
		switch {
		case st.err != nil:
			a.out("✗ agents  %s: %v", path, st.err)
			bad++
			continue
		case st.kind == agentMissing:
			verb = "wrote"
		case st.kind == agentMarked:
			if bytes.Equal(st.content, want[n]) {
				a.out("✓ agents  %s up to date", path)
				continue
			}
			verb = "updated"
		default:
			why := "not written by prstk setup"
			if st.kind == agentSymlink {
				why = "symlink to " + st.target
			}
			if !o.force {
				a.out("✗ agents  refused %s: %s (use --force to replace it)", path, why)
				bad++
				continue
			}
			verb = "replaced"
		}
		// Rename replaces a symlink itself, never the file it points at.
		if err := plan.WriteFileAtomic(path, want[n]); err != nil {
			a.out("✗ agents  %s: %v", path, err)
			bad++
			continue
		}
		a.out("✓ agents  %s %s", verb, path)
	}
	return bad, nil
}

// setupLink symlinks the definitions to a prstk checkout (for prstk developers).
func (a *App) setupLink(dir string, o setupOpts) (int, error) {
	checkout := o.link
	if !filepath.IsAbs(checkout) {
		checkout = filepath.Join(a.Cwd, checkout)
	}
	checkout = filepath.Clean(checkout)
	src := map[string]string{}
	for _, n := range agentNames {
		p := filepath.Join(checkout, "agents", n)
		if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
			return 0, fmt.Errorf("--link: %s not found (is %s a prstk checkout?)", p, checkout)
		}
		src[n] = p
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	bad := 0
	for _, n := range agentNames {
		path := filepath.Join(dir, n)
		st := inspectAgent(path)
		switch {
		case st.err != nil:
			a.out("✗ agents  %s: %v", path, st.err)
			bad++
			continue
		case st.kind == agentSymlink && st.target == src[n]:
			a.out("✓ agents  %s up to date (-> %s)", path, src[n])
			continue
		case st.kind != agentMissing:
			if !o.force {
				why := "a copy that isn't a link to " + src[n]
				if st.kind == agentSymlink {
					why = "symlink to " + st.target
				}
				a.out("✗ agents  refused %s: %s (use --force to replace it)", path, why)
				bad++
				continue
			}
			if err := os.Remove(path); err != nil {
				a.out("✗ agents  %s: %v", path, err)
				bad++
				continue
			}
		}
		if err := os.Symlink(src[n], path); err != nil {
			a.out("✗ agents  %s: %v", path, err)
			bad++
			continue
		}
		a.out("✓ agents  linked %s -> %s", path, src[n])
	}
	return bad, nil
}

type agentKind int

const (
	agentMissing agentKind = iota
	agentMarked            // regular file carrying the marker
	agentPlain             // regular file without the marker
	agentSymlink
)

type agentState struct {
	kind    agentKind
	content []byte // marked/plain: the file's bytes
	target  string // symlink: its target as stored
	err     error  // unreadable, or not a regular file or symlink
}

func inspectAgent(path string) agentState {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return agentState{kind: agentMissing}
	}
	if err != nil {
		return agentState{err: err}
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t, err := os.Readlink(path)
		return agentState{kind: agentSymlink, target: t, err: err}
	}
	if !fi.Mode().IsRegular() {
		return agentState{err: errors.New("not a regular file")}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return agentState{err: err}
	}
	if hasAgentMarker(b) {
		return agentState{kind: agentMarked, content: b}
	}
	return agentState{kind: agentPlain, content: b}
}

// setupConfig creates the repo's config.yaml when the cwd is in a repo with
// an origin. It never touches an existing one. It returns false on a write failure.
func (a *App) setupConfig() bool {
	rc, err := a.repoFromCwd()
	if err != nil {
		a.out("– config  skipped: not inside a git repo with an origin (run prstk setup from the repo to create its config.yaml)")
		return true
	}
	repoDir := filepath.Join(a.Home, rc.key)
	path := filepath.Join(repoDir, "config.yaml")
	if _, err := os.Lstat(path); err == nil {
		a.out("✓ config  %s (exists, left alone)", path)
		return true
	}
	base, detected := "main", false
	if out, err := a.Git.Run(rc.path, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.TrimPrefix(out, "origin/") != "" {
		base, detected = strings.TrimPrefix(out, "origin/"), true
	}
	reviewer := ""
	if n, err := a.Git.Run(rc.path, "config", "user.name"); err == nil {
		if f := strings.Fields(n); len(f) > 0 {
			reviewer = f[0]
		}
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		a.out("✗ config  %v", err)
		return false
	}
	// O_EXCL: never overwrite, even if one appeared since the check above.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		a.out("✓ config  %s (exists, left alone)", path)
		return true
	}
	if err != nil {
		a.out("✗ config  %v", err)
		return false
	}
	_, werr := f.WriteString(configTemplate(base, reviewer))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		a.out("✗ config  %s: %v", path, werr)
		return false
	}
	note := ""
	if !detected {
		note = fmt.Sprintf(" (couldn't detect origin's default branch locally; assumed %s, edit default_base if wrong)", base)
	}
	a.out("✓ config  wrote %s%s", path, note)
	return true
}

// yamlScalar renders s as a safe single-line YAML scalar.
func yamlScalar(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSpace(string(b))
}

func configTemplate(base, reviewer string) string {
	var b strings.Builder
	b.WriteString("# prstk config for this repo, created by `prstk setup`. Every key is optional.\n")
	fmt.Fprintf(&b, "default_base: %s   # branch new initiatives and PRs target (`prstk init --base` overrides)\n", yamlScalar(base))
	b.WriteString("max_workers: 2   # PRs worked in parallel\n")
	if reviewer != "" {
		fmt.Fprintf(&b, "reviewer: %s   # your name, used in the \"## Review: <name>\" heading (default: Human)\n", yamlScalar(reviewer))
	}
	b.WriteString(`
# Shell command run in each new worktree after it is created.
# setup: "go mod download"

# Shell commands ` + "`prstk check`" + ` runs on a finished PR, in order. Default in a Go repo:
# go vet, gofmt -l, go test.
# check:
#   - go vet ./...
#   - go test ./...

# Orphan branch that audit evidence is published to (default: prstk-evidence).
# evidence_branch: prstk-evidence

# false: never publish evidence captures to GitHub (e.g. a public repo).
# evidence_publish: false
`)
	return b.String()
}
