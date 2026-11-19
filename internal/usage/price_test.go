package usage

import (
	"math"
	"testing"
)

func TestPriceLookupLongestPrefix(t *testing.T) {
	p := WithOverrides(map[string]Price{
		"claude-opus-5-5-fast": {Input: 99},
		"claude-sonnet-5-5":    {Input: 3, Output: 15}, // override
		"local-model":          {Output: 1},            // addition
	})
	for model, want := range map[string]float64{
		"claude-opus-5-5":          4,
		"claude-opus-5-5-20261001": 4,
		"claude-opus-5-5-fast":     99,
		"claude-opus-5-5-fast-x":   99,
		"claude-sonnet-5-5":        3,
	} {
		got, ok := p.Lookup(model)
		if !ok || got.Input != want {
			t.Errorf("%s: input %v (%v), want %v", model, got.Input, ok, want)
		}
	}
	if got, ok := p.Lookup("local-model-7b"); !ok || got.Output != 1 {
		t.Errorf("added entry not found: %+v %v", got, ok)
	}
	if _, ok := p.Lookup("gpt-9"); ok {
		t.Error("unknown model must have no price")
	}
	if DefaultPrices["claude-sonnet-5-5"].Input != 2 {
		t.Error("WithOverrides must not modify DefaultPrices")
	}
}

func TestBucketPrice(t *testing.T) {
	var b Bucket
	b.addTokens("claude-opus-5-5", Tokens{Input: 1e6, CacheWrite5m: 1e6, CacheWrite1h: 1e6, CacheRead: 1e6, Output: 1e6}, 1)
	b.addTokens("mystery-1", Tokens{Output: 1e6}, 1)
	b.price(DefaultPrices)
	if want := 4 + 5 + 8 + 0.2 + 20.0; math.Abs(b.CostUSD-want) > 1e-9 {
		t.Errorf("cost = %v, want %v (unpriced model excluded)", b.CostUSD, want)
	}
	if len(b.Unpriced) != 1 || b.Unpriced[0] != "mystery-1" {
		t.Errorf("unpriced = %v", b.Unpriced)
	}
	if b.Tokens.Output != 2e6 || b.Messages != 2 {
		t.Errorf("tokens still count for unpriced models: %+v", b)
	}
}

func TestFormat(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0", 950: "950", 999: "999", 1000: "1.0k", 12_345: "12.3k", 999_949: "999.9k",
		999_950: "1.00M", 4_561_000: "4.56M", 2_500_000_000: "2.50B",
	} {
		if got := FormatTokens(n); got != want {
			t.Errorf("FormatTokens(%d) = %q, want %q", n, got, want)
		}
	}
	for f, want := range map[float64]string{
		0: "$0.00", 0.42: "$0.42", 12.3: "$12.30", 999.99: "$999.99", 999.996: "$1,000",
		1234.4: "$1,234", 1_234_567.8: "$1,234,568",
	} {
		if got := FormatUSD(f); got != want {
			t.Errorf("FormatUSD(%v) = %q, want %q", f, got, want)
		}
	}
}

func TestBucketSplitSumsExactly(t *testing.T) {
	var b Bucket
	b.addTokens("m", Tokens{Input: 10, Output: 7, CacheRead: 5}, 3)
	b.addTokens("n", Tokens{Output: 1}, 0)
	parts := b.split(3)
	var sum Bucket
	for _, p := range parts {
		sum.Add(p)
	}
	if sum.Messages != b.Messages || sum.Tokens != b.Tokens || sum.ByModel["m"] != b.ByModel["m"] || sum.ByModel["n"] != b.ByModel["n"] {
		t.Errorf("split doesn't sum back: %+v vs %+v", sum, b)
	}
	if parts[0].Tokens.Output != 3+1 || parts[1].Tokens.Output != 2 {
		t.Errorf("remainder should go to the first part: %+v", parts)
	}
}
