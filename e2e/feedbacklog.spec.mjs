import { test, expect, boot, openRail } from './fixtures.mjs';

/**
 * A feed's "Clear the backlog" setting, from the panel to the badge.
 *
 * # What this is guarding
 *
 * The window is a per-subscription preference that the SERVER applies: choosing
 * one marks the feed's older unread articles read straight away, and the poller
 * keeps doing it afterwards. The store tests prove which rows move
 * (internal/store/backlog_test.go: starred and other readers untouched, counters
 * agreeing with a recount). What only a browser can prove is the half in the
 * middle — that the press reaches the server as a patch, that the reply and the
 * sidebar badge both show the smaller pile at once, that the choice is still the
 * pressed one when the panel is reopened, and that it touched only this feed.
 *
 * The fixture feeds are dated July 2026, so every article in them is far older
 * than the shortest window and "a day" clears the whole of Alpha. The
 * setting persists on the shared server, and a sweep would keep clearing Alpha
 * for every spec that runs after this one, so the test puts it back to "never"
 * whatever happens.
 */

const ALPHA = 'Alpha Journal';
const BETA = 'Beta Notes';

/**
 * unreadOn reads a sidebar row's unread badge, or 0 when it has none.
 *
 * In place, without bringing the rail on screen: a badge's text is in the DOM
 * whichever pane is up (markallscope.spec.mjs explains what going through the
 * rail cost on a phone).
 */
async function unreadOn(page, name) {
  const row = page.locator('.pane-rail .feed-row:not(.stream-row)')
    .filter({ has: page.locator('.feed-name', { hasText: name }) }).first();
  const badge = row.locator('.feed-count');
  if ((await badge.count()) === 0) return 0;
  const text = (await badge.first().textContent()) || '';
  const n = parseInt(text.replace(/[^\d]/g, ''), 10);
  return Number.isNaN(n) ? 0 : n;
}

/** openFeedSettings opens a feed's panel by its gear, as dialogs.spec.mjs does. */
async function openFeedSettings(page, name) {
  const rail = await openRail(page);
  const slot = rail.locator('.feed-slot', { hasText: name }).first();
  // force:true: the gear is opacity:0 until hover on a desktop viewport, which
  // is the design and not what this spec is about.
  await slot.hover();
  await slot.locator('[data-action="feed-settings"]').click({ force: true });
  const fs = page.getByRole('dialog', { name: 'Feed settings' });
  await expect(fs).toBeVisible({ timeout: 20_000 });
  return fs;
}

/** backlogRow is the "Clear the backlog" row inside an open panel. */
function backlogRow(fs) {
  return fs.locator('.fs-row').filter({ hasText: 'Clear the backlog' });
}

function choice(fs, days) {
  return backlogRow(fs).locator(`[data-action="fs-backlog"][data-value="${days}"]`);
}

test.describe('clear the backlog', () => {
  test('the row offers never and four windows, with never in force by default', async ({ page }) => {
    await boot(page);
    const fs = await openFeedSettings(page, ALPHA);
    const row = backlogRow(fs);
    await expect(row).toBeVisible();

    await expect(row.locator('[data-action="fs-backlog"]')).toHaveCount(5);
    for (const [days, label] of [[0, 'never'], [1, 'a day'], [3, '3 days'],
      [7, 'a week'], [30, 'a month']]) {
      await expect(choice(fs, days)).toHaveText(label);
    }
    await expect(choice(fs, 0)).toHaveAttribute('aria-pressed', 'true');
    await expect(choice(fs, 1)).toHaveAttribute('aria-pressed', 'false');

    // The one-press "clear it now" is the existing Mark all read, and the row
    // says so rather than growing a second button that does the same thing.
    await expect(row).toContainText('Mark all read, below');
    await expect(fs.locator('[data-action="fs-markall"]')).toHaveCount(1);
  });

  test('choosing a window clears that feed now, keeps the choice, and leaves other feeds alone', async ({ page }) => {
    await boot(page);

    const alphaBefore = await unreadOn(page, ALPHA);
    const betaBefore = await unreadOn(page, BETA);
    expect(alphaBefore, 'the fixture must give Alpha unread articles to clear').toBeGreaterThan(0);
    expect(betaBefore, 'the fixture must give Beta unread articles to protect').toBeGreaterThan(0);

    let fs = await openFeedSettings(page, ALPHA);
    try {
      await choice(fs, 1).click();

      // Pressed once the save lands, and the badge falls with no poll and no
      // reload in between: the server applied the window as it stored it.
      await expect(choice(fs, 1)).toHaveAttribute('aria-pressed', 'true', { timeout: 20_000 });
      await expect(choice(fs, 0)).toHaveAttribute('aria-pressed', 'false');
      await expect.poll(() => unreadOn(page, ALPHA), { timeout: 20_000 }).toBe(0);
      expect(await unreadOn(page, BETA),
        'a window set on one feed reached another feed').toBe(betaBefore);

      // The choice is the server's, not the panel's: close, load the app afresh, reopen.
      await page.keyboard.press('Escape');
      await boot(page);
      fs = await openFeedSettings(page, ALPHA);
      await expect(choice(fs, 1)).toHaveAttribute('aria-pressed', 'true', { timeout: 20_000 });
    } finally {
      // Back to never, for every spec that follows (see the file comment).
      if (!(await fs.isVisible().catch(() => false))) {
        fs = await openFeedSettings(page, ALPHA);
      }
      await choice(fs, 0).click();
      await expect(choice(fs, 0)).toHaveAttribute('aria-pressed', 'true', { timeout: 20_000 });
    }
  });
});
