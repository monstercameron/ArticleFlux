import { test, expect, boot, openFeed, feedRow, openRail, openAddFeed } from './fixtures.mjs';

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

  test('jumping down the list does not read what it jumped over', async ({ page }) => {
    await boot(page);
    const rows = page.locator('.item-row');

    // Row 3 is the long fixture article, which is what makes this reproducible:
    // the pane can only scroll far enough to carry an article clean past the
    // fold when there is something taller than the viewport below it.
    //
    // Both waits below are load-bearing, and an earlier version of this test
    // passed against the unfixed client without them. The bug needs the seeded
    // article to have its BODY — a skeleton is short enough that the jump cannot
    // push it clear of the fold, so a test that clicks before the bodies land
    // asserts against a stream too short to reproduce anything.
    await rows.nth(0).click();
    await expect(rows.nth(0)).toHaveAttribute('data-read', 'true');
    await expect(page.locator('.article-body').first()).toBeVisible();

    await rows.nth(3).click();
    await expect(rows.nth(3)).toHaveAttribute('data-read', 'true');
    // The jump itself, waited on directly: the pane has travelled off the top,
    // which is the exact condition that used to mark the seeded article read.
    await expect
      .poll(async () => page.locator('.pane-article')
        .evaluate((el) => el.scrollTop), { timeout: 20_000 })
      .toBeGreaterThan(0);

    // The two in between. Opening row 3 seeds the article above it into the
    // stream and then scrolls the target to the top, which drags the seeded one
    // past the bottom edge — and "scrolled past" is one of the two ways this app
    // marks an article read. It must not count here: the app moved the article,
    // the reader did not, and nothing was ever on screen to be read.
    //
    // Asserted after a reload as well as before it. Optimistic state is local
    // and could simply not have been applied yet; a reload proves no SetItemState
    // was sent, which is the thing that actually went wrong.
    await expect(rows.nth(1)).toHaveAttribute('data-read', 'false');
    await expect(rows.nth(2)).toHaveAttribute('data-read', 'false');

    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').nth(1)).toHaveAttribute('data-read', 'false');
    await expect(page.locator('.item-row').nth(2)).toHaveAttribute('data-read', 'false');
  });

  test('scrolling through the stream still marks what it passes', async ({ page }) => {
    await boot(page);
    const rows = page.locator('.item-row');

    // The other half of the same behaviour, and the reason the fix is a
    // suppression list rather than switching scroll-past marking off: a reader
    // who scrolls to the end of an article HAS read it, and the previous test
    // passing by breaking this one would be a regression wearing a fix's hat.
    await rows.nth(3).click();
    await expect(rows.nth(3)).toHaveAttribute('data-read', 'true');

    const pane = page.locator('.pane-article');
    await pane.evaluate((el) => { el.scrollTop = el.scrollHeight; });

    // Row 4 follows row 3 in the list, so scrolling to the bottom of the stream
    // arrives in it.
    await expect(rows.nth(4)).toHaveAttribute('data-read', 'true', { timeout: 30_000 });
  });

  test('mark all read empties the unread view and zeroes the counts', async ({ page }) => {
    await boot(page);
    await page.locator('[data-action="mark-all"]').click();

    await expect(page.locator('.banner')).toContainText(/Marked \d+ read/);
    await expect(page.locator('.feed-count')).toHaveCount(0);
  });
});

test.describe('notes and tags', () => {
  /**
   * openNote reveals the note editor on the article being read.
   *
   * The panel is a disclosure: the summary row (tags, the note preview, the sync
   * mark) is always on screen and the textarea is behind the toggle. A test that
   * reaches straight for .note-field waits forever for something that is
   * deliberately not rendered yet.
   */
  async function openNote(page) {
    const field = page.locator('.note-field').first();
    if (!(await field.count())) {
      await page.locator('[data-action="toggle-note"]').first().click();
    }
    await expect(field).toBeVisible({ timeout: 15_000 });
    return field;
  }

  test('a note saves itself and survives a reload', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();

    const note = await openNote(page);
    await note.fill('Worth rereading when the n-gram work lands.');

    // No Ctrl+Enter, no blur, no button: typing and then stopping is the whole
    // interaction. The glyph is the only feedback there is, so it is what the
    // test waits on.
    //
    // The generous timeout is about the harness, not the debounce, which is 800ms.
    // Headless Chromium here runs requestAnimationFrame at well under one frame
    // per second when nothing is being clicked — measured at 5 frames in 12
    // seconds — and the render loop is driven by rAF, so each state transition
    // (pending → saving → saved) waits for a frame that is seconds away. A real
    // tab paints all three inside a second.
    const mark = page.locator('.article-note .note-sync').first();
    await expect(mark).toHaveAttribute('data-sync', 'saved', { timeout: 45_000 });

    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await page.locator('.item-row').first().click();
    await expect(await openNote(page)).toHaveValue(/Worth rereading/, { timeout: 30_000 });

    // And it is discoverable as a note, not just as text in a field. Anchored,
    // because "Beta Notes" is one of the fixture feeds.
    await openFeed(page, /^Notes$/);
    await expect(page.locator('.item-row')).toHaveCount(1);
  });

  test('leaving the field saves without waiting for the debounce', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();

    const note = await openNote(page);
    await note.fill('Clicked away mid-thought.');
    // Immediately — well inside the debounce window. The blur flush is what
    // stops a reader who types and clicks straight into the next article from
    // losing the sentence they just wrote.
    await note.blur();

    // Same rAF-throttling allowance as above; the flush itself is immediate.
    await expect(page.locator('.article-note .note-sync').first())
      .toHaveAttribute('data-sync', 'saved', { timeout: 45_000 });
  });

  test('a tag is visible on the article and removable from it', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();

    await openNote(page);
    const panel = page.locator('.article-note').first();
    await panel.locator('[data-role="tag"]').first().fill('mornings');
    await panel.locator('[data-action="add-tag"]').first().click();

    // It appears where it was added, not only in the feed's settings panel — and
    // on the summary row, so it is still there when the editor is closed again.
    const chip = panel.locator('.tag-chip', { hasText: 'mornings' });
    await expect(chip).toBeVisible({ timeout: 45_000 });

    // And the same chip takes it off again.
    await chip.click();
    await expect(panel.locator('.tag-chip')).toHaveCount(0, { timeout: 45_000 });

    // Gone on the server, not just out of the render: the last association
    // removes the tag, so the sidebar must not still be offering it.
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.pane-rail .feed-name', { hasText: 'mornings' }))
      .toHaveCount(0);
  });
});

test.describe('finding the feed', () => {
  // The fixture server's port is pinned in global-setup.mjs.
  const DECLARES = 'http://127.0.0.1:9011/declares.html';
  const NOFEED = 'http://127.0.0.1:9011/nofeed.html';

  test('a page that is not a feed offers the feed it points at', async ({ page }) => {
    await boot(page);
    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill(DECLARES);
    await dialog.locator('[data-action="add-feed"]').click();

    // The address is a page, so subscribing fails and the free rungs run
    // without the reader having to ask for them.
    const cand = dialog.locator('.af-cand');
    await expect(cand).toHaveCount(1, { timeout: 45_000 });
    // Fetched and parsed before being offered: the count is the evidence.
    await expect(cand.locator('.af-cand-meta')).toContainText(/item/);
    await expect(cand.locator('.af-cand-meta')).toContainText(/links to it/);

    // Taking the offer subscribes to the feed the page declared, not the page.
    await cand.locator('[data-action="add-feed-candidate"]').click();
    await expect(dialog).toBeHidden({ timeout: 45_000 });
    await expect(page.locator('.banner')).toContainText(/Alpha Journal/, { timeout: 45_000 });
  });

  test('a page with no feed offers Smart+, and says what it would send', async ({ page }) => {
    await boot(page);
    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill(NOFEED);
    await dialog.locator('[data-action="add-feed"]').click();

    const ladder = dialog.locator('.af-ladder');
    await expect(ladder).toBeVisible({ timeout: 45_000 });
    await expect(ladder).toContainText(/No feed here/);
    // The lamp is on the address row and is visible from the moment the dialog
    // opens — the capability has to be discoverable before it is needed.
    const lamp = dialog.locator('[data-action="add-feed-smart"]');
    await expect(lamp).toHaveAttribute('aria-pressed', 'false');
    // Off means no button to press: the sentence points at the lamp, and says
    // what turning it on would send. That sentence IS the consent, so its
    // absence is a bug rather than a wording preference.
    await expect(ladder).toContainText(/OpenAI/);
    await expect(ladder.locator('[data-action="add-feed-analyze"]')).toHaveCount(0);

    // Arming it and adding the address again runs the whole ladder in one press
    // — the consent is standing, so asking for a second press would be asking
    // the same question twice.
    await lamp.click();
    await expect(lamp).toHaveAttribute('aria-pressed', 'true', { timeout: 20_000 });
    await dialog.locator('[data-action="add-feed"]').click();

    // This instance has no OpenAI key, so it reports exactly that rather than
    // failing obscurely — the remedy belongs to whoever runs the server, and the
    // message says so.
    await expect(ladder).toContainText(/no OpenAI key/, { timeout: 45_000 });
  });
});

test.describe('categories', () => {
  // The fixture feed server's port is pinned in global-setup.mjs, and this is
  // the one spec that needs a feed URL rather than a feed row: adding an address
  // is what the dialog is for.
  const ALPHA = 'http://127.0.0.1:9011/alpha.xml';

  test('a feed is filed on the way in, and the rail groups it', async ({ page }, testInfo) => {
    // Named per project: the two projects share one server, so a fixed name
    // would have the phone run deleting the category the desktop run is
    // asserting on.
    const name = `Filed ${testInfo.project.name}`;
    await boot(page);

    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill(ALPHA);
    await dialog.locator('[data-action="add-feed-new-category"]').click();
    await dialog.locator('[data-role="add-feed-category"]').fill(name);
    await dialog.locator('[data-action="add-feed"]').click();

    // Adding an address already subscribed refiles it rather than erroring —
    // the form showed a category being chosen, so it has to take effect.
    await expect(dialog).toBeHidden({ timeout: 45_000 });
    const rail = await openRail(page);
    // The slot, not the name: a category is three buttons — the disclosure, the
    // row, and the editor — and all three carry the name for assistive tech.
    const slot = rail.locator('.cat-slot', { hasText: name });
    const row = slot.locator('.cat-row');
    await expect(row).toBeVisible({ timeout: 45_000 });

    // The category is a fold: its feeds appear under it when it is opened, and
    // the spine is what says they are inside it rather than after it.
    await slot.locator('[data-action="toggle-category"]').click();
    await expect(page.locator('.feed-nested .feed-name', { hasText: 'Alpha Journal' }))
      .toBeVisible({ timeout: 45_000 });

    // And selecting it lists everything filed under it — alpha's three items,
    // not the five in the megafeed.
    await row.click();
    await expect(page.locator('.item-row')).toHaveCount(3, { timeout: 45_000 });

    // Deleting a category unfiles its feeds and unsubscribes nothing. Two
    // presses: the first arms, the second does it.
    await slot.hover();
    await slot.locator('[data-action="category-open"]').click();
    const editor = page.locator('.af-narrow');
    await expect(editor).toBeVisible();
    await editor.locator('[data-action="category-delete"]').click();
    await editor.locator('[data-action="category-delete-confirm"]').click();
    await expect(editor).toBeHidden({ timeout: 45_000 });

    // The feed survived, which is the half of this that would be expensive to
    // get wrong.
    await expect(rail.locator('.feed-name', { hasText: 'Alpha Journal' }).first())
      .toBeVisible({ timeout: 45_000 });
    await expect(rail.getByRole('button', { name: new RegExp(name) })).toHaveCount(0);
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
    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill('not-a-url');
    await dialog.locator('[data-action="add-feed"]').click();

    // The refusal is in the dialog, beside the field that has to be fixed, and
    // the dialog stays open — closing it would throw away the URL.
    await expect(dialog.locator('.af-error')).toContainText(/Couldn't add that feed/);
    await expect(dialog.locator('[data-role="add-feed"]')).toHaveValue('not-a-url');
  });

  test('the SSRF guard refuses an internal address from the UI', async ({ page }) => {
    await boot(page);
    // The one attack the whole netguard package exists for, exercised through
    // the feature that carries it: subscribe-by-URL.
    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill('http://169.254.169.254/latest/meta-data/');
    await dialog.locator('[data-action="add-feed"]').click();

    await expect(dialog.locator('.af-error')).toContainText(/Couldn't add that feed/);
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
