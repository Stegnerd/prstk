package render

import (
	"strings"
	"testing"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
)

var hoNow = time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)

func TestHandoffSummary(t *testing.T) {
	in := pilot(t)
	// Director rounds are read from the spec body on disk, so write them first.
	if _, err := in.PR("02").AppendDirectorRound(hoNow, "approved", "ship it"); err != nil {
		t.Fatal(err)
	}
	if _, err := in.PR("02").AppendDirectorRound(hoNow, "approved with nits", "tidy"); err != nil {
		t.Fatal(err)
	}
	in, err := plan.LoadInitiative(in.Dir)
	if err != nil {
		t.Fatal(err)
	}
	// 02 and 03 await the human (03 stacked on 02); 04 and 06 need a fix.
	in.PR("02").Status = plan.AwaitingYou
	in.PR("03").Status = plan.AwaitingYou
	in.PR("03").Worker = "w3"
	in.PR("04").Status = plan.FixPending
	in.PR("04").BlockedReason = "merge conflict with 02: main.go, db.go"
	in.PR("05").Status = plan.Planned
	in.PR("06").Status = plan.FixPending
	in.PR("06").BlockedReason = "needs a rename"

	got := Handoff(in, HandoffOpts{Point: plan.PointMerges, Now: hoNow})
	for _, want := range []string{
		"# Handoff: 2026-11-03-1415-pokemon-api",
		"base `develop` · max_workers 2",
		"- Point: merges · written 2026-11-03 15:00",
		"| 02 | db-layer | 01 | awaiting-you (",
		"| #102 | r2 approved with nits |",
		"| 03 | read-endpoints | 02 | awaiting-you (",
		"| w3 |",
		"merged: 01 #101\n",
		"- 02 #102 db-layer\n",
		"- 03 #103 read-endpoints · ⏳ merge after #102\n",
		"- fix-pending 04 types-endpoints: merge conflict with 02: main.go, db.go\n",
		"- fix-pending 06 middleware: feedback: see ## Review: Human (needs a rename)\n",
		"`prstk next` prints:",
		"## Read first",
		in.PlanPath(), in.LogPath(), in.PR("02").SpecPath(), in.PR("06").SpecPath(),
		HandoffRecordLine,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, in.PR("01").SpecPath()) {
		t.Fatalf("merged PR's spec listed under Read first:\n%s", got)
	}
	if strings.Contains(got, "\n| 01 |") {
		t.Fatalf("merged PR has a table row:\n%s", got)
	}
	if strings.Contains(got, "## Director notes") {
		t.Fatalf("no notes were given:\n%s", got)
	}
	if n := strings.Count(got, "\n"); n > 60 {
		t.Fatalf("summary for 6 PRs is %d lines, want about 60 at most:\n%s", n, got)
	}
}

func TestHandoffLiveHeaderAndSavedLine(t *testing.T) {
	in := pilot(t)
	got := Handoff(in, HandoffOpts{Now: hoNow, Saved: "no handoff written"})
	if !strings.Contains(got, "- Live state as of 2026-11-03 15:00\n") || !strings.Contains(got, "- Handoff: no handoff written\n") {
		t.Fatalf("live header:\n%s", got)
	}
	if strings.Contains(got, "- Point:") {
		t.Fatalf("live summary names a point:\n%s", got)
	}
}

func TestHandoffNextAndWaitingWhenEmpty(t *testing.T) {
	in := pilot(t)
	for _, p := range in.PRs {
		p.Status = plan.Merged
	}
	got := Handoff(in, HandoffOpts{Now: hoNow})
	if !strings.Contains(got, "## Waiting on the human\n\nnothing\n") {
		t.Fatalf("waiting section:\n%s", got)
	}
	if !strings.Contains(got, "- none · nothing ready (0/2 used)\n") {
		t.Fatalf("next section:\n%s", got)
	}
	if strings.Contains(got, "| PR |") {
		t.Fatalf("a finished initiative has no table:\n%s", got)
	}
}

func TestHandoffNotesRoundTrip(t *testing.T) {
	in := pilot(t)
	notes := "Pending: ask about the schema.\n\n## Promised\n  - indented item\n\n```\n## Read first\n```\n"
	got := Handoff(in, HandoffOpts{Point: plan.PointPlanning, Now: hoNow, Notes: "\n" + notes + "\n\n"})
	if !strings.Contains(got, "\n## Director notes\n\nPending: ask about the schema.\n\n## Promised\n  - indented item\n") {
		t.Fatalf("notes not verbatim:\n%s", got)
	}
	if back := HandoffNotes(got); back != strings.TrimRight(notes, "\n") {
		t.Fatalf("round trip:\n%q\nwant\n%q", back, strings.TrimRight(notes, "\n"))
	}
	if HandoffNotes(Handoff(in, HandoffOpts{Now: hoNow})) != "" {
		t.Fatal("notes found in a summary without any")
	}
	if HandoffNotes("") != "" || HandoffNotes("## Director notes\n") != "" {
		t.Fatal("malformed input should give no notes")
	}
}

func TestHandoffBlockedReasonAndStatusCell(t *testing.T) {
	in := pilot(t)
	got := Handoff(in, HandoffOpts{Now: hoNow})
	if !strings.Contains(got, "| 06 | middleware | 01 | blocked") {
		t.Fatalf("blocked row:\n%s", got)
	}
}
