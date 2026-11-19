package usage

import "strings"

// Price is dollars per million tokens of each kind.
type Price struct {
	Input        float64 `json:"input" yaml:"input"`
	CacheWrite5m float64 `json:"cache_write_5m" yaml:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h" yaml:"cache_write_1h"`
	CacheRead    float64 `json:"cache_read" yaml:"cache_read"`
	Output       float64 `json:"output" yaml:"output"`
}

// Cost is the dollar cost of t at this price.
func (p Price) Cost(t Tokens) float64 {
	return (float64(t.Input)*p.Input +
		float64(t.CacheWrite5m)*p.CacheWrite5m +
		float64(t.CacheWrite1h)*p.CacheWrite1h +
		float64(t.CacheRead)*p.CacheRead +
		float64(t.Output)*p.Output) / 1e6
}

// Prices maps a model-id prefix to its price.
type Prices map[string]Price

// DefaultPrices is the built-in price table, from Anthropic's price list as
// of 2026-10-06. Cache writes are 1.25× input (5-minute TTL) and 2× input
// (1-hour TTL); cache reads are 0.1× input except where noted.
var DefaultPrices = Prices{
	"claude-opus-5-5":   {Input: 4.00, CacheWrite5m: 5.00, CacheWrite1h: 8.00, CacheRead: 0.20, Output: 20.00},
	"claude-sonnet-5-5": {Input: 2.00, CacheWrite5m: 2.50, CacheWrite1h: 4.00, CacheRead: 0.20, Output: 10.00},
	// The Haiku cache-read price isn't on the list; this assumes the usual 0.1× input.
	"claude-haiku-5-5": {Input: 0.10, CacheWrite5m: 0.125, CacheWrite1h: 0.20, CacheRead: 0.01, Output: 0.50},
	"claude-fable-5-1": {Input: 10.00, CacheWrite5m: 12.50, CacheWrite1h: 20.00, CacheRead: 0.25, Output: 50.00},
}

// WithOverrides returns the built-in table with overrides replacing or
// adding entries (config.yaml `prices:`).
func WithOverrides(overrides map[string]Price) Prices {
	p := Prices{}
	for k, v := range DefaultPrices {
		p[k] = v
	}
	for k, v := range overrides {
		p[k] = v
	}
	return p
}

// Lookup finds the price whose key is the longest prefix of model.
func (p Prices) Lookup(model string) (Price, bool) {
	best, found := "", false
	var out Price
	for k, v := range p {
		if strings.HasPrefix(model, k) && (!found || len(k) > len(best)) {
			best, out, found = k, v, true
		}
	}
	return out, found
}
