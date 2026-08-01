import { test, expect, boot, currentArticle, openRail } from './fixtures.mjs';

/**
 * Clicking through from a feed title or a tag, without opening the article
 * it happened to be sitting on.
 *
 * The category chip is not covered here: this fixture never runs
 * classification (see uncategorised.spec.mjs's own note on this), so no
 * seeded article ever carries a category and there is no route for a click
 * on one to prove out against real data. That markup and the row's
 * nested-click guard are pinned instead in client/view/rowlinks_test.go,
 * against a synthetic item built with GetCategory() set directly.
 *
 * What both cases below CAN prove, against the real fixture: that the title
 * and the tag route to the place their id names, and that clicking either
 * one — sitting as they do inside a row whose OWN click opens the article —
 * does the ONE thing, not both.
 */

function path(page) {
  const u = new URL(page.url());
  return u.pathname + u.search;
}

function place(page) {
  return path(page).replace(/\/read\/[^/?]+/, '') || '/';
}

test.describe('feed title links', () => {
  test('clicking the feed title in the list routes to that feed', async ({ page }) => {
    await boot(page);

    const row = page.locator('.item-row').first();
    const sourceID = await row.locator('.item-source').getAttribute('data-source-id');
    await expect(row).toHaveAttribute('data-read', 'false');

    await row.locator('.item-source').click();

    // Routed to that exact feed — not just "a" feed route.
    await expect.poll(() => place(page)).toBe(`/feed/${sourceID}`);
    await expect(page.locator('.pane-list')).toBeVisible();
  });

  // The row is a <button> that opens ITS article on click; the chip nested
  // inside carries data-source-id and must win instead — see
  // OnDelegatedRowClick's doc comment in client/platform/platform_wasm.go.
  // The last row is the fixture's OLDEST article and not its own feed's
  // topmost, so if the row's click fired instead of the chip's, the article
  // that ends up open would be this row's own headline rather than the one
  // its feed actually leads with.
  test('the feed title wins over the row\'s own click, even nested inside it', async ({ page }) => {
    await boot(page);

    const row = page.locator('.item-row').last();
    const ownTitle = await row.locator('.item-title').textContent();
    const sourceID = await row.locator('.item-source').getAttribute('data-source-id');

    await row.locator('.item-source').click();
    await expect.poll(() => place(page)).toBe(`/feed/${sourceID}`);

    // Whatever article the scope change auto-opened, it must not be THIS
    // row's — the row itself was never clicked, only the chip inside it.
    await expect.poll(() => path(page)).toMatch(/\/read\/[^/]+$/);
    await expect(currentArticle(page).locator('h1')).not.toHaveText(ownTitle);
  });

  test('clicking the feed title in an open article routes to that feed', async ({ page }) => {
    await boot(page);
    await page.locator('.item-row').first().click();
    await expect(currentArticle(page).locator('h1')).toBeVisible();

    await currentArticle(page).locator('.item-source').first().click();

    await expect.poll(() => place(page)).toMatch(/^\/feed\/[^/]+$/);
    await expect(page.locator('.pane-list')).toBeVisible();
  });
});

/** openNote reveals the note-and-tag editor on the article being read. */
async function openNote(page) {
  const field = page.locator('.note-field').first();
  if (!(await field.count())) {
    await page.locator('[data-action="toggle-note"]').first().click();
  }
  await expect(field).toBeVisible({ timeout: 15_000 });
  return field;
}

/** tagFirstArticle files the first article's feed under name — same recipe
 * tagsettings.spec.mjs uses, waiting for the server rather than the optimistic
 * chip. */
async function tagFirstArticle(page, name) {
  await page.locator('.item-row').first().click();
  await openNote(page);
  const panel = page.locator('.article-note').first();
  await panel.locator('[data-role="tag"]').first().fill(name);
  await panel.locator('[data-action="add-tag"]').first().click();

  await expect(panel.locator('.tag-chip-pending')).toHaveCount(0, { timeout: 45_000 });
  const chip = panel.locator('.tag-chip', { hasText: name });
  await expect(chip).toBeVisible({ timeout: 45_000 });
  return chip;
}

test.describe('tag chip links', () => {
  test('clicking a tag chip label routes to that tag, and does not remove it', async ({ page }) => {
    await boot(page);
    const chip = await tagFirstArticle(page, 'rust');

    // The label, not the × — [data-tag-id] is what the label carries.
    await chip.locator('[data-tag-id]').click();

    await expect.poll(() => place(page)).toMatch(/^\/tag\/[^/]+$/);
    await expect(page.locator('.pane-list')).toContainText('rust');

    // Still filed. A click that also removed it would leave the rail's tag
    // row gone, or its count at zero.
    const rail = await openRail(page);
    await expect(rail.locator('[data-tag-id]').filter({ hasText: 'rust' })).toBeVisible();
  });

  test('the × still removes the tag, and only the tag — no navigation', async ({ page }) => {
    await boot(page);
    const chip = await tagFirstArticle(page, 'golang');
    const before = path(page);

    await chip.locator('[data-action="remove-tag"]').click();

    await expect(chip).toHaveCount(0, { timeout: 45_000 });
    // No route change: removing is not one of the three links this feature
    // adds, so the address the reader was on (still reading the article they
    // tagged) must be exactly what it was before the click.
    expect(path(page)).toBe(before);
  });
});
