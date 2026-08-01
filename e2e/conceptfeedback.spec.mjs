import { test, expect, boot, currentArticle } from './fixtures.mjs';

/**
 * Concept feedback, end to end: liking one article should demote or promote
 * the CONCEPT it is about, not only that one row (internal/derive.conceptFeedback,
 * internal/derive.applyConceptFeedback).
 *
 * Before this feature, a like touched only the item's own score (applyDeliberate)
 * and, for a dislike, its feed's affinity (feedScore) — nothing propagated to a
 * sibling article on the same topic that the reader never rated at all. This test
 * proves the propagation reaches the reader through the real pipeline: a real
 * click on a real article, a real derivation, and a real ranked page — not just
 * the Go-level unit tests already covering the scoring math in internal/derive.
 *
 * /debug/ingest-cluster and /debug/derive-now are DevMode-only (see internal/app,
 * beside /debug/reset-state) and exist for exactly this: a controllable topic
 * cluster and a synchronous derivation, so the test does not have to poll a
 * background job on a timer to observe an effect it caused a moment ago.
 */
test.describe('concept feedback', () => {
  test('liking one article about a topic surfaces the effect on the ranked page', async ({
    page,
    baseURL,
  }) => {
    const ingest = await page.request.post(`${baseURL}/debug/ingest-cluster`);
    expect(ingest.ok(), await ingest.text()).toBeTruthy();

    await boot(page);

    // Open the first cluster item from the unread stream and like it. The
    // stream lists everything regardless of feed, so this does not need to know
    // which feed /debug/ingest-cluster used.
    await page.locator('[data-source-id="__all__"]').click().catch(() => {});
    const firstOfCluster = page
      .locator('.item-row', { hasText: 'E2E cluster: NPU inference latency benchmark one' })
      .first();
    await expect(firstOfCluster).toBeVisible({ timeout: 30_000 });
    await firstOfCluster.click();

    const article = currentArticle(page);
    await expect(article).toBeVisible();
    await article.locator('[data-action="like"]').first().click();
    await expect(article.locator('[data-action="like"]').first()).toHaveAttribute(
      'aria-pressed', 'true');

    const derive = await page.request.post(`${baseURL}/debug/derive-now`);
    expect(derive.ok(), await derive.text()).toBeTruthy();

    // --- the reliable assertion: the factor histogram on My Feed settings ---
    //
    // GetInterestProfile counts a reason once per RANKED ITEM regardless of
    // whether it survived truncation into that row's displayed blurb (see
    // internal/reader/interest.go), so this is the check that cannot be defeated
    // by a row's reason list being longer than the UI shows. It is the
    // authoritative assertion; the blurb check below is corroborating, not
    // load-bearing.
    await page.keyboard.press(',');
    await page.locator('[data-action="settings-tab"][data-value="myfeed"]').click();
    await expect(page.locator('.set-panel')).toBeVisible();
    await page.waitForTimeout(300);

    const conceptFactor = page.locator('.mf-factor', {
      has: page.locator('.mf-factor-name', { hasText: 'Related to something you liked or disliked' }),
    });
    await expect(conceptFactor, 'the concept-feedback factor never reached the histogram — ' +
      'either the derivation did not propagate the like, or the label fell back to the raw ' +
      'term name instead of the i18n string').toBeVisible({ timeout: 5_000 });
    await page.screenshot({
      path: 'test-results/conceptfeedback-factor-histogram.png', fullPage: true,
    });

    // --- corroborating: the reason is legible on an UNLIKED sibling row ---
    await page.keyboard.press('Escape');
    await page.locator('[data-source-id="__myfeed__"]').click();
    await page.waitForTimeout(1500);

    const siblingRows = page.locator('.item-row', { hasText: 'E2E cluster: NPU' });
    const siblingCount = await siblingRows.count();
    let sawTasteReason = false;
    for (let i = 0; i < siblingCount; i++) {
      const row = siblingRows.nth(i);
      const title = (await row.locator('.item-title').textContent()) || '';
      // Skip the row we liked directly — its own reason is expected to mention
      // the deliberate act, and the point here is the SIBLING that was never
      // itself touched.
      if (title.includes('benchmark one')) continue;
      const why = ((await row.locator('.item-why').textContent()) || '').trim();
      if (why.toLowerCase().includes('liked articles about this before')) {
        sawTasteReason = true;
        await row.scrollIntoViewIfNeeded();
        await page.screenshot({ path: 'test-results/conceptfeedback-sibling-row.png' });
        break;
      }
    }
    // Not a hard failure on its own: the histogram above already proved the
    // server-side propagation happened. This only confirms the phrasing is
    // legible in the UI when it does surface in a row's visible blurb.
    if (!sawTasteReason) {
      console.log('note: no visible sibling row showed the concept_feedback blurb text ' +
        '(it may have been truncated behind stronger reasons on every sibling) — the ' +
        'histogram assertion above is what actually proves propagation happened');
    }
  });
});
