import { spawn } from 'node:child_process';
import { mkdirSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '@playwright/test';
import { APP_PORT } from './ports.mjs';
import { serverBinary } from './platform.mjs';

/**
 * The keyboard path through the credential screens.
 *
 * Type a username, Tab, type a password, Enter. That is how anyone who signs in
 * regularly does it, and it is the one interaction on this screen nobody tries
 * carefully — it is muscle memory, and when it does nothing the reader's
 * conclusion is not "Enter is unsupported", it is "my password is wrong".
 *
 * # Why its own server
 *
 * The shared instance runs with `-dev`, which has no credential and no login
 * screen at all — Root goes straight to the reader. So, like setup.spec.mjs,
 * this file starts a server on an empty database, walks first-run setup to
 * create an account, and then signs in against it. APP_PORT+200 keeps it clear
 * of both the packed range (APP_PORT, APP_PORT+1) and setup.spec's +100.
 *
 * # Why typing rather than fill()
 *
 * `page.fill()` sets the value and dispatches one input event. Typing dispatches
 * a keydown per character, which is what the Enter handler shares a listener
 * with, and it is the only way to catch a handler that was registered against a
 * stale render — the state it submits would be the state as of the last
 * re-registration rather than what is on screen.
 */

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');
const PORT = APP_PORT + 200;
const BASE = `http://127.0.0.1:${PORT}`;
const DB = join(here, '.tmp', `loginkb-${process.pid}.db`);

const USER = 'keyboardcam';
const PASSWORD = 'correct-horse-battery-staple-77';

let server;

test.beforeAll(async () => {
  mkdirSync(join(here, '.tmp'), { recursive: true });
  for (const f of [DB, `${DB}-wal`, `${DB}-shm`]) rmSync(f, { force: true });

  server = spawn(
    serverBinary(repo),
    ['serve', '-addr', `127.0.0.1:${PORT}`, '-db', DB, '-web', join(repo, 'bin', 'web')],
    { cwd: repo, stdio: ['ignore', 'pipe', 'pipe'] },
  );
  let log = '';
  server.stdout.on('data', (d) => { log += d; });
  server.stderr.on('data', (d) => { log += d; });
  server.on('exit', (code) => { if (code) console.log(`[login-kb server exited ${code}]\n${log}`); });

  const deadline = Date.now() + 90_000;
  for (;;) {
    try {
      const res = await fetch(`${BASE}/healthz`);
      if (res.ok) break;
    } catch { /* not up yet */ }
    if (Date.now() > deadline) throw new Error(`login-kb server never came up on ${PORT}\n${log}`);
    await new Promise((r) => setTimeout(r, 250));
  }
});

test.afterAll(async () => {
  if (server) {
    const exited = new Promise((r) => server.once('exit', r));
    server.kill();
    await Promise.race([exited, new Promise((r) => setTimeout(r, 5000))]);
  }
  for (const f of [DB, `${DB}-wal`, `${DB}-shm`]) {
    for (let i = 0; i < 10; i++) {
      try { rmSync(f, { force: true }); break; } catch { await new Promise((r) => setTimeout(r, 200)); }
    }
  }
});

/**
 * type clears a field and enters it a character at a time, from the keyboard.
 *
 * The clear matters: on a loopback origin the login screen prefills the
 * documented dev credentials, so a test that only typed would be appending to
 * "cam".
 */
async function type(page, selector, text) {
  await page.locator(selector).click();
  await page.locator(selector).fill('');
  await page.locator(selector).pressSequentially(text, { delay: 15 });
}

// Ordered: this file's later tests need an account, and creating one is what
// the first test does. Setup is also a credential screen with the same Enter
// contract, so it is worth walking by keyboard rather than by fill+click.
test('setup submits on Enter from the last field', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="setup"]', { timeout: 60_000 });

  await type(page, '#setup-username', USER);
  await page.keyboard.press('Tab');
  await page.keyboard.type('cam@example.com', { delay: 15 });
  await page.keyboard.press('Tab');
  await page.keyboard.type(PASSWORD, { delay: 15 });
  await page.keyboard.press('Tab');
  await page.keyboard.type(PASSWORD, { delay: 15 });
  await page.keyboard.press('Enter');

  await expect(page.locator('[data-phase="setup-codes"]')).toBeVisible({ timeout: 60_000 });
  await page.click('[data-role="setup-done"]');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

test('username, Tab, password, Enter signs in', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

  await type(page, '#login-username', USER);
  // Tab, not a click: the point is that focus moved the way a keyboard moves
  // it, and that the handler reads the field that now has focus.
  await page.keyboard.press('Tab');
  await expect(page.locator('#login-password')).toBeFocused();
  await page.keyboard.type(PASSWORD, { delay: 15 });
  await page.keyboard.press('Enter');

  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

test('Enter from the username field submits too', async ({ page }) => {
  // Filling both and pressing Enter without leaving the first field is the
  // password-manager path: it writes both values and focus never moves.
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

  await type(page, '#login-password', PASSWORD);
  await type(page, '#login-username', USER);
  await page.keyboard.press('Enter');

  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

test('Enter with an empty password reports it and keeps what was typed', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

  await type(page, '#login-password', '');
  await type(page, '#login-username', USER);
  await page.keyboard.press('Enter');

  // An error, not a navigation. A stray Enter reaching the <form> would submit
  // it and reload the page, which looks like the app throwing away what you
  // typed for no stated reason.
  await expect(page.locator('.login-error:not(.is-empty)')).toBeVisible();
  await expect(page.locator('#login-username')).toHaveValue(USER);
  await expect(page.locator('[data-phase="login"]')).toBeVisible();
});

/**
 * The case that actually broke, and it was never about Enter.
 *
 * A password manager — and Chrome's own autofill — writes the value straight
 * into the element, and several of those paths dispatch no `input` event the
 * component can hear. State stayed empty while the screen visibly showed a
 * filled username and password, so submitting sent two empty strings and the
 * reader was told the credentials they could SEE were wrong.
 *
 * Assigning `.value` from script is exactly that write: no input event, no
 * keystroke, just a filled field. Before the fix this produced "invalid
 * username or password" on Enter AND on the button, which is why the fix is in
 * submit() rather than in the key handler.
 */
test('a manager-filled form submits what is on screen, not stale state', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

  await page.evaluate(([u, pw]) => {
    document.querySelector('#login-username').value = u;
    document.querySelector('#login-password').value = pw;
  }, [USER, PASSWORD]);

  await page.locator('#login-password').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

test('a manager-filled form submits from the button too', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

  await page.evaluate(([u, pw]) => {
    document.querySelector('#login-username').value = u;
    document.querySelector('#login-password').value = pw;
  }, [USER, PASSWORD]);

  await page.click('.login-submit');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

test('Enter on a wrong password reports it, clears the password, keeps the username',
  async ({ page }) => {
    await page.goto(BASE, { waitUntil: 'domcontentloaded' });
    await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

    await type(page, '#login-username', USER);
    await page.keyboard.press('Tab');
    await page.keyboard.type('not-the-password-at-all', { delay: 5 });
    await page.keyboard.press('Enter');

    await expect(page.locator('.login-error:not(.is-empty)')).toBeVisible({ timeout: 60_000 });
    // Retyping a username you got right is the small daily insult that makes a
    // login screen feel hostile, so only the password is cleared.
    await expect(page.locator('#login-username')).toHaveValue(USER);
    await expect(page.locator('#login-password')).toHaveValue('');

    // And the screen is still usable from the keyboard afterwards: the retry is
    // the moment a handler registered against a stale render shows itself,
    // because the failure path re-rendered everything it closes over.
    await page.keyboard.type(PASSWORD, { delay: 15 });
    await page.keyboard.press('Enter');
    await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
  });
