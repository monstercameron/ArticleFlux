import { test as base, expect } from '@playwright/test';

/**
 * Every test starts from the same state.
 *
 * The suite shares one server and one database — spinning both up per test would
 * cost more than the whole run. So instead each test resets the *user's* state
 * (read flags, stars) through a DevMode-only endpoint before it begins.
 *
 * Without this the suite passes only in one order: a test that opens an article
 * marks it read, and every later test that counts unread rows sees a different
 * world than the one it was written against. That is the failure mode that gets
 * mislabelled "flaky" and then papered over with retries.
 */
export const test = base.extend({
  page: async ({ page, baseURL }, use) => {
    const res = await page.request.post(`${baseURL}/debug/reset-state`);
    if (!res.ok()) {
      throw new Error(`reset-state failed (${res.status()}); is the server in DevMode?`);
    }
    await use(page);
  },
});

export { expect };

/** boot waits for the client to be running and connected. */
export async function boot(page) {
  await page.goto('/');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('.conn').first())
    .toHaveAttribute('data-state', 'live', { timeout: 30_000 });
  await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
}

/**
 * openFeed selects a feed by name, navigating there first on a narrow viewport.
 *
 * Below 1080px the sidebar is not on screen — that is the design, not a bug — so
 * a test that clicks a feed row directly waits forever for an element that is
 * deliberately hidden. Encoding the navigation here keeps every test honest about
 * the phone without each one re-implementing it.
 */
export async function openFeed(page, name) {
  const rail = page.locator('.pane-rail');
  if (!(await rail.isVisible())) {
    await page.getByRole('button', { name: '‹ Feeds' }).click();
    await expect(rail).toBeVisible();
  }
  await rail.getByRole('button', { name }).click();
  await expect(page.locator('.pane-list')).toBeVisible();
}

/** feedRow returns a sidebar row, making it visible first if needed. */
export async function feedRow(page, name) {
  const rail = page.locator('.pane-rail');
  if (!(await rail.isVisible())) {
    await page.getByRole('button', { name: '‹ Feeds' }).click();
    await expect(rail).toBeVisible();
  }
  return rail.getByRole('button', { name });
}

/**
 * openRail brings the sidebar on screen, which below 1080px it is not.
 *
 * Adding a feed lives at the foot of the rail now — the chosen design has no top
 * bar — so any test that adds a feed has to navigate there first on a phone.
 */
export async function openRail(page) {
  const rail = page.locator('.pane-rail');
  if (!(await rail.isVisible())) {
    await page.getByRole('button', { name: '‹ Feeds' }).click();
    await expect(rail).toBeVisible();
  }
  return rail;
}
