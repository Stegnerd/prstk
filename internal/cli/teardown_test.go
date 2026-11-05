package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stegnerd/prstk/internal/plan"
)

// teardownSetup creates an initiative with two PRs that each have a worktree
// and filled-in notes, both abandoned so teardown is allowed.
func teardownSetup(t *testing.T) (*env, *plan.Initiative) {
	t.Helper()
	e := newEnv(t)
	e.run("init", "tiny")
	e.run("add", "01", "one", "--title", "One")
	e.run("add", "02", "two", "--title", "Two")
	for _, id := range []string{"01", "02"} {
		e.run("assign", id, "--worker", "w-"+id)
		p := e.initiative().PR(id)
		if err := os.WriteFile(p.NotesPath(), []byte("## Description\nnotes for "+id+"\n\n## Worker notes\nnone\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		e.run("status", id, "abandoned")
	}
	return e, e.initiative()
}

// teardownSnapshot maps every spec.md/notes.md under an initiative dir (by
// path relative to it) to its contents.
func teardownSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, pat := range []string{"prs/*/spec.md", "prs/*/notes.md"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pat))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			rel, _ := filepath.Rel(dir, m)
			files[rel] = string(b)
		}
	}
	return files
}

// teardownCheckArchived asserts the archive-layout contract for in.
func teardownCheckArchived(t *testing.T, e *env, in *plan.Initiative, want map[string]string) {
	t.Helper()
	arch := plan.ArchivePath(in.RepoDir(), in.Name)
	if _, err := os.Stat(in.Dir); !os.IsNotExist(err) {
		t.Fatalf("initiative dir still exists: %v", err)
	}
	got := teardownSnapshot(t, arch)
	if len(want) != 4 || len(got) != len(want) {
		t.Fatalf("archived spec/notes files: got %d, want %d (%v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: archived content differs\ngot:  %q\nwant: %q", k, got[k], v)
		}
	}
	for _, f := range []string{"PLAN.md", "LOG.md", "board.html"} {
		if _, err := os.Stat(filepath.Join(arch, f)); err != nil {
			t.Errorf("archive missing %s: %v", f, err)
		}
	}
	for _, f := range []string{"worktrees", ".lock", ".prstk.log"} {
		if _, err := os.Lstat(filepath.Join(arch, f)); !os.IsNotExist(err) {
			t.Errorf("archive should not contain %s (err=%v)", f, err)
		}
	}
	planB, _ := os.ReadFile(filepath.Join(arch, "PLAN.md"))
	if !strings.Contains(string(planB), "prstk:generated:begin") {
		t.Errorf("archived PLAN.md lacks generated block:\n%s", planB)
	}
	logB, _ := os.ReadFile(filepath.Join(arch, "LOG.md"))
	if !strings.Contains(string(logB), "· teardown ·") || !strings.HasSuffix(strings.TrimSpace(string(logB)), "archived to _archive/"+in.Name+"/") {
		t.Errorf("archived LOG.md doesn't end with the teardown entry:\n%s", logB)
	}
	ain, err := plan.LoadInitiative(arch)
	if err != nil {
		t.Fatalf("LoadInitiative(archive): %v", err)
	}
	if len(ain.PRs) != 2 || ain.PR("01") == nil || ain.PR("02") == nil {
		t.Fatalf("archived initiative PRs: %+v", ain.PRs)
	}
	if plan.ReadCurrent(in.RepoDir()) != "" {
		t.Error("current not cleared")
	}
	if wl := git(t, e.repo, "worktree", "list"); strings.Contains(wl, in.Name) {
		t.Errorf("worktree still registered:\n%s", wl)
	}
}

func TestTeardownArchivesDir(t *testing.T) {
	e, in := teardownSetup(t)
	want := teardownSnapshot(t, in.Dir)
	// A legacy single-file archive with the same name must survive untouched.
	legacy := filepath.Join(plan.ArchiveRoot(in.RepoDir()), in.Name+".md")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	const legacyBody = "# legacy PLAN + LOG\n"
	if err := os.WriteFile(legacy, []byte(legacyBody), 0o644); err != nil {
		t.Fatal(err)
	}

	out := e.run("teardown")
	arch := plan.ArchivePath(in.RepoDir(), in.Name)
	if wantOut := "archived " + in.Name + " → " + arch + "/ · removed 2 worktree(s) · current cleared"; out != wantOut {
		t.Fatalf("teardown output:\ngot:  %s\nwant: %s", out, wantOut)
	}
	teardownCheckArchived(t, e, in, want)
	if b, err := os.ReadFile(legacy); err != nil || string(b) != legacyBody {
		t.Fatalf("legacy archive changed: %v %q", err, b)
	}
}

func TestTeardownRefusesOpenPR(t *testing.T) {
	e := newEnv(t)
	e.run("init", "tiny")
	e.run("add", "01", "one", "--title", "One")
	e.run("assign", "01", "--worker", "w")
	in := e.initiative()
	if _, se, code := e.try("teardown"); code == 0 || !strings.Contains(se, "refusing: not merged/abandoned: 01 (in-progress)") {
		t.Fatalf("teardown should refuse: %d %s", code, se)
	}
	teardownCheckUntouched(t, in)
}

func TestTeardownRefusesDirtyWorktree(t *testing.T) {
	e, in := teardownSetup(t)
	if err := os.WriteFile(filepath.Join(in.Worktree("02"), "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, se, code := e.try("teardown"); code == 0 || !strings.Contains(se, "has uncommitted changes") {
		t.Fatalf("teardown should refuse: %d %s", code, se)
	}
	teardownCheckUntouched(t, in)
}

func TestTeardownRefusesExistingArchive(t *testing.T) {
	for _, kind := range []string{"dir", "file"} {
		t.Run(kind, func(t *testing.T) {
			e, in := teardownSetup(t)
			arch := plan.ArchivePath(in.RepoDir(), in.Name)
			if err := os.MkdirAll(plan.ArchiveRoot(in.RepoDir()), 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "dir" {
				err = os.Mkdir(arch, 0o755)
			} else {
				err = os.WriteFile(arch, []byte("in the way"), 0o644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, se, code := e.try("teardown"); code == 0 || !strings.Contains(se, "refusing: "+arch+" already exists") {
				t.Fatalf("teardown should refuse: %d %s", code, se)
			}
			teardownCheckUntouched(t, in)
		})
	}
}

// teardownCheckUntouched asserts a refused teardown changed nothing: no
// teardown LOG entry, worktrees still present, current still set.
func teardownCheckUntouched(t *testing.T, in *plan.Initiative) {
	t.Helper()
	logB, err := os.ReadFile(in.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logB), "· teardown ·") {
		t.Errorf("refused teardown wrote a LOG entry:\n%s", logB)
	}
	for _, p := range in.PRs {
		if _, err := os.Stat(in.Worktree(p.ID)); err != nil {
			t.Errorf("worktree %s removed: %v", p.ID, err)
		}
	}
	if plan.ReadCurrent(in.RepoDir()) != in.Name {
		t.Error("current changed")
	}
}

func TestTeardownRenameFailureIsRerunnable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e, in := teardownSetup(t)
	want := teardownSnapshot(t, in.Dir)
	// A read-only _archive makes the rename into it fail (EACCES) after
	// worktrees are removed and the LOG entry is written.
	root := plan.ArchiveRoot(in.RepoDir())
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	_, se, code := e.try("teardown")
	if code == 0 || !strings.Contains(se, "archiving failed") || !strings.Contains(se, "re-run teardown") {
		t.Fatalf("teardown should fail on rename: %d %s", code, se)
	}
	// Nothing lost: the initiative is intact where it was and still loads.
	if got := teardownSnapshot(t, in.Dir); len(got) != len(want) {
		t.Fatalf("spec/notes after failed rename: %v", got)
	} else {
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s changed after failed rename", k)
			}
		}
	}
	if _, err := plan.LoadInitiative(in.Dir); err != nil {
		t.Fatalf("initiative no longer loads: %v", err)
	}
	if _, err := os.Lstat(plan.ArchivePath(in.RepoDir(), in.Name)); !os.IsNotExist(err) {
		t.Fatalf("partial archive created: %v", err)
	}
	if plan.ReadCurrent(in.RepoDir()) != in.Name {
		t.Fatal("current cleared despite failure")
	}

	// Fix the cause and re-run: teardown completes.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	out := e.run("teardown")
	if !strings.Contains(out, "archived "+in.Name) {
		t.Fatalf("re-run teardown: %s", out)
	}
	teardownCheckArchived(t, e, in, want)
}

func TestTeardownArchivesEvidence(t *testing.T) {
	e := newEnv(t)
	e.run("init", "tiny")
	e.run("add", "01", "one", "--title", "One", "--evidence", "video")
	e.run("assign", "01", "--worker", "w")
	e.run("evidence", "add", "01", "--phase", "before", "--file", e.writeTemp("Home Page.png", "png bytes"), "--caption", "Home")
	e.run("evidence", "add", "01", "--phase", "before", "--file", e.writeTemp("flow.webm", "webm bytes"))
	e.run("evidence", "script", "01", "--file", e.writeTemp("capture.mjs", "// script\n"))
	in := e.initiative()
	before, err := plan.LoadEvidence(in.PR("01"))
	if err != nil || len(before.Items) != 2 {
		t.Fatalf("manifest before teardown: %+v %v", before, err)
	}
	e.run("status", "01", "abandoned")
	e.run("teardown")

	arch := plan.ArchivePath(in.RepoDir(), in.Name)
	evDir := filepath.Join(arch, "prs", "01-one", "evidence")
	for _, f := range []string{"evidence.json", "before/home-page.png", "before/flow.webm", "script/capture.mjs"} {
		if _, err := os.Stat(filepath.Join(evDir, f)); err != nil {
			t.Errorf("archive missing evidence/%s: %v", f, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(evDir, "before", "home-page.png")); string(b) != "png bytes" {
		t.Errorf("archived capture content: %q", b)
	}
	ain, err := plan.LoadInitiative(arch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.LoadEvidence(ain.PR("01"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Script != "capture.mjs" || len(got.Items) != 2 || got.Items[0].File != "home-page.png" || got.Items[0].Caption != "Home" || got.Items[0].Commit != before.Items[0].Commit {
		t.Fatalf("archived manifest: %+v", got)
	}
	if ain.PR("01").EvidenceFilePath(got.Items[0]) != filepath.Join(evDir, "before", "home-page.png") {
		t.Fatalf("path: %s", ain.PR("01").EvidenceFilePath(got.Items[0]))
	}
}
