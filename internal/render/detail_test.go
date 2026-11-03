package render

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sampleLog = `# Log · pokemon-api

Preamble text that is not an entry.

## 2026-11-03 15:57 · assign · prstk
02 → v-02 (in-progress, opus)

## 2026-11-03 14:22 · replan · director
Split 102 into parts; 2026 is a year, not a PR.

Second paragraph mentions 03 and v-02 and 02-board.
- bullet with (01)

## 2026-11-03 16:05 · review · director
Round 1 on 02. See 2026-11-02 and 15:02 and v1.02 — none of those count.
## not an entry · just a heading · inside a body
`

func TestParseLog(t *testing.T) {
	got := ParseLog(sampleLog, []string{"01", "02", "03", "102x"})
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(got), got)
	}
	// file order kept even though 14:22 is earlier than 15:57
	times := []string{got[0].Time, got[1].Time, got[2].Time}
	if !reflect.DeepEqual(times, []string{"2026-11-03 15:57", "2026-11-03 14:22", "2026-11-03 16:05"}) {
		t.Fatalf("order: %v", times)
	}
	e := got[0]
	if e.Type != "assign" || e.By != "prstk" || e.Body != "02 → v-02 (in-progress, opus)" || !reflect.DeepEqual(e.PRs, []string{"02"}) {
		t.Fatalf("entry 0: %+v", e)
	}
	e = got[1]
	if e.Type != "replan" || e.By != "director" {
		t.Fatalf("entry 1 head: %+v", e)
	}
	if !strings.HasPrefix(e.Body, "Split 102") || !strings.HasSuffix(e.Body, "- bullet with (01)") || strings.Count(e.Body, "\n") != 3 {
		t.Fatalf("multi-line body: %q", e.Body)
	}
	if !reflect.DeepEqual(e.PRs, []string{"01", "03"}) { // not 02: only "102", "v-02", "02-board"
		t.Fatalf("entry 1 prs: %v", e.PRs)
	}
	e = got[2]
	if !reflect.DeepEqual(e.PRs, []string{"02"}) {
		t.Fatalf("entry 2 prs: %v", e.PRs)
	}
	if !strings.Contains(e.Body, "## not an entry") {
		t.Fatalf("heading without a time must stay in the body: %q", e.Body)
	}
}

func TestParseLogSeparatorsAndEdges(t *testing.T) {
	// "·" is U+00B7; tolerate extra spaces and CRLF; by may contain spaces.
	text := "## 2026-11-03  09:01 ·  feedback·Dalton S \r\nlooks good, 7 and 07\r\n"
	got := ParseLog(text, []string{"7"})
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	e := got[0]
	if e.Time != "2026-11-03 09:01" || e.Type != "feedback" || e.By != "Dalton S" || e.Body != "looks good, 7 and 07" {
		t.Fatalf("%+v", e)
	}
	if !reflect.DeepEqual(e.PRs, []string{"7"}) {
		t.Fatalf("prs: %v", e.PRs)
	}
	// a hyphen or plain dash instead of the middle dot is not a header
	if got := ParseLog("## 2026-11-03 09:01 - init - prstk\nx\n", nil); len(got) != 0 {
		t.Fatalf("non-· separators parsed: %+v", got)
	}
	empty := ParseLog("", nil)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("want empty non-nil, got %#v", empty)
	}
	one := ParseLog("## 2026-11-03 09:01 · init · prstk\n", nil)
	if one[0].PRs == nil || one[0].Body != "" {
		t.Fatalf("PRs must be non-nil: %#v", one[0])
	}
	b, _ := json.Marshal(one[0])
	if !strings.Contains(string(b), `"prs":[]`) {
		t.Fatalf("json: %s", b)
	}
}

func TestMentions(t *testing.T) {
	known := []string{"02", "03", "2026", "05b"}
	cases := map[string][]string{
		"02":                     {"02"},
		"on 02.":                 {"02"},
		"(02), 03!":              {"02", "03"},
		"02/03":                  {"02", "03"},
		"#02":                    {"02"},
		"102 1020 v-02 02-a 02a": {},
		"2026-11-02":             {},
		"at 15:02 and 1.02":      {},
		"in 2026 we did 05b":     {"2026", "05b"},
		"**02** `03`":            {"02", "03"},
	}
	for in, want := range cases {
		if got := mentions(in, known); !reflect.DeepEqual(got, want) {
			t.Errorf("mentions(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSplitSpecNoReviews(t *testing.T) {
	body := "## Goal\n\nDo it.\n\n## Definition of done\n- tests\n"
	got := SplitSpec(body)
	if got.Spec != strings.TrimSpace(body) || got.Dalton != "" || got.DirectorRounds == nil || len(got.DirectorRounds) != 0 {
		t.Fatalf("%#v", got)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"director_rounds":[]`) {
		t.Fatalf("json: %s", b)
	}
}

func TestSplitSpecRounds(t *testing.T) {
	body := "## Goal\nG\n\n```markdown\n## Review: Director\n### not a round\n```\n\n" +
		"## Review: Director\n\n### Round 1 (2026-11-03)\n- fix A\n\n```go\n### code, not a round\n```\n\n### Round 2\nall good\n\n" +
		"## Review: Dalton\n\nPlease rename X.\n\n### sub-heading stays in Dalton\nmore\n"
	got := SplitSpec(body)
	if !strings.HasPrefix(got.Spec, "## Goal") || !strings.Contains(got.Spec, "### not a round") || strings.Contains(got.Spec, "Round 1") {
		t.Fatalf("spec: %q", got.Spec)
	}
	if len(got.DirectorRounds) != 2 {
		t.Fatalf("rounds: %#v", got.DirectorRounds)
	}
	r1, r2 := got.DirectorRounds[0], got.DirectorRounds[1]
	if r1.Heading != "Round 1 (2026-11-03)" || !strings.HasPrefix(r1.Body, "- fix A") || !strings.Contains(r1.Body, "### code, not a round") {
		t.Fatalf("r1: %#v", r1)
	}
	if r2.Heading != "Round 2" || r2.Body != "all good" {
		t.Fatalf("r2: %#v", r2)
	}
	if got.Dalton != "Please rename X.\n\n### sub-heading stays in Dalton\nmore" {
		t.Fatalf("dalton: %q", got.Dalton)
	}
}

func TestSplitSpecOnlyDalton(t *testing.T) {
	got := SplitSpec("## Goal\nG\n\n## Review: Dalton\n  feedback here  \n")
	if got.Spec != "## Goal\nG" || got.Dalton != "feedback here" || len(got.DirectorRounds) != 0 {
		t.Fatalf("%#v", got)
	}
	// an empty Director section (as `prstk add` writes it) yields no rounds
	got = SplitSpec("## Goal\nG\n\n## Review: Director\n\n## Review: Dalton\n")
	if got.Spec != "## Goal\nG" || got.Dalton != "" || len(got.DirectorRounds) != 0 {
		t.Fatalf("empty sections: %#v", got)
	}
	// director text before the first ### is kept as an untitled round
	got = SplitSpec("## Review: Director\nloose finding\n### R1\nx\n")
	if len(got.DirectorRounds) != 2 || got.DirectorRounds[0] != (ReviewRound{Body: "loose finding"}) || got.DirectorRounds[1].Heading != "R1" {
		t.Fatalf("preamble: %#v", got.DirectorRounds)
	}
}

func TestBuildPRDetail(t *testing.T) {
	in := pilot(t)
	p := in.PR("02")
	os.WriteFile(p.NotesPath(), []byte("## Description\n<script>alert(1)</script>\n"), 0o644)
	os.WriteFile(in.LogPath(), []byte(sampleLog), 0o644)
	d, ok := BuildPRDetail(in, "02", time.Now())
	if !ok {
		t.Fatal("02 not found")
	}
	if d.ID != "02" || d.Parent != "01" || d.Status != "awaiting-you" || d.URL != "https://github.com/dalton/sample_proj/pull/102" || d.Model != "sonnet" {
		t.Fatalf("detail: %+v", d)
	}
	if !strings.Contains(d.Notes, "<script>") { // raw: sanitizing is the browser's job
		t.Fatalf("notes: %q", d.Notes)
	}
	if len(d.Log) != 2 || d.Log[0].Type != "assign" || d.Log[1].Type != "review" {
		t.Fatalf("log: %+v", d.Log)
	}
	b, _ := json.Marshal(d)
	for _, k := range []string{`"id":"02"`, `"parent":"01"`, `"branch":`, `"spec":{"spec":`, `"director_rounds":[]`, `"notes":`, `"log":[`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("json missing %s: %s", k, b)
		}
	}
	if _, ok := BuildPRDetail(in, "99", time.Now()); ok {
		t.Fatal("unknown id must be false")
	}
	if _, ok := BuildPRDetail(in, "../02", time.Now()); ok {
		t.Fatal("traversal id must be false")
	}
	os.Remove(in.LogPath())
	if d, _ := BuildPRDetail(in, "02", time.Now()); d.Log == nil || len(d.Log) != 0 {
		t.Fatalf("missing LOG.md: %#v", d.Log)
	}
}

func TestDetailPageOpts(t *testing.T) {
	page, err := BoardPage(PageOpts{Title: "<b>t</b>", API: "/api/x", Events: "/events", View: ViewPR, Key: "r/n", Base: "/i/r/n/", ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	for _, w := range []string{`"view":"pr"`, `"key":"r/n"`, `"base":"/i/r/n/"`, `"readonly":true`, `"events":""`, "&lt;b&gt;t&lt;/b&gt;",
		"marked@18.1.1", "dompurify@3.4.17", `integrity="sha384-`, "DOMPurify.sanitize(marked.parse("} {
		if !strings.Contains(s, w) {
			t.Errorf("page missing %q", w)
		}
	}
	if strings.Contains(s, "<b>t</b>") {
		t.Error("title not escaped")
	}
}
