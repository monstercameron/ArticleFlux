import { test, expect, boot, signIn, openFeed, openStream, openRail, feedRow, currentArticle, openRow } from './fixtures.mjs';

/**
 * The address bar (§20.13b).
 *
 * Every assertion here is about something that was impossible before routing
 * existed, and each one is a thing a reader does rather than a thing the codec
 * does — route_test.go already pins the grammar in isolation, and repeating it
 * through a browser would only make it slower to run and no more true.
 *
 * What only a browser can prove is the wiring: that navigating WRITES the
 * address, that an address READ at boot wins over the saved place, that Back
 * actually goes back, and that a deep link loads at all — which it did not
 * before, because index.html names every asset relatively and the shell is
 * served at every route (see the <base href> comment in web/index.html).
 */

/** path returns the current pathname + search, which is what a route is. */
function path(page) {
  const u = new URL(page.url());
  return u.pathname + u.search;
}

/**
 * place is the address with the open article stripped off.
 *
 * Almost every assertion here wants this rather than the raw path, because a
 * scope change settles in TWO steps and both are correct: the place lands first,
 * then the pick effect auto-opens an article in it (reader.go) and the address
 * gains a `/read/<id>` by replace. Asserting on the raw path is therefore a race
 * against a second write — it passes or fails on which step the poll happens to
 * catch, which is exactly the "flaky" that means nothing. The place is the part
 * navigation decides; the article is the part scrolling decides.
 */
function place(page) {
  return stripArticle(path(page));
}

/**
 * stripArticle removes a `/read/<id>` segment from an address string.
 *
 * Normalised back to "/" when nothing is left, because All feeds IS the root:
 * "/read/<id>" strips to the empty string, and an empty string is not an address.
 */
function stripArticle(p) {
  const out = p.replace(/\/read\/[^/?]+/, '');
  return out === '' ? '/' : out;
}

/**
 * settled waits for the PLACE to match, then returns the full address.
 *
 * Reading page.url() straight after a click is the other race, and the one that
 * cost the most: the address is written from an EFFECT, so it lands a commit
 * after the click that changed the state. A test that captured the URL
 * immediately got the PREVIOUS address, navigated back to it later, and reported
 * the app resuming the saved place — a correct app failing a test that had
 * recorded the wrong link. Anything that captures an address has to wait for it.
 */
async function settled(page, re) {
  await expect.poll(() => place(page)).toMatch(re);
  return path(page);
}

test.describe('the address bar', () => {
  test('navigating writes the address', async ({ page }) => {
    await boot(page);
    // The landing address is normalised to the place the reader resumed into,
    // rather than being left bare — that write is what makes a resumed place
    // linkable.
    await expect.poll(() => place(page)).toBe('/');

    await openStream(page, 'Unread');
    await expect.poll(() => place(page)).toBe('/unread');

    await openStream(page, 'Read later');
    await expect.poll(() => place(page)).toBe('/later');

    await openFeed(page, /Alpha Journal/);
    // A feed is addressed by id, so this asserts the shape rather than a literal
    // — the seed's ids are not fixed and a test that hardcoded one would be
    // pinning the fixture, not the router.
    await expect.poll(() => place(page)).toMatch(/^\/feed\/[^/]+$/);
  });

  test('opening an article puts it in the address', async ({ page }) => {
    await boot(page);
    await openRow(page, 0);
    await expect(currentArticle(page).locator('h1')).toBeVisible();
    await expect.poll(() => path(page)).toMatch(/\/read\/[^/]+$/);
  });

  test('a deep link to an article that is not first stays on that article', async ({ page }) => {
    // The stream opens with the article BEFORE the target above it, still a
    // skeleton; when its body lands it grows, and with nothing holding the target
    // the pane showed the middle of the previous article and the scroll-spy wrote
    // that one into the address — so every reload drifted one article up.
    await boot(page);
    await openFeed(page, /Alpha Journal/);
    await openRow(page, 2);
    await expect(currentArticle(page).locator('h1')).toBeAttached();
    const id = await currentArticle(page).getAttribute('data-article-id');
    // path(), not settled(): settled() polls place(), which is the address with
    // the article stripped off — the one thing this test is about.
    await expect.poll(() => path(page)).toMatch(/\/read\/[^/]+$/);
    const articlePath = path(page);
    expect(articlePath).toContain(`/read/${id}`);

    for (let i = 0; i < 2; i++) {
      await (i === 0 ? page.goto(articlePath) : page.reload());
      await signIn(page);
      await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
      await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
      // Long enough for the bodies above and below to have arrived and settled.
      await expect(currentArticle(page)).toHaveAttribute('data-article-id', id);
      await page.waitForTimeout(3000);
      await expect(currentArticle(page)).toHaveAttribute('data-article-id', id);
      expect(path(page)).toBe(articlePath);
    }
  });

  test('an article addressed without its feed opens that article', async ({ page }) => {
    await boot(page);
    await openFeed(page, /Alpha Journal/);
    await openRow(page, 2);
    const id = await currentArticle(page).getAttribute('data-article-id');

    await page.goto(`/read/${id}`);
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(currentArticle(page)).toHaveAttribute('data-article-id', id, { timeout: 30_000 });
    await page.waitForTimeout(2000);
    await expect(currentArticle(page)).toHaveAttribute('data-article-id', id);
  });

  test('a deep link to a feed opens that feed, not the saved place', async ({ page }) => {
    // Establish a saved place that is NOT where the link points. Read later is
    // empty in the seed, which makes the two trivially distinguishable.
    await boot(page);
    await openFeed(page, /Alpha Journal/);
    const feedPath = await settled(page, /^\/feed\/[^/]+$/);
    await openStream(page, 'Read later');
    await expect.poll(() => place(page)).toBe('/later');

    // Now arrive at the feed's address in a fresh load. The saved place says
    // Read later; the address says the feed. The address is the more recent,
    // explicit request and has to win — that is the whole precedence rule.
    await page.goto(feedPath);
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });

    await expect.poll(() => place(page)).toBe(stripArticle(feedPath));
    // And the header names the feed, which is the half a path cannot carry: the
    // address holds an id, so the title has to be resolved once the rail lands
    // (titleForScope). A blank header here would mean that repair never ran.
    await expect(page.locator('.pane-list')).toContainText('Alpha Journal');
  });

  test('a bare address still resumes the saved place', async ({ page }) => {
    // A30 must survive routing: the preference is still written on every
    // navigation, and the bare address — which is what a bookmark to the app and
    // every launcher icon produce — still lands where the reader left off.
    await boot(page);
    await openFeed(page, /Beta Notes/);
    const feedPath = await settled(page, /^\/feed\/[^/]+$/);

    await page.goto('/');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });

    // Resumed into the feed, and the address was then rewritten to name it.
    await expect.poll(() => place(page)).toBe(stripArticle(feedPath));
  });

  test('back returns to the previous place', async ({ page }) => {
    await boot(page);
    await openStream(page, 'Unread');
    await expect.poll(() => place(page)).toBe('/unread');
    await openStream(page, 'Read later');
    await expect.poll(() => place(page)).toBe('/later');

    await page.goBack();
    // The entry for a place carries whatever article was open in it, because
    // opening one REPLACES the entry rather than adding to it — so this comes
    // back to /unread/read/<id> and `place` is what makes that assertable.
    // Returning to the stream AND to the piece being read in it is the rule
    // working, not a wrinkle in it.
    await expect.poll(() => place(page)).toBe('/unread');
    // Not just the address: the reader has to actually be there. An address that
    // moves without the app moving is the failure this whole feature is against.
    await expect(page.locator('.pane-list')).toContainText('Unread');

    await page.goForward();
    await expect.poll(() => place(page)).toBe('/later');
  });

  test('scrolling the stream does not fill the history', async ({ page }) => {
    // The push-versus-replace rule, from the outside. The current article changes
    // as the reader scrolls (A28), and if each one pushed, ONE Back would no
    // longer leave the feed — it would step through articles nobody navigated to.
    await boot(page);
    await openFeed(page, /Alpha Journal/);
    const feedRoot = stripArticle(await settled(page, /^\/feed\/[^/]+$/));

    // Move to a DIFFERENT article in the same feed.
    await openRow(page, 1);
    await expect(currentArticle(page).locator('h1')).toBeVisible();
    await expect.poll(() => path(page)).toMatch(/\/read\/[^/]+$/);

    // One Back, from an article inside the feed, leaves the feed entirely —
    // which is only true if moving between articles REPLACED rather than pushed.
    // If it pushed, this Back would land on the previous article and the reader
    // would need one press per article they had passed through.
    await page.goBack();
    await expect.poll(() => path(page)).not.toMatch(
      new RegExp('^' + feedRoot.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  });

  test('the settings surface has an address', async ({ page }) => {
    await page.goto('/settings/appearance');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    // Opened ON the settings surface rather than opening the reader and then
    // switching to it — a link to a tab that flashed the reader first would mean
    // the tab was applied after the first frame.
    await expect(page.locator('.pane-settings')).toBeVisible({ timeout: 30_000 });
    await expect.poll(() => path(page)).toBe('/settings/appearance');
  });

  test('an address this build cannot read opens the reader', async ({ page }) => {
    // Not a 404 screen: an address here is a request to look at something, not a
    // resource, so a link from an older build or a typo has to land somewhere
    // usable.
    await page.goto('/not-a-real-place');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
    // And the address is corrected to the place it actually landed on, so the
    // reader is not left holding a link that lies. It resumes the saved place, as
    // a bare address does, so what it settles on is whichever place that was —
    // but never the unreadable address itself.
    await expect.poll(() => path(page)).not.toContain('not-a-real-place');
  });

  test('an address naming a feed that is not there says so, and offers All articles', async ({ page }) => {
    await page.goto('/feed/NOTAREALID');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    // Not "your feeds haven't brought anything in yet", which tells somebody with
    // articles to read that nothing is wrong with the link.
    const list = page.locator('.pane-list');
    await expect(list.locator('.list-title')).toHaveText('Not found', { timeout: 30_000 });
    await expect(list.locator('.empty')).toContainText("That feed isn't here");
    await list.locator('.empty button', { hasText: 'All articles' }).click();
    await expect(list.locator('.list-title')).toHaveText('All articles');
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
  });

  // A tag or a folder is judged only once its list has arrived, and an account
  // with none must still get the answer: an empty list used to mean "not loaded
  // yet", so /tag/zzz on such an account read as a tag with nothing in it.
  for (const [route, noun] of [['/tag/zzz', 'tag'], ['/category/zzz', 'folder']]) {
    test(`an address naming a ${noun} that is not there says so, and offers All articles`, async ({ page }) => {
      await page.goto(route);
      await signIn(page);
      await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

      const list = page.locator('.pane-list');
      await expect(list.locator('.list-title')).toHaveText('Not found', { timeout: 30_000 });
      await expect(list.locator('.empty')).toContainText(`That ${noun} isn't here`);
      await list.locator('.empty button', { hasText: 'All articles' }).click();
      await expect(list.locator('.list-title')).toHaveText('All articles');
      await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
    });
  }

  test('an address this build cannot read resumes the saved place', async ({ page }) => {
    await boot(page);
    await openFeed(page, /Alpha Journal/);
    const feedPath = await settled(page, /^\/feed\/[^/]+$/);

    await page.goto('/this/does/not/exist');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
    // Where the reader was, not All with the saved place overwritten by it.
    await expect.poll(() => place(page)).toBe(stripArticle(feedPath));
  });

  test('a deep link loads its assets', async ({ page }) => {
    // The regression test for the <base href> in web/index.html. Every asset is
    // named relatively and the shell is served at every route, so without a
    // declared base `app.wasm` resolves under the route and 404s — the module
    // never instantiates and the page is blank. That failure is invisible from
    // "/", which is the only address anybody opens by hand.
    const missing = [];
    page.on('response', (r) => {
      if (r.status() === 404) missing.push(new URL(r.url()).pathname);
    });
    await page.goto('/unread/read/does-not-exist');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    expect(missing, 'assets 404ed on a deep link — is <base href> still in index.html?')
      .toEqual([]);
  });
});
