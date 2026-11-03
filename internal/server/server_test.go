package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prstk/internal/plan"
)

func setup(t *testing.T) (home, dir string) {
	home = t.TempDir()
	dir = filepath.Join(home, "dalton-sample_proj", "2026-11-03-1415-pokemon-api")
	os.MkdirAll(filepath.Join(dir, "prs", "01-scaffold"), 0o755)
	in := &plan.Initiative{Dir: dir, Meta: plan.PlanMeta{Initiative: "pokemon-api", Repo: "dalton/sample_proj", BaseBranch: "develop", MaxWorkers: 2}}
	in.SavePlan(func(string) string { return "# P\n" })
	s := &plan.Spec{ID: "01", Slug: "scaffold", Title: "Scaffold", Status: plan.Planned, Model: "sonnet", Dir: filepath.Join(dir, "prs", "01-scaffold")}
	s.Save()
	os.WriteFile(s.NotesPath(), nil, 0o644)
	os.MkdirAll(filepath.Join(home, "dalton-sample_proj", "_archive"), 0o755)
	return home, dir
}

func TestAPIAndSSE(t *testing.T) {
	home, dir := setup(t)
	s := New(home)
	s.Poll = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	var idx []indexEntry
	getJSON(t, ts.URL+"/api/initiatives", &idx)
	if len(idx) != 1 || idx[0].Key != "dalton-sample_proj/2026-11-03-1415-pokemon-api" || idx[0].PRs != 1 {
		t.Fatalf("index: %+v", idx)
	}
	var board map[string]any
	getJSON(t, ts.URL+"/api/i/"+idx[0].Key, &board)
	if board["initiative"] != "pokemon-api" {
		t.Fatalf("board: %v", board)
	}
	for _, p := range []string{"/", "/i/" + idx[0].Key + "/"} {
		r, err := http.Get(ts.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", p, err, r.Status)
		}
		r.Body.Close()
	}
	if r, _ := http.Get(ts.URL + "/api/i/dalton-sample_proj/_archive"); r.StatusCode != 404 {
		t.Fatalf("archive should 404, got %d", r.StatusCode)
	}

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
	time.Sleep(60 * time.Millisecond) // let the watcher take its baseline
	spec := filepath.Join(dir, "prs", "01-scaffold", "spec.md")
	b, _ := os.ReadFile(spec)
	os.WriteFile(spec, []byte(strings.Replace(string(b), "status: planned", "status: in-progress", 1)), 0o644)
	select {
	case k := <-got:
		if k != idx[0].Key {
			t.Fatalf("event key %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE event after file change")
	}
}

func TestListenPortTaken(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if _, err := Listen(port); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("want clear port error, got %v", err)
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
