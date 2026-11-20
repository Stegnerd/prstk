package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"
)

// DefaultPricesURL is the price list prstk fetches: LiteLLM's model price
// map, which carries Anthropic's per-token prices including cache rates.
const DefaultPricesURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

// PriceFileVersion is the prices.json format version.
const PriceFileVersion = 1

// PriceFile is ~/.pr-plans/prices.json: the Claude prices from the last
// successful fetch. It layers over DefaultPrices, so a model the built-in
// table lacks still gets a price, and the file can be deleted at any time.
type PriceFile struct {
	Version   int       `json:"version"`
	FetchedAt time.Time `json:"fetched_at"`
	Source    string    `json:"source"`
	Prices    Prices    `json:"prices"`
}

// LoadPriceFile reads a prices.json. A missing file is an empty PriceFile,
// not an error.
func LoadPriceFile(path string) (PriceFile, error) {
	var f PriceFile
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return PriceFile{}, fmt.Errorf("%s: %w", path, err)
	}
	if f.Version > PriceFileVersion {
		return PriceFile{}, fmt.Errorf("%s: version %d is newer than this prstk understands (%d)", path, f.Version, PriceFileVersion)
	}
	return f, nil
}

// JSON renders the file for writing.
func (f PriceFile) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// liteLLMEntry is the part of a LiteLLM model entry prstk reads. Prices are
// dollars per token; nil means the field is absent.
type liteLLMEntry struct {
	Provider     string   `json:"litellm_provider"`
	Input        *float64 `json:"input_cost_per_token"`
	Output       *float64 `json:"output_cost_per_token"`
	CacheWrite5m *float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	CacheRead    *float64 `json:"cache_read_input_token_cost"`
}

// ParseLiteLLM extracts the Anthropic API's Claude prices (keys "claude-…"
// with provider "anthropic"; Bedrock, Vertex and Azure entries are skipped)
// from LiteLLM's price map, in dollars per million tokens. An entry needs
// input and output prices; missing cache prices take Anthropic's usual
// multipliers of input (1.25× and 2× for 5-minute and 1-hour writes, 0.1× for
// reads). It fails when no Claude entry is usable, so a broken or changed
// file never replaces a good one.
func ParseLiteLLM(data []byte) (Prices, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("price list: %w", err)
	}
	out := Prices{}
	for k, v := range raw {
		if !strings.HasPrefix(k, "claude-") {
			continue
		}
		var e liteLLMEntry
		if json.Unmarshal(v, &e) != nil || e.Provider != "anthropic" || e.Input == nil || e.Output == nil {
			continue
		}
		in := perMillion(*e.Input)
		p := Price{
			Input: in, Output: perMillion(*e.Output),
			CacheWrite5m: orDefault(e.CacheWrite5m, in*1.25),
			CacheWrite1h: orDefault(e.CacheWrite1h, in*2),
			CacheRead:    orDefault(e.CacheRead, in*0.1),
		}
		if p.Input < 0 || p.Output < 0 || p.CacheWrite5m < 0 || p.CacheWrite1h < 0 || p.CacheRead < 0 {
			continue
		}
		out[k] = p
	}
	if len(out) == 0 {
		return nil, errors.New("price list has no Anthropic Claude prices")
	}
	return out, nil
}

// perMillion converts dollars per token to dollars per million tokens,
// rounded to remove float noise (0.000004 × 1e6 = 4.000000000000001).
func perMillion(perToken float64) float64 {
	return math.Round(perToken*1e12) / 1e6
}

func orDefault(perToken *float64, def float64) float64 {
	if perToken == nil {
		return math.Round(def*1e6) / 1e6
	}
	return perMillion(*perToken)
}
