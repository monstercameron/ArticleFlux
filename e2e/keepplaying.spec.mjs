import { test, expect, boot } from './fixtures.mjs';

/**
 * Keep playing with the browser's own voice, against a synthesiser that is a stub.
 *
 * CI's Chromium has no voices, and a real engine is slow and unrepeatable, so the
 * page is given a synthesiser that "speaks" each utterance in a few milliseconds
 * and fires its `end` — which is enough to drive the app's own state machine
 * deterministically and is all these tests are about. What it records is what the
 * engine would have been asked to say, and at what rate.
 *
 * The failures this pins were all intermittent on a real voice: the list
 * "played" in a third of a second with nothing spoken, the rail's unread counts
 * collapsing to a third of the truth, and the reading speed never reaching the
 * voice.
 */
async function stubSynth(page, ms = 40) {
  await page.addInitScript((ms) => {
    const spoke = (window.__spoke = []);
    let queue = [];
    let busy = false;
    let paused = false;
    let epoch = 0;
    const pump = () => {
      if (busy || paused) return;
      const u = queue.shift();
      if (!u) return;
      busy = true;
      spoke.push({ text: u.text, rate: u.rate });
      const mine = epoch;
      setTimeout(() => {
        if (mine !== epoch) return;
        busy = false;
        if (u.onend) u.onend({});
        pump();
      }, ms);
    };
    const fake = {
      speaking: false, pending: false, paused: false,
      getVoices: () => [{ name: 'Test', lang: 'en', localService: true }],
      speak(u) { queue.push(u); pump(); },
      cancel() { queue = []; busy = false; epoch++; },
      pause() { paused = true; },
      resume() { paused = false; pump(); },
      addEventListener() {}, removeEventListener() {},
    };
    Object.defineProperty(window, 'speechSynthesis', { value: fake, configurable: true });
  }, ms);
}

/** How many rows the list shows as unread, and what the rail adds up to. */
async function counts(page) {
  return page.evaluate(() => {
    const unreadRows = document.querySelectorAll('.item-row[data-read="false"]').length;
    let rail = 0;
    // textContent, not innerText: on a phone the sidebar is a pane that is not
    // showing, and innerText of something that is not rendered is the empty
    // string — the count read 0 against four unread rows, on every phone run.
    for (const f of document.querySelectorAll('.feed-row')) {
      if (!/Alpha Journal|Beta Notes/.test(f.textContent)) continue;
      const m = f.textContent.match(/\d+\s*$/);
      if (m) rail += +m[0];
    }
    return { unreadRows, rail };
  });
}

async function startKeepPlaying(page) {
  await boot(page);
  const row = page.locator('.item-row').first();
  await row.click();
  const id = await row.getAttribute('data-item-id');
  const bar = page.locator(`.listen-bar[data-listen-for="${id}"]`);
  await expect(bar).toBeVisible();
  await bar.locator('[data-action="toggle-autoplay"]').click();
  await expect(bar.locator('[data-action="toggle-autoplay"]')).toHaveAttribute('aria-pressed', 'true');
  await bar.locator('[data-action="listen"]').click();
}

test.describe('keep playing, browser voice', () => {
  // The suite runs on loaded machines; the work is a few seconds, the waiting is not.
  test.describe.configure({ timeout: 240_000 });

  test('every article is spoken, headline first, in order, and the list ends with a notice', async ({ page }) => {
    await stubSynth(page);
    await boot(page);
    // However many the shared account holds by the time this runs: other specs
    // add feeds and articles to it, and "five" was true only when this spec ran
    // alone. What is asserted is every one of them, in order.
    const titles = await page.locator('.item-title').allInnerTexts();
    expect(titles.length).toBeGreaterThan(1);
    await startKeepPlaying(page);

    await expect(page.getByText("That's the end of the list")).toBeVisible({ timeout: 30_000 });

    const spoke = await page.evaluate(() => window.__spoke);
    // The headline is its own utterance, spoken before the body, with the source.
    const leads = spoke.filter((s) => / From /.test(s.text)).map((s) => s.text);
    expect(leads).toHaveLength(titles.length);
    leads.forEach((l, i) => expect(l.startsWith(titles[i].replace(/\.$/, ''))).toBe(true));
    // And each is followed by something that is not another headline.
    for (let i = 0; i < spoke.length; i++) {
      if (/ From /.test(spoke[i].text) && i + 1 < spoke.length) {
        expect(/ From /.test(spoke[i + 1].text)).toBe(false);
      }
    }
    // Everything played was marked read, and it survives a reload.
    await expect(page.locator('.item-row[data-read="false"]')).toHaveCount(0);
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row[data-read="false"]')).toHaveCount(0);
    expect((await counts(page)).rail).toBe(0);
  });

  test('the rail agrees with the list while it plays', async ({ page }) => {
    await stubSynth(page);
    await startKeepPlaying(page);
    // Sampled all the way through: the count used to fall three at a time, so it
    // only ever disagreed with the rows in the middle of a run.
    for (let i = 0; i < 40; i++) {
      const c = await counts(page);
      expect(c.rail, `rail ${c.rail} vs ${c.unreadRows} unread rows`).toBe(c.unreadRows);
      if (await page.getByText("That's the end of the list").isVisible()) break;
      await page.waitForTimeout(150);
    }
    await expect(page.getByText("That's the end of the list")).toBeVisible({ timeout: 30_000 });
  });

  test('the voice is given the reading speed, not 1', async ({ page }) => {
    await stubSynth(page);
    await boot(page);
    await chooseSpeed(page, '1.5');
    try {
      await page.goto('/');
      await startKeepPlaying(page);
      await expect(page.getByText("That's the end of the list")).toBeVisible({ timeout: 30_000 });
      const rates = await page.evaluate(() => [...new Set(window.__spoke.map((s) => Math.round(s.rate * 100) / 100))]);
      expect(rates).toEqual([1.5]);
    } finally {
      // The suite shares one account: leave the speed where it was found.
      await chooseSpeed(page, '1.1');
    }
  });

  test('Stop ends the run and nothing more is marked read', async ({ page }) => {
    // Slow enough that the run is still going when Stop is pressed.
    await stubSynth(page, 700);
    await startKeepPlaying(page);
    await expect(async () => {
      expect((await page.evaluate(() => window.__spoke.length))).toBeGreaterThan(2);
    }).toPass({ timeout: 20_000 });
    await page.locator('[data-action="listen-stop"]:visible').first().click();
    const readAtStop = await page.locator('.item-row[data-read="true"]').count();
    const spokeAtStop = await page.evaluate(() => window.__spoke.length);
    await page.waitForTimeout(2000);
    expect(await page.evaluate(() => window.__spoke.length)).toBe(spokeAtStop);
    expect(await page.locator('.item-row[data-read="true"]').count()).toBe(readAtStop);
    await expect(page.locator('[data-action="listen-stop"]:visible')).toHaveCount(0);
  });
});

/** chooseSpeed picks a Reading speed chip (`1.5`) in Settings → Listening. */
async function chooseSpeed(page, n) {
  await page.goto('/settings/listening');
  // The default's chip carries a note after the number ("1.1× (default)").
  const re = new RegExp('^' + n.replace('.', String.raw`\.`) + '×');
  const chip = page.getByText(re).first();
  await expect(chip).toBeVisible({ timeout: 30_000 });
  // Retried: the first click after a direct load of a settings address has been
  // dropped before, and what matters here is the speed, not the click.
  await expect(async () => {
    await chip.click();
    await expect(page.locator('[aria-pressed="true"]').filter({ hasText: re })).toHaveCount(1, { timeout: 1000 });
  }).toPass({ timeout: 15_000 });
}
