package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/render"
)

const usageJSON = `{"version":1,"updated_at":"2026-11-19T20:00:00Z",
 "total":{"messages":2,"tokens":{"input":10,"output":20,"cache_read":1000},"by_model":{"claude-opus-5-5":{"input":10,"output":20,"cache_read":1000}},"cost_usd":0.5},
 "director":{"messages":2,"tokens":{"input":10,"output":20,"cache_read":1000},"by_model":{"claude-opus-5-5":{"input":10,"output":20,"cache_read":1000}},"cost_usd":0.5},
 "workers":{"messages":0,"tokens":{}},"overhead":{"messages":0,"tokens":{}},
 "prs":{"01":{"director":{"messages":2,"tokens":{"input":10,"output":20,"cache_read":1000},"by_model":{"claude-opus-5-5":{"input":10,"output":20,"cache_read":1000}},"cost_usd":0.5},"workers":{},
  "total":{"messages":2,"tokens":{"input":10,"output":20,"cache_read":1000},"by_model":{"claude-opus-5-5":{"input":10,"output":20,"cache_read":1000}},"cost_usd":0.5}}},
 "sources":[]}`

func TestSignatureIncludesUsageJSON(t *testing.T) {
	home, dir := setup(t)
	s := New(home)
	key := "dalton-sample_proj/2026-11-03-1415-pokemon-api"
	before := s.signature(key)
	p := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(p, []byte(usageJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	created := s.signature(key)
	if created == before || !strings.Contains(created, "usage.json") {
		t.Fatalf("creating usage.json must change the signature:\n%s\n%s", before, created)
	}
	if err := os.WriteFile(p, []byte(usageJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s.signature(key) == created {
		t.Fatal("changing usage.json must change the signature")
	}
}

func TestUsageChangePushesSSE(t *testing.T) {
	home, dir := setup(t)
	s := New(home)
	s.Poll = 20 * time.Millisecond
	ts := httptest.NewServer(s.Handler())
	defer ts.Close() // runs after cancel: the SSE request must end first
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.watch(ctx)
	key := "dalton-sample_proj/2026-11-03-1415-pokemon-api"

	// the live endpoints carry usage once the file exists
	var b render.Board
	getJSON(t, ts.URL+"/api/i/"+key, &b)
	if b.Usage != nil {
		t.Fatalf("usage before the file: %+v", b.Usage)
	}
	events := sseEvents(t, ctx, ts.URL)
	time.Sleep(60 * time.Millisecond) // let the watcher take its baseline
	if err := os.WriteFile(filepath.Join(dir, "usage.json"), []byte(usageJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case k := <-events:
		if k != key {
			t.Fatalf("event key %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no SSE event after usage.json changed")
	}
	getJSON(t, ts.URL+"/api/i/"+key, &b)
	if b.Usage == nil || b.Usage.Total.Tokens != 1030 || b.Lanes[0].Cards[0].Usage == nil {
		t.Fatalf("board usage: %+v", b.Usage)
	}
	var d render.PRDetail
	getJSON(t, ts.URL+"/api/i/"+key+"/pr/01", &d)
	if d.Usage == nil || len(d.Usage.Rows) != 2 {
		t.Fatalf("detail usage: %+v", d.Usage)
	}
}
