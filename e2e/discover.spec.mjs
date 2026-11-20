import { test, expect, boot, storeFakeKey } from './fixtures.mjs';

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
  // Smart+ review is offered only where the server has a key to run it with, and
  // the harness has none — so these specs store a placeholder first.
  await storeFakeKey(page);
  await page.locator('[data-action="settings-tab"][data-value="discover"]').click();
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

  // The harness instance has no model key. Offering a switch that cannot do
  // anything, over a page that only works behind it, is the page lying about
  // what it can do — so it says why instead, and offers nothing to press.
  test('with no key on the server it offers no Smart+ review, and says why', async ({ page }) => {
    await boot(page);
    await page.locator('[data-action="open-discover"]').first().click();
    await expect(page.locator('#discover-page')).toBeVisible();
    await expect(page.locator('.discover-gate')).toContainText('no OpenAI key', { timeout: 30_000 });
    await expect(page.locator('.discover-smartplus')).toHaveCount(0);
    await expect(page.locator('.discover-refresh')).toHaveCount(0);
    // One heading style across the settings tabs: the group heading, not a title
    // of its own.
    await expect(page.locator('.discover-head .fs-group-title')).toHaveText(/discover/i);
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
    await expect(page.locator('.discover-head .fs-group-title')).toHaveText(/discover/i);
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

  // --- Not for me, and the way back (G26) -------------------------------------
  //
  // The harness has no model key and no outlink evidence, so the real scoring
  // pass never makes a card. /debug/seed-recommendations (DevMode only, beside
  // reset-state) writes open suggestions straight into the account, which is what
  // lets the card interactions below run in a browser against the real server.
  // Dismissals are permanent and survive reset-state, so every test names its own
  // domains; the open ones are withdrawn again afterwards so no later spec sees
  // them.
  const unique = () => Math.random().toString(36).slice(2, 8);
  const seed = (page, baseURL, domains) =>
    page.request.post(`${baseURL}/debug/seed-recommendations?` +
      domains.map((d) => `d=${encodeURIComponent(d)}`).join('&'));
  const card = (page, domain) => page.locator('.discover-card', { hasText: domain });
  const banner = (page) => page.locator('#discover-page .banner');

  test.describe('Not for me', () => {
    test.afterEach(async ({ page, baseURL }) => {
      await seed(page, baseURL, []);
    });

    // The server was never told, which is the whole of what Undo is: a reload
    // after it shows the card still offered.
    test('can be taken back, and the card returns where it was', async ({ page, baseURL }) => {
      const a = `a-${unique()}.example`, b = `b-${unique()}.example`, c = `c-${unique()}.example`;
      await boot(page);
      await seed(page, baseURL, [a, b, c]);
      await openDiscoverOptedIn(page);
      await expect(page.locator('.discover-card')).toHaveCount(3);

      await card(page, b).locator('.discover-reject').click();
      await expect(card(page, b)).toHaveCount(0);
      await expect(page.locator('.discover-card')).toHaveCount(2);
      await expect(banner(page)).toContainText('Dismissed');
      await expect(banner(page)).toContainText(b);

      await banner(page).locator('[data-discover-undo]').click();
      await expect(page.locator('.discover-card')).toHaveCount(3);
      // In place: the middle of the three, not the end of the list.
      await expect(page.locator('.discover-card-domain').nth(1)).toContainText(b);
      await expect(banner(page)).toHaveCount(0);
      await expect(page.locator('.discover-said')).toContainText('back');
      // The Undo that was pressed is gone; focus went to the card it brought back.
      await expect.poll(() => page.evaluate(() => document.activeElement?.dataset?.role ?? ''))
        .toBe(`rec-follow-${b}`);

      await page.reload();
      await expect(page.locator('.discover-card')).toHaveCount(3, { timeout: 30_000 });
    });

    test('is sent when the window runs out, and survives a reload', async ({ page, baseURL }) => {
      const a = `a-${unique()}.example`, b = `b-${unique()}.example`;
      await boot(page);
      await seed(page, baseURL, [a, b]);
      await openDiscoverOptedIn(page);
      await card(page, a).locator('.discover-reject').click();
      await expect(banner(page)).toBeVisible();
      // Ten seconds of Undo, then the banner leaves and the server has the answer.
      await expect(banner(page)).toHaveCount(0, { timeout: 20_000 });
      await expect(page.locator('.discover-card')).toHaveCount(1);

      await page.reload();
      await expect(page.locator('.discover-card')).toHaveCount(1, { timeout: 30_000 });
      await expect(card(page, a)).toHaveCount(0);
      await expect(card(page, b)).toHaveCount(1);
    });

    test('is sent at once on leaving the tab, not lost with the timer', async ({ page, baseURL }) => {
      const a = `a-${unique()}.example`;
      await boot(page);
      await seed(page, baseURL, [a]);
      await openDiscoverOptedIn(page);
      await card(page, a).locator('.discover-reject').click();
      await expect(banner(page)).toBeVisible();

      await page.locator('[data-action="settings-tab"][data-value="appearance"]').click();
      await expect(page.locator('#discover-page')).toHaveCount(0);
      // The RPC leaves from the unmount; give it its round trip.
      await page.waitForTimeout(1500);
      await page.reload();
      await page.locator('[data-action="settings-tab"][data-value="discover"]').click();
      await expect(page.locator('.discover-empty')).toBeVisible({ timeout: 30_000 });
      await expect(card(page, a)).toHaveCount(0);
    });

    // Only the latest dismissal can be taken back: the one before it is final.
    test('dismissing a second card makes the first final', async ({ page, baseURL }) => {
      const a = `a-${unique()}.example`, b = `b-${unique()}.example`;
      await boot(page);
      await seed(page, baseURL, [a, b]);
      await openDiscoverOptedIn(page);
      await card(page, a).locator('.discover-reject').click();
      await expect(card(page, a)).toHaveCount(0);
      await card(page, b).locator('.discover-reject').click();
      await expect(page.locator('.discover-card')).toHaveCount(0);
      await expect(banner(page)).toContainText(b);

      await banner(page).locator('[data-discover-undo]').click();
      await expect(page.locator('.discover-card')).toHaveCount(1);
      await expect(card(page, b)).toHaveCount(1);

      await page.waitForTimeout(1500);
      await page.reload();
      await expect(page.locator('.discover-card')).toHaveCount(1, { timeout: 30_000 });
      await expect(card(page, a)).toHaveCount(0);
      await expect(card(page, b)).toHaveCount(1);
    });
  });

  test('screenshot: legibility check', async ({ page }) => {
    await openDiscoverOptedIn(page);
    await expect(page.locator('.discover-empty')).toBeVisible();
    await page.screenshot({ path: 'discover-shot.png' });
  });
});
