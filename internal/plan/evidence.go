package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Evidence levels (Spec.Evidence).
const (
	EvidenceNone        = ""
	EvidenceScreenshots = "screenshots"
	EvidenceVideo       = "video"
)

// Phase is which side of a change a capture shows.
type Phase string

const (
	PhaseBefore Phase = "before"
	PhaseAfter  Phase = "after"
)

// EvidenceKind is the media type of a capture.
type EvidenceKind string

const (
	KindImage EvidenceKind = "image"
	KindVideo EvidenceKind = "video"
)

// MaxEvidenceBytes is the largest media file AddEvidence accepts.
const MaxEvidenceBytes = 200 << 20

// maxEvidenceScriptBytes is the largest capture script SetEvidenceScript accepts.
const maxEvidenceScriptBytes = 1 << 20

// maxEvidenceBytes is MaxEvidenceBytes; a variable so tests can lower it.
var maxEvidenceBytes int64 = MaxEvidenceBytes

// maxEvidenceStem is the longest sanitized file stem (before the extension), in bytes.
const maxEvidenceStem = 100

const evidenceManifestName = "evidence.json"

// EvidenceItem is one capture in the manifest.
type EvidenceItem struct {
	Phase      Phase        `json:"phase"`
	Step       int          `json:"step"` // 1-based; a before and after with the same Step form a pair
	File       string       `json:"file"` // stored basename inside evidence/<phase>/
	Kind       EvidenceKind `json:"kind"`
	Caption    string       `json:"caption"`
	Commit     string       `json:"commit"` // full sha of the code that was captured
	CapturedAt time.Time    `json:"captured_at"`
}

// EvidenceManifest is evidence/evidence.json. Only prstk writes it.
type EvidenceManifest struct {
	Script string         `json:"script,omitempty"` // stored basename inside evidence/script/
	Items  []EvidenceItem `json:"items"`            // append-only, in the order added
}

// EvidencePair is one step's before/after pair, as the board shows it.
type EvidencePair struct {
	Step    int
	Before  *EvidenceItem  // latest before for this step, nil if none
	After   *EvidenceItem  // latest after for this step, nil if none
	Earlier []EvidenceItem // older after items for this step (superseded rounds), oldest first
}

// EvidenceDir is <Dir>/evidence.
func (s *Spec) EvidenceDir() string { return filepath.Join(s.Dir, "evidence") }

// EvidenceManifestPath is <Dir>/evidence/evidence.json.
func (s *Spec) EvidenceManifestPath() string {
	return filepath.Join(s.EvidenceDir(), evidenceManifestName)
}

// EvidenceFilePath is <Dir>/evidence/<phase>/<file>. Only the base names of
// the phase and file are used, so a hand-edited manifest can't point outside
// the evidence dir.
func (s *Spec) EvidenceFilePath(it EvidenceItem) string {
	return filepath.Join(s.EvidenceDir(), filepath.Base(string(it.Phase)), filepath.Base(it.File))
}

// EvidenceScriptPath is <Dir>/evidence/script/<m.Script>, or "" if the
// manifest has no script.
func (s *Spec) EvidenceScriptPath(m EvidenceManifest) string {
	if m.Script == "" {
		return ""
	}
	return filepath.Join(s.EvidenceDir(), "script", filepath.Base(m.Script))
}

// LoadEvidence reads the manifest. A missing evidence dir or manifest returns
// an empty manifest (Items non-nil) and a nil error.
func LoadEvidence(s *Spec) (EvidenceManifest, error) {
	m := EvidenceManifest{Items: []EvidenceItem{}}
	data, err := os.ReadFile(s.EvidenceManifestPath())
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return EvidenceManifest{Items: []EvidenceItem{}}, fmt.Errorf("%s: %w", s.EvidenceManifestPath(), err)
	}
	if m.Items == nil {
		m.Items = []EvidenceItem{}
	}
	return m, nil
}

func (s *Spec) saveEvidence(m EvidenceManifest) error {
	if m.Items == nil {
		m.Items = []EvidenceItem{}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.EvidenceDir(), 0o755); err != nil {
		return err
	}
	return WriteFileAtomic(s.EvidenceManifestPath(), append(data, '\n'))
}

// EvidenceKindOf classifies a file name by extension (case-insensitive).
func EvidenceKindOf(name string) (EvidenceKind, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
		return KindImage, true
	case ".webm", ".mp4":
		return KindVideo, true
	}
	return "", false
}

func isEvidenceScript(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mjs", ".js", ".ts", ".tape":
		return true
	}
	return false
}

// sanitizeEvidenceName returns the stored name for src's basename: lowercased,
// runes outside [a-z0-9._-] replaced by '-', no leading '.', the stem cut to
// maxEvidenceStem bytes, never containing "..". ext is the lowercased
// extension of the original name (with dot).
func sanitizeEvidenceName(base string) string {
	ext := strings.ToLower(filepath.Ext(base))
	stem := strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	stem = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, stem)
	for strings.Contains(stem, "..") {
		stem = strings.ReplaceAll(stem, "..", ".")
	}
	stem = strings.Trim(stem, ".")
	if len(stem) > maxEvidenceStem {
		stem = strings.Trim(stem[:maxEvidenceStem], ".")
	}
	if stem == "" {
		stem = "file"
	}
	return stem + ext
}

// uniqueEvidenceName appends -2, -3, ... before the extension until name is
// neither a file in dir nor in taken.
func uniqueEvidenceName(dir, name string, taken map[string]bool) string {
	exists := func(n string) bool {
		if taken[n] {
			return true
		}
		_, err := os.Lstat(filepath.Join(dir, n))
		return err == nil
	}
	if !exists(name) {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		n := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if !exists(n) {
			return n
		}
	}
}

// checkEvidenceSource verifies src is a regular, non-symlink file no larger than max.
func checkEvidenceSource(src string, max int64) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", src)
	}
	if fi.Size() > max {
		return fmt.Errorf("%s: %d bytes exceeds the %d byte limit", src, fi.Size(), max)
	}
	return nil
}

// copyEvidenceFile copies src to dir/name atomically (temp file in dir, then
// rename). It refuses to read more than max bytes.
func copyEvidenceFile(src, dir, name string, max int64) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, "."+name+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	n, err := io.Copy(tmp, io.LimitReader(in, max+1))
	if err != nil {
		return fail(err)
	}
	if n > max {
		return fail(fmt.Errorf("%s: exceeds the %d byte limit", src, max))
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// AddEvidence validates src (extension, regular file, size), copies it
// atomically into evidence/<it.Phase>/ under its sanitized, de-duplicated
// name, fills it.File and it.Kind, and appends it to the manifest. If
// it.Step is 0 it becomes max(existing Step in that phase)+1. Phase must be
// before or after; Commit must be non-empty. A zero CapturedAt is set to the
// current time (UTC). Returns the stored item.
func (s *Spec) AddEvidence(src string, it EvidenceItem) (EvidenceItem, error) {
	if it.Phase != PhaseBefore && it.Phase != PhaseAfter {
		return it, fmt.Errorf("evidence phase must be %q or %q, got %q", PhaseBefore, PhaseAfter, it.Phase)
	}
	if strings.TrimSpace(it.Commit) == "" {
		return it, errors.New("evidence commit is required")
	}
	if it.Step < 0 {
		return it, fmt.Errorf("evidence step must be positive, got %d", it.Step)
	}
	base := filepath.Base(src)
	kind, ok := EvidenceKindOf(base)
	if !ok {
		return it, fmt.Errorf("%s: unsupported evidence type %q (want .png .jpg .jpeg .webp .gif .webm .mp4)", src, filepath.Ext(base))
	}
	if err := checkEvidenceSource(src, maxEvidenceBytes); err != nil {
		return it, err
	}
	m, err := LoadEvidence(s)
	if err != nil {
		return it, err
	}
	if it.Step == 0 {
		for _, o := range m.Items {
			if o.Phase == it.Phase && o.Step > it.Step {
				it.Step = o.Step
			}
		}
		it.Step++
	}
	dir := filepath.Join(s.EvidenceDir(), string(it.Phase))
	taken := map[string]bool{}
	for _, o := range m.Items {
		if o.Phase == it.Phase {
			taken[o.File] = true
		}
	}
	name := uniqueEvidenceName(dir, sanitizeEvidenceName(base), taken)
	if err := copyEvidenceFile(src, dir, name, maxEvidenceBytes); err != nil {
		return it, err
	}
	it.File, it.Kind = name, kind
	if it.CapturedAt.IsZero() {
		it.CapturedAt = time.Now().UTC()
	}
	m.Items = append(m.Items, it)
	if err := s.saveEvidence(m); err != nil {
		os.Remove(filepath.Join(dir, name))
		return it, err
	}
	return it, nil
}

// SetEvidenceScript copies a capture script into evidence/script/ and sets
// Manifest.Script (replacing the previous one; the old file stays on disk).
// It returns the stored name.
func (s *Spec) SetEvidenceScript(src string) (string, error) {
	base := filepath.Base(src)
	if !isEvidenceScript(base) {
		return "", fmt.Errorf("%s: unsupported script type %q (want .mjs .js .ts .tape)", src, filepath.Ext(base))
	}
	if err := checkEvidenceSource(src, maxEvidenceScriptBytes); err != nil {
		return "", err
	}
	m, err := LoadEvidence(s)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(s.EvidenceDir(), "script")
	name := uniqueEvidenceName(dir, sanitizeEvidenceName(base), nil)
	if err := copyEvidenceFile(src, dir, name, maxEvidenceScriptBytes); err != nil {
		return "", err
	}
	m.Script = name
	if err := s.saveEvidence(m); err != nil {
		os.Remove(filepath.Join(dir, name))
		return "", err
	}
	return name, nil
}

// Pairs groups items by Step across both phases, sorted by Step. Within a
// step "latest" means the last one added; older afters go to Earlier.
func (m EvidenceManifest) Pairs() []EvidencePair {
	byStep := map[int]*EvidencePair{}
	var steps []int
	for _, it := range m.Items {
		p := byStep[it.Step]
		if p == nil {
			p = &EvidencePair{Step: it.Step}
			byStep[it.Step] = p
			steps = append(steps, it.Step)
		}
		it := it
		switch it.Phase {
		case PhaseBefore:
			p.Before = &it
		case PhaseAfter:
			if p.After != nil {
				p.Earlier = append(p.Earlier, *p.After)
			}
			p.After = &it
		}
	}
	sort.Ints(steps)
	out := make([]EvidencePair, 0, len(steps))
	for _, n := range steps {
		out = append(out, *byStep[n])
	}
	return out
}

// Lookup finds the item stored as file in phase (exact match on File).
func (m EvidenceManifest) Lookup(phase Phase, file string) (EvidenceItem, bool) {
	for _, it := range m.Items {
		if it.Phase == phase && it.File == file {
			return it, true
		}
	}
	return EvidenceItem{}, false
}

// HasKind reports whether any item in phase is of kind k.
func (m EvidenceManifest) HasKind(phase Phase, k EvidenceKind) bool {
	for _, it := range m.Items {
		if it.Phase == phase && it.Kind == k {
			return true
		}
	}
	return false
}
