import { test, expect, boot } from './fixtures.mjs';

/**
 * The motion system, asserted against the running application.
 *
 * `client/design/sheet_test.go` already proves the things a stylesheet can be
 * wrong about on its own — no dangling token, no ungated duration, no literal
 * colour, no theme below AA. What it cannot see is whether a gesture actually
 * *happens*: every one of those checks passes on a sheet whose animations never
 * fire, because the markup stopped carrying the attribute the rule keys off.
 *
 * That is not hypothetical. Three motion bugs on the way in were exactly this
 * shape, and all three were invisible to the Go tests and to a screenshot:
 *
 *   - the list cursor's state attribute was written with a LOCALISED string, so
 *     `[data-cursor='false']` would have stopped matching in any other language;
 *   - the cursor was opt-out, so it sat on the first skeleton row of every load;
 *   - `transition` is a shorthand, so a second rule naming only `opacity`
 *     silently dropped the filmstrip's transform — the incoming pane slid and
 *     the outgoing one vanished.
 *
 * So these tests read computed style out of a real browser mid-gesture. They are
 * a ratchet: each one names a thing the reader would feel, and none of them
 * asserts a duration or a curve, because those are the sheet's business and
 * pinning them here would make every tuning pass a test edit.
 */

/** mid returns the computed value of `prop` on `sel` a beat into an interaction. */
async function styleOf(page, sel, prop, pseudo = null) {
  return page.evaluate(
    ([s, p, pe]) => getComputedStyle(document.querySelector(s), pe).getPropertyValue(p),
    [sel, prop, pseudo],
  );
}

/** translateX of an element, in px, from its computed transform matrix. */
async function offsetX(page, sel) {
  return page.evaluate((s) => {
    const el = document.querySelector(s);
    if (!el) return null;
    return Math.round(new DOMMatrixReadOnly(getComputedStyle(el).transform).m41);
  }, sel);
}

test.describe('motion', () => {
  // Where the reader was is ACCOUNT STATE (A30): scope, article and every filter
  // are server-side prefs, restored on connect. So a test that ends inside an
  // article decides what the next spec file boots into — and at phone widths
  // that means the item list is off the strip and unclickable, which is a
  // confusing way to learn about test isolation. Every case here hands the app
  // back on the list.
  test.afterEach(async ({ page }) => {
    // Twice: the first peels focus mode if it is on, the second returns to the
    // list. Both are no-ops when there is nothing to undo.
    await page.keyboard.press('Escape').catch(() => {});
    await page.keyboard.press('Escape').catch(() => {});
    await page.waitForTimeout(300);
  });

  test('the reduce-motion switch reaches the stylesheet, in both directions', async ({ page }) => {
    await boot(page);

    // --mo is the whole gate: every duration in the application is written
    // calc(var(--mo) * t), so this one number decides whether anything moves.
    const mo = () => page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue('--mo').trim());
    const chipDuration = () => styleOf(page, '.chip', 'transition-duration');

    await page.evaluate(() => document.documentElement.setAttribute('data-motion', 'reduced'));
    expect(await mo()).toBe('0');
    // Not "some transitions are shorter" — every one of them is zero.
    expect((await chipDuration()).split(',').every((d) => d.trim() === '0s')).toBe(true);

    // And back ON, which is the half a media query alone could never do: a
    // reader who wants motion on a machine configured to suppress it.
    await page.evaluate(() => document.documentElement.setAttribute('data-motion', 'full'));
    expect(await mo()).toBe('1');
    expect((await chipDuration()).split(',').some((d) => d.trim() !== '0s')).toBe(true);
  });

  test('the selection cursor travels with the list rather than cross-fading', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();
    await expect(page.locator('.list-scroll')).toHaveAttribute('data-cursor', 'true');

    const at = () => page.evaluate(() =>
      document.querySelector('.list-scroll').style.getPropertyValue('--cursor'));
    const start = await at();

    await page.keyboard.press('j');
    await page.waitForTimeout(500);
    const next = await at();
    expect(next).not.toBe(start);

    // It is ONE mark that moves: the row itself must not paint a selected
    // background, or there would be two of them lighting and unlighting.
    const rowBg = await styleOf(page, `.item-row[aria-current='true']`, 'background-color');
    expect(['rgba(0, 0, 0, 0)', 'transparent']).toContain(rowBg.trim());

    // The cursor lives in the scroller's CONTENT space, which is what lets it
    // scroll with the rows for free. Its offset must not change when the list
    // scrolls underneath it.
    const before = await styleOf(page, '.list-scroll', 'transform', '::before');
    await page.evaluate(() => { document.querySelector('.list-scroll').scrollTop += 200; });
    await page.waitForTimeout(300);
    expect(await styleOf(page, '.list-scroll', 'transform', '::before')).toBe(before);
  });

  test('a row animates when it is NEW, not when it scrolls into view', async ({ page }) => {
    await boot(page);
    // The list is virtualised. If arrival were a plain mount animation, every
    // row that scrolled past would fire one — a slot machine at any real speed,
    // and the list would twitch under the reader's hand on every press of j.
    const freshCount = () => page.locator(`.item-row[data-fresh='true']`).count();

    // Waited FOR rather than slept through. The spawn window is 900ms after the
    // page lands, but when the page lands depends on how long the boot took —
    // and a fixed sleep that is long enough alone becomes a flake the moment
    // this file runs after another one.
    await expect.poll(freshCount, { timeout: 10_000 }).toBe(0);

    await page.evaluate(() => { document.querySelector('.list-scroll').scrollTop += 900; });
    await page.waitForTimeout(600);
    expect(await freshCount()).toBe(0);

    // Marking read rebuilds the list out of items that are all already present.
    await page.evaluate(() => { document.querySelector('.list-scroll').scrollTop = 0; });
    await page.waitForTimeout(300);
    await page.locator('.item-row').first().click();
    await page.waitForTimeout(900);
    expect(await freshCount()).toBe(0);
  });

  test('a dialog leaves as well as arriving, and is untabbable when closed', async ({ page }) => {
    await boot(page);
    // `:has(.pal)` rather than `.first()`: all six dialogs are in the document
    // at all times now — that is what gives them exits — so a bare `.pal-scrim`
    // resolves to whichever comes first in source order, which is the help
    // sheet. A selector that used to be unambiguous is not any more, and that is
    // worth knowing about before some other spec trips over it.
    const scrim = page.locator('.pal-scrim:has(.pal)');

    // Closed: present, so it has something to animate with — and hidden, so it
    // is out of the accessibility tree and out of the tab order. opacity alone
    // would leave a reader tabbing into a palette that is not there.
    await expect(scrim).toHaveAttribute('data-open', 'false');
    expect((await styleOf(page, '.pal-scrim:has(.pal)', 'visibility')).trim()).toBe('hidden');

    // Pressed until it takes. The document-level key listener is attached in an
    // effect that runs after the first render, so there is a window — a few tens
    // of milliseconds after the list paints — where a keystroke lands on nothing.
    // A person cannot hit it; a test that fires the instant `.item-row` appears
    // hits it about one run in four, and only when something slower ran first.
    await expect.poll(async () => {
      if ((await scrim.getAttribute('data-open')) !== 'true') {
        await page.keyboard.press('Control+k');
      }
      return scrim.getAttribute('data-open');
    }, { timeout: 10_000 }).toBe('true');
    await page.waitForTimeout(400);
    expect((await styleOf(page, '.pal-scrim:has(.pal)', 'visibility')).trim()).toBe('visible');

    // Leaving is brisk on purpose: the transition a browser runs is the one
    // belonging to the state being moved TO, so the exit is allowed to be
    // quicker than the entrance without a line of JavaScript.
    const enter = await styleOf(page, '.pal', 'transition-duration');
    await page.keyboard.press('Escape');
    // Wait for the state to actually flip before reading: the client handles the
    // key through PostAsync, so reading immediately reads the OPEN rule and the
    // assertion below would compare a duration against itself.
    await expect(scrim).toHaveAttribute('data-open', 'false');
    const exit = await styleOf(page, '.pal', 'transition-duration');
    const ms = (v) => Math.max(...v.split(',').map((d) => parseFloat(d) * (d.includes('ms') ? 1 : 1000)));
    expect(ms(exit)).toBeLessThan(ms(enter));

    await page.waitForTimeout(500);
    await expect(scrim).toHaveAttribute('data-open', 'false');
    expect((await styleOf(page, '.pal-scrim:has(.pal)', 'visibility')).trim()).toBe('hidden');
  });

  test('the panes are a strip on a phone, and BOTH of them move', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 780 });
    await boot(page);

    const w = 390;
    expect(await offsetX(page, '.pane-list')).toBe(0);
    expect(await offsetX(page, '.pane-rail')).toBe(-w);

    // There is no "article" tab — the tab bar is home/feeds/notes/settings, and
    // an article is reached by tapping a row, which is the gesture worth
    // asserting anyway because it is the one a reader performs.
    await page.locator('.item-row').first().click();
    // Mid-gesture: the outgoing pane has to still be on screen and still moving,
    // which is the half that a `transition: opacity` elsewhere silently ate.
    await page.waitForTimeout(120);
    const outgoing = await offsetX(page, '.pane-list');
    expect(outgoing).toBeLessThan(0);
    expect(outgoing).toBeGreaterThan(-w);
    expect((await styleOf(page, '.pane-list', 'visibility')).trim()).toBe('visible');

    await page.waitForTimeout(600);
    expect(await offsetX(page, '.pane-article')).toBe(0);
    expect((await styleOf(page, '.pane-list', 'visibility')).trim()).toBe('hidden');

    // Nothing may hang off the side of the screen at any point in the gesture.
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBe(0);
  });

  test('focus mode closes the columns rather than hiding them', async ({ page }) => {
    await boot(page);
    const railWidth = () => page.evaluate(() =>
      Math.round(document.querySelector('.pane-rail').getBoundingClientRect().width));

    const open = await railWidth();
    expect(open).toBeGreaterThan(0);

    // Sampled across the whole gesture rather than at one instant. A fixed
    // sample is a race: the key goes through the client's async dispatch before
    // the attribute flips, so 120ms is sometimes before the transition has even
    // started. What is actually being asserted is that the rail passed THROUGH
    // intermediate widths — which is the difference between a grid track
    // interpolating and a pane being switched off.
    // The sampler is started BEFORE the key, and runs long enough to cover a
    // dropped one. Firing the press first and then sampling loses the race two
    // ways: the transition can begin during the round trip, and the keystroke
    // itself can land before the document listener is attached (see the palette
    // above) — in which case nothing ever moves and the sample is all-open.
    const sampler = page.evaluate(async () => {
      const rail = document.querySelector('.pane-rail');
      const seen = [];
      for (let i = 0; i < 150; i++) {
        seen.push(Math.round(rail.getBoundingClientRect().width));
        await new Promise((r) => requestAnimationFrame(r));
      }
      return seen;
    });
    await page.keyboard.press('w');
    await page.waitForTimeout(400);
    if ((await page.locator('.shell').getAttribute('data-focus')) !== 'true') {
      await page.keyboard.press('w');
    }
    const widths = await sampler;

    expect(widths.some((v) => v > 0 && v < open),
      `the rail went ${open} -> 0 without passing through anything between; ` +
      `grid tracks are switching rather than interpolating`).toBe(true);
    await page.waitForTimeout(600);
    expect(await railWidth()).toBe(0);

    await page.keyboard.press('Escape');
    await page.waitForTimeout(600);
    expect(await railWidth()).toBe(open);
  });
});
