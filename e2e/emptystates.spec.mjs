import { test, expect, boot, openStream } from './fixtures.mjs';

/**
 * The empty states, each scope's own copy.
 *
 * client/view/panes.go's `emptyList` used to route everything through one
 * generic "no articles" message; it is now a ten-way switch, one branch per
 * reason a list can be empty, because "add a feed" is nonsense advice on a
 * search that matched nothing and "press u" is nonsense on a search too. Only
 * a browser proves the RIGHT branch fires for the RIGHT scope — a unit test on
 * `emptyList` in isolation would happily pass with the switch's cases shuffled,
 * because each branch is independently well-formed.
 *
 * Five of the ten are covered here — Search, Read later, Liked, Notes and
 * Unread — because they are reachable from a fresh reset without extra setup
 * (Later/Liked/Notes are empty until something is saved/rated/annotated, and
 * Unread only needs mark-all-read). My Feed is not reachable empty against
 * this fixture set (cold-start still ranks the items that exist); Disliked has
 * no rail row by design (see panes.go's comment beside `specialRow`, and
 * en_streams.go); Tag and Category are only empty in states this suite has no
 * cheap way to construct (a tag or category stripped of every feed). Those four
 * are left to whoever next touches `emptyList` rather than forced here.
 */
test.describe('empty states', () => {
  test('a search that matches nothing says so, and how to recover', async ({ page }) => {
    await boot(page);
    await page.locator('[data-role="search"]').fill('zzznonexistentqueryzzz');
    await page.locator('[data-role="search"]').press('Enter');

    const empty = page.locator('.pane-list .empty');
    await expect(empty.locator('strong')).toHaveText('Nothing matched');
    await expect(empty).toContainText('Try fewer words, or clear the search box.');
  });

  test('Read later says nothing is saved, before anything is', async ({ page }) => {
    await boot(page);
    await openStream(page, 'Read later');

    const empty = page.locator('.pane-list .empty');
    await expect(empty.locator('strong')).toHaveText('Nothing saved for later');
    await expect(empty).toContainText('Press t on an article to put it here.');
  });

  test('Liked says nothing is liked, before anything is', async ({ page }) => {
    await boot(page);
    await openStream(page, 'Liked');

    const empty = page.locator('.pane-list .empty');
    await expect(empty.locator('strong')).toHaveText('Nothing liked yet');
    await expect(empty).toContainText('Press l on an article, or use ▲ Like.');
  });

  test('Notes says none are written, before any are', async ({ page }) => {
    await boot(page);
    await openStream(page, 'Notes');

    const empty = page.locator('.pane-list .empty');
    await expect(empty.locator('strong')).toHaveText('No notes yet');
    await expect(empty).toContainText('Open Note under an article and write one');
  });

  test('Unread says all caught up, once everything has been read via the u toggle', async ({ page }) => {
    await boot(page);
    await page.locator('[data-action="mark-all"]').click();
    await expect(page.locator('.banner')).toContainText(/Marked \d+ read/);

    // The TOGGLE route, not the rail's "Unread" stream row — see the paired
    // regression test below for why that distinction is load-bearing here.
    await page.keyboard.press('u');

    const empty = page.locator('.pane-list .empty');
    await expect(empty.locator('strong')).toHaveText('All caught up');
    await expect(empty).toContainText('Press u to show everything again.');
  });

  test('the rail\'s Unread STREAM shows the correct empty copy once caught up', async ({ page }) => {
    // Was a test.fail() (TODO testing-campaign Q3): client/view/panes.go's
    // `emptyList` used to switch on `p.unreadOnly` alone — the toggle set by
    // the 'u' key / `[data-action="toggle-unread"]` — and never checked the
    // rail's own "Unread" row, `p.sel.Unread` (client/view/reader.go, `case
    // streamUnread: a.pick(scope{..., Unread: true})`), even though the query
    // layer already ORs the two together when fetching. `emptyList` now reads
    // `case p.unreadOnly, p.sel.Unread:` — both fields reach "All caught up" —
    // with a comment on that case explaining the two fields have to agree
    // with the query's own OR. Confirmed by reading `client/view/panes.go`
    // 2026-07-31, not by a live Playwright run (ports 9400-9500 off limits
    // this session; worth a live confirmation pass before trusting this
    // fully).
    await boot(page);
    await page.locator('[data-action="mark-all"]').click();
    await expect(page.locator('.banner')).toContainText(/Marked \d+ read/);

    await openStream(page, 'Unread');

    const empty = page.locator('.pane-list .empty');
    await expect(empty.locator('strong')).toHaveText('All caught up');
    await expect(empty).toContainText('Press u to show everything again.');
  });
});
