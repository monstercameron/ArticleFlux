// The Smart+ opt-in, end to end, as a reader performs it.
//
// Three things are under test:
//
//  1. My Feed describes ITSELF — "N picks, ranked by what you read" — rather than borrowing
//     the unread stream's sentence, which said "3875 unread, newest first" beside a rail
//     badge reading 199. Two numbers for one list.
//  2. Flipping the toggle re-ranks. The preference always saved; nothing scheduled a
//     derivation, so the feed was identical afterwards for up to ninety seconds.
//  3. A promoted row states the MODEL's reason for moving it, not a free-tier reason
//     underneath a paid badge.
//
// Driven through the UI rather than the database because every defect this feature has had
// was invisible below the render: chips clipped by a row height, a regex that rewrote a
// string literal, a badge with no rationale beside it.
import { chromium } from '@playwright/test';

const URL = 'http://127.0.0.1:9310';
const OUT = 'C:/Users/mreca/AppData/Local/Temp/claude/C--Users-mreca-Desktop/fcf99512-deb7-48d2-ad16-ec544c68ab03/scratchpad';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1560, height: 950 } });
page.on('pageerror', e => console.log('PAGE ERROR:', e.message));

const openMyFeed = async () => {
    await page.locator('[data-source-id="__myfeed__"]').click();
    await page.waitForTimeout(3500);
};

// What the list says about itself, and what the rail's badge says. They describe the same
// list, so a disagreement is the bug regardless of which number is right.
const header = async () => ({
    sub: (await page.locator('.list-head').textContent() || '').replace(/\s+/g, ' ').trim().slice(0, 130),
    badge: (await page.locator('[data-source-id="__myfeed__"]').textContent().catch(() => '?') || '?')
        .replace(/\s+/g, ' ').trim(),
});

const rows = async () => page.locator('.item-row').evaluateAll(els => els.slice(0, 8).map(el => ({
    title: (el.querySelector('.item-title')?.textContent || '').trim().slice(0, 40),
    badge: !!el.querySelector('.item-plus'),
    why: (el.querySelector('.item-why')?.textContent || '').trim(),
})));

await page.goto(URL, { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.feed-row', { timeout: 120000 });
console.log('client booted');

// --- 1. the free-tier baseline ------------------------------------------------------
await openMyFeed();
const before = await header();
console.log(`\nBEFORE  rail: ${before.badge}`);
console.log(`        head: ${before.sub}`);
const rowsBefore = await rows();
console.log(`        smart+ badges: ${rowsBefore.filter(r => r.badge).length}`);
console.log(`        sample blurb: ${rowsBefore[0]?.why || '(none)'}`);
await page.screenshot({ path: `${OUT}/plus-before.png` });

// --- 2. opt in, through the control a reader would use -------------------------------
const toggle = page.locator('[data-action="smart-feed-plus"]');
if (await toggle.count() === 0) {
    // The switch lives on the Smart settings pane. open-settings is the gear in the list
    // head — the desktop route; tab-settings is the same destination on the narrow tab
    // bar, which is not clickable at this width. Matching on /setting/ instead picks up
    // the per-tag and per-feed gears, which open something else entirely.
    console.log('\nopening the settings pane');
    await page.locator('[data-action="open-settings"]').first().click();
    await page.waitForTimeout(1500);
    // Settings is tabbed; Smart is one of them and may need selecting.
    const smartTab = page.locator('[data-action^="set-tab"], .set-tab').filter({ hasText: /smart/i });
    if (await smartTab.count() > 0) {
        await smartTab.first().click();
        await page.waitForTimeout(1200);
    }
}
if (await toggle.count() === 0) {
    console.log('COULD NOT FIND the Smart+ toggle. Visible actions:');
    console.log(await page.locator('[data-action]').evaluateAll(
        els => [...new Set(els.map(e => e.getAttribute('data-action')))].join(', ')));
} else {
    await toggle.first().click();
    console.log('\ntoggled Smart+ ON');
}
await page.screenshot({ path: `${OUT}/plus-settings.png` });

// The derivation runs on the server and an LLM re-rank takes tens of seconds, so this waits
// for the RESULT rather than a fixed delay. Polling My Feed is what a reader does anyway.
let appeared = 0;
for (let i = 0; i < 20 && !appeared; i++) {
    await page.waitForTimeout(5000);
    await openMyFeed();
    appeared = (await rows()).filter(r => r.badge).length;
    console.log(`  poll ${i + 1}: ${appeared} badged`);
}

// --- 3. what the reader sees afterwards ----------------------------------------------
const after = await header();
console.log(`\nAFTER   rail: ${after.badge}`);
console.log(`        head: ${after.sub}`);
for (const r of await rows()) {
    console.log(`  ${r.badge ? 'SMART+' : '      '} ${r.title}`);
    console.log(`         why: ${r.why || '(EMPTY)'}`);
}
await page.screenshot({ path: `${OUT}/plus-after.png` });
console.log('\nscreenshots written');
await browser.close();
