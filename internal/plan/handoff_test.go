package plan

import (
	"strings"
	"testing"
	"time"
)

func hoInit(prs ...*Spec) *Initiative {
	return &Initiative{Name: "demo", PRs: prs}
}

func pr(id string, st Status, worker string) *Spec {
	return &Spec{ID: id, Slug: "s" + id, Status: st, Worker: worker}
}

func entry(typ, body string) LogEntry { return LogEntry{Type: typ, By: "prstk", Body: body} }

func TestParseLog(t *testing.T) {
	text := "# Log · demo\n\n## 2026-11-03 14:15 · init · prstk\ninitiative demo\n\n" +
		"## 2026-11-03 14:20 · feedback · director\nline one\n## not a heading\nline three\n\n" +
		"## 2026-11-03 14:30 · handoff · prstk\npoint=planning · 2 open PRs\n"
	got := ParseLog(text, time.UTC)
	if len(got) != 3 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	if got[0].Type != "init" || got[0].By != "prstk" || got[0].Body != "initiative demo" {
		t.Fatalf("entry 0: %+v", got[0])
	}
	if got[1].Type != "feedback" || got[1].By != "director" || got[1].Body != "line one\n## not a heading\nline three" {
		t.Fatalf("entry 1: %+v", got[1])
	}
	if !got[2].At.Equal(time.Date(2026, 11, 3, 14, 30, 0, 0, time.UTC)) || got[2].Type != "handoff" {
		t.Fatalf("entry 2: %+v", got[2])
	}
	if len(ParseLog("", time.UTC)) != 0 {
		t.Fatal("empty log should have no entries")
	}
}

func TestLogSinceHandoff(t *testing.T) {
	log := []LogEntry{entry("init", ""), entry("merged", ""), entry("handoff", ""), entry("assign", ""), entry("handoff", ""), entry("assign", ""), entry("merged", "")}
	last, n, ok := LogSinceHandoff(log)
	if !ok || n != 2 || last.Type != "handoff" {
		t.Fatalf("got %+v %d %v", last, n, ok)
	}
	if _, _, ok := LogSinceHandoff(log[:2]); ok {
		t.Fatal("no handoff entry should report not found")
	}
}

func TestHandoffPoint(t *testing.T) {
	okIssues := []Issue{{Error: false, Msg: "just a warning"}}
	errIssues := []Issue{{Error: true, PR: "01", Msg: "broken"}}
	ready := entry("ready", "01 #1 ready for review")
	handoff := entry("handoff", "point=waiting · 2 open PRs")
	child := func(id, parent string, st Status) *Spec {
		p := pr(id, st, "")
		p.Parent = parent
		return p
	}

	tests := []struct {
		name   string
		in     *Initiative
		log    []LogEntry
		issues []Issue
		want   HandoffPointKind
		errHas []string // all must appear; empty means no error
	}{
		{name: "planning", in: hoInit(pr("01", Planned, ""), pr("02", Planned, "")), log: []LogEntry{entry("init", "")}, issues: okIssues, want: PointPlanning},
		{name: "planning twice is fine", in: hoInit(pr("01", Planned, "")), log: []LogEntry{handoff}, want: PointPlanning},
		{name: "planning with validation errors", in: hoInit(pr("01", Planned, "")), issues: errIssues, errHas: []string{"1 validation error", "prstk validate"}},
		{name: "planning wins over waiting", in: hoInit(pr("01", Planned, "")), log: []LogEntry{ready}, want: PointPlanning},
		{name: "abandoned is not started", in: hoInit(pr("01", Planned, ""), pr("02", Abandoned, "")), want: PointPlanning},

		{name: "no PRs", in: hoInit(), errHas: []string{"no PRs"}},
		{name: "in-progress worker", in: hoInit(pr("01", InProgress, "w1"), pr("02", Planned, "")), errHas: []string{"01 (w1, in-progress)", HandoffAllowed}},
		{name: "blocked", in: hoInit(pr("01", Blocked, "w1")), errHas: []string{"01 (w1, blocked)"}},
		{name: "director-review", in: hoInit(pr("01", DirectorReview, "w1")), errHas: []string{"01 (w1, director-review)"}},
		{name: "changes-requested", in: hoInit(pr("01", ChangesRequested, "w1")), errHas: []string{"01 (w1, changes-requested)"}},
		{name: "fixing", in: hoInit(pr("01", Fixing, "w1")), errHas: []string{"01 (w1, fixing)"}},
		{name: "slot-holding status without a worker", in: hoInit(pr("01", InProgress, "")), errHas: []string{"01 (no worker recorded, in-progress)"}},
		{name: "planned with a worker recorded", in: hoInit(pr("01", Planned, "w1")), errHas: []string{"01 (w1, planned)"}},
		{name: "awaiting-you with a worker recorded", in: hoInit(pr("01", Merged, ""), pr("02", AwaitingYou, "w2")), log: []LogEntry{ready}, errHas: []string{"02 (w2, awaiting-you)"}},
		{name: "every live PR is named", in: hoInit(pr("01", InProgress, "w1"), pr("02", Blocked, "w2")), errHas: []string{"01 (w1, in-progress)", "02 (w2, blocked)"}},
		{name: "unknown status", in: hoInit(pr("01", Status("bogus"), "")), errHas: []string{`01 has an unknown status "bogus"`, "prstk status"}},
		{name: "a worker left on a merged PR is ignored", in: hoInit(pr("01", Merged, "old"), pr("02", AwaitingYou, "")), log: []LogEntry{ready}, want: PointWaiting},

		{name: "finished", in: hoInit(pr("01", Merged, ""), pr("02", Abandoned, "")), log: []LogEntry{ready}, errHas: []string{"nothing left to hand off", "prstk teardown"}},
		{name: "finished beats a worker on a done PR", in: hoInit(pr("01", Merged, "w1")), log: []LogEntry{ready}, errHas: []string{"nothing left to hand off"}},

		{name: "waiting without an earlier handoff", in: hoInit(pr("01", AwaitingYou, "")), log: []LogEntry{entry("init", ""), ready}, want: PointWaiting},
		{name: "waiting with merged and abandoned PRs", in: hoInit(pr("01", Merged, ""), pr("02", AwaitingYou, ""), pr("03", Abandoned, "")), log: []LogEntry{ready}, want: PointWaiting},
		{name: "waiting with a planned PR that isn't schedulable", in: hoInit(pr("01", AwaitingYou, ""), child("02", "99", Planned)), log: []LogEntry{ready}, want: PointWaiting},
		{name: "ready after the last handoff", in: hoInit(pr("01", AwaitingYou, ""), pr("02", AwaitingYou, "")), log: []LogEntry{ready, handoff, entry("assign", ""), ready}, want: PointWaiting},
		{name: "waiting ignores validation errors", in: hoInit(pr("01", AwaitingYou, "")), log: []LogEntry{ready}, issues: errIssues, want: PointWaiting},
		{name: "waiting ignores a merged entry", in: hoInit(pr("01", Merged, ""), pr("02", AwaitingYou, "")), log: []LogEntry{handoff, entry("merged", "merged 01")}, errHas: []string{"no PR was marked ready since the last handoff"}},

		{name: "fix-pending is schedulable", in: hoInit(pr("01", AwaitingYou, ""), pr("02", FixPending, "")), log: []LogEntry{ready}, errHas: []string{"prstk next lists work (02)", HandoffAllowed}},
		{name: "a schedulable planned PR is refused", in: hoInit(pr("01", AwaitingYou, ""), pr("02", Planned, "")), log: []LogEntry{ready}, errHas: []string{"prstk next lists work (02)"}},
		{name: "a planned child of an awaiting-you PR is schedulable", in: hoInit(pr("01", AwaitingYou, ""), child("02", "01", Planned)), log: []LogEntry{ready}, errHas: []string{"prstk next lists work (02)"}},
		{name: "every listed PR is named", in: hoInit(pr("01", AwaitingYou, ""), pr("02", Planned, ""), pr("03", FixPending, "")), log: []LogEntry{ready}, errHas: []string{"(03, 02)"}},
		{name: "no PR awaiting you", in: hoInit(pr("01", Merged, ""), child("02", "99", Planned)), log: []LogEntry{ready}, errHas: []string{"no PR is awaiting-you", HandoffAllowed}},
		{name: "awaiting-you with no ready since the last handoff", in: hoInit(pr("01", Merged, ""), pr("02", AwaitingYou, "")), log: []LogEntry{ready, handoff, entry("merged", "")}, errHas: []string{"no PR was marked ready since the last handoff", HandoffAllowed}},
		{name: "awaiting-you with no ready at all", in: hoInit(pr("02", AwaitingYou, "")), log: []LogEntry{entry("init", "")}, errHas: []string{"no PR was marked ready"}},
		{name: "second handoff with nothing new", in: hoInit(pr("01", AwaitingYou, "")), log: []LogEntry{ready, handoff}, errHas: []string{"no PR was marked ready since"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HandoffPoint(tc.in, tc.log, tc.issues)
			if len(tc.errHas) > 0 {
				if err == nil {
					t.Fatalf("want an error, got point %q", got)
				}
				for _, s := range tc.errHas {
					if !strings.Contains(err.Error(), s) {
						t.Fatalf("error %q lacks %q", err, s)
					}
				}
				if got != "" {
					t.Fatalf("point %q returned with an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestHandoffPointRefusalsSayWhenAllowed(t *testing.T) {
	// Every refusal except "finished", "no PRs", "unknown status" and
	// "validation errors" ends with the allowed-points sentence.
	for _, in := range []*Initiative{
		hoInit(pr("01", InProgress, "w")),
		hoInit(pr("01", AwaitingYou, "")),
		hoInit(pr("01", AwaitingYou, ""), pr("02", FixPending, "")),
	} {
		_, err := HandoffPoint(in, nil, nil)
		if err == nil || !strings.HasSuffix(err.Error(), HandoffAllowed) {
			t.Fatalf("error: %v", err)
		}
	}
}
