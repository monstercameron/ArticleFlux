import { test, expect, boot } from './fixtures.mjs';

/**
 * The folded rail: the feeds put away at the edge of the window, and brought
 * back by pointing at where they went.
 *
 * The claims, each of which is a way this feature could be on screen and wrong:
 *
 *   - Folding gives the width to the list and the article. A rail that hid and
 *     left its column empty would pass an attribute check and fail the reader.
 *   - Pointing at the strip brings the rail back OVER the list. If it re-opened
 *     its column instead, every glance at a feed name would move the article
 *     being read, twice.
 *   - It goes away again when the pointer leaves — including after a feed was
 *     clicked, which leaves focus on a button inside the rail. That is the case
 *     `:focus-within` gets wrong, and it is the common one.
 *   - A keyboard reaches it: focus arriving by Tab opens it.
 *   - The choice survives a reload.
 *
 * Geometry is read from the layout, not from class names: the rail is "open"
 * when its box starts at the window's edge and "folded" when all but a strip of
 * it is outside the window.
 */

const STRIP = 14; // design.RailPeek

const box = async (page, sel) => page.locator(sel).first().boundingBox();
const left = async (page, sel) => Math.round((await box(page, sel)).x);
const right = async (page, sel) => {
  const b = await box(page, sel);
  return Math.round(b.x + b.width);
};

/** The rail's visible width: how much of it is inside the window. */
const railShowing = (page) => right(page, '.pane-rail');

async function fold(page) {
  await page.keyboard.press('b');
  await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'folded');
  await expect.poll(() => railShowing(page)).toBeLessThanOrEqual(STRIP + 1);
}

test.describe('the folded rail', () => {
  test.beforeEach(async ({}, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop',
      'the rail is a pane of its own on a phone; there is nothing beside it to give the width to');
  });

  test('b folds the rail to a strip and the list and the article take the width', async ({ page }) => {
    await boot(page);
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'open');
    const listBefore = await left(page, '.pane-list');
    const articleBefore = (await box(page, '.pane-article')).width;

    await fold(page);

    // The list starts where the strip ends, not where the rail used to.
    await expect.poll(() => left(page, '.pane-list')).toBe(STRIP);
    expect(listBefore).toBeGreaterThan(200);
    // And the article, not an empty column, got what the rail gave up.
    await expect.poll(async () => (await box(page, '.pane-article')).width)
      .toBeGreaterThan(articleBefore + 200);

    // The grip that resized the rail is not a tab stop for a column that is gone.
    await expect(page.locator('.grip-rail')).toBeHidden();

    // b again brings it back into its column.
    await page.keyboard.press('b');
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'open');
    await expect.poll(() => left(page, '.pane-list')).toBe(listBefore);
  });

  test('pointing at the strip slides the rail out over the list, and leaving puts it away', async ({ page }) => {
    await boot(page);
    await fold(page);
    const list = await left(page, '.pane-list');
    const article = await left(page, '.pane-article');

    await page.mouse.move(4, 400);
    await expect.poll(() => left(page, '.pane-rail')).toBe(0);
    // Over, not beside: nothing the reader was looking at moved.
    expect(await left(page, '.pane-list')).toBe(list);
    expect(await left(page, '.pane-article')).toBe(article);
    // And it is the rail that is on top — a feed is clickable where the list is.
    await expect(page.locator('.pane-rail .feed-row').first()).toBeVisible();

    await page.mouse.move(900, 400);
    await expect.poll(() => railShowing(page)).toBeLessThanOrEqual(STRIP + 1);
  });

  test('choosing a feed from the slid-out rail does not pin it open', async ({ page }) => {
    await boot(page);
    await fold(page);
    await page.mouse.move(4, 400);
    await expect.poll(() => left(page, '.pane-rail')).toBe(0);

    // A click leaves focus on the feed's button. If "open" were keyed to
    // :focus-within, the rail would now sit over the list until the reader
    // clicked something else.
    const feed = page.locator('.pane-rail .feed-row').first();
    await feed.click();
    await expect(feed).toHaveAttribute('aria-current', 'true');

    await page.mouse.move(900, 400);
    await expect.poll(() => railShowing(page)).toBeLessThanOrEqual(STRIP + 1);
  });

  test('focus arriving by keyboard opens it', async ({ page }) => {
    await boot(page);
    await fold(page);
    // Park the pointer well away from the edge, so only the keyboard can be
    // what opens it.
    await page.mouse.move(900, 400);

    // Tab until focus is inside the rail. It is first in the document after the
    // skip link, so this is a handful of presses at most.
    for (let i = 0; i < 8; i++) {
      await page.keyboard.press('Tab');
      if (await page.evaluate(() => !!document.activeElement?.closest('.pane-rail'))) break;
    }
    expect(await page.evaluate(() => !!document.activeElement?.closest('.pane-rail'))).toBe(true);
    await expect.poll(() => left(page, '.pane-rail')).toBe(0);
  });

  test('the button in the rail says what it will do, and keeps the rail open', async ({ page }) => {
    await boot(page);
    const button = page.locator('[data-action="toggle-rail"]');
    await expect(button).toHaveAttribute('aria-pressed', 'false');
    await expect(button).toHaveAccessibleName('Fold the feeds away');

    await button.click();
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'folded');
    // The pointer is still over the rail, so it has not gone anywhere yet —
    // and the button under it now offers the way back.
    await expect(button).toHaveAttribute('aria-pressed', 'true');
    await expect(button).toHaveAccessibleName('Keep the feeds open');

    await button.click();
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'open');
  });

  test('a folded rail is still folded after a reload', async ({ page }) => {
    await boot(page);
    await fold(page);
    // The preference is written through the same path as every other view
    // preference, over the tunnel; give the write a moment to leave before the
    // page that is sending it goes away.
    await page.waitForTimeout(400);
    await page.reload();
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'folded');
    await expect.poll(() => railShowing(page)).toBeLessThanOrEqual(STRIP + 1);

    // Leave the shared account as the other specs expect to find it.
    await page.mouse.move(900, 400);
    await page.keyboard.press('b');
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'open');
    await page.waitForTimeout(400);
  });

  test('focus mode still owns the window when the rail is folded', async ({ page }) => {
    await boot(page);
    await fold(page);
    await page.keyboard.press('w');
    await expect(page.locator('.shell')).toHaveAttribute('data-focus', 'true');
    // Not even the strip: focus mode closes the rail's track outright, and a
    // strip that slid a rail over a centred article would be the two layouts
    // answering at once.
    await expect.poll(async () => (await box(page, '.pane-article')).x).toBe(0);
    await page.keyboard.press('w');
    await expect(page.locator('.shell')).toHaveAttribute('data-focus', 'false');
    await page.keyboard.press('b');
    await expect(page.locator('.shell')).toHaveAttribute('data-rail', 'open');
  });
});
