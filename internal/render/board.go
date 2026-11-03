package render

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"prstk/internal/plan"
)

var Columns = []string{"Planned", "Ready", "In Progress", "Director Review", "Awaiting You", "Merged"}

type Board struct {
	Key        string       `json:"key"` // <repoKey>/<initiative dir>
	Initiative string       `json:"initiative"`
	Dir        string       `json:"dir"`
	Repo       string       `json:"repo"`
	Base       string       `json:"base"`
	MaxWorkers int          `json:"max_workers"`
	Updated    time.Time    `json:"updated"`
	Slots      []Slot       `json:"slots"`
	Columns    []string     `json:"columns"`
	Lanes      []Lane       `json:"lanes"`
	Mermaid    string       `json:"mermaid"`
	Issues     []plan.Issue `json:"issues"`
	Next       []string     `json:"next"`
}

type Slot struct {
	Worker string    `json:"worker"`
	PR     string    `json:"pr"`
	Status string    `json:"status"`
	Since  time.Time `json:"since"`
}

type Lane struct {
	Label string `json:"label"`
	Cards []Card `json:"cards"`
}

type Card struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Parent   string    `json:"parent"`
	Status   string    `json:"status"`
	Column   string    `json:"column"`
	Tag      string    `json:"tag,omitempty"`
	PRNumber int       `json:"pr_number,omitempty"`
	URL      string    `json:"url,omitempty"`
	Opus     bool      `json:"opus,omitempty"`
	Blocked  bool      `json:"blocked,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Worker   string    `json:"worker,omitempty"`
	Since    time.Time `json:"since"`
}

func column(in *plan.Initiative, p *plan.Spec) (col, tag string) {
	switch p.Status {
	case plan.Planned:
		if in.IsReady(p) {
			return "Ready", ""
		}
		return "Planned", ""
	case plan.FixPending:
		return "Ready", "fix"
	case plan.InProgress:
		return "In Progress", ""
	case plan.Blocked:
		return "In Progress", "blocked"
	case plan.ChangesRequested:
		return "In Progress", "changes requested"
	case plan.Fixing:
		return "In Progress", "fixing"
	case plan.DirectorReview:
		return "Director Review", ""
	case plan.AwaitingYou:
		return "Awaiting You", ""
	case plan.Merged:
		return "Merged", ""
	}
	return "", ""
}

func BuildBoard(in *plan.Initiative, now time.Time) Board {
	b := Board{
		Key: in.RepoKey + "/" + in.Name, Initiative: in.Meta.Initiative, Dir: in.Name,
		Repo: in.Meta.Repo, Base: in.Meta.BaseBranch, MaxWorkers: in.MaxWorkers(),
		Updated: now, Columns: Columns, Mermaid: Mermaid(in), Issues: in.Validate(now),
		Slots: []Slot{}, Lanes: []Lane{}, Next: []string{},
	}
	if b.Issues == nil {
		b.Issues = []plan.Issue{}
	}
	for _, p := range in.PRs {
		if p.Status.HoldsSlot() {
			b.Slots = append(b.Slots, Slot{Worker: p.Worker, PR: p.ID, Status: string(p.Status), Since: p.StatusSince})
		}
	}
	cands, _ := in.Next()
	for _, c := range cands {
		b.Next = append(b.Next, c.ID)
	}
	card := func(p *plan.Spec) Card {
		col, tag := column(in, p)
		c := Card{ID: p.ID, Title: p.Title, Parent: p.Parent, Status: DisplayStatus(in, p), Column: col, Tag: tag,
			PRNumber: p.PRNumber, Opus: p.Model == "opus", Blocked: p.Status == plan.Blocked,
			Reason: p.BlockedReason, Worker: p.Worker, Since: p.StatusSince}
		if p.PRNumber > 0 {
			c.URL = fmt.Sprintf("https://github.com/%s/pull/%d", in.Meta.Repo, p.PRNumber)
		}
		return c
	}
	shown := map[string]bool{}
	for i, path := range in.Paths() {
		var lane Lane
		for _, p := range path {
			if shown[p.ID] {
				continue
			}
			if len(lane.Cards) == 0 && i > 0 {
				off := p.Parent
				if off == "" || in.PR(off) == nil {
					off = in.Meta.BaseBranch
				}
				lane.Label = "off " + off
			}
			shown[p.ID] = true
			lane.Cards = append(lane.Cards, card(p))
		}
		if i == 0 {
			lane.Label = "main"
		}
		if len(lane.Cards) > 0 {
			b.Lanes = append(b.Lanes, lane)
		}
	}
	// PRs not on any path (cycles, missing parents that loop) — keep visible.
	var stray Lane
	for _, p := range in.PRs {
		if !shown[p.ID] && p.Status != plan.Abandoned {
			stray.Cards = append(stray.Cards, card(p))
		}
	}
	if len(stray.Cards) > 0 {
		stray.Label = "unattached"
		b.Lanes = append(b.Lanes, stray)
	}
	return b
}

//go:embed board.html.tmpl
var boardTmpl string

var tmpl = template.Must(template.New("board").Parse(boardTmpl))

// BoardPage renders a board page. For the static fallback pass data (it is
// inlined and the page meta-refreshes); for live mode pass api/events URLs.
type PageOpts struct {
	Title   string
	Static  bool
	Data    *Board
	API     string // live: JSON endpoint
	Events  string // live: SSE endpoint
	IndexJS bool   // live index page
}

func BoardPage(o PageOpts) ([]byte, error) {
	cfg := map[string]any{"static": o.Static, "api": o.API, "events": o.Events, "index": o.IndexJS}
	if o.Data != nil {
		cfg["data"] = o.Data
	}
	js, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]any{
		"Title":  o.Title,
		"Static": o.Static,
		// </script> can't appear in JSON from encoding/json (it escapes <), so
		// this is safe to inline.
		"Config": template.JS(strings.ReplaceAll(string(js), "</", `<\/`)),
	})
	return buf.Bytes(), err
}
