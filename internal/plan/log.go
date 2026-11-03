package plan

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// AppendLog appends a mechanical one-line LOG.md entry.
func (in *Initiative) AppendLog(now time.Time, typ, msg string) error {
	f, err := os.OpenFile(in.LogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	msg = strings.ReplaceAll(strings.TrimSpace(msg), "\n", " ")
	_, err = fmt.Fprintf(f, "\n## %s · %s · prstk\n%s\n", now.Format("2006-01-02 15:04"), typ, msg)
	return err
}

// Verbose appends a line to .prstk.log. Errors are ignored: it's diagnostics.
func (in *Initiative) Verbose(format string, args ...any) {
	f, err := os.OpenFile(in.VerbosePath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
