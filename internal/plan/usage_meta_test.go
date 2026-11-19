package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oldPlan = `---
initiative: demo
repo: o/r
base_branch: main
created: 2026-11-03T14:15:00-05:00
max_workers: 2
repo_path: /repo
---
# Demo
`

func TestPlanSessions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "2026-11-03-1415-demo")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "PLAN.md"), []byte(oldPlan), 0o644)

	// Old PLAN.md without sessions loads, and saving it doesn't add the key.
	in, err := LoadInitiative(dir)
	if err != nil || len(in.Meta.Sessions) != 0 {
		t.Fatalf("load: %+v %v", in, err)
	}
	if err := in.SavePlan(nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(in.PlanPath()); string(b) != oldPlan {
		t.Errorf("save without sessions changed PLAN.md:\n%s", b)
	}

	in.Meta.Sessions = []string{"abc-1", "def-2"}
	if err := in.SavePlan(nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(in.PlanPath())
	if !strings.Contains(string(b), "repo_path: /repo\nsessions: [\"abc-1\", \"def-2\"]\n---\n# Demo\n") {
		t.Errorf("PLAN.md:\n%s", b)
	}
	in2, err := LoadInitiative(dir)
	if err != nil || strings.Join(in2.Meta.Sessions, ",") != "abc-1,def-2" {
		t.Fatalf("reload: %+v %v", in2.Meta, err)
	}
	if in2.UsagePath() != filepath.Join(dir, "usage.json") {
		t.Errorf("UsagePath = %s", in2.UsagePath())
	}
}

func TestConfigPrices(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(`prices:
  claude-opus-5-5: {input: 4, cache_write_5m: 5, cache_write_1h: 8, cache_read: 0.2, output: 20}
  local-model: {output: 1.5}
`), 0o644)
	c, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := PriceYAML{Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.2, Output: 20}
	if c.Prices["claude-opus-5-5"] != want || c.Prices["local-model"].Output != 1.5 {
		t.Errorf("prices = %+v", c.Prices)
	}
}
