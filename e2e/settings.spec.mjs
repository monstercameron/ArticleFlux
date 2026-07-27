import { test, expect, boot } from './fixtures.mjs';

/**
 * The nine settings tabs, each asserted to render ITS OWN content.
 *
 * client/view/settings.go keys `.set-panel` on the active tab specifically so
 * switching tabs replaces the element rather than patching it — which is also
 * what lets the panel's entrance animation run again. A Go `switch` with nine
 * cases can still have two of them return the same thing by accident (a
 * copy-paste of the wrong case, an early return that never reaches its branch),
 * and nothing renders that mistake more legibly than nine really being nine on
 * screen.
 *
 * Content is compared rather than asserted against fixed copy: hardcoding nine
 * strings here would make every wording pass in Go's own tests fail here too,
 * for a reason that has nothing to do with what this file is supposed to catch
 * (nine DISTINCT panels), which is the "transcription instead of reasoning"
 * trap CLAUDE.md warns about, applied to nine strings instead of one.
 */

const TABS = ['reading', 'appearance', 'listening', 'smart', 'feeds', 'account', 'server', 'activity', 'speed'];

test.describe('settings', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('all nine tabs render, and each shows content none of the others do', async ({ page }) => {
    await boot(page);
    await page.keyboard.press(',');
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
    await page.keyboard.press(',');
    await page.locator('[data-action="settings-tab"][data-value="feeds"]').click();

    const current = page.locator('[data-action="settings-tab"][aria-current="true"]');
    await expect(current).toHaveCount(1);
    await expect(current).toHaveAttribute('data-value', 'feeds');
  });
});
