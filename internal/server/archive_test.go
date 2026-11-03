package server_test

// External test package: the real-teardown test imports internal/cli, which
// imports internal/server, so it can't live in package server.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prstk/internal/cli"
	"prstk/internal/gh"
	"prstk/internal/plan"
	"prstk/internal/render"
	"prstk/internal/server"
)

const (
	repoA = "dalton-sample_proj"
	repoB = "dalton-other_repo"
)

// mkInit writes an initiative with two merged PRs (02 stacks on 01) under
// root/name. The titles and notes carry name so tests can tell copies apart.
func mkInit(t *testing.T, root, name, initiative, repo string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	in := &plan.Initiative{Dir: dir, Meta: plan.PlanMeta{Initiative: initiative, Repo: repo, BaseBranch: "develop", MaxWorkers: 2}}
	for _, s := range []*plan.Spec{
		{ID: "01", Slug: "scaffold", Title: "Scaffold " + initiative, Status: plan.Merged, Model: "sonnet", Worker: "w-01"},
		{ID: "02", Slug: "store", Title: "Store " + initiative, Parent: "01", Status: plan.Merged, Model: "opus", Worker: "w-02", PRNumber: 7},
	} {
		s.Dir = filepath.Join(dir, "prs", s.ID+"-"+s.Slug)
		if err := os.MkdirAll(s.Dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(s.NotesPath(), []byte("## Description\nnotes for "+s.ID+" of "+initiative+"\n<script>window.pwned=1</script>\n"), 0o644)
	}
	if err := in.SavePlan(func(string) string { return "# " + initiative + "\n" }); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "LOG.md"), []byte("# Log\n\n## 2026-11-03 15:55 · add · prstk\n01 scaffold of "+initiative+"\n\n## 2026-11-03 15:56 · merged · prstk\n02 merged\n"), 0o644)
	return dir
}

const legacyText = "---\ninitiative: old-thing\nrepo: dalton/sample_proj\nbase_branch: develop\nmax_workers: 2\n---\n# Old Thing\n\nlegacy **body** <script>window.pwned=1</script>\n"

// archiveFixture builds a home with:
//
//	repoA: active 2026-11-04-0900-live; archived 2026-11-03-1415-gamma (dir),
//	       2026-11-01-1000-alpha (dir), 2026-10-30-0900-old-thing.md (legacy),
//	       a PLAN-less dir and a non-md file (both ignored)
//	repoB: archived 2026-11-02-0800-beta (dir)
//
// and a secret.md next to the repos that traversal must not reach.
func archiveFixture(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	home := t.TempDir()
	a, b := filepath.Join(home, repoA), filepath.Join(home, repoB)
	mkInit(t, a, "2026-11-04-0900-live", "live", "dalton/sample_proj")
	arch := plan.ArchiveRoot(a)
	mkInit(t, arch, "2026-11-03-1415-gamma", "gamma", "dalton/sample_proj")
	mkInit(t, arch, "2026-11-01-1000-alpha", "alpha", "dalton/sample_proj")
	mkInit(t, plan.ArchiveRoot(b), "2026-11-02-0800-beta", "beta", "dalton/other_repo")
	os.WriteFile(filepath.Join(arch, "2026-10-30-0900-old-thing.md"), []byte(legacyText), 0o644)
	os.MkdirAll(filepath.Join(arch, "no-plan-here"), 0o755)
	os.WriteFile(filepath.Join(arch, "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(home, "secret.md"), []byte("SECRET"), 0o644)
	ts := httptest.NewServer(server.New(home).Handler())
	t.Cleanup(ts.Close)
	return ts, home
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	// Don't follow redirects: tests assert them.
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if loc := r.Header.Get("Location"); loc != "" {
		return r.StatusCode, loc
	}
	return r.StatusCode, string(b)
}

func jsonOf(t *testing.T, url string, v any) {
	t.Helper()
	code, body := get(t, url)
	if code != 200 {
		t.Fatalf("GET %s: %d %s", url, code, body)
	}
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("GET %s: %v\n%s", url, err, body)
	}
}

type archRow struct {
	Key        string `json:"key"`
	Initiative string `json:"initiative"`
	Repo       string `json:"repo"`
	Dir        string `json:"dir"`
	PRs        int    `json:"prs"`
	Legacy     bool   `json:"legacy"`
}

func TestArchiveListAcrossRepos(t *testing.T) {
	ts, _ := archiveFixture(t)
	var rows []archRow
	jsonOf(t, ts.URL+"/api/archives", &rows)
	want := []archRow{
		{repoA + "/2026-11-03-1415-gamma", "gamma", "dalton/sample_proj", "2026-11-03-1415-gamma", 2, false},
		{repoB + "/2026-11-02-0800-beta", "beta", "dalton/other_repo", "2026-11-02-0800-beta", 2, false},
		{repoA + "/2026-11-01-1000-alpha", "alpha", "dalton/sample_proj", "2026-11-01-1000-alpha", 2, false},
		{repoA + "/legacy/2026-10-30-0900-old-thing.md", "old-thing", "dalton/sample_proj", "2026-10-30-0900-old-thing", 0, true},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d: got %+v, want %+v", i, rows[i], want[i])
		}
	}

	// The existing endpoint keeps its array shape and lists only active work.
	var active []map[string]any
	jsonOf(t, ts.URL+"/api/initiatives", &active)
	if len(active) != 1 || active[0]["initiative"] != "live" {
		t.Fatalf("/api/initiatives: %v", active)
	}
	if _, has := active[0]["archived"]; has {
		t.Fatalf("/api/initiatives must not grow archive fields: %v", active[0])
	}

	// No archives at all: an empty array, not null.
	empty := httptest.NewServer(server.New(t.TempDir()).Handler())
	defer empty.Close()
	if _, body := get(t, empty.URL+"/api/archives"); strings.TrimSpace(body) != "[]" {
		t.Fatalf("empty list: %q", body)
	}
}

func TestArchivedBoardPRAndLogJSON(t *testing.T) {
	ts, _ := archiveFixture(t)
	key := repoA + "/2026-11-03-1415-gamma"

	var b render.Board
	jsonOf(t, ts.URL+"/api/a/"+key, &b)
	if b.Key != key || b.Initiative != "gamma" || b.Dir != "2026-11-03-1415-gamma" || b.Repo != "dalton/sample_proj" {
		t.Fatalf("board header: key=%q initiative=%q dir=%q repo=%q", b.Key, b.Initiative, b.Dir, b.Repo)
	}
	if len(b.Slots) != 0 || len(b.Next) != 0 || len(b.Lanes) != 1 || len(b.Lanes[0].Cards) != 2 {
		t.Fatalf("board: slots=%v next=%v lanes=%+v", b.Slots, b.Next, b.Lanes)
	}
	for _, c := range b.Lanes[0].Cards {
		if c.Column != "Merged" {
			t.Errorf("card %s in %q, want Merged", c.ID, c.Column)
		}
	}

	var d render.PRDetail
	jsonOf(t, ts.URL+"/api/a/"+key+"/pr/02", &d)
	if d.ID != "02" || d.Title != "Store gamma" || d.Parent != "01" || d.Status != "merged" || d.Model != "opus" {
		t.Fatalf("detail: %+v", d)
	}
	if !strings.Contains(d.Notes, "notes for 02 of gamma") || d.URL != "https://github.com/dalton/sample_proj/pull/7" {
		t.Fatalf("notes/url: %q %q", d.Notes, d.URL)
	}
	if len(d.Log) != 1 || d.Log[0].Type != "merged" {
		t.Fatalf("pr log: %+v", d.Log)
	}

	var log []render.LogEntry
	jsonOf(t, ts.URL+"/api/a/"+key+"/log", &log)
	if len(log) != 2 || log[0].Type != "add" || log[1].Type != "merged" || len(log[0].PRs) != 1 || log[0].PRs[0] != "01" {
		t.Fatalf("log: %+v", log)
	}
}

func TestArchivedPagesAreReadOnly(t *testing.T) {
	ts, _ := archiveFixture(t)
	key := repoA + "/2026-11-03-1415-gamma"
	for path, view := range map[string]string{
		"/a/" + key + "/":       `"view":""`,
		"/a/" + key + "/pr/01/": `"view":"pr"`,
		"/a/" + key + "/log/":   `"view":"log"`,
		"/a/" + repoA + "/legacy/2026-10-30-0900-old-thing.md": `"view":"legacy"`,
	} {
		code, body := get(t, ts.URL+path)
		if code != 200 {
			t.Fatalf("GET %s: %d", path, code)
		}
		for _, w := range []string{`"readonly":true`, `"events":""`, view} {
			if !strings.Contains(body, w) {
				t.Errorf("GET %s: page config lacks %s", path, w)
			}
		}
		if !strings.Contains(body, `"api":"/api/`) {
			t.Errorf("GET %s: no api", path)
		}
	}
	// links on archived pages must stay under /a/, never /i/
	_, body := get(t, ts.URL+"/a/"+key+"/pr/01/")
	// archived PR titles flow into Mermaid labels: strict mode must be explicit
	if !strings.Contains(body, `securityLevel: "strict"`) {
		t.Fatal("mermaid.initialize must set securityLevel strict")
	}
	if !strings.Contains(body, `"base":"/a/`+key+`/"`) || strings.Contains(body, `"base":"/i/`) {
		t.Fatalf("base: %s", body[:min(len(body), 200)])
	}

	for _, p := range []string{"/a/" + key, "/a/" + key + "/pr/01", "/a/" + key + "/log"} {
		if code, loc := get(t, ts.URL+p); code != http.StatusMovedPermanently || loc != p+"/" {
			t.Errorf("GET %s: %d → %q", p, code, loc)
		}
	}
}

func TestLegacyPage(t *testing.T) {
	ts, _ := archiveFixture(t)
	var d struct {
		Key, Initiative, Repo, Dir, File, Body string
	}
	jsonOf(t, ts.URL+"/api/legacy/"+repoA+"/2026-10-30-0900-old-thing.md", &d)
	if d.Initiative != "old-thing" || d.Repo != "dalton/sample_proj" || d.File != "2026-10-30-0900-old-thing.md" ||
		d.Key != repoA+"/legacy/2026-10-30-0900-old-thing.md" {
		t.Fatalf("legacy doc: %+v", d)
	}
	// frontmatter stripped; the raw markdown (including the script tag) is
	// handed to the client, which renders it only through DOMPurify
	if !strings.HasPrefix(d.Body, "# Old Thing") || strings.Contains(d.Body, "initiative:") || !strings.Contains(d.Body, "<script>") {
		t.Fatalf("body: %q", d.Body)
	}
	// the HTML page itself never contains the file's text
	if _, body := get(t, ts.URL+"/a/"+repoA+"/legacy/2026-10-30-0900-old-thing.md"); strings.Contains(body, "window.pwned") || strings.Contains(body, "legacy **body**") {
		t.Fatal("legacy page inlines archived content")
	}
}

func TestArchiveNotFound(t *testing.T) {
	ts, home := archiveFixture(t)
	arch := plan.ArchiveRoot(filepath.Join(home, repoA))
	// a .md that is a directory, a symlinked .md and a symlinked dir archive
	os.MkdirAll(filepath.Join(arch, "dir.md"), 0o755)
	os.Symlink(filepath.Join(home, "secret.md"), filepath.Join(arch, "link.md"))
	os.Symlink(filepath.Join(arch, "2026-11-03-1415-gamma"), filepath.Join(arch, "2026-11-09-0000-linked"))

	key := repoA + "/2026-11-03-1415-gamma"
	bad := []string{
		// unknown repo / name / id
		"/a/nope/2026-11-03-1415-gamma/", "/api/a/nope/x",
		"/a/" + repoA + "/nope/", "/a/" + repoA + "/nope/log/", "/a/" + repoA + "/nope/log", "/a/" + repoA + "/nope/pr/01",
		"/api/a/" + repoA + "/nope", "/api/a/" + repoA + "/nope/log", "/api/a/" + repoA + "/nope/pr/01",
		"/a/" + key + "/pr/99/", "/a/" + key + "/pr/99", "/api/a/" + key + "/pr/99", "/api/a/" + key + "/pr/1",
		"/a/" + key + "/pr/", "/a/" + key + "/other", "/a/" + key + "/pr/01/extra",
		// dirs without a PLAN.md, and files that are not archives
		"/a/" + repoA + "/no-plan-here/", "/api/a/" + repoA + "/no-plan-here",
		// traversal through {id}
		"/api/a/" + key + "/pr/%2e%2e", "/api/a/" + key + "/pr/..%2F..%2F..%2Fsecret.md", "/api/a/" + key + "/pr/01%2F..%2F..",
		"/api/a/" + key + "/pr/01%5C..", "/a/" + key + "/pr/..%2F..%2Fsecret.md/", "/a/" + key + "/pr/..%2F..%2Fsecret.md",
		// traversal through {repo} / {name}
		"/api/a/%2e%2e/" + key + "/log", "/api/a/" + repoA + "/%2e%2e/log", "/api/a/" + repoA + "/..%2F..%2Fsecret.md/log",
		"/a/" + repoA + "/..%2F..%2Fsecret.md/log/", "/a/..%2F" + repoA + "/x/",
		"/a/" + repoA + "/_archive/", "/a/_archive/x/", "/a/" + repoA + "/.git/",
		// symlinked dir archive
		"/a/" + repoA + "/2026-11-09-0000-linked/", "/api/a/" + repoA + "/2026-11-09-0000-linked",
		// legacy {file}: traversal, wrong type, wrong repo, not a regular file
		"/a/" + repoA + "/legacy/..%2Fsecret.md", "/a/" + repoA + "/legacy/..%2F..%2Fsecret.md", "/a/" + repoA + "/legacy/%2e%2e%2fsecret.md",
		"/a/" + repoA + "/legacy/%2e%2e", "/a/" + repoA + "/legacy/secret.md", "/a/" + repoA + "/legacy/nope.md",
		"/a/" + repoA + "/legacy/notes.txt", "/a/" + repoA + "/legacy/2026-10-30-0900-old-thing", "/a/" + repoA + "/legacy/2026-10-30-0900-old-thing.md/",
		"/a/" + repoA + "/legacy/dir.md", "/a/" + repoA + "/legacy/link.md", "/a/" + repoA + "/legacy/.md",
		"/a/" + repoA + "/legacy/..%5Csecret.md", "/a/" + repoA + "/legacy/%2fetc%2fpasswd",
		"/a/" + repoB + "/legacy/2026-10-30-0900-old-thing.md", "/a/nope/legacy/2026-10-30-0900-old-thing.md",
		"/api/legacy/" + repoA + "/..%2Fsecret.md", "/api/legacy/" + repoA + "/secret.md", "/api/legacy/" + repoA + "/link.md",
		"/api/legacy/" + repoA + "/dir.md", "/api/legacy/%2e%2e/secret.md", "/api/legacy/" + repoB + "/2026-10-30-0900-old-thing.md",
	}
	for _, p := range bad {
		code, body := get(t, ts.URL+p)
		// Go's mux redirects an unclean path (literal "..") to its cleaned
		// form, which then goes through the same validation; nothing else may.
		if code/100 == 3 {
			if !strings.Contains(p, "/../") && !strings.HasSuffix(p, "/..") {
				t.Errorf("GET %s: unexpected redirect to %q", p, body)
			}
			continue
		}
		if code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", p, code)
		}
		if strings.Contains(body, "SECRET") || strings.Contains(body, "legacy **body**") {
			t.Errorf("GET %s leaked: %s", p, body)
		}
	}
	// the literal-".." forms must not leak either way
	for _, p := range []string{"/api/legacy/" + repoA + "/../secret.md", "/a/" + repoA + "/legacy/../secret.md"} {
		if _, body := get(t, ts.URL+p); strings.Contains(body, "SECRET") {
			t.Errorf("GET %s leaked", p)
		}
	}
}

func TestActiveAndArchivedNeverMix(t *testing.T) {
	ts, home := archiveFixture(t)
	live := repoA + "/2026-11-04-0900-live"
	gamma := repoA + "/2026-11-03-1415-gamma"
	// the active initiative is not reachable under /a/, the archived one not under /i/
	for _, p := range []string{
		"/a/" + live + "/", "/a/" + live + "/log/", "/a/" + live + "/pr/01/", "/api/a/" + live, "/api/a/" + live + "/log", "/api/a/" + live + "/pr/01",
		"/i/" + gamma + "/", "/i/" + gamma + "/log/", "/i/" + gamma + "/pr/01/", "/api/i/" + gamma, "/api/i/" + gamma + "/log", "/api/i/" + gamma + "/pr/01",
	} {
		if code, _ := get(t, ts.URL+p); code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", p, code)
		}
	}

	// the same dir name in both places resolves per route family
	mkInit(t, filepath.Join(home, repoA), "2026-11-03-1415-gamma", "gamma-live", "dalton/sample_proj")
	var a, i render.Board
	jsonOf(t, ts.URL+"/api/a/"+gamma, &a)
	jsonOf(t, ts.URL+"/api/i/"+gamma, &i)
	if a.Initiative != "gamma" || i.Initiative != "gamma-live" {
		t.Fatalf("archived=%q active=%q", a.Initiative, i.Initiative)
	}
	var rows []archRow
	jsonOf(t, ts.URL+"/api/archives", &rows)
	for _, r := range rows {
		if r.Initiative == "gamma-live" || r.Initiative == "live" {
			t.Errorf("active initiative listed as archived: %+v", r)
		}
	}
}

// TestRealTeardownThenServe runs the real `prstk teardown` against a plan
// home, with the server already watching it, and checks the result is served
// and the index is told to refresh.
func TestRealTeardownThenServe(t *testing.T) {
	home := t.TempDir()
	name := "2026-11-03-1555-visibility"
	dir := mkInit(t, filepath.Join(home, repoA), name, "visibility", "dalton/sample_proj")

	s := server.New(home)
	s.Poll = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(ctx, ln)
	base := "http://" + ln.Addr().String()

	var rows []archRow
	jsonOf(t, base+"/api/archives", &rows)
	if len(rows) != 0 {
		t.Fatalf("archives before teardown: %+v", rows)
	}
	var active []map[string]any
	jsonOf(t, base+"/api/initiatives", &active)
	if len(active) != 1 {
		t.Fatalf("active before teardown: %v", active)
	}

	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	events := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if l, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				events <- l
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // let the watcher take its baseline

	var out, errOut bytes.Buffer
	app := &cli.App{Stdout: &out, Stderr: &errOut, Cwd: dir, Home: home, Now: func() time.Time { return time.Date(2026, 11, 3, 17, 0, 0, 0, time.Local) },
		GH: gh.NewFake(), Getenv: func(string) string { return "" }, UserHome: t.TempDir()}
	if code := app.Run([]string{"teardown"}); code != 0 {
		t.Fatalf("teardown: exit %d\n%s\n%s", code, out.String(), errOut.String())
	}

	// the index must be told to refresh: a "*" event for the removed initiative
	deadline := time.After(3 * time.Second)
	for star := false; !star; {
		select {
		case e := <-events:
			star = e == "*"
		case <-deadline:
			t.Fatal("no \"*\" event after teardown")
		}
	}

	key := repoA + "/" + name
	jsonOf(t, base+"/api/archives", &rows)
	if len(rows) != 1 || rows[0].Key != key || rows[0].Initiative != "visibility" || rows[0].PRs != 2 || rows[0].Legacy {
		t.Fatalf("archives after teardown: %+v", rows)
	}
	jsonOf(t, base+"/api/initiatives", &active)
	if len(active) != 0 {
		t.Fatalf("active after teardown: %v", active)
	}
	if code, _ := get(t, base+"/i/"+key+"/"); code != http.StatusNotFound {
		t.Fatalf("torn-down initiative still served as active: %d", code)
	}

	var b render.Board
	jsonOf(t, base+"/api/a/"+key, &b)
	if b.Key != key || len(b.Lanes) != 1 || len(b.Lanes[0].Cards) != 2 {
		t.Fatalf("archived board: %+v", b)
	}
	var d render.PRDetail
	jsonOf(t, base+"/api/a/"+key+"/pr/02", &d)
	if !strings.Contains(d.Notes, "notes for 02 of visibility") {
		t.Fatalf("archived notes: %q", d.Notes)
	}
	var log []render.LogEntry
	jsonOf(t, base+"/api/a/"+key+"/log", &log)
	if len(log) == 0 || log[len(log)-1].Type != "teardown" || !strings.Contains(log[len(log)-1].Body, "archived to _archive/"+name+"/") {
		t.Fatalf("archived log should end with the teardown entry: %+v", log)
	}
	if code, _ := get(t, base+"/a/"+key+"/"); code != 200 {
		t.Fatalf("archived page: %d", code)
	}
}

// TestPageScriptParses guards the inline page script against syntax errors
// (nothing else in the Go tests executes it). It needs node and is skipped
// without it.
func TestPageScriptParses(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	ts, _ := archiveFixture(t)
	_, body := get(t, ts.URL+"/")
	i := strings.LastIndex(body, "<script>")
	j := strings.LastIndex(body, "</script>")
	if i < 0 || j < i {
		t.Fatal("no inline script in the index page")
	}
	f := filepath.Join(t.TempDir(), "page.js")
	os.WriteFile(f, []byte(body[i+len("<script>"):j]), 0o644)
	if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
		t.Fatalf("page script has a syntax error:\n%s", out)
	}
}
