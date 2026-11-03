// Package cli implements the prstk commands.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"prstk/internal/gh"
	"prstk/internal/gitx"
	"prstk/internal/plan"
	"prstk/internal/render"
)

type App struct {
	Stdout, Stderr io.Writer
	Cwd            string
	Home           string
	Now            func() time.Time
	GH             gh.Client
	Git            gitx.Git
	Getenv         func(string) string
	UserHome       string // for ~/.claude/settings.json in doctor

	// set per invocation
	initiativeFlag string
	jsonOut        bool
	parsed         bool             // args/flags parsed; RunE started
	in             *plan.Initiative // loaded initiative, for verbose logging
}

// usageError is printed with a hint to run help.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// Run executes one command and returns the exit code: 0 ok, 1 command
// failed, 2 usage error (bad args/flags, unknown command).
func (a *App) Run(argv []string) int {
	a.initiativeFlag, a.jsonOut, a.parsed, a.in = "", false, false, nil
	root := a.rootCmd()
	root.SetArgs(argv)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	err := root.Execute()
	name := commandName(root, argv)
	if err == nil {
		if a.in != nil {
			a.in.Verbose("%s ok", strings.Join(argv, " "))
		}
		return 0
	}
	if a.in != nil {
		a.in.Verbose("%s failed: %v", strings.Join(argv, " "), err)
	}
	fmt.Fprintf(a.Stderr, "%s: %v\n", errPrefix(name), err)
	var ue usageError
	if !a.parsed || errors.As(err, &ue) {
		hint := "prstk --help"
		if name != "" {
			hint = "prstk " + name + " --help"
		}
		fmt.Fprintf(a.Stderr, "run '%s' for usage\n", hint)
		return 2
	}
	return 1
}

// ---- initiative resolution & mutation ----

func (a *App) repoKeyFromCwd() (string, error) {
	url, err := a.Git.Origin(a.Cwd)
	if err != nil {
		return "", err
	}
	o, r, err := plan.ParseRemote(url)
	if err != nil {
		return "", err
	}
	return plan.RepoKey(o, r), nil
}

func (a *App) resolve() (string, error) {
	return plan.Resolver{Home: a.Home, Cwd: a.Cwd, Flag: a.initiativeFlag, RepoKey: a.repoKeyFromCwd}.Resolve()
}

func (a *App) load() (*plan.Initiative, error) {
	dir, err := a.resolve()
	if err != nil {
		return nil, err
	}
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		return nil, err
	}
	a.attach(in)
	return in, nil
}

// attach routes verbose git/gh output to the initiative's .prstk.log.
func (a *App) attach(in *plan.Initiative) {
	a.in = in
	a.Git.Log = in.Verbose
	if e, ok := a.GH.(gh.Exec); ok {
		e.Log = in.Verbose
		a.GH = e
	}
}

type validationFailed struct{ n int }

func (v validationFailed) Error() string { return fmt.Sprintf("%d validation error(s)", v.n) }

// mutate resolves, locks, loads, runs fn, then validates and regenerates the
// PLAN.md block and board.html. fn returns the one-line success message.
func (a *App) mutate(fn func(in *plan.Initiative) (string, error)) error {
	dir, err := a.resolve()
	if err != nil {
		return err
	}
	return a.mutateDir(dir, fn)
}

func (a *App) mutateDir(dir string, fn func(in *plan.Initiative) (string, error)) error {
	lock, err := plan.AcquireLock(dir+"/.lock", 30*time.Second)
	if err != nil {
		return err
	}
	defer lock.Release()
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		return err
	}
	a.attach(in)
	msg, ferr := fn(in)
	// Regenerate even on failure: partial progress should be visible.
	issues, rerr := a.regenerate(dir)
	if ferr != nil {
		return ferr
	}
	if rerr != nil {
		return fmt.Errorf("%s, but regenerating failed: %w", msg, rerr)
	}
	errs, _ := plan.CountErrors(issues)
	if errs > 0 {
		msg += fmt.Sprintf(" · ⚠ validate: %d error(s)", errs)
		for _, i := range issues {
			if i.Error {
				fmt.Fprintln(a.Stderr, i.String())
			}
		}
	}
	a.out(msg)
	return nil
}

// regenerate reloads from disk, validates, and rewrites generated outputs.
func (a *App) regenerate(dir string) ([]plan.Issue, error) {
	in, err := plan.LoadInitiative(dir)
	if err != nil {
		return nil, err
	}
	now := a.Now()
	issues := in.Validate(now)
	block := render.PlanBlock(in, now)
	if err := in.SavePlan(func(old string) string {
		return render.ReplaceBlock(old, render.PlanBegin, render.PlanEnd, block)
	}); err != nil {
		return issues, err
	}
	b := render.BuildBoard(in, now)
	page, err := render.BoardPage(render.PageOpts{Title: in.Meta.Initiative + " · prstk", Static: true, Data: &b})
	if err != nil {
		return issues, err
	}
	return issues, plan.WriteFileAtomic(in.BoardPath(), page)
}

func (a *App) out(format string, args ...any) {
	fmt.Fprintf(a.Stdout, format+"\n", args...)
}

func (a *App) printJSON(v any) error {
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// NewDefault builds an App wired to the real environment.
func NewDefault() (*App, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	home, err := plan.Home()
	if err != nil {
		return nil, err
	}
	uh, _ := os.UserHomeDir()
	return &App{
		Stdout: os.Stdout, Stderr: os.Stderr, Cwd: cwd, Home: home,
		Now: time.Now, GH: gh.Exec{}, Getenv: os.Getenv, UserHome: uh,
	}, nil
}
