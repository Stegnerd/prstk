package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Stegnerd/prstk/internal/plan"
	"github.com/Stegnerd/prstk/internal/render"
)

// handoffReminder is printed after a successful handoff: the one thing the
// command can't check for the director.
const handoffReminder = "Before you /clear: anything the human said in chat that isn't in LOG.md must be recorded with `prstk feedback` / `prstk log` first, or it is lost."

type handoffOpts struct {
	file        string
	print, hook bool
}

// handoffStdin is the stdin the --hook mode reads. It returns nil when stdin
// is a terminal, so a hand-run `prstk handoff --hook` never blocks. Tests
// replace it.
var handoffStdin = func() io.Reader {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return os.Stdin
}

func cmdHandoff(a *App, o handoffOpts) error {
	switch {
	case o.print && o.hook:
		return usageError{"--print and --hook can't be combined"}
	case o.file != "" && (o.print || o.hook):
		return usageError{"--file can't be combined with --print or --hook"}
	case o.hook:
		return cmdHandoffHook(a)
	case o.print:
		return cmdHandoffPrint(a)
	}
	return cmdHandoffWrite(a, o.file)
}

// cmdHandoffWrite applies the gate and writes HANDOFF.md under the lock.
func cmdHandoffWrite(a *App, file string) error {
	notes := ""
	if file != "" {
		var err error
		if notes, err = a.readSource(file); err != nil {
			return err
		}
		if strings.TrimSpace(notes) == "" {
			return fmt.Errorf("%s is empty", file)
		}
	}
	err := a.mutate(func(in *plan.Initiative) (string, error) {
		now := a.Now()
		log, err := in.ReadLog()
		if err != nil {
			return "", err
		}
		point, err := plan.HandoffPoint(in, log, in.Validate(now))
		if err != nil {
			return "", err
		}
		md := render.Handoff(in, render.HandoffOpts{Point: point, Now: now, Notes: notes})
		if err := plan.WriteFileAtomic(in.HandoffPath(), []byte(md)); err != nil {
			return "", err
		}
		if err := in.AppendLog(now, "handoff", fmt.Sprintf("point=%s · %d open PRs", point, in.OpenPRs())); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %s (%s)", in.HandoffPath(), point), nil
	})
	if err == nil {
		a.out("%s", handoffReminder)
	}
	return err
}

// cmdHandoffPrint prints the live summary. It takes no lock and writes
// nothing (not even the verbose log).
func cmdHandoffPrint(a *App) error {
	dir, err := a.resolve()
	if err != nil {
		return err
	}
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		return err
	}
	out, err := a.handoffView(in)
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.Stdout, out)
	return err
}

// handoffView builds what --print and --hook show: the live summary with the
// saved director notes, plus a line saying whether the handoff is stale.
func (a *App) handoffView(in *plan.Initiative) (string, error) {
	log, err := in.ReadLog()
	if err != nil {
		return "", err
	}
	saved := "no handoff written"
	notes := ""
	raw, err := os.ReadFile(in.HandoffPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return "", err
	default:
		notes = render.HandoffNotes(string(raw))
		var at time.Time
		last, since, found := plan.LogSinceHandoff(log)
		if found {
			at = last.At
		} else if fi, err := os.Stat(in.HandoffPath()); err == nil {
			at = fi.ModTime()
		}
		saved = "notes written " + at.Format(timeFmt)
		if found && since > 0 {
			saved += fmt.Sprintf("; %d LOG entries since", since)
		}
	}
	return render.Handoff(in, render.HandoffOpts{Now: a.Now(), Notes: notes, Saved: saved}), nil
}

// hookInput is the part of Claude Code's SessionStart hook JSON prstk uses;
// unknown fields are ignored.
type hookInput struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source"`
	Cwd       string `json:"cwd"`
}

// cmdHandoffHook is the SessionStart hook. It never fails the session start:
// it always returns nil, prints nothing on stderr, and prints nothing at all
// outside an initiative, in a worker session, or when anything goes wrong
// (the reason goes to .prstk.log when there's an initiative to hold it).
func cmdHandoffHook(a *App) error {
	var in *plan.Initiative
	defer func() {
		if r := recover(); r != nil && in != nil {
			in.Verbose("handoff --hook: panic: %v", r)
		}
	}()
	if a.Getenv != nil && a.Getenv("CLAUDE_CODE_AGENT") == "prstk-worker" {
		return nil
	}

	var hi hookInput
	jsonOK := false
	if r := handoffStdin(); r != nil {
		if b, err := io.ReadAll(io.LimitReader(r, 1<<20)); err == nil && json.Unmarshal(b, &hi) == nil {
			jsonOK = true
		}
	}
	if st, err := os.Stat(hi.Cwd); jsonOK && hi.Cwd != "" && err == nil && st.IsDir() {
		a.Cwd = hi.Cwd
	}

	dir, err := a.resolve()
	if err != nil {
		return nil // not in an initiative: stay silent
	}
	in, err = a.hookLoad(dir, hi.SessionID, jsonOK)
	if err != nil {
		(&plan.Initiative{Dir: dir}).Verbose("handoff --hook: %v", err)
		return nil
	}
	out, err := a.handoffView(in)
	if err != nil {
		in.Verbose("handoff --hook: %v", err)
		return nil
	}
	fmt.Fprintf(a.Stdout, "prstk: resuming initiative %s from a handoff\n\n%s", in.Meta.Initiative, out)
	return nil
}

// hookLoad loads the initiative and records the hook's session id in PLAN.md
// (under the lock, once per id). When the lock can't be taken it skips the
// recording and still returns the initiative.
func (a *App) hookLoad(dir, sessionID string, record bool) (*plan.Initiative, error) {
	sessionID = strings.TrimSpace(sessionID)
	record = record && sessionID != "" && len(sessionID) <= 200 && !strings.ContainsAny(sessionID, "\r\n")
	lock, lerr := plan.AcquireLock(dir+"/.lock", 5*time.Second)
	if lerr == nil {
		defer lock.Release()
	}
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		return nil, err
	}
	a.attach(in)
	switch {
	case lerr != nil:
		in.Verbose("handoff --hook: not recording the session: %v", lerr)
	case record && !slices.Contains(in.Meta.Sessions, sessionID):
		in.Meta.Sessions = append(in.Meta.Sessions, sessionID)
		if err := in.SavePlan(nil); err != nil {
			in.Verbose("handoff --hook: recording the session: %v", err)
		}
	}
	return in, nil
}
