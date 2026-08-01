import { test, expect, boot, openRail, openStream } from './fixtures.mjs';

/**
 * The rail's Uncategorised row: articles the classifier gave no label.
 *
 * # What it is not
 *
 * The rail already had a row that looked like this one and is a different
 * thing entirely. "Unfiled" is a FEED nobody put in a folder, and its articles
 * are classified normally — a reader who opens it expecting the unlabelled
 * pile gets a list full of category chips, which is exactly the report this
 * row exists to answer. Unfiled is about sources; this is about articles.
 *
 * # What this test can and cannot prove
 *
 * The MEMBERSHIP rule — an article belongs here when no label cleared its
 * floor, and a Smart+ verdict wins outright — is pinned in
 * `internal/store/categorybrowse_test.go`, and was checked against the real
 * 132MB database: 200 listed items, none carrying a resolved label. It cannot
 * be pinned here, because the e2e seed never runs classification: in this
 * fixture EVERY item is unlabelled, so a filter that did nothing at all would
 * return the same rows as one that works.
 *
 * What only a browser can prove is the wiring: that the row renders with a
 * count the server agreed to, that clicking it actually sends the flag rather
 * than falling back to "everything", and that the list it opens carries no
 * category chips. Those are the three places the feature can be broken while
 * every Go test still passes.
 */

const UNCAT = '[data-category-none]';

test.describe('uncategorised', () => {
  test('the row is in the rail, under the topics, with a count', async ({ page }) => {
    await boot(page);
    await openRail(page);

    const row = page.locator(UNCAT);
    await expect(row).toHaveCount(1);

    // Last, after the taxonomy: it is the leftover pile, not one more label.
    const rows = page.locator('.topic-row');
    const total = await rows.count();
    await expect(rows.nth(total - 1)).toHaveAttribute('data-category-none', '1');

    // The count comes from the server's own complement query. Greater than
    // zero is a real assertion in this fixture and not a tautology: it is what
    // fails if UnreadUncategorised stops being wired to the rail at all.
    const count = row.locator('.feed-count');
    await expect(count).toBeVisible();
    expect(parseInt((await count.textContent()).replace(/[^\d]/g, ''), 10))
      .toBeGreaterThan(0);

    // Its marker is hollow — every other row's dot carries its label's hue,
    // and this row is the absence of a label.
    await expect(row.locator('.topic-dot-none')).toHaveCount(1);
  });

  test('opening it lists articles, and none of them carry a category chip',
    async ({ page }) => {
      await boot(page);
      await openRail(page);

      await page.locator(UNCAT).click();
      await expect(page.locator('.item-row').first()).toBeVisible();

      // The header names the list, so a reader can tell which of the two
      // similar-sounding rows they landed on.
      await expect(page.locator('.pane-list')).toContainText('Uncategorised');

      // The point of the whole row. A chip here means the filter let a
      // labelled article through — the reported bug, in its visible form.
      expect(await page.locator('.item-row .cat-chip').count(),
        'an article in the uncategorised list carried a category chip').toBe(0);

      // And the row knows it is the current list, so the rail and the pane
      // cannot disagree about where the reader is.
      await expect(page.locator(UNCAT)).toHaveAttribute('aria-current', 'true');
    });

  test('leaving it clears the filter', async ({ page }) => {
    await boot(page);
    await openRail(page);

    await page.locator(UNCAT).click();
    // Wait for the list to actually arrive before reading the rail. On a phone
    // the rail is a view that slides away, and openRail's "is it visible?"
    // check answers yes mid-transition � so a fast assertion here leaves the
    // next navigation waiting on a rail that is already gone.
    await expect(page.locator('.item-row').first()).toBeVisible();
    await expect(page.locator(UNCAT)).toHaveAttribute('aria-current', 'true');

    // Exactly one row may claim to be the current list. "All articles" tested
    // every narrowing field on scope except three � FolderID, CategorySlug and
    // Uncategorised � so browsing a topic left two rows highlighted at once,
    // and a screen reader was told the wrong one.
    await expect(page.locator('.pane-rail [aria-current="true"]')).toHaveCount(1);

    // Back to a stream. On a phone the rail is a view that slides away, and
    // openRail's "is it visible?" check answers yes while it is still sliding
    // - so it returns a rail that is gone a frame later, and the next click
    // then waits out the whole timeout on a hidden button. A flat pause is
    // crude; the real fix belongs in the fixture, not in this test.
    await page.waitForTimeout(700);
    await openStream(page, 'All articles');
    await openRail(page);
    await expect(page.locator(UNCAT)).toHaveAttribute('aria-current', 'false');
  });
});

/**
 * The row that caused the report: "Unfiled", showing every article.
 *
 * Unfiled is a FEED with no folder, so its articles are classified normally
 * and its list is full of category chips. That is correct behaviour and it is
 * also unreadable in the state every reader starts in: with nothing filed, the
 * row holds EVERY feed, renders inside a band called CATEGORIES immediately
 * above the classification labels, and carries a count identical to All
 * articles. It looks exactly like a broken "articles with no category".
 *
 * So it now waits for a sibling. `client/view/panes_test.go`'s
 * TestUnfiledRowWaitsForSomethingToBeFiled pins all four branches of that rule
 * against a rendered rail; this pins the one that matters in a real browser
 * against the real seed, plus the invariant that made the row misleading in
 * the first place — a count in CATEGORIES equal to the whole library.
 */
test.describe('unfiled', () => {
  test('with nothing filed, the Unfiled row is not in the rail', async ({ page }) => {
    await boot(page);
    await openRail(page);

    // The fixture subscribes feeds and files none of them, which is the state
    // the bug lived in. Guard that, so this test cannot quietly start passing
    // because the seed changed and there is nothing unfiled left to hide.
    await expect(page.locator('.pane-rail [data-folder-id]')).toHaveCount(0);

    const all = page.locator('[data-source-id="__all__"] .feed-count');
    const total = (await all.textContent()).replace(/[^\d]/g, '');
    expect(parseInt(total, 10)).toBeGreaterThan(0);

    // Nothing between the CATEGORIES heading and the topics may claim the
    // whole library. A row that counts everything groups nothing, and next to
    // twenty-six real labels it reads as one more of them.
    const counts = await page.locator('.cat-row .feed-count').allTextContents();
    expect(counts.map((t) => t.replace(/[^\d]/g, '')),
      'a folder row is showing the whole library as if it were a category')
      .not.toContain(total);
  });
});
