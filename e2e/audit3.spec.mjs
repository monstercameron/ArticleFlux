import { test, expect, boot, openAddFeed, openRail, openRow, signIn } from './fixtures.mjs';
import { FEED_ORIGIN } from './ports.mjs';

/**
 * Audit 3's defects that are about what a reader's hands and eyes meet: where a
 * link goes, what adding a feed says, how many times Tab is pressed, where an
 * address leads, where focus lands, and what a slow boot shows.
 */

/** submitAdd fills and submits the add dialog; the caller says what to expect. */
async function submitAdd(page, url, title) {
  const dialog = await openAddFeed(page);
  await dialog.locator('[data-role="add-feed"]').fill(url);
  if (title) await dialog.locator('[data-role="add-feed-title"]').fill(title);
  await dialog.locator('[data-action="add-feed"]').click();
  return dialog;
}

/**
 * feedCount is how many subscriptions the rail lists — once the list has stopped
 * arriving. Read right after boot it can be a first page of the rail's feeds, so
 * it is read until two reads agree.
 */
async function feedCount(page) {
  const rail = await openRail(page);
  const rows = rail.locator('.feed-row:not(.stream-row):not(.topic-row):not(.tag-row)');
  let last = await rows.count();
  for (let i = 0; i < 12; i++) {
    await page.waitForTimeout(600);
    const now = await rows.count();
    if (now === last && now > 0) return now;
    last = now;
  }
  return last;
}

/** isNarrow: a phone or a tablet, where there is no Tab key and no sidebar to land in. */
function isNarrow(page) {
  return (page.viewportSize()?.width ?? 1400) <= 900;
}

/**
 * dropProbeFeeds takes every feed whose address contains `match` out of the
 * shared account, through the server and not through the interface.
 *
 * This is the cleanup that has to happen whatever else did: the suite shares one
 * account, and a probe feed left behind is an extra article in every spec that
 * runs afterwards (six slideshow tests went from "1 of 5" to "1 of 6" on one
 * leaked row). The interface route below — hover, settings, arm, confirm — is
 * four chances to fail inside a `finally` that is running BECAUSE something
 * failed. unsubscribeByTitle stays for the tests that are about unsubscribing.
 */
async function dropProbeFeeds(page, baseURL, match) {
  const res = await page.request.post(`${baseURL}/debug/unsubscribe?match=${encodeURIComponent(match)}`);
  if (!res.ok()) throw new Error(`/debug/unsubscribe answered ${res.status()}`);
}

/** unsubscribeByTitle takes a feed this suite added out of the account again. */
async function unsubscribeByTitle(page, title) {
  const rail = await openRail(page);
  const slot = rail.locator('.feed-slot', { hasText: title }).first();
  if (!(await slot.count())) return;
  await slot.hover();
  await slot.locator('[data-action="feed-settings"]').click({ force: true });
  const fs = page.getByRole('dialog', { name: 'Feed settings' });
  await fs.locator('[data-action="fs-unsubscribe"]').click();
  await fs.locator('[data-action="fs-unsubscribe-confirm"]').click();
  await expect(rail.locator('.feed-slot', { hasText: title })).toHaveCount(0, { timeout: 45_000 });
}

test.describe('adding a feed says what the server saw (H5, H8)', () => {
  const cases = [
    ['a 404', `${FEED_ORIGIN}/no-such-feed.xml`, /HTTP 404/],
    ['a feed that is broken', `${FEED_ORIGIN}/broken.xml`, /malformed/i],
    ['a host that does not answer', 'http://127.0.0.1:9/feed.xml', /could not connect/i],
  ];
  for (const [name, url, says] of cases) {
    test(`${name} is refused in the dialog, not subscribed`, async ({ page }) => {
      await boot(page);
      const before = await feedCount(page);
      const dialog = await submitAdd(page, url);
      const err = dialog.locator('[role="alert"]');
      await expect(err).toContainText(says, { timeout: 45_000 });
      // Refused: the dialog stays, with the address in it, and the field it is
      // about is marked and focused.
      await expect(dialog).toBeVisible();
      await expect(dialog.locator('#af-url')).toHaveAttribute('aria-invalid', 'true');
      await expect(dialog.locator('#af-url')).toBeFocused();
      // And it does NOT then claim the page publishes no feed: that contradicts
      // the reason just given (broken, 404 and unreachable are about the feed).
      await expect(dialog.getByText(/No feed here/)).toHaveCount(0);
      await page.keyboard.press('Escape');
      expect(await feedCount(page)).toBe(before);
    });
  }

  test('text that is not an address is refused in words about addresses', async ({ page }) => {
    await boot(page);
    const dialog = await submitAdd(page, 'not a url at all');
    await expect(dialog.locator('[role="alert"]')).toContainText(/web address/i);
    await expect(dialog.locator('[role="alert"]')).not.toContainText(/went wrong/i);
    await expect(dialog.locator('#af-url')).toHaveAttribute('aria-invalid', 'true');
  });
});

test.describe('links in an article body leave in a new tab (H4)', () => {
  test('clicking one opens it elsewhere and the app stays', async ({ page, context, baseURL }, testInfo) => {
    const title = `Links probe ${testInfo.project.name}`;
    await context.route('**://elsewhere.example/**', (r) => r.fulfill({ body: 'elsewhere', contentType: 'text/html' }));
    // Anything an earlier, failed attempt left: gone before this one counts.
    await dropProbeFeeds(page, baseURL, '/with-links.xml');
    await boot(page);
    const before = await feedCount(page);
    try {
      const dialog = await submitAdd(page, `${FEED_ORIGIN}/with-links.xml?p=${testInfo.project.name}`, title);
      await expect(dialog).toBeHidden({ timeout: 45_000 });
      const rail = await openRail(page);
      await rail.locator('.feed-row', { hasText: title }).first().click();
      await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 20_000 });
      await openRow(page, 0);
      const link = page.locator('.article-body a[href^="https://elsewhere.example"]');
      await expect(link).toBeVisible({ timeout: 20_000 });
      const here = page.url();
      const [popup] = await Promise.all([context.waitForEvent('page', { timeout: 15_000 }), link.click()]);
      await popup.close();
      // The app's own tab did not navigate, and the link says how it was opened.
      expect(page.url()).toBe(here);
      await expect(link).toHaveAttribute('target', '_blank');
      await expect(link).toHaveAttribute('rel', /noopener/);
      await expect(link).toHaveAttribute('rel', /noreferrer/);
    } finally {
      // The suite shares one account: leave nothing behind, whatever happened
      // above — through the server, so this cannot fail the way the page did.
      await dropProbeFeeds(page, baseURL, '/with-links.xml');
    }
    await page.goto('/');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    expect(await feedCount(page)).toBe(before);
  });
});

test.describe('the article list is one Tab stop (H11)', () => {
  test('one row takes Tab, the rest take the arrows', async ({ page }) => {
    test.skip(isNarrow(page), 'a phone has no Tab key to count');
    await boot(page);
    expect(await page.locator('.item-row').count()).toBeGreaterThan(2);
    // Exactly one row is in the tab order.
    await expect(page.locator('.item-row:not([tabindex="-1"])')).toHaveCount(1);
    await page.locator('.item-row[tabindex="0"]').focus();
    await page.keyboard.press('ArrowDown');
    expect(await page.evaluate(() => document.activeElement.classList.contains('item-row'))).toBe(true);
    // Tab leaves the list instead of stepping to the next row.
    await page.keyboard.press('Tab');
    expect(await page.evaluate(() => document.activeElement.classList.contains('item-row'))).toBe(false);
    // And it is still exactly one stop after all that.
    await expect(page.locator('.item-row:not([tabindex="-1"])')).toHaveCount(1);
  });
});

test.describe('a topic has an address (H12)', () => {
  test('/topic/<slug> opens that topic, survives a reload, and a made-up one says so', async ({ page }) => {
    await page.goto('/topic/software');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.list-title')).toContainText(/Software/i, { timeout: 30_000 });
    await page.reload();
    await expect(page.locator('.list-title')).toContainText(/Software/i, { timeout: 60_000 });
    expect(new URL(page.url()).pathname).toBe('/topic/software');

    await page.goto('/topic/zzz-not-a-topic');
    await expect(page.locator('[data-role="missing"]')).toContainText(/topic isn't here/i, { timeout: 60_000 });
  });

  test('clicking a topic row writes its address', async ({ page }) => {
    await boot(page);
    const rail = await openRail(page);
    const row = rail.locator('.topic-row[data-category-slug]').first();
    test.skip(!(await row.count()), 'no classified articles on this instance, so the rail shows no topics yet (H19)');
    const slug = await row.getAttribute('data-category-slug');
    await row.click();
    // The topic, and then whichever article the reader opened on arriving — it
    // opens the first unread one, so the address is the topic's or the topic's
    // with /read/<id> after it, depending on whether anything in it is unread.
    // The first version of this demanded the bare form and failed on CI, where
    // the fixture's Software article is still unread at this point.
    await expect.poll(() => new URL(page.url()).pathname, { timeout: 15_000 })
      .toMatch(new RegExp(`^/topic/${slug}(/read/[^/]+)?$`));
  });
});

test.describe('focus goes somewhere real (H13)', () => {
  test('leaving the slideshow with Escape does not drop focus on the page', async ({ page }) => {
    test.skip(isNarrow(page), 'Escape is a keyboard path');
    await boot(page);
    const opener = page.getByRole('button', { name: /^Slideshow$/ });
    await expect(async () => {
      await opener.focus();
      await opener.click();
      await expect(page.locator('.slides')).toBeVisible({ timeout: 2000 });
    }).toPass({ timeout: 30_000 });
    await page.keyboard.press('Escape');
    await expect(page.locator('.slides')).toHaveCount(0);
    await expect.poll(() => page.evaluate(() => document.activeElement === document.body), { timeout: 5000 }).toBe(false);
  });

  test('an add that closes the dialog gives focus back, and an unsubscribe leaves it in the sidebar', async ({ page, baseURL }, testInfo) => {
    test.skip(isNarrow(page), 'a phone has no focus ring to lose');
    await dropProbeFeeds(page, baseURL, '/beta.xml?focus=');
    await boot(page);
    const before = await feedCount(page);
    const title = `Focus probe ${testInfo.project.name}`;
    try {
      const dialog = await submitAdd(page, `${FEED_ORIGIN}/beta.xml?focus=${testInfo.project.name}`, title);
      await expect(dialog).toBeHidden({ timeout: 45_000 });
      await expect.poll(() => page.evaluate(() => document.activeElement?.getAttribute('data-action')), { timeout: 5000 })
        .toBe('add-feed-open');
      // Through the interface this time: the unsubscribe is what is being tested.
      await unsubscribeByTitle(page, title);
      // The gear went with its row: focus is in the sidebar, not on <body>.
      await expect.poll(() => page.evaluate(() => !!document.activeElement?.closest('.pane-rail')), { timeout: 5000 }).toBe(true);
      expect(await feedCount(page)).toBe(before);
    } finally {
      await dropProbeFeeds(page, baseURL, '/beta.xml?focus=');
    }
  });
});

test.describe('a failed first round trip does not show the sign-in screen (H15)', () => {
  test('the first connection dropping leaves the splash up and then the reader', async ({ page }) => {
    // Signed in first, so a token is held: what is tested is a reader who IS
    // signed in meeting a bad first connection, not the first-ever visit.
    await boot(page);
    await page.addInitScript(() => {
      window.__sawLogin = false;
      new MutationObserver(() => {
        if (document.querySelector('[data-phase="login"]')) window.__sawLogin = true;
      }).observe(document, { childList: true, subtree: true });
    });
    let sockets = 0;
    await page.routeWebSocket(/\/grpc/, (ws) => {
      sockets += 1;
      // The first is refused outright; the redial goes through.
      if (sockets === 1) { ws.close({ code: 1011, reason: 'first round trip fails' }); return; }
      ws.connectToServer();
    });
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 90_000 });
    expect(sockets).toBeGreaterThan(1);
    expect(await page.evaluate(() => window.__sawLogin)).toBe(false);
  });
});

test.describe('settings on a phone and offline (H16, H18)', () => {
  test('on a phone the tab menu leaves room for the panel above the fold', async ({ page }) => {
    test.skip(!isNarrow(page), 'only a phone stacks the tab menu above the panel');
    await page.goto('/settings/discover');
    await signIn(page);
    await expect(page.locator('.set-panel')).toBeVisible({ timeout: 60_000 });
    const height = page.viewportSize().height;
    const top = async () => (await page.locator('.set-panel').boundingBox())?.y ?? 9999;
    // It began at y=500 of 915 behind a wrapped block of fourteen pills; each
    // group is a single scrolling line now.
    await expect.poll(top, { timeout: 10_000 }).toBeLessThan(height * 0.5);
    // The current tab is in view in its line, however far along it sits.
    const tab = page.locator('.set-tab[aria-current="true"]');
    const box = await tab.boundingBox();
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(page.viewportSize().width + 1);
    // And choosing another tab leaves the panel on screen.
    await page.locator('[data-action="settings-tab"][data-value="data"]').click();
    await expect(page.locator('[data-action="settings-tab"][data-value="data"]')).toHaveAttribute('aria-current', 'true');
    await expect.poll(top, { timeout: 10_000 }).toBeLessThan(height * 0.5);
  });

  test('a tab opened offline says it will load, and loads when the connection is back', async ({ page, context }) => {
    test.skip(isNarrow(page), 'one viewport is enough for the sentence');
    await boot(page);
    await context.setOffline(true);
    await page.waitForTimeout(1500);
    await page.keyboard.press(',');
    await page.locator('[data-action="settings-tab"][data-value="server"]').click();
    await expect(page.locator('.set-panel')).toContainText(/connection is down/i, { timeout: 15_000 });
    await expect(page.locator('.set-panel .sk')).toHaveCount(0);
    await context.setOffline(false);
    await expect(page.locator('.set-panel')).not.toContainText(/connection is down/i, { timeout: 60_000 });
    await expect(page.locator('.set-panel')).toContainText(/version|uptime|database/i, { timeout: 60_000 });
  });
});
