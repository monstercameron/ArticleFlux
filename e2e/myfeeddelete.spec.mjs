import { test, expect, boot } from './fixtures.mjs';

/**
 * Deleting a My Feed contribution row, and resetting a whole category
 * (§18.9's correction UI, extended with an explicit delete/reset).
 *
 * Topics and entities need weeks of real reading behind them (§18.4's cold
 * start) — this fixture cannot produce those in one run, so client/view's
 * own render tests (myfeeddelete_test.go) carry that half. Feeds are
 * different: every subscribed feed gets a row the moment it has ANY opens
 * behind it, which /debug/derive-now can produce synchronously — so this is
 * the one category a browser can prove end to end: armed, confirmed,
 * persisted server-side, and undone by a category reset.
 */

async function openMyFeedWithData(page, baseURL) {
  await boot(page);
  // One open is enough signal for the deriver to give Alpha Journal a
  // non-zero score. Not a loop over every row: on a phone the list and the
  // article are exclusive views, so opening a second row would first need
  // navigating back to the list — extra flake this fixture does not need
  // when one read already produces a row to act on.
  await page.locator('.item-row').first().click();
  await expect(page.locator('.article[data-current="true"] h1')).toBeVisible();
  await page.request.post(`${baseURL}/debug/derive-now`);

  await page.keyboard.press(',');
  await page.locator('[data-action="settings-tab"][data-value="myfeed"]').click();
  await page.locator('[data-action="mf-refresh"]').click();
  await expect(page.locator('.mf-row').first()).toBeVisible({ timeout: 15_000 });
}

test.describe('My Feed row delete and category reset', () => {
  test('a feed row arms, confirms, and can be cancelled without acting', async ({ page, baseURL }) => {
    await openMyFeedWithData(page, baseURL);
    const row = page.locator('.mf-row', { hasText: 'Alpha Journal' });

    await row.locator('[data-action="mf-delete-arm"]').click();
    await expect(row.locator('[data-action="mf-delete-confirm"]')).toBeVisible();
    await expect(row.locator('[data-action="mf-delete-confirm"]')).toHaveAttribute('data-armed', 'true');

    // Cancel backs out — no write, the row goes back to a bare Remove.
    await row.locator('[data-action="mf-delete-cancel"]').click();
    await expect(row.locator('[data-action="mf-delete-arm"]')).toBeVisible();
    await expect(row.locator('.chip-off')).toHaveCount(0);
  });

  test('confirming removes the feed from the ranked pool, and reload keeps it', async ({ page, baseURL }) => {
    await openMyFeedWithData(page, baseURL);
    const row = page.locator('.mf-row', { hasText: 'Alpha Journal' });

    await row.locator('[data-action="mf-delete-arm"]').click();
    await row.locator('[data-action="mf-delete-confirm"]').click();

    await expect(row.locator('.chip-off')).toBeVisible({ timeout: 15_000 });
    // Back to a bare Remove, not stuck confirming a press that already landed.
    await expect(row.locator('[data-action="mf-delete-arm"]')).toBeVisible();

    // A server-side write, not a local flag — it survives a reload.
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await page.keyboard.press(',');
    await page.locator('[data-action="settings-tab"][data-value="myfeed"]').click();
    await expect(page.locator('.mf-row', { hasText: 'Alpha Journal' }).locator('.chip-off'))
      .toBeVisible({ timeout: 15_000 });
  });

  test('resetting the category restores every row it excluded, with its own confirm', async ({ page, baseURL }) => {
    await openMyFeedWithData(page, baseURL);
    const row = page.locator('.mf-row', { hasText: 'Alpha Journal' });

    await row.locator('[data-action="mf-delete-arm"]').click();
    await row.locator('[data-action="mf-delete-confirm"]').click();
    await expect(row.locator('.chip-off')).toBeVisible({ timeout: 15_000 });

    const resetBtn = page.locator('[data-action="mf-reset-arm"]');
    await resetBtn.click();
    await expect(page.locator('[data-action="mf-reset-confirm"]')).toBeVisible();
    await expect(page.locator('[role="alert"]', { hasText: /undoes every/ })).toBeVisible();

    await page.locator('[data-action="mf-reset-confirm"]').click();

    await expect(row.locator('.chip-off')).toHaveCount(0, { timeout: 15_000 });
    // Armed to a bare Reset again, not stuck confirming.
    await expect(page.locator('[data-action="mf-reset-arm"]')).toBeVisible();
  });
});
