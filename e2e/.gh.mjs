import { chromium } from '@playwright/test';
const browser = await chromium.launch();
const ctx = await browser.newContext();
const page = await ctx.newPage();
page.on('console', (m) => console.log('[console]', m.type(), m.text().slice(0, 220)));
page.on('pageerror', (e) => console.log('[pageerror]', e.message.slice(0, 220)));
page.on('requestfailed', (r) => console.log('[failed]', r.url().slice(0, 100), r.failure()?.errorText));
page.on('response', (r) => {
  if (r.status() >= 400 || /wasm|sw\.js|index/.test(r.url())) {
    console.log('[resp]', r.status(), r.url().slice(0, 110), r.headers()['content-type'] || '', r.headers()['content-encoding'] || '');
  }
});
await page.goto('https://monstercameron.github.io/ArticleFlux/', { waitUntil: 'networkidle', timeout: 90000 });
await page.waitForTimeout(6000);
console.log('[body]', (await page.locator('body').innerText()).slice(0, 300).replace(/\n+/g, ' | '));
const sw = await page.evaluate(async () => {
  const rs = await navigator.serviceWorker.getRegistrations();
  return rs.map((r) => ({ scope: r.scope, active: !!r.active, state: r.active?.state }));
});
console.log('[sw]', JSON.stringify(sw));
await browser.close();
