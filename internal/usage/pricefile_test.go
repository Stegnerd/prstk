package usage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const liteLLMSample = `{
  "sample_spec": {"input_cost_per_token": 0},
  "claude-opus-5-5": {"litellm_provider": "anthropic", "input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002,
    "cache_creation_input_token_cost": 0.000005, "cache_creation_input_token_cost_above_1hr": 0.000008, "cache_read_input_token_cost": 2e-7},
  "claude-new-9": {"litellm_provider": "anthropic", "input_cost_per_token": 0.000002, "output_cost_per_token": 0.00001},
  "claude-opus-latest": {},
  "claude-sonnet-4-5-20250929-v1:0": {"litellm_provider": "bedrock", "input_cost_per_token": 0.000003, "output_cost_per_token": 0.000015},
  "anthropic.claude-opus-5-5": {"litellm_provider": "bedrock", "input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002},
  "claude-broken": "not an object",
  "gpt-9": {"litellm_provider": "openai", "input_cost_per_token": 0.000001, "output_cost_per_token": 0.000002}
}`

func TestParseLiteLLM(t *testing.T) {
	p, err := ParseLiteLLM([]byte(liteLLMSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(p) != 2 {
		t.Fatalf("got %d entries, want only the two direct-API Claude ones: %v", len(p), p)
	}
	if got, want := p["claude-opus-5-5"], (Price{Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.2, Output: 20}); got != want {
		t.Errorf("opus = %+v, want %+v (exact, no float noise)", got, want)
	}
	if got, want := p["claude-new-9"], (Price{Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.2, Output: 10}); got != want {
		t.Errorf("missing cache prices should take the usual multipliers: %+v, want %+v", got, want)
	}
}

func TestParseLiteLLMRejectsUnusable(t *testing.T) {
	for name, data := range map[string]string{
		"not json":     "<html>rate limited</html>",
		"no claude":    `{"gpt-9": {"litellm_provider": "openai", "input_cost_per_token": 1, "output_cost_per_token": 1}}`,
		"only bedrock": `{"claude-x": {"litellm_provider": "bedrock", "input_cost_per_token": 1, "output_cost_per_token": 1}}`,
		"no output":    `{"claude-x": {"litellm_provider": "anthropic", "input_cost_per_token": 1}}`,
	} {
		if _, err := ParseLiteLLM([]byte(data)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestPriceFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if f, err := LoadPriceFile(path); err != nil || len(f.Prices) != 0 {
		t.Fatalf("missing file: %+v %v", f, err)
	}
	want := PriceFile{Version: PriceFileVersion, FetchedAt: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		Source: "https://example.test/p.json", Prices: Prices{"claude-x": {Input: 1, Output: 2}}}
	b, err := want.JSON()
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, b, 0o644)
	got, err := LoadPriceFile(path)
	if err != nil || !got.FetchedAt.Equal(want.FetchedAt) || got.Source != want.Source || got.Prices["claude-x"] != want.Prices["claude-x"] {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	os.WriteFile(path, []byte(`{"version": 99}`), 0o644)
	if _, err := LoadPriceFile(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("future version: %v", err)
	}
}

func TestWithOverridesLayers(t *testing.T) {
	fetched := map[string]Price{"claude-opus-5-5": {Input: 1}, "claude-new-9": {Input: 2}}
	global := map[string]Price{"claude-new-9": {Input: 3}}
	repo := map[string]Price{"claude-new-9": {Input: 4}}
	p := WithOverrides(fetched, global, repo)
	if p["claude-opus-5-5"].Input != 1 || p["claude-new-9"].Input != 4 || p["claude-haiku-5-5"] != DefaultPrices["claude-haiku-5-5"] {
		t.Errorf("layers: %+v", p)
	}
}
