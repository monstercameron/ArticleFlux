import { test, expect, boot } from './fixtures.mjs';

/**
 * The slideshow (§19).
 *
 * Almost everything about this mode is unreachable from a unit test: the timing
 * arithmetic is covered natively in client/view/slideshow_test.go, but whether
 * the title card actually rises into a header, whether the scroll distance was
 * measured from a laid-out DOM, and whether the whole surface leaves when it is
 * told to are all facts about a browser.
 *
 * Two things are deliberately NOT asserted here:
 *
 *   - **Fullscreen.** Headless Chromium reports `fullscreenElement` unreliably
 *     and the request is refused outside a user gesture in ways that differ per
 *     platform. The mode is built to be correct without it — the overlay covers
 *     the viewport either way — so a test that pinned it would be testing the
 *     harness rather than the feature.
 *   - **The wake lock.** There is nothing observable from the page: the API
 *     returns a sentinel and the screen either sleeps or does not, hours later.
 *
 * The rAF note in reader.spec applies here more than anywhere else in the suite:
 * headless Chromium throttles animation hard when nothing is being clicked, and
 * this mode is entirely animation. Every wait for a phase change is generous for
 * that reason, not because the timings are uncertain.
 */

/** slides is the overlay, which does not exist at all until the mode starts. */
const slides = (page) => page.locator('.slides');

/** startShow opens the slideshow from the list header and waits for a story. */
async function startShow(page) {
  await page.getByRole('button', { name: /^Slideshow$/ }).click();
  await expect(slides(page)).toBeVisible();
  await expect(page.locator('.slide-head')).not.toBeEmpty();
}

/**
 * openSettings opens the settings surface and waits for it, rather than for the
 * control that was clicked.
 *
 * Waiting for the PANE is the point. `boot` returns as soon as the shell is
 * painted, and the settings surface is dispatched through the delegated click
 * listener — so a click that lands in the window between the two does nothing at
 * all, and the failure arrives later as "the setting did not save".
 */
async function openSettings(page) {
  await page.locator('.list-tools').getByRole('button', { name: 'Settings' }).click();
  await expect(page.locator('.pane-settings')).toBeVisible();
}

test.describe('slideshow', () => {
  test('starts on the feed being read, one story filling the screen', async ({ page }) => {
    await boot(page);
    const firstHeadline = await page.locator('.item-title').first().innerText();

    await startShow(page);

    // The story it opens on is the one the reader was already looking at — not
    // the top of the list, and not an arbitrary one. Nothing has been clicked
    // yet, so that is the first row.
    await expect(page.locator('.slide-head')).toHaveText(firstHeadline);

    // The slug carries who and when, and the running order. The order is the one
    // piece of furniture here that has to be TRUE rather than decorative: five
    // fixture items, this is the first.
    await expect(page.locator('.slide-source')).toContainText(/Alpha Journal|Beta Notes/);
    await expect(page.locator('.slide-order')).toHaveText('1 of 5');

    // It is a mode, not a dialog: it covers the viewport rather than sitting in
    // it. A box that merely appeared over the reader would still let a click
    // reach the list underneath.
    const box = await slides(page).boundingBox();
    const view = page.viewportSize();
    expect(box.width).toBeGreaterThanOrEqual(view.width - 1);
    expect(box.height).toBeGreaterThanOrEqual(view.height - 1);
  });

  test('the title card holds, then opens onto the story', async ({ page }) => {
    await boot(page);
    await startShow(page);

    // The card first. This is the whole opening gesture: the headline is up and
    // the body is not, so there is one thing on screen to read.
    await expect(slides(page)).toHaveAttribute('data-phase', 'card');

    // Then the story arrives under it — the same element, the same headline,
    // rather than a second screen. `data-phase` is what the stylesheet animates
    // from, so asserting on it is asserting on the transition itself.
    await expect(slides(page)).toHaveAttribute('data-phase', 'read', { timeout: 30_000 });
    await expect(page.locator('.slide-body')).toBeVisible();
    await expect(page.locator('.slide-body')).toContainText(/n-gram table|\w{6,}/);
  });

  test('the rule fills as the story runs', async ({ page }) => {
    await boot(page);
    await startShow(page);

    const fill = () => slides(page).evaluate(
      (el) => parseFloat(getComputedStyle(el).getPropertyValue('--fill')) || 0);

    // It starts empty. This is also the regression guard for the seam: the fill
    // is a keyed element precisely so it does not inherit the previous story's
    // value and glide backwards.
    expect(await fill()).toBeLessThan(0.2);

    await expect.poll(fill, { timeout: 30_000 }).toBeGreaterThan(0.25);
  });

  test('pause stops the clock, and the space bar is the control', async ({ page }) => {
    await boot(page);
    await startShow(page);

    await page.keyboard.press('Space');
    await expect(slides(page)).toHaveAttribute('data-paused', 'true');

    const fill = () => slides(page).evaluate(
      (el) => parseFloat(getComputedStyle(el).getPropertyValue('--fill')) || 0);
    const held = await fill();
    await page.waitForTimeout(3000);
    // A paused display has to actually stop. The tolerance covers the one tick
    // that may already have been in flight when the key landed.
    expect(await fill()).toBeLessThan(held + 0.05);

    await page.keyboard.press('Space');
    await expect(slides(page)).toHaveAttribute('data-paused', 'false');
  });

  test('the arrows move between stories, and the order follows', async ({ page }) => {
    await boot(page);
    await startShow(page);
    await expect(page.locator('.slide-order')).toHaveText('1 of 5');
    const first = await page.locator('.slide-head').innerText();

    await page.keyboard.press('ArrowRight');
    await expect(page.locator('.slide-order')).toHaveText('2 of 5');
    await expect(page.locator('.slide-head')).not.toHaveText(first);

    await page.keyboard.press('ArrowLeft');
    await expect(page.locator('.slide-order')).toHaveText('1 of 5');
    await expect(page.locator('.slide-head')).toHaveText(first);

    // It LOOPS rather than stopping, because the mode is something you leave
    // running: reaching the end and going dark is the display turning itself off
    // at some point during the afternoon.
    await page.keyboard.press('ArrowLeft');
    await expect(page.locator('.slide-order')).toHaveText('5 of 5');
  });

  test('escape leaves, and the reader is where the show got to', async ({ page }) => {
    await boot(page);
    await startShow(page);
    await page.keyboard.press('ArrowRight');
    await expect(page.locator('.slide-order')).toHaveText('2 of 5');

    await page.keyboard.press('Escape');

    // Gone entirely, not hidden: it holds a parsed article body and a
    // full-screen gradient, and a reader who left the mode should be paying for
    // neither.
    await expect(slides(page)).toHaveCount(0);
    await expect(page.locator('.pane-list')).toBeVisible();
  });

  test('while it runs it owns the keyboard', async ({ page }) => {
    await boot(page);
    await startShow(page);

    // The palette is the sharpest version of the test: Ctrl+K is handled ABOVE
    // the typing guard and from anywhere, so if any binding survives the mode it
    // is this one. A dialog opening behind a fullscreen overlay is a control the
    // reader cannot get back to.
    await page.keyboard.press('Control+k');
    await expect(page.locator('.pal-scrim[data-open="true"]')).toHaveCount(0);

    // And a plain letter that means something outside: `u` toggles unread-only.
    await page.keyboard.press('u');
    await expect(slides(page)).toBeVisible();
  });

  test('the transport is reachable and hidden until it is wanted', async ({ page, isMobile }) => {
    // The HUD reveals itself on approach, and there is no approach on a
    // touchscreen — a tap either presses a button or it does not. Skipped rather
    // than adapted, because the thing under test here IS the hover reveal.
    test.skip(isMobile, 'the reveal is a pointer gesture');
    await boot(page);
    await startShow(page);

    const hud = page.locator('.slide-hud');
    // Present for the keyboard and for a pointer that comes looking, invisible
    // to someone watching from across the room.
    await expect(hud).toHaveCount(1);
    expect(await hud.evaluate((el) => getComputedStyle(el).opacity)).toBe('0');

    await hud.hover();
    await expect.poll(
      () => hud.evaluate((el) => getComputedStyle(el).opacity),
      { timeout: 10_000 }).toBe('1');

    await page.getByRole('button', { name: 'Next story' }).click();
    await expect(page.locator('.slide-order')).toHaveText('2 of 5');

    await page.getByRole('button', { name: 'Leave the slideshow' }).click();
    await expect(slides(page)).toHaveCount(0);
  });

  test('the pace is a saved preference', async ({ page }) => {
    await boot(page);

    await openSettings(page);
    await page.getByRole('button', { name: /^30 sec$/ }).click();
    await expect(page.getByRole('button', { name: /^30 sec$/ }))
      .toHaveAttribute('aria-pressed', 'true');

    // Server-side, like every other preference here: someone who set a pace on
    // the laptop in the kitchen has decided how they like the news, not how this
    // browser behaves. A reload is what proves it left the tab.
    await page.reload();
    await boot(page);
    await openSettings(page);
    await expect(page.getByRole('button', { name: /^30 sec$/ }))
      .toHaveAttribute('aria-pressed', 'true');
  });
});
