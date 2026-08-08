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

/**
 * pickTheme opens Appearance and presses a theme card.
 *
 * # Why it does not just click the settings button
 *
 * On a phone the layout shows one pane at a time, so the gear is in the DOM and
 * permanently not visible — `locator.click()` sat there for the full two-minute
 * timeout reporting "206 × waiting for element to be visible, enabled and
 * stable", which reads as a hung app and is a button on a pane that is not on
 * screen. Every theme test in this file was red on the mobile project for that
 * reason, and had been.
 *
 * `,` is the keyboard route to the same surface (client/view/reader_keyboard.go),
 * and it does not care which pane is showing. Deliberately NOT a `page.goto` to
 * `/settings/appearance`: that is a full reload, and the test below this one is
 * specifically about a theme change that happens WITHOUT one — navigating there
 * would make it assert nothing and pass on any build.
 */
async function pickTheme(page, name) {
  const tab = page.locator(`[data-action='settings-tab'][data-value='appearance']`);
  if (!(await tab.isVisible().catch(() => false))) {
    const gear = page.locator(`[data-action='open-settings']`).first();
    if (await gear.isVisible().catch(() => false)) {
      await gear.click();
    } else {
      await page.evaluate(() => document.activeElement?.blur());
      await page.keyboard.press(',');
    }
    await expect(tab).toBeVisible({ timeout: 30_000 });
  }
  await tab.click();
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

  /**
   * The Go floor and the browser have to agree about what `--ink` IS.
   *
   * `client/design/oklab.go` reproduces `color-mix(in oklab, var(--c), var(--cream)
   * 62%)` in Go, so that `design.Sanitize` can refuse a GENERATED light theme whose
   * source names would be illegible — a check nothing could make while the five
   * hand-written palettes were the only palettes, and one that matters the moment a
   * model is choosing `--cream` and the ground.
   *
   * A reimplementation of a browser operation is worth exactly as much as the
   * evidence that it matches. So: four (ground, cream) pairs, the amber source's
   * `--ink` as Go computes it, and the same value read back out of the shipping
   * engine as real sRGB bytes. Amber is the worst case on every one of them, which
   * is why it is the one pinned.
   *
   * If this fails, Go is measuring a colour the browser does not paint, and every
   * ratio the readability floor reports for a light theme is wrong.
   */
  const INK_CASES = [
    { bg: '#F7F2E9', cream: '#241C30', ink: '#6E5B49', ratio: 5.79 }, // Daylight itself
    { bg: '#EDE6D8', cream: '#3A2F1E', ink: '#7F6736', ratio: 4.34 }, // a plausible "old paper"
    { bg: '#FFFFFF', cream: '#000000', ink: '#423412', ratio: 12.13 }, // the extreme
    { bg: '#D8D2C4', cream: '#1A1A1A', ink: '#685836', ratio: 4.59 }, // near the floor
  ];
  const AMBER = '#FFCE5C';

  test('the Go ink mix is the mix the browser paints', async ({ page }) => {
    await boot(page);

    const got = await page.evaluate(
      ({ cases, hue }) => {
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

        // The tokens are set exactly where applyAppearance sets them — on
        // documentElement — and the tone attribute with them, because the light
        // rule is what selects the mix at all.
        const root = document.documentElement;
        const before = {
          bg: root.style.getPropertyValue('--bg'),
          cream: root.style.getPropertyValue('--cream'),
          tone: root.getAttribute('data-tone'),
        };
        const out = [];
        for (const c of cases) {
          root.style.setProperty('--bg', c.bg);
          root.style.setProperty('--cream', c.cream);
          root.setAttribute('data-tone', 'light');

          // The same shape the list paints: --c on the row, .item-source inside it.
          const row = document.createElement('div');
          row.style.setProperty('--c', hue);
          const name = document.createElement('span');
          name.className = 'item-source';
          row.appendChild(name);
          document.body.appendChild(row);
          const ink = rgb(getComputedStyle(name).color);
          row.remove();
          out.push({ ink, ratio: Math.round(ratio(ink, rgb(c.bg)) * 100) / 100 });
        }
        // Put the reader back where it was, since these prefs are server-side and
        // the next test opens on whatever this one left.
        root.style.setProperty('--bg', before.bg);
        root.style.setProperty('--cream', before.cream);
        if (before.tone) root.setAttribute('data-tone', before.tone);
        return out;
      },
      { cases: INK_CASES, hue: AMBER },
    );

    for (let i = 0; i < INK_CASES.length; i++) {
      const want = INK_CASES[i];
      const wantRGB = [1, 3, 5].map((k) => parseInt(want.ink.slice(k, k + 2), 16));
      for (let ch = 0; ch < 3; ch++) {
        expect(
          Math.abs(got[i].ink[ch] - wantRGB[ch]),
          `--ink for ${AMBER} on (${want.bg}, ${want.cream}): the browser paints ` +
            `rgb(${got[i].ink}) and client/design/oklab.go computes ${want.ink}. ` +
            `MixOklab is reproducing a different operation, so every --ink ratio ` +
            `design.Sanitize reports for a light theme is wrong.`,
        ).toBeLessThanOrEqual(2);
      }
      // And the ratio itself, which is the number the floor actually acts on.
      expect(Math.abs(got[i].ratio - want.ratio)).toBeLessThanOrEqual(0.1);
    }
  });

  /**
   * The page GROUND has to move with the theme, on a load that is not the first.
   *
   * The splash is painted from a palette mirrored into localStorage, because the
   * theme lives on the server and cannot be known before the transport is up —
   * and web/index.html applies it as an inline style on <body>, which is the one
   * declaration `client/view/theme.go` cannot outrank by writing custom
   * properties onto <html>. Written once at boot and never updated, it froze the
   * ground for the whole session: every token moved on a theme change except the
   * paper underneath them, so Contrast's white secondary type landed on
   * Daylight's cream and every rail entry, settings row title and inactive tab
   * name went invisible.
   *
   * The reload is the entire test. A first-ever visit has nothing mirrored yet,
   * writes no inline style, and passes this on any build — which is why the
   * existing specs, all of which set a theme on a freshly opened page, could not
   * see it.
   *
   * Asserted against `--bg` rather than against a pinned hex: the invariant is
   * that the ground and the tokens are one theme, and a test carrying its own
   * copy of five palettes is a test that goes stale the day one is retuned.
   */
  test('the page ground follows the theme on a returning load', async ({ page }) => {
    await boot(page);

    // Mirror a palette, then come back the way a reader does. The shim now has
    // something to read, and from here the inline declaration is in play.
    await pickTheme(page, 'contrast');
    await page.reload();
    await boot(page);

    for (const theme of ['daylight', 'ledger', 'contrast']) {
      await pickTheme(page, theme);
      const got = await page.evaluate(() => {
        const cv = document.createElement('canvas');
        cv.width = cv.height = 1;
        const ctx = cv.getContext('2d', { willReadFrequently: true });
        const rgb = (css) => {
          ctx.clearRect(0, 0, 1, 1);
          ctx.fillStyle = '#000';
          ctx.fillStyle = css;
          ctx.fillRect(0, 0, 1, 1);
          const d = ctx.getImageData(0, 0, 1, 1).data;
          return `${d[0]},${d[1]},${d[2]}`;
        };
        const cs = getComputedStyle(document.body);
        return {
          ground: rgb(cs.backgroundColor),
          token: rgb(getComputedStyle(document.documentElement)
            .getPropertyValue('--bg').trim()),
          noise: cs.backgroundImage,
        };
      });

      expect(
        got.ground,
        `on "${theme}" the tokens say the ground is rgb(${got.token}) and <body> ` +
          `is painted rgb(${got.ground}). The boot shim's inline background on ` +
          `<body> outranks the sheet, so it has to be kept in step — see ` +
          `platform.SetBodyGround.`,
      ).toBe(got.token);

      // The same inline declaration used to be written as the `background`
      // SHORTHAND, which resets background-image at a priority no stylesheet can
      // reach — so the fractal-noise overlay that makes the ground read as a
      // material was silently gone on every load after the first, and nothing
      // measured it.
      expect(
        got.noise,
        `the noise overlay is missing on "${theme}". Something is writing the ` +
          '`background` shorthand where it should write background-color.',
      ).not.toBe('none');
    }

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
