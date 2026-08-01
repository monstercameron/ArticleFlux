import { test, expect, boot, openFeed, feedRow, openStream } from './fixtures.mjs';

/**
 * Mark all read reaches the list it was pressed on, and no further.
 *
 * # What this is guarding
 *
 * The control took one source id, so it could say "this feed" or "everything".
 * Every other list a reader can be on — My Feed, a tag, a category, Liked,
 * Disliked, Read later — sent an empty source id, which the server read as
 * "everything subscribed". Pressing Mark all read while looking at one list
 * marked all of them.
 *
 * `internal/transport/grpcsrv/markallscope_test.go` pins the server side per
 * scope. This is the half that only a browser can prove: that the CLIENT sends
 * the scope it is displaying, and that the confirmation stands between the
 * press and the damage. Both were wrong in ways the server tests cannot see —
 * the client resolved the scope in a second place that had drifted, and the
 * confirmation was two presses of the same chip in the same spot, which a
 * double-click went straight through.
 */

const ALPHA = 'Alpha Journal';
const BETA = 'Beta Notes';

/** unreadOn reads a sidebar row's unread badge, or 0 when it has none. */
async function unreadOn(page, name) {
  const row = await feedRow(page, name);
  const badge = row.locator('.feed-count');
  if ((await badge.count()) === 0) return 0;
  const text = (await badge.first().textContent()) || '';
  const n = parseInt(text.replace(/[^\d]/g, ''), 10);
  return Number.isNaN(n) ? 0 : n;
}

test.describe('mark all read scope', () => {
  test('marking one feed leaves the other feed alone', async ({ page }) => {
    await boot(page);

    const betaBefore = await unreadOn(page, BETA);
    expect(betaBefore, 'the fixture must give Beta unread items to protect').toBeGreaterThan(0);

    await openFeed(page, ALPHA);
    await page.locator('[data-action="mark-all-arm"]').click();
    await page.locator('[data-action="mark-all"]').click();
    await expect(page.locator('.banner').filter({ hasText: /Marked \d+ read/ }))
      .toBeVisible();

    expect(await unreadOn(page, ALPHA), 'the feed we marked should be clear').toBe(0);
    expect(await unreadOn(page, BETA),
      'marking one feed read reached another feed').toBe(betaBefore);
  });

  /**
   * The scope that was most dangerous: everything on My Feed is unread by
   * construction, so it is the list a reader is most likely to press this on,
   * and it sent an empty source id — the "everything" case.
   *
   * The assertion is deliberately about what SURVIVES rather than about My
   * Feed emptying: what the ranking holds depends on what the deriver has had
   * time to rank, but "some unread remains outside the ranked set" is true for
   * this fixture either way, and it is precisely what the bug destroyed.
   */
  test('marking My Feed does not empty every feed', async ({ page }) => {
    await boot(page);

    const alphaBefore = await unreadOn(page, ALPHA);
    const betaBefore = await unreadOn(page, BETA);
    const totalBefore = alphaBefore + betaBefore;
    expect(totalBefore, 'the fixture must have unread items to protect').toBeGreaterThan(0);

    await openStream(page, 'My Feed');
    await page.locator('[data-action="mark-all-arm"]').click();
    await page.locator('[data-action="mark-all"]').click();
    // The banner is the mark landing; without waiting for it the counts below
    // can be read before the sidebar refresh this triggers.
    await expect(page.locator('.banner').filter({ hasText: /Marked \d+ read/ }))
      .toBeVisible();

    const totalAfter = (await unreadOn(page, ALPHA)) + (await unreadOn(page, BETA));
    expect(totalAfter,
      'Mark all read on My Feed marked every subscribed feed read — it must only ' +
      'reach what the ranking actually holds').toBeGreaterThan(0);
  });

  test('the confirmation names the list, and cancelling marks nothing', async ({ page }) => {
    await boot(page);
    await openFeed(page, ALPHA);
    const before = await unreadOn(page, ALPHA);
    expect(before).toBeGreaterThan(0);

    await page.locator('[data-action="mark-all-arm"]').click();

    // The confirmation says WHICH list, which is the question the scope bug
    // made urgent: a reader could not tell what the press was about to reach.
    const confirm = page.locator('.banner').filter({ hasText: 'Mark everything in' });
    await expect(confirm).toBeVisible();
    await expect(confirm).toContainText(ALPHA);

    await page.locator('[data-action="mark-all-cancel"]').click();
    // Gone from the DOM, not merely faded: a closed banner-slot is opacity 0
    // with a zero-height row, so a confirmation left rendered inside one would
    // keep a destructive button reachable by keyboard and screen reader with
    // nothing on screen asking anything. See listHead's own comment.
    await expect(confirm).toHaveCount(0);
    await expect(page.locator('[data-action="mark-all"]')).toHaveCount(0);
    expect(await unreadOn(page, ALPHA), 'cancelling still marked the list read').toBe(before);
  });

  /**
   * The failure the previous confirmation design had: arm and confirm were the
   * same chip in the same place, so a double-click — a thing people do to
   * buttons — passed straight through the confirmation without it ever having
   * a frame in which it could be read.
   */
  test('double-clicking the chip does not mark anything read', async ({ page }) => {
    await boot(page);
    await openFeed(page, ALPHA);
    const before = await unreadOn(page, ALPHA);
    expect(before).toBeGreaterThan(0);

    await page.locator('[data-action="mark-all-arm"]').dblclick();

    // The confirmation is open and waiting, and nothing has been marked.
    await expect(page.locator('.banner').filter({ hasText: 'Mark everything in' }))
      .toBeVisible();
    expect(await unreadOn(page, ALPHA),
      'a double-click on the chip marked the list read without a confirmation ' +
      'the reader could act on').toBe(before);
  });

  /**
   * Search and Notes cannot be expressed as a filter the mark can honour, so
   * the control is absent rather than present-and-wrong. See reader.go's
   * selectorFor and listHead's ui.If.
   */
  test('the control is not offered on a search, where it could not honour the list', async ({ page }) => {
    await boot(page);
    await expect(page.locator('[data-action="mark-all-arm"]')).toBeVisible();

    await page.locator('[data-role="search"]').fill('the');
    await page.locator('[data-role="search"]').press('Enter');
    await expect(page.locator('.list-head')).toContainText('Matching your search');

    await expect(page.locator('[data-action="mark-all-arm"]'),
      'Mark all read on a search would mark something other than the search results')
      .toHaveCount(0);
  });
});
