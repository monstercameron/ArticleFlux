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
async function expectNoHorizontalOverflow(page) {
  const overflow = await page.evaluate(() => {
    const d = document.documentElement;
    return {
      scrollW: d.scrollWidth,
      clientW: d.clientWidth,
      offenders: [...document.querySelectorAll('*')]
        .filter((el) => el.getBoundingClientRect().right > d.clientWidth + 1)
        .slice(0, 5)
        .map((el) => el.className || el.tagName),
    };
  });
  expect(overflow.offenders, `elements overflowing the viewport: ${overflow.offenders}`)
    .toEqual([]);
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
    await expect(page.locator('.article h1')).toBeVisible();
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

test('reduced motion is respected', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await boot(page);
  const animated = await page.evaluate(() =>
    [...document.querySelectorAll('*')].filter((el) => {
      const s = getComputedStyle(el);
      return s.animationName !== 'none' && s.animationDuration !== '0s';
    }).length);
  expect(animated).toBe(0);
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
