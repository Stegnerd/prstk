package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Home is the plans root: $PRSTK_HOME or ~/.pr-plans.
func Home() (string, error) {
	if h := os.Getenv("PRSTK_HOME"); h != "" {
		return filepath.Abs(h)
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(u, ".pr-plans"), nil
}

var remoteRe = regexp.MustCompile(`^(?:[a-z+]+://)?(?:[^@/]+@)?[^:/]+(?::\d+)?[:/](.+?)/([^/]+?)(?:\.git)?/?$`)

// ParseRemote extracts owner and repo from a git remote URL (ssh or https).
func ParseRemote(url string) (owner, repo string, err error) {
	url = strings.TrimSpace(url)
	// Local remotes (/path/owner/repo.git, file:///path/owner/repo.git):
	// use the last two path segments.
	if p, ok := strings.CutPrefix(url, "file://"); ok || strings.HasPrefix(url, "/") {
		if !ok {
			p = url
		}
		p = strings.TrimSuffix(strings.TrimSuffix(p, "/"), ".git")
		dir, name := filepath.Split(p)
		owner := filepath.Base(dir)
		if name == "" || owner == "" || owner == "/" || owner == "." {
			return "", "", fmt.Errorf("can't parse owner/repo from remote %q", url)
		}
		return owner, name, nil
	}
	m := remoteRe.FindStringSubmatch(url)
	if m == nil {
		return "", "", fmt.Errorf("can't parse owner/repo from remote %q", url)
	}
	parts := strings.Split(m[1], "/")
	return parts[len(parts)-1], m[2], nil
}

// RepoKey is the plans directory name for a repo. GitHub owner/repo names
// are case-insensitive, so the key is lowercased.
func RepoKey(owner, repo string) string { return strings.ToLower(owner + "-" + repo) }

// Resolver finds the active initiative (§8 Global behavior).
type Resolver struct {
	Home string
	Cwd  string
	Flag string // --initiative
	// RepoKey returns the repo key for cwd (from the origin remote). It may
	// be nil or fail when cwd isn't a git repo.
	RepoKey func() (string, error)
}

func (r Resolver) Resolve() (string, error) {
	// 2. cwd inside ~/.pr-plans/<repo>/<initiative>/ — checked first for the
	// repo key even when --initiative is given.
	repoKey := ""
	if rel, err := filepath.Rel(r.Home, r.Cwd); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		parts := strings.Split(rel, string(filepath.Separator))
		repoKey = parts[0]
		if len(parts) >= 2 && r.Flag == "" && parts[1] != "_archive" {
			dir := filepath.Join(r.Home, parts[0], parts[1])
			if isInitiative(dir) {
				return dir, nil
			}
		}
	}
	if repoKey == "" && r.RepoKey != nil {
		k, err := r.RepoKey()
		if err != nil {
			if r.Flag != "" {
				return r.byFlagAnyRepo()
			}
			return "", fmt.Errorf("can't determine repo: %w (run inside the repo or pass --initiative)", err)
		}
		repoKey = k
	}
	if repoKey == "" {
		return "", errors.New("can't determine repo (run inside the repo or pass --initiative)")
	}
	repoDir := filepath.Join(r.Home, repoKey)

	// 1. --initiative
	if r.Flag != "" {
		dir := filepath.Join(repoDir, r.Flag)
		if !isInitiative(dir) {
			return "", fmt.Errorf("initiative %q not found under %s", r.Flag, repoDir)
		}
		return dir, nil
	}
	// 3. current
	if b, err := os.ReadFile(filepath.Join(repoDir, "current")); err == nil {
		name := strings.TrimSpace(string(b))
		if name != "" {
			dir := filepath.Join(repoDir, name)
			if isInitiative(dir) {
				return dir, nil
			}
		}
	}
	// 4. exactly one active
	names, _ := ListInitiatives(repoDir)
	switch len(names) {
	case 1:
		return filepath.Join(repoDir, names[0]), nil
	case 0:
		return "", fmt.Errorf("no active initiative for %s (run `prstk init <slug>`)", repoKey)
	}
	return "", fmt.Errorf("multiple initiatives for %s; pass --initiative or `prstk use`: %s", repoKey, strings.Join(names, ", "))
}

func (r Resolver) byFlagAnyRepo() (string, error) {
	matches, _ := filepath.Glob(filepath.Join(r.Home, "*", r.Flag))
	var found []string
	for _, m := range matches {
		if isInitiative(m) {
			found = append(found, m)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", fmt.Errorf("initiative %q: %d matches", r.Flag, len(found))
}

func isInitiative(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "PLAN.md"))
	return err == nil
}

// ListInitiatives lists active initiative dir names under a repo dir.
func ListInitiatives(repoDir string) ([]string, error) {
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") && !strings.HasPrefix(e.Name(), ".") && isInitiative(filepath.Join(repoDir, e.Name())) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// SetCurrent writes (or with name=="" clears) <repo>/current.
func SetCurrent(repoDir, name string) error {
	p := filepath.Join(repoDir, "current")
	if name == "" {
		err := os.Remove(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return WriteFileAtomic(p, []byte(name+"\n"))
}

func ReadCurrent(repoDir string) string {
	b, _ := os.ReadFile(filepath.Join(repoDir, "current"))
	return strings.TrimSpace(string(b))
}
