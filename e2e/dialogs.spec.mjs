import { test, expect, boot, openRail, openAddFeed } from './fixtures.mjs';
import { FEED_ORIGIN } from './ports.mjs';

/**
 * All six dialogs, put through the same cycle.
 *
 * client/view/modal.go's `scrim` is the backdrop every one of them shares —
 * Add a feed, Category, Feed settings, Tag settings, the shortcut sheet and the
 * command palette — and the reason it exists as a shared function is that all
 * six used to unmount on close and therefore could only ever animate ARRIVING.
 * Now they stay in the document at `data-open="false"` so they have something
 * to animate on the way out, at the cost that a closed dialog's markup is still
 * there unless `visibility: hidden` takes it out of the tab order.
 *
 * motion.spec.mjs already proves the FULL gesture — timed animation, exit
 * quicker than entrance, computed styles mid-transition — for one of the six,
 * the palette, because the palette is also where a ranked list makes "is it
 * expensive while closed" worth checking. What nothing proved before this file
 * is that the other five honour the same `data-open` / untabbable contract:
 * five different Go files call the same `scrim()`, which does not guarantee
 * five different call sites all pass it the right arguments.
 */

test.describe('every dialog', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
    await page.keyboard.press('Escape').catch(() => {});
    await page.waitForTimeout(200);
  });

  /** visibility reads the computed style the same way motion.spec.mjs does. */
  async function visibility(page, selector) {
    return page.evaluate((s) => getComputedStyle(document.querySelector(s)).visibility, selector);
  }

  /**
   * cycle drives one dialog through closed -> open -> closed and asserts the
   * shared contract at each step, by NAME rather than by class — several of
   * these dialogs share a class ("fs" for both Feed settings and Tag settings),
   * so the accessible name is the only thing that tells them apart, which is
   * also the thing a screen reader actually has.
   */
  async function cycle(page, name, openIt, closeIt,
    { mountedBeforeOpen = true, strictExit = true } = {}) {
    const scrimSel = `.pal-scrim:has([aria-label="${name}"])`;
    const dialog = page.locator(`[aria-label="${name}"][role="dialog"]`);

    if (mountedBeforeOpen) {
      await expect(page.locator(scrimSel)).toHaveAttribute('data-open', 'false');
      expect((await visibility(page, scrimSel)).trim()).toBe('hidden');
    } else {
      // Tag settings is the one dialog that does NOT pre-mount
      // (client/view/tagsettings.go: "The one dialog that still guards, and
      // only on the tag" — there is nothing honest to render before a tag has
      // ever been chosen). Not present is its correct closed state here.
      await expect(dialog).toHaveCount(0);
    }

    await openIt();
    await expect(page.locator(scrimSel)).toHaveAttribute('data-open', 'true', { timeout: 20_000 });
    await expect(dialog).toBeVisible();
    expect((await visibility(page, scrimSel)).trim()).toBe('visible');

    await closeIt();

    if (!strictExit) {
      // A looser "did it actually close" check, for a dialog whose exit
      // ANIMATION is not what this particular cycle is verifying — see the
      // dedicated regression test below for that contract on Tag settings.
      await expect(dialog).toHaveCount(0, { timeout: 20_000 });
      return;
    }

    await expect(page.locator(scrimSel)).toHaveAttribute('data-open', 'false', { timeout: 20_000 });
    // The exit transition is deliberately brisk (motion.spec.mjs) but is still a
    // transition; visibility flips at its end, not at the moment data-open does.
    await expect.poll(() => visibility(page, scrimSel), { timeout: 5_000 })
      .toBe('hidden');

    // Untabbable when closed: visibility:hidden removes an element from the tab
    // order by the CSS spec, which is what the check above already establishes
    // — restated here as the property the reader actually experiences, so a
    // future change to how "closed" is expressed (opacity, display) trips this
    // rather than only the internal-looking visibility check above.
    const reachable = await page.evaluate((s) => {
      const el = document.querySelector(s);
      if (!el) return false;
      const r = el.getBoundingClientRect();
      // elementFromPoint returns null / a different element for anything a
      // pointer or the tab order cannot land on.
      const hit = document.elementFromPoint(
        Math.max(1, r.left + 1), Math.max(1, r.top + 1));
      return !!hit && el.contains(hit);
    }, `[aria-label="${name}"][role="dialog"]`);
    expect(reachable, `${name} is still hit-testable while closed`).toBe(false);
  }

  // closeButton scopes to the header button INSIDE the dialog, by its
  // accessible name, rather than by `[data-action="X-close"]` bare: the same
  // action name is deliberately shared by the backdrop (click-outside-closes)
  // and the header button (client/view/modal.go's `scrim` puts the action on
  // both), so an unscoped selector's `.first()` is the FULL-VIEWPORT backdrop
  // in every dialog wide enough to cover the page centre — a `force: true`
  // click on it then lands on the dialog panel sitting on top, which is the
  // `modal-keep` no-op, not a close. This cost real time to find: the failure
  // looked exactly like "the dialog will not close", because from data-open's
  // point of view, that is what happened.
  function closeButton(page, name) {
    return page.locator(`[aria-label="${name}"][role="dialog"]`).getByRole('button', { name: /close/i });
  }

  test('Add a feed opens, closes, and is untabbable when closed', async ({ page }) => {
    await boot(page);
    await cycle(page, 'Add a feed',
      async () => { await openRail(page); await page.locator('[data-action="add-feed-open"]').click(); },
      async () => { await closeButton(page, 'Add a feed').click(); });
  });

  test('Feed settings opens, closes, and is untabbable when closed', async ({ page }) => {
    await boot(page);
    await cycle(page, 'Feed settings',
      async () => {
        const rail = await openRail(page);
        const row = rail.locator('.feed-slot', { hasText: 'Alpha Journal' }).first();
        // force:true: the gear is opacity:0 until hover on a desktop viewport
        // (tagsettings.spec.mjs's openTagSettings does the same for the
        // identical reason), which is the design, not something this cycle is
        // testing. Without the hover first, Playwright's own actionability
        // check sees the row underneath as the thing intercepting the click.
        await row.hover();
        await row.getByRole('button', { name: /Settings for Alpha Journal/ }).click({ force: true });
      },
      async () => { await closeButton(page, 'Feed settings').click(); });
  });

  test('Keyboard shortcuts opens, closes, and is untabbable when closed', async ({ page }) => {
    await boot(page);
    await cycle(page, 'Keyboard shortcuts',
      async () => { await page.keyboard.press('?'); },
      async () => { await page.keyboard.press('Escape'); });
  });

  test('Command palette opens, closes, and is untabbable when closed', async ({ page }) => {
    await boot(page);
    await cycle(page, 'Command palette',
      async () => { await page.keyboard.press('Control+k'); },
      async () => { await page.keyboard.press('Escape'); });
  });

  /** tagUp files an article under TAG and returns its rail slot, gear-hoverable. */
  async function tagUp(page, TAG) {
    await page.locator('.item-row').first().click();
    await page.locator('[data-action="toggle-note"]').first().click();
    const panel = page.locator('.article-note').first();
    await panel.locator('[data-role="tag"]').first().fill(TAG);
    await panel.locator('[data-action="add-tag"]').first().click();
    await expect(panel.locator('.tag-chip-pending')).toHaveCount(0, { timeout: 45_000 });

    const rail = await openRail(page);
    const slot = rail.locator('.feed-slot')
      .filter({ has: page.locator('[data-tag-id]') })
      .filter({ hasText: TAG })
      .first();
    await expect(slot).toBeVisible({ timeout: 45_000 });
    return slot;
  }

  // Was two tests: one for mount timing (`mountedBeforeOpen: false`,
  // `strictExit: false`) and a `test.fail()` documenting that Tag settings
  // closed with a hard cut instead of the shared exit animation (TODO
  // testing-campaign Q3). Both premises are stale — collapsed into one test
  // 2026-07-31, by reading rather than a live Playwright run (ports 9400-9500
  // off limits this session; worth a live confirmation pass before trusting
  // this fully). `client/view/tagsettings.go`'s `tagSettings()` no longer has
  // an early return on `p.t == nil`: the doc comment there now reads "No
  // early return on p.t == nil: like its five siblings … this panel is
  // rendered unconditionally and leans on scrim's data-open attribute to
  // carry open/closed." `client/view/reader.go` calls `tagSettings(tr,
  // tagSettingsProps{...})` unconditionally too (no `ui.If` gate), so the
  // scrim + dialog markup — including its `aria-label` — is now in the DOM
  // from boot, exactly like the other five. That removes both the "not
  // present before it ever has" premise (mountedBeforeOpen was never really
  // false any more) and the "hard cut" premise (nothing tears the subtree out
  // mid-transition any more) in the same change, so the standard `cycle()`
  // call — the one all four other siblings above use — is the correct test.
  test('Tag settings opens, closes, and is untabbable when closed', async ({ page }) => {
    await boot(page);
    // A tag has to exist before it can be configured; tags are created by use,
    // through the article panel — the same route reader.spec.mjs and
    // tagsettings.spec.mjs use.
    const slot = await tagUp(page, 'kbdmap1');

    await cycle(page, 'Tag settings',
      // force:true: the gear is opacity:0 until hover on desktop, which is the
      // design, not something this cycle is testing.
      async () => { await slot.locator('[data-action="tag-settings"]').click({ force: true }); },
      async () => { await closeButton(page, 'Tag settings').click(); });
  });

  test('Category opens, closes, and is untabbable when closed', async ({ page }, testInfo) => {
    // Named per project, same reasoning as reader.spec.mjs's filing test: the
    // two projects share a server, and a fixed name would race.
    const name = `Kbd ${testInfo.project.name}`;
    await boot(page);

    // Re-filing an address already subscribed refiles it rather than erroring
    // (reader.spec.mjs), so this does not create a second Alpha Journal.
    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill(`${FEED_ORIGIN}/alpha.xml`);
    await dialog.locator('[data-action="add-feed-new-category"]').click();
    await dialog.locator('[data-role="add-feed-category"]').fill(name);
    await dialog.locator('[data-action="add-feed"]').click();
    await expect(dialog).toBeHidden({ timeout: 45_000 });

    const rail = await openRail(page);
    const slot = rail.locator('.cat-slot', { hasText: name });
    await expect(slot.locator('.cat-row')).toBeVisible({ timeout: 45_000 });

    await cycle(page, 'Category',
      async () => { await slot.hover(); await slot.locator('[data-action="category-open"]').click(); },
      async () => { await closeButton(page, 'Category').click(); });

    // Left filed rather than deleted: deleting it is reader.spec.mjs's job, and
    // doing it here too would just be two tests proving the same thing.
  });
});
