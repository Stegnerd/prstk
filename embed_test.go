package prstk

import (
	"bytes"
	"os"
	"testing"
)

// The embedded definitions must be byte-identical to the files on disk, and a
// renamed or added agent file must fail loudly here.
func TestAgentsMatchDisk(t *testing.T) {
	for _, name := range []string{"prstk-director.md", "prstk-worker.md"} {
		emb, err := Agents.ReadFile("agents/" + name)
		if err != nil {
			t.Fatal(err)
		}
		disk, err := os.ReadFile("agents/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(emb, disk) {
			t.Errorf("%s: embedded copy differs from agents/%s", name, name)
		}
	}
	entries, err := os.ReadDir("agents")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, err := Agents.ReadFile("agents/" + e.Name()); err != nil {
			t.Errorf("agents/%s is not embedded (update embed.go and prstk setup)", e.Name())
		}
	}
}
