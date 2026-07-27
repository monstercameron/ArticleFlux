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

/**
 * startShow opens the slideshow from the list header and waits for a story.
 *
 * The click is retried until the overlay appears, and that is about the harness
 * rather than the feature. Every control in this app is dispatched through one
 * delegated listener registered in an effect, so a click that lands between the
 * shell painting and that effect running does nothing at all — `boot` waits for
 * the connection and the first row, neither of which implies the listener. On an
 * unloaded machine the window is invisible; on this one, with two agents and a
 * browser competing, it is occasionally real.
 *
 * `toPass` rather than a fixed second sleep: it retries at the speed the machine
 * is actually running at instead of at the speed one was once measured at.
 */
async function startShow(page) {
  await expect(async () => {
    await page.getByRole('button', { name: /^Slideshow$/ }).click();
    await expect(slides(page)).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 30_000 });
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

  // The transport used to be revealed by `:hover` on its own corner, which does
  // not work and could not: a fullscreen element is permanently hovered the
  // moment the pointer is anywhere inside it, so hover-to-reveal was either
  // always-on or a hunt for one specific corner — and on a touchscreen there is
  // no hover at all, which left it unreachable on a phone entirely. It reveals
  // on MOVEMENT now, which is what every video player does and what someone
  // reaching to pause a narrator will try.
  test('the transport shows on the way in, fades, and comes back on movement',
    async ({ page }) => {
      await boot(page);
      await startShow(page);
      const hud = page.locator('.slide-hud');
      const shown = () => slides(page).getAttribute('data-hud');

      // Revealed by movement. Deliberately NOT asserting the reveal that happens
      // when the mode opens: the linger is four seconds and `startShow` waits on
      // a cold wasm boot, so on a loaded machine it can legitimately have faded
      // before the first assertion runs. That made this test flaky and told
      // nobody anything — the mechanism is what matters, and it is below.
      await page.mouse.move(300, 300);
      await page.mouse.move(320, 340);
      await expect.poll(shown, { timeout: 10_000 }).toBe('true');

      // Then out of the way. This is a display meant to be left running.
      await expect.poll(shown, { timeout: 20_000 }).toBe('false');
      expect(await hud.evaluate((el) => getComputedStyle(el).opacity)).toBe('0');
      // And unclickable while invisible, or the strip swallows clicks aimed at
      // the picture behind it.
      expect(await hud.evaluate((el) => getComputedStyle(el).pointerEvents)).toBe('none');

      // Back on movement, anywhere on the surface rather than in one corner —
      // which is the whole difference from the `:hover` version this replaced.
      await page.mouse.move(200, 200);
      await page.mouse.move(240, 260);
      await expect.poll(shown, { timeout: 10_000 }).toBe('true');

      await page.getByRole('button', { name: 'Next story' }).click();
      await expect(page.locator('.slide-order')).toHaveText('2 of 5');

      await page.getByRole('button', { name: 'Leave the slideshow' }).click();
      await expect(slides(page)).toHaveCount(0);
    });

  // Pausing is the one time the controls must NOT fade: the reader stopped the
  // show to decide something, and the answer is usually one of the buttons
  // beside the one they pressed.
  test('a paused show keeps its controls', async ({ page }) => {
    await boot(page);
    await startShow(page);

    await page.keyboard.press('Space');
    await expect(slides(page)).toHaveAttribute('data-paused', 'true');
    await expect(slides(page)).toHaveAttribute('data-hud', 'true');

    // Well past the linger, with nothing moving.
    await page.waitForTimeout(6000);
    await expect(slides(page)).toHaveAttribute('data-hud', 'true');
    expect(await page.locator('.slide-hud').evaluate((el) => getComputedStyle(el).opacity))
      .toBe('1');

    // Resuming lets it fade again.
    await page.keyboard.press('Space');
    await expect(slides(page)).toHaveAttribute('data-paused', 'false');
    await expect.poll(() => slides(page).getAttribute('data-hud'),
      { timeout: 20_000 }).toBe('false');
  });

  // The regression this file most needs.
  //
  // Read to me used to switch the clock OFF and hand pacing to the narrator. When
  // the narrator never started — no Smart+ voice, no key on the server, a refused
  // ticket, a failed synthesis — nothing was left to drive the display: it froze
  // on its first title card, the headline never opened onto the story, the scroll
  // never ran, and the browser's own voice read the article underneath it. There
  // was no error, because nothing had errored; the mode had simply removed its
  // own clock. The reader's notice banner said why, and sat behind the overlay.
  //
  // This instance has no OpenAI key and the account has not opted in, which is
  // exactly that case.
  test('read to me without the Smart+ voice keeps running, and leads to the settings', async ({ page }) => {
    await boot(page);
    await startShow(page);

    await page.keyboard.press('v');

    // It says why, on the slide, as a BUTTON. The requirements themselves are no
    // longer a panel over the show — four preference switches inside a
    // fullscreen mode is the one place they are both most in the way and hardest
    // to find again afterwards.
    const line = page.locator('.slide-voice');
    await expect(line).toBeVisible();
    await expect(line).toContainText(/read to me/i);

    // And the show goes on underneath it. Both halves matter: the phase reaching
    // `read` is the headline having animated into its header and the story having
    // opened, and the fill moving is the clock still running.
    await expect(slides(page)).toHaveAttribute('data-phase', 'read', { timeout: 40_000 });
    await expect(page.locator('.slide-body')).toBeVisible();
    await expect.poll(
      () => slides(page).evaluate(
        (el) => parseFloat(getComputedStyle(el).getPropertyValue('--fill')) || 0),
      { timeout: 30_000 }).toBeGreaterThan(0.25);

    // Pressing the line ends the show and lands on Settings -> Podcast, which is
    // where every one of those switches lives.
    await line.click();
    await expect(slides(page)).toHaveCount(0);
    const panel = page.locator('.set-panel');
    await expect(panel).toBeVisible();
    await expect(panel.locator('.pc-need')).toHaveCount(4);

    // This instance has no key, so the one requirement the reader cannot fix
    // says so in words rather than offering a switch that could not work.
    await expect(panel.locator('.pc-need[data-on="false"]').last())
      .toContainText(/not on this server/i);
    // Start refuses rather than disappearing: a button that vanishes leaves the
    // reader hunting for it.
    await expect(page.getByRole('button', { name: /Start reading to me/i }))
      .toHaveAttribute('aria-disabled', 'true');

    // The switches are real. Flipping one here is flipping it everywhere -- no
    // staged copy, no Apply.
    const smartRow = panel.locator('.pc-need').first();
    await smartRow.getByRole('button').click();
    await expect(smartRow).toHaveAttribute('data-on', 'true');
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
  /**
   * The music catalogue comes down the TUNNEL, not from a URL (§19).
   *
   * That decision is invisible until it breaks, and when it breaks it breaks
   * silently: the picker renders, the show runs, and there is simply no music.
   * So this test asserts the one thing that proves the RPC answered — the
   * tracks are named on the screen, with names only the server knows.
   *
   * It also asserts what is NOT offered. The openings and the beds are different
   * recordings for different jobs, and only the beds are choosable: an opening
   * played as background is a mix nobody can hear the news over.
   */
  test('the background music is named by the server, and only the beds are offered', async ({ page }) => {
    await boot(page);
    await openSettings(page);
    await page.getByRole('button', { name: 'Listening' }).click();

    const music = page.getByRole('group', { name: /Opening sting and music/i });
    await expect(music).toBeVisible();

    // Both takes of the bed, by name. These strings live in the server's table
    // and nowhere in the client, so seeing them here means the stream ran.
    await expect(music.getByRole('button', { name: /^Late Night Patchcord$/ })).toBeVisible();
    await expect(music.getByRole('button', { name: /^Late Night Patchcord II$/ })).toBeVisible();
    // And silence, for anyone who would rather have it.
    await expect(music.getByRole('button', { name: /^off$/ })).toBeVisible();
    // The openings are not choices. They play at the top of a broadcast and
    // nowhere else.
    await expect(music.getByRole('button', { name: /Signal and Ideas/ })).toHaveCount(0);
    await expect(music.getByRole('button', { name: /Midnight Thought Loop/ })).toHaveCount(0);

    // Choosing one is a saved preference like every other here.
    await music.getByRole('button', { name: /^Late Night Patchcord II$/ }).click();
    await expect(music.getByRole('button', { name: /^Late Night Patchcord II$/ }))
      .toHaveAttribute('aria-pressed', 'true');
    await page.reload();
    await boot(page);
    await openSettings(page);
    await page.getByRole('button', { name: 'Listening' }).click();
    await expect(page.getByRole('group', { name: /Opening sting and music/i })
      .getByRole('button', { name: /^Late Night Patchcord II$/ }))
      .toHaveAttribute('aria-pressed', 'true');
  });
});
