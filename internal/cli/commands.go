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
	c.Flags().StringVar(&add.evidence, "evidence", "", "require audit evidence: screenshots or video")
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

	var openCmd, readyCmd *cobra.Command
	var skipOpen, skipReady string
	openCmd = cmd("open <id>", "Push and open a draft PR (or re-enter director review)", 1, func(args []string) error {
		skip, err := skipReason(openCmd, skipOpen)
		if err != nil {
			return err
		}
		return cmdOpen(a, args[0], skip)
	})
	openCmd.Flags().StringVar(&skipOpen, "skip-evidence", "", "bypass the audit-evidence gate, with a `reason` (logged)")
	readyCmd = cmd("ready <id>", "Mark the PR ready for review and release the worker", 1, func(args []string) error {
		skip, err := skipReason(readyCmd, skipReady)
		if err != nil {
			return err
		}
		return cmdReady(a, args[0], skip)
	})
	readyCmd.Flags().StringVar(&skipReady, "skip-evidence", "", "bypass the audit-evidence gate, with a `reason` (logged)")
	cmd("propagate <id>", "Merge each parent into its children, parent-first", 1, func(args []string) error {
		return cmdPropagate(a, args[0])
	})
	cmd("merged <id>", "Record a merge verified on GitHub; retarget children", 1, func(args []string) error {
		return cmdMerged(a, args[0])
	})

	// ---- director records ----
	var logFile string
	logCmd := &cobra.Command{
		Use:   "log <type> [message...]",
		Short: "Append a timestamped director entry (review, feedback or replan) to LOG.md",
		Args:  cobra.MinimumNArgs(1),
		RunE: run(func(args []string) error {
			return cmdLog(a, args[0], args[1:], logFile)
		}),
	}
	logCmd.Flags().StringVar(&logFile, "file", "", "read the (multi-line) message from `path`")
	root.AddCommand(logCmd)

	var rv reviewOpts
	c = cmd("review <id>", "Post a review to the PR, record it as a numbered round, and log it", 1, func(args []string) error {
		rv.id = args[0]
		return cmdReview(a, rv)
	})
	c.Flags().StringVar(&rv.file, "file", "", "review text `path` (required)")
	c.Flags().StringVar(&rv.verdict, "verdict", "", "approved, approved-with-nits or changes-requested (required)")
	c.Flags().StringVar(&rv.summary, "summary", "", "one-line LOG summary (default: first line of the file)")
	c.Flags().BoolVar(&rv.noPost, "no-post", false, "record locally without posting to GitHub")
	c.MarkFlagRequired("file")
	c.MarkFlagRequired("verdict")

	var fb feedbackOpts
	c = cmd("feedback <id>", "Record the human reviewer's feedback verbatim in the spec (from GitHub or a file) and log it", 1, func(args []string) error {
		fb.id = args[0]
		return cmdFeedback(a, fb)
	})
	c.Flags().BoolVar(&fb.fromGH, "from-gh", false, "import new reviews and comments from the PR on GitHub")
	c.Flags().StringVar(&fb.file, "file", "", "feedback text `path` (feedback given in chat)")
	c.Flags().StringVar(&fb.author, "author", "", "with --from-gh: only this GitHub `login`")

	var checkCmds []string
	c = cmd("check <id>", "Merge the latest base into a throwaway copy of the PR branch and run the checks", 1, func(args []string) error {
		return cmdCheck(a, args[0], checkCmds)
	})
	c.Flags().StringArrayVar(&checkCmds, "cmd", nil, "check command to run instead of config.yaml check (repeatable)")

	var spawnWorker string
	c = cmd("spawn-prompt <id>", "Print the prompt for spawning the PR's worker", 1, func(args []string) error {
		return cmdSpawnPrompt(a, args[0], spawnWorker)
	})
	c.Flags().StringVar(&spawnWorker, "worker", "", "teammate name (default: the assigned worker)")

	// ---- audit evidence ----
	evCmd := &cobra.Command{Use: "evidence", Short: "Record and list audit evidence (before/after captures)"}
	root.AddCommand(evCmd)
	evSub := func(use, short string, fn func(args []string) error) *cobra.Command {
		c := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: run(fn)}
		evCmd.AddCommand(c)
		return c
	}
	var evAdd evidenceAddOpts
	c = evSub("add <id>", "Record a before/after capture for a PR", func(args []string) error {
		evAdd.id = args[0]
		return cmdEvidenceAdd(a, evAdd)
	})
	c.Flags().StringVar(&evAdd.phase, "phase", "", "before or after (required)")
	c.Flags().StringVar(&evAdd.file, "file", "", "capture `path` (png, jpg, webp, gif, webm, mp4; required)")
	c.Flags().IntVar(&evAdd.step, "step", 0, "step number (default: next, or for after the first uncovered before step)")
	c.Flags().StringVar(&evAdd.caption, "caption", "", "caption")
	c.MarkFlagRequired("phase")
	c.MarkFlagRequired("file")
	var evScript string
	c = evSub("script <id>", "Register the capture script for a PR", func(args []string) error {
		return cmdEvidenceScript(a, args[0], evScript)
	})
	c.Flags().StringVar(&evScript, "file", "", "script `path` (.mjs, .js, .ts, .tape; required)")
	c.MarkFlagRequired("file")
	c = evSub("ls <id>", "List a PR's evidence steps", func(args []string) error {
		return cmdEvidenceLs(a, args[0])
	})
	jsonFlag(c)

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
		return strings.TrimPrefix(c.CommandPath(), root.Name()+" ")
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
