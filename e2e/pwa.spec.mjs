import { test, expect, boot, signIn } from './fixtures.mjs';

/**
 * Installability, and the three things it is made of (§20.24).
 *
 * `internal/appicon`'s test already checks that the four files AGREE with each
 * other — the manifest names the icons that are rendered, the shell links the
 * manifest, the worker precaches it. What it cannot check is whether a browser
 * accepts any of it: it reads the files off disk, and every failure mode here is
 * about what happens when a real engine fetches them over a real server.
 *
 * The three that only a browser can answer:
 *
 *   - **Every URL in the manifest resolves.** Relative paths that are correct at
 *     the repository root and wrong when served — or a Content-Type the browser
 *     refuses — produce an install prompt that simply does not appear, with
 *     nothing in the page's own console to say why.
 *   - **The Service Worker installs and its cache is warm.** An installed app
 *     whose shell is not cached is one that is blank the first time it opens
 *     without a network, which is the single thing installing it was for.
 *   - **A launch parameter does something.** A shortcut the client ignores opens
 *     the app, changes nothing, and reads as broken rather than unimplemented.
 */

/** The manifest, parsed, from the running server. */
async function manifest(page) {
  const href = await page.locator('link[rel=manifest]').getAttribute('href');
  expect(href, 'the shell does not link a manifest').toBeTruthy();
  const res = await page.request.get(new URL(href, page.url()).toString());
  expect(res.status(), 'the manifest did not resolve').toBe(200);
  return { res, json: JSON.parse(await res.text()) };
}

test.describe('pwa', () => {
  test('the manifest is served as one, and every URL in it resolves', async ({ page }) => {
    await boot(page);
    const { res, json } = await manifest(page);

    // application/manifest+json. Browsers are lenient about this and audits are
    // not, which makes it exactly the kind of defect nobody notices until
    // somebody runs Lighthouse and is told the app is not installable.
    expect(res.headers()['content-type']).toContain('application/manifest+json');

    // The fields a browser gates the install prompt on.
    expect(json.name).toBeTruthy();
    expect(json.short_name).toBeTruthy();
    expect(['standalone', 'fullscreen', 'minimal-ui']).toContain(json.display);
    expect(json.start_url).toBeTruthy();

    // Every icon, fetched from where the browser would fetch it: resolved
    // against the MANIFEST's own address, which is what makes the relative paths
    // work identically at the root and under /<repo>/.
    const base = new URL('manifest.webmanifest', page.url()).toString();
    for (const icon of json.icons) {
      const url = new URL(icon.src, base).toString();
      const got = await page.request.get(url);
      expect(got.status(), `${icon.src} did not resolve`).toBe(200);
      expect(got.headers()['content-type'], `${icon.src} is not a PNG`).toContain('image/png');
      expect((await got.body()).length, `${icon.src} is empty`).toBeGreaterThan(200);
    }

    // Sizes are a claim about the bytes, and a wrong one is how an icon ends up
    // upscaled and soft in a launcher. Decoded in the page rather than trusted.
    for (const icon of json.icons) {
      const [w, h] = icon.sizes.split('x').map(Number);
      const got = await page.evaluate(
        (src) =>
          new Promise((resolve) => {
            const img = new Image();
            img.onload = () => resolve([img.naturalWidth, img.naturalHeight]);
            img.onerror = () => resolve([0, 0]);
            img.src = src;
          }),
        new URL(icon.src, base).toString(),
      );
      expect(got, `${icon.src} claims ${icon.sizes}`).toEqual([w, h]);
    }

    // iOS reads none of the above, so its icon is a <link> and nothing else
    // supplies it. A missing one is not a broken image — it is a screenshot of
    // the page, scaled down, as the home-screen icon.
    const apple = await page.locator('link[rel="apple-touch-icon"]').getAttribute('href');
    expect(apple).toBeTruthy();
    const appleRes = await page.request.get(new URL(apple, page.url()).toString());
    expect(appleRes.status(), 'the iOS icon did not resolve').toBe(200);
  });

  /**
   * The window chrome follows the theme, which is the one part of a standalone
   * window a stylesheet cannot reach.
   *
   * Every other token is a CSS custom property and switching themes is a paint
   * (§20.16). `theme-color` is a meta element, so without applyAppearance
   * rewriting it an installed Daylight reader keeps a plum title bar around a
   * page of paper for the whole session.
   */
  test('theme-color follows the theme', async ({ page }) => {
    await boot(page);
    const meta = page.locator('meta[name="theme-color"]');

    await page.locator('[data-action="open-settings"]').first().click();
    await page.locator(`[data-action='settings-tab'][data-value='appearance']`).click();

    await page.locator(`.thm-card[data-value='daylight']`).click();
    await expect(meta).toHaveAttribute('content', '#F7F2E9');

    await page.locator(`.thm-card[data-value='fanciful']`).click();
    await expect(meta).toHaveAttribute('content', '#221A2E');
  });

  /**
   * The worker installs, and its cache holds the install.
   *
   * `?sw=1` is required and is not a workaround: the shell EVICTS the worker on
   * a loopback origin, because a development box that caches the wasm module
   * cache-first serves a stale build forever (see web/index.html). The escape
   * hatch exists so that the one machine where somebody would test installing is
   * not the one machine where it cannot happen.
   */
  test('the worker installs and precaches the manifest and its icons', async ({ page }) => {
    await page.goto('/?sw=1');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    // Registration happens AFTER the app is running, deliberately — a cache in
    // front of the boot fetch could stop the app starting at all — so this waits
    // rather than asserting immediately.
    const active = await page.waitForFunction(
      async () => {
        const reg = await navigator.serviceWorker.getRegistration();
        return !!(reg && reg.active);
      },
      null,
      { timeout: 30_000 },
    ).then(() => true).catch(() => false);
    expect(active, 'no Service Worker became active').toBe(true);

    const cached = await page.waitForFunction(
      async () => {
        const keys = await caches.keys();
        const shell = keys.find((k) => k.startsWith('articleflux-shell-'));
        if (!shell) return false;
        const cache = await caches.open(shell);
        const want = ['./manifest.webmanifest', './icons/icon-192.png', './icons/icon-512.png'];
        for (const url of want) {
          if (!(await cache.match(url))) return false;
        }
        return true;
      },
      null,
      { timeout: 30_000 },
    ).then(() => true).catch(() => false);
    expect(cached, 'the shell cache does not hold the manifest and its icons').toBe(true);

    // And back off, so the rest of the suite runs against the same
    // no-worker development box every other test assumes.
    await page.goto('/?sw=0');
    await page.waitForFunction(
      async () => (await navigator.serviceWorker.getRegistrations()).length === 0,
      null,
      { timeout: 30_000 },
    ).catch(() => {});
  });

  /**
   * A shortcut opens what it says, and outranks the saved view.
   *
   * The saved view is A30's resume — where this reader was yesterday. A shortcut
   * is a request made half a second ago, so it wins; and the parameter is
   * consumed, because left in the bar a reload would re-apply an instruction the
   * reader has already had.
   */
  test('a manifest shortcut opens its stream and is consumed', async ({ page }) => {
    // Somewhere else first, so "it opened Read later" cannot be the resume
    // happening to agree with the shortcut.
    await boot(page);
    await page.locator('[data-action="open-settings"]').first().click();

    await page.goto('/?view=later');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    await expect(page.locator('.list-title')).toHaveText('Read later');
    // Consumed: the query is gone from the address, so a reload is an ordinary
    // boot rather than the same instruction again.
    await expect.poll(() => new URL(page.url()).search).toBe('');
  });

  /**
   * A share opens the add-feed dialog with the address in it.
   *
   * This is the whole reason a feed reader is worth installing on a phone:
   * sharing a page to it means "subscribe to this", and the manifest's
   * share_target is the only way an installed app can receive that.
   */
  test('a shared address arrives in the add-feed dialog', async ({ page }) => {
    const shared = 'https://example.com/blog';
    await page.goto(`/?url=${encodeURIComponent(shared)}&title=Example+Blog`);
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    const dialog = page.getByRole('dialog', { name: 'Add a feed' });
    await expect(dialog).toBeVisible({ timeout: 30_000 });
    await expect(page.locator('[data-role="add-feed"]')).toHaveValue(shared);
    await expect.poll(() => new URL(page.url()).search).toBe('');
  });

  /**
   * Android's share sheet mostly does not use the `url` field.
   *
   * A browser sharing from its address bar sends `url`; almost everything else —
   * reader apps, social clients, the "share article" button in a news app —
   * sends `Some Headline https://example.com/x` as `text`. A share target that
   * only reads `url` therefore works when you test it and does nothing in the
   * case that actually happens.
   */
  test('a share that puts the address in text still works', async ({ page }) => {
    await page.goto('/?text=' + encodeURIComponent('Look at this https://example.com/post.'));
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    await expect(page.getByRole('dialog', { name: 'Add a feed' })).toBeVisible({ timeout: 30_000 });
    // The trailing full stop is sentence punctuation, not part of the address.
    await expect(page.locator('[data-role="add-feed"]')).toHaveValue('https://example.com/post');
  });
});
