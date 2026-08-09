import { test, expect, boot } from './fixtures.mjs';

/**
 * Changing your password, on Settings → Account (§7.3).
 *
 * The RPC has been there since sudo mode was built and had no control anywhere:
 * the tab carried a sentence headed "Not on this screen yet" and the documented
 * way to change a password was a shell on the server. This is the browser half
 * of that gap, so what is worth proving here is the wiring a Go test cannot
 * reach — that the fields exist, that the rules react to typing, and that a
 * refusal from the policy comes back as a sentence rather than as silence.
 *
 * # What this deliberately does NOT do
 *
 * It never completes a change. The suite shares one server and one account, and
 * a spec that actually changed the password would invalidate the session every
 * later spec is using — the exact class of cross-file leak that cost this suite
 * fifty-five failures. So the success path is left to the unit tests and to the
 * server's own coverage, and this drives the paths that end in a refusal.
 */

async function openAccount(page) {
  await page.keyboard.press(',');
  await expect(page.locator('.set-tabs')).toBeVisible();
  await page.locator('[data-action="settings-tab"][data-value="account"]').click();
  await expect(page.locator('[data-action="settings-tab"][data-value="account"]'))
    .toHaveAttribute('aria-current', 'true');
}

test.describe('changing the password', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('the Account tab asks for the current password, the new one, and offers a rename', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    // The current password is ALWAYS asked for. The sudo window alone would
    // leave the fifteen minutes after a login open to anyone at an unattended
    // screen, and this call revokes every other session — see
    // grpcsrv.proveCurrentPassword.
    await expect(page.locator('[data-role="pw-confirm"]')).toBeVisible({ timeout: 30_000 });
    await expect(page.locator('[data-role="pw-new"]')).toBeVisible();
    await expect(page.locator('[data-role="pw-repeat"]')).toBeVisible();
    // One confirmation for both writes, not one each.
    await expect(page.locator('[data-role="pw-confirm"]')).toHaveCount(1);
    await expect(page.locator('[data-role="name-new"]')).toBeVisible();
  });

  test('a rename that is not an email address is refused', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    await page.locator('[data-role="name-new"]').fill('cam');
    await page.locator('[data-action="name-change"]').click();

    await expect(page.locator('.fs-error')).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('.fs-error')).toContainText(/email address/i);
  });

  test('a password change asks for confirmation, and says what it will do', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    await page.locator('[data-role="pw-confirm"]').fill('any-current-password');
    await page.locator('[data-role="pw-new"]').fill('harbour tin lantern');
    await page.locator('[data-role="pw-repeat"]').fill('harbour tin lantern');
    await page.locator('[data-action="pw-change"]').click();

    const dialog = page.locator('.cred-confirm');
    await expect(dialog).toBeVisible({ timeout: 20_000 });
    // The consequence that is invisible from the form is the one worth naming.
    await expect(dialog).toContainText(/signs out every other device/i);
    await expect(dialog).toContainText(/keeps you signed in here/i);

    // Cancelling changes nothing and keeps what was typed — the reader said
    // "not yet", not "start again".
    await page.locator('button[data-action="cred-cancel"]').click();
    await expect(dialog).toHaveCount(0);
    await expect(page.locator('[data-role="pw-new"]')).toHaveValue('harbour tin lantern');
  });

  test('a rename asks for confirmation, naming both the new and the old name', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    await page.locator('[data-role="pw-confirm"]').fill('any-current-password');
    await page.locator('[data-role="name-new"]').fill('someone.else@example.com');
    await page.locator('[data-action="name-change"]').click();

    const dialog = page.locator('.cred-confirm');
    await expect(dialog).toBeVisible({ timeout: 20_000 });
    await expect(dialog).toContainText('someone.else@example.com');
    await expect(dialog).toContainText(/stays signed in/i);

    await page.locator('button[data-action="cred-cancel"]').click();
    await expect(dialog).toHaveCount(0);
  });

  test('the confirmation is actually in the viewport, including on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await boot(page);
    await openAccount(page);

    await page.locator('[data-role="pw-confirm"]').fill('any-current-password');
    await page.locator('[data-role="name-new"]').fill('someone.else@example.com');
    await page.locator('[data-action="name-change"]').click();

    const dialog = page.locator('.cred-confirm');
    await expect(dialog).toBeVisible({ timeout: 20_000 });

    // toBeVisible is NOT this assertion. It means "rendered with a non-zero
    // box", and it passed while the dialog sat off the top of the screen: it is
    // position:fixed, and mounted inside the settings panel it resolved against
    // `.panes`, which transforms. Only a screenshot showed it. This is that
    // check, written down — the box has to be inside the viewport.
    const box = await dialog.boundingBox();
    const view = page.viewportSize();
    expect(box, 'the dialog has no box at all').not.toBeNull();
    expect(box.y, 'the dialog is off the top of the screen').toBeGreaterThanOrEqual(0);
    expect(box.x, 'the dialog is off the left of the screen').toBeGreaterThanOrEqual(0);
    expect(box.y + box.height, 'the dialog runs off the bottom').toBeLessThanOrEqual(view.height);
    expect(box.x + box.width, 'the dialog runs off the right').toBeLessThanOrEqual(view.width);
  });

  test('Escape closes the confirmation and leaves the screen behind it', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    await page.locator('[data-role="pw-confirm"]').fill('any-current-password');
    await page.locator('[data-role="pw-new"]').fill('harbour tin lantern');
    await page.locator('[data-role="pw-repeat"]').fill('harbour tin lantern');
    await page.locator('[data-action="pw-change"]').click();
    await expect(page.locator('.cred-confirm')).toBeVisible({ timeout: 20_000 });

    // Escape peels ONE layer. It used to fall through to the settings panel and
    // close both, so somebody who hesitated at the question lost the dialog, the
    // screen and the three fields they had filled in.
    await page.keyboard.press('Escape');
    await expect(page.locator('.cred-confirm')).toHaveCount(0);
    await expect(page.locator('.set-tabs')).toBeVisible();
    await expect(page.locator('[data-role="pw-new"]')).toHaveValue('harbour tin lantern');
  });

  test('the rules answer as you type, and the breached-list one waits for the server', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    const rules = page.locator('.pw-rule');
    await expect(rules).toHaveCount(4);

    // Nothing typed: no rule is passing or failing yet. A red mark against an
    // empty field is the interface telling somebody off for not having started.
    await expect(page.locator('.pw-rule[data-state="met"]')).toHaveCount(0);
    await expect(page.locator('.pw-rule[data-state="unmet"]')).toHaveCount(0);

    // Too short, and the length rule says so without a round trip.
    await page.locator('[data-role="pw-new"]').fill('short');
    await expect(page.locator('.pw-rule[data-state="unmet"]').first()).toBeVisible();

    // Long enough, and matching.
    await page.locator('[data-role="pw-new"]').fill('harbour tin lantern');
    await page.locator('[data-role="pw-repeat"]').fill('harbour tin lantern');
    await expect(page.locator('.pw-rule[data-state="met"]')).toHaveCount(3);

    // The fourth never turns green on its own: whether a password is on the
    // bundled breached list is the server's answer, and claiming it here would
    // be the screen promising something it cannot check.
    await expect(page.locator('.pw-rule').nth(3)).not.toHaveAttribute('data-state', 'met');
  });

  test('two different entries are refused here, before the server is asked', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    await page.locator('[data-role="pw-confirm"]').fill('any-current-password');
    await page.locator('[data-role="pw-new"]').fill('harbour tin lantern');
    await page.locator('[data-role="pw-repeat"]').fill('harbour tin lantorn');
    await page.locator('[data-action="pw-change"]').click();

    await expect(page.locator('.fs-error')).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('.fs-error')).toContainText(/different/i);
  });

  test('a password the policy refuses comes back as a sentence', async ({ page }) => {
    await boot(page);
    await openAccount(page);

    // Long enough to pass the local length rule, and on the bundled list once
    // folded — so the refusal can only have come from the server, which is the
    // point of the assertion.
    await page.locator('[data-role="pw-confirm"]').fill('any-current-password');
    await page.locator('[data-role="pw-new"]').fill('passwordpassword');
    await page.locator('[data-role="pw-repeat"]').fill('passwordpassword');
    await page.locator('[data-action="pw-change"]').click();
    // Through the confirmation: the local checks pass, so this password only
    // meets the policy at the server — which is the point of the assertion.
    await page.locator('button[data-action="cred-confirm"]').click();

    await expect(page.locator('.fs-error')).toBeVisible({ timeout: 30_000 });
    // The catalog's own wording for `srv.weakPassword`, not pwpolicy's. The
    // server sends a KEY plus an English fallback and the client resolves the
    // key against its catalog, because the reader's language is a per-device
    // choice the server never sees — so the sentence on screen is this build's,
    // and asserting the server's string here would pass only in English.
    //
    // Worth knowing while reading this: the catalog line is generic across all
    // three ways a password can be refused. The specific reason pwpolicy
    // computed does not survive the translation, which is a real cost of the
    // key-based scheme and is noted rather than asserted away.
    await expect(page.locator('.fs-error')).toContainText(/known-password list|longer password/i);
  });
});
