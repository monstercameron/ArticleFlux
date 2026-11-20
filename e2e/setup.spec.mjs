import { spawn } from 'node:child_process';
import { mkdirSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { test, expect } from '@playwright/test';
import { APP_PORT } from './ports.mjs';
import { serverBinary } from './platform.mjs';

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
    serverBinary(repo),
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

// The username IS an email address (internal/username), so the form has to say so
// before the field is refused rather than after, and there is no second box for an
// address — it would be the same string typed twice.
test('setup says the username is an email address, and asks for no second one', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="setup"]', { timeout: 60_000 });

  const user = page.locator('#setup-username');
  await expect(page.getByLabel(/email address/i)).toBeVisible();
  await expect(user).toHaveAttribute('type', 'email');
  await expect(user).toHaveAttribute('autocomplete', 'username');
  await expect(page.locator('#setup-email')).toHaveCount(0);
  // The hint is the field's description, read when it takes focus.
  await expect(user).toHaveAttribute('aria-describedby', /setup-username-hint/);
});

// The refusal arrives with the press, beside the field it is about, and nothing
// the reader typed elsewhere is thrown away. It used to come back from the server
// as a sentence under the button and empty both password fields.
test('a username that is not an address is refused beside its field, and the passwords stay', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="setup"]', { timeout: 60_000 });

  await page.fill('#setup-username', 'owner');
  await page.fill('#setup-password', PASSWORD);
  await page.fill('#setup-confirm', PASSWORD);
  await page.click('.login-submit');

  const err = page.locator('#setup-error');
  await expect(err).toContainText(/email address/i);
  await expect(page.locator('#setup-username')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#setup-username')).toHaveAttribute('aria-describedby', /setup-error/);
  await expect(page.locator('#setup-password')).not.toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#setup-username')).toBeFocused();
  await expect(page.locator('#setup-password')).toHaveValue(PASSWORD);
  await expect(page.locator('#setup-confirm')).toHaveValue(PASSWORD);

  // Correcting the field retires the message that was about it.
  await page.fill('#setup-username', 'owner@example.com');
  await page.locator('#setup-username').press('x');
  await expect(page.locator('#setup-username')).not.toHaveAttribute('aria-invalid', 'true');
});

test('a password the server refuses is named, cleared, and focused — and the username stays', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="setup"]', { timeout: 60_000 });

  // Long enough to pass the client's checks and short of the server's policy: a
  // straight run of the keyboard is refused by pwpolicy, which only the server runs.
  const weak = 'qwertyuiopasdfgh';
  await page.fill('#setup-username', 'owner@example.com');
  await page.fill('#setup-password', weak);
  await page.fill('#setup-confirm', weak);
  await page.click('.login-submit');

  await expect(page.locator('#setup-error')).toContainText(/password/i, { timeout: 30_000 });
  await expect(page.locator('#setup-password')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#setup-username')).not.toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#setup-password')).toBeFocused();
  await expect(page.locator('#setup-username')).toHaveValue('owner@example.com');
  await expect(page.locator('#setup-password')).toHaveValue('');
  await expect(page.locator('#setup-confirm')).toHaveValue('');
});

test('setup creates the account, shows the codes once, and signs you in', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="setup"]', { timeout: 60_000 });

  await page.fill('#setup-username', 'cam@example.com');
  await page.fill('#setup-password', PASSWORD);
  await page.fill('#setup-confirm', PASSWORD);
  await page.click('.login-submit');

  // The codes step is not skippable, and that is the point: they are stored
  // hashed, so this render is the only moment they are readable anywhere.
  await expect(page.locator('[data-phase="setup-codes"]')).toBeVisible({ timeout: 60_000 });
  const codes = page.locator('.setup-code');
  await expect(codes.first()).toBeVisible();
  expect(await codes.count()).toBeGreaterThan(0);
  // Read off a screen and typed back months later, so a monospace face: in the
  // proportional one a 0 and an O were the same shape.
  expect(await codes.first().evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/mono/i);

  // The outcome of Copy sits beside Copy, and is a polite status.
  const copyRow = page.locator('.setup-copyrow');
  await expect(copyRow.locator('[data-role="setup-copy"]')).toBeVisible();
  // H13: the form that held focus is gone, so focus is put on the first thing to
  // do with the codes instead of falling to <body>.
  await expect(copyRow.locator('[data-role="setup-copy"]')).toBeFocused();
  await expect(copyRow.locator('[role="status"]')).toHaveAttribute('aria-live', 'polite');

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
  await page.fill('#login-username', 'cam@example.com');
  await page.fill('#login-password', PASSWORD);
  await page.click('.login-submit');
  await expect(page.locator('.shell')).toBeVisible({ timeout: 60_000 });
});

// The way back in for somebody who cannot sign in, and the ONE thing about it
// nothing tested: that the login screen offers it at all.
//
// The recovery card itself has eight tests in client/view — Enter behaviour, a
// spent code, the confirmation, a reset link arriving with its token. All of
// them render the card directly. So the card was proven and the door to it was
// not, which is the same shape as the sign-out control that was written, worked,
// and had no affordance for its whole life.
//
// It lives in this file because only a CLAIMED server shows a login screen. The
// `-dev` instance every other spec drives has no door at all — which is also why
// nobody noticed the link was untested, and why Cam could not find it: on a dev
// server there is nothing to find it on.
test('the login screen offers a way in for somebody who cannot sign in', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });

  const link = page.locator('[data-role="login-recover"]');
  await expect(link).toBeVisible();

  await link.click();
  // The recovery screen, by what it asks for rather than by a class: a code and
  // a new password is what makes it the recovery screen.
  await expect(page.locator('[data-role="recover-code"]')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('[data-role="recover-password"]')).toBeVisible();

  // And back, because a one-way door on the screen somebody reached by mistake
  // is worse than no door: they arrived here from a password they mistyped.
  await page.locator('[data-role="recover-back"]').click();
  await expect(page.locator('#login-password')).toBeVisible({ timeout: 30_000 });
});

// The recovery card is the sign-in card answering a different question, so it
// keeps what the reader already typed, asks for the new password twice as setup
// does, and says which box a refusal is about.
test('recovery keeps the typed username, asks twice, and names the refused box', async ({ page }) => {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });
  // The loopback prefill is "cam"; a typed address must survive the switch.
  await page.fill('#login-username', 'someone@example.com');
  await page.locator('[data-role="login-recover"]').click();
  await expect(page.locator('[data-phase="recover"]')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('#login-username')).toHaveValue('someone@example.com');

  await page.fill('#recover-code', 'AAAA-BBBB-CCCC-DDDD');
  await page.fill('#recover-password', 'a-brand-new-password-1');
  await page.fill('#recover-confirm', 'a-different-password-2');
  await page.locator('[data-role="recover-submit"]').click();
  await expect(page.locator('#recover-error')).toContainText(/not the same/i);
  await expect(page.locator('#recover-confirm')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#recover-confirm')).toBeFocused();

  // A code the server refuses: the code box is the one named, emptied and focused,
  // and the new password the reader chose is still there.
  await page.fill('#recover-confirm', 'a-brand-new-password-1');
  await page.locator('[data-role="recover-submit"]').click();
  await expect(page.locator('#recover-error')).toContainText(/recovery code/i, { timeout: 30_000 });
  await expect(page.locator('#recover-code')).toHaveAttribute('aria-invalid', 'true');
  await expect(page.locator('#recover-code')).toHaveValue('');
  await expect(page.locator('#recover-code')).toBeFocused();
  await expect(page.locator('#recover-password')).toHaveValue('a-brand-new-password-1');
});

// signIn takes a fresh context from the address to the reader.
//
// Every test here gets its own browser context, so nothing carries a credential
// in from the test above — which is the property this file already relies on and
// the one the sign-out tests below need most: each starts genuinely signed in.
async function signIn(page) {
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('[data-phase="login"]', { timeout: 60_000 });
  await page.fill('#login-username', 'cam@example.com');
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

// The control itself is now rendered everywhere, including on the `-dev`
// instance every other spec drives, but only an authenticated instance can prove
// the press did anything — a dev press clears a credential that was never issued
// and reloads back into the reader. So these stay in this file, where signing in
// is real: what they pin is the revocation, not the button's existence.
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
