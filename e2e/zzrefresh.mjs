import { chromium } from '@playwright/test';
const b = await chromium.launch();
const p = await b.newPage({ viewport: { width: 1280, height: 900 } });
await p.goto('http://127.0.0.1:9000', { waitUntil: 'domcontentloaded' });
await p.waitForSelector('.item-row', { timeout: 30000 });
await p.waitForTimeout(2000);
await p.locator('button[title="Discover"], [aria-label="Discover"]').first().click();
await p.waitForSelector('#discover-page', { timeout: 20000 });
await p.waitForTimeout(3000);
const t = p.locator('[data-discover-smartplus]');
console.log('smart+ pressed:', await t.getAttribute('aria-pressed'));
if ((await t.getAttribute('aria-pressed')) !== 'true') { await t.click(); await p.waitForTimeout(3000); }
console.log('smart+ now:', await t.getAttribute('aria-pressed'));
await p.locator('[data-discover-refresh]').click();
console.log('refresh pressed; waiting for the job...');
await p.waitForTimeout(25000);
console.log('cards:', await p.locator('.discover-card').count());
console.log('body:', (await p.locator('#discover-page').innerText()).split('\n').slice(0, 8).join(' | '));
await b.close();
