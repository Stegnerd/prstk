package usage

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTranscriptDedupeAndKinds(t *testing.T) {
	tr, err := parseTranscript(filepath.Join("testdata", "transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if tr.malformed != 1 {
		t.Errorf("malformed = %d, want 1", tr.malformed)
	}
	byKey := map[string]*message{}
	for _, m := range tr.msgs {
		byKey[m.key] = m
	}
	if len(tr.msgs) != 5 {
		t.Fatalf("got %d messages, want 5 (msg_1, msg_2, req_3, synthetic, zero)", len(tr.msgs))
	}

	// msg_1: three lines (thinking, text, tool_use), counted once, with the
	// 5m/1h split from cache_creation.
	m1 := byKey["msg_1"]
	want1 := Tokens{Input: 3, CacheWrite5m: 100, CacheWrite1h: 200, CacheRead: 1000, Output: 50}
	if m1 == nil || m1.tokens != want1 {
		t.Fatalf("msg_1 tokens = %+v, want %+v", m1, want1)
	}
	if m1.model != "claude-opus-5-5" || m1.at.Format("15:04:05") != "10:00:01" {
		t.Errorf("msg_1 model/at = %s %s", m1.model, m1.at)
	}
	if len(m1.bash) != 1 || m1.bash[0].id != "toolu_1" || !strings.HasPrefix(m1.bash[0].command, "prstk add 01") {
		t.Errorf("msg_1 bash = %+v", m1.bash)
	}

	// msg_2: no cache_creation, so all cache_creation_input_tokens are 5m.
	if got, want := byKey["msg_2"].tokens, (Tokens{Input: 7, CacheWrite5m: 400, Output: 9}); got != want {
		t.Errorf("msg_2 tokens = %+v, want %+v", got, want)
	}
	if !tr.errored["toolu_2"] || tr.errored["toolu_1"] {
		t.Errorf("errored = %v, want only toolu_2", tr.errored)
	}

	// Empty message.id falls back to requestId: two lines, one message.
	if got := byKey["req:req_3"]; got == nil || got.tokens.Total() != 3 {
		t.Errorf("req_3 = %+v, want one message with 3 tokens", got)
	}

	if byKey["msg_syn"].counted() || byKey["msg_zero"].counted() {
		t.Error("synthetic and zero-usage messages must not count")
	}
	if !m1.counted() {
		t.Error("msg_1 should count")
	}
}

func TestSubagentName(t *testing.T) {
	dir := filepath.Join("testdata", "subagents")
	for file, want := range map[string]string{
		"agent-aw-03-0123456789abcdef.jsonl": "w-03",  // from the file name (no meta)
		"agent-aw-07-fedcba9876543210.jsonl": "w-07b", // meta wins over the file name
		"agent-a5f2e9c1d0b7a6e44.jsonl":      "",      // built-in agent: no name
	} {
		if got := subagentName(filepath.Join(dir, file)); got != want {
			t.Errorf("%s: name %q, want %q", file, got, want)
		}
	}
	if got := subagentName("/x/agent-aw-01-eca37e403c20a10a.jsonl"); got != "w-01" {
		t.Errorf("got %q, want w-01", got)
	}
}
