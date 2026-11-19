import { test, expect, boot, openAddFeed, openRail, openSettings, isPhone } from './fixtures.mjs';
import { FEED_ORIGIN } from './ports.mjs';

/**
 * Arm, then confirm: the removals that cannot be taken back from the screen
 * they are made on (client/view/confirm.go).
 *
 * The contract, which is the same for all four controls that share
 * armableRemove — Unsubscribe, Remove key, Clear passphrase, Forget this
 * theme:
 *
 *   - the control keeps its data-action and now only ARMS; nothing has happened
 *     yet, and the consequence is on screen in words;
 *   - Cancel takes focus, so a reflexive second Enter keeps the thing;
 *   - Escape (or Cancel) disarms, and focus goes back to the control that armed;
 *   - a NEW action, `<old>-confirm`, is the only thing that performs it.
 *
 * Two of the four are proven here. The other two cannot be reached honestly
 * from this suite's instance, and the reason is the thing they remove:
 *
 *   - Clear passphrase only appears once a passphrase is set, and setting one
 *     cuts a new key to the account — password.spec.mjs explains why completing
 *     that here would strand every later spec on a changed credential.
 *   - Forget this theme only appears once a theme has been COMPOSED, and
 *     composing one is a Smart+ call this instance has no key for.
 *
 * Both render through the same armableRemove as the two below, so what is left
 * unproven for them is their wiring to it, not the contract.
 */

/** disarm backs out of an armed confirmation the way the device would. */
async function disarm(page) {
  // Escape on a desktop — the contract names it, and it is the one a keyboard
  // reader reaches for. Cancel on a phone, which has no Escape to press.
  if (await isPhone(page)) {
    await page.locator('[data-role="arm-cancel"]').click();
  } else {
    await page.keyboard.press('Escape');
  }
}

test.describe('arm, then confirm', () => {
  test('Unsubscribe arms, Escape disarms, and only the confirm unsubscribes', async ({ page }, testInfo) => {
    // A feed of its own, so the one this test removes is one nobody else uses.
    // The fixture's beta feed under a query string the server cannot fold into
    // Beta Notes, and a title per project so the two projects' runs cannot
    // collide on one shared server.
    const title = `Unsub probe ${testInfo.project.name}`;
    await boot(page);
    const rail = await openRail(page);
    const subs = rail.locator('.feed-row:not(.stream-row)');
    const before = await subs.count();

    const dialog = await openAddFeed(page);
    await dialog.locator('[data-role="add-feed"]').fill(
      `${FEED_ORIGIN}/beta.xml?probe=${testInfo.project.name}`);
    await dialog.locator('[data-role="add-feed-title"]').fill(title);
    await dialog.locator('[data-action="add-feed"]').click();
    await expect(dialog).toBeHidden({ timeout: 45_000 });

    await openRail(page);
    const slot = rail.locator('.feed-slot', { hasText: title }).first();
    await expect(slot).toBeVisible({ timeout: 45_000 });
    // A NEW subscription, not Beta Notes refiled under a new name. If the
    // server had folded the address into the existing feed, the confirm below
    // would unsubscribe a fixture feed every later spec depends on — so this
    // stops the test first.
    await expect(subs).toHaveCount(before + 1);

    // Its settings, by the gear — hover first so the forced click cannot land
    // on a row that is still moving (tagsettings.spec.mjs, openTagSettings).
    await slot.hover();
    await slot.locator('[data-action="feed-settings"]').click({ force: true });
    const fs = page.getByRole('dialog', { name: 'Feed settings' });
    await expect(fs).toBeVisible({ timeout: 20_000 });

    // Arm. Nothing has happened: the feed is still in the rail, the
    // consequence is in words, and Cancel — not the destructive button — holds
    // focus.
    await fs.locator('[data-action="fs-unsubscribe"]').click();
    const confirm = fs.locator('[data-action="fs-unsubscribe-confirm"]');
    await expect(confirm).toBeVisible();
    await expect(fs.locator('[role="alert"]', { hasText: /Stop following/ })).toBeVisible();
    await expect(fs.locator('[data-role="arm-cancel"]')).toBeFocused();
    await expect(rail.locator('.feed-slot', { hasText: title })).toHaveCount(1);

    // Disarm. The confirmation is gone, the dialog is not, and focus is back
    // on the control that armed it — a keyboard reader is where they started.
    await disarm(page);
    await expect(confirm).toHaveCount(0);
    await expect(fs).toBeVisible();
    await expect(fs.locator('[data-role="arm-unsubscribe"]')).toBeFocused();
    await expect(rail.locator('.feed-slot', { hasText: title })).toHaveCount(1);

    // Arm again, and confirm: now, and only now, it goes.
    await fs.locator('[data-action="fs-unsubscribe"]').click();
    await confirm.click();
    await expect(rail.locator('.feed-slot', { hasText: title })).toHaveCount(0, { timeout: 45_000 });
    await expect(subs).toHaveCount(before, { timeout: 45_000 });
  });

  test('Remove key arms, Escape disarms, and only the confirm removes it', async ({ page }) => {
    // A key has to be stored before it can be removed. The server checks the
    // SHAPE of a key, not whether it works (grpcsrv SetSmartConfig), so a
    // placeholder with the right prefix is stored and nothing ever spends it:
    // this test removes it before it ends.
    await boot(page);
    await openSettings(page);
    await page.locator('[data-action="settings-tab"][data-value="smart"]').click();
    await page.locator('[data-role="smart-key"]').fill('sk-e2e-placeholder-not-a-real-key');
    await page.locator('[data-action="smart-key-save"]').click();

    const arm = page.locator('[data-action="smart-key-clear"]');
    await expect(arm).toBeVisible({ timeout: 30_000 });

    // Arm. The key is still configured: the confirming control is up instead
    // of the arming one, the consequence is said, and Cancel has focus.
    await arm.click();
    const confirm = page.locator('[data-action="smart-key-clear-confirm"]');
    await expect(confirm).toBeVisible();
    await expect(page.locator('[role="alert"]', { hasText: /Every Smart\+ feature stops/ }))
      .toBeVisible();
    await expect(page.locator('[data-role="arm-cancel"]')).toBeFocused();

    // Disarm, and focus returns to Remove key.
    await disarm(page);
    await expect(confirm).toHaveCount(0);
    await expect(page.locator('[data-role="arm-smart-key"]')).toBeFocused();

    // Arm again and confirm: the key is gone, so the control that removes it is
    // too — it only exists when there is something to remove.
    await arm.click();
    await confirm.click();
    await expect(page.locator('[data-action="smart-key-clear"]')).toHaveCount(0, { timeout: 30_000 });
    await expect(confirm).toHaveCount(0);
  });
});
