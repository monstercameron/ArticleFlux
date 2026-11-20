import { test, expect, signIn } from './fixtures.mjs';

/**
 * H6 (audit 3): the first mouse click after loading /settings/<tab> by address
 * — or reloading it — was dropped, and a second one worked. Settings tabs are
 * the one place a click is the whole interaction, so a dropped one is a choice
 * that silently did not save.
 */
test.describe('first click on a settings tab opened by address', () => {
  for (const tab of ['listening', 'reading']) {
    test(`/settings/${tab}: one click changes the setting`, async ({ page }) => {
      await page.goto(`/settings/${tab}`);
      await signIn(page);
      await expect(page.locator('.set-panel')).toBeVisible({ timeout: 60_000 });
      await expect(page.locator('.conn').first())
        .toHaveAttribute('data-state', 'live', { timeout: 30_000 });
      // The reader has looked at the page for a moment before pointing.
      await page.waitForTimeout(1500);

      const chip = page.locator(tab === 'listening'
        ? '[data-action="toggle-autoplay"]' : '[data-action="toggle-expand-all"], .set-panel [aria-pressed]').first();
      const before = await chip.getAttribute('aria-pressed');
      await chip.click();
      // The CAUSE, asserted directly: the pane took focus on entry, focus-visible
      // swapped its entrance animation for the focus ring, and leaving it swapped
      // back — replaying pane-in (a 4% jump and a fade) under the press, so the
      // release landed on a neighbour and no click happened. Nothing may be
      // running on the pane once the press has moved focus off it.
      expect(await page.locator('.pane-settings').evaluate(
        (el) => el.getAnimations().filter((a) => a.animationName.startsWith('pane-in') && a.playState === 'running').length),
      'the settings pane replayed its entrance when focus left it').toBe(0);
      // ONE click: no retry. The old behaviour is exactly that it needed two.
      await expect(chip).not.toHaveAttribute('aria-pressed', before ?? '', { timeout: 10_000 });
      // Leave the account as found.
      await chip.click();
      await expect(chip).toHaveAttribute('aria-pressed', before ?? '', { timeout: 10_000 });
    });
  }
});
