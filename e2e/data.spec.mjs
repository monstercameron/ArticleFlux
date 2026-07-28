import { test, expect, boot } from './fixtures.mjs';

/**
 * Settings › Data: bringing a subscription list in, and taking one out (F1).
 *
 * This is the one feature whose whole point is a file crossing the boundary
 * between the browser and the server, so it is the one feature no Go test can
 * prove works. `internal/reader`'s tests cover the migration itself — folders
 * become categories, a bad row costs only itself, an export re-imports — and
 * everything they cannot see is here: that a chooser opens at all, that the
 * bytes reach the RPC, that the report lands on screen, that the sidebar catches
 * up, and that the download is a real file with the feeds in it.
 *
 * The feeds imported here are never fetched: import subscribes and leaves the
 * poller to fill in, which is exactly why example.com addresses are safe to use
 * and why the assertions are about the SIDEBAR rather than about articles.
 */

const OPML = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>An export from somewhere else</title></head>
  <body>
    <outline text="Imported">
      <outline type="rss" text="E2E Alpha" xmlUrl="https://example.com/e2e-alpha.xml"/>
      <outline type="rss" text="E2E Beta" xmlUrl="https://example.com/e2e-beta.xml"/>
    </outline>
    <outline type="rss" text="E2E Broken" xmlUrl="not a url"/>
  </body>
</opml>`;

/** Opens Settings and switches to the Data tab. */
async function openDataTab(page) {
  await boot(page);
  await page.keyboard.press(',');
  await expect(page.locator('.set-tabs')).toBeVisible();
  await page.locator('[data-action="settings-tab"][data-value="data"]').click();
  await expect(page.locator('[data-action="settings-tab"][data-value="data"]'))
    .toHaveAttribute('aria-current', 'true');
}

test.describe('settings › data', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('an OPML file dropped on the tab subscribes it, and says what it skipped', async ({ page }) => {
    await openDataTab(page);

    // The chooser is opened by the app, from inside the click's own gesture —
    // if that ever regresses to a deferred call that loses user activation,
    // this line is where it shows up, because no chooser event ever arrives.
    const [chooser] = await Promise.all([
      page.waitForEvent('filechooser'),
      page.locator('[data-action="data-import"]').click(),
    ]);
    await chooser.setFiles({
      name: 'somewhere-else.opml',
      mimeType: 'text/x-opml',
      buffer: Buffer.from(OPML, 'utf8'),
    });

    const panel = page.locator('.set-panel');
    // Two subscribed, one skipped, and the skipped row NAMED — "1 skipped" is
    // not something a reader can act on, which is why the list exists at all.
    await expect(panel).toContainText('An export from somewhere else', { timeout: 60_000 });
    await expect(panel.locator('.imp-skip')).toHaveCount(1);
    await expect(panel.locator('.imp-skip-name')).toContainText('E2E Broken');
    await expect(panel.locator('.imp-skip-why')).not.toBeEmpty();

    // The sidebar is the point of the feature. An import that reports success
    // and leaves the rail unchanged is the failure this asserts against — and
    // the category with it, because filing feeds into categories and showing
    // neither looks exactly like an import that did nothing.
    //
    // On a phone the sidebar is off screen while Settings is up — that is the
    // design — and Settings is reached from the tab bar rather than from a back
    // button, so fixtures' feedRow (which looks for "‹ Feeds") cannot get there
    // from here. The tab bar can.
    const rail = page.locator('.pane-rail');
    if (!(await rail.isVisible())) {
      await page.locator('[data-action="tab-feeds"]').click();
      await expect(rail).toBeVisible();
    }
    await expect(rail.getByRole('button', { name: 'E2E Alpha' }).and(rail.locator('.feed-row')))
      .toBeVisible({ timeout: 30_000 });
    await expect(rail).toContainText('Imported');
  });

  test('export downloads a file that contains the subscriptions', async ({ page }) => {
    await openDataTab(page);

    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.locator('[data-action="data-export"]').click(),
    ]);
    expect(download.suggestedFilename()).toBe('feeds.opml');

    const path = await download.path();
    const { readFileSync } = await import('node:fs');
    const body = readFileSync(path, 'utf8');
    expect(body).toContain('<opml');
    // Whatever the seeded database holds, an export of it is not an empty body:
    // a file with a header and no outlines is the roach motel with a download
    // button on it.
    expect(body).toContain('xmlUrl=');

    await expect(page.locator('.set-note-live')).toContainText('feeds.opml');
  });
});
