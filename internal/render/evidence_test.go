package render

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

// addEv copies a dummy file named name into p's evidence.
func addEv(t *testing.T, p *plan.Spec, name string, phase plan.Phase, step int, caption string) plan.EvidenceItem {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	it, err := p.AddEvidence(src, plan.EvidenceItem{Phase: phase, Step: step, Caption: caption, Commit: sha,
		CapturedAt: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func cardOf(t *testing.T, in *plan.Initiative, id string) Card {
	t.Helper()
	for _, l := range BuildBoard(in, time.Now()).Lanes {
		for _, c := range l.Cards {
			if c.ID == id {
				return c
			}
		}
	}
	t.Fatalf("no card %s", id)
	return Card{}
}

func TestEvidenceNone(t *testing.T) {
	in := pilot(t)
	if c := cardOf(t, in, "02"); c.Evidence != nil {
		t.Fatalf("card evidence: %+v", c.Evidence)
	}
	d, _ := BuildPRDetail(in, "02", time.Now())
	if d.Evidence != nil {
		t.Fatalf("detail evidence: %+v", d.Evidence)
	}
	b, _ := json.Marshal(d)
	if strings.Contains(string(b), `"evidence"`) {
		t.Fatalf("evidence key present: %s", b)
	}
}

func TestEvidenceLevelNoItems(t *testing.T) {
	in := pilot(t)
	in.PR("02").Evidence = plan.EvidenceScreenshots
	c := cardOf(t, in, "02")
	if c.Evidence == nil || *c.Evidence != (CardEvidence{Level: "screenshots"}) {
		t.Fatalf("card: %+v", c.Evidence)
	}
	d, _ := BuildPRDetail(in, "02", time.Now())
	if d.Evidence == nil || d.Evidence.Level != "screenshots" || d.Evidence.Script != nil || d.Evidence.Pairs == nil || len(d.Evidence.Pairs) != 0 {
		t.Fatalf("detail: %+v", d.Evidence)
	}
	b, _ := json.Marshal(d)
	if !strings.Contains(string(b), `"evidence":{"level":"screenshots","script":null,"pairs":[]}`) {
		t.Fatalf("json: %s", b)
	}
}

func TestEvidencePairsEarlierAndVideo(t *testing.T) {
	in := pilot(t)
	p := in.PR("02")
	p.Evidence = plan.EvidenceVideo
	addEv(t, p, "home.png", plan.PhaseBefore, 1, "home page")
	addEv(t, p, "home.png", plan.PhaseAfter, 1, "home v1")
	addEv(t, p, "home.png", plan.PhaseAfter, 1, "home v2") // stored as home-2.png
	addEv(t, p, "flow.webm", plan.PhaseAfter, 2, "checkout flow")
	script := filepath.Join(t.TempDir(), "capture.mjs")
	os.WriteFile(script, []byte("// script"), 0o644)
	if _, err := p.SetEvidenceScript(script); err != nil {
		t.Fatal(err)
	}

	c := cardOf(t, in, "02")
	want := CardEvidence{Level: "video", Steps: 2, Before: 1, After: 2, Video: true,
		ThumbBefore: "pr/02/evidence/before/home.png", ThumbAfter: "pr/02/evidence/after/home-2.png"}
	if c.Evidence == nil || *c.Evidence != want {
		t.Fatalf("card: %+v", c.Evidence)
	}

	d, _ := BuildPRDetail(in, "02", time.Now())
	ev := d.Evidence
	if ev == nil || ev.Level != "video" || ev.Script == nil || *ev.Script != (EvidenceScript{Name: "capture.mjs", URL: "pr/02/evidence/script"}) {
		t.Fatalf("detail: %+v", ev)
	}
	if len(ev.Pairs) != 2 {
		t.Fatalf("pairs: %+v", ev.Pairs)
	}
	s1, s2 := ev.Pairs[0], ev.Pairs[1]
	if s1.Step != 1 || s1.Before == nil || s1.Before.URL != "pr/02/evidence/before/home.png" || s1.Before.Kind != "image" ||
		s1.Before.Commit != "0123456" || s1.Before.Caption != "home page" || s1.Before.File != "home.png" {
		t.Fatalf("step 1 before: %+v", s1.Before)
	}
	if s1.After == nil || s1.After.Caption != "home v2" || len(s1.Earlier) != 1 || s1.Earlier[0].Caption != "home v1" ||
		s1.Earlier[0].URL != "pr/02/evidence/after/home.png" {
		t.Fatalf("step 1 after/earlier: %+v %+v", s1.After, s1.Earlier)
	}
	if s2.Step != 2 || s2.Before != nil || s2.After == nil || s2.After.Kind != "video" || s2.Earlier == nil || len(s2.Earlier) != 0 {
		t.Fatalf("step 2: %+v", s2)
	}
	b, _ := json.Marshal(d)
	for _, k := range []string{`"before":null`, `"earlier":[]`, `"captured_at":"2026-11-04T12:00:00Z"`, `"commit":"0123456"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("json missing %s: %s", k, b)
		}
	}
	// the card summary is not repeated inside the detail payload
	if strings.Count(string(b), `"evidence":`) != 1 {
		t.Errorf("evidence appears more than once: %s", b)
	}

	// a video at step 1 gets no thumbnail
	p2 := in.PR("03")
	addEv(t, p2, "run.mp4", plan.PhaseBefore, 1, "")
	c = cardOf(t, in, "03")
	if c.Evidence == nil || c.Evidence.ThumbBefore != "" || !c.Evidence.Video || c.Evidence.Level != "" || c.Evidence.Before != 1 {
		t.Fatalf("video card: %+v", c.Evidence)
	}
}

func TestEvidenceURLEscaping(t *testing.T) {
	in := pilot(t)
	p := in.PR("02")
	// Stored names are sanitized by plan, but a hand-edited manifest may hold
	// anything: URLs must escape every segment.
	m := plan.EvidenceManifest{Items: []plan.EvidenceItem{
		{Phase: plan.PhaseBefore, Step: 1, File: "a b#?%/../x.png", Kind: plan.KindImage, Commit: "abc"},
	}}
	os.MkdirAll(p.EvidenceDir(), 0o755)
	data, _ := json.Marshal(m)
	os.WriteFile(p.EvidenceManifestPath(), data, 0o644)
	c := cardOf(t, in, "02")
	if c.Evidence == nil || c.Evidence.ThumbBefore != "pr/02/evidence/before/a%20b%23%3F%25%2F..%2Fx.png" {
		t.Fatalf("thumb: %+v", c.Evidence)
	}
	d, _ := BuildPRDetail(in, "02", time.Now())
	if d.Evidence.Pairs[0].Before.URL != c.Evidence.ThumbBefore || d.Evidence.Pairs[0].Before.Commit != "abc" {
		t.Fatalf("detail: %+v", d.Evidence.Pairs[0].Before)
	}

	// a corrupt manifest reads as empty rather than failing the board
	os.WriteFile(p.EvidenceManifestPath(), []byte("{nope"), 0o644)
	if c := cardOf(t, in, "02"); c.Evidence != nil {
		t.Fatalf("corrupt manifest: %+v", c.Evidence)
	}
}
