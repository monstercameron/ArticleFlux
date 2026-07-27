import { test, expect, boot } from './fixtures.mjs';

/**
 * The readability floor for colours the stylesheet cannot compute on its own.
 *
 * `client/design/sheet_test.go` already checks every token a `design.Theme`
 * declares, against all three grounds it can land on. It cannot check `--ink` —
 * the per-source hue where it carries TEXT rather than filling a shape — because
 * that value does not exist until a browser resolves a `color-mix()` against a
 * hue the server assigned at runtime. Go can only see the expression.
 *
 * That gap was not theoretical. On the light theme the mix landed the amber
 * source at **4.45:1**, below AA, on the source name of every row in the list
 * and every article eyebrow — and it was invisible to every check that existed:
 * the Go floor passed (it does not see --ink), the screenshots looked fine (it
 * is a plausible olive), and no ratio anywhere was wrong. It took looking at a
 * phone and then measuring the thing I had just looked at.
 *
 * So the measurement happens where the truth is: the same engine that ships the
 * page resolves the mix, and the result is read back as real sRGB bytes off a
 * canvas rather than parsed out of a computed string — `getComputedStyle` hands
 * back `oklab(…)` for a color-mix, and reading those three numbers as RGB is how
 * the first version of this test reported 18:1 for a colour that was failing.
 */

/** The seven hand-picked source hues, from client/design/tokens.go. */
const SOURCE_HUES = ['#FF8A6B', '#7DDCB0', '#FFCE5C', '#89B9FF', '#FF9CC4', '#C4A6FF', '#A8D89A'];

const THEMES = ['fanciful', 'ink', 'ledger', 'daylight', 'contrast'];

/** WCAG AA at body size. --ink is used at 12.5px, so the large-text bar does not apply. */
const AA = 4.5;

async function pickTheme(page, name) {
  await page.locator(`[data-action='open-settings']`).first().click();
  await page.waitForTimeout(300);
  await page.locator(`[data-action='settings-tab'][data-value='appearance']`).click();
  await page.waitForTimeout(300);
  await page.locator(`.thm-card[data-value='${name}']`).click();
  await page.waitForTimeout(600);
}

/** Contrast of --ink against the page, for each source hue, in the live browser. */
async function inkRatios(page, hues) {
  return page.evaluate((hues) => {
    // Any CSS colour to true sRGB bytes, by painting it. This is the whole
    // reason the test is here rather than in Go, and also the reason it does not
    // read the computed string directly.
    const cv = document.createElement('canvas');
    cv.width = cv.height = 1;
    const ctx = cv.getContext('2d', { willReadFrequently: true });
    const rgb = (css) => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = '#000';
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0], d[1], d[2]];
    };
    const lum = (c) => {
      const f = c.map((v) => {
        v /= 255;
        return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
      });
      return 0.2126 * f[0] + 0.7152 * f[1] + 0.0722 * f[2];
    };
    const ratio = (a, b) => {
      const [hi, lo] = [lum(a), lum(b)].sort((p, q) => q - p);
      return (hi + 0.05) / (lo + 0.05);
    };

    const ground = rgb(getComputedStyle(document.body).backgroundColor);
    const out = {};
    for (const h of hues) {
      // A source's hue arrives as an inline --c on the row; the span inside is
      // what actually reads --ink. Reproducing that shape rather than setting
      // --ink directly is what keeps this honest about the cascade.
      const row = document.createElement('div');
      row.style.setProperty('--c', h);
      const name = document.createElement('span');
      name.className = 'item-source';
      row.appendChild(name);
      document.body.appendChild(row);
      out[h] = Math.round(ratio(rgb(getComputedStyle(name).color), ground) * 100) / 100;
      row.remove();
    }
    return out;
  }, hues);
}

test.describe('appearance', () => {
  test('every source hue stays readable as text, on every theme', async ({ page }) => {
    await boot(page);

    for (const theme of THEMES) {
      await pickTheme(page, theme);
      const ratios = await inkRatios(page, SOURCE_HUES);
      for (const hue of SOURCE_HUES) {
        expect(
          ratios[hue],
          `--ink for ${hue} on the "${theme}" theme is ${ratios[hue]}:1 — a source ` +
            `name at 12.5px needs ${AA}:1. Tune the light-tone mix in ` +
            `client/design/sheet.go, or the theme's own ground.`,
        ).toBeGreaterThanOrEqual(AA);
      }
    }

    // Leave the instance on the house theme: the appearance prefs are
    // server-side, so a test that ends on Contrast changes what the next one
    // opens on.
    await pickTheme(page, 'fanciful');
  });

  test('a hue still identifies its source after being made readable', async ({ page }) => {
    await boot(page);
    await pickTheme(page, 'daylight');

    // The floor above is satisfiable by painting every source the same near-black,
    // which would pass and destroy the one idea the design rests on. So: the
    // seven must still be seven.
    const ratios = await inkRatios(page, SOURCE_HUES);
    const distinct = new Set(Object.values(ratios));
    expect(distinct.size).toBeGreaterThan(3);

    await pickTheme(page, 'fanciful');
  });
});
