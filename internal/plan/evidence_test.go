package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newEvidenceSpec(t *testing.T) *Spec {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "prs", "01-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Spec{ID: "01", Slug: "x", Title: "X", Status: Planned, Model: "sonnet", Dir: dir}
	s.InitBody("## Goal\nx\n")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return s
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEvidenceFieldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "spec.md"), []byte(sampleSpec), 0o644)
	s, err := LoadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Evidence != EvidenceNone {
		t.Fatalf("Evidence = %q, want empty", s.Evidence)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.SpecPath())
	if !strings.Contains(string(b), `evidence: ""`) {
		t.Fatalf("want evidence: \"\" in:\n%s", b)
	}
	s.Evidence = EvidenceVideo
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := LoadSpec(dir)
	if s2.Evidence != EvidenceVideo {
		t.Fatalf("reload Evidence = %q", s2.Evidence)
	}
}

func TestEvidenceWrittenAfterModel(t *testing.T) {
	s := newEvidenceSpec(t)
	s.Evidence = EvidenceScreenshots
	s.Save()
	b, _ := os.ReadFile(s.SpecPath())
	out := string(b)
	if !(strings.Index(out, "model:") < strings.Index(out, "evidence:") && strings.Index(out, "evidence:") < strings.Index(out, "worker:")) {
		t.Fatalf("evidence should sit between model and worker:\n%s", out)
	}
}

func TestSanitizeEvidenceName(t *testing.T) {
	long := strings.Repeat("a", 150)
	for in, want := range map[string]string{
		"Home Page.PNG":         "home-page.png",
		"../x.png":              "x.png",
		"a/b/c.JPG":             "c.jpg",
		"héllo wörld.webp":      "h-llo-w-rld.webp",
		"日本.png":                "--.png",
		".hidden.png":           "hidden.png",
		"...png":                "file.png", // ext ".png", stem ".." -> empty
		"a..b.png":              "a.b.png",
		"we!rd@name#.gif":       "we-rd-name-.gif",
		long + ".mp4":           strings.Repeat("a", 100) + ".mp4",
		"under_score-dash.WEBM": "under_score-dash.webm",
	} {
		if got := sanitizeEvidenceName(filepath.Base(in)); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
		got := sanitizeEvidenceName(filepath.Base(in))
		if strings.Contains(got, "/") || strings.Contains(got, "..") || strings.HasPrefix(got, ".") {
			t.Errorf("sanitize(%q) = %q has forbidden parts", in, got)
		}
	}
}

func TestAddEvidenceStoresAndDedups(t *testing.T) {
	s := newEvidenceSpec(t)
	src := writeTemp(t, "My Shot.PNG", "png-bytes")
	when := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	it, err := s.AddEvidence(src, EvidenceItem{Phase: PhaseBefore, Commit: "abc", Caption: "cap", CapturedAt: when})
	if err != nil {
		t.Fatal(err)
	}
	if it.File != "my-shot.png" || it.Kind != KindImage || it.Step != 1 || !it.CapturedAt.Equal(when) {
		t.Fatalf("bad item: %+v", it)
	}
	got, err := os.ReadFile(s.EvidenceFilePath(it))
	if err != nil || string(got) != "png-bytes" {
		t.Fatalf("stored file: %q %v", got, err)
	}
	if filepath.Dir(s.EvidenceFilePath(it)) != filepath.Join(s.Dir, "evidence", "before") {
		t.Fatalf("path: %s", s.EvidenceFilePath(it))
	}
	it2, err := s.AddEvidence(src, EvidenceItem{Phase: PhaseBefore, Commit: "abc"})
	if err != nil || it2.File != "my-shot-2.png" || it2.Step != 2 {
		t.Fatalf("second: %+v %v", it2, err)
	}
	it3, _ := s.AddEvidence(src, EvidenceItem{Phase: PhaseBefore, Commit: "abc"})
	if it3.File != "my-shot-3.png" {
		t.Fatalf("third: %+v", it3)
	}
	// same name in the other phase is independent
	it4, err := s.AddEvidence(src, EvidenceItem{Phase: PhaseAfter, Commit: "abc"})
	if err != nil || it4.File != "my-shot.png" || it4.Step != 1 {
		t.Fatalf("after: %+v %v", it4, err)
	}
	m, err := LoadEvidence(s)
	if err != nil || len(m.Items) != 4 {
		t.Fatalf("manifest: %+v %v", m, err)
	}
	if m.Items[0].Caption != "cap" || m.Items[0].Commit != "abc" {
		t.Fatalf("manifest item: %+v", m.Items[0])
	}
	// no temp files left behind
	ents, _ := os.ReadDir(filepath.Join(s.EvidenceDir(), "before"))
	if len(ents) != 3 {
		t.Fatalf("before dir has %d entries", len(ents))
	}
	ents, _ = os.ReadDir(s.EvidenceDir())
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
}

func TestAddEvidenceDedupAgainstManifestOnly(t *testing.T) {
	s := newEvidenceSpec(t)
	src := writeTemp(t, "a.png", "x")
	it, _ := s.AddEvidence(src, EvidenceItem{Phase: PhaseAfter, Commit: "c"})
	os.Remove(s.EvidenceFilePath(it)) // file gone, manifest still names it
	it2, err := s.AddEvidence(src, EvidenceItem{Phase: PhaseAfter, Commit: "c"})
	if err != nil || it2.File != "a-2.png" {
		t.Fatalf("got %+v %v", it2, err)
	}
}

func TestAddEvidenceStepAutoNumberPerPhase(t *testing.T) {
	s := newEvidenceSpec(t)
	src := writeTemp(t, "a.png", "x")
	add := func(ph Phase, step int) EvidenceItem {
		it, err := s.AddEvidence(src, EvidenceItem{Phase: ph, Step: step, Commit: "c"})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	if add(PhaseBefore, 0).Step != 1 || add(PhaseBefore, 0).Step != 2 {
		t.Fatal("before auto numbering")
	}
	if add(PhaseAfter, 0).Step != 1 {
		t.Fatal("after should start at 1 independently")
	}
	if add(PhaseAfter, 5).Step != 5 || add(PhaseAfter, 0).Step != 6 {
		t.Fatal("explicit step then auto")
	}
	if add(PhaseAfter, 1).Step != 1 { // re-capture of step 1
		t.Fatal("explicit step kept")
	}
}

func TestAddEvidenceRejects(t *testing.T) {
	s := newEvidenceSpec(t)
	good := writeTemp(t, "a.png", "x")
	ok := EvidenceItem{Phase: PhaseBefore, Commit: "c"}

	if _, err := s.AddEvidence(writeTemp(t, "notes.txt", "x"), ok); err == nil {
		t.Error("txt should be rejected")
	}
	if _, err := s.AddEvidence(writeTemp(t, "noext", "x"), ok); err == nil {
		t.Error("no extension should be rejected")
	}
	if _, err := s.AddEvidence(good, EvidenceItem{Phase: "during", Commit: "c"}); err == nil {
		t.Error("bad phase")
	}
	if _, err := s.AddEvidence(good, EvidenceItem{Phase: PhaseAfter}); err == nil {
		t.Error("missing commit")
	}
	if _, err := s.AddEvidence(good, EvidenceItem{Phase: PhaseAfter, Commit: "c", Step: -1}); err == nil {
		t.Error("negative step")
	}
	if _, err := s.AddEvidence(filepath.Join(t.TempDir(), "nope.png"), ok); err == nil {
		t.Error("missing source")
	}
	dir := filepath.Join(t.TempDir(), "d.png")
	os.Mkdir(dir, 0o755)
	if _, err := s.AddEvidence(dir, ok); err == nil {
		t.Error("directory source")
	}
	link := filepath.Join(t.TempDir(), "link.png")
	if err := os.Symlink(good, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := s.AddEvidence(link, ok); err == nil {
		t.Error("symlink source should be rejected")
	}
	if _, err := os.Stat(s.EvidenceManifestPath()); err == nil {
		t.Error("manifest written despite only failures")
	}
}

func TestAddEvidenceOversize(t *testing.T) {
	old := maxEvidenceBytes
	maxEvidenceBytes = 10
	defer func() { maxEvidenceBytes = old }()
	s := newEvidenceSpec(t)
	if _, err := s.AddEvidence(writeTemp(t, "big.png", "01234567890"), EvidenceItem{Phase: PhaseBefore, Commit: "c"}); err == nil {
		t.Fatal("11 bytes should exceed a 10 byte limit")
	}
	if _, err := s.AddEvidence(writeTemp(t, "ok.png", "0123456789"), EvidenceItem{Phase: PhaseBefore, Commit: "c"}); err != nil {
		t.Fatalf("10 bytes should fit: %v", err)
	}
	ents, _ := os.ReadDir(filepath.Join(s.EvidenceDir(), "before"))
	if len(ents) != 1 {
		t.Fatalf("want only ok.png, got %d entries", len(ents))
	}
	// the real limit is 200 MiB; a sparse file checks it without writing data
	maxEvidenceBytes = old
	sparse := filepath.Join(t.TempDir(), "huge.mp4")
	f, _ := os.Create(sparse)
	if err := f.Truncate(MaxEvidenceBytes + 1); err != nil {
		f.Close()
		t.Skip("sparse files unavailable")
	}
	f.Close()
	if _, err := s.AddEvidence(sparse, EvidenceItem{Phase: PhaseAfter, Commit: "c"}); err == nil {
		t.Fatal("sparse file over MaxEvidenceBytes should be rejected")
	}
}

func TestSetEvidenceScript(t *testing.T) {
	s := newEvidenceSpec(t)
	if _, err := s.SetEvidenceScript(writeTemp(t, "cap.png", "x")); err == nil {
		t.Error("png is not a script")
	}
	if _, err := s.SetEvidenceScript(writeTemp(t, "big.mjs", strings.Repeat("x", 1<<20+1))); err == nil {
		t.Error("script over 1 MiB")
	}
	name, err := s.SetEvidenceScript(writeTemp(t, "Capture Flow.MJS", "console.log(1)"))
	if err != nil || name != "capture-flow.mjs" {
		t.Fatalf("got %q %v", name, err)
	}
	m, _ := LoadEvidence(s)
	if m.Script != name || s.EvidenceScriptPath(m) != filepath.Join(s.Dir, "evidence", "script", name) {
		t.Fatalf("manifest: %+v path %s", m, s.EvidenceScriptPath(m))
	}
	name2, err := s.SetEvidenceScript(writeTemp(t, "capture-flow.mjs", "v2"))
	if err != nil || name2 != "capture-flow-2.mjs" {
		t.Fatalf("second: %q %v", name2, err)
	}
	m, _ = LoadEvidence(s)
	if m.Script != name2 {
		t.Fatalf("script not replaced: %+v", m)
	}
	if _, err := os.Stat(filepath.Join(s.EvidenceDir(), "script", name)); err != nil {
		t.Fatalf("old script should stay on disk: %v", err)
	}
	if s.EvidenceScriptPath(EvidenceManifest{}) != "" {
		t.Fatal("no script -> empty path")
	}
	// script and items coexist
	if _, err := s.AddEvidence(writeTemp(t, "a.png", "x"), EvidenceItem{Phase: PhaseBefore, Commit: "c"}); err != nil {
		t.Fatal(err)
	}
	m, _ = LoadEvidence(s)
	if m.Script != name2 || len(m.Items) != 1 {
		t.Fatalf("manifest lost data: %+v", m)
	}
}

func TestLoadEvidenceMissing(t *testing.T) {
	s := newEvidenceSpec(t)
	m, err := LoadEvidence(s)
	if err != nil || m.Items == nil || len(m.Items) != 0 || m.Script != "" {
		t.Fatalf("got %+v %v", m, err)
	}
	os.MkdirAll(s.EvidenceDir(), 0o755)
	if m, err = LoadEvidence(s); err != nil || m.Items == nil {
		t.Fatalf("empty dir: %+v %v", m, err)
	}
	os.WriteFile(s.EvidenceManifestPath(), []byte(`{"items":null}`), 0o644)
	if m, err = LoadEvidence(s); err != nil || m.Items == nil {
		t.Fatalf("null items: %+v %v", m, err)
	}
	os.WriteFile(s.EvidenceManifestPath(), []byte(`{not json`), 0o644)
	if _, err = LoadEvidence(s); err == nil {
		t.Fatal("corrupt manifest should error")
	}
}

func TestEvidenceKindOf(t *testing.T) {
	for name, want := range map[string]EvidenceKind{
		"a.png": KindImage, "a.JPG": KindImage, "a.jpeg": KindImage, "a.webp": KindImage, "a.gif": KindImage,
		"a.webm": KindVideo, "a.MP4": KindVideo,
	} {
		if k, ok := EvidenceKindOf(name); !ok || k != want {
			t.Errorf("%s: %q %v", name, k, ok)
		}
	}
	for _, name := range []string{"a.txt", "a", "a.svg", "png"} {
		if _, ok := EvidenceKindOf(name); ok {
			t.Errorf("%s should not be evidence", name)
		}
	}
}

func TestPairsLookupHasKind(t *testing.T) {
	mk := func(ph Phase, step int, file string, k EvidenceKind) EvidenceItem {
		return EvidenceItem{Phase: ph, Step: step, File: file, Kind: k}
	}
	m := EvidenceManifest{Items: []EvidenceItem{
		mk(PhaseAfter, 2, "a2-r1.png", KindImage),
		mk(PhaseBefore, 1, "b1.png", KindImage),
		mk(PhaseAfter, 1, "a1-r1.png", KindImage),
		mk(PhaseBefore, 2, "b2.webm", KindVideo),
		mk(PhaseAfter, 1, "a1-r2.png", KindImage),
		mk(PhaseAfter, 1, "a1-r3.png", KindImage),
		mk(PhaseAfter, 3, "a3.png", KindImage),
	}}
	pairs := m.Pairs()
	if len(pairs) != 3 || pairs[0].Step != 1 || pairs[1].Step != 2 || pairs[2].Step != 3 {
		t.Fatalf("pairs: %+v", pairs)
	}
	p1 := pairs[0]
	if p1.Before.File != "b1.png" || p1.After.File != "a1-r3.png" || len(p1.Earlier) != 2 ||
		p1.Earlier[0].File != "a1-r1.png" || p1.Earlier[1].File != "a1-r2.png" {
		t.Fatalf("pair 1: %+v", p1)
	}
	if pairs[1].Before.File != "b2.webm" || pairs[1].After.File != "a2-r1.png" || len(pairs[1].Earlier) != 0 {
		t.Fatalf("pair 2: %+v", pairs[1])
	}
	if pairs[2].Before != nil || pairs[2].After.File != "a3.png" {
		t.Fatalf("pair 3: %+v", pairs[2])
	}
	if len((EvidenceManifest{}).Pairs()) != 0 {
		t.Fatal("empty manifest has no pairs")
	}

	if it, ok := m.Lookup(PhaseAfter, "a1-r2.png"); !ok || it.Step != 1 {
		t.Fatalf("lookup: %+v %v", it, ok)
	}
	if _, ok := m.Lookup(PhaseBefore, "a1-r2.png"); ok {
		t.Fatal("lookup must match the phase")
	}
	if _, ok := m.Lookup(PhaseAfter, "A1-R2.PNG"); ok {
		t.Fatal("lookup is exact")
	}

	if !m.HasKind(PhaseBefore, KindVideo) || m.HasKind(PhaseAfter, KindVideo) ||
		!m.HasKind(PhaseAfter, KindImage) || (EvidenceManifest{}).HasKind(PhaseBefore, KindImage) {
		t.Fatal("HasKind")
	}
}

func TestEvidenceFilePathStaysInside(t *testing.T) {
	s := &Spec{Dir: "/p/01-x"}
	got := s.EvidenceFilePath(EvidenceItem{Phase: PhaseAfter, File: "../../etc/passwd"})
	if got != "/p/01-x/evidence/after/passwd" {
		t.Fatalf("got %s", got)
	}
}

func TestEvidenceSurvivesArchive(t *testing.T) {
	repo := t.TempDir()
	prDir := filepath.Join(repo, "2026-11-04-1200-init", "prs", "01-x")
	os.MkdirAll(prDir, 0o755)
	s := &Spec{ID: "01", Slug: "x", Title: "X", Status: Merged, Model: "sonnet", Dir: prDir}
	s.InitBody("## Goal\nx\n")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEvidence(writeTemp(t, "shot.png", "img"), EvidenceItem{Phase: PhaseAfter, Commit: "deadbeef", Caption: "done"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEvidenceScript(writeTemp(t, "cap.tape", "Output x.webm")); err != nil {
		t.Fatal(err)
	}
	// teardown renames the initiative dir into _archive
	dst := ArchivePath(repo, "2026-11-04-1200-init")
	os.MkdirAll(ArchiveRoot(repo), 0o755)
	if err := os.Rename(filepath.Join(repo, "2026-11-04-1200-init"), dst); err != nil {
		t.Fatal(err)
	}
	as, err := LoadSpec(filepath.Join(dst, "prs", "01-x"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := LoadEvidence(as)
	if err != nil || len(m.Items) != 1 || m.Script != "cap.tape" {
		t.Fatalf("archived manifest: %+v %v", m, err)
	}
	if b, err := os.ReadFile(as.EvidenceFilePath(m.Items[0])); err != nil || string(b) != "img" {
		t.Fatalf("archived file: %q %v", b, err)
	}
	if !strings.HasPrefix(as.EvidenceFilePath(m.Items[0]), dst) {
		t.Fatalf("path not under archive: %s", as.EvidenceFilePath(m.Items[0]))
	}
}

func TestLatestDirectorVerdict(t *testing.T) {
	spec := func(body string) *Spec {
		s := &Spec{}
		s.InitBody(body)
		return s
	}
	for name, tc := range map[string]struct {
		body  string
		round int
		verd  string
	}{
		"no section":           {"## Goal\nx\n", 0, ""},
		"no rounds":            {"## Review: Director\n\nnothing yet\n\n## Review: Dalton\n", 0, ""},
		"one round":            {"## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n", 1, "approved"},
		"several":              {"## Review: Director\n\n### Round 1 (2026-11-02): changes-requested\n\nfix\n\n### Round 2 (2026-11-03): approved\n\nok\n\n## Review: Dalton\n\n### Round 1\nhuman\n", 2, "approved"},
		"human rounds ignored": {"## Review: Director\n\n### Round 1 (2026-11-02): changes-requested\n\n## Review: Dalton\n\n### Round 3 (2026-11-05): approved\n", 1, "changes-requested"},
		"fenced round ignored": {"## Review: Director\n\n### Round 1 (2026-11-02): approved\n\n```\n### Round 2 (2026-11-03): rejected\n```\n", 1, "approved"},
		"spaces":               {"## Review: Director  \n\n### Round 4 (2026-11-02):   needs work  \n", 4, "needs work"},
		"before other":         {"## Review: Director\n\n### Round 1 (2026-11-02): approved\n\n## Notes\n\n### Round 9 (2026-11-02): x\n", 1, "approved"},
	} {
		r, v := spec(tc.body).LatestDirectorVerdict()
		if r != tc.round || v != tc.verd {
			t.Errorf("%s: got (%d, %q), want (%d, %q)", name, r, v, tc.round, tc.verd)
		}
	}
	// matches what AppendDirectorRound writes
	s := newEvidenceSpec(t)
	if _, err := s.AppendDirectorRound(time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), "changes-requested", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendDirectorRound(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC), "approved", "y"); err != nil {
		t.Fatal(err)
	}
	if r, v := s.LatestDirectorVerdict(); r != 2 || v != "approved" {
		t.Fatalf("after AppendDirectorRound: (%d, %q)", r, v)
	}
}

func TestValidateEvidence(t *testing.T) {
	mk := func(ev, body string) *Initiative {
		s := &Spec{ID: "01", Slug: "x", Status: Planned, Model: "sonnet", Evidence: ev}
		s.InitBody(body)
		return &Initiative{PRs: []*Spec{s}}
	}
	count := func(in *Initiative) (errs, warns int) {
		return CountErrors(in.Validate(time.Now()))
	}
	for name, tc := range map[string]struct {
		ev, body    string
		errs, warns int
	}{
		"none":                     {"", "## Goal\nx\n", 0, 0},
		"screenshots with section": {"screenshots", "## Goal\nx\n\n## Evidence\nShow the page.\n", 0, 0},
		"video with section":       {"video", "## Evidence\nrecord it\n\n## Goal\nx\n", 0, 0},
		"screenshots no section":   {"screenshots", "## Goal\nx\n", 0, 1},
		"video empty section":      {"video", "## Evidence\n\n   \n## Goal\nx\n", 0, 1},
		"empty section at end":     {"screenshots", "## Goal\nx\n\n## Evidence\n", 0, 1},
		"unknown":                  {"gif", "## Evidence\nstuff\n", 1, 0},
		"unknown no section":       {"Screenshots", "## Goal\nx\n", 1, 0},
	} {
		in := mk(tc.ev, tc.body)
		// parent-less planned spec with no worktree: nothing else fires
		if e, w := count(in); e != tc.errs || w != tc.warns {
			t.Errorf("%s: got %d errors %d warnings, want %d/%d: %v", name, e, w, tc.errs, tc.warns, in.Validate(time.Now()))
		}
	}
}
