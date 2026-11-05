// playwright-capture.mjs: a before/after capture template for prstk audit evidence.
//
// Run it once before you change any code (--phase before) and again on the
// approved code (--phase after). Same script, same steps, so the two sets of
// screenshots line up step for step on the prstk board.
//
// Setup, once, in a scratch directory OUTSIDE the worktree (nothing is added to the repo):
//
//   mkdir -p "$TMPDIR/pw-capture" && cd "$TMPDIR/pw-capture"
//   npm init -y && npm i -D playwright@1.64.1
//   npx playwright install chromium            # downloads the browser build this version pins
//   cp <path-to-prstk>/docs/evidence/playwright-capture.mjs ./capture.mjs   # then edit `steps`
//
// (`npx -y playwright@1.64.1 install chromium` also fetches the browser without a local install,
// but the script's `import 'playwright'` needs the package next to it, hence the scratch install.)
//
// Run:
//
//   node capture.mjs --base-url http://localhost:3000 --out ./out --phase before
//   node capture.mjs --base-url http://localhost:3000 --out ./out --phase after --video
//
// Keep the version pinned to the exact value above: it pins the Chromium revision,
// and a different revision renders fonts and anti-aliasing slightly differently,
// which shows up as noise in a before/after comparison.
//
// Arguments:
//   --base-url <url>        where the app is running (default http://localhost:3000)
//   --out <dir>             where files are written (default ./evidence-out)
//   --phase before|after    prefixes file names so the two runs never collide
//   --video                 also record the whole run as one .webm
//
// Output, one line per file, on stdout (everything else goes to stderr):
//   step <n>  <absolute path>  <caption>
// The video line uses the step after the last screenshot step, so before and
// after videos pair up. Feed each line to prstk, for example:
//   prstk evidence add 03 --phase before --file <path> --step <n> --caption "<caption>"
//
// To adapt: edit `steps` below. Each step navigates or interacts, then the
// script takes one full-page screenshot. Nothing else here is repo-specific.

import { chromium } from 'playwright';
import { mkdirSync, readdirSync, renameSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';

// ---- edit this part -------------------------------------------------------

// One entry per screen or state to capture. `name` becomes part of the file
// name, `caption` is what the board shows, `action` drives the page into the
// state to photograph. Steps run in order on one page, so state carries over.
const steps = [
  {
    name: 'home',
    caption: 'Home page after load',
    action: async (page, baseUrl) => {
      await page.goto(baseUrl, { waitUntil: 'networkidle' });
    },
  },
  // {
  //   name: 'signed-in',
  //   caption: 'Dashboard after signing in',
  //   action: async (page) => {
  //     await page.getByLabel('Email').fill('demo@example.com');
  //     await page.getByRole('button', { name: 'Sign in' }).click();
  //     await page.waitForURL('**/dashboard');
  //   },
  // },
];

// Hide anything that changes between runs (clocks, "x minutes ago", random
// avatars) so the before and after shots differ only where the code changed.
const hideForDeterminism = `
  [data-testid="clock"], time, .timestamp, .relative-time { visibility: hidden !important; }
`;

// ---------------------------------------------------------------------------

function parseArgs(argv) {
  const args = { baseUrl: 'http://localhost:3000', out: './evidence-out', phase: '', video: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--base-url') args.baseUrl = argv[++i];
    else if (a === '--out') args.out = argv[++i];
    else if (a === '--phase') args.phase = argv[++i];
    else if (a === '--video') args.video = true;
    else {
      console.error(`unknown argument: ${a}`);
      process.exit(2);
    }
  }
  if (args.phase !== 'before' && args.phase !== 'after') {
    console.error('--phase before|after is required');
    process.exit(2);
  }
  if (!args.baseUrl || !args.out) {
    console.error('--base-url and --out need a value');
    process.exit(2);
  }
  return args;
}

const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'step';

const { baseUrl, out, phase, video } = parseArgs(process.argv.slice(2));
const outDir = resolve(out);
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1280, height: 800 },
  deviceScaleFactor: 1,
  reducedMotion: 'reduce',
  colorScheme: 'light',
  locale: 'en-US',
  timezoneId: 'UTC',
  ...(video ? { recordVideo: { dir: join(outDir, `.${phase}-video-tmp`), size: { width: 1280, height: 800 } } } : {}),
});
const page = await context.newPage();
// Freeze the clock so any time the CSS below misses is still stable.
await page.clock.setFixedTime(new Date('2026-01-01T12:00:00Z'));

const produced = [];
let failed = null;
try {
  for (const [i, step] of steps.entries()) {
    const n = i + 1;
    await step.action(page, baseUrl);
    await page.addStyleTag({ content: hideForDeterminism });
    await page.waitForTimeout(250); // let transitions settle
    const file = join(outDir, `${phase}-step-${n}-${slug(step.name)}.png`);
    await page.screenshot({ path: file, fullPage: true, animations: 'disabled' });
    produced.push({ step: n, file, caption: step.caption });
  }
} catch (err) {
  failed = err;
} finally {
  // Closing the context is what flushes the video to disk.
  await context.close();
  await browser.close();
}

if (video) {
  const tmp = join(outDir, `.${phase}-video-tmp`);
  const webm = readdirSync(tmp).find((f) => f.endsWith('.webm'));
  if (webm) {
    const file = join(outDir, `${phase}-run.webm`);
    renameSync(join(tmp, webm), file);
    produced.push({ step: steps.length + 1, file, caption: 'Full run (video)' });
  }
  rmSync(tmp, { recursive: true, force: true });
}

if (failed) {
  console.error(`capture failed: ${failed.stack || failed}`);
  process.exit(1);
}
for (const p of produced) console.log(`step ${p.step}\t${p.file}\t${p.caption}`);
