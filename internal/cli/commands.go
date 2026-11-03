package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// rootCmd builds the command tree. It's rebuilt per Run so flag state never
// leaks between invocations (tests run many in one process).
func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "prstk",
		Short:         "Break one task into a stack of GitHub PRs worked in parallel by agents",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.initiativeFlag, "initiative", "", "initiative `dir` name (default: from cwd, then the repo's current initiative)")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return usageError{err.Error()} })

	// run marks that argument/flag parsing succeeded, so Run can tell usage
	// errors (exit 2) from command failures (exit 1).
	run := func(fn func(args []string) error) func(*cobra.Command, []string) error {
		return func(_ *cobra.Command, args []string) error {
			a.parsed = true
			return fn(args)
		}
	}
	jsonFlag := func(c *cobra.Command) {
		c.Flags().BoolVar(&a.jsonOut, "json", false, "JSON output")
	}
	cmd := func(use, short string, nargs int, fn func(args []string) error) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(nargs), RunE: run(fn)}
		root.AddCommand(c)
		return c
	}

	// ---- planning ----
	var base string
	c := cmd("init <slug>", "Create an initiative and make it current", 1, func(args []string) error {
		return cmdInit(a, args[0], base)
	})
	c.Flags().StringVar(&base, "base", "", "base branch (default: config default_base, then develop)")

	cmd("use <initiative>", "Set the current initiative", 1, func(args []string) error {
		return cmdUse(a, args[0])
	})

	var add addOpts
	c = cmd("add <id> <slug>", "Add a PR to the plan", 2, func(args []string) error {
		add.id, add.slug = args[0], args[1]
		return cmdAdd(a, add)
	})
	c.Flags().StringVar(&add.title, "title", "", "PR title (required)")
	c.Flags().StringVar(&add.parent, "parent", "", "parent PR id (default: stacks on the base branch)")
	c.Flags().StringVar(&add.model, "model", "sonnet", "worker model: sonnet or opus")
	c.Flags().StringSliceVar(&add.conflicts, "conflicts-with", nil, "same-wave PR ids touching the same files (kept symmetric)")
	c.Flags().BoolVar(&add.earlyStart, "early-start", false, "allow starting before the parent passes director review")
	c.MarkFlagRequired("title")

	c = cmd("validate", "Check the plan and regenerate PLAN.md and board.html", 0, func([]string) error {
		return cmdValidate(a)
	})
	jsonFlag(c)
	c = cmd("next", "List PRs to assign now, up to free slots", 0, func([]string) error {
		return cmdNext(a)
	})
	jsonFlag(c)
	c = cmd("show <id>", "Show a PR's frontmatter and paths", 1, func(args []string) error {
		return cmdShow(a, args[0])
	})
	jsonFlag(c)

	var reason string
	c = cmd("status <id> <status>", "Change status (statuses without a dedicated command)", 2, func(args []string) error {
		return cmdStatus(a, args[0], args[1], reason)
	})
	c.Flags().StringVar(&reason, "reason", "", "reason (required for blocked)")

	// ---- git / GitHub ----
	var worker string
	var ignoreConflicts bool
	c = cmd("assign <id>", "Create the worktree and assign a worker", 1, func(args []string) error {
		return cmdAssign(a, args[0], worker, ignoreConflicts)
	})
	c.Flags().StringVar(&worker, "worker", "", "teammate name (required)")
	c.Flags().BoolVar(&ignoreConflicts, "ignore-conflicts", false, "assign even if a conflicts_with PR is active")
	c.MarkFlagRequired("worker")

	cmd("open <id>", "Push and open a draft PR (or re-enter director review)", 1, func(args []string) error {
		return cmdOpen(a, args[0])
	})
	cmd("ready <id>", "Mark the PR ready for review and release the worker", 1, func(args []string) error {
		return cmdReady(a, args[0])
	})
	cmd("propagate <id>", "Merge each parent into its children, parent-first", 1, func(args []string) error {
		return cmdPropagate(a, args[0])
	})
	cmd("merged <id>", "Record a merge verified on GitHub; retarget children", 1, func(args []string) error {
		return cmdMerged(a, args[0])
	})

	// ---- board / maintenance ----
	var serve bool
	var port int
	c = cmd("board", "Write board.html, or serve the live board", 0, func([]string) error {
		return cmdBoard(a, serve, port)
	})
	c.Flags().BoolVar(&serve, "serve", false, "serve the live board")
	c.Flags().IntVar(&port, "port", 7777, "live board port")

	cmd("doctor", "Check git, gh, auth, base branch, and the agent-teams flag", 0, func([]string) error {
		return cmdDoctor(a)
	})
	cmd("teardown", "Archive and delete a finished initiative", 0, func([]string) error {
		return cmdTeardown(a)
	})
	return root
}

// commandName returns the subcommand for error prefixes ("prstk assign: …").
func commandName(root *cobra.Command, argv []string) string {
	if c, _, err := root.Find(argv); err == nil && c != root {
		return c.Name()
	}
	if len(argv) > 0 && !strings.HasPrefix(argv[0], "-") {
		return argv[0]
	}
	return ""
}

func errPrefix(name string) string {
	if name == "" {
		return "prstk"
	}
	return fmt.Sprintf("prstk %s", name)
}
