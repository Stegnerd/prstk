# TODO

Feedback from the audit-evidence initiative (2026-11-04, PRs #9–#12).

## Process (director)

- [ ] **Write the bypass cases into specs for gate/permission PRs.** All three of PR 02's review findings were spec gaps: ready with a skipped before gate, committing after approval without pushing, and the PR description freezing stale counts. For any PR that adds a check, list "ways around it" under `## Scope` (gate skipped, HEAD moved, X missing) before assigning.
- [ ] **Ignore stale idle notices.** Idle notices arrive after the worker's "done" message has already been handled, and caused about seven noisy turns. Add a line to `agents/prstk-director.md`: when a worker's latest "done" has been handled, don't act on or report the idle notice.
- [ ] **Pin environment workarounds in the spec.** w-03 and w-04 each solved the Playwright/Chromium cache mismatch differently (`executablePath` vs a scratch browser download). When a known environment quirk exists, write the one way to handle it into the spec.
- [ ] **`early_start` is useless at max_workers 2 unless a slot is free.** 04's early start never mattered. Only use it when a slot will actually be free; maybe note this in the planning heuristics.
- [ ] **Be precise about conditional behaviour when describing features.** I said doctor "warns if a PR needs a tool", which read as "always checks".

## prstk changes

- [ ] **Audit evidence never reaches GitHub.** It lives only on the local board and in `_archive`, and `gh` can't upload images. For evidence that others can audit, consider pushing captures to an orphan branch (e.g. `prstk-evidence`) and linking them from the PR body or a comment. Decide this before other work on evidence.
- [ ] **Workers' first push always fails with "no upstream".** `ensureBranch` creates branches with `--no-track`, so all four workers had to `git push -u`. Either have `prstk assign` set the upstream, or say `git push -u origin <branch>` in `agents/prstk-worker.md`.
- [ ] **Let prstk dogfood its own changes.** The no-reinstall-mid-run rule meant PR 03 (board UI) couldn't carry before/after evidence. Consider a supported dev-binary override (e.g. `PRSTK_BIN`) that the director and workers both use for one initiative.
- [ ] **"Approved with nits" verdict.** 04's second round was a one-word fix. A verdict that approves while handing small fixes to the still-assigned worker would save a full round trip (the director can't edit code).
- [ ] **doctor: make the capture-tool check useful.**
  - Today the `playwright` line only checks that `npx` is on PATH; check that the pinned `playwright@1.64.1` and its Chromium build are installed, and print the install command if not.
  - Optionally, always print the `playwright`/`vhs` lines (warning-only), not just when a live PR has evidence.
- [ ] **DESIGN.md has fallen behind.** It doesn't cover `check`, `feedback` or `spawn-prompt` (noticed by w-04).

## One-off

- [ ] **Run the vhs template once.** `brew install vhs`, then run a copy of `docs/evidence/vhs-capture.tape` pointed at a real CLI (e.g. `prstk --help` / `prstk doctor`). It's never been executed.