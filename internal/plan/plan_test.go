package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleSpec = `---
id: "03"
slug: list-endpoint
title: Add list endpoint
parent: "02"               # null = stacks directly on base_branch
conflicts_with: []         # same-wave PRs touching the same files; must be symmetric
custom_field: keep me
status: planned
status_since: 2026-11-03T14:22:00-05:00
blocked_reason: ""
early_start: false
model: sonnet
worker: ""
branch: prstk/pokemon-api/03-list-endpoint
pr_number: 0
---
## Goal
List things.
`

func TestSpecRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte(sampleSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "03" || s.Parent != "02" || s.Status != Planned || s.Model != "sonnet" {
		t.Fatalf("bad decode: %+v", s)
	}
	if s.StatusSince.IsZero() {
		t.Fatal("status_since not parsed")
	}
	s.SetStatus(InProgress, time.Date(2026, 11, 3, 15, 0, 0, 0, time.FixedZone("EST", -5*3600)))
	s.Worker = "worker-1"
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.SpecPath())
	out := string(b)
	for _, want := range []string{
		`id: "03"`,
		`parent: "02" # null = stacks directly on base_branch`,
		"custom_field: keep me",
		"status: in-progress",
		"status_since: 2026-11-03T15:00:00-05:00",
		"worker: worker-1",
		"## Goal\nList things.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// order preserved: custom_field still between conflicts_with and status
	if strings.Index(out, "custom_field") > strings.Index(out, "status:") {
		t.Errorf("field order changed:\n%s", out)
	}
	// blocked_from is new → appended at end
	if strings.Index(out, "blocked_from") < strings.Index(out, "pr_number") {
		t.Errorf("new field should be appended:\n%s", out)
	}
	s2, err := LoadSpec(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Status != InProgress || s2.Worker != "worker-1" || s2.ID != "03" {
		t.Fatalf("reload: %+v", s2)
	}
}

func TestNullParent(t *testing.T) {
	dir := t.TempDir()
	s := &Spec{ID: "01", Slug: "scaffold", Title: "Scaffold", Status: Planned, Model: "sonnet", Dir: dir}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(s.SpecPath())
	if !strings.Contains(string(b), "parent: null") {
		t.Fatalf("want null parent:\n%s", b)
	}
	s2, err := LoadSpec(dir)
	if err != nil || s2.Parent != "" {
		t.Fatalf("reload: %v %+v", err, s2)
	}
}

func TestParseRemote(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:dalton/sample_proj.git":       "dalton-sample_proj",
		"https://github.com/dalton/sample_proj":       "dalton-sample_proj",
		"https://github.com/dalton/sample_proj.git":   "dalton-sample_proj",
		"ssh://git@github.com/dalton/sample_proj.git": "dalton-sample_proj",
		"https://github.com/Stegnerd/sample_proj.git": "stegnerd-sample_proj",
	} {
		o, r, err := ParseRemote(in)
		if err != nil || RepoKey(o, r) != want {
			t.Errorf("%s → %s-%s %v, want %s", in, o, r, err, want)
		}
	}
}

func TestLockExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".lock")
	l1, err := AcquireLock(p, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(p, 100*time.Millisecond); err == nil {
		t.Fatal("second lock should time out")
	}
	l1.Release()
	l2, err := AcquireLock(p, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	l2.Release()
}

func TestAtomicWriteNoTempLeft(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.md")
	if err := WriteFileAtomic(p, []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("b")); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("temp files left: %v", ents)
	}
}

func TestCheckTransition(t *testing.T) {
	if _, err := CheckTransition(InProgress, Blocked, ""); err != nil {
		t.Error(err)
	}
	if got, err := CheckTransition(Blocked, InProgress, Fixing); err != nil || got != Fixing {
		t.Errorf("unblock should restore fixing: %v %v", got, err)
	}
	if _, err := CheckTransition(Planned, InProgress, ""); err == nil || !strings.Contains(err.Error(), "assign") {
		t.Errorf("want hint to use assign: %v", err)
	}
	if _, err := CheckTransition(Merged, Planned, ""); err == nil {
		t.Error("merged is terminal")
	}
}

func TestSection(t *testing.T) {
	md := "## Description\nHello\nworld\n\n## Worker notes\nx\n"
	if got := Section(md, "Description"); got != "Hello\nworld" {
		t.Fatalf("got %q", got)
	}
}
