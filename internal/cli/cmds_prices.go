package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/usage"
)

// pricesTimeout bounds the price-list fetch, so init never hangs on it.
const pricesTimeout = 10 * time.Second

// maxPriceList caps the download (LiteLLM's file is about 5 MB).
const maxPriceList = 32 << 20

// httpGet is the default App.FetchPrices.
func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPriceList+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxPriceList {
		return nil, fmt.Errorf("GET %s: larger than %d MB", url, maxPriceList>>20)
	}
	return b, nil
}

// pricesURL is $PRSTK_PRICES_URL or the LiteLLM list; "off" disables
// fetching.
func (a *App) pricesURL() string {
	if a.Getenv != nil {
		if u := strings.TrimSpace(a.Getenv("PRSTK_PRICES_URL")); u != "" {
			return u
		}
	}
	return usage.DefaultPricesURL
}

// refreshPrices fetches the price list and rewrites ~/.pr-plans/prices.json.
// On any failure the old file is kept. skipped is true when fetching is off.
func (a *App) refreshPrices() (n int, skipped bool, err error) {
	url := a.pricesURL()
	if a.FetchPrices == nil || url == "off" {
		return 0, true, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), pricesTimeout)
	defer cancel()
	data, err := a.FetchPrices(ctx, url)
	if err != nil {
		return 0, false, err
	}
	p, err := usage.ParseLiteLLM(data)
	if err != nil {
		return 0, false, err
	}
	b, err := usage.PriceFile{Version: usage.PriceFileVersion, FetchedAt: a.Now().UTC(), Source: url, Prices: p}.JSON()
	if err != nil {
		return 0, false, err
	}
	if err := os.MkdirAll(a.Home, 0o755); err != nil {
		return 0, false, err
	}
	if err := plan.WriteFileAtomic(plan.FetchedPricesPath(a.Home), b); err != nil {
		return 0, false, err
	}
	return len(p), false, nil
}

// refreshPricesQuietly is init's best-effort refresh: a failure is one line
// on stderr and never fails the command.
func (a *App) refreshPricesQuietly() {
	if _, _, err := a.refreshPrices(); err != nil {
		fallback := "the built-in table"
		if f, ferr := usage.LoadPriceFile(plan.FetchedPricesPath(a.Home)); ferr == nil && !f.FetchedAt.IsZero() {
			fallback = "prices fetched " + f.FetchedAt.Local().Format("2006-01-02")
		}
		fmt.Fprintf(a.Stderr, "prstk: couldn't refresh token prices (%v); using %s\n", err, fallback)
	}
}

// priceLayers are the layers over DefaultPrices, lowest first, with names
// for prstk prices. A layer that can't be read is left out with a warning.
type priceLayer struct {
	name   string
	prices map[string]usage.Price
}

func (a *App) priceLayers(repoPrices map[string]plan.PriceYAML) (layers []priceLayer, fetched usage.PriceFile, warnings []string) {
	fetched, err := usage.LoadPriceFile(plan.FetchedPricesPath(a.Home))
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	layers = append(layers, priceLayer{"fetched", fetched.Prices})
	global, err := plan.LoadGlobalPrices(a.Home)
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	layers = append(layers, priceLayer{"prices.yaml", toPrices(global)}, priceLayer{"config.yaml", toPrices(repoPrices)})
	return layers, fetched, warnings
}

func toPrices(in map[string]plan.PriceYAML) map[string]usage.Price {
	out := map[string]usage.Price{}
	for k, v := range in {
		out[k] = usage.Price(v)
	}
	return out
}

// effectivePrices is the price table usage.json is priced with.
func (a *App) effectivePrices(repoPrices map[string]plan.PriceYAML) (usage.Prices, []string) {
	layers, _, warnings := a.priceLayers(repoPrices)
	maps := make([]map[string]usage.Price, len(layers))
	for i, l := range layers {
		maps[i] = l.prices
	}
	return usage.WithOverrides(maps...), warnings
}

type priceRow struct {
	Model  string      `json:"model"`
	Price  usage.Price `json:"price"`
	Source string      `json:"source"` // built-in, fetched, prices.yaml or config.yaml
}

// cmdPrices prints the effective price table (or refreshes it first): the
// one place to look a price up, from any repo.
func cmdPrices(a *App, refresh bool) error {
	if refresh {
		n, skipped, err := a.refreshPrices()
		switch {
		case err != nil:
			return fmt.Errorf("refreshing prices: %w (the old prices are kept)", err)
		case skipped:
			return usageError{"price fetching is off (PRSTK_PRICES_URL=off)"}
		}
		if !a.jsonOut {
			a.out("fetched %d Claude prices from %s", n, a.pricesURL())
		}
	}
	var repoPrices map[string]plan.PriceYAML
	if rc, err := a.repoFromCwd(); err == nil {
		if cfg, err := plan.LoadConfig(filepath.Join(a.Home, rc.key)); err == nil {
			repoPrices = cfg.Prices
		}
	}
	layers, fetched, warnings := a.priceLayers(repoPrices)
	src := map[string]string{}
	eff := usage.Prices{}
	for k, v := range usage.DefaultPrices {
		eff[k], src[k] = v, "built-in"
	}
	for _, l := range layers {
		for k, v := range l.prices {
			eff[k], src[k] = v, l.name
		}
	}
	rows := make([]priceRow, 0, len(eff))
	for k, v := range eff {
		rows = append(rows, priceRow{k, v, src[k]})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Model < rows[j].Model })

	if a.jsonOut {
		return a.printJSON(struct {
			FetchedAt *time.Time `json:"fetched_at,omitempty"`
			Source    string     `json:"source,omitempty"`
			Prices    []priceRow `json:"prices"`
			Warnings  []string   `json:"warnings,omitempty"`
		}{fetchedAt(fetched), fetched.Source, rows, warnings})
	}
	for _, w := range warnings {
		a.out("warning: %s", w)
	}
	if t := fetchedAt(fetched); t != nil {
		a.out("fetched %s from %s", t.Local().Format("2006-01-02 15:04"), fetched.Source)
	} else {
		a.out("never fetched (prstk init fetches; or run prstk prices --refresh)")
	}
	a.out("$ per million tokens")
	a.out("%-26s %8s %9s %9s %8s %8s  %s", "model", "input", "write 5m", "write 1h", "read", "output", "source")
	for _, r := range rows {
		p := r.Price
		a.out("%-26s %8s %9s %9s %8s %8s  %s", r.Model, num(p.Input), num(p.CacheWrite5m), num(p.CacheWrite1h), num(p.CacheRead), num(p.Output), r.Source)
	}
	return nil
}

func fetchedAt(f usage.PriceFile) *time.Time {
	if f.FetchedAt.IsZero() {
		return nil
	}
	return &f.FetchedAt
}

// num prints a price without trailing zeros: 4, 0.2, 0.125.
func num(f float64) string {
	s := strings.TrimRight(fmt.Sprintf("%.4f", f), "0")
	return strings.TrimSuffix(s, ".")
}
