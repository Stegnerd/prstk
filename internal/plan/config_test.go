package plan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReviewerName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "Human"},
		{"   \t", "Human"},
		{"Ada", "Ada"},
		{"  Ada Lovelace ", "Ada Lovelace"},
	} {
		if got := (Config{Reviewer: tc.in}).ReviewerName(); got != tc.want {
			t.Errorf("ReviewerName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadConfigReviewer(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadConfig(dir)
	if err != nil || c.ReviewerName() != "Human" {
		t.Fatalf("no file: %+v %v", c, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("default_base: develop\nreviewer: Ada\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(dir)
	if err != nil || c.Reviewer != "Ada" || c.ReviewerName() != "Ada" || c.DefaultBase != "develop" {
		t.Fatalf("with reviewer: %+v %v", c, err)
	}
}

func TestEvidenceConfig(t *testing.T) {
	dir := t.TempDir()
	c, _ := LoadConfig(dir)
	if c.EvidenceBranchName() != "prstk-evidence" || !c.PublishEvidence() {
		t.Fatalf("defaults: %q %v", c.EvidenceBranchName(), c.PublishEvidence())
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("evidence_branch: audit\nevidence_publish: false\n"), 0o644)
	c, err := LoadConfig(dir)
	if err != nil || c.EvidenceBranchName() != "audit" || c.PublishEvidence() {
		t.Fatalf("set: %+v %v", c, err)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("evidence_publish: true\n"), 0o644)
	if c, _ := LoadConfig(dir); !c.PublishEvidence() {
		t.Fatal("explicit true")
	}
}
