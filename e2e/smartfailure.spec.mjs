import { test, expect, boot, openRow, openSettings, storeFakeKey, currentArticle } from './fixtures.mjs';

/**
 * When Smart+ fails, the app says Smart+ failed — and nothing else (second UX
 * audit: G2, G8, G16, G17).
 *
 * The wasm client's connection tracker reads every `Unavailable` as "the
 * transport dropped". The server used to answer a provider failure with that
 * code, so a bad key repainted the list header ("Retry") and turned the search
 * dot red while the connection was fine. A Go test pins the code; what only a
 * browser can show is the CONSEQUENCE: the indicator stays live and the failure
 * is said once, on the article, in one sentence.
 *
 * The key stored here is a placeholder. Whether the provider is reachable from
 * the machine running this does not matter: either way the call fails, which is
 * the situation under test.
 */

/** The Smart+ tab, with a placeholder key stored and article translation on. */
async function translationOn(page) {
  await storeFakeKey(page);
  const toggle = page.locator('[data-action="smart-article-translate"]');
  await expect(toggle).toBeVisible({ timeout: 15_000 });
  if ((await toggle.getAttribute('aria-pressed')) !== 'true') await toggle.click();
  await expect(toggle).toHaveAttribute('aria-pressed', 'true', { timeout: 15_000 });
}

test.describe('a Smart+ failure', () => {
  test.afterEach(async ({ page }) => {
    await page.keyboard.press('Escape').catch(() => {});
  });

  test('does not make the app say the connection dropped', async ({ page }) => {
    await boot(page);
    await translationOn(page);

    // The interface in French: the article is English, so Translate has work to
    // do and has to ask the provider, which the placeholder key will not satisfy.
    await page.locator('[data-action="settings-tab"][data-value="appearance"]').click();
    await page.locator('[data-action="smart-language"][data-value="fr"]').click();

    // G18: the background fill fails too, and the picker used to stop at
    // "0 of 1,519 translated." without a word. It now says it stopped.
    await expect(page.locator('.lang-next')).toContainText(/stopped/i, { timeout: 90_000 });

    // Back to the reader, and an article.
    await page.keyboard.press('Escape');
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
    await openRow(page, 0);
    const article = currentArticle(page);
    const chip = article.locator('[data-action="translate"]');
    await expect(chip).toBeVisible({ timeout: 30_000 });
    await chip.click();

    // The failure is said once, on the article, as the server's own sentence —
    // not wrapped in a second one that repeats it (G17) …
    const note = article.locator('.article-trnote');
    await expect(note).toHaveAttribute('data-bad', 'true', { timeout: 90_000 });
    const text = ((await note.textContent()) ?? '').trim();
    expect(text).not.toBe('');
    // "Couldn't translate this article: Couldn't translate this article just now."
    // said "translate" twice; a sentence that says it once is the point.
    expect(text.match(/translat/gi)?.length ?? 0).toBeLessThanOrEqual(2);
    expect(text).not.toMatch(/:\s/);

    // … and the connection indicator never left "live" (G2).
    await expect(page.locator('.conn').first()).toHaveAttribute('data-state', 'live');
    await expect(page.locator('.pane-list')).not.toContainText(/Retry/);
  });
});

test.describe('the Smart+ model field', () => {
  test('a blank save puts the model back to the default, and says so', async ({ page }) => {
    await boot(page);
    await openSettings(page);
    await page.getByRole('button', { name: /^Smart\+$/ }).click();
    const field = page.locator('[data-role="smart-model"]');
    await expect(field).toBeVisible({ timeout: 15_000 });

    await field.fill('gpt-e2e-model');
    await page.locator('[data-action="smart-model-save"]').click();
    const said = page.locator('.set-panel [role="status"]', { hasText: /Model saved/ });
    await expect(said).toContainText('gpt-e2e-model', { timeout: 15_000 });

    // Blank is the default — the field says so, and used to ignore it (G8).
    await field.fill('');
    await page.locator('[data-action="smart-model-save"]').click();
    await expect(page.locator('.set-panel [role="status"]', { hasText: /reset to the default/ }))
      .toBeVisible({ timeout: 15_000 });

    // It stuck: a reload shows the default, not the model that was cleared.
    await page.reload();
    await openSettings(page);
    await page.getByRole('button', { name: /^Smart\+$/ }).click();
    await expect(page.locator('[data-role="smart-model"]')).toHaveValue('', { timeout: 15_000 });
    await expect(page.locator('.set-panel')).toContainText(/Default: \S+/);
  });
});
