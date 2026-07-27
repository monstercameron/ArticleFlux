import { test, expect, boot, openRail } from './fixtures.mjs';

/**
 * The tag panel: renaming a tag's row without renaming the tag.
 *
 * Its own file rather than another case in reader.spec.mjs because what it is
 * really testing is an INVARIANT that spans three surfaces — the rail, the
 * article chip and the server — rather than one interaction. The feature is
 * two names for one object, and every way it can go wrong is a place where one
 * of those names leaked into the other's job.
 *
 * A tag has to exist before it can be configured, and tags are created by use.
 * So each test files a feed first, through the same article panel a reader
 * would use.
 */

/** openNote reveals the note-and-tag editor on the article being read. */
async function openNote(page) {
  const field = page.locator('.note-field').first();
  if (!(await field.count())) {
    await page.locator('[data-action="toggle-note"]').first().click();
  }
  await expect(field).toBeVisible({ timeout: 15_000 });
  return field;
}

/** tagFirstArticle files the first article's feed under name. */
async function tagFirstArticle(page, name) {
  await page.locator('.item-row').first().click();
  await openNote(page);
  const panel = page.locator('.article-note').first();
  await panel.locator('[data-role="tag"]').first().fill(name);
  await panel.locator('[data-action="add-tag"]').first().click();

  // Wait for the SERVER to have it, not merely for the screen to show it.
  //
  // The chip appears instantly now — adding is acknowledged optimistically, in a
  // pending state — so a test that waits for a chip is satisfied before the RPC
  // has left. Waiting for the pending state to CLEAR is the honest gate: it goes
  // away only when the call has come back, which is the precondition everything
  // below actually needs.
  await expect(panel.locator('.tag-chip-pending')).toHaveCount(0, { timeout: 45_000 });
  await expect(panel.locator('.tag-chip', { hasText: name })).toBeVisible({ timeout: 45_000 });

  // And then the rail, which is refetched rather than patched and so is the last
  // thing to catch up.
  const rail = await openRail(page);
  await expect(rail.locator('[data-tag-id]').filter({ hasText: name }))
    .toBeVisible({ timeout: 45_000 });
  return panel;
}

/** openTagSettings clicks the gear on a tag's rail row. */
async function openTagSettings(page, name) {
  const rail = await openRail(page);
  const slot = rail.locator('.feed-slot')
    .filter({ has: page.locator('[data-tag-id]') })
    .filter({ hasText: name })
    .first();
  // The gear is hidden until hover on a desktop viewport, which is the design.
  // force:true rather than hovering first: the element is opacity:0, not
  // display:none, so it is present, hit-testable after the hover Playwright
  // does implicitly, and this removes one source of flake for a control whose
  // visibility is not what the test is about.
  await slot.locator('[data-action="tag-settings"]').click({ force: true });
  const panel = page.locator('[aria-label="Tag settings"]');
  await expect(panel).toBeVisible({ timeout: 30_000 });
  return panel;
}

test.describe('tag settings', () => {
  test('renaming a tag renames its row and leaves the tag alone', async ({ page }) => {
    await boot(page);
    const article = await tagFirstArticle(page, 'rust');

    const panel = await openTagSettings(page, 'rust');

    // The field is seeded EMPTY, showing the tag's name as a placeholder. Seeded
    // with the name it would make every unrenamed tag look renamed, and pressing
    // Rename without typing would store a copy of the name as an override.
    const field = panel.locator('[data-role="tag-label"]');
    await expect(field).toHaveValue('');
    await expect(field).toHaveAttribute('placeholder', 'rust');

    await field.fill('Systems programming');
    await panel.locator('[data-action="ts-rename"]').click();

    // The rail now says the label...
    const rail = await openRail(page);
    await expect(rail.locator('[data-tag-id]').filter({ hasText: 'Systems programming' }))
      .toBeVisible({ timeout: 45_000 });

    // ...and so does the chip on the article, because one tag reading as two
    // different things in two panes is the failure this feature invites.
    await expect(article.locator('.tag-chip', { hasText: 'Systems programming' }))
      .toBeVisible({ timeout: 45_000 });

    // The panel still reports the underlying tag, unchanged. This is the
    // assertion the whole feature turns on.
    await expect(panel.locator('.chip-static', { hasText: /^rust$/ })).toBeVisible();

    // And it survives a reload, so it was stored rather than rendered.
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    const railAfter = await openRail(page);
    await expect(railAfter.locator('[data-tag-id]').filter({ hasText: 'Systems programming' }))
      .toBeVisible({ timeout: 45_000 });
  });

  test('a renamed tag is still removed by its own name', async ({ page }) => {
    await boot(page);
    const article = await tagFirstArticle(page, 'rust');

    const panel = await openTagSettings(page, 'rust');
    await panel.locator('[data-role="tag-label"]').fill('Systems programming');
    await panel.locator('[data-action="ts-rename"]').click();
    // The Close BUTTON, not the scrim — both carry the close action, which is
    // the point of putting it on the scrim, and a bare attribute selector
    // matches them both.
    await panel.locator('button[data-action="tag-settings-close"]').click();

    // The chip says the label; SetFeedTag is keyed by the name. If the chip sent
    // what it displays, this click asks the server to remove a tag called
    // "Systems programming", which does not exist — and the chip stays put.
    const chip = article.locator('.tag-chip', { hasText: 'Systems programming' });
    await expect(chip).toBeVisible({ timeout: 45_000 });
    await chip.click();
    await expect(article.locator('.tag-chip')).toHaveCount(0, { timeout: 45_000 });

    // The last association takes the tag with it, label and all.
    await page.reload();
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
    const rail = await openRail(page);
    await expect(rail.locator('[data-tag-id]')).toHaveCount(0, { timeout: 45_000 });
  });

  test('clearing the name restores the tag’s own', async ({ page }) => {
    await boot(page);
    await tagFirstArticle(page, 'rust');

    let panel = await openTagSettings(page, 'rust');
    await panel.locator('[data-role="tag-label"]').fill('Systems programming');
    await panel.locator('[data-action="ts-rename"]').click();

    const rail = await openRail(page);
    await expect(rail.locator('[data-tag-id]').filter({ hasText: 'Systems programming' }))
      .toBeVisible({ timeout: 45_000 });

    // The save writes the stored label back into the field, and in headless
    // Chromium that render can be a second behind the rail. Clearing before it
    // lands would be typing into a field that is about to be overwritten.
    const field = panel.locator('[data-role="tag-label"]');
    await expect(field).toHaveValue('Systems programming', { timeout: 45_000 });

    // An empty field is a real value here — it clears the override — rather than
    // a no-op the panel declines to send.
    await field.fill('');
    await expect(field).toHaveValue('');
    await panel.locator('[data-action="ts-rename"]').click();
    await expect(rail.locator('[data-tag-id]').filter({ hasText: 'rust' }))
      .toBeVisible({ timeout: 45_000 });
  });

  test('choosing a glyph marks the row, and the default puts the dot back', async ({ page }) => {
    await boot(page);
    await tagFirstArticle(page, 'rust');

    const panel = await openTagSettings(page, 'rust');

    // Fifty of them, plus the default cell. The count is the specification, and
    // a picker that quietly renders 43 is a picker somebody edited a list in.
    await expect(panel.locator('.ts-glyph')).toHaveCount(51);

    // Named, not just drawn: a grid of unlabelled symbols is unusable with a
    // screen reader, and the name is the only thing separating ◆ from ◈.
    const atom = panel.locator('.ts-glyph[aria-label="Atom"]');
    await atom.click();

    const rail = await openRail(page);
    const row = rail.locator('.feed-slot')
      .filter({ has: page.locator('[data-tag-id]') })
      .filter({ hasText: 'rust' }).first();

    // The glyph replaces the plain dot rather than joining it.
    await expect(row.locator('.tag-gl')).toHaveText('⚛', { timeout: 45_000 });
    await expect(row.locator('.tag-dot')).toHaveCount(0);
    await expect(atom).toHaveAttribute('aria-pressed', 'true');

    // The default is one of the choices, not an escape hatch beside them.
    await panel.locator('.ts-glyph[aria-label="Default mark"]').click();
    await expect(row.locator('.tag-dot')).toHaveCount(1, { timeout: 45_000 });
    await expect(row.locator('.tag-gl')).toHaveCount(0);
  });
});
