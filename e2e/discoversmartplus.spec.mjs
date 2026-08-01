import { test, expect, boot } from './fixtures.mjs';

/**
 * Discover's Smart+ review switch: it turns on, and it stays on.
 *
 * # The report
 *
 * "The discover page smart+ button can't be toggled on." It could — by clicking
 * the tab. What could not was the page you arrive at by RELOADING while Discover
 * is the open tab, which is what happens every time you come back to the app
 * with that tab remembered. Instrumented on the real instance: a Discover
 * mounted from a click ran its load effect; a Discover mounted from a reload ran
 * it ZERO times. The page sat on its spinner, and the switch never became
 * operable at all. Leaving the tab and returning — a remount — fixed it every
 * time, which is what made it look intermittent rather than broken.
 *
 * Two things came out of that and both are pinned below: the load no longer
 * depends on an effect firing, and the switch is disabled until the stored value
 * has actually been read, because a switch whose position is unknown must not be
 * operable — clicking one computes the opposite of a value nobody chose.
 */

const TOGGLE = '[data-discover-smartplus]';

async function openDiscover(page) {
  await page.locator('button[title="Discover"], [aria-label="Discover"]').first().click();
  await expect(page.locator('#discover-page')).toBeVisible();
}

/** settled waits for the stored value to have been read, which is what the
 *  switch becoming operable means. */
async function settled(page) {
  await expect(page.locator(TOGGLE)).toBeEnabled({ timeout: 30_000 });
}

test.describe('discover smart+ switch', () => {
  test('it becomes operable, and says so only once it knows', async ({ page }) => {
    await boot(page);
    await openDiscover(page);
    await settled(page);

    // A fresh instance has never been opted in, so the gate is up and the body
    // says what the switch is for rather than showing an empty list.
    await expect(page.locator(TOGGLE)).toHaveAttribute('aria-pressed', 'false');
    await expect(page.locator('.discover-gate')).toBeVisible();
  });

  test('switching it on opens the page and survives a reload', async ({ page }) => {
    await boot(page);
    await openDiscover(page);
    await settled(page);

    await page.locator(TOGGLE).click();
    await expect(page.locator(TOGGLE)).toHaveAttribute('aria-pressed', 'true');
    // The gate is what the switch gates. If it stays up, the toggle moved a
    // flag and nothing else.
    await expect(page.locator('.discover-gate')).toHaveCount(0);

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#discover-page')).toBeVisible({ timeout: 60_000 });
    await settled(page);
    await expect(page.locator(TOGGLE),
      'the opt-in did not survive a reload — this is the reported bug')
      .toHaveAttribute('aria-pressed', 'true');
  });

  test('a reload that lands on Discover is not stuck', async ({ page }) => {
    // The exact failure: mounted by a reload rather than by a click. Before the
    // fix the switch here stayed disabled forever and the body never left its
    // spinner, no matter how long you waited.
    await boot(page);
    await openDiscover(page);
    await settled(page);

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#discover-page')).toBeVisible({ timeout: 60_000 });
    await settled(page);
    await expect(page.locator('.discover-status')).toHaveCount(0);
  });

  test('switching it back off puts the gate back, and that survives too',
    async ({ page }) => {
      await boot(page);
      await openDiscover(page);
      await settled(page);

      if ((await page.locator(TOGGLE).getAttribute('aria-pressed')) !== 'true') {
        await page.locator(TOGGLE).click();
        await expect(page.locator(TOGGLE)).toHaveAttribute('aria-pressed', 'true');
      }

      await page.locator(TOGGLE).click();
      await expect(page.locator(TOGGLE)).toHaveAttribute('aria-pressed', 'false');
      await expect(page.locator('.discover-gate')).toBeVisible();

      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.locator('#discover-page')).toBeVisible({ timeout: 60_000 });
      await settled(page);
      await expect(page.locator(TOGGLE)).toHaveAttribute('aria-pressed', 'false');
    });
});
