package render

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/usage"
)

func usageTok(in, cw, cr, out int64) usage.Tokens {
	return usage.Tokens{Input: in, CacheWrite5m: cw * 3 / 4, CacheWrite1h: cw - cw*3/4, CacheRead: cr, Output: out}
}

func usageBucket(msgs int, cost float64, byModel map[string]usage.Tokens, unpriced ...string) usage.Bucket {
	b := usage.Bucket{Messages: msgs, CostUSD: cost, ByModel: byModel, Unpriced: unpriced}
	for _, t := range byModel {
		b.Tokens.Input += t.Input
		b.Tokens.CacheWrite5m += t.CacheWrite5m
		b.Tokens.CacheWrite1h += t.CacheWrite1h
		b.Tokens.CacheRead += t.CacheRead
		b.Tokens.Output += t.Output
	}
	return b
}

func usageSum(bs ...usage.Bucket) usage.Bucket {
	var out usage.Bucket
	for _, b := range bs {
		out.Add(b)
		out.CostUSD += b.CostUSD
		out.Unpriced = append(out.Unpriced, b.Unpriced...)
	}
	return out
}

func writeUsageJSON(t *testing.T, in *plan.Initiative, r *usage.Report) {
	t.Helper()
	b, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.UsagePath(), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// sampleReport has usage on PRs 02 (director, two workers incl. a fix worker,
// one model unpriced), 03 (director only) and 04 (worker only). PR 01 has none.
func sampleUsageReport() *usage.Report {
	d02 := usageBucket(5, 1.00, map[string]usage.Tokens{"claude-opus-5-5": usageTok(10, 1000, 200_000, 2000)})
	w02 := usageBucket(9, 2.00, map[string]usage.Tokens{"claude-sonnet-5-5": usageTok(20, 4000, 800_000, 9000)})
	w02b := usageBucket(3, 0.50, map[string]usage.Tokens{"claude-sonnet-5-5": usageTok(6, 500, 100_000, 1000), "claude-x-9": usageTok(1, 100, 10_000, 500)}, "claude-x-9")
	d03 := usageBucket(4, 0.40, map[string]usage.Tokens{"claude-opus-5-5": usageTok(5, 500, 50_000, 800)})
	w04 := usageBucket(6, 0.70, map[string]usage.Tokens{"claude-sonnet-5-5": usageTok(8, 700, 90_000, 1500)})
	over := usageBucket(2, 0.10, map[string]usage.Tokens{"claude-opus-5-5": usageTok(2, 100, 20_000, 300)})
	r := &usage.Report{
		Version:   usage.Version,
		UpdatedAt: time.Date(2026, 11, 19, 20, 0, 0, 0, time.UTC),
		PRs: map[string]*usage.PRUsage{
			"01": {Workers: map[string]usage.Bucket{}},
			"02": {Director: d02, Workers: map[string]usage.Bucket{"w-02": w02, "w-02b": w02b}, Total: usageSum(d02, w02, w02b)},
			"03": {Director: d03, Workers: map[string]usage.Bucket{}, Total: usageSum(d03)},
			"04": {Workers: map[string]usage.Bucket{"w-04": w04}, Total: usageSum(w04)},
		},
		Overhead: over,
		Director: usageSum(d02, d03, over),
		Workers:  usageSum(w02, w02b, w04),
		Warnings: []string{"one", "two"},
	}
	r.Total = usageSum(r.Director, r.Workers)
	return r
}

func TestBoardWithoutUsageJSON(t *testing.T) {
	in := pilot(t)
	b := BuildBoard(in, time.Now())
	if b.Usage != nil {
		t.Fatalf("Usage = %+v, want nil", b.Usage)
	}
	for _, l := range b.Lanes {
		for _, c := range l.Cards {
			if c.Usage != nil {
				t.Errorf("card %s usage = %+v", c.ID, c.Usage)
			}
		}
	}
	js, _ := json.Marshal(b)
	if strings.Contains(string(js), `"usage"`) {
		t.Fatalf("board JSON mentions usage without usage.json: %s", js)
	}
	d, ok := BuildPRDetail(in, "02", time.Now())
	if !ok || d.Usage != nil {
		t.Fatalf("detail usage = %+v", d.Usage)
	}
	if js, _ := json.Marshal(d); strings.Contains(string(js), `"usage"`) {
		t.Fatalf("detail JSON mentions usage: %s", js)
	}
}

func TestBoardUsage(t *testing.T) {
	in := pilot(t)
	r := sampleUsageReport()
	writeUsageJSON(t, in, r)
	b := BuildBoard(in, time.Now())
	u := b.Usage
	if u == nil {
		t.Fatal("no board usage")
	}
	if u.Total.Tokens != r.Total.Tokens.Total() || u.Total.TokensText != usage.FormatTokens(r.Total.Tokens.Total()) {
		t.Errorf("total tokens %d %q", u.Total.Tokens, u.Total.TokensText)
	}
	if !u.Total.Partial || !strings.HasPrefix(u.Total.CostText, "≥ $") || !reflect.DeepEqual(u.Total.Unpriced, []string{"claude-x-9"}) {
		t.Errorf("total must be partial: %+v", u.Total)
	}
	// The total keeps cache reads and states them separately.
	if u.Total.CacheRead != r.Total.Tokens.CacheRead || u.Total.CacheReadText != usage.FormatTokens(r.Total.Tokens.CacheRead) {
		t.Errorf("total cache read %d %q", u.Total.CacheRead, u.Total.CacheReadText)
	}
	if u.Total.Tokens <= u.Total.CacheRead || u.Total.Tokens != r.Total.Tokens.Total() {
		t.Errorf("total %d must include cache reads %d", u.Total.Tokens, u.Total.CacheRead)
	}
	if u.Director.CacheReadText != usage.FormatTokens(r.Director.Tokens.CacheRead) ||
		u.Workers.CacheReadText != usage.FormatTokens(r.Workers.Tokens.CacheRead) ||
		u.Overhead.CacheReadText != usage.FormatTokens(r.Overhead.Tokens.CacheRead) {
		t.Errorf("cache read texts: %q %q %q", u.Director.CacheReadText, u.Workers.CacheReadText, u.Overhead.CacheReadText)
	}
	// director has no unpriced model: a plain cost
	if u.Director.Partial || u.Director.CostText != usage.FormatUSD(r.Director.CostUSD) {
		t.Errorf("director: %+v", u.Director)
	}
	if !u.Workers.Partial || u.Overhead.Tokens != r.Overhead.Tokens.Total() {
		t.Errorf("workers/overhead: %+v %+v", u.Workers, u.Overhead)
	}
	if !u.UpdatedAt.Equal(r.UpdatedAt) || u.Warnings != 2 {
		t.Errorf("updated/warnings: %v %d", u.UpdatedAt, u.Warnings)
	}

	card := map[string]Card{}
	for _, l := range b.Lanes {
		for _, c := range l.Cards {
			card[c.ID] = c
		}
	}
	if card["01"].Usage != nil {
		t.Errorf("PR 01 has no usage, card.Usage = %+v", card["01"].Usage)
	}
	if card["05"].Usage != nil { // not in the report at all
		t.Errorf("PR 05 not in report, card.Usage = %+v", card["05"].Usage)
	}
	c2 := card["02"].Usage
	if c2 == nil || c2.Total.Tokens != r.PRs["02"].Total.Tokens.Total() || c2.Director.Empty || c2.Workers.Empty {
		t.Fatalf("card 02: %+v", c2)
	}
	if !c2.Workers.Partial || c2.Director.Partial {
		t.Errorf("card 02 partial flags: workers %v director %v", c2.Workers.Partial, c2.Director.Partial)
	}
	if c2.Total.CacheReadText != usage.FormatTokens(r.PRs["02"].Total.Tokens.CacheRead) || c2.Total.CacheReadText == "" {
		t.Errorf("card 02 cache read text %q", c2.Total.CacheReadText)
	}
	if c3 := card["03"].Usage; c3 == nil || !c3.Workers.Empty || c3.Workers.TokensText != "—" || c3.Director.Empty {
		t.Errorf("card 03 (director only): %+v", c3)
	}
	if c4 := card["04"].Usage; c4 == nil || !c4.Director.Empty || c4.Workers.Empty {
		t.Errorf("card 04 (worker only): %+v", c4)
	}
}

func TestDetailUsageRows(t *testing.T) {
	in := pilot(t)
	r := sampleUsageReport()
	writeUsageJSON(t, in, r)

	d, ok := BuildPRDetail(in, "02", time.Now())
	if !ok || d.Usage == nil {
		t.Fatalf("detail usage missing: %+v", d.Usage)
	}
	var labels []string
	for _, row := range d.Usage.Rows {
		labels = append(labels, row.Label)
	}
	if !reflect.DeepEqual(labels, []string{"Director", "w-02", "w-02b", "Total"}) {
		t.Fatalf("rows = %v", labels)
	}
	dir := d.Usage.Rows[0]
	t02 := r.PRs["02"].Director.Tokens
	if dir.Kind != "director" || dir.Input != usage.FormatTokens(t02.Input) ||
		dir.CacheWrite != usage.FormatTokens(t02.CacheWrite5m+t02.CacheWrite1h) ||
		dir.CacheRead != usage.FormatTokens(t02.CacheRead) || dir.Output != usage.FormatTokens(t02.Output) ||
		dir.Total != usage.FormatTokens(t02.Total()) || dir.Cost != "$1.00" || dir.Partial || dir.Empty {
		t.Errorf("director row: %+v", dir)
	}
	fix := d.Usage.Rows[2]
	if fix.Kind != "worker" || !fix.Partial || fix.Cost != "≥ $0.50" {
		t.Errorf("fix worker row (unpriced model): %+v", fix)
	}
	total := d.Usage.Rows[3]
	if total.Kind != "total" || !total.Partial || total.Cost != "≥ $3.50" {
		t.Errorf("total row: %+v", total)
	}
	if !reflect.DeepEqual(d.Usage.Unpriced, []string{"claude-x-9"}) {
		t.Errorf("unpriced: %v", d.Usage.Unpriced)
	}
	// models: biggest first, unpriced flagged
	var ms []string
	for _, m := range d.Usage.Models {
		ms = append(ms, m.Model)
		if m.Unpriced != (m.Model == "claude-x-9") {
			t.Errorf("model %s unpriced = %v", m.Model, m.Unpriced)
		}
	}
	if !reflect.DeepEqual(ms, []string{"claude-sonnet-5-5", "claude-opus-5-5", "claude-x-9"}) {
		t.Errorf("models order: %v", ms)
	}
	// the card embedded in the detail carries no second copy of usage
	if d.Card.Usage != nil {
		t.Errorf("Card.Usage = %+v", d.Card.Usage)
	}
	js, _ := json.Marshal(d)
	var m map[string]any
	json.Unmarshal(js, &m)
	if _, ok := m["usage"].(map[string]any)["rows"]; !ok {
		t.Errorf("detail JSON usage should be the DetailUsage: %s", js)
	}
}

func TestDetailUsageDashesAndAbsent(t *testing.T) {
	in := pilot(t)
	writeUsageJSON(t, in, sampleUsageReport())

	d, _ := BuildPRDetail(in, "03", time.Now()) // director only
	if d.Usage == nil || len(d.Usage.Rows) != 2 || d.Usage.Rows[0].Empty || d.Usage.Rows[1].Label != "Total" {
		t.Fatalf("03 rows: %+v", d.Usage)
	}
	d, _ = BuildPRDetail(in, "04", time.Now()) // worker only: director row is dashes, not zeros
	if d.Usage == nil || len(d.Usage.Rows) != 3 {
		t.Fatalf("04 rows: %+v", d.Usage)
	}
	row := d.Usage.Rows[0]
	if !row.Empty || row.Total != "—" || row.Cost != "—" || row.Input != "—" || row.Output != "—" {
		t.Errorf("04 director row: %+v", row)
	}
	if d.Usage.Rows[1].Label != "w-04" || d.Usage.Rows[1].Empty {
		t.Errorf("04 worker row: %+v", d.Usage.Rows[1])
	}
	for _, id := range []string{"01", "05"} { // empty / absent
		d, _ = BuildPRDetail(in, id, time.Now())
		if d.Usage != nil {
			t.Errorf("PR %s usage = %+v, want nil", id, d.Usage)
		}
	}
}

func TestUnusableUsageJSONRendersWithoutUsage(t *testing.T) {
	cases := map[string]string{
		"malformed":  "{not json",
		"truncated":  `{"version":1,"total":{"messages":`,
		"future":     `{"version":2,"total":{"messages":3,"tokens":{"input":5}}}`,
		"no version": `{"total":{"messages":3,"tokens":{"input":5}}}`,
		"empty":      `{"version":1,"prs":{}}`,
		"empty file": ``,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			in := pilot(t)
			if err := os.WriteFile(in.UsagePath(), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			b := BuildBoard(in, time.Now())
			if b.Usage != nil {
				t.Errorf("board usage = %+v", b.Usage)
			}
			if len(b.Lanes) == 0 {
				t.Error("board must still render")
			}
			if d, ok := BuildPRDetail(in, "02", time.Now()); !ok || d.Usage != nil {
				t.Errorf("detail ok=%v usage=%+v", ok, d.Usage)
			}
		})
	}
}

func TestStaticPageInlinesUsage(t *testing.T) {
	in := pilot(t)
	writeUsageJSON(t, in, sampleUsageReport())
	b := BuildBoard(in, time.Now())
	page, err := BoardPage(PageOpts{Title: "t", Static: true, Data: &b})
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	if !strings.Contains(s, `"usage":{"total":`) || !strings.Contains(s, `"tokens_text"`) {
		t.Errorf("static page does not carry usage")
	}
	// the page script has the three usage renderers
	for _, want := range []string{"usageStrip(d.usage)", "usageBadge(c.usage)", "usageSec(d.usage)"} {
		if !strings.Contains(s, want) {
			t.Errorf("template missing %s", want)
		}
	}
}

func TestFigureCacheRead(t *testing.T) {
	f := figure(usageBucket(1, 0.65, map[string]usage.Tokens{"claude-opus-5-5": usageTok(1000, 90_000, 1_280_000, 5000)}))
	if f.TokensText != usage.FormatTokens(f.Tokens) || f.CacheReadText != "1.28M" || f.Tokens <= f.CacheRead {
		t.Errorf("figure: %+v", f)
	}
	// No cache reads: no cache-read text, so the page adds no parenthetical.
	f = figure(usageBucket(1, 0.01, map[string]usage.Tokens{"claude-opus-5-5": usageTok(1000, 0, 0, 500)}))
	if f.CacheReadText != "" || f.CacheRead != 0 {
		t.Errorf("figure without cache reads: %+v", f)
	}
	// An empty bucket stays dashes.
	if f = figure(usage.Bucket{}); !f.Empty || f.CacheReadText != "" || f.TokensText != noValue {
		t.Errorf("empty figure: %+v", f)
	}
}
