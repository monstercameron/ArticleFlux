import { test, expect, boot, openFeed } from './fixtures.mjs';

/**
 * Responsive behaviour, asserted rather than eyeballed.
 *
 * The rule these enforce: a screen that is only correct at desktop width is not
 * finished. Every one of these caught a real bug the first time it ran — the
 * topbar's non-shrinking inputs were forcing the whole page wider than the
 * phone viewport, which then let list titles run off the screen.
 */


// The single most valuable responsive assertion there is: nothing may make the
// page scroll sideways. It catches a whole class of layout bugs that are
// invisible in a screenshot taken at the wrong scroll position.
/**
 * Nothing may stick out sideways — where "stick out" means a reader can see or
 * scroll to it.
 *
 * The element sweep is the useful half of this check: `scrollWidth` alone misses
 * an overflowing child inside a clipping ancestor, which is exactly the shape of
 * the long-unbroken-URL bug it was written for. But a box whose rect lies past
 * the viewport is only a defect if something can actually reveal it, and since
 * the panes became a filmstrip (plan §20.22) that is no longer a safe
 * assumption: two of the three panes sit a full viewport off to the side ON
 * PURPOSE, clipped by `.panes`, waiting to slide in.
 *
 * So the sweep asks the question it always meant: is this box outside the
 * viewport AND not clipped by an ancestor that hides it? A clipped box is
 * furniture; an unclipped one is a bug.
 */
async function expectNoHorizontalOverflow(page) {
  const overflow = await page.evaluate(() => {
    const d = document.documentElement;

    // Is this box's excess hidden by something?
    //
    // The question is not "is the element off-screen" — mid-slide a pane
    // legitimately straddles the edge — but "can this ever make the PAGE scroll
    // sideways". It cannot if some ancestor clips horizontally and that ancestor
    // itself stays inside the viewport: whatever sticks out is cut off there.
    //
    // This still catches what the check was written for. `.pane` sets only
    // `overflow-y`, which computes `overflow-x: auto` — scrollable, not clipping
    // — so a long unbroken URL widening the article column is still reported.
    const clippedInsideViewport = (el) => {
      for (let a = el.parentElement; a; a = a.parentElement) {
        const s = getComputedStyle(a);
        if (s.overflowX !== 'hidden' && s.overflowX !== 'clip') continue;
        if (a.getBoundingClientRect().right <= d.clientWidth + 1) return true;
      }
      return false;
    };

    return {
      scrollW: d.scrollWidth,
      clientW: d.clientWidth,
      offenders: [...document.querySelectorAll('*')]
        .filter((el) => el.getBoundingClientRect().right > d.clientWidth + 1)
        .filter((el) => !clippedInsideViewport(el))
        .slice(0, 5)
        .map((el) => el.className || el.tagName),
    };
  });
  expect(overflow.offenders, `elements overflowing the viewport: ${overflow.offenders}`)
    .toEqual([]);
  // The other half, unchanged and still the one a reader would feel: the page
  // itself must not scroll sideways.
  expect(overflow.scrollW).toBeLessThanOrEqual(overflow.clientW + 1);
}

const widths = [
  { name: 'phone-small', width: 320, height: 700 },
  { name: 'phone', width: 390, height: 844 },
  { name: 'phone-large', width: 430, height: 932 },
  { name: 'tablet', width: 768, height: 1024 },
  { name: 'laptop', width: 1280, height: 800 },
  { name: 'wide', width: 1920, height: 1080 },
];

for (const w of widths) {
  test(`no horizontal overflow at ${w.name} (${w.width}px)`, async ({ page }) => {
    await page.setViewportSize({ width: w.width, height: w.height });
    await boot(page);
    await expectNoHorizontalOverflow(page);

    // And in the article view, which renders arbitrary publisher HTML — the
    // most likely thing to break out of the column.
    await page.locator('.item-row').first().click();
    // .first(): the reading pane is a STREAM (A28), so opening an article can
    // leave several in it — and on a phone it now reliably does, because the
    // pane is laid out at every width since the filmstrip landed and can
    // finally measure whether its own bottom is in view. A bare `.article h1`
    // is a strict-mode violation; this is TODO 8b.34's failure shape (a).
    await expect(page.locator('.article h1').first()).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });
}

test('below 720px exactly one pane is visible at a time', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await boot(page);

  // A three-pane layout squeezed onto 390px is three unusable panes, so the
  // phone shows one and navigates between them.
  await expect(page.locator('.pane-list')).toBeVisible();
  await expect(page.locator('.pane-article')).toBeHidden();

  await page.locator('.item-row').first().click();
  await expect(page.locator('.pane-article')).toBeVisible();
  await expect(page.locator('.pane-list')).toBeHidden();

  // And back, which is the half people forget.
  await page.getByRole('button', { name: '‹ List' }).click();
  await expect(page.locator('.pane-list')).toBeVisible();
  await expect(page.locator('.pane-article')).toBeHidden();
});

test('at 1080px and up all three panes are visible together', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await boot(page);

  await expect(page.locator('.pane-rail')).toBeVisible();
  await expect(page.locator('.pane-list')).toBeVisible();
  await expect(page.locator('.pane-article')).toBeVisible();

  // Clicking must not swap panes on a wide screen — that would be the phone's
  // behaviour leaking onto the desktop.
  await page.locator('.item-row').first().click();
  await expect(page.locator('.pane-list')).toBeVisible();
  await expect(page.locator('.pane-rail')).toBeVisible();
});

test('the connection indicator survives the narrowest viewport', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 700 });
  await boot(page);
  // Other chrome is allowed to hide at 320px. This is not: it is the only thing
  // that distinguishes "quiet news day" from "silently disconnected".
  await expect(page.locator('.conn').first()).toBeVisible();
});

test('long unbroken text wraps instead of overflowing', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 700 });
  await boot(page);
  // The beta fixture carries an 80-character unbroken token precisely to test
  // this: -webkit-line-clamp truncates lines, it does not create them.
  await openFeed(page, /Beta Notes/);
  await expectNoHorizontalOverflow(page);
});

/**
 * Reduced motion, asserted against the mechanism that now implements it (A39,
 * plan §20.16).
 *
 * This used to count elements with a non-zero `animation-duration` and require
 * zero, which was the right check for the `* { animation: none }` rule it was
 * written against. That rule is gone, and deliberately: it could only ever
 * answer the machine, so a reader who wanted motion on a system configured to
 * suppress it had no way to say so, and it was a broom that quietly stopped
 * covering anything it could not reach.
 *
 * The gate is now `--mo`, a multiplier every duration is written through. What
 * replaces the count is stricter, not looser:
 *
 *   - the gate itself is off;
 *   - EVERY transition in the document takes zero time — no exceptions;
 *   - the only animations still running are the ambient loops, which keep a real
 *     duration ON PURPOSE (a zero-second infinite animation is a spec corner
 *     browsers disagree about) and are gated on amplitude instead, animating to
 *     the value they started from. They are named here, so a new unbounded
 *     animation fails this test rather than joining the exemption silently.
 */
test('reduced motion is respected', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await boot(page);

  // The reader has to ASK to follow the machine, and this test now asks.
  //
  // A39 removed the `prefers-reduced-motion` rule from the sheet on purpose: an
  // unset preference means full motion "on every machine", and only the explicit
  // `system` choice consults the OS. So emulating the media query alone proves
  // nothing — and this test was passing only because an earlier test in the file
  // had left the preference behind, which stopped once `reset-state` began
  // clearing `user_prefs`.
  //
  // Setting it here is the honest version: it is what a reader does, and it
  // makes the test say what it is actually about — that CHOOSING to follow a
  // machine which asks for less motion stops everything.
  await page.locator(`[data-action='open-settings']`).first().click();
  await page.locator(`[data-action='settings-tab'][data-value='appearance']`).click();
  await page.locator(`[data-action='motion-system']`).click();
  await expect(page.locator(`[data-action='motion-system']`)).toHaveCount(0);
  await page.keyboard.press('Escape');

  expect(await page.evaluate(() =>
    getComputedStyle(document.documentElement).getPropertyValue('--mo').trim())).toBe('0');

  const moving = await page.evaluate(() => {
    // The ambient loops, by the class that carries them.
    const AMBIENT = ['sk', 'sk-row', 'conn-dot', 'sync-gl', 'fs-saving', 'spin-ring',
      'is-loading', 'banner'];
    const out = { transitions: [], animations: [] };
    for (const el of document.querySelectorAll('*')) {
      const s = getComputedStyle(el);
      if (s.transitionDuration.split(',').some((d) => parseFloat(d) > 0)) {
        out.transitions.push(el.className || el.tagName);
      }
      if (s.animationName !== 'none' && parseFloat(s.animationDuration) > 0) {
        const cls = (el.className || '').toString().split(/\s+/);
        if (!cls.some((c) => AMBIENT.includes(c))) {
          out.animations.push(el.className || el.tagName);
        }
      }
    }
    out.transitions = out.transitions.slice(0, 5);
    out.animations = out.animations.slice(0, 5);
    return out;
  });

  expect(moving.transitions,
    `these still transition with motion reduced: ${moving.transitions}`).toEqual([]);
  expect(moving.animations,
    `these animate with motion reduced and are not a known ambient loop: ${moving.animations}`)
    .toEqual([]);
});

test('keyboard focus is visible', async ({ page }) => {
  await boot(page);
  await page.keyboard.press('Tab');
  const outline = await page.evaluate(() => {
    const el = document.activeElement;
    if (!el) return null;
    const s = getComputedStyle(el);
    return { width: s.outlineWidth, style: s.outlineStyle };
  });
  // A reader whose primary interface is the keyboard cannot have an invisible
  // focus ring.
  expect(outline).not.toBeNull();
  expect(outline.style).not.toBe('none');
});

// Navigation must never be hidden by a "make room on small screens" rule.
// An earlier `.btn-ghost { display:none }` at ≤559px also hid "‹ Feeds", which
// on a phone is the only route from the list back to the sidebar — a secondary
// action can be traded away, the sole means of navigation cannot.
test('phone navigation survives the narrow-viewport chrome rules', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 700 });
  await boot(page);

  const back = page.getByRole('button', { name: '‹ Feeds' });
  await expect(back).toBeVisible();
  await back.click();
  await expect(page.locator('.pane-rail')).toBeVisible();

  // And from the sidebar into a feed, and from the article back again — the
  // full round trip, because a one-way door is the same bug wearing a hat.
  await openFeed(page, /Alpha Journal/);
  await page.locator('.item-row').first().click();
  await expect(page.locator('.pane-article')).toBeVisible();
  await page.getByRole('button', { name: '‹ List' }).click();
  await expect(page.locator('.pane-list')).toBeVisible();
});
