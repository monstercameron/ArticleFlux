import { spawn } from 'node:child_process';
import { execFile } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

import { APP_PORT } from './ports.mjs';

// The repository root, which the server is spawned FROM.
//
// Not decoration: `serve` resolves its web root relative to the working
// directory, so a server started from anywhere else comes up with no client and
// answers 404 for the page while /healthz stays green. That failure looks
// exactly like "the restart did not work" and is not.
const repo = join(dirname(fileURLToPath(import.meta.url)), '..');

const run = promisify(execFile);

// Stopping and starting the app server FROM A TEST, for T21(e) (TODO 8c.17).
//
// # Why this is not just calling into global-setup
//
// globalSetup runs in Playwright's main process and specs run in workers, which
// are separate processes — so the `app` handle the setup holds is not reachable
// from a test. The two halves communicate the only way they can: the setup puts
// what it knows into the environment, workers inherit it, and this module acts
// on that.
//
// # Killing by port, not by image name
//
// `taskkill /IM articleflux.exe` would take out every other run on this machine,
// including the other agent's. The PID comes from netstat for the port this run
// owns, which is the same rule global-setup follows and for the same reason.

/**
 * stopServer kills whatever holds this run's app port.
 *
 * Deliberately a KILL rather than a graceful shutdown: the state being tested is
 * a server that went away, not one that said goodbye. A clean shutdown closes
 * the tunnel politely and the client would learn about it through a channel that
 * a crashed server does not have.
 */
export async function stopServer() {
  const pid = await pidOnPort(APP_PORT);
  if (!pid) return false;
  try {
    await run('taskkill', ['/PID', pid, '/F']);
  } catch {
    // Already gone between the lookup and the kill, which is a success.
  }
  await waitForPort(APP_PORT, false);
  return true;
}

/**
 * startServer brings it back on the same database and port.
 *
 * The same database matters: the point of the restart half is that the reader's
 * list comes BACK, not that a fresh empty one appears.
 */
export async function startServer() {
  const bin = process.env.AF_E2E_BIN;
  const db = process.env.AF_E2E_DB;
  if (!bin || !db) {
    throw new Error(
      'AF_E2E_BIN / AF_E2E_DB are not set — global-setup did not export them, ' +
      'so a test cannot restart the server on the same database.');
  }
  const app = spawn(bin,
    ['serve', '-db', db, '-addr', `127.0.0.1:${APP_PORT}`, '-poll', '0', '-dev'],
    {
      cwd: repo,
      stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, OPENAI_API_KEY: '', OPENAI_SDK_KEY: '' },
    });

  // Kept, and only used if it does not come up. A restart that fails silently
  // reports as "the port never became healthy", which says nothing about why —
  // and the why is usually one line the server printed before exiting.
  let said = '';
  const keep = (d) => { said += d; if (said.length > 4000) said = said.slice(-4000); };
  app.stdout.on('data', keep);
  app.stderr.on('data', keep);

  try {
    await waitForPort(APP_PORT, true);
  } catch (err) {
    throw new Error(`${err.message}
the server said:
${said.trim() || '(nothing)'}`);
  }
  return app;
}

async function pidOnPort(port) {
  try {
    const { stdout } = await run('netstat', ['-ano', '-p', 'TCP']);
    for (const line of stdout.split(/\r?\n/)) {
      // LISTENING only: an established connection to the port is a CLIENT, and
      // killing it would kill the browser rather than the server.
      if (!line.includes(`:${port} `) || !line.includes('LISTENING')) continue;
      const pid = line.trim().split(/\s+/).pop();
      if (pid && pid !== '0') return pid;
    }
  } catch { /* netstat unavailable; treat as nothing listening */ }
  return null;
}

/** waitForPort waits for the port to start or stop answering. */
async function waitForPort(port, want, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    let healthy = false;
    try {
      const res = await fetch(`http://127.0.0.1:${port}/healthz`, {
        signal: AbortSignal.timeout(1000),
      });
      healthy = res.ok;
    } catch { healthy = false; }
    if (healthy === want) return;
    if (Date.now() > deadline) {
      throw new Error(`port ${port} did not become ${want ? 'healthy' : 'dead'} in ${timeoutMs}ms`);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
}
