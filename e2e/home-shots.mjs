// Captures the screenshots the homepage uses, from the live dev server.
//
// Not part of the suite: it is a content tool, run by hand when the reader's
// chrome changes and the front door's pictures go stale.
//
//   node e2e/home-shots.mjs                    # against http://127.0.0.1:9000
//   AF_BASE=http://host:9000 node e2e/home-shots.mjs
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
async function ready(page, path = '/unread') {
  await page.goto(base.replace(/\/$/, '') + path, { waitUntil: 'load' });
  await page.waitForSelector('.item-row', { timeout: 90000 });
  await page.waitForTimeout(2500);
}

// ---------------------------------------------------------------- desktop ---
{
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 }, deviceScaleFactor: 2 });
  const page = await ctx.newPage();
  await ready(page);

  // 1. The three panes with an article open.
  await page.locator('.item-row').first().click();
  await page.waitForTimeout(3500);
  await shot(page, 'reader-desktop');

  // 2. The command palette, mid-query.
  await page.keyboard.press('Control+k');
  await page.waitForTimeout(600);
  await page.keyboard.type('ha', { delay: 60 });
  await page.waitForTimeout(900);
  await shot(page, 'palette');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(500);

  // 3. Search, over FTS5, with results.
  await page.keyboard.press('/');
  await page.waitForTimeout(400);
  await page.keyboard.type('battery', { delay: 40 });
  await page.keyboard.press('Enter');
  await page.waitForTimeout(3000);
  await shot(page, 'search');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(600);

  // 4. My Feed — the ranked stream, each row carrying its reason.
  //
  // Reloaded first: the search box still holds the query from the shot above,
  // and a picture of the ranked stream with somebody's search sitting in it is a
  // picture of two things at once.
  // Straight to the address rather than through the rail: the search box is part
  // of the saved view, so a reload restores the query from the shot above, and a
  // URL outranks the saved view at boot.
  await page.goto(base.replace(/\/$/, '') + '/myfeed', { waitUntil: 'load' });
  try {
    await page.waitForSelector('.item-row', { timeout: 90000 });
    await page.waitForTimeout(4500);
    await shot(page, 'myfeed');
  } catch { console.log('skip myfeed'); }

  // 5. Appearance — five theme cards, each drawn in its own colours.
  await page.keyboard.press(',');
  await page.waitForSelector('.set-tabs', { timeout: 20000 });
  await page.locator("[data-action='settings-tab'][data-value='appearance']").click();
  await page.waitForTimeout(1500);
  await shot(page, 'appearance');

  // 6. Server — what a self-hosted application owes its operator.
  await page.locator("[data-action='settings-tab'][data-value='server']").click();
  await page.waitForTimeout(2000);
  await shot(page, 'server');

  // 7. Listening — the tab that says what leaves the machine.
  await page.locator("[data-action='settings-tab'][data-value='listening']").click();
  await page.waitForTimeout(1200);
  await shot(page, 'listening');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  // 8. Focus mode — the reading pane takes the window.
  await ready(page);
  await page.locator('.item-row').first().click();
  await page.waitForTimeout(2500);
  await page.keyboard.press('w');
  await page.waitForTimeout(2000);
  await shot(page, 'focus');
  await page.keyboard.press('w');
  await page.waitForTimeout(1200);

  // 9. The shortcut sheet, as the reader draws it.
  await page.keyboard.press('?');
  await page.waitForTimeout(900);
  await shot(page, 'keys');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);

  // 11. Add a feed — named and filed at the moment of adding.
  try {
    await page.locator('.pane-rail').getByRole('button', { name: /Add a feed/i }).first().click();
    await page.waitForTimeout(1200);
    await shot(page, 'addfeed');
    await page.keyboard.press('Escape');
    await page.waitForTimeout(800);
  } catch { console.log('skip addfeed'); }

  // 10. The slideshow — the same queue, full screen.
  try {
    await page.keyboard.press('s');
    await page.waitForTimeout(3500);
    await shot(page, 'slideshow');
    await page.keyboard.press('Escape');
    await page.waitForTimeout(1000);
  } catch { console.log('skip slideshow'); }

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
  await shot(page, 'reader-phone');

  await step('phone-article', async () => {
    await page.locator('.item-row').first().click();
    await page.waitForTimeout(3000);
  });

  await step('focus-phone', async () => {
    await page.keyboard.press('w');
    await page.waitForTimeout(1800);
  });
  await page.keyboard.press('w').catch(() => {});
  await page.waitForTimeout(800);

  await step('keys-phone', async () => {
    await page.keyboard.press('?');
    await page.waitForTimeout(900);
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

  await step('search-phone', async () => {
    await page.keyboard.press('/');
    await page.waitForTimeout(400);
    await page.keyboard.type('battery', { delay: 40 });
    await page.keyboard.press('Enter');
    await page.waitForTimeout(3000);
  });

  await step('myfeed-phone', async () => {
    await page.goto(base.replace(/\/$/, '') + '/myfeed', { waitUntil: 'load' });
    await page.waitForSelector('.item-row', { timeout: 90000 });
    await page.waitForTimeout(4000);
  });

  await step('slideshow-phone', async () => {
    await page.keyboard.press('s');
    await page.waitForTimeout(3500);
  });
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(800);

  // The settings surface, tab by tab. On a phone it replaces the panes rather
  // than sharing the window, which is the whole reason these are worth their own
  // pictures.
  for (const [tab, name] of [['appearance', 'appearance-phone'], ['listening', 'listening-phone'], ['server', 'server-phone']]) {
    await step(name, async () => {
      await page.keyboard.press(',');
      await page.waitForSelector('.set-tabs', { timeout: 20000 });
      await page.locator(`[data-action='settings-tab'][data-value='${tab}']`).click();
      await page.waitForTimeout(1600);
    });
  }
  await page.keyboard.press('Escape').catch(() => {});
  await page.waitForTimeout(800);

  await step('addfeed-phone', async () => {
    await ready(page);
    const rail = page.locator('.pane-rail');
    if (!(await rail.isVisible())) {
      await page.getByRole('button', { name: /Feeds/ }).first().click();
      await page.waitForTimeout(900);
    }
    await page.getByRole('button', { name: /Add a feed/i }).first().click();
    await page.waitForTimeout(1400);
  });

  await ctx.close();
}

await browser.close();
console.log('wrote', out);
