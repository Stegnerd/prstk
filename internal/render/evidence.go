package render

import (
	"net/url"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

// Evidence URLs are relative to the initiative's page base (PageOpts.Base),
// so the same JSON works under /i/ and /a/:
//
//	pr/<id>/evidence/<phase>/<file>
//	pr/<id>/evidence/script
//
// Every segment is url.PathEscape'd. The server resolves them only through
// the loaded manifest (see internal/server/evidence.go).

// CardEvidence is the board card's evidence summary.
type CardEvidence struct {
	Level       string `json:"level"`                  // "", "screenshots", "video"
	Steps       int    `json:"steps"`                  // len(Pairs())
	Before      int    `json:"before"`                 // steps with a before item
	After       int    `json:"after"`                  // steps with an after item
	Video       bool   `json:"video"`                  // any video in either phase
	ThumbBefore string `json:"thumb_before,omitempty"` // relative URL of step 1's before image, if it's an image
	ThumbAfter  string `json:"thumb_after,omitempty"`  // same, latest after
}

// Media is one capture as the detail page shows it.
type Media struct {
	URL        string    `json:"url"` // relative to the page base
	Kind       string    `json:"kind"`
	Caption    string    `json:"caption"`
	Commit     string    `json:"commit"` // short (7)
	CapturedAt time.Time `json:"captured_at"`
	File       string    `json:"file"`
}

// EvidenceStep is one step's before/after pair on the detail page.
type EvidenceStep struct {
	Step    int     `json:"step"`
	Before  *Media  `json:"before"`
	After   *Media  `json:"after"`
	Earlier []Media `json:"earlier"` // superseded afters, oldest first; never nil
}

// EvidenceScript is the capture script link.
type EvidenceScript struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// DetailEvidence is the PR detail page's Evidence section.
type DetailEvidence struct {
	Level  string          `json:"level"`
	Script *EvidenceScript `json:"script"`
	Pairs  []EvidenceStep  `json:"pairs"` // never nil
}

// loadEvidence reads p's manifest; a missing or unreadable one is empty, and
// ok is false when there is nothing to show (no level and no items).
func loadEvidence(p *plan.Spec) (m plan.EvidenceManifest, ok bool) {
	m, err := plan.LoadEvidence(p)
	if err != nil {
		m = plan.EvidenceManifest{Items: []plan.EvidenceItem{}}
	}
	return m, p.Evidence != "" || len(m.Items) > 0 || m.Script != ""
}

func evidenceBase(id string) string { return "pr/" + url.PathEscape(id) + "/evidence/" }

// evidenceURL is the page-relative URL of it.
func evidenceURL(id string, it plan.EvidenceItem) string {
	return evidenceBase(id) + url.PathEscape(string(it.Phase)) + "/" + url.PathEscape(it.File)
}

func shortCommit(c string) string {
	if len(c) > 7 {
		return c[:7]
	}
	return c
}

// buildCardEvidence summarises p's evidence; nil when there is none.
func buildCardEvidence(p *plan.Spec) *CardEvidence {
	m, ok := loadEvidence(p)
	if !ok {
		return nil
	}
	pairs := m.Pairs()
	e := &CardEvidence{Level: p.Evidence, Steps: len(pairs),
		Video: m.HasKind(plan.PhaseBefore, plan.KindVideo) || m.HasKind(plan.PhaseAfter, plan.KindVideo)}
	for _, pr := range pairs {
		if pr.Before != nil {
			e.Before++
		}
		if pr.After != nil {
			e.After++
		}
	}
	if len(pairs) > 0 {
		first := pairs[0]
		if first.Before != nil && first.Before.Kind == plan.KindImage {
			e.ThumbBefore = evidenceURL(p.ID, *first.Before)
		}
		if first.After != nil && first.After.Kind == plan.KindImage {
			e.ThumbAfter = evidenceURL(p.ID, *first.After)
		}
	}
	return e
}

func media(id string, it plan.EvidenceItem) Media {
	return Media{URL: evidenceURL(id, it), Kind: string(it.Kind), Caption: it.Caption, Commit: shortCommit(it.Commit),
		CapturedAt: it.CapturedAt, File: it.File}
}

// buildDetailEvidence is p's full evidence; nil when there is none.
func buildDetailEvidence(p *plan.Spec) *DetailEvidence {
	m, ok := loadEvidence(p)
	if !ok {
		return nil
	}
	d := &DetailEvidence{Level: p.Evidence, Pairs: []EvidenceStep{}}
	if m.Script != "" {
		d.Script = &EvidenceScript{Name: m.Script, URL: evidenceBase(p.ID) + "script"}
	}
	for _, pr := range m.Pairs() {
		s := EvidenceStep{Step: pr.Step, Earlier: []Media{}}
		if pr.Before != nil {
			b := media(p.ID, *pr.Before)
			s.Before = &b
		}
		if pr.After != nil {
			a := media(p.ID, *pr.After)
			s.After = &a
		}
		for _, it := range pr.Earlier {
			s.Earlier = append(s.Earlier, media(p.ID, it))
		}
		d.Pairs = append(d.Pairs, s)
	}
	return d
}
