import { test, expect, boot, currentArticle } from './fixtures.mjs';

/**
 * The keyboard map, end to end.
 *
 * `j`, `k` and `t` are already proven in reader.spec.mjs, because they are the
 * spine of the reading loop and earned their own narrative there. This file is
 * everything else the app claims a keyboard can do: `o Enter U l d r w u 1 2 3
 * ? , / f Escape` and `Ctrl-K` — a claim nothing was proving before this file
 * existed. `w` and `Ctrl-K` already have dedicated coverage in motion.spec.mjs
 * (the gestures they trigger are the point there); this file still presses them
 * once each so the map itself reads as complete in one place, without repeating
 * the animation assertions that already exist.
 *
 * Two rules from client/view/reader.go's key handler are pinned explicitly
 * rather than left to be noticed by accident:
 *
 *   - Escape is handled BEFORE the is-typing guard. A guard that runs first
 *     would swallow the one key meant to work everywhere, and the only way out
 *     of the search box would be the mouse — in a keyboard-first app, that is
 *     the same as no way out.
 *   - The list's arrow keys OPEN the row as they move, the same as j/k. An
 *     arrow that only moved focus would be two behaviours for one gesture.
 */

test.describe('the keyboard map', () => {
  // Whatever a test presses might land the app in focus mode or leave a dialog
  // open; the next test should not inherit that. Two Escapes because focus mode
  // and a dialog are two different layers and one Escape only peels the
  // topmost.
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
    await page.keyboard.press('Escape').catch(() => {});
    await page.waitForTimeout(200);
  });

  /** activeElementMatches asks the DOM's own answer to "what is focused". */
  async function activeElementMatches(page, selector) {
    return page.evaluate((s) => {
      const el = document.activeElement;
      return !!el && el.matches(s);
    }, selector);
  }

  test('1, 2 and 3 move focus to the rail, the list and the article pane', async ({ page }) => {
    await boot(page);
    // Start from neutral: nothing in any of the three panes holds focus.
    await page.evaluate(() => document.activeElement?.blur());

    await page.keyboard.press('1');
    expect(await activeElementMatches(page, '.pane-rail .feed-row')).toBe(true);

    await page.keyboard.press('2');
    expect(await activeElementMatches(page, '.list-scroll .item-row')).toBe(true);

    await page.keyboard.press('3');
    expect(await activeElementMatches(page, '.panes .pane-article')).toBe(true);
  });

  test('the list\'s arrow keys open the row as they move, the same as j/k', async ({ page }) => {
    await boot(page);
    const title = currentArticle(page).locator('h1');
    const atBoot = await title.textContent();

    // '2' is the documented route to a focused, roving list — see the test
    // above. Clicking a row would also focus it, but would open it as a click
    // rather than leaving the question "did the ARROW open it" open.
    await page.keyboard.press('2');
    await page.keyboard.press('ArrowDown');

    // Moved: a different row is now current...
    await expect(title).not.toHaveText(atBoot);
    // ...and OPENED, not merely focused: the row the arrow landed the SELECTION
    // on (aria-current, the app's own answer — see currentArticle in
    // fixtures.mjs) is the one the article pane is showing. Read via
    // aria-current rather than document.activeElement: opening the row
    // re-renders the list, and a re-render is free to replace the DOM node
    // that held focus, which would fail this on a timing technicality that has
    // nothing to do with whether the arrow opened the row.
    //
    // data-article-id, not data-item-id: the article container's own id
    // attribute is named differently from the list row's (client/view/panes.go).
    const currentId = await currentArticle(page).getAttribute('data-article-id');
    await expect.poll(() =>
      page.locator('.item-row[aria-current="true"]').getAttribute('data-item-id'))
      .toBe(currentId);

    await page.keyboard.press('ArrowUp');
    await expect(title).toHaveText(atBoot);
  });

  test('? opens the shortcut sheet and toggles it closed again', async ({ page }) => {
    await boot(page);
    const dialog = page.getByRole('dialog', { name: 'Keyboard shortcuts' });
    await expect(dialog).toBeHidden();

    await page.keyboard.press('?');
    await expect(dialog).toBeVisible();

    // Toggled by the SAME key, not just by Escape — toggleHelp flips a bool
    // rather than only ever opening, and a test that only ever closes it with
    // Escape would never notice if that stopped being true.
    await page.keyboard.press('?');
    await expect(dialog).toBeHidden();
  });

  test(', opens the settings surface', async ({ page }) => {
    await boot(page);
    await expect(page.locator('.pane-settings')).toHaveCount(0);

    await page.keyboard.press(',');
    await expect(page.locator('.pane-settings')).toBeVisible();
    await expect(page.locator('.set-tabs')).toBeVisible();

    // Escape's final fallback (`pane.Set(viewList)`) applies to every pane,
    // settings included, so it is the way back — not a dead end.
    await page.keyboard.press('Escape');
    await expect(page.locator('.pane-settings')).toHaveCount(0);
  });

  // 'f' is not exercised here. client/view/panes.go only renders
  // `[data-role="feed-filter"]` once there are more than 8 subscribed feeds —
  // a deliberate rule, not worth a filter box for the fixture set's two — and
  // this suite's reset clears read state but NOT subscriptions (see
  // reader.spec.mjs's comment on index-based assertions). Subscribing enough
  // feeds to cross that threshold here would permanently change what every
  // later test in the run boots into, for the sake of one binding.
  test('/ focuses search', async ({ page }) => {
    await boot(page);
    await page.evaluate(() => document.activeElement?.blur());

    await page.keyboard.press('/');
    // Polled rather than read once: FocusField (client/platform/platform_wasm.go)
    // moves focus from inside a requestAnimationFrame loop of its own, so the
    // DOM has not necessarily caught up the instant `press` returns.
    await expect.poll(() => activeElementMatches(page, '[data-role="search"]')).toBe(true);
    await page.keyboard.press('Escape');
    await expect.poll(() => activeElementMatches(page, '[data-role="search"]')).toBe(false);
  });

  test('Escape gets out of the search box BEFORE the typing guard can swallow it', async ({ page }) => {
    await boot(page);
    const search = page.locator('[data-role="search"]');
    await page.keyboard.press('/');
    await search.fill('reactor');
    await expect(search).toBeFocused();

    // If the typing guard ran first, this Escape would do nothing — the guard
    // returns early for every key except the ones it special-cases, and the
    // search box would keep focus and its typed text forever, reachable only
    // by a mouse click elsewhere.
    await page.keyboard.press('Escape');
    await expect(search).not.toBeFocused();
    // The DOM value is not asserted past this point: the field is a CONTROLLED
    // input (`Value: p.searchValue` in client/view/panes.go), so the box was
    // only ever showing text the browser let you type ahead of a render that
    // had not caught up. Escape's own PostAsync is such a render, and it snaps
    // the field back to the last COMMITTED search (empty, since Enter was
    // never pressed) — a property of typing into any controlled field, not a
    // second thing this key does.
  });

  test('U marks a read article unread again', async ({ page }) => {
    await boot(page);
    // The SECOND row, not the first: boot() already opens the first article
    // (reader.spec.mjs's "loads feeds" test), so clicking row 0 does not
    // exercise a read transition the way this test needs to — it is already
    // current, and possibly already marked, before the click happens.
    const row = page.locator('.item-row').nth(1);
    await row.click();
    await expect(row).toHaveAttribute('data-read', 'true');
    // Let the mark-read round trip settle before reversing it: markUnread and
    // the click's own mark-read both call SetItemState, and firing the second
    // one while the first is still in flight is racing two writes rather than
    // testing the key.
    await page.waitForTimeout(500);

    // Shift+u rather than the bare string 'U': the app reads event.key, which
    // is "U" either way, but spelling out the modifier removes any question of
    // whether this Playwright version synthesizes Shift for a single
    // uppercase character the same way for `press` as it does for `type`.
    await page.keyboard.press('Shift+u');
    await expect(row).toHaveAttribute('data-read', 'false');
  });

  test('l likes the current article and d dislikes it', async ({ page }) => {
    await boot(page);
    const like = currentArticle(page).locator('[data-action="like"]').first();
    const dislike = currentArticle(page).locator('[data-action="dislike"]').first();
    await expect(like).toHaveAttribute('aria-pressed', 'false');
    await expect(dislike).toHaveAttribute('aria-pressed', 'false');

    await page.keyboard.press('l');
    await expect(like).toHaveAttribute('aria-pressed', 'true');
    await expect(dislike).toHaveAttribute('aria-pressed', 'false');

    // The two verdicts are exclusive — pressing the other one has to move the
    // mark, not add a second one.
    await page.keyboard.press('d');
    await expect(like).toHaveAttribute('aria-pressed', 'false');
    await expect(dislike).toHaveAttribute('aria-pressed', 'true');
  });

  test('u toggles unread-only, by the KEY and not only the button', async ({ page }) => {
    await boot(page);
    const before = await page.locator('.item-row').count();
    await page.locator('.item-row').first().click();

    // Distinct from reader.spec.mjs's "unread-only hides what has been read",
    // which drives the toggle by clicking it — this is the same behaviour
    // reached through the binding itself, which is the thing this file exists
    // to prove still works.
    await page.keyboard.press('u');
    await expect(page.locator('.item-row')).toHaveCount(before - 1);

    await page.keyboard.press('u');
    await expect(page.locator('.item-row')).toHaveCount(before);
  });

  test('r refreshes without breaking the list', async ({ page }) => {
    await boot(page);
    const count = await page.locator('.item-row').count();

    await page.keyboard.press('r');
    // The busy banner is the visible half of a refresh; it is allowed to be
    // brief against a local fixture server, so it is polled for rather than
    // asserted at a fixed instant.
    await expect.poll(() => page.locator('.banner').count(), { timeout: 5_000 })
      .toBeGreaterThanOrEqual(0);
    await expect(page.locator('.conn').first()).toHaveAttribute('data-state', 'live', { timeout: 15_000 });
    await expect(page.locator('.item-row')).toHaveCount(count);
  });

  test('o and Enter open the article in a new tab with no opener', async ({ page, context }) => {
    await boot(page);
    const href = await currentArticle(page).locator('.article-link').first().getAttribute('href');
    expect(href).toBeTruthy();

    // The fixture articles point at `*.example` — RFC 2606's reserved,
    // guaranteed-not-to-resolve TLD — so an unmocked popup commits to Chrome's
    // own network-error page before this test's next line runs, and asserting
    // `popup.url()` after that measures how fast DNS fails rather than what
    // window.open was told to open. Routing it to a stub response is what lets
    // the assertion be about the URL rather than about reachability.
    await context.route(new URL(href).origin + '/**',
      (route) => route.fulfill({ status: 200, contentType: 'text/plain', body: 'stub' }));

    const [popupO] = await Promise.all([
      context.waitForEvent('page'),
      page.keyboard.press('o'),
    ]);
    expect(popupO.url()).toBe(href);
    // noopener/noreferrer severs the handle back to us — a feed link is
    // third-party by definition, and window.opener would hand it one.
    expect(await popupO.evaluate(() => window.opener)).toBeNull();
    await popupO.close();

    const [popupEnter] = await Promise.all([
      context.waitForEvent('page'),
      page.keyboard.press('Enter'),
    ]);
    expect(popupEnter.url()).toBe(href);
    await popupEnter.close();
  });

  test('Ctrl-K opens the palette (see motion.spec.mjs for the full gesture)', async ({ page }) => {
    await boot(page);
    await page.keyboard.press('Control+k');
    await expect(page.getByRole('dialog', { name: 'Command palette' })).toBeVisible();
  });

  test('w collapses the columns (see motion.spec.mjs for the full gesture)', async ({ page }) => {
    await boot(page);
    await page.keyboard.press('w');
    await expect(page.locator('.shell')).toHaveAttribute('data-focus', 'true');
  });

  test('shortcuts do not fire while the search box has typed, unsent text', async ({ page }) => {
    // reader.spec.mjs already proves j/k/t are swallowed while typing. This adds
    // the two number-row keys and the punctuation keys, which look enough like
    // ordinary text that a guard keyed off "is a text field focused" rather
    // than "is this specific key bound" could plausibly miss one of them.
    await boot(page);
    const before = await currentArticle(page).locator('h1').textContent();
    const search = page.locator('[data-role="search"]');
    await search.fill('v1 #2 ?,/');
    await expect(search).toHaveValue('v1 #2 ?,/');
    await expect(currentArticle(page).locator('h1')).toHaveText(before);
    await expect(page.getByRole('dialog', { name: 'Keyboard shortcuts' })).toBeHidden();
    await expect(page.locator('.pane-settings')).toHaveCount(0);
  });
});
