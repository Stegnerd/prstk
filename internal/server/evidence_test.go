package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

const (
	evPNG    = "\x89PNG\r\n\x1a\nfake-png-bytes"
	evWebm   = "\x1aE\xdf\xa3webm-0123456789-abcdefghij"
	evScript = "// capture script\nconsole.log(1)\n"
	evSHA    = "0123456789abcdef0123456789abcdef01234567"
)

// evidenceHome builds setup()'s initiative with evidence on PR 01: a png
// before, a webm after, a capture script, a stray file on disk that the
// manifest doesn't list, a manifest entry that is a symlink to a secret, and
// hand-edited entries with a traversal name and a disallowed extension.
// archived moves the initiative into _archive. It returns the server and the
// URL prefix of PR 01 ("/i/<repo>/<name>/pr/01" or the /a/ form).
func evidenceHome(t *testing.T, archived bool) (*httptest.Server, string) {
	t.Helper()
	home, dir := setup(t)
	os.WriteFile(filepath.Join(home, "secret.png"), []byte("SECRET"), 0o644)
	s := &plan.Spec{Dir: filepath.Join(dir, "prs", "01-scaffold")}
	src := t.TempDir()
	add := func(name, body string, phase plan.Phase, step int) {
		p := filepath.Join(src, name)
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := s.AddEvidence(p, plan.EvidenceItem{Phase: phase, Step: step, Commit: evSHA, Caption: "c " + name}); err != nil {
			t.Fatal(err)
		}
	}
	add("home page.png", evPNG, plan.PhaseBefore, 1) // stored as home-page.png
	add("flow.webm", evWebm, plan.PhaseAfter, 1)
	os.WriteFile(filepath.Join(src, "cap.mjs"), []byte(evScript), 0o644)
	if _, err := s.SetEvidenceScript(filepath.Join(src, "cap.mjs")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.EvidenceDir(), "before", "stray.png"), []byte("STRAY"), 0o644)
	if err := os.Symlink(filepath.Join(home, "secret.png"), filepath.Join(s.EvidenceDir(), "after", "link.png")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.EvidenceDir(), "after", "page.html"), []byte("<script>x</script>"), 0o644)
	m, err := plan.LoadEvidence(s)
	if err != nil {
		t.Fatal(err)
	}
	m.Items = append(m.Items,
		plan.EvidenceItem{Phase: plan.PhaseAfter, Step: 2, File: "link.png", Kind: plan.KindImage, Commit: evSHA},
		plan.EvidenceItem{Phase: plan.PhaseAfter, Step: 3, File: "../../../../../secret.png", Kind: plan.KindImage, Commit: evSHA},
		plan.EvidenceItem{Phase: plan.PhaseAfter, Step: 4, File: "page.html", Kind: plan.KindImage, Commit: evSHA},
	)
	data, _ := json.Marshal(m)
	os.WriteFile(s.EvidenceManifestPath(), data, 0o644)

	repo, name := filepath.Base(filepath.Dir(dir)), filepath.Base(dir)
	prefix := "/i/" + repo + "/" + name + "/pr/01"
	if archived {
		if err := os.Rename(dir, plan.ArchivePath(filepath.Dir(dir), name)); err != nil {
			t.Fatal(err)
		}
		prefix = "/a/" + repo + "/" + name + "/pr/01"
	}
	ts := httptest.NewServer(New(home).Handler())
	t.Cleanup(ts.Close)
	return ts, prefix
}

func get(t *testing.T, url string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r, string(b)
}

func forBoth(t *testing.T, f func(t *testing.T, ts *httptest.Server, prefix string)) {
	for _, archived := range []bool{false, true} {
		name := "live"
		if archived {
			name = "archive"
		}
		t.Run(name, func(t *testing.T) {
			ts, prefix := evidenceHome(t, archived)
			f(t, ts, prefix)
		})
	}
}

func TestEvidenceServe(t *testing.T) {
	forBoth(t, func(t *testing.T, ts *httptest.Server, prefix string) {
		for _, c := range []struct{ path, ctype, body string }{
			{prefix + "/evidence/before/home-page.png", "image/png", evPNG},
			{prefix + "/evidence/after/flow.webm", "video/webm", evWebm},
			{prefix + "/evidence/script", "text/plain; charset=utf-8", evScript},
		} {
			r, b := get(t, ts.URL+c.path, nil)
			if r.StatusCode != 200 || b != c.body {
				t.Fatalf("GET %s: %d %q", c.path, r.StatusCode, b)
			}
			hd := r.Header
			if hd.Get("Content-Type") != c.ctype || hd.Get("X-Content-Type-Options") != "nosniff" ||
				hd.Get("Content-Security-Policy") != evidenceCSP || hd.Get("Cache-Control") != "no-cache" {
				t.Fatalf("GET %s headers: %v", c.path, hd)
			}
		}
		// range request: video seeking
		r, b := get(t, ts.URL+prefix+"/evidence/after/flow.webm", map[string]string{"Range": "bytes=4-9"})
		if r.StatusCode != http.StatusPartialContent || b != evWebm[4:10] ||
			r.Header.Get("Content-Range") != "bytes 4-9/"+strconv.Itoa(len(evWebm)) || r.Header.Get("Content-Type") != "video/webm" {
			t.Fatalf("range: %d %q %v", r.StatusCode, b, r.Header)
		}
		// the detail JSON's URLs resolve against the page base
		api := strings.Replace(strings.Replace(prefix, "/i/", "/api/i/", 1), "/a/", "/api/a/", 1)
		_, body := get(t, ts.URL+api, nil)
		if !strings.Contains(body, `"url":"pr/01/evidence/before/home-page.png"`) || !strings.Contains(body, `"url":"pr/01/evidence/script"`) {
			t.Fatalf("detail json: %s", body)
		}
	})
}

func TestEvidenceNotFound(t *testing.T) {
	forBoth(t, func(t *testing.T, ts *httptest.Server, prefix string) {
		base := strings.TrimSuffix(prefix, "/pr/01")
		bad := []string{
			prefix + "/evidence/before/nope.png",                           // unknown file
			prefix + "/evidence/before/stray.png",                          // on disk, not in the manifest
			prefix + "/evidence/after/link.png",                            // in the manifest, but a symlink
			prefix + "/evidence/after/page.html",                           // in the manifest, extension not allowed
			prefix + "/evidence/after/flow.WEBM",                           // exact match only
			prefix + "/evidence/before/flow.webm",                          // wrong phase for the file
			prefix + "/evidence/after/..%2F..%2F..%2F..%2F..%2Fsecret.png", // traversal key that is in the manifest
			prefix + "/evidence/after/%2e%2e%2f%2e%2e%2fsecret.png",
			prefix + "/evidence/before/%2e%2e",
			prefix + "/evidence/before/..%5Cspec.md",
			prefix + "/evidence/before/home-page.png%00",
			prefix + "/evidence/%2e%2e/spec.md",
			prefix + "/evidence/..%2Fspec.md/x",
			prefix + "/evidence/script/cap.mjs", // bad phase
			prefix + "/evidence/BEFORE/home-page.png",
			prefix + "/evidence/evidence.json/x",
			prefix + "/evidence/before",
			prefix + "/evidence/scripts",
			base + "/pr/99/evidence/before/home-page.png", // unknown id
			base + "/pr/99/evidence/script",
			base + "/pr/%2e%2e/evidence/before/home-page.png",
			base + "/pr/01%2F..%2F01/evidence/before/home-page.png",
			strings.Replace(base, "pokemon-api", "nope", 1) + "/pr/01/evidence/before/home-page.png",
		}
		for _, p := range bad {
			r, b := get(t, ts.URL+p, nil)
			if r.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s: %d, want 404", p, r.StatusCode)
			}
			for _, leak := range []string{"SECRET", "STRAY", "<script>", "evidence.json", "/private/", os.TempDir()} {
				if strings.Contains(b, leak) {
					t.Errorf("GET %s leaked %q: %s", p, leak, b)
				}
			}
		}
		// A literal ".." is cleaned by the mux (a redirect) and then re-validated.
		r, _ := get(t, ts.URL+prefix+"/evidence/before/../../spec.md", nil)
		if r.StatusCode/100 == 3 {
			r, _ = get(t, ts.URL+r.Header.Get("Location"), nil)
		}
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("dotdot: %d", r.StatusCode)
		}
	})
}

// A symlinked phase directory is refused even when the file inside is regular.
func TestEvidenceSymlinkedDir(t *testing.T) {
	home, dir := setup(t)
	s := &plan.Spec{Dir: filepath.Join(dir, "prs", "01-scaffold")}
	src := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(src, []byte(evPNG), 0o644)
	if _, err := s.AddEvidence(src, plan.EvidenceItem{Phase: plan.PhaseBefore, Step: 1, Commit: evSHA}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(home, "elsewhere")
	if err := os.Rename(filepath.Join(s.EvidenceDir(), "before"), outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.EvidenceDir(), "before")); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(home).Handler())
	defer ts.Close()
	r, _ := get(t, ts.URL+"/i/dalton-sample_proj/2026-11-03-1415-pokemon-api/pr/01/evidence/before/a.png", nil)
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("symlinked dir: %d", r.StatusCode)
	}
}

func TestServeEvidenceOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(t.TempDir(), "x.png")
	os.WriteFile(other, []byte("x"), 0o644)
	for _, p := range []string{other, root, filepath.Join(root, "..", "x.png")} {
		rec := httptest.NewRecorder()
		serveEvidence(rec, httptest.NewRequest("GET", "/", nil), root, p, "image/png")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestEvidenceWatch(t *testing.T) {
	home, dir := setup(t)
	s := New(home)
	s.Poll = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if l, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				got <- l
			}
		}
	}()
	time.Sleep(60 * time.Millisecond) // baseline
	sp := &plan.Spec{Dir: filepath.Join(dir, "prs", "01-scaffold")}
	src := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(src, []byte(evPNG), 0o644)
	if _, err := sp.AddEvidence(src, plan.EvidenceItem{Phase: plan.PhaseBefore, Step: 1, Commit: evSHA}); err != nil {
		t.Fatal(err)
	}
	select {
	case k := <-got:
		if k != "dalton-sample_proj/2026-11-03-1415-pokemon-api" {
			t.Fatalf("event key %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE event after evidence.json write")
	}
}
