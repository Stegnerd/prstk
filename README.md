# prstk

Break one task into a **stack of GitHub PRs** and build them in parallel with Claude Code agents.

`prstk` is a Go CLI that does every deterministic mechanic: worktrees, branches, draft PRs, stack navigation, scheduling, retargeting and archiving. A **director** agent does the judgment work: planning, specs and reviews. **Worker** agents write the code, one PR each, in their own git worktrees. You review and merge.

![The live board for a three-PR stack: status columns across the top, the stack tree below](docs/images/board.png)

```text
            you (human)
              │  base branch, review feedback, "merged #N"
              ▼
        ┌───────────┐   prstk CLI   ┌──────────────────────────┐
        │ director  │──────────────▶│ ~/.pr-plans/<repo>/<init>│
        │  (opus)   │               │ PLAN.md LOG.md prs/*/    │
        └─────┬─────┘               └──────────────────────────┘
   spawns /   │   \ reviews                    ▲
          ▼   ▼    ▼                           │ notes.md
      ┌────────┐ ┌────────┐                    │
      │worker A│ │worker B│ ── commit/push ────┘
      │ wt/01  │ │ wt/02  │    (one worktree per PR)
      └────────┘ └────────┘
```

The full design is in [DESIGN.md](DESIGN.md). This README covers setup, the day-to-day flow, and what we learned in the first two runs.

---

## Setup (one time)

### 1. Prerequisites

| Need | Check |
|---|---|
| Go 1.27+ | `go version` |
| git | `git --version` |
| GitHub CLI, logged in | `gh auth status` |
| Claude Code with agent teams | `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` |

Turn on agent teams in your **Claude Code user settings**, `~/.claude/settings.json`, under `env`. Claude Code applies it to every session, including the teammates it spawns, without touching your shell profile:

```json
{
  "env": {
    "CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"
  }
}
```

If `settings.json` already exists, add the key to its existing `env` object (or add an `env` object) rather than replacing the file. The value must be the string `"1"`. Restart Claude Code afterwards. `prstk doctor` reads the variable from its own environment, so to check it, run `doctor` from Claude Code's shell, or export the variable in your terminal just for that check.

### 2. Install the CLI

```sh
go install github.com/Stegnerd/prstk/cmd/prstk@latest   # installs to $(go env GOPATH)/bin/prstk
prstk --help                                            # should list review and log among the commands
```

Make sure `$(go env GOPATH)/bin` is on your PATH.

Or install from a clone. The agent definitions in step 3 live in the repo, so you need a clone anyway:

```sh
git clone https://github.com/Stegnerd/prstk.git
cd prstk
go install ./cmd/prstk
```

Below, `<path-to-prstk>` means wherever you cloned it.

> **Don't reinstall while an initiative is running.** The director and its workers use the `prstk` on your PATH, and swapping it mid-run changes the tool under them. Reinstall between initiatives. To try a change during a run, build it to a temp path instead: `go build -o /tmp/prstk-dev ./cmd/prstk`.

### 3. Symlink the agent definitions

Claude Code loads agent definitions from `~/.claude/agents/`. **Symlink** them to the copies in this repo; don't copy them. With a symlink, every merged change to `agents/*.md` takes effect in your next session, with nothing to resync.

```sh
mkdir -p ~/.claude/agents
ln -sf <path-to-prstk>/agents/prstk-director.md ~/.claude/agents/prstk-director.md
ln -sf <path-to-prstk>/agents/prstk-worker.md   ~/.claude/agents/prstk-worker.md
```

Check them:

```sh
ls -l ~/.claude/agents/prstk-*.md     # each should show "-> .../prstk/agents/..."
```

`ln -sf` replaces any existing plain copies. A plain copy is how the first two runs went stale: the repo's definitions were updated, but `~/.claude/agents` still held the old ones.

Agent definitions are read when a session starts, so **start a fresh session** after changing them.

### 4. Check the target repo

prstk opens PRs on GitHub against a base branch, so the repo you want to work on needs:

- an `origin` remote on GitHub
- the base branch pushed (for example `develop` or `main`)

Then run, from inside that repo:

```sh
prstk doctor
```

It checks git, gh, gh auth, that the base branch exists on origin, and the agent-teams flag. `doctor` checks for `develop` by default. If your base is `main`, a red "base branch" line is expected until you run `prstk init --base main`. Alternatively, set `default_base` in that repo's `config.yaml` (see below).

### 5. Optional per-repo config

`~/.pr-plans/<owner>-<repo>/config.yaml`:

```yaml
default_base: develop       # base branch when init has no --base
max_workers: 2              # parallel worker slots
setup: "test -f go.mod && go mod download || true"   # run in each new worktree
reviewer: <your name>       # name in the "## Review: <name>" heading; default "Human"
```

`reviewer` is the name of the human who reviews and merges. The director copies your feedback word for word into a `## Review: <name>` section of the PR's spec, using this name. If you leave it out, the heading is `## Review: Human`. Specs written earlier with another name (for example `## Review: Dalton`) keep working.

---

## First-time use

### Start the director

From inside the target repo:

```sh
cd path/to/your-repo
claude --agent prstk-director --add-dir ~/.pr-plans
```

- `--agent prstk-director` makes the session the director.
- `--add-dir ~/.pr-plans` lets the director and its workers edit the plan files and worktrees, which live **outside** the repo, without stalling on permission prompts.

### Give it a task

Tell it what to build and which base branch to use, for example:

> Build the Pokémon API from DESIGN.md §12: Gin + SQLite, split into the 6 PRs shown there, model: opus on 02, 06 conflicts_with 02. Base branch is develop.

If you don't name a base branch, the director asks for one.

### Watch the board

In another terminal tab:

```sh
prstk board --serve          # http://127.0.0.1:7777  (--port to change)
```

- **Index:** every active initiative, plus an **Archived** section.
- **Board:** a slot bar, status columns, and a stack diagram.
- **Card → PR page:** frontmatter, the spec, each director review round, your feedback, the worker's `notes.md`, and the log entries for that PR.
- **Log:** every LOG.md entry, filterable by type and PR.

The board updates live as the files change. If port 7777 is taken, an old board is probably still running. Find it with `lsof -nP -iTCP:7777 -sTCP:LISTEN` and stop it.

**Index:** active initiatives and the archive.

![Board index listing one active initiative and two archived ones](docs/images/board-index.png)

**PR page:** the spec, with status, branch and model at the top.

![PR page showing the spec's Goal, Scope, Owns and Provides sections](docs/images/pr-page-spec.png)

Further down the same page: the director's review rounds, your feedback, and the worker's `notes.md`.

![PR page showing a director review round, the empty human-feedback section, and worker notes](docs/images/pr-page-review.png)

**Log:** every mechanic and decision, with timestamps.

![Log entries for add, assign, replan, open, review, ready and merged](docs/images/log.png)

### Your part

You only ever do three things:

1. **Answer the director's questions**, such as the base branch or a scope decision.
2. **Review PRs** the director marks ready, the ones showing `awaiting-you` on the board. Give feedback in chat; the director records it word for word and assigns a fresh worker to fix it.
3. **Merge on GitHub**, then tell the director `merged #N`.

Merge with a **merge commit**, not a squash. Children are built on the parent's commits, so a squash makes them carry duplicate history.

---

## The flow

```text
 plan ──▶ assign ──▶ build ──▶ verify ──▶ open ──▶ review ──┬──▶ ready ──▶ you review ──▶ merge ──▶ teardown
                                                             │                │
                                    changes-requested ◀──────┘                └─▶ fix-pending ─▶ fresh worker
```

1. **Plan.** The director runs `prstk init <slug> --base <branch>`, fills PLAN.md (Goal, Non-goals, Split rationale), runs `prstk add` for each PR, and writes each `spec.md` body:
   - `Goal`
   - `Scope` (`In` / `Out`)
   - `Owns`: which files the PR may touch
   - `Provides`: the exact interface children rely on
   - `Consumes`: what it uses from its parent
   - `Definition of done`

   It runs `prstk validate` until the plan is clean.
2. **Assign.** `prstk next` lists what can start now, given free slots, parents, conflicts and critical path. For each line, the director runs `prstk assign <id> --worker <name>`, which creates the worktree and branch, then spawns a `prstk-worker` teammate with the absolute paths.
3. **Build.** The worker stays inside its worktree and its `Owns` list, commits, fills `notes.md` (Description becomes the PR body; Worker notes is the handoff), and sends **one** done message.
4. **Verify.** The director re-runs the definition of done itself. If the PR shares a Go package with sibling PRs, it trial-merges the current base in a **throwaway** worktree, never the worker's own.
5. **Open.** `prstk open <id>` pushes the branch and opens a **draft** PR with stack navigation.
6. **Review.** The director reads `gh pr diff`, writes its findings to a file, and runs:
   ```sh
   prstk review <id> --file review.md --verdict approved|changes-requested
   ```
   That posts a comment review to the PR, adds `### Round N` to the spec and writes the LOG entry, all in one step.
   - Changes needed: `prstk status <id> changes-requested`, message the worker, then re-open and re-review.
   - Approved: `prstk ready <id>` flips the draft to ready and frees the slot. Then the director messages the worker and **stops it with `TaskStop`**, because workers can't exit on their own.
7. **Your feedback.** The director copies it word for word into `## Review: <reviewer>` (the `reviewer` name from `config.yaml`, default `Human`), runs `prstk status <id> fix-pending` and `prstk log feedback "…"`, and a **fresh** worker fixes it.
8. **Propagate.** When a PR that has children gets new commits, `prstk propagate <id>` merges it down the stack. Conflicted children become `fix-pending`.
9. **Merged.** You say `merged #N`, and `prstk merged <id>` verifies it on GitHub, retargets the children to the base branch, updates the stack navigation and removes the worktree.
10. **Teardown.** When every PR is `merged` or `abandoned`, `prstk teardown` moves the whole initiative to `_archive/<name>/` and clears `current`.

Decisions such as re-plans, feedback and reviews are recorded with `prstk log <type> "…"` or `prstk review`, never typed into LOG.md by hand. prstk stamps the real time.

### Statuses

| Status | Meaning | Holds a slot |
|---|---|---|
| `planned` | not started (shows as *ready* once its parent passed review) | no |
| `in-progress` | worker building | yes |
| `blocked` | worker needs a decision | yes |
| `director-review` | draft PR open, director reviewing | yes |
| `changes-requested` | back to the worker | yes |
| `awaiting-you` | ready for your review | no |
| `fix-pending` | needs a fresh worker (your feedback or a merge conflict) | no |
| `fixing` | fix worker active | yes |
| `merged` / `abandoned` | done | no |

### Commands

| Command | What it does |
|---|---|
| `doctor` | Check git, gh, auth, base branch, agent-teams flag |
| `init <slug> --base <b>` | Create an initiative and make it current |
| `add <id> <slug> --title … [--parent] [--model opus] [--conflicts-with] [--early-start]` | Add a PR |
| `validate` | Check the plan; regenerate PLAN.md and board.html |
| `next` | What to assign now |
| `show <id> [--json]` | A PR's frontmatter and paths |
| `assign <id> --worker <name>` | Create the worktree and branch; mark in-progress |
| `open <id>` | Push and open a draft PR (or re-enter director review) |
| `review <id> --file f --verdict v [--summary] [--no-post]` | Post the review, record the round, log it |
| `log review\|feedback\|replan "…" [--file f]` | Timestamped director LOG entry |
| `status <id> <status> [--reason]` | Statuses without a dedicated command |
| `ready <id>` | Mark the draft ready; release the worker |
| `propagate <id>` | Merge a parent into its children, parent first |
| `merged <id>` | Verify the merge on GitHub; retarget children |
| `teardown` | Archive the finished initiative |
| `use <initiative>` | Switch the current initiative |
| `board [--serve] [--port]` | Write board.html, or serve the live board |

### Where things live

```text
~/.pr-plans/
  <owner>-<repo>/
    config.yaml                     # optional per-repo config
    current                         # name of the current initiative
    <yyyy-mm-dd-hhmm-slug>/         # an active initiative
      PLAN.md                       # director's sections + generated stack/status block
      LOG.md                        # append-only: prstk mechanics + director decisions
      board.html                    # static fallback board
      prs/<id>-<slug>/spec.md       # frontmatter (prstk) + body (director) + review rounds
      prs/<id>-<slug>/notes.md      # the worker's handoff
      worktrees/<id>/               # one git worktree per active PR
    _archive/
      <initiative>/                 # torn-down initiative, specs and notes included
      <initiative>.md               # legacy single-file archive (before the dir format)
```

Each file region has exactly one writer:

| Region | Writer |
|---|---|
| spec frontmatter and generated blocks | prstk |
| PLAN sections and spec bodies | the director |
| `### Round N` review items | `prstk review` |
| `notes.md` | the worker |

---

### Known follow-ups

- **Board security:** add a Content-Security-Policy header, and an SRI hash on the Mermaid script tag.
- **Index refresh:** refresh an open index when an archive appears by any route other than teardown.
- **Teardown:** close the small lock race just before the final rename, and make the "re-run teardown" message honest when the repo path can't be resolved.
- **`prstk check <id>`:** trial merge, vet, tests and an Owns-vs-diff check in one call. Today this is done by hand before every open.
- **Other retro ideas:**
  - `prstk spawn-prompt`, to generate worker prompts
  - `prstk set`, to change early-start, model or conflicts after `add`
  - `prstk feedback`, to record your feedback
  - a warning on squash merges
  - a warning when sibling PRs share a Go package

---

### Tips

- **Cut PRs by package.** One reviewable concern per PR, schema and interfaces first, and short chains with wide trees. If sibling PRs must share a package, declare `--conflicts-with` or require a merge of the latest base in the definition of done.
- **Use `--model opus` for risky PRs**: schemas and migrations, anything that deletes data, security, concurrency.
- **Pin interfaces in `Provides`.** Children are written against them, so exact signatures, routes and tables save fix rounds.
- **"Merged #N" means the GitHub PR number, not the prstk PR id.** In the pilot, prstk PR 06 was GitHub #4. The board shows both.

---

## License

MIT. See [LICENSE](LICENSE).
