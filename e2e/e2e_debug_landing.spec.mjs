import { test, expect, boot } from './fixtures.mjs';

test('debug landing options', async ({ page }) => {
  await boot(page);
  await page.keyboard.press(',');
  const select = page.locator('select[aria-label="Landing view"]');
  await expect(select).toBeVisible();
  const opts = await select.locator('option').evaluateAll(els => els.map(e => ({v: e.value, t: e.textContent, sel: e.selected})));
  console.log(JSON.stringify(opts));
  await select.selectOption('myfeed');
  await page.waitForTimeout(1000);
  const opts2 = await select.locator('option').evaluateAll(els => els.map(e => ({v: e.value, t: e.textContent, sel: e.selected})));
  console.log('after select:', JSON.stringify(opts2));
});
