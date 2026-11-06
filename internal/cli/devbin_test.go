package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func doctorOut(t *testing.T, e *env) (string, int) {
	t.Helper()
	var o, er bytes.Buffer
	a := e.app(e.repo)
	a.Stdout, a.Stderr = &o, &er
	code := a.Run([]string{"doctor"})
	return o.String(), code
}

func TestPrstkBinResolution(t *testing.T) {
	t.Setenv("PRSTK_BIN", "")
	if p, env := prstkBin(); p != "prstk" || env {
		t.Fatalf("unset: %q %v", p, env)
	}
	t.Setenv("PRSTK_BIN", "/x/prstk-dev")
	if p, env := prstkBin(); p != "/x/prstk-dev" || !env {
		t.Fatalf("set: %q %v", p, env)
	}
}

func TestSpawnPromptPrstkBin(t *testing.T) {
	e := newEnv(t)
	e.run("init", "vis")
	e.run("add", "01", "scaffold", "--title", "Scaffold")
	e.run("assign", "01", "--worker", "w-01")

	t.Setenv("PRSTK_BIN", "")
	if out := e.run("spawn-prompt", "01"); strings.Contains(out, "PRSTK_BIN") {
		t.Fatalf("unset should not mention PRSTK_BIN:\n%s", out)
	}
	t.Setenv("PRSTK_BIN", "/opt/prstk-dev")
	out := e.run("spawn-prompt", "01")
	want := "Run prstk as `/opt/prstk-dev` (PRSTK_BIN) for every prstk command in this initiative; never rebuild or replace it.\n"
	if !strings.Contains(out, want) {
		t.Fatalf("missing line:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "Read the spec first.") {
		t.Fatalf("line should precede the closing sentence:\n%s", out)
	}
}

func TestDoctorPrstkLine(t *testing.T) {
	e := newEnv(t)
	e.run("init", "dd")

	t.Setenv("PRSTK_BIN", "")
	if out, _ := doctorOut(t, e); !strings.Contains(out, "✓ prstk") || strings.Contains(out, "PRSTK_BIN") {
		t.Fatalf("unset:\n%s", out)
	}

	dir := t.TempDir()
	good := filepath.Join(dir, "prstk-dev")
	if err := os.WriteFile(good, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRSTK_BIN", good)
	if out, _ := doctorOut(t, e); !strings.Contains(out, "✓ prstk          PRSTK_BIN="+good) {
		t.Fatalf("valid:\n%s", out)
	}

	t.Setenv("PRSTK_BIN", filepath.Join(dir, "missing"))
	out, code := doctorOut(t, e)
	if !strings.Contains(out, "✗ prstk") || code == 0 {
		t.Fatalf("missing should fail doctor (code %d):\n%s", code, out)
	}

	t.Setenv("PRSTK_BIN", "bin/prstk")
	out, code = doctorOut(t, e)
	if !strings.Contains(out, "✗ prstk") || !strings.Contains(out, "absolute") || code == 0 {
		t.Fatalf("relative should fail doctor (code %d):\n%s", code, out)
	}

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRSTK_BIN", plain)
	out, code = doctorOut(t, e)
	if !strings.Contains(out, "✗ prstk") || !strings.Contains(out, "not an executable") || code == 0 {
		t.Fatalf("non-executable should fail doctor (code %d):\n%s", code, out)
	}
}
