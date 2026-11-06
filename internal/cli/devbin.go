package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// prstkBinEnv names the dev-binary override: an absolute path to the prstk
// binary that the director and every worker run in place of `prstk`.
const prstkBinEnv = "PRSTK_BIN"

// prstkBin returns $PRSTK_BIN when it is set (fromEnv is true), else "prstk".
// It does not validate the value; see checkPrstkBin.
func prstkBin() (path string, fromEnv bool) {
	if v := os.Getenv(prstkBinEnv); v != "" {
		return v, true
	}
	return "prstk", false
}

// checkPrstkBin validates a PRSTK_BIN value: absolute, existing, a regular
// file, and executable.
func checkPrstkBin(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s=%s must be an absolute path", prstkBinEnv, path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s=%s: %v", prstkBinEnv, path, err)
	}
	if fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s=%s is not an executable file", prstkBinEnv, path)
	}
	return nil
}

// doctorPrstk prints doctor's `prstk` line and reports whether it failed.
// Without PRSTK_BIN it shows the binary found on PATH (falling back to the
// running executable); with it, it validates the override.
func (a *App) doctorPrstk() (failed bool) {
	if path, fromEnv := prstkBin(); fromEnv {
		if err := checkPrstkBin(path); err != nil {
			a.out("✗ %-14s %v", "prstk", err)
			return true
		}
		a.out("✓ %-14s %s=%s", "prstk", prstkBinEnv, path)
		return false
	}
	p, err := lookPath("prstk")
	if err != nil {
		if exe, eerr := os.Executable(); eerr == nil {
			p = exe
		} else {
			p = "prstk"
		}
	}
	a.out("✓ %-14s %s", "prstk", p)
	return false
}

// prstkBinSpawnLine is the spawn-prompt line telling a worker which binary to
// run; empty when PRSTK_BIN is unset.
func prstkBinSpawnLine() string {
	path, fromEnv := prstkBin()
	if !fromEnv {
		return ""
	}
	return fmt.Sprintf("Run prstk as `%s` (%s) for every prstk command in this initiative; never rebuild or replace it.\n", path, prstkBinEnv)
}
