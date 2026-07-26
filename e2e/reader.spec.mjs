import { test, expect, boot, openFeed, feedRow, openRail } from './fixtures.mjs';

/**
 * The reading loop, end to end.
 *
 * Every assertion here crosses the whole stack — wasm renders it, the gRPC
 * tunnel carried it, SQLite stored it. That is deliberate: the unit suites
 * already cover parsing, normalisation and the repository in isolation, so
 * anything these tests add has to be about the wiring between them.
 */


test.describe('reading', () => {
  test('loads feeds and items from the server', async ({ page }) => {
    await boot(page);

    // Both fixture feeds ingested, with the titles the publishers declared
    // rather than the URLs we subscribed to. The sidebar is off-screen on a
    // phone, so this asks for it rather than assuming it.
    await expect(await feedRow(page, /Alpha Journal/)).toBeVisible();
    await expect(await feedRow(page, /Beta Notes/)).toBeVisible();

    // 3 items from alpha + 2 from beta.
    await expect(page.locator('.item-row')).toHaveCount(5);

    // Sorted newest first: beta-1 is 2026-07-26T10:00Z, alpha-1 is 12:00Z.
    await expect(page.locator('.item-title').first())
      .toHaveText(/Speculative decoding/);
  });

  test('opening an article shows its body and marks it read', async ({ page }) => {
    await boot(page);

    const row = page.locator('.item-row').first();
    await expect(row).toHaveAttribute('data-read', 'false');

    await row.click();

    // The body arrives from GetItem, not from the list payload — list responses
    // deliberately omit content so a 50-item page is not megabytes of unread text.
    await expect(page.locator('.article h1')).toHaveText(/Speculative decoding/);
    await expect(page.locator('.article-body')).toContainText('n-gram table');

    // Marking read is optimistic; it must also survive a reload, which is what
    // proves the write actually reached SQLite.
    await expect(row).toHaveAttribute('data-read', 'true');
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').first()).toHaveAttribute('data-read', 'true');
  });

  test('feed HTML is rendered, not escaped, and not executed', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();
    await expect(page.locator('.article-body strong')).toHaveText('proposals');

    // The sanitizer is the highest-stakes thing in the app: this is third-party
    // markup rendered inside our origin.
    await expect(page.locator('.article-body script')).toHaveCount(0);
  });

  test('starring persists across a reload', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();

    // Scoped to the article pane. A bare /Star/ also matches the sidebar's
    // "Starred" stream, which is ambiguous the moment the rail exists.
    const article = page.locator('.pane-article');
    await article.locator('[data-action="star"]').click();
    await expect(article.locator('[data-action="star"]')).toHaveText('★ Starred');

    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await openFeed(page, 'Starred');
    await expect(page.locator('.item-row')).toHaveCount(1);
  });

  test('selecting a feed filters the list', async ({ page }) => {
    await boot(page);
    await openFeed(page, /Beta Notes/);

    await expect(page.locator('.item-row')).toHaveCount(2);
    for (const src of await page.locator('.item-source').allTextContents()) {
      expect(src).toBe('Beta Notes');
    }
  });

  test('search runs against FTS5', async ({ page }) => {
    await boot(page);

    await page.locator('[data-role="search"]').fill('sourdough');
    await page.locator('[data-role="search"]').press('Enter');

    await expect(page.locator('.item-row')).toHaveCount(1);
    await expect(page.locator('.item-title')).toHaveText(/Sourdough hydration/);

    // Porter stemming, which is the tokenizer §18.2's term affinity depends on:
    // "lock" must reach "locks" in the corpus.
    await page.locator('[data-role="search"]').fill('lock');
    await page.locator('[data-role="search"]').press('Enter');
    await expect(page.locator('.item-title').first()).toHaveText(/lock/i);
  });

  test('unread-only hides what has been read', async ({ page }) => {
    await boot(page);
    const before = await page.locator('.item-row').count();

    await page.locator('.item-row').first().click();
    await page.locator('[data-action="toggle-unread"]').click();

    await expect(page.locator('.item-row')).toHaveCount(before - 1);
  });

  test('mark all read empties the unread view and zeroes the counts', async ({ page }) => {
    await boot(page);
    await page.locator('[data-action="mark-all"]').click();

    await expect(page.locator('.banner')).toContainText(/Marked \d+ read/);
    await expect(page.locator('.feed-count')).toHaveCount(0);
  });
});

test.describe('keyboard', () => {
  // Google Reader's map, unchanged. Muscle memory transfers on day one and
  // renaming these would throw that away for nothing.
  test('j and k move through the list, s stars', async ({ page }) => {
    await boot(page);

    await page.locator('body').press('j');
    await expect(page.locator('.article h1')).toBeVisible();
    const first = await page.locator('.article h1').textContent();

    await page.locator('body').press('j');
    const second = await page.locator('.article h1').textContent();
    expect(second).not.toBe(first);

    await page.locator('body').press('k');
    await expect(page.locator('.article h1')).toHaveText(first);

    await page.locator('body').press('s');
    await expect(page.locator('.pane-article [data-action="star"]')).toHaveText('★ Starred');
  });

  test('shortcuts do not fire while typing', async ({ page }) => {
    await boot(page);
    const search = page.locator('[data-role="search"]');
    await search.fill('jjjkkkss');

    // If the handler had fired, an article would have opened.
    await expect(search).toHaveValue('jjjkkkss');
    await expect(page.locator('.article h1')).toHaveCount(0);
  });
});

test.describe('errors and edges', () => {
  test('a bad feed URL reports rather than failing silently', async ({ page }) => {
    await boot(page);
    await openRail(page);
    await page.locator('[data-role="add-feed"]').fill('not-a-url');
    await page.locator('[data-action="add-feed"]').click();

    await expect(page.locator('.banner')).toContainText(/Couldn't add that feed/);
  });

  test('the SSRF guard refuses an internal address from the UI', async ({ page }) => {
    await boot(page);
    // The one attack the whole netguard package exists for, exercised through
    // the feature that carries it: subscribe-by-URL.
    await openRail(page);
    await page.locator('[data-role="add-feed"]').fill('http://169.254.169.254/latest/meta-data/');
    await page.locator('[data-action="add-feed"]').click();

    await expect(page.locator('.banner')).toContainText(/Couldn't add that feed/);
    await expect(page.getByRole('button', { name: /169\.254/ })).toHaveCount(0);
  });

  test('connection state is always visible', async ({ page }) => {
    await boot(page);
    // A reader that has silently stopped receiving looks identical to a quiet
    // news day. This indicator is the only thing separating them.
    await expect(page.locator('.conn')).toBeVisible();
    await expect(page.locator('.conn')).toHaveAttribute('data-state', 'live');
  });
});
