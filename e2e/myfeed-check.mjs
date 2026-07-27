import { chromium } from '@playwright/test';

const URL = 'http://127.0.0.1:9141';
const OUT = 'C:/Users/mreca/AppData/Local/Temp/claude/C--Users-mreca-Desktop/fcf99512-deb7-48d2-ad16-ec544c68ab03/scratchpad';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1560, height: 950 } });
page.on('pageerror', e => console.log('PAGE ERROR:', e.message));

await page.goto(URL, { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.feed-row', { timeout: 120000 });
console.log('client booted');

await page.locator('[data-source-id="__myfeed__"]').click();
await page.waitForTimeout(4500);

console.log('rows:', await page.locator('.item-row').count());
console.log('row height:', await page.locator('.item-row').first().evaluate(e => e.getBoundingClientRect().height));
const blurbs = await page.locator('.item-why').allTextContents();
console.log('blurbs found:', blurbs.length);
blurbs.slice(0, 6).forEach(b => console.log('   ·', b.trim()));
console.log('smart+ marks:', await page.locator('.item-plus').count());
console.log('learning band:', await page.locator('.list-learning').count());
await page.screenshot({ path: `${OUT}/myfeed.png` });
console.log('screenshot written');
await browser.close();
