package usage

import (
	"testing"
	"time"
)

const sampleLog = `# Log · demo

## 2026-11-03 10:00 · init · prstk
initiative demo on o/r, base main

## 2026-11-03 10:05 · assign · prstk
01 → w-01 (in-progress, sonnet)

## 2026-11-03 10:06 · feedback · director
something about 02 → w-9 that isn't an assign

## 2026-11-03 11:00 · assign · prstk
03 → w-01 (in-progress, opus)

## 2026-11-03 11:30 · assign · prstk
01 → w-01b (fixing, sonnet)
`

func TestParseAssigns(t *testing.T) {
	got := ParseAssigns(sampleLog, time.UTC)
	want := []Assign{
		{At: time.Date(2026, 11, 3, 10, 5, 0, 0, time.UTC), PR: "01", Worker: "w-01"},
		{At: time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC), PR: "03", Worker: "w-01"},
		{At: time.Date(2026, 11, 3, 11, 30, 0, 0, time.UTC), PR: "01", Worker: "w-01b"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if !got[i].At.Equal(want[i].At) || got[i].PR != want[i].PR || got[i].Worker != want[i].Worker {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPRForReassignedWorker(t *testing.T) {
	as := ParseAssigns(sampleLog, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, time.UTC) }
	for _, tc := range []struct {
		worker string
		t      time.Time
		want   string
	}{
		{"w-01", at(10, 5), "01"},
		{"w-01", at(10, 59), "01"},
		{"w-01", at(11, 0), "03"},
		{"w-01", at(12, 0), "03"},
		{"w-01", at(9, 0), "01"}, // before any assign: the first one
		{"w-01b", at(11, 45), "01"},
		{"w-9", at(11, 0), ""},
	} {
		if got := prFor(as, tc.worker, tc.t); got != tc.want {
			t.Errorf("%s at %s: %q, want %q", tc.worker, tc.t.Format("15:04"), got, tc.want)
		}
	}
}
