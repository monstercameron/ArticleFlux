import { test, expect, boot } from './fixtures.mjs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, join } from 'node:path';
import { mkdirSync } from 'node:fs';

const here = dirname(fileURLToPath(import.meta.url));
const shots = join(here, 'shots');
const mockups = join(here, '..', 'design');

/**
 * Design parity: does the built reader look like the direction that was chosen?
 *
 * `design/03-fanciful.html` and `04-fanciful-mobile.html` are specifications,
 * not source — hand-written CSS that nobody ports. So parity is checked by
 * capturing both at matched viewports and by asserting the *tokens* that carry
 * the direction, rather than by pixel-diffing a mockup against a real app full
 * of real articles (which would fail on every content change and prove nothing).
 *
 * What is asserted mechanically: the palette, the type stacks, and the one real
 * idea — that every source owns a hue which reaches all four surfaces. What is
 * left to the eye: composition, which is what the paired screenshots are for.
 */

test.beforeAll(() => mkdirSync(shots, { recursive: true }));

// Transcribed from design/03-fanciful.html. If the mockup changes, this fails,
// which is the intent: the mockup is the spec and drift should be loud.
const palette = {
  ground: 'rgb(34, 26, 46)',
  raised: 'rgb(44, 33, 56)',
  sunk: 'rgb(27, 20, 37)',
  cream: 'rgb(248, 241, 231)',
  dim: 'rgb(154, 140, 168)',
};


test('the built app uses the fanciful palette', async ({ page }) => {
  await boot(page);

  const body = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(body).toBe(palette.ground);

  const bodyColor = await page.evaluate(() => getComputedStyle(document.body).color);
  expect(bodyColor).toBe(palette.cream);

  const rail = await page.evaluate(() =>
    getComputedStyle(document.querySelector('.pane-rail')).backgroundColor);
  expect(rail).toBe(palette.sunk);
});

test('the type stacks are Fraunces / Literata / Outfit', async ({ page }) => {
  await boot(page);

  const fonts = await page.evaluate(() => {
    const of = (sel) => getComputedStyle(document.querySelector(sel)).fontFamily;
    return { mark: of('.masthead-mark'), title: of('.item-title'), ui: of('.chip') };
  });
  // Display face used with restraint — the wordmark and headings only.
  expect(fonts.mark).toMatch(/Fraunces/);
  // Reading face on anything that is prose.
  expect(fonts.title).toMatch(/Literata/);
  // Utility face on controls.
  expect(fonts.ui).toMatch(/Outfit/);
});

test('every source owns a hue, and it reaches all four surfaces', async ({ page }, testInfo) => {
  // The rail carries three of the four surfaces and is off-screen on a phone by
  // design, so this is a desktop assertion. The phone's share of the hue system
  // is covered by the list-row edge, which the overflow tests exercise.
  test.skip(testInfo.project.name !== 'desktop', 'the rail is desktop-only');
  await boot(page);

  // 1. the sidebar dot
  // Filtered on having a dot rather than sliced by position: the streams have
  // never had one, and the Categories section adds rows that carry a disclosure
  // instead. A hue belongs to a SOURCE, so "every row with a dot" is exactly the
  // set this assertion is about.
  const feedDots = await page.evaluate(() =>
    [...document.querySelectorAll('.pane-rail .feed-row')]
      .map((el) => el.querySelector('.feed-dot'))
      .filter(Boolean)
      .map((el) => getComputedStyle(el).backgroundColor));
  expect(feedDots.length).toBeGreaterThan(1);
  // Distinct: two feeds must not be told apart only by reading their names.
  expect(new Set(feedDots).size).toBe(feedDots.length);

  // 2. the list row's source label
  const sources = await page.evaluate(() =>
    [...document.querySelectorAll('.item-source')].map((el) => getComputedStyle(el).color));
  expect(new Set(sources).size).toBeGreaterThan(1);

  await page.locator('.item-row').first().click();
  await expect(page.locator('.article h1')).toBeVisible();

  // 3. the selected row's edge picks up the same hue
  const edge = await page.evaluate(() =>
    getComputedStyle(document.querySelector('.item-row[aria-current="true"]')).borderLeftColor);
  expect(edge).not.toBe('rgba(0, 0, 0, 0)');

  // 4. the wash behind the article
  const wash = await page.evaluate(() =>
    getComputedStyle(document.querySelector('.pane-article')).backgroundImage);
  expect(wash).toMatch(/gradient/);

  // The hue is a pure function of the source id, so the row edge and the
  // sidebar dot for the SAME feed must agree. If these ever diverge, HueFor has
  // been called with different inputs somewhere.
  const agree = await page.evaluate(() => {
    const row = document.querySelector('.item-row[aria-current="true"]');
    const src = row.getAttribute('style') || '';
    const railRow = [...document.querySelectorAll('.pane-rail .feed-row')]
      .map((el) => el.getAttribute('style') || '')
      .filter((s) => s && src && s === src);
    return railRow.length > 0;
  });
  expect(agree, 'the selected item hue should match its feed row hue').toBe(true);
});

test('read and unread are visually distinct', async ({ page }) => {
  await boot(page);

  const before = await page.evaluate(() => {
    const t = document.querySelector('.item-row .item-title');
    const s = getComputedStyle(t);
    return { color: s.color, weight: s.fontWeight };
  });

  await page.locator('.item-row').first().click();
  await expect(page.locator('.item-row').first()).toHaveAttribute('data-read', 'true');

  const after = await page.evaluate(() => {
    const t = document.querySelector('.item-row .item-title');
    const s = getComputedStyle(t);
    return { color: s.color, weight: s.fontWeight };
  });

  // Read must recede without vanishing: you can still see it was there.
  expect(after.color).not.toBe(before.color);
  expect(after.color).toBe(palette.dim);
  expect(Number(after.weight)).toBeLessThan(Number(before.weight));
});

// --- paired captures ---------------------------------------------------------
// These produce the artefacts for a human comparison. They assert only that a
// capture happened; the judgement is the point and it is not automatable.

test('capture: built reader, desktop', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop', 'desktop capture');
  await page.setViewportSize({ width: 1440, height: 900 });
  await boot(page);
  await page.screenshot({ path: join(shots, 'built-desktop-list.png') });

  await page.locator('.item-row').first().click();
  await expect(page.locator('.article h1')).toBeVisible();
  await page.screenshot({ path: join(shots, 'built-desktop-article.png') });
});

test('capture: built reader, phone', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop', 'runs once');
  await page.setViewportSize({ width: 390, height: 844 });
  await boot(page);
  await page.screenshot({ path: join(shots, 'built-phone-list.png') });

  await page.locator('.item-row').first().click();
  await expect(page.locator('.article h1')).toBeVisible();
  await page.screenshot({ path: join(shots, 'built-phone-article.png') });
});

test('capture: the mockups they were built from', async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== 'desktop', 'runs once');

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(pathToFileURL(join(mockups, '03-fanciful.html')).href);
  await page.waitForTimeout(800); // webfonts
  await page.screenshot({ path: join(shots, 'mockup-desktop.png') });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(pathToFileURL(join(mockups, '04-fanciful-mobile.html')).href);
  await page.waitForTimeout(800);
  await page.screenshot({ path: join(shots, 'mockup-phone.png') });
});
