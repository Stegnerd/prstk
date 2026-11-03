package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stegnerd/prstk/internal/render"
)

func detailServer(t *testing.T) (*httptest.Server, string, string) {
	t.Helper()
	home, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "prs", "01-scaffold", "notes.md"), []byte("## Description\n<script>window.pwned=1</script>\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "LOG.md"), []byte("# Log\n\n## 2026-11-03 15:55 · add · prstk\n01 scaffold on develop\n\n## 2026-11-03 15:56 · replan · director\nnothing about 101 or 2026\nline two\n"), 0o644)
	// a secret next to the plans dir that traversal must not reach
	os.WriteFile(filepath.Join(home, "secret.md"), []byte("SECRET"), 0o644)
	ts := httptest.NewServer(New(home).Handler())
	t.Cleanup(ts.Close)
	return ts, "dalton-sample_proj/2026-11-03-1415-pokemon-api", home
}

func TestAPIPRDetail(t *testing.T) {
	ts, key, _ := detailServer(t)
	var d render.PRDetail
	getJSON(t, ts.URL+"/api/i/"+key+"/pr/01", &d)
	if d.ID != "01" || d.Title != "Scaffold" || d.Model != "sonnet" || d.Parent != "" {
		t.Fatalf("detail: %+v", d)
	}
	if !strings.Contains(d.Notes, "<script>") || d.Spec.DirectorRounds == nil {
		t.Fatalf("notes/spec: %+v", d)
	}
	if len(d.Log) != 1 || d.Log[0].Type != "add" {
		t.Fatalf("log: %+v", d.Log)
	}
}

func TestAPILog(t *testing.T) {
	ts, key, _ := detailServer(t)
	var log []render.LogEntry
	getJSON(t, ts.URL+"/api/i/"+key+"/log", &log)
	if len(log) != 2 || log[0].Type != "add" || log[1].Type != "replan" || log[1].Body != "nothing about 101 or 2026\nline two" {
		t.Fatalf("log: %+v", log)
	}
	if len(log[0].PRs) != 1 || log[0].PRs[0] != "01" || log[1].PRs == nil || len(log[1].PRs) != 0 {
		t.Fatalf("prs: %+v", log)
	}
}

func TestDetailPagesAndRedirects(t *testing.T) {
	ts, key, _ := detailServer(t)
	for _, p := range []string{"/i/" + key + "/pr/01/", "/i/" + key + "/log/"} {
		r, err := http.Get(ts.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", p, err, r.Status)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if !strings.Contains(string(b), `"base":"/i/`+key+`/"`) || !strings.Contains(string(b), `"key":"`+key+`"`) {
			t.Fatalf("%s: page config missing base/key", p)
		}
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range []string{"/i/" + key + "/pr/01", "/i/" + key + "/log"} {
		r, err := noFollow.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusMovedPermanently || r.Header.Get("Location") != p+"/" {
			t.Fatalf("GET %s: %d → %q", p, r.StatusCode, r.Header.Get("Location"))
		}
	}
}

func TestDetailNotFound(t *testing.T) {
	ts, key, _ := detailServer(t)
	bad := []string{
		"/api/i/" + key + "/pr/99",
		"/api/i/" + key + "/pr/1",
		"/api/i/" + key + "/pr/..",
		"/api/i/" + key + "/pr/%2e%2e",
		"/api/i/" + key + "/pr/..%2F..%2F..%2Fsecret.md",
		"/api/i/" + key + "/pr/01%2F..%2F..",
		"/api/i/" + key + "/pr/01%5C..",
		"/api/i/dalton-sample_proj/%2e%2e/pr/01",
		"/api/i/%2e%2e/" + key + "/log",
		"/api/i/dalton-sample_proj/..%2F..%2Fsecret.md/log",
		"/api/i/dalton-sample_proj/_archive/log",
		"/api/i/dalton-sample_proj/nope/log",
		"/i/" + key + "/pr/99/",
		"/i/" + key + "/pr/99",
		"/i/" + key + "/pr/%2e%2e/",
		"/i/" + key + "/pr/..%2F..%2Fsecret.md/",
		"/i/dalton-sample_proj/..%2F..%2Fsecret.md/log/",
		"/i/dalton-sample_proj/nope/log/",
		"/i/dalton-sample_proj/nope/log",
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, p := range bad {
		r, err := noFollow.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		// Go's mux answers an unclean path (a literal "..") with a redirect to
		// the cleaned path, which then goes through the same validation. Only
		// those may redirect; everything else must be a 404.
		if r.StatusCode/100 == 3 {
			if !strings.HasSuffix(p, "/..") {
				t.Errorf("GET %s: unexpected redirect to %q", p, r.Header.Get("Location"))
			}
		} else if r.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", p, r.StatusCode)
		}
		if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), `"id":"01"`) {
			t.Errorf("GET %s leaked: %s", p, b)
		}
	}
}
