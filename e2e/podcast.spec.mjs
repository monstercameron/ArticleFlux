import { test, expect, boot } from './fixtures.mjs';

/**
 * The two modes (plan §19, "Two modes, and the correction one of them makes").
 *
 * One entry point plus a persisted toggle became two buttons, and the narrated
 * one shows the words it is saying rather than the article it is not reading.
 *
 * Three of the four facts below are only observable in a browser, which is why
 * they are here rather than in client/view's own tests:
 *
 *   - which mode a show actually opened in, since that is now decided by which
 *     control was pressed rather than by a value read at boot;
 *   - that a mode is NOT inherited from the last one, which is a fact about two
 *     sessions and a preference that no longer exists;
 *   - that a refusing control is still reachable, focusable and leads somewhere,
 *     which is the whole reason it is not `disabled`.
 *
 * What is deliberately NOT asserted: the captions themselves. They need a
 * written script, a script needs a model call, and this instance has no key —
 * see client/view/script_test.go for the splitter and the cursor, and
 * internal/app/speechtext_test.go for the promise that the text served is the
 * text spoken. What IS asserted here is the fallback: with no script, the slide
 * shows the article and says so in `data-captioned`.
 */

const slides = (page) => page.locator('.slides');
const slideshowChip = (page) => page.getByRole('button', { name: /^Slideshow$/ });
const podcastChip = (page) => page.getByRole('button', { name: /^Podcast/ });

/**
 * startSilent opens the slideshow and waits for a story.
 *
 * Retried for the reason slideshow.spec gives at length: every control here is
 * dispatched through one delegated listener registered in an effect, so a click
 * that lands between the shell painting and that effect running does nothing at
 * all.
 */
async function startSilent(page) {
  await expect(async () => {
    await slideshowChip(page).click();
    await expect(slides(page)).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 30_000 });
  await expect(page.locator('.slide-head')).not.toBeEmpty();
}

test.describe('the two modes', () => {
  test('both entry points are offered, and they are different things', async ({ page }) => {
    await boot(page);

    // Two controls in the same row, and they must not say the same word: one
    // shows the feed and the other reads it.
    await expect(slideshowChip(page)).toBeVisible();
    await expect(podcastChip(page)).toBeVisible();

    const slideName = await slideshowChip(page).textContent();
    const podName = await podcastChip(page).textContent();
    expect(slideName.trim()).not.toBe(podName.trim());
  });

  test('the slideshow is silent, and shows the article', async ({ page }) => {
    await boot(page);
    await startSilent(page);

    // The mode is the one that was pressed. `silent` is the clock driving;
    // `read` is the narrator driving.
    await expect(slides(page)).toHaveAttribute('data-mode', 'silent');

    // And it shows the ARTICLE, which is what it is for. data-captioned is the
    // one honest answer to "is this slide showing the words being said" — false
    // here, because nothing is being said.
    await expect(slides(page)).toHaveAttribute('data-phase', 'read', { timeout: 40_000 });
    const body = page.locator('.slide-body');
    await expect(body).toBeVisible();
    await expect(body).toHaveAttribute('data-captioned', 'false');
    await expect(page.locator('.slide-said-note')).toHaveCount(0);
  });

  test('the podcast says what it needs, and goes where it can be fixed', async ({ page }) => {
    await boot(page);

    // This instance has no OpenAI key and the account has not opted in, so the
    // control refuses. Present and refusing rather than absent or disabled:
    // absent hides the thing the reader came to do, and disabled cannot be
    // focused, cannot be read by a screen reader walking the tab order, and
    // cannot say why.
    const chip = podcastChip(page);
    await expect(chip).toBeVisible();
    await expect(chip).not.toHaveAttribute('disabled', '');
    await expect(chip).toContainText(/needs setting up/i);

    // Pressing it lands on the screen where those conditions live, rather than
    // doing nothing — which is what the Podcast tab's own start button used to
    // do, while looking live the whole time.
    await expect(async () => {
      await chip.click();
      await expect(page.locator('.set-panel')).toBeVisible({ timeout: 2000 });
    }).toPass({ timeout: 30_000 });
    await expect(page.locator('.set-panel .pc-need')).toHaveCount(4);

    // No show was started. A control that refuses must not half-open the thing
    // it refused.
    await expect(slides(page)).toHaveCount(0);
  });

  test('the mode is not remembered between shows', async ({ page }) => {
    await boot(page);
    await startSilent(page);

    // `v` turns the narrator on inside a running show, which is the one place a
    // live toggle earns its keep. This instance cannot speak, so the show says
    // so and carries on — that is slideshow.spec's territory; what matters here
    // is only that the mode really did change.
    await page.keyboard.press('v');
    await expect(slides(page)).toHaveAttribute('data-mode', 'read');

    await page.keyboard.press('Escape');
    await expect(slides(page)).toHaveCount(0);

    // Press Slideshow again. It must be SILENT: the mode comes from the button,
    // and `slides.readToMe` is no longer a preference anybody reads. Before this
    // change the second show inherited `read` from the first and the reader got
    // a narrator they had not asked for — from a toggle they may have flipped
    // days earlier.
    await startSilent(page);
    await expect(slides(page)).toHaveAttribute('data-mode', 'silent');
    await expect(page.locator('.slide-voice')).toHaveCount(0);
  });
});
