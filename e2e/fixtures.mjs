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
  await signIn(page);
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('.conn').first())
    .toHaveAttribute('data-state', 'live', { timeout: 30_000 });
  await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 30_000 });
}

/**
 * signIn gets past the login screen when one is showing.
 *
 * The client asks for credentials whenever it holds no token, even against a
 * `-dev` server that would have served the local account anyway — so a fresh
 * browser context lands on the sign-in form and every later assertion waits for
 * a `.shell` that is not there. The account is the one `seed` created.
 *
 * Conditional rather than unconditional: a reload inside a test still has its
 * token, and clicking a form that is not on screen would fail every one of them.
 */
export async function signIn(page) {
  const password = page.locator('input[type="password"]');
  if (!(await password.count())) return;
  await page.locator('input').first().fill('cam');
  await password.fill('articleflux');
  await page.getByRole('button', { name: /sign in/i }).click();
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

/**
 * openAddFeed opens the add-a-feed dialog from the foot of the rail.
 *
 * The rail's foot is a button now, not a URL box: naming a feed and filing it
 * are decisions made while adding one, and there was nowhere for them to happen
 * in a single pinned field. Every test that adds a feed goes through here, so
 * the navigation to the rail on a phone stays in one place.
 */
export async function openAddFeed(page) {
  await openRail(page);
  await page.locator('[data-action="add-feed-open"]').click();
  // By role and name, not by .af: every dialog in this app is built from the
  // same box, and the category editor is now always in the DOM behind a closed
  // scrim — so a class selector matches two things and Playwright refuses,
  // correctly, to guess which one the test meant.
  const dialog = page.getByRole('dialog', { name: 'Add a feed' });
  await expect(dialog).toBeVisible({ timeout: 20_000 });
  return dialog;
}
