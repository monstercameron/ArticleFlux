import { test, expect, boot, signIn, isPhone } from './fixtures.mjs';

/**
 * Arabic, as a layout: a smoke check that a right-to-left interface is MIRRORED
 * rather than merely right-aligned.
 *
 * The locale is preseeded the way client/i18n says a test should — under
 * i18n.PersistenceKey, read by GWC's UseLocale — before the app boots. This
 * instance has no Smart+ key, so the catalog cannot be translated and the WORDS
 * stay English. That is fine and deliberate: direction comes from the locale
 * (i18n.DirectionFor), not from the strings, so everything below is about
 * where things are, which is the part a missing translation cannot hide.
 *
 * Four claims, each a thing that has gone wrong in some RTL port somewhere:
 * the root says rtl; nothing scrolls sideways at either width; the rail is on
 * the RIGHT on a desktop; and on a phone the strip slides the mirrored way,
 * because every pane's transform multiplies by `--dirx`, which is -1 under
 * [dir="rtl"] (client/design/filmstrip.go).
 */

const LOCALE_KEY = 'articleflux.locale';

/**
 * shell loads the app as far as its layout, and no further.
 *
 * Not boot(), which also waits for the connection to go live and for rows to
 * arrive. Under any non-English locale on this build it never does — the badge
 * stays at "connecting" with the tunnel open (the last test below, filed as a
 * product bug) — and the layout claims here do not depend on data, so they are
 * not held hostage to it.
 */
async function shell(page) {
  await page.goto('/');
  await signIn(page);
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
  await expect.poll(() => page.evaluate(() => document.documentElement.dir)).toBe('rtl');
}

test.describe('right to left', () => {
  test.beforeEach(async ({ page }) => {
    // Before ANY page script, so the very first render already reads it. Each
    // test has its own browser context, so this never leaks into another spec.
    await page.addInitScript((key) => {
      try { localStorage.setItem(key, 'ar'); } catch { /* private mode */ }
    }, LOCALE_KEY);
  });

  test('the document is right-to-left', async ({ page }) => {
    await shell(page);
    await expect.poll(() => page.evaluate(() => document.documentElement.lang)).toBe('ar');
    // And the mirror sign the panes read is in force, not just the attribute.
    expect(await page.evaluate(() =>
      getComputedStyle(document.querySelector('.shell')).getPropertyValue('--dirx').trim()))
      .toBe('-1');
  });

  test('nothing scrolls sideways', async ({ page }) => {
    await shell(page);
    // The page itself, and the shell inside it: an RTL bug usually shows up as
    // something positioned with `left` that is now hanging off the far edge,
    // which widens the document without anything looking broken at a glance.
    const overflow = await page.evaluate(() => {
      const d = document.documentElement;
      const shell = document.querySelector('.shell');
      return {
        doc: d.scrollWidth - d.clientWidth,
        shell: shell ? shell.scrollWidth - shell.clientWidth : 0,
      };
    });
    expect(overflow.doc, 'the document is wider than the viewport').toBeLessThanOrEqual(0);
    expect(overflow.shell, 'the shell is wider than itself').toBeLessThanOrEqual(0);
  });

  test('the rail is on the right', async ({ page }, testInfo) => {
    // A desktop claim: below 900px one pane shows at a time, so there is no
    // "side" for the rail to be on — the phone's version is the slide below.
    test.skip(testInfo.project.name !== 'desktop', 'a phone shows one pane, so there is no side');
    await shell(page);
    const rail = await page.locator('.pane-rail').boundingBox();
    const list = await page.locator('.pane-list').boundingBox();
    const article = await page.locator('.pane-article').boundingBox();
    const width = page.viewportSize().width;
    // Rail rightmost, then the list, then the article at the left: the reading
    // order of the three panes, mirrored.
    expect(rail.x, 'the rail is not to the right of the list').toBeGreaterThan(list.x);
    expect(list.x, 'the list is not to the right of the article').toBeGreaterThan(article.x);
    expect(Math.round(rail.x + rail.width), 'the rail does not reach the right edge')
      .toBeGreaterThanOrEqual(width - 2);
  });

  test('on a phone, the strip slides the mirrored way', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name === 'desktop', 'the strip only exists below 900px');
    await shell(page);
    expect(await isPhone(page)).toBe(true);

    // The translation each pane has once the slide has finished: the e of its
    // computed matrix, in pixels.
    const tx = (sel) => page.locator(sel).evaluate((el) => {
      const m = getComputedStyle(el).transform;
      return m === 'none' ? 0 : new DOMMatrixReadOnly(m).m41;
    });

    // From the list to the Feeds tab: SHALLOWER, so the rail comes in and the
    // list goes out. By the tab rather than by opening an article, because the
    // tab is local state and an article needs rows, which this build does not
    // deliver under a non-English locale (see the last test).
    //
    // Left-to-right, the rail sits to the LEFT of the list and the list leaves
    // to the right (positive). Mirrored, the rail is to the list's right, so the
    // list has to leave to the LEFT — a negative translation is the mirror
    // working, and a positive one is the left-to-right strip under Arabic.
    await page.locator('.tabbar [data-action="tab-feeds"]').click();
    await expect(page.locator('.shell')).toHaveAttribute('data-view', 'rail');
    await expect.poll(() => tx('.pane-rail'), { timeout: 10_000 }).toBe(0);
    expect(await tx('.pane-list'), 'the list left the wrong way for rtl').toBeLessThan(0);
    expect(await tx('.pane-article'), 'the article left the wrong way for rtl').toBeLessThan(0);
  });

  // The one claim here about behaviour rather than layout, and it fails on the
  // 11:22 build: with `articleflux.locale` set to anything but English (ar and
  // fr both checked), the tunnel opens and the badge stays at "connecting" with
  // no rows for as long as anyone waits; English on the same server is live in
  // seconds. Kept as a failing test, not skipped, because it is the bug — a
  // reader who picks a language must still be able to read.
  test('a non-English interface still connects and lists articles', async ({ page }) => {
    await boot(page);
  });
});
