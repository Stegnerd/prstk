package plan

import (
	"path/filepath"
	"testing"
)

func TestArchivePaths(t *testing.T) {
	repo := filepath.Join("/plans", "dalton-sample_proj")
	if got, want := ArchiveRoot(repo), "/plans/dalton-sample_proj/_archive"; got != want {
		t.Fatalf("ArchiveRoot = %q, want %q", got, want)
	}
	if got, want := ArchivePath(repo, "2026-11-03-1415-x"), "/plans/dalton-sample_proj/_archive/2026-11-03-1415-x"; got != want {
		t.Fatalf("ArchivePath = %q, want %q", got, want)
	}
	if filepath.Dir(ArchivePath(repo, "n")) != ArchiveRoot(repo) {
		t.Fatal("ArchivePath not under ArchiveRoot")
	}
}
