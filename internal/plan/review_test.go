package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const roundFront = `---
id: "04"
slug: x
title: X
parent: null
conflicts_with: ["01"]   # keep this comment
status: in-progress
status_since: 2026-11-03T16:01:32-05:00
blocked_reason: ""
early_start: false
model: sonnet
worker: v-04
branch: b
pr_number: 0
---
`

func TestAppendDirectorRound(t *testing.T) {
	d := time.Date(2026, 11, 3, 18, 0, 0, 0, time.Local)
	tests := []struct {
		name, body string
		verdict    string
		text       string
		wantRound  int
		want       string
	}{
		{
			name:      "empty director section, legacy Dalton heading follows (back-compat fixture)",
			body:      "## Goal\nx\n\n## Review: Director\n\n## Review: Dalton\n",
			verdict:   "approved",
			text:      "Looks good.\n",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nLooks good.\n\n## Review: Dalton\n",
		},
		{
			name:      "empty director section, Human follows",
			body:      "## Goal\nx\n\n## Review: Director\n\n## Review: Human\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n\n## Review: Human\n",
		},
		{
			name:      "section missing, Human exists",
			body:      "## Goal\nx\n\n## Review: Human\n\n### Round 1\nfeedback\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n\n## Review: Human\n\n### Round 1\nfeedback\n",
		},
		{
			name:      "second round, Human section has rounds",
			body:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n## Review: Human\n\n### Round 1\nv\n",
			verdict:   "approved",
			text:      "again",
			wantRound: 2,
			want:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n### Round 2 (2026-11-03): approved\n\nagain\n\n## Review: Human\n\n### Round 1\nv\n",
		},
		{
			name:      "empty director section, Ada follows",
			body:      "## Goal\nx\n\n## Review: Director\n\n## Review: Ada\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n\n## Review: Ada\n",
		},
		{
			name:      "section missing, Ada exists",
			body:      "## Goal\nx\n\n## Review: Ada\n\n### Round 1\nfeedback\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n\n## Review: Ada\n\n### Round 1\nfeedback\n",
		},
		{
			name:      "second round, Ada section has rounds",
			body:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n## Review: Ada\n\n### Round 1\nv\n",
			verdict:   "approved",
			text:      "again",
			wantRound: 2,
			want:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n### Round 2 (2026-11-03): approved\n\nagain\n\n## Review: Ada\n\n### Round 1\nv\n",
		},
		{
			name:      "review heading inside a fence is not the reviewer section",
			body:      "## Goal\n```\n## Review: Ada\n```\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\n```\n## Review: Ada\n```\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n",
		},
		{
			name:      "empty director section at end",
			body:      "## Goal\nx\n\n## Review: Director\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n",
		},
		{
			name:      "second round without a reviewer section",
			body:      "## Review: Director\n\n### Round 1 (2026-11-02): changes requested\n\nfix it\n",
			verdict:   "approved",
			text:      "fine now",
			wantRound: 2,
			want:      "## Review: Director\n\n### Round 1 (2026-11-02): changes requested\n\nfix it\n\n### Round 2 (2026-11-03): approved\n\nfine now\n",
		},
		{
			name:      "second round with a reviewer section containing rounds (legacy Dalton heading)",
			body:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n## Review: Dalton\n\n### Round 1 (2026-11-02)\n\nverbatim\n",
			verdict:   "changes requested",
			text:      "again",
			wantRound: 2,
			want:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n### Round 2 (2026-11-03): changes requested\n\nagain\n\n## Review: Dalton\n\n### Round 1 (2026-11-02)\n\nverbatim\n",
		},
		{
			name:      "section missing, legacy Dalton heading exists",
			body:      "## Goal\nx\n\n## Review: Dalton\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n\n## Review: Dalton\n",
		},
		{
			name:      "section missing, nothing else",
			body:      "## Goal\nx\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Goal\nx\n\n## Review: Director\n\n### Round 1 (2026-11-03): approved\n\nok\n",
		},
		{
			name:      "headings in review text are demoted, fences left alone",
			body:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n",
			verdict:   "approved",
			text:      "## Findings\n### Round 9\n```\n## code\n```\n",
			wantRound: 2,
			want:      "## Review: Director\n\n### Round 1 (2026-11-02): approved\n\nfine\n\n### Round 2 (2026-11-03): approved\n\n#### Findings\n#### Round 9\n```\n## code\n```\n",
		},
		{
			name:      "round-like heading inside fence is not counted",
			body:      "## Review: Director\n\n```\n### Round 1 fake\n```\n",
			verdict:   "approved",
			text:      "ok",
			wantRound: 1,
			want:      "## Review: Director\n\n```\n### Round 1 fake\n```\n\n### Round 1 (2026-11-03): approved\n\nok\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "spec.md")
			if err := os.WriteFile(path, []byte(roundFront+tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			s, err := LoadSpec(dir)
			if err != nil {
				t.Fatal(err)
			}
			n, err := s.AppendDirectorRound(d, tc.verdict, tc.text)
			if err != nil {
				t.Fatal(err)
			}
			if n != tc.wantRound {
				t.Errorf("round = %d, want %d", n, tc.wantRound)
			}
			got, _ := os.ReadFile(path)
			if string(got) != roundFront+tc.want {
				t.Errorf("file:\n%q\nwant:\n%q", got, roundFront+tc.want)
			}
			if !strings.HasPrefix(string(got), roundFront) {
				t.Error("frontmatter bytes changed")
			}
			if s.Body() != tc.want {
				t.Errorf("in-memory body not updated")
			}
		})
	}
}

func TestAppendEntry(t *testing.T) {
	dir := t.TempDir()
	in := &Initiative{Dir: dir}
	now := time.Date(2026, 11, 3, 9, 5, 0, 0, time.Local)
	if err := in.AppendEntry(now, "review", "director", "line one\nline two  \n\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(in.LogPath())
	if want := "\n## 2026-11-03 09:05 · review · director\nline one\nline two\n"; string(b) != want {
		t.Errorf("got %q want %q", b, want)
	}
}

func TestVerdictKind(t *testing.T) {
	for text, want := range map[string]string{
		"approved":                              VerdictApproved,
		"approved (before PR opened)":           VerdictApproved,
		"approved with nits":                    VerdictApprovedWithNits,
		"approved with nits (before PR opened)": VerdictApprovedWithNits,
		"approved-with-nits":                    VerdictApprovedWithNits,
		"changes requested":                     VerdictChangesRequested,
		"changes requested (before PR opened)":  VerdictChangesRequested,
		"changes-requested":                     VerdictChangesRequested,
		"  approved  ":                          VerdictApproved,
		"":                                      VerdictNone,
		"approved, mostly":                      VerdictNone,
		"approvedish":                           VerdictNone,
		"LGTM":                                  VerdictNone,
		"(before PR opened)":                    VerdictNone,
	} {
		if got := VerdictKind(text); got != want {
			t.Errorf("VerdictKind(%q) = %q, want %q", text, got, want)
		}
	}
	for _, k := range []string{VerdictApproved, VerdictApprovedWithNits, VerdictChangesRequested} {
		if VerdictKind(VerdictHeading(k)) != k {
			t.Errorf("heading %q doesn't round-trip to %q", VerdictHeading(k), k)
		}
	}
	if VerdictHeading(VerdictNone) != "" || VerdictHeading("meh") != "" {
		t.Error("unknown kinds must have no heading")
	}
}

func TestLatestDirectorVerdictKind(t *testing.T) {
	d := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		heading string
		want    string
	}{
		{"approved", VerdictApproved},
		{"approved" + BeforeOpenedSuffix, VerdictApproved},
		{"approved with nits", VerdictApprovedWithNits},
		{"approved with nits" + BeforeOpenedSuffix, VerdictApprovedWithNits},
		{"changes requested", VerdictChangesRequested},
		{"changes requested" + BeforeOpenedSuffix, VerdictChangesRequested},
		{"approved-ish (hand edited)", VerdictNone},
	} {
		s := newEvidenceSpec(t)
		if r, k := s.LatestDirectorVerdictKind(); r != 0 || k != VerdictNone {
			t.Fatalf("no rounds: (%d, %q)", r, k)
		}
		if _, err := s.AppendDirectorRound(d, "changes requested", "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendDirectorRound(d, tc.heading, "y"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(s.Body(), "### Round 2 (2026-11-04): "+tc.heading+"\n") {
			t.Fatalf("%q: body:\n%s", tc.heading, s.Body())
		}
		if r, k := s.LatestDirectorVerdictKind(); r != 2 || k != tc.want {
			t.Errorf("%q: got (%d, %q), want (2, %q)", tc.heading, r, k, tc.want)
		}
	}
}
