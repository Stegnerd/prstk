package usage

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// at is 2026-11-03 h:m UTC.
func at(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.UTC) }

// asst is one assistant transcript line with output tokens out and one Bash
// tool_use per command (tool ids "tu_<id>_<n>").
func asst(id string, t time.Time, model string, out int64, cmds ...string) string {
	content := []any{map[string]any{"type": "text", "text": "…"}}
	for i, c := range cmds {
		content = append(content, map[string]any{
			"type": "tool_use", "id": fmt.Sprintf("tu_%s_%d", id, i), "name": "Bash",
			"input": map[string]any{"command": c},
		})
	}
	b, _ := json.Marshal(map[string]any{
		"type": "assistant", "timestamp": t.Format(time.RFC3339Nano), "uuid": "u-" + id, "requestId": "r-" + id,
		"message": map[string]any{
			"id": id, "model": model, "content": content,
			"usage": map[string]any{"input_tokens": 1, "output_tokens": out, "cache_read_input_tokens": 0},
		},
	})
	return string(b)
}

// toolError is a user line reporting tool_use id as failed.
func toolError(toolID string, t time.Time) string {
	b, _ := json.Marshal(map[string]any{
		"type": "user", "timestamp": t.Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": toolID, "is_error": true, "content": "refusing"},
		}},
	})
	return string(b)
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const opus = "claude-opus-5-5"

const scenarioLog = `# Log · demo

## 2026-11-03 10:05 · assign · prstk
01 → w-01 (in-progress, sonnet)

## 2026-11-03 10:05 · assign · prstk
02 → w-02 (in-progress, sonnet)

## 2026-11-03 11:00 · assign · prstk
03 → w-01 (in-progress, opus)
`

// scenario builds a Claude dir with one director session (s1) and three
// subagents. Created is 10:00; see the comments for where each token goes.
func scenario(t *testing.T) (Input, string) {
	t.Helper()
	claude := t.TempDir()
	proj := filepath.Join(claude, "projects", "-repo")
	writeLines(t, filepath.Join(proj, "s1.jsonl"),
		// A teardown of an earlier initiative in the same session: the
		// session starts after it.
		asst("d0", at(9, 0), opus, 1000, "prstk teardown"),
		// Planning before prstk init counts (overhead).
		asst("d1", at(9, 10), opus, 1),
		asst("d2", at(9, 20), opus, 2, "prstk init demo --base main && prstk doctor"),
		// 01: pending (4) + assign (8).
		asst("d3", at(10, 10), opus, 4),
		asst("d4", at(10, 11), opus, 8, "prstk assign 01 --worker w-01"),
		// Two ids: 17 split 9/8.
		asst("d5", at(10, 20), opus, 17, "prstk show 01 && $PRSTK_BIN show 02"),
		// Env prefix: 02.
		asst("d6", at(10, 30), opus, 32, "FOO=1 prstk status 02 in-progress"),
		// --initiative elsewhere, gh pr diff, cat: none count, so these stay
		// pending until evidence add 02.
		asst("d7", at(10, 40), opus, 64, "prstk --initiative 2026-01-01-0000-other show 01"),
		asst("d8", at(10, 41), opus, 128, "gh pr diff 01"),
		asst("d9", at(10, 42), opus, 256, "cat prs/01-a/spec.md"),
		asst("d10", at(10, 43), opus, 512, "prstk evidence add 02 --phase before --file x.png"),
		// Not one of this initiative's PRs: planning.
		asst("d11", at(10, 50), opus, 1024, "prstk show 99"),
		// A refused teardown doesn't end the window (planning).
		asst("d12", at(11, 0), opus, 2048, "prstk teardown"),
		toolError("tu_d12_0", at(11, 0)),
		// Pending, then the real teardown: overhead, and the window ends.
		asst("d13", at(12, 0), opus, 4096),
		asst("d14", at(12, 1), opus, 8192, "prstk teardown"),
		// After teardown: not counted.
		asst("d15", at(13, 0), opus, 16384, "prstk show 01"),
	)
	subs := filepath.Join(proj, "s1", "subagents")
	// w-01 from the file name; assigned to 01 then 03.
	writeLines(t, filepath.Join(subs, "agent-aw-01-0123456789abcdef.jsonl"),
		asst("w1a", at(9, 30), opus, 1), // before created
		asst("w1b", at(10, 30), opus, 10),
		asst("w1c", at(11, 30), opus, 20),
		asst("w1d", at(12, 30), opus, 40), // after teardown
	)
	// w-02 from the meta file, on a model with no price.
	writeLines(t, filepath.Join(subs, "agent-aw-02-1111111111111111.jsonl"),
		asst("w2a", at(10, 30), "mystery-model", 5),
	)
	writeLines(t, filepath.Join(subs, "agent-aw-02-1111111111111111.meta.json"), `{"name":"w-02"}`)
	// An Explore agent: director overhead, within the session window.
	writeLines(t, filepath.Join(subs, "agent-a0123456789abcdef0.jsonl"),
		asst("e1", at(8, 0), opus, 7), // before the session start
		asst("e2", at(10, 15), opus, 100),
	)
	writeLines(t, filepath.Join(subs, "agent-a0123456789abcdef0.meta.json"), `{"agentType":"Explore"}`)

	return Input{
		ClaudeDir: claude, Initiative: "2026-11-03-1000-demo", Created: at(10, 0),
		Sessions: []string{"s1"}, PRs: []string{"01", "02", "03"},
		Assigns: ParseAssigns(scenarioLog, time.UTC), Now: at(14, 0),
	}, claude
}

func checkInvariants(t *testing.T, r *Report) {
	t.Helper()
	sum := func(bs ...Bucket) Bucket {
		var s Bucket
		for _, b := range bs {
			s.Add(b)
		}
		return s
	}
	same := func(what string, got, want Bucket, cost float64) {
		t.Helper()
		if got.Messages != want.Messages || got.Tokens != want.Tokens {
			t.Errorf("%s: %d msgs %+v, want %d msgs %+v", what, got.Messages, got.Tokens, want.Messages, want.Tokens)
		}
		if math.Abs(got.CostUSD-cost) > 1e-9 {
			t.Errorf("%s: cost %v, want %v", what, got.CostUSD, cost)
		}
	}
	same("Total == Director + Workers", r.Total, sum(r.Director, r.Workers), r.Director.CostUSD+r.Workers.CostUSD)
	dir := []Bucket{r.Overhead}
	dirCost := r.Overhead.CostUSD
	var ws []Bucket
	var wCost float64
	for id, p := range r.PRs {
		dir = append(dir, p.Director)
		dirCost += p.Director.CostUSD
		prParts, prCost := []Bucket{p.Director}, p.Director.CostUSD
		for _, w := range p.Workers {
			ws = append(ws, w)
			wCost += w.CostUSD
			prParts = append(prParts, w)
			prCost += w.CostUSD
		}
		same("PRs["+id+"].Total == Director + Σ Workers", p.Total, sum(prParts...), prCost)
	}
	same("Director == Overhead + Σ PRs.Director", r.Director, sum(dir...), dirCost)
	same("Workers == Σ PRs.Workers", r.Workers, sum(ws...), wCost)
}

func TestCollectScenario(t *testing.T) {
	in, _ := scenario(t)
	r := Collect(in, nil)
	checkInvariants(t, r)
	if len(r.Warnings) != 0 {
		t.Errorf("warnings: %v", r.Warnings)
	}
	out := func(b Bucket) int64 { return b.Tokens.Output }
	for what, c := range map[string]struct{ got, want int64 }{
		"01 director": {out(r.PRs["01"].Director), 4 + 8 + 9},
		"02 director": {out(r.PRs["02"].Director), 8 + 32 + 64 + 128 + 256 + 512},
		"03 director": {out(r.PRs["03"].Director), 0},
		"overhead":    {out(r.Overhead), 1 + 2 + 1024 + 2048 + 4096 + 8192 + 100},
		"01 w-01":     {out(r.PRs["01"].Workers["w-01"]), 10},
		"03 w-01":     {out(r.PRs["03"].Workers["w-01"]), 20},
		"02 w-02":     {out(r.PRs["02"].Workers["w-02"]), 5},
		"workers":     {out(r.Workers), 35},
	} {
		if c.got != c.want {
			t.Errorf("%s output = %d, want %d", what, c.got, c.want)
		}
	}
	// The 17-token message split between 01 and 02 counts once in total.
	if got := r.PRs["01"].Director.Messages + r.PRs["02"].Director.Messages + r.Overhead.Messages; got != r.Director.Messages {
		t.Errorf("director messages %d vs %d", got, r.Director.Messages)
	}
	if r.Director.Messages != 15 { // d1..d14 (d0 and d15 out of window) + e2
		t.Errorf("director messages = %d, want 15", r.Director.Messages)
	}
	// Unpriced model: tokens counted, cost excluded, listed.
	w2 := r.PRs["02"].Workers["w-02"]
	if w2.CostUSD != 0 || len(w2.Unpriced) != 1 || w2.Unpriced[0] != "mystery-model" {
		t.Errorf("w-02 = %+v", w2)
	}
	if got := r.Total.Unpriced; len(got) != 1 {
		t.Errorf("total unpriced = %v", got)
	}
	if want := float64(10+20)*20/1e6 + 2*1*4/1e6; math.Abs(r.Workers.CostUSD-want) > 1e-12 {
		t.Errorf("workers cost = %v, want %v", r.Workers.CostUSD, want)
	}
	// Kinds and window bookkeeping on the sources.
	kinds := map[string]string{}
	for _, s := range r.Sources {
		kinds[filepath.Base(s.Path)] = s.Kind + ":" + s.Agent
		if s.Kind == KindDirector && len(s.Teardowns) != 2 {
			t.Errorf("director teardowns = %+v, want 2 (the refused one excluded)", s.Teardowns)
		}
	}
	for f, want := range map[string]string{
		"s1.jsonl":                           "director:",
		"agent-aw-01-0123456789abcdef.jsonl": "worker:w-01",
		"agent-aw-02-1111111111111111.jsonl": "worker:w-02",
		"agent-a0123456789abcdef0.jsonl":     "agent:",
	} {
		if kinds[f] != want {
			t.Errorf("%s kind = %q, want %q", f, kinds[f], want)
		}
	}
}

func TestCollectPricesOverride(t *testing.T) {
	in, _ := scenario(t)
	in.Prices = map[string]Price{"mystery": {Output: 1e6}} // $1 per output token
	r := Collect(in, nil)
	if w2 := r.PRs["02"].Workers["w-02"]; w2.CostUSD != 5 || len(w2.Unpriced) != 0 {
		t.Errorf("w-02 with a config price = %+v", w2)
	}
	checkInvariants(t, r)
}

func roundTrip(t *testing.T, r *Report) *Report {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.json")
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCollectMergeKeepsDeletedSources(t *testing.T) {
	in, claude := scenario(t)
	first := roundTrip(t, Collect(in, nil))

	w1 := filepath.Join(claude, "projects", "-repo", "s1", "subagents", "agent-aw-01-0123456789abcdef.jsonl")
	if err := os.Remove(w1); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(claude, "projects", "-repo", "s1.jsonl")); err != nil {
		t.Fatal(err)
	}
	second := Collect(in, first)
	checkInvariants(t, second)
	if second.Total.Tokens != first.Total.Tokens || second.PRs["03"].Workers["w-01"].Tokens != first.PRs["03"].Workers["w-01"].Tokens {
		t.Errorf("totals changed after deleting sources:\nfirst  %+v\nsecond %+v", first.Total.Tokens, second.Total.Tokens)
	}
	missing := 0
	for _, s := range second.Sources {
		if s.Missing {
			missing++
		}
	}
	if missing != 2 {
		t.Errorf("missing sources = %d, want 2", missing)
	}
	// And it stays stable across another round trip.
	third := Collect(in, roundTrip(t, second))
	if third.Total.Tokens != first.Total.Tokens {
		t.Errorf("third refresh: %+v", third.Total.Tokens)
	}
}

func TestCollectSkipsUnchangedFiles(t *testing.T) {
	in, claude := scenario(t)
	first := roundTrip(t, Collect(in, nil))

	// Same size and mtime, different content: the stored result is reused.
	p := filepath.Join(claude, "projects", "-repo", "s1", "subagents", "agent-aw-02-1111111111111111.jsonl")
	fi, _ := os.Stat(p)
	if err := os.WriteFile(p, []byte(strings.Repeat("x", int(fi.Size()))), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(p, fi.ModTime(), fi.ModTime())
	second := Collect(in, first)
	if second.Total.Tokens != first.Total.Tokens || len(second.Warnings) != 0 {
		t.Errorf("unchanged file was re-parsed: %+v %v", second.Total.Tokens, second.Warnings)
	}

	// A context change (a new assign) forces a re-parse.
	in.Assigns = append(in.Assigns, Assign{At: at(10, 1), PR: "03", Worker: "w-02"})
	third := Collect(in, first)
	if len(third.Warnings) == 0 {
		t.Error("context change should re-parse (and warn about the garbage line)")
	}
}

func TestCollectNoClaudeDir(t *testing.T) {
	r := Collect(Input{ClaudeDir: filepath.Join(t.TempDir(), "nope"), Sessions: []string{"s"}, PRs: []string{"01"}, Now: at(1, 0)}, nil)
	if len(r.Warnings) != 1 || r.Total.Messages != 0 || r.PRs["01"] == nil {
		t.Errorf("report = %+v", r)
	}
	r = Collect(Input{ClaudeDir: t.TempDir(), PRs: []string{"01"}}, nil)
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "no director sessions") {
		t.Errorf("warnings = %v", r.Warnings)
	}
	r = Collect(Input{ClaudeDir: t.TempDir(), Sessions: []string{"gone"}}, nil)
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "no transcript for director session gone") {
		t.Errorf("warnings = %v", r.Warnings)
	}
	checkInvariants(t, r)
}

func TestLoadMissing(t *testing.T) {
	r, err := Load(filepath.Join(t.TempDir(), "usage.json"))
	if r != nil || err != nil {
		t.Errorf("Load(missing) = %v, %v", r, err)
	}
}
