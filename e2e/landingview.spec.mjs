import { test, expect, boot, signIn, openFeed } from './fixtures.mjs';

/**
 * The landing view setting (Settings → Reading → "Where you land").
 *
 * A30 already covers "resume wherever I left off" end to end (routing.spec.mjs).
 * What's new here is a reader OVERRIDING that default with a fixed place — and
 * the thing only a browser can prove is the round trip: chosen in the select,
 * persisted server-side, and honoured on the NEXT bare-address boot, the same
 * one a bookmark or a home-screen icon produces.
 */

function landingSelect(page) {
  return page.locator('select[aria-label="Landing view"]');
}

async function openReadingSettings(page) {
  await page.keyboard.press(',');
  await expect(page.locator('.set-tabs')).toBeVisible();
  await expect(landingSelect(page)).toBeVisible();
}

test.describe('landing view', () => {
  test('defaults to resuming where you left off', async ({ page }) => {
    await boot(page);
    await openReadingSettings(page);
    await expect(landingSelect(page)).toHaveValue('resume');
  });

  test('choosing My Feed changes where a fresh boot lands', async ({ page }) => {
    await boot(page);
    await openReadingSettings(page);
    await landingSelect(page).selectOption('myfeed');

    // The save is fire-and-forget (savePrefs, reader.go) — there is no
    // pending indicator to poll, unlike a tag add/remove. A short, honest
    // wait for the write to reach the server is the least bad option here;
    // see uncategorised.spec.mjs's own note on the same trade.
    await page.waitForTimeout(500);
    await page.keyboard.press('Escape');

    // page.goto('/'), not reload(): Escape closes the settings dialog
    // visually but leaves the address on /settings/reading, and reloading
    // THAT is an addressed boot (the settings screen), not the bare address
    // this setting is about — see routing.spec.mjs's own bare-address tests
    // for the same distinction.
    await page.goto('/');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    await expect(page.locator('.pane-list')).toContainText('My Feed', { timeout: 30_000 });

    // And the picker itself reports the choice back, round-tripped through
    // the server rather than merely held in a local draft.
    await openReadingSettings(page);
    await expect(landingSelect(page)).toHaveValue('myfeed');
  });

  test('choosing a specific feed lands there on a bare address, not on the article last read', async ({ page }) => {
    await boot(page);

    // Leave a trail on a DIFFERENT feed, so landing on Alpha Journal is
    // provably the setting's doing and not an accident of A30's resume.
    await openFeed(page, /Beta Notes/);
    await page.locator('.item-row').first().click();
    await expect(page.locator('.article[data-current="true"] h1')).toBeVisible();

    await openReadingSettings(page);
    await landingSelect(page).selectOption({ label: 'Alpha Journal' });
    await page.waitForTimeout(500);
    await page.keyboard.press('Escape');

    await page.goto('/');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });

    await expect(page.locator('.pane-list')).toContainText('Alpha Journal', { timeout: 30_000 });
    // Opens fresh on the feed's own list, not mid-article — see
    // effectiveResumeItem's doc comment on why a fixed landing view clears
    // the resumed article rather than carrying it along.
    const u = new URL(page.url());
    expect(u.pathname).toMatch(/^\/feed\/[^/]+$/);
  });

  test('switching back to "Resume where I left off" clears the override', async ({ page }) => {
    await boot(page);
    await openReadingSettings(page);
    await landingSelect(page).selectOption('unread');
    await expect(landingSelect(page)).toHaveValue('unread');

    await landingSelect(page).selectOption('resume');
    await page.waitForTimeout(500);
    await page.keyboard.press('Escape');

    // page.goto('/'), not reload() — see the same note in the My Feed test
    // above: reload() while the address is still /settings/reading is an
    // addressed boot, not the bare address this test needs.
    await page.goto('/');
    await signIn(page);
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });

    // Back to A30's ordinary behaviour: a bare address resumes the place the
    // reader was actually in (All, from boot()) rather than the unread
    // stream that was tried and undone.
    await expect(page.locator('.pane-list')).toContainText('All articles');

    await openReadingSettings(page);
    await expect(landingSelect(page)).toHaveValue('resume');
  });
});
