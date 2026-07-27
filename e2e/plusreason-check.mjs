// Does a SMART+ row show the model's own reason for moving it?
//
// The data half is already proven: all 12 smart_plus rows carry a {"t":"smartplus"} reason
// and none of the 188 free rows do. What is unproven is the RENDER — every previous defect
// in this feature (chips clipped by a 96px row, a regex that rewrote a string literal into
// `d.stream.myFeed`) was invisible to the compiler and to the database, and visible only here.
import { chromium } from '@playwright/test';

const URL = 'http://127.0.0.1:9310';
const OUT = 'C:/Users/mreca/AppData/Local/Temp/claude/C--Users-mreca-Desktop/fcf99512-deb7-48d2-ad16-ec544c68ab03/scratchpad';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1560, height: 950 } });
page.on('pageerror', e => console.log('PAGE ERROR:', e.message));

await page.goto(URL, { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.feed-row', { timeout: 120000 });
console.log('client booted');

await page.locator('[data-source-id="__myfeed__"]').click();
await page.waitForTimeout(5000);

const rows = await page.locator('.item-row').count();
console.log('rows:', rows, ' smart+ marks:', await page.locator('.item-plus').count());

// The claim under test: a row wearing the badge shows the paid tier's OWN sentence.
// Reading the two together per row is the point — a badge beside a free-tier reason is
// exactly the defect that prompted this, and counting them separately would miss it.
const pairs = await page.locator('.item-row').evaluateAll(els => els.slice(0, 14).map(el => ({
    title: (el.querySelector('.item-title')?.textContent || '').trim().slice(0, 44),
    badge: !!el.querySelector('.item-plus'),
    why: (el.querySelector('.item-why')?.textContent || '').trim(),
    // Is the blurb actually on screen, or clipped out of the row's box?
    visible: (() => {
        const w = el.querySelector('.item-why');
        if (!w) return 'none';
        const r = w.getBoundingClientRect(), o = el.getBoundingClientRect();
        return (r.height > 0 && r.bottom <= o.bottom + 1) ? 'yes' : `CLIPPED (${Math.round(r.bottom - o.bottom)}px over)`;
    })(),
})));

let badged = 0, badgedWithPlusReason = 0;
for (const p of pairs) {
    if (p.badge) badged++;
    // whyMovedUp renders as "moved up:" ahead of the model's fragment.
    const led = /moved up/i.test(p.why);
    if (p.badge && led) badgedWithPlusReason++;
    console.log(`${p.badge ? 'SMART+' : '      '} ${p.visible.padEnd(9)} ${p.title}`);
    console.log(`         why: ${p.why || '(EMPTY)'}`);
}
console.log(`\nbadged rows: ${badged}, of which led by the paid tier's reason: ${badgedWithPlusReason}`);

await page.screenshot({ path: `${OUT}/plusreason.png` });
console.log('screenshot written');
await browser.close();
