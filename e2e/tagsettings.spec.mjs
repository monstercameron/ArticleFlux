import { test, expect, boot, openNote, openRail, openRow, isPhone } from './fixtures.mjs';

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

/** tagFirstArticle files the first article's feed under name. */
async function tagFirstArticle(page, name) {
  await openRow(page, 0);
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

/**
 * backToArticle puts the tagged article on screen again, on a phone.
 *
 * The rail and the article are separate panes there, so after a trip to the
 * rail for the tag panel, the article's chip is in the DOM and not showing. A
 * phone reader gets back to it the only way there is — the Read tab, then the
 * article — and the article is row 0 of All, which is what tagFirstArticle
 * tagged. A no-op on a wide viewport, where the article never left.
 */
async function backToArticle(page) {
  if (!(await isPhone(page))) return;
  // The panel is modal, and its scrim would take the tap meant for the tab bar.
  // Asked of the scrim's `data-open`, not of visibility: a panel the test has
  // just closed is still visible for its exit transition, and pressing Close
  // on it then waits out the timeout on a button that is fading away.
  const panel = page.locator('[aria-label="Tag settings"]');
  const open = page.locator('.pal-scrim[data-open="true"]:has([aria-label="Tag settings"])');
  if (await open.count()) {
    await panel.locator('button[data-action="tag-settings-close"]').click();
  }
  await expect(panel).toBeHidden();
  await openRow(page, 0);
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
  //
  // The hover first is what makes force:true safe. Forcing skips Playwright's
  // wait for the element to stop MOVING, and right after tagFirstArticle the
  // rail is still settling — on a phone it is sliding in, and on any width the
  // refetch that adds the tag's row can shift the rows under it. A forced click
  // then lands where the gear was, which is on the tag's row, and the row's own
  // click selects the tag: the run that failed here was sitting on the "rust"
  // list with the panel never opened (once on desktop, every time on a phone).
  // Hovering the slot waits for it to be stable, which forcing does not.
  await slot.hover();
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

    // The panel still reports the underlying tag, unchanged. This is the
    // assertion the whole feature turns on. Asked while the panel is still open:
    // on a phone the trip back to the article below has to close it first.
    await expect(panel.locator('.chip-static', { hasText: /^rust$/ })).toBeVisible();

    // ...and the chip on the article says the TAG, not the label: the label is
    // what the sidebar calls the row, and the panel's own copy promises the chips
    // say the tag (FEATURES 11a). It used to draw the label, so a renamed tag
    // read "Systems programming" under every article and in the remove button's
    // name, while the tag field beside it still wanted "rust" (G3).
    await backToArticle(page);
    await expect(article.locator('.tag-chip', { hasText: 'rust' }))
      .toBeVisible({ timeout: 45_000 });
    await expect(article.locator('.tag-chip', { hasText: 'Systems programming' })).toHaveCount(0);
    await expect(article.locator('.tag-x')).toHaveAttribute('aria-label', 'Remove tag rust');

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
    await backToArticle(page);
    const chip = article.locator('.tag-chip', { hasText: 'rust' });
    await expect(chip).toBeVisible({ timeout: 45_000 });
    // The ×, by its action — not the chip. A chip's label is a LINK now
    // (rowlinks.spec.mjs: it routes to the tag and deliberately removes
    // nothing), so a click on the chip's centre lands on the label, changes
    // scope, and unmounts the article. The count below then read zero because
    // the article was gone rather than because the tag was — and the reload at
    // the end found the tag still filed, which is the failure this test showed.
    await chip.locator('[data-action="remove-tag"]').click();
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
    await panel.locator('.ts-glyph[aria-label="Default symbol"]').click();
    await expect(row.locator('.tag-dot')).toHaveCount(1, { timeout: 45_000 });
    await expect(row.locator('.tag-gl')).toHaveCount(0);
  });
});
