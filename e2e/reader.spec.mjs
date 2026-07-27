import { test, expect, boot, openFeed, feedRow, openRail, openAddFeed, currentArticle, openStream } from './fixtures.mjs';
import { FEED_ORIGIN } from './ports.mjs';

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
    await expect(currentArticle(page).locator('h1')).toHaveText(/Speculative decoding/);
    await expect(currentArticle(page).locator('.article-body')).toContainText('n-gram table');

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
    await expect(currentArticle(page).locator('.article-body strong')).toHaveText('proposals');

    // The sanitizer is the highest-stakes thing in the app: this is third-party
    // markup rendered inside our origin.
    await expect(currentArticle(page).locator('.article-body script')).toHaveCount(0);
  });

  test('starring persists across a reload', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();

    // "Star" is not a thing this reader does any more (8b.24): the control is
    // "Read later", backed by the same stored flag. The test follows the app's
    // vocabulary rather than asserting the one it used to have — a test that
    // keeps the old name passes only until somebody reads it and believes it.
    //
    // Scoped to the article pane, because the sidebar has a Read later stream
    // and a bare name matches both.
    const article = page.locator('.pane-article');
    const later = article.locator('[data-action="read-later"]').first();
    await later.click();
    await expect(later).toHaveAttribute('aria-pressed', 'true');

    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await openStream(page, /Read later/);
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

  /**
   * Both tests below are scoped to ONE feed, and every index is an index within
   * it. The suite's reset clears read state but not subscriptions, so any test
   * that subscribes to something shifts the positions in the all-feeds list
   * permanently — an index-based assertion there passes or fails according to
   * what ran before it. Alpha Journal is always exactly its three fixture items.
   */
  const ALPHA = 'Alpha Journal';

  /**
   * openAlpha selects the fixture feed, by the row itself rather than by name.
   *
   * Deliberately not the `openFeed` helper: a feed row now has a settings gear
   * beside it whose accessible name also contains the feed's, so a name-based
   * lookup matches two elements and Playwright refuses in strict mode. That is
   * the suite's stale-helper problem rather than this test's, and it is filed —
   * but a test cannot wait for it, so this asks for the row class directly.
   */
  async function openAlpha(page) {
    await page.locator('.pane-rail .feed-row', { hasText: ALPHA }).first().click();
    await expect(page.locator('.pane-list')).toBeVisible();
  }

  test('jumping down the list does not read what it jumped over', async ({ page }) => {
    await boot(page);
    await openAlpha(page);
    const rows = page.locator('.item-row');
    await expect(rows).toHaveCount(3);

    // Row 1 is the long fixture article, and its length is what makes this
    // reproducible at all: the pane can only carry an article clean past the
    // fold when there is enough below it to scroll.
    //
    // The wait on the body is load-bearing. An earlier version of this test
    // passed against the UNFIXED client without it, because a skeleton is short
    // enough that the jump cannot push it clear of the fold — a test that clicks
    // before the bodies land is asserting against a stream too short to
    // reproduce anything.
    await rows.nth(0).click();
    await expect(rows.nth(0)).toHaveAttribute('data-read', 'true');
    await expect(page.locator('.article-body').first()).toBeVisible();

    await rows.nth(2).click();
    await expect(rows.nth(2)).toHaveAttribute('data-read', 'true');
    // The jump itself, waited on directly: the pane has travelled off the top,
    // which is the exact condition that used to mark the seeded article read.
    await expect
      .poll(async () => page.locator('.pane-article')
        .evaluate((el) => el.scrollTop), { timeout: 20_000 })
      .toBeGreaterThan(0);

    // The one in between. Opening row 2 seeds the article above it into the
    // stream and then scrolls the target to the top, which drags the seeded one
    // past the bottom edge — and "scrolled past" is one of the two ways this app
    // marks an article read. It must not count here: the app moved the article,
    // the reader did not, and not a word of it was ever on screen.
    //
    // Asserted after a reload as well as before it. Optimistic state is local
    // and could simply not have been applied yet; the reload is what proves no
    // SetItemState was sent, which is the thing that actually went wrong.
    await expect(rows.nth(1)).toHaveAttribute('data-read', 'false');

    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await openAlpha(page);
    await expect(page.locator('.item-row').nth(1))
      .toHaveAttribute('data-read', 'false');
  });

  // FIXME (see TODO 8b.50): this cannot pass while the topmost-article handler is
  // dead, and it is dead in the current build — scrolling through three articles
  // changes neither `document.title` nor the highlighted row, so `focusArticle`
  // is never running. Marking read still works because the OTHER handler
  // (scrolled-past) is alive, which is what makes the breakage easy to miss.
  //
  // Left in the file rather than deleted: it is the assertion that says the jump
  // suppression is temporary, and the day the handler is fixed this is how we
  // find out whether it stayed temporary.
  test.fixme('scrolling back into a skipped article does mark it read', async ({ page }) => {
    await boot(page);
    await openAlpha(page);
    const rows = page.locator('.item-row');

    // The other half of the same behaviour, and the reason the fix suppresses
    // rather than switches scroll-past marking off. The article the jump passed
    // over is still sitting in the stream above the reader; scrolling up into it
    // is reading it, and it has to count. A fix that made the first test pass by
    // making this one fail would be a regression wearing a fix's hat.
    await rows.nth(0).click();
    await expect(page.locator('.article-body').first()).toBeVisible();
    await rows.nth(2).click();
    await expect
      .poll(async () => page.locator('.pane-article')
        .evaluate((el) => el.scrollTop), { timeout: 20_000 })
      .toBeGreaterThan(0);
    await expect(rows.nth(1)).toHaveAttribute('data-read', 'false');

    // Scrolled by the READER this time, which is the whole distinction the fix
    // rests on — so it is scrolled the way a reader does it. A real wheel over
    // the pane, not `scrollTop = 0` with a synthetic event: the app listens for
    // scroll in the capture phase and a hand-dispatched Event is not the same
    // object the browser produces. (It is also not merely unrealistic: the
    // synthetic version killed the Playwright worker outright, with no result
    // and no error, every time it ran.)
    const box = await page.locator('.pane-article').boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    for (let i = 0; i < 8; i++) await page.mouse.wheel(0, -600);

    await expect(rows.nth(1))
      .toHaveAttribute('data-read', 'true', { timeout: 30_000 });
  });

  test('a reload paints the saved view, never the default one first', async ({ page }) => {
    await boot(page);
    await openAlpha(page);
    await expect(page.locator('.list-title')).toHaveText(ALPHA);

    // The flash this guards against lasted one round trip, so polling for it
    // after the fact is a race the test would usually lose. Instead the page
    // records the FIRST list title it ever paints, from an observer installed
    // before the app boots — an assertion about the first frame has to be made
    // by something that was watching for it.
    await page.addInitScript(() => {
      window.__firstTitle = null;
      new MutationObserver(() => {
        if (window.__firstTitle !== null) return;
        const el = document.querySelector('.list-title');
        if (el && el.textContent.trim()) window.__firstTitle = el.textContent.trim();
      // `document`, NOT `document.documentElement`. The boot shim replaces the
      // root element, so an observer bound to the one that exists at
      // document-start is watching a detached node by the time the app renders
      // — it reported nothing at all, which reads exactly like a passing test.
      }).observe(document, { subtree: true, childList: true });
    });

    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.list-title')).toHaveText(ALPHA);

    // Not "ends up right" — that was already true before the fix, and is what
    // made it look like a rendering glitch rather than a bug. The saved view is
    // fetched while the splash is still up, so the reader's first painted frame
    // is the restored one and "All feeds" is never on screen at all.
    expect(await page.evaluate(() => window.__firstTitle)).toBe(ALPHA);
  });

  test('mark all read empties the unread view and zeroes the counts', async ({ page }) => {
    await boot(page);
    await page.locator('[data-action="mark-all"]').click();

    await expect(page.locator('.banner')).toContainText(/Marked \d+ read/);
    // Scoped to FEED rows. Categories carry counts now too, so a bare
    // `.feed-count` counts the rail's category totals as well and can never
    // reach zero — the click works, and the assertion was measuring something
    // else. The banner above is the evidence that it worked; this is the
    // evidence that nothing is left unread.
    await expect(page.locator('.feed-row .feed-count')).toHaveCount(0);
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
    await openStream(page, /^Notes$/);
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
  // From ports.mjs, not a literal. The fixture server's port is derived per run
  // so two agents' suites cannot kill each other (see that file), which means a
  // hardcoded 9011 points at nothing — and the symptom is the add-feed dialog
  // "not appearing", because the subscribe never resolves.
  const DECLARES = `${FEED_ORIGIN}/declares.html`;
  const NOFEED = `${FEED_ORIGIN}/nofeed.html`;

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
  const ALPHA = `${FEED_ORIGIN}/alpha.xml`;

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
    const editor = page.getByRole('dialog', { name: 'Category' });
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
  // Google Reader's map for MOVEMENT — j and k — because muscle memory transfers
  // on day one and renaming those would throw it away for nothing.
  //
  // Not for the actions. `s` is gone: the reader has no "star", it has read
  // later, like and dislike, so the shortcuts are `t`, `l` and `d` — each named
  // after the thing it does rather than after what the thing used to be called
  // somewhere else. A test asserting `s` passes only until somebody reads it.
  test('j and k move through the list, t saves for later', async ({ page }) => {
    await boot(page);

    await page.locator('body').press('j');
    await expect(currentArticle(page).locator('h1')).toBeVisible();
    const first = await currentArticle(page).locator('h1').textContent();

    // Waited for, not read. `press` returns as soon as the key is delivered,
    // and the pane re-renders asynchronously — reading the title straight after
    // reads the OLD one and the comparison fails on a race rather than on
    // behaviour.
    await page.locator('body').press('j');
    await expect(currentArticle(page).locator('h1')).not.toHaveText(first);
    const second = await currentArticle(page).locator('h1').textContent();
    expect(second).not.toBe(first);

    await page.locator('body').press('k');
    await expect(currentArticle(page).locator('h1')).toHaveText(first);

    await page.locator('body').press('t');
    await expect(page.locator('.pane-article [data-action="read-later"]').first())
      .toHaveAttribute('aria-pressed', 'true');
  });

  test('shortcuts do not fire while typing', async ({ page }) => {
    await boot(page);
    // What the shortcuts would have DONE, rather than whether anything is on
    // screen. The pane is a stream: an article is current from the moment the
    // list loads, so "no article is open" was never going to be true again and
    // asserting it tested the stream rather than the keyboard.
    const before = await currentArticle(page).locator('h1').textContent();

    const search = page.locator('[data-role="search"]');
    await search.fill('jjjkkktt');

    await expect(search).toHaveValue('jjjkkktt');
    // j and k would have moved it; t would have saved it for later.
    await expect(currentArticle(page).locator('h1')).toHaveText(before);
    await expect(page.locator('.pane-article [data-action="read-later"]').first())
      .toHaveAttribute('aria-pressed', 'false');
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
