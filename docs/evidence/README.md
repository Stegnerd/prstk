# Audit evidence capture templates

Ready-to-copy scripts for the before/after captures that a prstk PR with an `evidence` level records. prstk itself never runs them; it stores the files you give it (`prstk evidence add`) and shows them on the board. See the main [README](../../README.md#audit-evidence) for the flow and [DESIGN.md](../../DESIGN.md) for the rules.

| File | Use it for |
|---|---|
| [`playwright-capture.mjs`](playwright-capture.mjs) | anything in a browser: web UIs, pages, forms, flows |
| [`vhs-capture.tape`](vhs-capture.tape) | terminal output, CLIs and TUIs |

Pick one per PR. If the change shows up in a browser, use Playwright. If it shows up in a terminal, use vhs.

## Install

| Tool | Install | Notes |
|---|---|---|
| Playwright | Node (`npx` comes with it) | The template pins `playwright@1.64.1`. Use that exact version: it pins the Chromium revision, and different revisions render slightly differently. |
| vhs | `brew install vhs` | Also installs `ttyd` and `ffmpeg`, which it needs. Other platforms: see <https://github.com/charmbracelet/vhs>. |

`prstk doctor` prints a `playwright` and a `vhs` line when a live PR has evidence, and warns (never fails) when the tool is missing.

Do the install outside the worktree, so nothing about capturing lands in the PR. For Playwright:

```sh
mkdir -p "$TMPDIR/pw-capture" && cd "$TMPDIR/pw-capture"
npm init -y && npm i -D playwright@1.64.1
npx playwright install chromium
cp <path-to-prstk>/docs/evidence/playwright-capture.mjs ./capture.mjs
```

The script must sit next to the `node_modules` that holds Playwright, because an ES module resolves `import 'playwright'` from its own directory.

## The routine

Everything below uses PR `03` as the example. Run `prstk` from the PR's worktree.

1. **Before any code change**, start the app from the worktree the way `## Evidence` in the spec says, and edit the template's `steps` (Playwright) or commands (vhs) to match the numbered steps in the spec.
2. Run the capture for the **before** phase:

   ```sh
   # Playwright
   node capture.mjs --base-url http://localhost:3000 --out ./out --phase before [--video]
   # vhs (tape names the output; the Screenshot lines write the step images)
   vhs before.tape
   ```

   Playwright prints one line per file: `step <n>`, the path and the caption, tab-separated. Use those values below.
3. Register the script so the after run (and any fix worker) can repeat it exactly, then record each output:

   ```sh
   prstk evidence script 03 --file ./capture.mjs          # or ./before.tape
   prstk evidence add 03 --phase before --file ./out/before-step-1-home.png --step 1 --caption "Home page after load"
   prstk evidence add 03 --phase before --file ./out/before-run.webm --step 2 --caption "Full run (video)"
   ```

   A `video` PR needs a before video (the `--video` run, or the `.webm` from vhs). A `screenshots` PR needs at least one before capture.
4. Build the change. The director's `prstk open` is refused until before evidence exists (unless it passes `--skip-evidence`).
5. **After the director approves**, from the same untouched, pushed HEAD, run the same script with the phase switched, and record it with the **same step numbers**:

   ```sh
   node capture.mjs --base-url http://localhost:3000 --out ./out --phase after [--video]
   # vhs: sed 's/before/after/g' before.tape > after.tape && vhs after.tape

   prstk evidence add 03 --phase after --file ./out/after-step-1-home.png --step 1 --caption "Home page after load"
   prstk evidence add 03 --phase after --file ./out/after-run.webm --step 2 --caption "Full run (video)"
   prstk evidence ls 03
   ```

   `prstk evidence ls 03` should show every step with an `after` that is `current`, not `stale`. Then tell the director. `prstk ready` checks the same thing, and also needs at least one after capture at HEAD (even if the before gate was skipped), plus an after video at HEAD for a `video` PR.

Never edit anything under `evidence/` by hand; prstk owns it. Re-recording a step just adds a newer capture, and the older one stays visible on the board as an earlier capture.

### Timing rules prstk enforces

- **Before** evidence only from an untouched in-progress worktree: no tracked changes, no commits beyond the branch's start point.
- **After** evidence only once the PR is in `director-review` with an `approved` verdict, with no uncommitted tracked changes, and with the worktree HEAD equal to what is on `origin/<branch>` (the code the director reviewed). If the director asked for changes, you push, the director runs `prstk open` again and re-approves, and only then do you re-capture.
- With no `--step`, `after` takes the lowest before step that has no after at HEAD. Passing `--step N` explicitly is clearer.

## Determinism tips

A before/after comparison is only useful if the only difference is the change. So:

- **Fixed viewport and terminal size.** The templates use 1280x800 at `deviceScaleFactor: 1` (Playwright) and a fixed width, height and font size (vhs). Do not change them between phases.
- **Seeded data.** Point both runs at the same seeded database or fixtures, not live data.
- **Hide clocks and timestamps.** The Playwright template freezes the browser clock and hides `time`, `.timestamp` and `.relative-time`; add the selectors your app uses. In vhs, set `TZ` and avoid prompts that print the time.
- **Reduced motion.** The Playwright template sets `reducedMotion: 'reduce'` and `animations: 'disabled'`. If your app animates, wait for it to finish before the screenshot.
- **Same browser build.** Keep the pinned Playwright version for both phases.
- **Deterministic waits.** Wait for a visible element or URL rather than a fixed delay where you can.
- **No secrets.** Evidence is shown on the board and kept in the archive. Do not capture tokens, personal data or real accounts.

## Limits

prstk accepts `.png`, `.jpg`, `.jpeg`, `.webp`, `.gif` (images) and `.webm`, `.mp4` (video), up to 200 MiB each. Scripts must be `.mjs`, `.js`, `.ts` or `.tape` and at most 1 MiB. File names are lowercased and sanitized when stored.
