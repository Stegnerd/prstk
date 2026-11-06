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

## One-off

- [ ] **First real publish.** #18 has only run against a fake GitHub in tests. On the next evidence PR, check that the `prstk-evidence` branch and the PR comment come out as expected and that the images render.
