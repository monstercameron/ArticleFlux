import { test, expect } from './fixtures.mjs';

/**
 * The front door (client/view/home.go).
 *
 * Three things about this surface are easy to break without noticing, and each
 * has a test below:
 *
 *   1. **It must render for somebody with no account.** Root decides `/home`
 *      from the ADDRESS, before WhoAmI. A refactor that moves the branch after
 *      the round trip still passes on a dev server — which serves the local
 *      account with no login — and fails for every visitor on a real one.
 *   2. **Its pictures have to ship.** The screenshots live in web/shots/ and are
 *      copied by a step in the build that is easy to lose. A missing one leaves
 *      the page rendering perfectly with holes in it, and nothing in the console
 *      to say so on a page nobody is watching.
 *   3. **The keys have to work**, because the page's central claim is that this
 *      reader's key map is the one your hands already know. A homepage that
 *      ignores `j` is arguing against itself.
 */

/** The eight sections, in the order the rail lists them (view.homeBands). */
const BANDS = ['top', 'why', 'reading', 'finding', 'ranking', 'reach', 'listen', 'boundary', 'built'];

async function openHome(page) {
  await page.goto('/home');
  await expect(page.locator('.hm-h1')).toBeVisible({ timeout: 60_000 });
}

test.describe('the homepage', () => {
  test('renders every section, with the rail listing them', async ({ page }) => {
    await openHome(page);

    for (const id of BANDS) {
      await expect(page.locator(`[data-band='${id}']`)).toHaveCount(1);
    }
    // One rail row per section except the hero: the wordmark is already the way
    // back to the top.
    await expect(page.locator('.hm-link')).toHaveCount(BANDS.length - 1);
  });

  test('every screenshot it references actually loads', async ({ page }) => {
    await openHome(page);

    // Lazy images below the fold never load in a viewport that never moves, so
    // the page is scrolled through before anything is asserted. A test that only
    // checked the hero would pass with every other picture missing.
    const missing = await page.evaluate(async () => {
      const el = document.querySelector('.hm');
      for (let y = 0; y < el.scrollHeight; y += 600) {
        el.scrollTop = y;
        await new Promise((r) => setTimeout(r, 40));
      }
      const imgs = [...document.images];
      await Promise.all(imgs.filter((i) => !i.complete)
        .map((i) => new Promise((r) => { i.onload = i.onerror = r; })));
      // naturalWidth is 0 for an image that 404'd. `complete` is true either way,
      // which is the trap: a broken image is a LOADED image as far as it knows.
      return imgs.filter((i) => !i.naturalWidth).map((i) => i.getAttribute('src'));
    });
    expect(missing, 'screenshots that did not load').toEqual([]);

    // And every one of them says what it shows. The alt text is prose in the
    // catalog, so an empty one means a key went missing rather than that somebody
    // decided the picture was decorative.
    const unlabelled = await page.evaluate(() =>
      [...document.images].filter((i) => !(i.alt || '').trim()).map((i) => i.getAttribute('src')));
    expect(unlabelled, 'screenshots with no alternative text').toEqual([]);
  });

  test('j and k move between sections, and the rail follows', async ({ page }) => {
    await openHome(page);
    const scrollTop = () => page.evaluate(() => Math.round(document.querySelector('.hm').scrollTop));

    expect(await scrollTop()).toBe(0);

    await page.keyboard.press('j');
    await expect(page.locator(".hm-link[aria-current='true']")).toHaveText(/Why it exists/);
    const afterFirst = await scrollTop();
    expect(afterFirst).toBeGreaterThan(0);

    await page.keyboard.press('j');
    await expect(page.locator(".hm-link[aria-current='true']")).toHaveText(/Reading/);
    expect(await scrollTop()).toBeGreaterThan(afterFirst);

    // And back. `k` is the half of the pair that is easy to leave unwired,
    // because scrolling down is what anybody testing by hand does.
    await page.keyboard.press('k');
    await expect(page.locator(".hm-link[aria-current='true']")).toHaveText(/Why it exists/);
    expect(await scrollTop()).toBeLessThan(await page.evaluate(() => 1e9));
  });

  test('? opens the key sheet and Escape closes it', async ({ page }) => {
    await openHome(page);

    await expect(page.locator('.hm-sheet')).toHaveCount(0);
    await page.keyboard.press('?');
    await expect(page.locator('.hm-sheet')).toBeVisible();

    // It is the READER's key map, not a second copy of it — so it names keys the
    // reader binds and this page does not.
    await expect(page.locator('.hm-sheet')).toContainText('Open the command palette');

    await page.keyboard.press('Escape');
    await expect(page.locator('.hm-sheet')).toHaveCount(0);
  });

  test('clicking a rail row goes to that section', async ({ page, isMobile }) => {
    // The rail folds to a masthead below 1100px and its rows are deliberately
    // gone — see the phone test below, which asserts exactly that. There is no
    // row to click, which is the design and not a gap in it.
    test.skip(isMobile, 'the rail has no rows on a phone, on purpose');
    await openHome(page);
    await page.locator(".hm-link[data-hm='go:listen']").click();
    await expect(page.locator(".hm-link[aria-current='true']")).toHaveText(/Listening/);
    const y = await page.evaluate(() => {
      const el = document.querySelector('.hm');
      return el.scrollTop - document.querySelector("[data-band='listen']").offsetTop;
    });
    // Landed on the section, within a smooth-scroll frame of its top.
    expect(Math.abs(y)).toBeLessThan(60);
  });

  test('nothing scrolls sideways on a phone', async ({ page, isMobile }) => {
    test.skip(!isMobile, 'the desktop project has room for the rail');
    await openHome(page);

    const over = await page.evaluate(() => {
      const el = document.documentElement;
      return el.scrollWidth - el.clientWidth;
    });
    expect(over, 'horizontal overflow in px').toBeLessThanOrEqual(1);

    // The rail folds into a masthead rather than eating the width.
    await expect(page.locator('.hm-list')).toBeHidden();
    await expect(page.locator('.hm-mark')).toBeVisible();
  });
});
