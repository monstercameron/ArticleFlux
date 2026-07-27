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
  // Nothing to do if one is already answering, and this is not an optimisation.
  //
  // The `afterEach` that guarantees a server for the next spec runs after the
  // test that already restarted one, so this was spawning a DUPLICATE every
  // time. The duplicate loses the bind race and exits — but not before it has
  // opened the same SQLite file, run migrations and derived the interest layer.
  // Two writers on one WAL database, several times per run, for no reason: the
  // suite went flaky here and the second process is the only thing in the room
  // that could touch state the first one owns.
  if (await healthy(APP_PORT)) return null;

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
      // DETACHED, and this is the whole reason the suite could not be trusted
      // past this file.
      //
      // A server started from a test is a child of the WORKER process, and
      // Playwright recycles workers between spec files. The replacement server
      // therefore died the moment this file finished — leaving every later spec
      // to fail at `reset-state: ECONNREFUSED`, which reads as "the harness is
      // broken" and points at nothing. The original server survives the whole
      // run because global-setup owns it from the main process; a restart has to
      // survive on the same terms.
      //
      // Teardown kills by PORT rather than by handle, so the replacement is
      // cleaned up even though nobody holds a reference to it.
      detached: true,
    });
  // The parent must not wait on it at exit. Without this the worker process
  // hangs at the end of the file, holding the run open on a child that is
  // deliberately outliving it.
  app.unref();

  // Echoed AND kept. The buffer is for a server that never comes up, where the
  // why is usually one line it printed before exiting. The echo is for the
  // opposite case, which cost an hour: a server that came up fine and died
  // later. Its output goes to a worker process whose stdout nobody was reading,
  // so a panic after the health check was invisible, and the failure surfaced
  // three tests later as "reset-state: connection refused" — which reads as a
  // harness bug and is not one. global-setup prefixes its server's output for
  // exactly this reason; a restart is the same server and deserves the same.
  let said = '';
  const keep = (d) => {
    said += d;
    if (said.length > 4000) said = said.slice(-4000);
    process.stdout.write(`[restarted] ${d}`);
  };
  app.stdout.on('data', keep);
  app.stderr.on('data', keep);

  // And its death is announced. A non-zero exit after a clean start is the
  // event every later failure descends from, so it is worth one line at the
  // moment it happens rather than a mystery at the next reset-state.
  app.on('exit', (code, signal) => {
    if (code !== 0 && code !== null) {
      process.stdout.write(`[restarted] server exited with code ${code}\n`);
    } else if (signal) {
      process.stdout.write(`[restarted] server was killed by ${signal}\n`);
    }
  });

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

/** healthy reports whether something is answering /healthz on the port. */
async function healthy(port) {
  try {
    const res = await fetch(`http://127.0.0.1:${port}/healthz`, {
      signal: AbortSignal.timeout(1000),
    });
    return res.ok;
  } catch {
    return false;
  }
}

/** waitForPort waits for the port to start or stop answering. */
async function waitForPort(port, want, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await healthy(port) === want) return;
    if (Date.now() > deadline) {
      throw new Error(`port ${port} did not become ${want ? 'healthy' : 'dead'} in ${timeoutMs}ms`);
    }
    await new Promise((r) => setTimeout(r, 200));
  }
}
