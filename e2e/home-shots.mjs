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

async function ready(page) {
  await page.goto(base, { waitUntil: 'load' });
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
{
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true,
  });
  const page = await ctx.newPage();
  await ready(page);
  await shot(page, 'reader-phone');

  await page.locator('.item-row').first().click();
  await page.waitForTimeout(3000);
  await shot(page, 'phone-article');
  await ctx.close();
}

await browser.close();
console.log('wrote', out);
