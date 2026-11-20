import { test, expect, boot, openSettings } from './fixtures.mjs';

/**
 * Settings → Listening is honest about a browser that cannot speak.
 *
 * The row used to say "always available" whatever the browser had. A synthesiser
 * with no voice accepts every utterance and says nothing, so on one the reader
 * pressed Listen and heard silence from a page that had promised otherwise. The
 * voice list loads late in some browsers, so the page asks again for a few seconds
 * before it says none — which is why these wait.
 */
async function openListening(page) {
  await boot(page);
  await openSettings(page);
  await page.locator('[data-action="settings-tab"][data-value="listening"]').click();
}

test.describe('browser voice', () => {
  test('a browser with a voice is told it is available', async ({ page }) => {
    // Headless Chromium on a developer machine has voices; CI's may not. Give the
    // list one, the way a browser that has installed one would.
    await page.addInitScript(() => {
      const real = speechSynthesis.getVoices.bind(speechSynthesis);
      speechSynthesis.getVoices = () => (real().length ? real() : [{ name: 'Test', lang: 'en' }]);
    });
    await openListening(page);
    await page.waitForTimeout(4500);
    await expect(page.locator('.voice-status')).toHaveCount(0);
    await expect(page.getByText('always available')).toBeVisible();
  });

  test('a browser with a synthesiser and no voice says Read aloud will be silent', async ({ page }) => {
    await page.addInitScript(() => {
      speechSynthesis.getVoices = () => [];
    });
    await openListening(page);
    await expect(page.locator('.voice-status')).toContainText('no voice installed', { timeout: 15_000 });
    await expect(page.locator('.voice-status-note')).toContainText('silent');
    await expect(page.getByText('always available')).toHaveCount(0);
  });
});
