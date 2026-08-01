// Captures the screenshots the homepage uses, from the live dev server.
//
// Not part of the suite: it is a content tool, run by hand when the reader's
// chrome changes and the front door's pictures go stale.
//
//   node e2e/home-shots.mjs                             # against http://127.0.0.1:9000
//   AF_BASE=http://host:9000 node e2e/home-shots.mjs
//   RESTORE_THEME=ledger node e2e/home-shots.mjs        # put the account's theme back at the end
//
// Every shot is the REAL application against real feeds — no mock-ups, because a
// homepage whose pictures are drawings is a homepage that stops matching the
// product within a release.
import { chromium } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const base = process.env.AF_BASE || 'http://127.0.0.1:9000';
const out = fileURLToPath(new URL('../web/shots/', import.meta.url));
mkdirSync(out, { recursive: true });

const browser = await chromium.launch();
const shot = (page, name) =>
  page.screenshot({ path: `${out}${name}.jpg`, type: 'jpeg', quality: 84 });

// ready opens the reader on a KNOWN scope.
//
// Not the bare address: where you were is account state (A30), so `/` resumes
// whatever the last run left behind — and after the My Feed shot below, that is
// My Feed. The hero silently became a second picture of the ranked stream, which
// is the kind of drift nobody notices until the page ships with two of the same
// screenshot. A path outranks the saved view at boot, so naming one pins it.
//
// The timeout is generous because every theme group below costs a full boot of a
// 33MB wasm module, and this instance re-ranks the head of the feed on each one.
async function ready(page, path = '/unread') {
  await page.goto(base.replace(/\/$/, '') + path, { waitUntil: 'load' });
  try {
    await page.waitForSelector('.item-row', { timeout: 60000 });
  } catch {
    // A phone shows ONE pane at a time, so a boot that lands on the article —
    // which is what the address carries after a story has been opened — has no
    // list in the DOM at all, and no amount of waiting will conjure one. Ask for
    // it. Costs a click on the wide layout, where the tab is not there and the
    // catch never fires.
    await page.getByRole('button', { name: /^Read$/ }).first().click().catch(() => {});
    await page.waitForTimeout(1200);
    await page.waitForSelector('.item-row', { timeout: 120000 });
  }
  await page.waitForTimeout(2500);
}

// The shots are taken in FIVE different themes, and grouped by theme rather than
// interleaved.
//
// Why rotate at all: the palette is a set of Go values resolved at render time,
// so a screenshot in Daylight is the same code path as one in Ink — same
// components, same stylesheet, one token table swapped. A page of screenshots
// that were all one colour would hide the single thing the design system exists
// to do, and would let a theme rot unnoticed: nothing catches "Ledger broke six
// days ago" if nobody ever photographs it.
//
// Why GROUPED: each theme change ends in a page load (see theme() below), a load
// is a fresh wasm boot, and a fresh boot on this instance triggers a Smart+
// re-rank — a real request against somebody's API key. Grouping turns thirteen
// of those into five.
//
// The theme is ACCOUNT state, not browser state, so a run leaves the instance
// wherever its last group put it. Pass RESTORE_THEME to put it back.

async function openSettings(page) {
  await page.keyboard.press(',');
  await page.waitForSelector('.set-tabs', { timeout: 20000 });
  await page.waitForTimeout(400);
}

async function closeSettings(page) {
  await page.keyboard.press('Escape');
  await page.waitForTimeout(700);
}

// theme() ends with a full page LOAD, and that is not belt-and-braces.
//
// A theme change that lands AFTER a client-side navigation updates the custom
// properties on :root but leaves <body>'s own background on the previous theme.
// Switch to Contrast having navigated while in Daylight and you get Contrast's
// white type drawn on Daylight's paper ground — every secondary label vanishes,
// and the screen photographs as a rendering fault rather than as a palette. Two
// runs of this script were thrown away to it before it was isolated.
//
// Reproduced 2026-08-01, measured off the DOM rather than guessed at:
//
//   after daylight              body background rgb(247,242,233)  type #241C30
//   after navigating to /myfeed body background rgb(247,242,233)  type #241C30
//   after switching to contrast body background rgb(247,242,233)  type #FFFFFF   <-- stale
//   after a reload              body background rgb(0,0,0)        type #FFFFFF
//
// Switching twice inside ONE settings session does not reproduce it; the
// navigation between two switches is the trigger. Filed in TODO.md. Do not
// "optimise" this load away without checking that the row titles on
// Settings → Listening are still on the screen: this is the workaround, and the
// workaround is not the fix.
async function theme(page, name, path = '/unread') {
  await openSettings(page);
  await page.locator("[data-action='settings-tab'][data-value='appearance']").click();
  await page.waitForTimeout(700);
  await page.locator(`[data-action='set-theme'][data-value='${name}']`).click();
  await page.waitForTimeout(2000);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);
  await ready(page, path);
}

// settingsShot: open settings, land on a tab, photograph it, come back out.
async function settingsShot(page, tab, name, settle = 2000) {
  await openSettings(page);
  await page.locator(`[data-action='settings-tab'][data-value='${tab}']`).click();
  await page.waitForTimeout(settle);
  await shot(page, name);
  await closeSettings(page);
}

// ---------------------------------------------------------------- desktop ---
{
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2 });
  const page = await ctx.newPage();
  await ready(page);

  // ---- Fanciful: the house plum ----
  await theme(page, 'fanciful');

  // The three panes with an article open. This is the page's hero.
  await page.locator('.item-row').first().click();
  await page.waitForTimeout(3500);
  await shot(page, 'reader-desktop');

  // Appearance — five theme cards, each drawn in its own colours. The screen
  // that changed every other picture this script takes.
  await settingsShot(page, 'appearance', 'appearance', 2200);

  // ---- Contrast: black ground, white type ----
  await theme(page, 'contrast');

  // The command palette, mid-query.
  await page.keyboard.press('Control+k');
  await page.waitForTimeout(600);
  await page.keyboard.type('ha', { delay: 60 });
  await page.waitForTimeout(900);
  await shot(page, 'palette');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);

  // Server — what a self-hosted application owes its operator.
  await settingsShot(page, 'server', 'server', 2500);

  // Listening — the tab that says what leaves the machine.
  await settingsShot(page, 'listening', 'listening', 2000);

  // The slideshow.
  //
  // The wait is long on purpose. A slide opens on a title card that holds for
  // 2.6s and THEN lifts, so a 3.5s capture photographs the card alone: a
  // headline on an empty screen, which is the one moment of the mode that looks
  // like nothing is happening. Past the rise the shot has the header, the body
  // and the source's wash all at once, which is what the mode actually is.
  try {
    await page.keyboard.press('s');
    await page.waitForTimeout(11000);
    await shot(page, 'slideshow');
    await page.keyboard.press('Escape');
    await page.waitForTimeout(1200);
  } catch { console.log('skip slideshow'); }

  // ---- Ledger: sepia and lamplight ----
  await theme(page, 'ledger');

  // The shortcut sheet, as the reader draws it. Before the search below, so the
  // list behind it is the plain scope rather than somebody's query.
  await page.keyboard.press('?');
  await page.waitForTimeout(1200);
  await shot(page, 'keys');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  // Discover — suggestions that each carry their evidence.
  await settingsShot(page, 'discover', 'discover', 2800);

  // Search, over FTS5, with results.
  await page.keyboard.press('/');
  await page.waitForTimeout(400);
  await page.keyboard.type('battery', { delay: 40 });
  await page.keyboard.press('Enter');
  await page.waitForTimeout(3500);
  await shot(page, 'search');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);

  // ---- Daylight: paper ----
  //
  // Straight to the address rather than through the rail: the search box is part
  // of the saved view, so a reload would otherwise restore the query from the
  // shot above, and a picture of the ranked stream with somebody's search
  // sitting in it is a picture of two things at once. A URL outranks the saved
  // view at boot.
  await theme(page, 'daylight', '/myfeed');
  await page.waitForTimeout(2500);
  await shot(page, 'myfeed');

  // Focus mode — the reading pane takes the window.
  await page.locator('.item-row').first().click();
  await page.waitForTimeout(2500);
  await page.keyboard.press('w');
  await page.waitForTimeout(2500);
  await shot(page, 'focus');
  await page.keyboard.press('w');
  await page.waitForTimeout(1500);

  // ---- Ink: near-black and cold ----
  await theme(page, 'ink');

  // FluxCast — the one screen the brand name appears on, and the only place the
  // broadcast's prerequisites are stated with their CURRENT state rather than
  // discovered by entering the mode and getting silence.
  await settingsShot(page, 'podcast', 'fluxcast', 2400);

  // Add a feed — named and filed at the moment of adding.
  try {
    await page.locator('.pane-rail').getByRole('button', { name: /Add a feed/i }).first().click();
    await page.waitForTimeout(1500);
    await shot(page, 'addfeed');
    await page.keyboard.press('Escape');
    await page.waitForTimeout(800);
  } catch { console.log('skip addfeed'); }

  await ctx.close();
}

// ------------------------------------------------------------------ phone ---
//
// Every wide shot above is captured AGAIN at 390px, and that is not a nicety.
// A 1600px-logical picture of a three-pane application shown in a 350px column
// is rendered at 0.21x — the reader's 14.5px body text lands at 3px on screen.
// The page swaps to these below 1000px through a <picture> element, where they
// display at roughly 1:1 and can actually be read.
//
// So the journey is repeated rather than the pictures resized. A phone shot of
// the reader is a different photograph, not a smaller one: it is the filmstrip,
// the tab bar, and one pane at a time.
{
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true,
  });
  const page = await ctx.newPage();
  const step = async (name, fn) => {
    try { await fn(); await shot(page, name); } catch (e) { console.log('skip', name, e.message.split('\n')[0]); }
  };

  await ready(page);

  // ---- Fanciful ----
  await theme(page, 'fanciful');
  await shot(page, 'reader-phone');

  await step('addfeed-phone', async () => {
    const rail = page.locator('.pane-rail');
    if (!(await rail.isVisible())) {
      await page.getByRole('button', { name: /Feeds/ }).first().click();
      await page.waitForTimeout(900);
    }
    await page.getByRole('button', { name: /Add a feed/i }).first().click();
    await page.waitForTimeout(1500);
  });
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(800);

  // ---- Daylight ----
  await theme(page, 'daylight');

  await step('phone-article', async () => {
    await page.locator('.item-row').first().click();
    await page.waitForTimeout(3000);
  });

  await step('focus-phone', async () => {
    await page.keyboard.press('w');
    await page.waitForTimeout(2000);
  });
  await page.keyboard.press('w').catch(() => {});
  await page.waitForTimeout(800);

  // ---- Ink ----
  await theme(page, 'ink');

  await step('keys-phone', async () => {
    await page.keyboard.press('?');
    await page.waitForTimeout(1200);
  });
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(600);

  await step('palette-phone', async () => {
    await page.keyboard.press('Control+k');
    await page.waitForTimeout(600);
    await page.keyboard.type('ha', { delay: 60 });
    await page.waitForTimeout(900);
  });
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(600);

  await step('slideshow-phone', async () => {
    await page.keyboard.press('s');
    await page.waitForTimeout(11000);   // past the title card's rise — see the desktop step
  });
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(1000);

  // ---- Ledger ----
  await theme(page, 'ledger');

  await step('search-phone', async () => {
    await page.keyboard.press('/');
    await page.waitForTimeout(400);
    await page.keyboard.type('battery', { delay: 40 });
    await page.keyboard.press('Enter');
    await page.waitForTimeout(3500);
  });
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(600);

  await step('myfeed-phone', async () => {
    await page.goto(base.replace(/\/$/, '') + '/myfeed', { waitUntil: 'load' });
    await page.waitForSelector('.item-row', { timeout: 180000 });
    await page.waitForTimeout(4000);
  });

  // ---- Contrast ----
  //
  // The settings surface, tab by tab. On a phone it replaces the panes rather
  // than sharing the window, which is the whole reason these are worth their own
  // pictures.
  await theme(page, 'contrast');
  for (const [tab, name] of [
    ['appearance', 'appearance-phone'],
    ['listening', 'listening-phone'],
    ['podcast', 'fluxcast-phone'],
    ['discover', 'discover-phone'],
    ['server', 'server-phone'],
  ]) {
    await step(name, async () => {
      await openSettings(page);
      await page.locator(`[data-action='settings-tab'][data-value='${tab}']`).click();
      await page.waitForTimeout(1800);
    });
    await page.keyboard.press('Escape').catch(() => {});
    await page.waitForTimeout(600);
  }

  // Put the account's theme back. The rotation above is a photographic device,
  // not a preference change, and whoever ran this was reading in something when
  // they started.
  //
  // Skipped when unset rather than guessed at: this script cannot know what the
  // theme was before its first switch, and writing a default here would be it
  // choosing one.
  if (process.env.RESTORE_THEME) {
    await theme(page, process.env.RESTORE_THEME);
    console.log('restored theme:', process.env.RESTORE_THEME);
  }

  await ctx.close();
}

await browser.close();
console.log('wrote', out);
