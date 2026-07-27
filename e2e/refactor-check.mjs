// Exercises the three sections extracted out of reader.go, in a real browser.
//
// The package compiling proves the refactor is type-correct; it proves nothing about
// BEHAVIOUR. What could still be wrong after moving 1,900 lines of closures and effects is
// a hook that no longer registers (positional order) or a rewritten identifier that now
// reads the wrong handle — and both of those look like "a control quietly stopped working",
// which no compiler reports.
//
// So: click something (delegated clicks), press a key (the keyboard map), and open the
// add-a-feed dialog (its wiring). Those are the three files.
import { chromium } from '@playwright/test';

const URL = 'http://127.0.0.1:9147';
const OUT = 'C:/Users/mreca/AppData/Local/Temp/claude/C--Users-mreca-Desktop/fcf99512-deb7-48d2-ad16-ec544c68ab03/scratchpad';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1560, height: 950 } });
const errs = [];
page.on('pageerror', e => errs.push(e.message));

await page.goto(URL, { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.feed-row', { timeout: 120000 });
console.log('booted');

// --- reader_clicks.go: the delegated data-source-id listener -----------------
await page.locator('[data-source-id="__myfeed__"]').click();
await page.waitForTimeout(3500);
const title = (await page.locator('.list-title').first().innerText().catch(() => '')).trim();
console.log('clicked My Feed -> list title:', JSON.stringify(title));

// --- reader_clicks.go: the delegated data-item-id listener ------------------
const before = await page.locator('.article').count();
await page.locator('.item-row').nth(1).click();
await page.waitForTimeout(2500);
console.log('clicked an item -> articles mounted:', before, '->', await page.locator('.article').count());

// --- reader_keyboard.go: j moves the cursor, ? opens help -------------------
const cursorBefore = await page.locator('.list-scroll').first()
  .evaluate(e => e.style.getPropertyValue('--cursor') || getComputedStyle(e).getPropertyValue('--cursor'));
await page.keyboard.press('j');
await page.waitForTimeout(1200);
const cursorAfter = await page.locator('.list-scroll').first()
  .evaluate(e => e.style.getPropertyValue('--cursor') || getComputedStyle(e).getPropertyValue('--cursor'));
console.log('pressed j -> cursor', JSON.stringify(cursorBefore), '->', JSON.stringify(cursorAfter));

await page.keyboard.press('Control+k');
await page.waitForTimeout(900);
console.log('Ctrl+K -> palette open:', await page.locator('[data-role="palette"]').count());
await page.keyboard.press('Escape');
await page.waitForTimeout(500);

// --- reader_addfeed_wire.go: openAddFeed ------------------------------------
await page.locator('[data-action="add-feed-open"], [data-action="add-feed"], .rail-add').first().click();
await page.waitForTimeout(1200);
console.log('add-a-feed dialog fields:', await page.locator('[data-role="add-feed"]').count());
await page.keyboard.press('Escape');
await page.waitForTimeout(500);
console.log('after Escape, dialog fields:', await page.locator('[data-role="add-feed"]').count());

console.log('page errors:', errs.length ? errs : 'none');
await page.screenshot({ path: `${OUT}/refactor.png` });
await browser.close();
