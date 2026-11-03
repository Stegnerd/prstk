package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Spec is one PR: prs/<id>-<slug>/spec.md frontmatter.
type Spec struct {
	ID            string
	Slug          string
	Title         string
	Parent        string // "" = stacks on base_branch
	ConflictsWith []string
	Status        Status
	StatusSince   time.Time
	BlockedReason string
	BlockedFrom   Status
	EarlyStart    bool
	Model         string
	Worker        string
	Branch        string
	PRNumber      int

	Dir string // absolute path of prs/<id>-<slug>
	doc *Doc
}

type specYAML struct {
	ID            string    `yaml:"id"`
	Slug          string    `yaml:"slug"`
	Title         string    `yaml:"title"`
	Parent        *string   `yaml:"parent"`
	ConflictsWith []string  `yaml:"conflicts_with"`
	Status        string    `yaml:"status"`
	StatusSince   time.Time `yaml:"status_since"`
	BlockedReason string    `yaml:"blocked_reason"`
	BlockedFrom   string    `yaml:"blocked_from"`
	EarlyStart    bool      `yaml:"early_start"`
	Model         string    `yaml:"model"`
	Worker        string    `yaml:"worker"`
	Branch        string    `yaml:"branch"`
	PRNumber      int       `yaml:"pr_number"`
}

func (s *Spec) SpecPath() string  { return filepath.Join(s.Dir, "spec.md") }
func (s *Spec) NotesPath() string { return filepath.Join(s.Dir, "notes.md") }
func (s *Spec) Body() string {
	if s.doc == nil {
		return ""
	}
	return s.doc.Body
}

// Name is the human label used in stack nav and boards: slug with spaces.
func (s *Spec) Name() string { return strings.ReplaceAll(s.Slug, "-", " ") }

// InitBody sets the body for a spec that hasn't been written yet.
func (s *Spec) InitBody(body string) { s.doc = NewDoc(s.SpecPath(), body) }

// SetStatus changes status and stamps status_since.
func (s *Spec) SetStatus(st Status, now time.Time) {
	if s.Status != st {
		s.Status = st
		s.StatusSince = now
	}
}

func (s *Spec) node() *yaml.Node {
	b := newMap().
		add("id", quoted(s.ID)).
		add("slug", scalar(s.Slug)).
		add("title", scalar(s.Title)).
		add("parent", nullable(s.Parent)).
		add("conflicts_with", quotedList(s.ConflictsWith)).
		add("status", scalar(string(s.Status))).
		add("status_since", scalar(s.StatusSince.Truncate(time.Second))).
		add("blocked_reason", scalar(s.BlockedReason)).
		add("blocked_from", scalar(string(s.BlockedFrom))).
		add("early_start", scalar(s.EarlyStart)).
		add("model", scalar(s.Model)).
		add("worker", scalar(s.Worker)).
		add("branch", scalar(s.Branch)).
		add("pr_number", scalar(s.PRNumber))
	return b.n
}

// Save writes the spec frontmatter atomically, leaving the body untouched
// (the body is re-read from disk first so concurrent director edits survive).
func (s *Spec) Save() error {
	if s.doc == nil {
		s.doc = NewDoc(s.SpecPath(), "")
	} else if fresh, err := LoadDoc(s.SpecPath()); err == nil {
		s.doc = fresh
	}
	s.doc.Set(s.node())
	return s.doc.Save()
}

// LoadSpec parses dir/spec.md.
func LoadSpec(dir string) (*Spec, error) {
	d, err := LoadDoc(filepath.Join(dir, "spec.md"))
	if err != nil {
		return nil, err
	}
	var y specYAML
	if err := d.Decode(&y); err != nil {
		return nil, err
	}
	s := &Spec{
		ID: y.ID, Slug: y.Slug, Title: y.Title, ConflictsWith: y.ConflictsWith,
		Status: Status(y.Status), StatusSince: y.StatusSince, BlockedReason: y.BlockedReason,
		BlockedFrom: Status(y.BlockedFrom), EarlyStart: y.EarlyStart, Model: y.Model,
		Worker: y.Worker, Branch: y.Branch, PRNumber: y.PRNumber, Dir: dir, doc: d,
	}
	if y.Parent != nil {
		s.Parent = *y.Parent
	}
	if s.Model == "" {
		s.Model = "sonnet"
	}
	return s, nil
}

// PlanMeta is PLAN.md frontmatter.
type PlanMeta struct {
	Initiative string    `yaml:"initiative"`
	Repo       string    `yaml:"repo"`
	BaseBranch string    `yaml:"base_branch"`
	Created    time.Time `yaml:"created"`
	MaxWorkers int       `yaml:"max_workers"`
	RepoPath   string    `yaml:"repo_path"`
}

func (m PlanMeta) node() *yaml.Node {
	return newMap().
		add("initiative", scalar(m.Initiative)).
		add("repo", scalar(m.Repo)).
		add("base_branch", scalar(m.BaseBranch)).
		add("created", scalar(m.Created.Truncate(time.Second))).
		add("max_workers", scalar(m.MaxWorkers)).
		add("repo_path", scalar(m.RepoPath)).n
}

// Initiative is one loaded plan directory.
type Initiative struct {
	Dir     string // ~/.pr-plans/<repo>/<name>
	Name    string
	RepoKey string
	Meta    PlanMeta
	PRs     []*Spec // sorted by id
	Config  Config

	// LoadErrors are problems found while loading that validation reports.
	LoadErrors []string

	planDoc *Doc
}

func (in *Initiative) PlanPath() string    { return filepath.Join(in.Dir, "PLAN.md") }
func (in *Initiative) LogPath() string     { return filepath.Join(in.Dir, "LOG.md") }
func (in *Initiative) BoardPath() string   { return filepath.Join(in.Dir, "board.html") }
func (in *Initiative) VerbosePath() string { return filepath.Join(in.Dir, ".prstk.log") }
func (in *Initiative) LockPath() string    { return filepath.Join(in.Dir, ".lock") }
func (in *Initiative) PRsDir() string      { return filepath.Join(in.Dir, "prs") }
func (in *Initiative) RepoDir() string     { return filepath.Dir(in.Dir) }
func (in *Initiative) Worktree(id string) string {
	return filepath.Join(in.Dir, "worktrees", id)
}

func (in *Initiative) MaxWorkers() int {
	if in.Meta.MaxWorkers > 0 {
		return in.Meta.MaxWorkers
	}
	if in.Config.MaxWorkers > 0 {
		return in.Config.MaxWorkers
	}
	return 2
}

func (in *Initiative) PlanBody() string {
	if in.planDoc == nil {
		return ""
	}
	return in.planDoc.Body
}

// SetPlanBody replaces PLAN.md's body (used for the generated block) and
// saves. Frontmatter is re-read so hand edits survive.
func (in *Initiative) SavePlan(body func(old string) string) error {
	d, err := LoadDoc(in.PlanPath())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		d = NewDoc(in.PlanPath(), "")
	}
	d.Set(in.Meta.node())
	if body != nil {
		d.Body = body(d.Body)
	}
	in.planDoc = d
	return d.Save()
}

func (in *Initiative) PR(id string) *Spec {
	for _, p := range in.PRs {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (in *Initiative) Children(id string) []*Spec {
	var out []*Spec
	for _, p := range in.PRs {
		if p.Parent == id {
			out = append(out, p)
		}
	}
	return out
}

var prDirRe = regexp.MustCompile(`^([0-9A-Za-z]+)-(.+)$`)

// LoadInitiative loads everything under dir. Structural problems (bad
// frontmatter, missing files, duplicate ids) are collected in LoadErrors.
func LoadInitiative(dir string) (*Initiative, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	in := &Initiative{Dir: dir, Name: filepath.Base(dir), RepoKey: filepath.Base(filepath.Dir(dir))}
	d, err := LoadDoc(in.PlanPath())
	if err != nil {
		return nil, fmt.Errorf("load PLAN.md: %w", err)
	}
	if err := d.Decode(&in.Meta); err != nil {
		return nil, err
	}
	in.planDoc = d
	in.Config, err = LoadConfig(in.RepoDir())
	if err != nil {
		in.LoadErrors = append(in.LoadErrors, err.Error())
	}

	entries, err := os.ReadDir(in.PRsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	seen := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pdir := filepath.Join(in.PRsDir(), e.Name())
		m := prDirRe.FindStringSubmatch(e.Name())
		if _, err := os.Stat(filepath.Join(pdir, "spec.md")); err != nil {
			in.LoadErrors = append(in.LoadErrors, fmt.Sprintf("prs/%s: missing spec.md", e.Name()))
			continue
		}
		s, err := LoadSpec(pdir)
		if err != nil {
			in.LoadErrors = append(in.LoadErrors, err.Error())
			continue
		}
		if _, err := os.Stat(s.NotesPath()); err != nil {
			in.LoadErrors = append(in.LoadErrors, fmt.Sprintf("%s: missing notes.md", s.ID))
		}
		if m == nil || m[1] != s.ID {
			in.LoadErrors = append(in.LoadErrors, fmt.Sprintf("prs/%s: id %q doesn't match directory prefix", e.Name(), s.ID))
		}
		if prev, ok := seen[s.ID]; ok {
			in.LoadErrors = append(in.LoadErrors, fmt.Sprintf("duplicate id %q (prs/%s and prs/%s)", s.ID, prev, e.Name()))
			continue
		}
		seen[s.ID] = e.Name()
		in.PRs = append(in.PRs, s)
	}
	sort.Slice(in.PRs, func(i, j int) bool { return in.PRs[i].ID < in.PRs[j].ID })
	return in, nil
}

// Config is the per-repo config.yaml.
type Config struct {
	DefaultBase string `yaml:"default_base"`
	MaxWorkers  int    `yaml:"max_workers"`
	Setup       string `yaml:"setup"`
	Reviewer    string `yaml:"reviewer"` // human reviewer's name; "" means "Human"
}

// DefaultReviewer is the human reviewer's name when config.yaml sets none.
const DefaultReviewer = "Human"

// ReviewerName returns the trimmed Reviewer, or "Human" when it is blank.
func (c Config) ReviewerName() string {
	if n := strings.TrimSpace(c.Reviewer); n != "" {
		return n
	}
	return DefaultReviewer
}

func LoadConfig(repoDir string) (Config, error) {
	var c Config
	data, err := os.ReadFile(filepath.Join(repoDir, "config.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("config.yaml: %w", err)
	}
	return c, nil
}
