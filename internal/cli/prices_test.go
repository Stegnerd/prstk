package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/usage"
)

const fakePriceList = `{
  "claude-opus-5-5": {"litellm_provider": "anthropic", "input_cost_per_token": 0.000004, "output_cost_per_token": 0.00002,
    "cache_creation_input_token_cost": 0.000005, "cache_creation_input_token_cost_above_1hr": 0.000008, "cache_read_input_token_cost": 2e-7},
  "claude-fable-9": {"litellm_provider": "anthropic", "input_cost_per_token": 0.00002, "output_cost_per_token": 0.0001}
}`

// runPrices runs prstk with a fake price fetcher that records the URL.
func (e *env) runPrices(fetch func(ctx context.Context, url string) ([]byte, error), vars map[string]string, args ...string) (string, string, int) {
	var o, er bytes.Buffer
	a := e.app(e.repo)
	a.Stdout, a.Stderr = &o, &er
	a.FetchPrices = fetch
	a.Getenv = func(k string) string { return vars[k] }
	code := a.Run(args)
	return strings.TrimSpace(o.String()), strings.TrimSpace(er.String()), code
}

func fakeFetch(body string, err error, gotURL *string) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, url string) ([]byte, error) {
		if gotURL != nil {
			*gotURL = url
		}
		return []byte(body), err
	}
}

func TestInitFetchesPrices(t *testing.T) {
	e := newEnv(t)
	var url string
	if _, errOut, code := e.runPrices(fakeFetch(fakePriceList, nil, &url), nil, "init", "tiny"); code != 0 || errOut != "" {
		t.Fatalf("init: exit %d, stderr %q", code, errOut)
	}
	if url != usage.DefaultPricesURL {
		t.Errorf("fetched %q, want the default URL", url)
	}
	f, err := usage.LoadPriceFile(plan.FetchedPricesPath(e.home))
	if err != nil || len(f.Prices) != 2 || f.Prices["claude-fable-9"].Input != 20 || f.Source != url || f.FetchedAt.IsZero() {
		t.Fatalf("prices.json: %+v %v", f, err)
	}
}

func TestInitPriceFetchFailureKeepsOldFile(t *testing.T) {
	e := newEnv(t)
	e.runPrices(fakeFetch(fakePriceList, nil, nil), nil, "init", "tiny")
	before, _ := os.ReadFile(plan.FetchedPricesPath(e.home))

	for name, fetch := range map[string]func(context.Context, string) ([]byte, error){
		"network": fakeFetch("", errors.New("dial tcp: no route"), nil),
		"garbage": fakeFetch("<html>rate limited</html>", nil, nil),
	} {
		_, errOut, code := e.runPrices(fetch, nil, "init", "tiny")
		if code != 0 {
			t.Fatalf("%s: init must not fail on a price fetch error (exit %d)", name, code)
		}
		if !strings.Contains(errOut, "couldn't refresh token prices") || !strings.Contains(errOut, "using prices fetched") {
			t.Errorf("%s: stderr %q", name, errOut)
		}
		if after, _ := os.ReadFile(plan.FetchedPricesPath(e.home)); !bytes.Equal(before, after) {
			t.Errorf("%s: prices.json changed", name)
		}
	}
}

func TestPricesURLOverrideAndOff(t *testing.T) {
	e := newEnv(t)
	var url string
	e.runPrices(fakeFetch(fakePriceList, nil, &url), map[string]string{"PRSTK_PRICES_URL": "https://example.test/p.json"}, "init", "tiny")
	if url != "https://example.test/p.json" {
		t.Errorf("fetched %q", url)
	}
	os.Remove(plan.FetchedPricesPath(e.home))
	called := false
	off := func(context.Context, string) ([]byte, error) { called = true; return nil, nil }
	if _, errOut, code := e.runPrices(off, map[string]string{"PRSTK_PRICES_URL": "off"}, "init", "tiny"); code != 0 || errOut != "" || called {
		t.Errorf("off: exit %d, stderr %q, fetched %v", code, errOut, called)
	}
	if _, _, code := e.runPrices(off, map[string]string{"PRSTK_PRICES_URL": "off"}, "prices", "--refresh"); code == 0 {
		t.Error("prices --refresh with fetching off must fail")
	}
}

func TestPricesShowsLayers(t *testing.T) {
	e := newEnv(t)
	e.runPrices(fakeFetch(fakePriceList, nil, nil), nil, "init", "tiny")
	os.WriteFile(plan.GlobalPricesPath(e.home), []byte("prices:\n  claude-fable-9: {input: 7, output: 70}\n  local-model: {output: 1}\n"), 0o644)
	repoDir := filepath.Join(e.home, "dalton-sample_proj")
	os.WriteFile(filepath.Join(repoDir, "config.yaml"), []byte("prices:\n  local-model: {output: 2}\n"), 0o644)

	out, _, code := e.runPrices(nil, nil, "prices")
	if code != 0 {
		t.Fatalf("prices: exit %d\n%s", code, out)
	}
	// Columns: model, input, write 5m, write 1h, read, output, source.
	for model, want := range map[string][]string{
		"claude-haiku-5-5": {"0.1", "0.5", "built-in"},
		"claude-opus-5-5":  {"4", "20", "fetched"},
		"claude-fable-9":   {"7", "70", "prices.yaml"},
		"local-model":      {"0", "2", "config.yaml"},
	} {
		line := lineWith(out, model+" ")
		if f := strings.Fields(line); len(f) != 7 || f[1] != want[0] || f[5] != want[1] || f[6] != want[2] {
			t.Errorf("%s: %q, want input %s, output %s from %s", model, line, want[0], want[1], want[2])
		}
	}
	if !strings.Contains(out, "fetched 2026-11-03") {
		t.Errorf("missing fetched line:\n%s", out)
	}

	// usage.json is priced with the same layers.
	a := e.app(e.repo)
	p, warnings := a.effectivePrices(map[string]plan.PriceYAML{"local-model": {Output: 2}})
	if len(warnings) != 0 || p["claude-fable-9"].Input != 7 || p["local-model"].Output != 2 || p["claude-opus-5-5"].Input != 4 {
		t.Errorf("effective prices: %+v %v", p, warnings)
	}
}

func TestPricesBadFilesWarn(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(e.home, 0o755)
	os.WriteFile(plan.FetchedPricesPath(e.home), []byte("{not json"), 0o644)
	os.WriteFile(plan.GlobalPricesPath(e.home), []byte("prices: [1, 2"), 0o644)
	out, _, code := e.runPrices(nil, nil, "prices")
	if code != 0 || strings.Count(out, "warning:") != 2 || !strings.Contains(out, "never fetched") || !strings.Contains(out, "claude-opus-5-5") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func lineWith(out, s string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, s) {
			return l
		}
	}
	return ""
}

func TestPricesRefreshCreatesHome(t *testing.T) {
	e := newEnv(t) // e.home doesn't exist yet
	out, errOut, code := e.runPrices(fakeFetch(fakePriceList, nil, nil), nil, "prices", "--refresh")
	if code != 0 || !strings.Contains(out, "fetched 2 Claude prices") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
}
