import { test as base, expect } from '@playwright/test';

import { APP_PORT } from './ports.mjs';
import { startServer } from './server.mjs';

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
    await resetState(page, baseURL);
    await use(page);
  },
});

/**
 * resetState clears the user's state, and recovers if the server has died.
 *
 * # Why this is more than one POST
 *
 * When the app server goes away mid-run, every remaining test fails at this line
 * with the same connection error — and a run that reported "16 passed, 39
 * failed" was reporting one dead socket thirty-nine times. Every one of those
 * reads as a product bug, the real baseline is invisible, and it took three
 * attempts to learn that the suite had not found anything.
 *
 * So a failure here asks WHY before it reports. If the port is answering, this
 * is a genuine failure and it is thrown as one. If it is not, the server is
 * restarted — the harness already knows how, T21(e) needs it anyway — and the
 * reset is retried once. A run that can heal is worth more than a run that
 * explains itself well, and the message covers the case where it cannot.
 */
async function resetState(page, baseURL) {
  try {
    const res = await page.request.post(`${baseURL}/debug/reset-state`);
    if (res.ok()) return;
    throw new Error(`reset-state answered ${res.status()}; is the server in DevMode?`);
  } catch (first) {
    if (await healthy()) {
      // The server is up and said no. That is a real failure about this test.
      throw first;
    }
    await startServer();
    const res = await page.request.post(`${baseURL}/debug/reset-state`);
    if (res.ok()) return;
    throw new Error(
      'THE APP SERVER WENT AWAY and could not be restarted. Every test after this ' +
      'one will fail for the same reason, and none of those failures is about the ' +
      `product. Original error: ${first.message}`);
  }
}

/** healthy reports whether this run's server is answering. */
async function healthy() {
  try {
    const res = await fetch(`http://127.0.0.1:${APP_PORT}/healthz`, {
      signal: AbortSignal.timeout(2000),
    });
    return res.ok;
  } catch {
    return false;
  }
}

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
  await railRow(rail, name).click();
  await expect(page.locator('.pane-list')).toBeVisible();
}

/** feedRow returns a sidebar row, making it visible first if needed. */
export async function feedRow(page, name) {
  const rail = page.locator('.pane-rail');
  if (!(await rail.isVisible())) {
    await page.getByRole('button', { name: '‹ Feeds' }).click();
    await expect(rail).toBeVisible();
  }
  return railRow(rail, name);
}

/**
 * railRow is the one place that knows a feed row is not the only button with
 * that feed's name in it.
 *
 * Every row grew a settings gear whose accessible name is "Settings for Alpha
 * Journal", so `getByRole('button', {name: /Alpha Journal/})` now resolves to
 * two elements and every test using it fails on strict mode. That is the app
 * being right and the selector being stale — which is worth fixing HERE rather
 * than at each call site, because the next control added to a row would break
 * them all again.
 *
 * Matched on the role name AND the row class rather than on the class alone:
 * the class says which element, the accessible name says which feed, and a test
 * that stops asserting the accessible name stops noticing when a row becomes
 * unreadable to a screen reader.
 */
function railRow(rail, name) {
  return rail.getByRole('button', { name }).and(rail.locator('.feed-row'));
}

/**
 * currentArticle is the one the reader opened, in a pane that shows several.
 *
 * The reading pane became a STREAM: opening an article renders it and its
 * neighbours, so `.article h1` resolves to five headings and every assertion
 * about "the article" fails on strict mode. That is the app being right and the
 * selector being stale.
 *
 * `data-current` is the app's own answer to "which one is open" — it is what
 * drives the styling — so tests read the same signal the UI does rather than
 * inventing a rule like "the first one", which would quietly pass while the
 * wrong article was showing.
 */
export function currentArticle(page) {
  return page.locator('.article[data-current="true"]');
}

/**
 * openStream opens one of the rail's built-in streams — Read later, All, and
 * so on — as distinct from a feed.
 *
 * Separate from openFeed because they are different rows: `railRow` scopes to
 * `.feed-row`, which is right for a subscribed feed and wrong for a stream, and
 * a helper that silently matched both would let a test claim to open a feed
 * while opening a stream that happened to share a word.
 */
export async function openStream(page, name) {
  const rail = await openRail(page);
  await rail.getByRole('button', { name }).first().click();
  await expect(page.locator('.pane-list')).toBeVisible();
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
