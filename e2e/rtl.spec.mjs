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
    // lang describes the words on screen and dir the layout. This instance has no
    // key, so no Arabic is cached and every string is the English fallback: the
    // document says so, while the layout really is mirrored.
    expect(await page.evaluate(() => document.documentElement.lang)).toBe('en');
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

  // The interface's direction is not the article's. Text took its direction from
  // its own letters through CSS, but list markers, blockquote bars and indents
  // follow the BOX, so an English article read in Arabic had its bullets on the
  // wrong side of every line. The article body, and its headline, resolve their
  // own direction (dir="auto") — an English one left to right, an Arabic one
  // right to left, in the same interface.
  test('an article takes its direction from its own text', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();
    const body = page.locator('.article-body').first();
    await expect(body).toHaveAttribute('dir', 'auto', { timeout: 30_000 });
    await expect(page.locator('.article h1').first()).toHaveAttribute('dir', 'auto');

    const dirs = await body.evaluate((el) => {
      const probe = (html) => {
        el.innerHTML = html;
        return ['ul', 'ol', 'blockquote'].map((t) => getComputedStyle(el.querySelector(t)).direction);
      };
      return {
        english: probe('<p>An English paragraph.</p><ul><li>one</li></ul><ol><li>two</li></ol><blockquote>quoted</blockquote>'),
        arabic: probe('<p>فقرة عربية.</p><ul><li>واحد</li></ul><ol><li>اثنان</li></ol><blockquote>اقتباس</blockquote>'),
      };
    });
    expect(dirs.english).toEqual(['ltr', 'ltr', 'ltr']);
    expect(dirs.arabic).toEqual(['rtl', 'rtl', 'rtl']);
  });

  // H9. With no key the catalog stays English inside a mirrored layout, and every
  // run of text was laid out right to left: ".Everything this reader does...",
  // "+Smart", "OF 10 2", "K Ctrl". One rule on the root (data-fallback) lets each
  // run take its direction from its own letters while the layout stays mirrored.
  //
  // Asserted as rendered positions, not as the CSS property: what a reader sees is
  // the order of the words, and the property is only how it is meant to get there.
  test('English fallback text keeps its own order and its keys their own', async ({ page }) => {
    await boot(page);
    await expect.poll(() => page.evaluate(() => document.documentElement.dir)).toBe('rtl');
    expect(await page.evaluate(() => document.documentElement.dataset.fallback)).toBe('true');

    // Where, on screen, a word of an element's text starts: its left edge.
    const wordLeft = (sel, word) => page.evaluate(([sel, word]) => {
      const root = document.querySelector(sel);
      const walk = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const at = n.textContent.indexOf(word);
        if (at < 0) continue;
        const r = document.createRange();
        r.setStart(n, at);
        r.setEnd(n, at + word.length);
        return r.getBoundingClientRect().left;
      }
      return null;
    }, [sel, word]);

    // The palette's footer, "move · Enter open · Esc close", in reading order from
    // the left. It read "move · Enter open · Esc close" from the right. A touch
    // screen's footer is a different sentence, with no keys in it, so the
    // palette's half is a desktop claim.
    if (!(await isPhone(page))) {
      await page.keyboard.press('Control+k');
      await expect(page.locator('.pal-foot')).toBeVisible();
      expect(await page.locator('.pal-foot').innerText()).toMatch(/move.*open.*close/);
      const [move, open, close] = await Promise.all(
        ['move', 'open', 'close'].map((w) => wordLeft('.pal-foot', w)));
      expect(move, 'the footer reads right to left').toBeLessThan(open);
      expect(open, 'the footer reads right to left').toBeLessThan(close);
      // The field's own hint ends with its ellipsis, at the END of the line.
      expect(await page.evaluate(() =>
        getComputedStyle(document.querySelector('.pal input'), '::placeholder').unicodeBidi))
        .toBe('plaintext');
      await page.keyboard.press('Escape');
    }

    // The shortcut sheet: a combination's chips in the order they are typed. The
    // chips are siblings in a flex row, which the mirror reverses; keys are not
    // language, so they are carved out of it.
    // By the button rather than the key: a key pressed straight after the palette
    // closes lands before focus has come back to the page.
    await page.getByRole('button', { name: 'Keyboard shortcuts' }).first().click();
    await expect(page.locator('.help')).toBeVisible();
    const combo = await page.evaluate(() => {
      const row = [...document.querySelectorAll('.help-keys')].find((k) => k.querySelectorAll('.kbd').length === 2);
      return [...row.querySelectorAll('.kbd')].map((k) => [k.textContent, k.getBoundingClientRect().left]);
    });
    expect(combo.length).toBe(2);
    expect(combo[0][1], `${combo[0][0]} ${combo[1][0]} reads backwards`).toBeLessThan(combo[1][1]);
    await page.keyboard.press('Escape');
  });

  test('the slideshow lays an English story out left to right in an Arabic interface', async ({ page }) => {
    await boot(page);
    await page.getByRole('button', { name: /^Slideshow$/ }).click();
    await expect(page.locator('.slides[data-phase="read"]')).toBeVisible({ timeout: 60_000 });
    // The per-article rule the reading pane has: the body and the headline resolve
    // their own direction, so English is not laid out against the wrong edge with
    // its punctuation on the wrong side.
    await expect(page.locator('.slide-body')).toHaveAttribute('dir', 'auto');
    await expect(page.locator('.slide-head')).toHaveAttribute('dir', 'auto');
    const d = await page.evaluate(() => [
      getComputedStyle(document.querySelector('.slide-body')).direction,
      getComputedStyle(document.querySelector('.slide-head')).direction,
    ]);
    expect(d).toEqual(['ltr', 'ltr']);
    // And the counter in the slug reads "1 of 5", not "5 of 1" or "OF 5 1".
    const order = page.locator('.slide-order');
    await expect(order).toHaveText(/^\d+ of \d+$/);
    expect(await order.evaluate((el) => getComputedStyle(el).unicodeBidi)).toBe('plaintext');
    await page.keyboard.press('Escape');
  });

  // H10. Arrow keys follow the screen. In this interface the Next button is on the
  // LEFT of the transport and Previous on the right, so the left arrow is next.
  test('in the slideshow the arrows go where they point', async ({ page }) => {
    await boot(page);
    await page.getByRole('button', { name: /^Slideshow$/ }).click();
    await expect(page.locator('.slides')).toBeVisible({ timeout: 60_000 });
    await page.mouse.move(900, 400);

    // The transport mirrors, and its two marks with it: Next is drawn on the left
    // and its mark points left; Previous on the right, pointing right.
    const hud = await page.evaluate(() => {
      const at = (action) => {
        const b = document.querySelector(`.slide-btn[data-action="${action}"]`);
        return { left: b.getBoundingClientRect().left, flip: getComputedStyle(b.firstElementChild).transform };
      };
      return { prev: at('slide-prev'), next: at('slide-next') };
    });
    expect(hud.next.left, 'Next is not on the left of Previous').toBeLessThan(hud.prev.left);
    expect(hud.next.flip, 'Next still points the left-to-right way').toBe('matrix(-1, 0, 0, 1, 0, 0)');
    expect(hud.prev.flip, 'Previous still points the left-to-right way').toBe('matrix(-1, 0, 0, 1, 0, 0)');

    const order = page.locator('.slide-order');
    await expect(order).toHaveText('1 of 5');
    await page.keyboard.press('ArrowLeft');
    await expect(order).toHaveText('2 of 5');
    await page.keyboard.press('ArrowRight');
    await expect(order).toHaveText('1 of 5');
    // j and k name the step, not a side, and are unchanged.
    await page.keyboard.press('j');
    await expect(order).toHaveText('2 of 5');
    await page.keyboard.press('Escape');
  });

  // The pinned bar's Previous and Next: Previous is on the right and points right,
  // Next on the left and points left, whichever way the document reads.
  test('the article bar\'s Previous and Next point the way reading goes', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();
    const step = page.locator('.article-step').first();
    await expect(step).toBeVisible({ timeout: 30_000 });
    const at = await step.evaluate((el) => {
      const one = (label) => {
        const b = el.querySelector(`button[aria-label="${label}"]`);
        const g = b.querySelector('.gl') || b.firstElementChild;
        return { left: b.getBoundingClientRect().left, glyph: g.textContent, flip: getComputedStyle(g).transform };
      };
      return { prev: one('Previous article'), next: one('Next article') };
    });
    expect(at.next.left, 'Next is not on the left').toBeLessThan(at.prev.left);
    // Drawn mirrored: the stored mark is the left-to-right one and the transform
    // turns it round, so what is on screen is "‹" for Next and "›" for Previous.
    expect(at.prev.flip).toBe('matrix(-1, 0, 0, 1, 0, 0)');
    expect(at.next.flip).toBe('matrix(-1, 0, 0, 1, 0, 0)');
  });

  test('in the sidebar the left arrow reaches the gear that is drawn on the left', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'desktop', 'a phone has no key-driven rail');
    await boot(page);
    // Parked off the sidebar: a pointer resting on a row reveals its gear and would
    // move the focus ring for reasons that are not the key.
    await page.mouse.move(900, 400);
    const row = page.locator('.feed-slot:has(.feed-gear) .feed-row').first();
    await row.focus();
    const left = (loc) => loc.evaluate((el) => el.getBoundingClientRect().left);
    const gear = page.locator('.feed-slot:has(.feed-gear) .feed-gear').first();
    expect(await left(gear), 'the gear is not on the left of its row').toBeLessThan(await left(row) + 40);
    await page.keyboard.press('ArrowRight');
    expect(await page.evaluate(() => document.activeElement.className),
      'the right arrow must not reach a gear drawn on the left').not.toMatch(/feed-gear/);
    await row.focus();
    await page.keyboard.press('ArrowLeft');
    await expect(page.locator('.feed-gear:focus')).toHaveCount(1);
  });
});
