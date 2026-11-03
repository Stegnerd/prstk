package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"prstk/internal/plan"
)

func pilot(t *testing.T) *plan.Initiative {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dalton-sample_proj", "2026-11-03-1415-pokemon-api")
	in := &plan.Initiative{Dir: dir, Meta: plan.PlanMeta{Initiative: "pokemon-api", Repo: "dalton/sample_proj", BaseBranch: "develop", MaxWorkers: 2}}
	os.MkdirAll(filepath.Join(dir, "prs"), 0o755)
	if err := in.SavePlan(func(string) string { return "# P\n" }); err != nil {
		t.Fatal(err)
	}
	add := func(id, slug, parent string, st plan.Status, pr int) {
		d := filepath.Join(dir, "prs", id+"-"+slug)
		os.MkdirAll(d, 0o755)
		s := &plan.Spec{ID: id, Slug: slug, Title: slug, Parent: parent, Status: st, Model: "sonnet", PRNumber: pr, Dir: d,
			StatusSince: time.Now().Add(-40 * time.Minute)}
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(s.NotesPath(), nil, 0o644)
	}
	add("01", "scaffold", "", plan.Merged, 101)
	add("02", "db-layer", "01", plan.AwaitingYou, 102)
	add("03", "read-endpoints", "02", plan.DirectorReview, 103)
	add("04", "types-endpoints", "02", plan.InProgress, 104)
	add("05", "write-endpoints", "03", plan.Planned, 105)
	add("06", "middleware", "01", plan.Blocked, 106)
	loaded, err := plan.LoadInitiative(dir)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestStackNavMatchesSpec(t *testing.T) {
	in := pilot(t)
	got := StackNav(in, "03")
	want := `**Stack** (pokemon-api)
- #101 01 scaffold ✅ merged
- #102 02 db layer
  - **#103 03 read endpoints ← this PR**
    - #105 05 write endpoints
  - #104 04 types endpoints
- #106 06 middleware
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestReplaceBlockPreservesOutside(t *testing.T) {
	body := "Intro by Dalton.\n\n" + StackBegin + "\nold\n" + StackEnd + "\n\nTrailing notes."
	got := ReplaceBlock(body, StackBegin, StackEnd, "new")
	if got != "Intro by Dalton.\n\n"+StackBegin+"\nnew\n"+StackEnd+"\n\nTrailing notes." {
		t.Fatalf("got %q", got)
	}
	if again := ReplaceBlock(got, StackBegin, StackEnd, "new"); again != got {
		t.Fatal("not idempotent")
	}
	appended := ReplaceBlock("no markers", StackBegin, StackEnd, "x")
	if !strings.HasPrefix(appended, "no markers\n\n"+StackBegin) {
		t.Fatalf("append: %q", appended)
	}
}

func TestBoardLanesAndColumns(t *testing.T) {
	in := pilot(t)
	b := BuildBoard(in, time.Now())
	var labels []string
	cols := map[string]string{}
	for _, l := range b.Lanes {
		labels = append(labels, l.Label)
		for _, c := range l.Cards {
			cols[c.ID] = c.Column + "/" + c.Tag
		}
	}
	if strings.Join(labels, ",") != "main,off 02,off 01" {
		t.Fatalf("lanes: %v", labels)
	}
	want := map[string]string{"01": "Merged/", "02": "Awaiting You/", "03": "Director Review/", "04": "In Progress/", "05": "Planned/", "06": "In Progress/blocked"}
	for id, w := range want {
		if cols[id] != w {
			t.Errorf("%s: %s, want %s", id, cols[id], w)
		}
	}
	if len(b.Slots) != 3 { // 03, 04, 06 hold slots → also an over-limit validation error
		t.Fatalf("slots: %+v", b.Slots)
	}
	page, err := BoardPage(PageOpts{Title: "t", Static: true, Data: &b})
	if err != nil {
		t.Fatal(err)
	}
	s := string(page)
	if !strings.Contains(s, `http-equiv="refresh" content="10"`) || !strings.Contains(s, `"initiative":"pokemon-api"`) {
		t.Fatal("static page missing refresh or data")
	}
}

func TestPlanBlock(t *testing.T) {
	in := pilot(t)
	blk := PlanBlock(in, time.Now())
	for _, w := range []string{"```mermaid", "pr_01 --> pr_02", "classDef merged", "[#102](https://github.com/dalton/sample_proj/pull/102)", "· · 03"} {
		if !strings.Contains(blk, w) {
			t.Errorf("missing %q in:\n%s", w, blk)
		}
	}
}
