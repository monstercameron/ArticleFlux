import { test, expect, boot, openSettings, signIn } from './fixtures.mjs';

/**
 * Every settings tab, each asserted to render ITS OWN content.
 *
 * client/view/settings.go keys `.set-panel` on the active tab specifically so
 * switching tabs replaces the element rather than patching it — which is also
 * what lets the panel's entrance animation run again. A Go `switch` with a case
 * per tab can still have two of them return the same thing by accident (a
 * copy-paste of the wrong case, an early return that never reaches its branch),
 * and nothing renders that mistake more legibly than every tab being on screen.
 *
 * Content is compared rather than asserted against fixed copy: hardcoding the
 * strings here would make every wording pass in Go's own tests fail here too,
 * for a reason that has nothing to do with what this file is supposed to catch
 * (DISTINCT panels), which is the "transcription instead of reasoning" trap
 * CLAUDE.md warns about.
 *
 * The list is the tabs in settingsTabs order, and it is kept in step by hand —
 * which is the point: a tab added in Go and forgotten here fails the count
 * assertion below rather than going untested. It went stale once (this file
 * asserted nine while the surface had grown to twelve), so the count is checked
 * against the DOM rather than against itself.
 */

// Fourteen, in settingsTabs order. It had gone stale a second time — thirteen
// here against fourteen in Go, with Discover the one missing — and the count
// assertion below was failing on that rather than on anything about a panel.
const TABS = [
  'reading', 'appearance', 'myfeed', 'listening', 'podcast',
  'feeds', 'classify', 'discover', 'data',
  'smart', 'account', 'server', 'activity', 'speed',
];

test.describe('settings', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('every tab renders, and each shows content none of the others do', async ({ page }) => {
    await boot(page);
    await openSettings(page);
    await expect(page.locator('.set-tabs')).toBeVisible();

    await expect(page.locator('[data-action="settings-tab"]')).toHaveCount(TABS.length);

    const panels = {};
    for (const id of TABS) {
      await page.locator(`[data-action="settings-tab"][data-value="${id}"]`).click();
      await expect(page.locator(`[data-action="settings-tab"][data-value="${id}"]`))
        .toHaveAttribute('aria-current', 'true');

      // Server/Activity/Speed fetch on first visit (client/view/reader.go's
      // settingsTabTo); the panel is keyed on the tab either way, so waiting
      // for it to be visible is enough to know the swap landed, but the async
      // fetch's content needs a moment longer to paint.
      const panel = page.locator('.set-panel');
      await expect(panel).toBeVisible();
      await page.waitForTimeout(300);
      const text = (await panel.innerText()).trim();
      expect(text.length, `the "${id}" tab rendered no content at all`).toBeGreaterThan(0);
      panels[id] = text;
    }

    // Pairwise distinct. A loop rather than a Set on values(), so a collision
    // names BOTH tabs involved instead of just shrinking the count.
    for (let i = 0; i < TABS.length; i++) {
      for (let j = i + 1; j < TABS.length; j++) {
        expect(panels[TABS[i]],
          `settings tabs "${TABS[i]}" and "${TABS[j]}" rendered identical content`)
          .not.toBe(panels[TABS[j]]);
      }
    }
  });

  test('the active tab is the only one marked aria-current', async ({ page }) => {
    await boot(page);
    await openSettings(page);
    await page.locator('[data-action="settings-tab"][data-value="feeds"]').click();

    const current = page.locator('[data-action="settings-tab"][aria-current="true"]');
    await expect(current).toHaveCount(1);
    await expect(current).toHaveAttribute('data-value', 'feeds');
  });
});

/**
 * A tab that needs a fetch has it however it was reached.
 *
 * The fetches used to hang off the tab CLICK alone, so the address (and with it a
 * reload, and Back) opened these tabs on whatever state the page had when it
 * booted: "checking" for ever on FluxCast's server-key row, a model field reading
 * "Default:" with no model after it, a Server tab that was only its heading. The
 * assertions are the populated state of each, not the absence of an error — an
 * empty panel has none to show.
 */
test.describe('settings tabs reached by address', () => {
  async function arrive(page, tab) {
    await page.goto(`/settings/${tab}`);
    await signIn(page);
    await expect(page.locator('.pane-settings')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.set-panel')).toBeVisible();
  }

  for (const reload of [false, true]) {
    const how = reload ? 'after a reload' : 'on arrival';

    test(`FluxCast resolves its server-key row ${how}`, async ({ page }) => {
      await arrive(page, 'podcast');
      if (reload) { await page.reload(); await expect(page.locator('.set-panel')).toBeVisible({ timeout: 60_000 }); }
      await expect(page.locator('.set-panel')).not.toContainText(/\bchecking\b/, { timeout: 15_000 });
    });

    test(`Smart+ names the default model ${how}`, async ({ page }) => {
      await arrive(page, 'smart');
      if (reload) { await page.reload(); await expect(page.locator('.set-panel')).toBeVisible({ timeout: 60_000 }); }
      await expect(page.locator('.set-panel')).toContainText(/Default: \S+/, { timeout: 15_000 });
    });

    test(`My Feed lists the feeds competing ${how}`, async ({ page }) => {
      await arrive(page, 'myfeed');
      if (reload) { await page.reload(); await expect(page.locator('.set-panel')).toBeVisible({ timeout: 60_000 }); }
      await expect(page.locator('.set-panel')).toContainText('Alpha Journal', { timeout: 15_000 });
    });

    test(`Server shows the build ${how}`, async ({ page }) => {
      await arrive(page, 'server');
      if (reload) { await page.reload(); await expect(page.locator('.set-panel')).toBeVisible({ timeout: 60_000 }); }
      await expect(page.locator('.set-panel')).toContainText(/Commit/i, { timeout: 15_000 });
    });
  }
});
