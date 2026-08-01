import { test, expect, boot } from './fixtures.mjs';

/**
 * Discover (§18.7, M16): sites the reader does not follow yet.
 *
 * Mounted as a settings-style tab (Cam, 2026-08-01) rather than a route —
 * see client/view/discover.go's package doc. Opened the same way every
 * settings tab is: a chip in the list header's corner (data-action
 * "open-discover"), landing directly on the Discover tab rather than
 * whichever one was last open, the same "showSettings + settingsTabTo" chain
 * slideNeeds uses for the Podcast tab.
 *
 * # The whole page is gated behind Smart+ review (Cam, 2026-08-01, 2nd pass)
 *
 * With the toggle off (the fixture user's default — nobody has opted in),
 * Discover shows the gate message and never calls ListRecommendations at
 * all. Every test that needs the actual list has to flip the toggle first.
 *
 * # What this suite can and cannot prove
 *
 * The fixture user has no outlink/engagement history — building that
 * fixture is a subscribe-and-read pipeline this suite does not set up
 * elsewhere — so no candidate ever clears internal/recommend's health gate
 * here, and the reachable, honest state (once opted in) is the EMPTY one.
 * What this proves is real: the tab opens, is marked current, the gate
 * renders correctly when off, opting in loads the list for real, the empty
 * state renders (not a blank panel and not a stuck loading spinner), and
 * Refresh round-trips to the server without breaking the panel. Accept/Reject
 * against a real card are covered at the Go layer instead (internal/transport/
 * grpcsrv/reader_recommend_test.go's TestAcceptRecommendationSubscribesAndClearsIt /
 * TestRejectRecommendationIsPermanent), which drive the same RPCs this page
 * calls, end to end, against a real subscribe.
 */

async function openDiscover(page) {
  await boot(page);
  await page.locator('[data-action="open-discover"]').first().click();
  await expect(page.locator('.set-tabs')).toBeVisible();
  await expect(page.locator('[data-action="settings-tab"][data-value="discover"]'))
    .toHaveAttribute('aria-current', 'true');
}

// Opens Discover and turns Smart+ review on, waiting for the real
// ListRecommendations round trip the toggle triggers to settle.
async function openDiscoverOptedIn(page) {
  await openDiscover(page);
  await expect(page.locator('.discover-gate')).toBeVisible();
  await page.locator('.discover-smartplus').click();
  await expect(page.locator('.discover-status')).toHaveCount(0, { timeout: 15_000 });
}

test.describe('discover', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('opens with Smart+ review off and shows the gate, not the list', async ({ page }) => {
    await openDiscover(page);

    await expect(page.locator('.discover-gate')).toBeVisible();
    await expect(page.locator('.discover-smartplus')).toHaveAttribute('aria-pressed', 'false');
    await expect(page.locator('.discover-card')).toHaveCount(0);
    // Refresh is disabled while gated — pressing it would be a no-op against
    // a list that was never loaded.
    await expect(page.locator('.discover-refresh')).toBeDisabled();

    // The panel is not blank: real copy is on screen, not just an empty shell.
    await expect(page.locator('.discover-title')).toHaveText(/discover/i);
    await expect(page.locator('.discover-gate')).not.toBeEmpty();
  });

  test('turning Smart+ review on loads the real list', async ({ page }) => {
    await openDiscoverOptedIn(page);

    await expect(page.locator('.discover-smartplus')).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('.discover-gate')).toHaveCount(0);
    await expect(page.locator('.discover-empty')).toBeVisible();
    await expect(page.locator('.discover-card')).toHaveCount(0);
    await expect(page.locator('.discover-refresh')).toBeEnabled();
  });

  test('turning it back off clears the list and restores the gate', async ({ page }) => {
    await openDiscoverOptedIn(page);
    await expect(page.locator('.discover-empty')).toBeVisible();

    await page.locator('.discover-smartplus').click();

    await expect(page.locator('.discover-gate')).toBeVisible();
    await expect(page.locator('.discover-smartplus')).toHaveAttribute('aria-pressed', 'false');
    await expect(page.locator('.discover-refresh')).toBeDisabled();
  });

  test('Refresh round-trips without breaking the panel', async ({ page }) => {
    await openDiscoverOptedIn(page);
    await expect(page.locator('.discover-empty')).toBeVisible();

    const refresh = page.locator('.discover-refresh');
    await refresh.click();
    // refreshing.Get() disables the button while the job is enqueuing, but the
    // round trip against a local fixture server is fast enough that the
    // disabled window can close before an assertion's poll interval catches
    // it — the button re-enabling is what actually matters here.
    await expect(refresh).toBeEnabled({ timeout: 15_000 });

    // No card exists to score yet (see the suite doc above), so the honest
    // outcome of a refresh here is still the empty state — not an error banner.
    await expect(page.locator('.discover-status-error')).toHaveCount(0);
    await expect(page.locator('.discover-empty')).toBeVisible();
  });

  test('screenshot: legibility check', async ({ page }) => {
    await openDiscoverOptedIn(page);
    await expect(page.locator('.discover-empty')).toBeVisible();
    await page.screenshot({ path: 'discover-shot.png' });
  });
});
