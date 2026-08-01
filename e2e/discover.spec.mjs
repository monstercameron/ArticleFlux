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
 * # What this suite can and cannot prove
 *
 * The fixture user has no outlink/engagement history — building that
 * fixture is a subscribe-and-read pipeline this suite does not set up
 * elsewhere — so no candidate ever clears internal/recommend's health gate
 * here, and the reachable, honest state is the EMPTY one. What this proves is
 * real: the tab opens, is marked current, the empty state renders (not a
 * blank panel and not a stuck loading spinner), and Refresh round-trips to
 * the server without breaking the panel. Accept/Reject against a real card
 * are covered at the Go layer instead (internal/transport/grpcsrv/
 * reader_recommend_test.go's TestAcceptRecommendationSubscribesAndClearsIt /
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

test.describe('discover', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('opens directly on the Discover tab and settles on the empty state', async ({ page }) => {
    await openDiscover(page);

    // Not stuck on "Loading…" — the RPC round-trips and the panel settles.
    await expect(page.locator('.discover-status')).toHaveCount(0, { timeout: 15_000 });
    await expect(page.locator('.discover-empty')).toBeVisible();
    await expect(page.locator('.discover-card')).toHaveCount(0);

    // The panel is not blank: real copy is on screen, not just an empty shell.
    await expect(page.locator('.discover-title')).toHaveText(/discover/i);
    await expect(page.locator('.discover-empty')).not.toBeEmpty();
  });

  test('Refresh round-trips without breaking the panel', async ({ page }) => {
    await openDiscover(page);
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
    await openDiscover(page);
    // The first ListRecommendations round trip over a freshly-opened tunnel
    // can take several seconds on a loaded machine — the same generous
    // timeout the settle test above uses, not a fixed short sleep, which is
    // what made this shot flaky (a 1s wait was not always enough).
    await expect(page.locator('.discover-status')).toHaveCount(0, { timeout: 15_000 });
    await expect(page.locator('.discover-empty')).toBeVisible();
    await page.screenshot({ path: 'discover-shot.png' });
  });
});
