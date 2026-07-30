import { spawn } from 'node:child_process';
import { mkdirSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '@playwright/test';
import { APP_PORT } from './ports.mjs';

// First-run setup (§7.11), against a server with an EMPTY database.
//
// Every other spec in this directory runs against the shared instance
// global-setup.mjs seeds, which has an account — and an instance with an
// account is precisely the one state where this screen must not appear. So this
// file starts its own server on its own port with its own database, and stops
// it afterwards.
//
// The port is APP_PORT + 100, and the offset is not arbitrary. ports.mjs packs
// slots two-wide from 9400 — app at BASE+slot*2, the fixture feed at BASE+1 —
// so APP_PORT+1 is the feed server this suite already started, and taking it
// fails the whole run with "the fixture feed port is already in use". Clearing
// the packed range by 100 keeps this server unique per slot and out of it.
const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');
const PORT = APP_PORT + 100;
const BASE = `http://127.0.0.1:${PORT}`;
const DB = join(here, '.tmp', `setup-${process.pid}.db`);

const PASSWORD = 'correct-horse-battery-staple-77';

let server;

test.beforeAll(async () => {
  mkdirSync(join(here, '.tmp'), { recursive: true });
  rmSync(DB, { force: true });
  rmSync(`${DB}-wal`, { force: true });
  rmSync(`${DB}-shm`, { force: true });

  server = spawn(
    join(repo, 'bin', 'articleflux.exe'),
    ['serve', '-addr', `127.0.0.1:${PORT}`, '-db', DB, '-web', join(repo, 'bin', 'web')],
    { cwd: repo, stdio: ['ignore', 'pipe', 'pipe'] },
  );
  // Captured rather than ignored: when this server fails to start, the reason is
  // on its stderr and the alternative is a bare "never came up" with the cause
  // thrown away.
  let log = '';
  server.stdout.on('data', (d) => { log += d; });
  server.stderr.on('data', (d) => { log += d; });
  server.on('exit', (code) => { if (code) console.log(`[setup server exited ${code}]
${log}`); });

  // Wait for the port rather than sleeping: the first boot of an empty database
  // runs 29 migrations and tunes Argon2id, which is fast on a desktop and not
  // on a loaded CI runner.
  const deadline = Date.now() + 90_000;
  for (;;) {
    try {
      const res = await fetch(`${BASE}/healthz`);
      if (res.ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > deadline) {
      throw new Error(`setup server never came up on ${PORT}
${log}`);
    }
    await new Promise((r) => setTimeout(r, 250));
  }
});

test.afterAll(async () => {
  // Wait for the process to actually exit before touching its files. On Windows
  // a killed process keeps its handles for a moment longer, and rmSync against
  // a database SQLite still has open throws EPERM — which Playwright attributes
  // to the last test that ran, so a perfectly good test reports a permission
  // error about a file it never touched. That is what this cost the first time.
  if (server) {
    const exited = new Promise((r) => server.once('exit', r));
    server.kill();
    await Promise.race([exited, new Promise((r) => setTimeout(r, 5000))]);
  }
  for (const f of [DB, `${DB}-wal`, `${DB}-shm`]) {
    for (let i = 0; i < 10; i++) {
      try {
        rmSync(f, { force: true });
        break;
      } catch {
        // A temp file left behind is untidy; a failed teardown that fails a
        // passing test is worse. .tmp is disposable either way.
        await new Promise((r) => setTimeout(r, 200));
      }
    }
  }
});

test('an unclaimed server offers setup, not a password prompt', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-phase="setup"]')).toBeVisible({ timeout: 60_000 });
  // The distinction that matters: a login screen here would be a door with no
  // room behind it, since no password exists that could open it.
  await expect(page.locator('[data-phase="login"]')).toHaveCount(0);
});

test('setup creates the account, shows the codes once, and signs you in', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="setup"]', { timeout: 60_000 });

  await page.fill('#setup-username', 'cam');
  await page.fill('#setup-email', 'cam@example.com');
  await page.fill('#setup-password', PASSWORD);
  await page.fill('#setup-confirm', PASSWORD);
  await page.click('.login-submit');

  // The codes step is not skippable, and that is the point: they are stored
  // hashed, so this render is the only moment they are readable anywhere.
  await expect(page.locator('[data-phase="setup-codes"]')).toBeVisible({ timeout: 60_000 });
  const codes = page.locator('.setup-code');
  await expect(codes.first()).toBeVisible();
  expect(await codes.count()).toBeGreaterThan(0);

  await page.click('[data-role="setup-done"]');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

test('a claimed server sends the next visitor to the login screen', async ({ page }) => {
  // Ordering dependency on the test above, deliberately: "claimed" is a
  // property this file creates and cannot assert without creating.
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-phase="login"]')).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('[data-phase="setup"]')).toHaveCount(0);
});

test('the account that setup created can sign in', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });
  await page.fill('#login-username', 'cam');
  await page.fill('#login-password', PASSWORD);
  await page.click('.login-submit');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

// signIn takes a fresh context from the address to the reader.
//
// Every test here gets its own browser context, so nothing carries a credential
// in from the test above — which is the property this file already relies on and
// the one the sign-out tests below need most: each starts genuinely signed in.
async function signIn(page) {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });
  await page.fill('#login-username', 'cam');
  await page.fill('#login-password', PASSWORD);
  await page.click('.login-submit');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
}

async function openAccountTab(page) {
  await page.keyboard.press(',');
  await expect(page.locator('.set-tabs')).toBeVisible();
  await page.locator('[data-action="settings-tab"][data-value="account"]').click();
  await expect(page.locator('.set-panel')).toBeVisible();
}

// The sign-out control exists ONLY on an authenticated instance, which is why
// these tests live in this file rather than beside the other settings specs: the
// shared instance every other spec drives is started with `-dev`, holds no
// credential, and correctly renders no sign-out at all.
test('signing out takes two presses, and the first one only asks', async ({ page }) => {
  await signIn(page);
  await openAccountTab(page);

  const arm = page.locator('[data-action="sign-out"]');
  await expect(arm).toBeVisible();
  await arm.click();

  // Armed, not done. The reader is still in the reader, and the button now
  // carries the action that actually ends the session.
  await expect(page.locator('[data-action="sign-out-confirm"]')).toBeVisible();
  await expect(page.locator('.set-panel [role="alert"]')).toBeVisible();
  await expect(page.locator('.shell')).toBeVisible();

  // Leaving the tab disarms it, so a confirmation nobody remembers giving cannot
  // be spent by a later click that happens to land on the same pixels.
  await page.locator('[data-action="settings-tab"][data-value="reading"]').click();
  await page.locator('[data-action="settings-tab"][data-value="account"]').click();
  await expect(page.locator('[data-action="sign-out"]')).toBeVisible();
  await expect(page.locator('[data-action="sign-out-confirm"]')).toHaveCount(0);
});

test('the second press ends the session and the credential does not come back', async ({ page }) => {
  await signIn(page);
  await openAccountTab(page);

  await page.locator('[data-action="sign-out"]').click();
  await page.locator('[data-action="sign-out-confirm"]').click();

  await expect(page.locator('[data-phase="login"]')).toBeVisible({ timeout: 60_000 });

  // The assertion that separates a real logout from a screen that merely looks
  // like one: reload, and the stored credential is still gone. Before this
  // control existed, clearing local storage by hand was the only way to reach
  // this state — a reload that walked back into the reader would mean the token
  // survived and the button had only navigated.
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-phase="login"]')).toBeVisible({ timeout: 60_000 });
});
