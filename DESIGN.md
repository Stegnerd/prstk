# prstk — Design

A framework for breaking one task into a **stack of GitHub PRs**, worked in parallel by AI agents in separate git worktrees, with progress and history visible to a human on a live board.

`prstk` is the Go CLI at the center of it. This document is the full spec: roles, files, rules, commands, board, and the pilot project used to test it.

---

## 1. Roles

| Role | Who | Does | Never does |
|---|---|---|---|
| **Director** | Claude Code lead session, **Opus** | Plans the split, writes PR specs, runs `prstk`, spawns workers, reviews code, posts review comments, records Dalton's feedback, re-plans | Merges, force-pushes, writes code for PRs, edits `notes.md` or generated blocks |
| **Worker** (max 2 at once) | Claude Code teammate, **Sonnet** (Opus if the PR's spec says `model: opus`) | Implements one PR in its own worktree, writes `notes.md`, commits and pushes its own branch, resolves merge conflicts | Merges, force-pushes, edits `spec.md` / `PLAN.md` / `LOG.md`, renegotiates scope with other workers |
| **`prstk`** | Go CLI | All deterministic mechanics: worktrees, branches, PR creation, retargeting, stack nav, validation, board | Anything requiring judgment (e.g. conflict resolution) |
| **Dalton** | Human | Reviews PRs, gives feedback to the director in chat, **merges** | — |

Agents run with **Claude Code agent teams** (`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1`): the director is the lead, workers are teammates. Messaging is used for coordination; **anything that changes the plan must land in a file.**

The director runs `prstk` directly. There is no "clerk" agent; the CLI's terse output keeps the director's context clean.

---

## 2. Non-negotiable rules

1. **Only Dalton merges.** Nothing in the system merges a PR.
2. **No history rewriting.** No rebases, no force-pushes, no squash. Dalton merges with merge commits.
3. **Formal stacks.** Each PR's GitHub base is its parent's branch (or the initiative base branch if it has no parent).
4. **One parent per PR.** A PR that would need two parents is a planning error; the director must restructure.
5. **Fixes propagate up a stack by merging the parent into the child** (`git merge --no-ff <parent-branch>`), never rebasing.
6. **Single writer per file** (see §4).
7. **Plan state lives outside the repo** and is deleted when the initiative is done (after archiving).

---

## 3. Directory layout

```
~/.pr-plans/
  <owner>-<repo>/
    config.yaml                      # per-repo settings (optional)
    current                          # name of the active initiative (set by init/use)
    _archive/
      2026-11-01-0900-older.md       # legacy single-file archive (pre-dir teardown; left as is)
      2026-11-03-1415-pokemon-api/   # whole initiative dir, moved here at teardown
        PLAN.md  LOG.md  board.html  # final; LOG ends with the teardown entry
        prs/<id>-<slug>/spec.md, notes.md
    2026-11-03-1415-pokemon-api/     # one initiative
      PLAN.md
      LOG.md
      board.html                     # generated static fallback
      .prstk.log                     # verbose CLI output
      .lock
      prs/
        01-scaffold/
          spec.md
          notes.md
        02-db-layer/
          spec.md
          notes.md
      worktrees/
        01/                          # git worktree for PR 01
        02/
```

- Repo key is `<owner>-<repo>` from the `origin` remote URL, not the folder name.
- Initiative dir name: `YYYY-MM-DD-HHMM-<slug>` (local time), so it sorts chronologically.
- Worktrees live inside the initiative so teardown removes everything together.
- At teardown the initiative dir is moved (one `rename`) to `_archive/<initiative>/`, minus `worktrees/`, `.lock` and `.prstk.log`. It stays loadable as an initiative. Legacy `_archive/<initiative>.md` files from older versions sit alongside and are never modified.

---

## 4. Files and schemas

### Ownership

| File | Writer(s) | Readers |
|---|---|---|
| `PLAN.md` hand-written sections | Director | All |
| `PLAN.md` generated block | `prstk` | All |
| `LOG.md` | `prstk` (mechanical entries, and the director's decision entries via `prstk log` / `prstk review`) | Dalton, director |
| `prs/*/spec.md` frontmatter | `prstk` (and director at creation via `prstk add`) | All |
| `prs/*/spec.md` body | Director (`### Round N` items inside `## Review: Director` are written by `prstk review`) | Worker, director |
| `prs/*/notes.md` | Assigned worker only | Director, downstream workers |

No two writers share a file region. `prstk` touches frontmatter and marker-delimited generated blocks, and appends `### Round N` items as the last item in `## Review: Director` (via `prstk review`) and entries to the end of `LOG.md`. It never edits anything else in a spec body.

### `PLAN.md`

```markdown
---
initiative: pokemon-api
repo: dalton/sample_proj
base_branch: develop
created: 2026-11-03T14:15:00-05:00
max_workers: 2
---

# Pokémon API

## Goal
## Non-goals
## Split rationale

<!-- prstk:generated:begin -->
(stack tree as Mermaid + status table — owned by prstk, never hand-edited)
<!-- prstk:generated:end -->
```

### `LOG.md`

Append-only. One entry per event:

```markdown
## 2026-11-03 14:22 · replan · director
Split 02 into 02a/02b. Auth turned out to be its own concern; 03 now stacks on 02b.
```

Entry types: `init`, `add`, `assign`, `open`, `review`, `feedback`, `ready`, `propagate`, `merged`, `replan`, `blocked`, `teardown`.
`prstk` writes mechanical entries as one line. The director's `review`, `feedback`, and `replan` entries (with reasoning, possibly multi-line) are also written by `prstk`, through `prstk review` and `prstk log`, so the timestamp is always real and the author is `director`.

### `prs/<id>-<slug>/spec.md`

```yaml
---
id: "03"
slug: list-endpoint
title: Add list endpoint
parent: "02"               # null = stacks directly on base_branch
conflicts_with: []         # same-wave PRs touching the same files; must be symmetric
status: planned
status_since: 2026-11-03T14:22:00-05:00
blocked_reason: ""
early_start: false         # director may allow start before parent passes review
model: sonnet              # sonnet | opus
worker: ""                 # teammate name while assigned
branch: prstk/pokemon-api/03-list-endpoint
pr_number: 0
---
```

Body sections (director-written):

- `## Goal`
- `## Scope` — `### In` / `### Out`
- `## Owns` — files and areas this PR may change; touching anything else requires asking the director
- `## Provides` — interface this PR exposes to children
- `## Consumes` — interface it relies on from its parent
- `## Definition of done` — tests pass, lint clean, notes filled, etc.
- `## Review: Director` — director's review findings per round
- `## Review: Dalton` — Dalton's feedback, copied in by the director **before** a fix worker is spawned

### `prs/<id>-<slug>/notes.md`

Worker-written. Must be good enough for a cold-start successor.

- `## Description` — becomes the PR body
- `## Worker notes` — decisions made, surprises, gotchas, anything downstream PRs should know

### `config.yaml` (per repo, optional)

```yaml
default_base: develop
max_workers: 2
setup: "go mod download"   # run in each new worktree after creation
```

---

## 5. Statuses

| Status | Set by | Meaning | Holds a worker slot? |
|---|---|---|---|
| `planned` | director (`add`) | Exists, not started | no |
| *ready* | **derived, never stored** | `planned` and parent has passed director review (or no parent, or `early_start` with parent ≥ `in-progress`) | — |
| `in-progress` | `prstk assign` | Worker building | yes |
| `blocked` | worker (`prstk status`) | Requires `blocked_reason` | yes |
| `director-review` | `prstk open` | Draft PR open, director reviewing | yes |
| `changes-requested` | director (`prstk status`) | Back to the worker | yes |
| `awaiting-you` | `prstk ready` | PR marked ready for review; worker released | no |
| `fix-pending` | director / `prstk propagate` | Needs a fresh worker (Dalton feedback or merge conflict) | no |
| `fixing` | `prstk assign` (from `fix-pending`) | Fix worker active | yes |
| `merged` | `prstk merged` | Verified merged via `gh` | no |
| `abandoned` | director (`prstk status`) | Dropped/replaced during re-plan | no |

"Passed director review" = parent status in `{awaiting-you, fix-pending, fixing, merged}`.

Workers stay alive until **director review completes** (`prstk ready`). After that, any further change is done by a fresh worker using `spec.md` + `notes.md` as the handoff.

---

## 6. Lifecycle

1. Director: `prstk init`, writes PLAN.md sections, `prstk add` for each PR, writes spec bodies, `prstk validate`.
2. Director: `prstk next` → `prstk assign <id> --worker <name>` → spawns teammate pointed at the worktree and spec.
3. Worker: implements in its worktree, commits, pushes its own branch, fills `notes.md`, messages director "done".
4. Director: `prstk open <id>` → draft PR created.
5. Director reviews (`gh pr diff`), posts findings as **comment reviews** on GitHub (approve/request-changes aren't possible on your own PRs) and in `## Review: Director`. If changes needed: `prstk status <id> changes-requested`, messages worker, loop.
6. Director satisfied: `prstk ready <id>` → draft flipped to ready for review, worker released, slot freed.
7. Dalton reviews. Feedback goes to the director in chat. Director copies it into `## Review: Dalton`, runs `prstk status <id> fix-pending`, then assigns a fresh worker when `next` says so. After fixes, director re-reviews and runs `prstk ready` again.
8. If a PR with children receives new commits: `prstk propagate <id>`.
9. Dalton merges and tells the director "merged #N". Director runs `prstk merged <id>`.
10. When everything is `merged` or `abandoned`: `prstk teardown`.

---

## 7. Scheduling (`prstk next`)

Free slots = `max_workers` − count of PRs in slot-holding statuses.

Candidate order:
1. `fix-pending` PRs (they unblock Dalton's merges)
2. *ready* PRs, by **critical path**: number of non-merged, non-abandoned descendants, descending
3. Tiebreak by id ascending

Output: one line per assignable PR, up to free slots. `--json` available.

---

## 8. CLI: `prstk`

### Global behavior

- **Initiative resolution**, in order:
  1. `--initiative <dir-name>` flag
  2. cwd is inside `~/.pr-plans/<repo>/<initiative>/` (e.g. a worktree)
  3. `~/.pr-plans/<repo>/current`
  4. exactly one active initiative for the repo
  5. otherwise error listing candidates
- **Locking:** every mutating command takes an exclusive `flock` on `<initiative>/.lock`.
- **Atomic writes:** write temp file in same dir, then rename.
- **Output:** one line on stdout on success; clear error on stderr with non-zero exit on failure; details appended to `.prstk.log`. `--json` on read commands.
- **Idempotency:** every command must be safe to re-run after a partial failure. It inspects actual state (branch exists? pushed? PR exists?) and continues from where it stopped.
- **Validation after mutation:** every mutating command runs validation and regenerates the PLAN.md generated block and `board.html`. If validation fails, the command reports it (the mutation stays; the director fixes the plan).
- **PR bodies:** `prstk` only edits content between `<!-- prstk:stack:begin -->` and `<!-- prstk:stack:end -->`; anything else in a PR body is left alone.

### Permissions (enforced by agent instructions, not by the CLI)

- Workers may run: `prstk status <own-id> blocked --reason …`, `prstk status <own-id> in-progress` (unblock), `prstk validate`, `prstk show <id>`.
- Everything else is director-only.

### Commands

| Command | Behavior |
|---|---|
| `prstk init <slug> [--base <branch>]` | Creates initiative dir, PLAN.md skeleton, LOG.md; sets `current`. Base defaults to `config.default_base`, then `develop`. Fails if base branch doesn't exist on the remote. |
| `prstk use <initiative>` | Sets `current`. |
| `prstk add <id> <slug> --title "…" [--parent <id>] [--model opus] [--conflicts-with <id,…>]` | Creates `prs/<id>-<slug>/spec.md` (frontmatter + empty body sections) and empty `notes.md`. Sets `branch`. Adds symmetric `conflicts_with`. |
| `prstk validate` | Runs §9 checks, regenerates PLAN.md block + `board.html`. |
| `prstk next` | §7. |
| `prstk show <id>` | Prints frontmatter + paths (spec, notes, worktree). |
| `prstk assign <id> --worker <name>` | Requires PR to be *ready* or `fix-pending` and a free slot. If no worktree: creates branch from parent branch (or base) and `git worktree add worktrees/<id>`, runs `config.setup`. Sets status `in-progress` (or `fixing` from `fix-pending`), sets `worker`. |
| `prstk open <id>` | Pushes branch, creates **draft** PR (`gh pr create --draft --base <parent-branch or base> --body …`) using `notes.md` Description + stack nav block. Records `pr_number`. Updates stack nav in all open PRs of the initiative. Sets `director-review`. |
| `prstk status <id> <status> [--reason "…"]` | Generic transition for statuses without a dedicated command. Validates transition legality. `blocked` requires `--reason`. |
| `prstk ready <id>` | `gh pr ready`, sets `awaiting-you`, clears `worker`. |
| `prstk review <id> --file <path> --verdict approved\|changes-requested [--summary "…"] [--no-post]` | If the PR is open (`pr_number > 0`) and not `--no-post`, posts the file as a comment review (`gh pr review --comment -R <repo>`, falling back to `gh pr comment`). Then appends `### Round N (date): <verdict>` to `## Review: Director` (suffixed ` (before PR opened)` when there's no PR) and a `review` LOG entry. Takes the lock for the writes; if both gh paths fail, writes nothing and exits 1. Does not change status. |
| `prstk log <review\|feedback\|replan> [message…] [--file <path>]` | Appends a timestamped `director` entry to LOG.md. The message is the args or a (multi-line) file, not both. |
| `prstk propagate <id>` | For each descendant in parent-first order: requires clean worktree, runs `git merge --no-ff <parent-branch>` in the child's worktree. Clean merge → push. Conflict → `git merge --abort`, set child `fix-pending` with reason listing conflicted files, skip that child's subtree, continue with others. Prints summary. |
| `prstk merged <id>` | Verifies via `gh pr view --json state,mergedAt`. Sets `merged`. For each child: `gh pr edit <child> --base <base_branch>`. Updates stack nav. Removes the merged PR's worktree (refuses if dirty). |
| `prstk board [--serve] [--port 7777]` | Without `--serve`: writes `board.html`. With `--serve`: live board (§10). |
| `prstk doctor` | Checks `git`, `gh` installed; `gh auth status`; base branch exists on remote; agent-teams flag set in env or `~/.claude/settings.json`. |
| `prstk teardown` | Refuses unless every PR is `merged` or `abandoned` and every worktree is clean. Also refuses if `_archive/<initiative>` already exists (dir or file). Appends the teardown LOG entry, regenerates PLAN.md and board.html, removes the worktrees, deletes `.lock`, `.prstk.log` and the empty `worktrees/`, then moves the initiative dir to `_archive/<initiative>/` with a single rename and clears `current`. If any step fails the initiative stays where it was, and re-running teardown completes it. |

### Stack nav block (in every PR body)

```markdown
<!-- prstk:stack:begin -->
**Stack** (pokemon-api)
- #101 01 scaffold ✅ merged
- #102 02 db layer
  - **#103 03 read endpoints ← this PR**
    - #105 05 write endpoints
  - #104 04 types endpoints
- #106 06 middleware
<!-- prstk:stack:end -->
```

---

## 9. Validation rules

Errors:
- Duplicate ids; id doesn't match directory prefix
- `parent` references a missing PR
- Cycle in parent chain
- `conflicts_with` references a missing PR, or isn't symmetric
- Unknown status
- `blocked` without `blocked_reason`
- PR past `planned` while not *ready* at time of start (child ahead of parent per §5 rules)
- PR `merged` while its parent isn't `merged`
- Non-abandoned PR whose parent is `abandoned` (needs re-plan)
- Slot-holding PR count > `max_workers`
- Status ≥ `director-review` with `pr_number` unset
- Missing `spec.md` or `notes.md`

Warnings:
- Status unchanged for > 2h in a slot-holding status
- `notes.md` Description empty at `director-review`

---

## 10. Live board

`prstk board --serve` — Dalton runs this in a terminal tab. The director does not start it.

- Go `net/http`, HTML/JS/CSS via `embed`. No external Go deps beyond YAML parsing.
- **Change detection:** poll mtimes of all plan files once per second (not fsnotify — no recursive watch, editors save via rename).
- **Push:** Server-Sent Events; client re-fetches JSON and re-renders in place (no full reload, keeps scroll).
- **Index page:** every active initiative under `~/.pr-plans/` (skip `_archive`).
- Port defaults to 7777; if taken, fail with a clear message.
- Static `board.html` (meta refresh 10s) is still written on every validate as a fallback.

### Layout

- **Slot bar:** `Slot 1: worker-1 → 03 (in-progress, 40m)` · `Slot 2: idle`
- **Columns:** Planned · Ready · In Progress · Director Review · Awaiting You · Merged
  - `changes-requested` and `fixing` show in In Progress with a tag; `fix-pending` shows in Ready with a tag
- **Swimlanes:** one per root-to-leaf path; a PR appears in the first lane that contains it. Lanes after the first are labeled by where they branch ("off 02").
- **Card:** id, title, PR link, parent id, Opus badge (only if opus), blocked badge (reason on hover), time in current status.
- **Stack tree:** Mermaid diagram (from CDN), nodes colored by status.
- Clicking a card (or a PR id in the slot bar) opens that PR's detail page; the header links to the initiative log.

### Detail and log views

Read-only, live-updating over the same SSE stream (a page re-fetches when an event names its initiative or `*`).

| Route | Serves |
|---|---|
| `GET /i/{repo}/{name}/pr/{id}/` | PR detail page (`/pr/{id}` redirects here) |
| `GET /i/{repo}/{name}/log/` | initiative log page (`/log` redirects here) |
| `GET /api/i/{repo}/{name}/pr/{id}` | `render.PRDetail` JSON |
| `GET /api/i/{repo}/{name}/log` | `[]render.LogEntry` JSON, file order (oldest first) |

- **PR page:** frontmatter facts (status, reason, parent, branch, worker, model, GitHub link), then collapsible sections: Spec (body up to `## Review: Director`), Director review (one block per `###` round), Dalton's feedback, Worker notes (`notes.md`), and the log entries that mention the PR.
- **Log page:** every LOG.md entry, filterable by type and PR (`?type=review&pr=03`, plus `order=newest`). An entry starts at a `## <yyyy-mm-dd hh:mm> · <type> · <by>` line. It mentions a PR when the PR's id appears as a whole token (`03` matches in "on 03." but not in `103`, `2026-11-03`, `v-03` or `03-slug`).
- **Path parameters:** `{repo}` and `{name}` must be safe directory names and load as an initiative; `{id}` must equal the id of one of its loaded PRs. Anything else is a 404. Files are read only through paths of the loaded initiative.
- **Untrusted markdown:** specs, notes and log entries are rendered in the browser only through `DOMPurify.sanitize(marked.parse(...))` (pinned versions from cdn.jsdelivr.net, with SRI). Every other field is inserted as text, never as HTML.

### Archived initiatives

Teardown (§8) moves a finished initiative to `<repo>/_archive/<dir>/`. The board browses these read-only, with no SSE and no write routes. Older teardowns left a single `_archive/<dir>.md` (PLAN plus LOG), and those open too.

- **Index:** below the active list, an "Archived" section lists every `_archive/<dir>/` that has a loadable `PLAN.md` and every legacy `_archive/*.md`, across all repos, newest first by the date prefix of the directory name. `GET /api/archives` returns `[{key, initiative, repo, dir, prs, legacy}]`. It is a sibling of `/api/initiatives`, which keeps its array shape. Teardown removes the initiative from the active list in the same step, so the existing `*` event refreshes the index.

| Route | Serves |
|---|---|
| `GET /a/{repo}/{name}/` | archived board (`/a/{repo}/{name}` redirects here) |
| `GET /a/{repo}/{name}/pr/{id}/` | archived PR detail |
| `GET /a/{repo}/{name}/log/` | archived log |
| `GET /a/{repo}/legacy/{file}` | legacy `.md` archive, rendered as sanitized markdown |
| `GET /api/a/{repo}/{name}`, `.../pr/{id}`, `.../log` | the same JSON as `/api/i/...` |
| `GET /api/legacy/{repo}/{file}` | `{key, initiative, repo, dir, file, body}`; frontmatter stripped |

- **Read-only pages:** `PageOpts.ReadOnly` hides the live indicator and opens no `EventSource`. The slot bar and "next" list are not shown. Links stay under `/a/{repo}/{name}/`.
- **Validation:** same rules as the active routes, rooted at `_archive`. `{repo}` and `{name}` must be safe directory names, and the archive must be a real directory (not a symlink) that loads as an initiative; `{id}` must equal a loaded PR's id. `{file}` must equal the name of a regular `*.md` file found by listing `_archive`. Anything else is a 404. Active initiatives are not served under `/a/`, nor archived ones under `/i/`.
- **Untrusted content:** archived markdown goes through the same `DOMPurify.sanitize(marked.parse(...))` path as live content, and Mermaid runs with `securityLevel: "strict"`.

---

## 11. Agent definitions (written after the CLI)

Global, in `~/.claude/agents/`:

- `prstk-director.md` — model: opus. Planning heuristics (schema/migrations first; interfaces before implementations; short chains; no two-parent PRs; flag risky PRs `model: opus`), full lifecycle (§6), review standards, the rule that plan changes go into files, never merges/force-pushes. Asks Dalton for the base branch at init.
- `prstk-worker.md` — model: sonnet. Work only in assigned worktree; read `spec.md` first; stay inside `## Owns`; message the director (not other workers) for scope/interface changes; commit and push own branch, never force; fill `notes.md` for a cold-start successor; report "done" only when Definition of done is met.

---

## 12. Pilot: `sample_proj`

Gin + SQLite Pokémon API. SQLite driver: **`modernc.org/sqlite`** (pure Go, no cgo — every fresh worktree builds without a C toolchain).

Prereq: `sample_proj` is a GitHub repo with an initial commit on `develop`, and `develop` is the default branch.

```
develop
└─ 01 scaffold
   ├─ 02 db layer        (model: opus)
   │  ├─ 03 read endpoints
   │  │  └─ 05 write endpoints
   │  └─ 04 types endpoints
   └─ 06 middleware      (conflicts_with: 02)
```

| PR | Scope | Parent |
|---|---|---|
| 01 scaffold | `go mod`, Gin server, `/health`, config, Makefile | develop |
| 02 db layer | SQLite connection, migrations, schema (`pokemon`, `types`, `pokemon_types`), seed ~20 Pokémon, repository interface | 01 |
| 03 read endpoints | `GET /pokemon` (pagination, `?type=` filter), `GET /pokemon/:id` | 02 |
| 04 types endpoints | `GET /types`, `GET /types/:name/pokemon` | 02 |
| 05 write endpoints | `POST` / `PUT` / `DELETE /pokemon` with validation | 03 |
| 06 middleware | Request logging, panic recovery, request IDs | 01 |

Exercises: a 4-deep chain, two sets of parallel siblings, `conflicts_with`, the `model` field, propagation through multiple descendants, and merge-time retargeting.

---

## 13. Build order

Each step testable before the next. Tests use temp git repos; `gh` behind an interface so it can be faked.

1. Plan model: load/parse PLAN.md, spec frontmatter, notes; write frontmatter atomically; locking
2. `init`, `use`, `add`, `validate`, `next`, `show`, `status`
3. Git/gh commands: `assign`, `open`, `ready`, `propagate`, `merged`
4. Generated blocks: PLAN.md block, stack nav, `board.html`
5. `board --serve`
6. `doctor`, `teardown`
7. Agent definitions (§11)
8. Run the pilot

---

## 14. Open items to verify before/while building

- **Agent teams specifics:** how a teammate's model is set, and how its working directory is set (third-party writeups disagree on whether teams use worktrees; `prstk` creates them regardless, but the worker must start in its worktree). Check the current Claude Code docs.
- **`gh` comment reviews on your own PR:** confirm `gh pr review --comment` works on PRs authored by the same account.
- **YAML library:** pick one that round-trips frontmatter without reordering fields (e.g. `gopkg.in/yaml.v3` with `yaml.Node`).
