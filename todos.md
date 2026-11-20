# TODO

Feedback from the audit-evidence initiative (2026-11-04, PRs #9–#12).

## Process (director)

- [x] **Write the bypass cases into specs for gate/permission PRs.** All three of PR 02's review findings were spec gaps: ready with a skipped before gate, committing after approval without pushing, and the PR description freezing stale counts. For any PR that adds a check, list "ways around it" under `## Scope` (gate skipped, HEAD moved, X missing) before assigning. *(#13: Planning heuristics in `agents/prstk-director.md`)*
- [x] **Ignore stale idle notices.** Idle notices arrive after the worker's "done" message has already been handled, and caused about seven noisy turns. Add a line to `agents/prstk-director.md`: when a worker's latest "done" has been handled, don't act on or report the idle notice. *(#13: `## Talking to the human and to workers`)*
- [x] **Pin environment workarounds in the spec.** w-03 and w-04 each solved the Playwright/Chromium cache mismatch differently (`executablePath` vs a scratch browser download). When a known environment quirk exists, write the one way to handle it into the spec. *(#13)*
- [x] **`early_start` is useless at max_workers 2 unless a slot is free.** 04's early start never mattered. Only use it when a slot will actually be free; maybe note this in the planning heuristics. *(#13)*
- [x] **Be precise about conditional behaviour when describing features.** I said doctor "warns if a PR needs a tool", which read as "always checks". *(#13)*

## prstk changes

- [x] **Audit evidence never reaches GitHub.** It lives only on the local board and in `_archive`, and `gh` can't upload images. For evidence that others can audit, consider pushing captures to an orphan branch (e.g. `prstk-evidence`) and linking them from the PR body or a comment. Decide this before other work on evidence. *(#18: `prstk evidence publish`, run by `prstk ready`; `evidence_publish: false` turns it off)*
- [x] **Workers' first push always fails with "no upstream".** `ensureBranch` creates branches with `--no-track`, so all four workers had to `git push -u`. Either have `prstk assign` set the upstream, or say `git push -u origin <branch>` in `agents/prstk-worker.md`. *(#14: both)*
- [x] **Let prstk dogfood its own changes.** The no-reinstall-mid-run rule meant PR 03 (board UI) couldn't carry before/after evidence. Consider a supported dev-binary override (e.g. `PRSTK_BIN`) that the director and workers both use for one initiative. *(#17)*
- [x] **"Approved with nits" verdict.** 04's second round was a one-word fix. A verdict that approves while handing small fixes to the still-assigned worker would save a full round trip (the director can't edit code). *(#15, which also added a verdict gate and an unpushed-commits check to `ready`)*
- [x] **doctor: make the capture-tool check useful.** *(#16)*
  - Today the `playwright` line only checks that `npx` is on PATH; check that the pinned `playwright@1.64.1` and its Chromium build are installed, and print the install command if not.
  - Optionally, always print the `playwright`/`vhs` lines (warning-only), not just when a live PR has evidence.
- [x] **DESIGN.md has fallen behind.** It doesn't cover `check`, `feedback` or `spawn-prompt` (noticed by w-04). *(#13)*

## One-off

- [x] **Run the vhs template once.** `brew install vhs`, then run a copy of `docs/evidence/vhs-capture.tape` pointed at a real CLI (e.g. `prstk --help` / `prstk doctor`). It's never been executed. *(Done as #16's before/after capture; findings below.)*
- [x] **Install Chromium 1248 for `playwright@1.64.1`.** `npx -y playwright@1.64.1 install chromium` (shared cache, not a global npm install).

---

# Follow-ups

From the todo-followups initiative (2026-11-06, PRs #13–#18).

## prstk changes

- [ ] **Tie an approval to the commit it approved.** `prstk ready` can't tell commits pushed with `prstk open` after an approval (with no new review round) from the approved ones: the latest round still reads `approved`. Record the approved HEAD sha in the `### Round N` entry and have `ready` (and the after-evidence gate) refuse when HEAD differs. Noted in #15's review.
- [ ] **Evidence branch cleanup.** `prstk-evidence` only grows, and nothing prunes it. Old links are pinned to commits, so deleting the branch (or rewriting it) breaks the images on every past PR's comment; the local captures in `_archive` survive. If it's ever needed: a human-run `prstk evidence prune` that recreates the branch with only open PRs' evidence and rewrites those PRs' comments. Destructive by design; never automatic.
- [ ] **Fix the vhs template** (`docs/evidence/vhs-capture.tape` + `docs/evidence/README.md`), from #16's first real run:
  - `sed 's/before/after/g'` also rewrites "before" inside paths; suggest a phase variable or a narrower substitution.
  - A second working directory needs its own `Hide` / `cd` / `Show` block; add an example.
  - Long absolute binary paths wrap in the screenshot; suggest a short symlink or `export PATH` in the `Hide` block.
  - `Output <name>.webm` makes vhs take over two minutes to return, though the screenshots are written in seconds; say so, and use `Output` only when a video is wanted.
  - Screenshots taken before the screen settles caused most of PR 01's capture retries (director-handoff); the template needs proven `Sleep`s around `Screenshot`.

## One-off

- [ ] **First real publish.** #18 has only run against a fake GitHub in tests. On the next evidence PR, check that the `prstk-evidence` branch and the PR comment come out as expected and that the images render.

---

# Follow-ups: token usage

From the token-usage initiative (2026-11-19, PRs #20–#22). That session's director made 88 API calls, with context growing from 43k to 261k tokens: 16.6M processed, of which 65k was output. Cost scales with calls × context size, so the items below cut one or the other.

## Director cost

- [x] **Stale idle notices cost a full-context turn each.** Seven director turns answered idle notices or repeated "done" messages that needed nothing; at 200k+ context each, that's about 1.5–2M tokens (around 10% of the session). Have workers send exactly one "done" and nothing after it (`agents/prstk-worker.md`). If the harness allows it, mute idle notices from a teammate whose latest "done" is already handled. *(Workers now end the turn right after "done", with no closing text, and don't answer acknowledgements. The director sends no acknowledgement messages, since each one starts a worker turn that ends in another notice. Muting isn't possible: every teammate turn ends with an idle notice to the lead that carries its final reply, and the `TeammateIdle` hook can only keep the teammate working (exit 2 / `decision: block`), not suppress the notice. Re-check if Claude Code adds a filter.)*
- [ ] **Look things up with a targeted search, not a full load.** Loading the whole `claude-api` skill to get five prices added about 25k tokens that every later call carried (around 2M over the session). In `agents/prstk-director.md`: grep a reference file for the fact you need rather than loading a whole skill or doc. The price table now lives in `internal/usage/price.go`.
- [ ] **A compact review bundle instead of full diffs.** The director read #20's full core diff (about 700 lines) plus #21 and #22, and in #22's fix round it diffed across a merge, so 03's already-reviewed code came through again. Ideas:
  - `prstk review <id> --pack` (or a separate command) prints: files changed, the code diff without tests, the worker's "Review first" list from notes.md, and the last `prstk check` result.
  - For fix rounds and nits, show only the commits since the last approved round, first-parent (no merged-in parent/main code). This pairs with "tie an approval to the commit it approved" above, which records that SHA.
  - Better still: a reviewer subagent reads the diff in its own throwaway context and returns findings; the director judges them and runs `prstk review`.
- [x] **A fresh director between waves.** By the end, every call carried the whole planning conversation (transcript research, spec drafting, the price lookup), none of it needed to review the last PR. Add a `prstk handoff` that writes a short state summary (open PRs, statuses, workers, pending decisions, anything the human said that isn't in LOG.md yet). The director then suggests `/clear` or a new session after planning and after each wave of merges. The plan files already hold the record, and recorded sessions keep `usage.json` continuous across director sessions. *(#23, #24: prstk handoff + /clear hook)*

## Evidence cost (director-handoff, 2026-11-19)

- [ ] **No before evidence without a real before.** PR 01 of director-handoff spent about 33 worker calls (about $1 of $3.25) capturing four "unknown command" screenshots. The director asks for before evidence only when the before state shows something real; `ready` already accepts an after-only PR.
- [ ] **Capture evidence in a Haiku subagent.** Capturing is mechanical (run the script, check the images, `prstk evidence add`). A Haiku subagent starts with a small context and its cache reads cost $0.01/M against Sonnet's $0.20/M. Tried on PR 02's before and PR 01's after capture: 16 and 22 Haiku calls, about $0.02 each, against about $1 for PR 01's Sonnet before capture.
- [ ] **usage.json files a worker's subagents under the director.** The Haiku capture agents that w-01 and w-02 spawned are counted as director overhead (`internal/usage/collect.go`). Attribute a subagent to the worker that spawned it, so evidence cost shows up on the PR. Measured by hand: 16 and 22 Haiku calls, about $0.02 each, against about $1 for PR 01's Sonnet before capture.
- [ ] **A teammate's subagent reports to the lead, not the teammate.** The Haiku capture agents and the claude-code-guide agent that w-01 and w-02 spawned delivered their final reports to the director's session, so both workers waited for, and then re-checked, results they never received. Until that's fixed, a worker should check the outcome itself (e.g. `prstk evidence ls`) rather than wait for the subagent's report; `agents/prstk-worker.md` says so.

## Token-usage gaps (from #20's review)

- [ ] **`teardown` doesn't record a new director session id.** It doesn't go through `mutateDir`, so if a restarted director's first command is `teardown`, its last turns aren't counted. Record the session in `cmdTeardown` before the final regenerate.
- [ ] **One director session running two initiatives at once.** A plain `prstk teardown` of the older initiative, run after the newer one was created, ends the newer one's usage window early (`internal/usage/collect.go` `window`: any teardown at or after `created` with no `--initiative` counts). Fix by resolving which initiative each teardown call targeted, e.g. from its tool result ("archived <name> → …") rather than its command line. Rare, so low priority.
