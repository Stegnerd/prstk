package render

import (
	"sort"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/usage"
)

// Usage UI (DESIGN.md §10). Everything here reads only usage.json, through
// usage.Load, once per build; the numbers arrive on the page already formatted
// with usage.FormatTokens and usage.FormatUSD, so the page has a single
// number format.

// noValue is shown instead of a zero for a bucket with no usage.
const noValue = "—"

// UsageFigure is one bucket as the board shows it.
type UsageFigure struct {
	Tokens     int64   `json:"tokens"`      // all kinds
	Output     int64   `json:"output"`      // output tokens
	CacheRead  int64   `json:"cache_read"`  // cache-read tokens (part of Tokens)
	CostUSD    float64 `json:"cost_usd"`    // priced models only
	Partial    bool    `json:"partial"`     // some model has no price: cost undercounts
	TokensText string  `json:"tokens_text"` // usage.FormatTokens(Tokens), cache reads included
	CostText   string  `json:"cost_text"`   // "$1.20", or "≥ $1.20" when Partial
	// CacheReadText is usage.FormatTokens(CacheRead), so the page can state how
	// much of the total is cache reads; empty when there are none.
	CacheReadText string `json:"cache_read_text,omitempty"`
	// Unpriced names the models without a price (sorted); set with Partial.
	Unpriced []string `json:"unpriced,omitempty"`
	// Empty is true when the bucket counts nothing; the page shows dashes.
	Empty bool `json:"empty,omitempty"`
}

func figure(b usage.Bucket) UsageFigure {
	f := UsageFigure{Tokens: b.Tokens.Total(), Output: b.Tokens.Output, CostUSD: b.CostUSD,
		CacheRead: b.Tokens.CacheRead, Partial: len(b.Unpriced) > 0, Empty: b.Empty()}
	if f.Empty {
		f.TokensText, f.CostText = noValue, noValue
		return f
	}
	f.TokensText = usage.FormatTokens(f.Tokens)
	if f.CacheRead > 0 {
		f.CacheReadText = usage.FormatTokens(f.CacheRead)
	}
	f.CostText = costText(b)
	if f.Partial {
		f.Unpriced = append([]string{}, b.Unpriced...)
	}
	return f
}

// costText is the dollar estimate, "≥ " prefixed when a model has no price,
// so a partial cost never reads as a plain total.
func costText(b usage.Bucket) string {
	if len(b.Unpriced) > 0 {
		return "≥ " + usage.FormatUSD(b.CostUSD)
	}
	return usage.FormatUSD(b.CostUSD)
}

// BoardUsage is the initiative's token usage strip. Nil (usage omitted) when
// there is no usable usage.json or nothing has been counted yet.
type BoardUsage struct {
	Total     UsageFigure `json:"total"`
	Director  UsageFigure `json:"director"` // includes overhead
	Workers   UsageFigure `json:"workers"`
	Overhead  UsageFigure `json:"overhead"` // director usage not attributed to a PR
	UpdatedAt time.Time   `json:"updated_at"`
	Warnings  int         `json:"warnings,omitempty"`
}

// CardUsage is a card's usage badge: the PR's total, with the director and
// worker split for the tooltip.
type CardUsage struct {
	Total    UsageFigure `json:"total"`
	Director UsageFigure `json:"director"`
	Workers  UsageFigure `json:"workers"`
}

// UsageRow is one row of the detail page's usage table.
type UsageRow struct {
	Label      string `json:"label"` // "Director", a worker name, "Total"
	Kind       string `json:"kind"`  // "director" | "worker" | "total"
	Empty      bool   `json:"empty,omitempty"`
	Input      string `json:"input"`
	CacheWrite string `json:"cache_write"` // 5m + 1h
	CacheRead  string `json:"cache_read"`
	Output     string `json:"output"`
	Total      string `json:"total"`
	Cost       string `json:"cost"` // "≥ $x" when partial
	Partial    bool   `json:"partial,omitempty"`
}

// UsageModel is one model's share of the PR total.
type UsageModel struct {
	Model    string `json:"model"`
	Total    string `json:"total"`
	Output   string `json:"output"`
	Unpriced bool   `json:"unpriced,omitempty"`
}

// DetailUsage is the PR detail page's Usage section. Nil when the PR has no
// usage yet.
type DetailUsage struct {
	Rows      []UsageRow   `json:"rows"`   // Director, each worker (sorted), Total
	Models    []UsageModel `json:"models"` // the total's per-model breakdown, biggest first; never nil
	Unpriced  []string     `json:"unpriced,omitempty"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// loadUsage reads in's usage.json. A missing, unreadable or future-version
// file is nil, so the board renders without usage rather than failing.
func loadUsage(in *plan.Initiative) *usage.Report {
	r, err := usage.Load(in.UsagePath())
	if err != nil || r == nil || r.Version != usage.Version {
		return nil
	}
	return r
}

func buildBoardUsage(r *usage.Report) *BoardUsage {
	if r == nil || r.Total.Empty() {
		return nil
	}
	return &BoardUsage{Total: figure(r.Total), Director: figure(r.Director), Workers: figure(r.Workers),
		Overhead: figure(r.Overhead), UpdatedAt: r.UpdatedAt, Warnings: len(r.Warnings)}
}

// workersBucket sums a PR's worker buckets (counts, then priced from the
// workers' own costs; Unpriced merged).
func workersBucket(u *usage.PRUsage) usage.Bucket {
	var out usage.Bucket
	seen := map[string]bool{}
	for _, b := range u.Workers {
		out.Add(b)
		out.CostUSD += b.CostUSD
		for _, m := range b.Unpriced {
			if !seen[m] {
				seen[m] = true
				out.Unpriced = append(out.Unpriced, m)
			}
		}
	}
	sort.Strings(out.Unpriced)
	return out
}

func buildCardUsage(r *usage.Report, id string) *CardUsage {
	if r == nil {
		return nil
	}
	u := r.PRs[id]
	if u == nil || u.Total.Empty() {
		return nil
	}
	return &CardUsage{Total: figure(u.Total), Director: figure(u.Director), Workers: figure(workersBucket(u))}
}

func usageRow(label, kind string, b usage.Bucket) UsageRow {
	if b.Empty() {
		d := noValue
		return UsageRow{Label: label, Kind: kind, Empty: true, Input: d, CacheWrite: d, CacheRead: d, Output: d, Total: d, Cost: d}
	}
	t := b.Tokens
	return UsageRow{Label: label, Kind: kind,
		Input:      usage.FormatTokens(t.Input),
		CacheWrite: usage.FormatTokens(t.CacheWrite5m + t.CacheWrite1h),
		CacheRead:  usage.FormatTokens(t.CacheRead),
		Output:     usage.FormatTokens(t.Output),
		Total:      usage.FormatTokens(t.Total()),
		Cost:       costText(b), Partial: len(b.Unpriced) > 0}
}

func buildDetailUsage(r *usage.Report, id string) *DetailUsage {
	if r == nil {
		return nil
	}
	u := r.PRs[id]
	if u == nil || u.Total.Empty() {
		return nil
	}
	d := &DetailUsage{UpdatedAt: r.UpdatedAt, Models: []UsageModel{}}
	d.Rows = append(d.Rows, usageRow("Director", "director", u.Director))
	names := make([]string, 0, len(u.Workers))
	for n := range u.Workers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d.Rows = append(d.Rows, usageRow(n, "worker", u.Workers[n]))
	}
	d.Rows = append(d.Rows, usageRow("Total", "total", u.Total))
	if len(u.Total.Unpriced) > 0 {
		d.Unpriced = append([]string{}, u.Total.Unpriced...)
	}
	unpriced := map[string]bool{}
	for _, m := range u.Total.Unpriced {
		unpriced[m] = true
	}
	for m, t := range u.Total.ByModel {
		if t.Total() == 0 {
			continue
		}
		d.Models = append(d.Models, UsageModel{Model: m, Total: usage.FormatTokens(t.Total()),
			Output: usage.FormatTokens(t.Output), Unpriced: unpriced[m]})
	}
	sort.Slice(d.Models, func(i, j int) bool {
		ti, tj := u.Total.ByModel[d.Models[i].Model].Total(), u.Total.ByModel[d.Models[j].Model].Total()
		if ti != tj {
			return ti > tj
		}
		return strings.Compare(d.Models[i].Model, d.Models[j].Model) < 0
	})
	return d
}
