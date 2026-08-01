import { chromium } from '@playwright/test';
const OUT = 'C:/Users/mreca/AppData/Local/Temp/claude/C--Users-mreca-Desktop/696a819b-d99e-4e0e-ad3a-f7ab280ce635/scratchpad';
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 900, height: 400 } });
await page.goto('http://127.0.0.1:9000', { waitUntil: 'domcontentloaded' });
await page.waitForSelector('.feed-row, .item-row', { timeout: 60000 }).catch(() => {});
await page.waitForTimeout(1200);
const refreshCount = await page.locator('[data-action="refresh"]').count();
console.log('refresh chip count in served build:', refreshCount);
await page.locator('.list-tools').screenshot({ path: `${OUT}/toolbar-check.png` }).catch(async () => {
  await page.screenshot({ path: `${OUT}/toolbar-check.png` });
});
await browser.close();
