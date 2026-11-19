package render

import (
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/usage"
)

func bucket(in, cw5, cw1, cr, out int64, cost float64) usage.Bucket {
	return usage.Bucket{Messages: 1, CostUSD: cost,
		Tokens: usage.Tokens{Input: in, CacheWrite5m: cw5, CacheWrite1h: cw1, CacheRead: cr, Output: out}}
}

func samplePRUsage() *usage.PRUsage {
	return &usage.PRUsage{
		Director: bucket(40, 10_000, 5_000, 2_000_000, 4_000, 1.5),
		Workers: map[string]usage.Bucket{
			"w-03b": bucket(10, 1_000, 0, 100_000, 500, 0.1),
			"w-03":  bucket(60, 50_000, 0, 1_000_000, 20_000, 3.6),
		},
		Total: bucket(110, 66_000, 0, 3_100_000, 24_500, 5.2),
	}
}

func TestUsageBlockGolden(t *testing.T) {
	got := UsageBlock(samplePRUsage(), time.Date(2026, 11, 19, 16, 2, 0, 0, time.UTC))
	want := `<details><summary>Token usage (est.): 3.19M tokens (3.10M cached) · $5.20</summary>

| | Input | Cache write | Cache read | Output | Est. cost |
|---|---|---|---|---|---|
| Director | 40 | 15.0k | 2.00M | 4.0k | $1.50 |
| Worker w-03 | 60 | 50.0k | 1.00M | 20.0k | $3.60 |
| Worker w-03b | 10 | 1.0k | 100.0k | 500 | $0.10 |
| **Total** | **110** | **66.0k** | **3.10M** | **24.5k** | **$5.20** |

As of 2026-11-19 16:02. Estimated from Claude Code transcripts; director tokens are attributed by the prstk commands that named this PR.
</details>`
	if got != want {
		t.Errorf("UsageBlock:\n%s\nwant:\n%s", got, want)
	}
}

func TestUsageBlockPartialCost(t *testing.T) {
	pr := samplePRUsage()
	pr.Total.Unpriced = []string{"mystery-model"}
	w := pr.Workers["w-03"]
	w.Unpriced = []string{"mystery-model"}
	pr.Workers["w-03"] = w
	got := UsageBlock(pr, time.Date(2026, 11, 19, 16, 2, 0, 0, time.UTC))
	for _, want := range []string{
		"tokens (3.10M cached) · ≥ $5.20 (partial)</summary>",
		"| Worker w-03 | 60 | 50.0k | 1.00M | 20.0k | ≥ $3.60 |",
		"| **Total** | **110** | **66.0k** | **3.10M** | **24.5k** | **≥ $5.20 (partial)** |",
		"| Director | 40 | 15.0k | 2.00M | 4.0k | $1.50 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestUsageBlockEmpty(t *testing.T) {
	if got := UsageBlock(nil, time.Now()); got != "" {
		t.Errorf("nil usage: %q", got)
	}
	if got := UsageBlock(&usage.PRUsage{Workers: map[string]usage.Bucket{}}, time.Now()); got != "" {
		t.Errorf("zero usage: %q", got)
	}
	// An empty worker bucket gets no row.
	pr := samplePRUsage()
	pr.Workers["w-09"] = usage.Bucket{}
	if got := UsageBlock(pr, time.Now()); strings.Contains(got, "w-09") {
		t.Errorf("empty worker has a row:\n%s", got)
	}
}

func TestRemoveBlock(t *testing.T) {
	blk := UsageBegin + "\nx\n" + UsageEnd
	cases := map[string]string{
		"above\n\n" + blk + "\n":           "above\n",
		"above\n\n" + blk + "\n\nbelow\n":  "above\n\nbelow\n",
		blk + "\n\nbelow\n":                "below\n",
		"no block here\n":                  "no block here\n",
		"half " + UsageBegin + " no end\n": "half " + UsageBegin + " no end\n",
	}
	for in, want := range cases {
		if got := RemoveBlock(in, UsageBegin, UsageEnd); got != want {
			t.Errorf("RemoveBlock(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPRBodyOrder(t *testing.T) {
	body := PRBody("Does a thing.", "nav", "usage")
	i, j, k := strings.Index(body, "Does a thing."), strings.Index(body, StackBegin), strings.Index(body, UsageBegin)
	if !(i == 0 && i < j && j < k) {
		t.Errorf("order wrong:\n%s", body)
	}
	if strings.Contains(PRBody("d", "nav", ""), UsageBegin) {
		t.Error("empty usage should add no block")
	}
}

func TestUsageBlockNoCacheReads(t *testing.T) {
	pr := &usage.PRUsage{Total: bucket(1000, 0, 0, 0, 500, 0.2)}
	got := UsageBlock(pr, time.Date(2026, 11, 19, 16, 2, 0, 0, time.UTC))
	if !strings.Contains(got, "<summary>Token usage (est.): 1.5k tokens · $0.20</summary>") {
		t.Errorf("no cached text expected:\n%s", got)
	}
}
