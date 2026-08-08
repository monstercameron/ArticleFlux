import { spawn } from 'node:child_process';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

// The three things this suite does that are not the same on Linux and Windows.
//
// # Why this file exists
//
// The suite was written on Windows and hardcoded it: `bin/articleflux.exe`, and
// `netstat -ano` + `taskkill` to find and kill a listener. Four files carried a
// copy of one or the other. That is the entire reason ~30 specs — the only tests
// in the repository that exercise wasm ↔ gRPC-over-WebSocket ↔ SQLite ↔ FTS5 —
// never ran in CI: every runner is Linux, so the release gate was thorough about
// compilation and blind to whether the application works in a browser.
//
// One module rather than four `process.platform` checks, because the intent is
// identical in all four and only the tool differs. A fifth copy is how one of
// them ends up killing by image name.

// fileURLToPath, NOT `new URL('.', import.meta.url).pathname`.
//
// On Windows that property yields `/C:/Users/…/e2e/` — with a leading slash —
// which is not a directory any process can be spawned in. `spawn` then fails
// with ENOENT, `run` below reports it as "no such tool", and every lookup
// returns an empty set. The visible symptom is `stopServer()` answering
// "nothing was listening to kill" about a server that is demonstrably
// listening, which reads as a bug in the server rather than in a path join.
// Caught by connection.spec's restart test on the first run of this file.
const here = dirname(fileURLToPath(import.meta.url));

/** serverBinary is the built server for this platform. */
export function serverBinary(repo) {
  return join(repo, 'bin', process.platform === 'win32' ? 'articleflux.exe' : 'articleflux');
}

/**
 * listenerPids returns the pids LISTENING on a TCP port.
 *
 * Listening only, never established: a connection TO the port is a client, and
 * killing that would kill the browser rather than the server.
 *
 * Best-effort by design. An empty set means "nothing found or no tool to ask
 * with", and the caller treats those the same way — a stale listener is a
 * nuisance, and a missing `ss` is not a reason to refuse to run the suite. The
 * bind that follows reports the real problem if there is one.
 */
export async function listenerPids(port) {
  return process.platform === 'win32' ? windowsPids(port) : unixPids(port);
}

/**
 * killPid terminates one process, hard.
 *
 * Deliberately never by IMAGE NAME. `taskkill /IM articleflux.exe` or
 * `pkill articleflux` would also take out the dev server on :9000 that somebody
 * is watching, and the other agent's suite on this machine — which is the whole
 * reason the callers go to the trouble of finding a pid first.
 *
 * A kill rather than a graceful stop, because the state several specs are
 * testing is a server that WENT AWAY: a clean shutdown closes the tunnel
 * politely, and the client learns about that through a channel a crashed server
 * does not have.
 */
export async function killPid(pid) {
  if (process.platform === 'win32') {
    await run('taskkill', ['/PID', String(pid), '/F']);
    return;
  }
  // `kill` rather than process.kill, so a pid that has already exited between
  // the lookup and here is a non-zero exit rather than a thrown ESRCH.
  await run('kill', ['-9', String(pid)]);
}

async function windowsPids(port) {
  const pids = new Set();
  const { code, out } = await run('netstat', ['-ano', '-p', 'TCP']);
  if (code !== 0) return pids;
  for (const line of out.split(/\r?\n/)) {
    if (!line.includes('LISTENING')) continue;
    const cols = line.trim().split(/\s+/);
    if (!(cols[1] || '').endsWith(`:${port}`)) continue;
    const pid = cols[cols.length - 1];
    if (pid && pid !== '0') pids.add(pid);
  }
  return pids;
}

// unixPids asks `ss`, then `lsof`.
//
// `ss` is present on every modern Linux including the GitHub runners. `lsof` is
// the fallback for macOS, where `ss` does not exist, and for a container with
// neither `ss` nor a readable /proc/net.
async function unixPids(port) {
  const pids = new Set();

  const ss = await run('ss', ['-lptnH', `sport = :${port}`]);
  if (ss.code === 0) {
    // users:(("articleflux",pid=1234,fd=7))
    for (const m of ss.out.matchAll(/pid=(\d+)/g)) pids.add(m[1]);
    if (pids.size) return pids;
  }

  const lsof = await run('lsof', ['-nP', `-iTCP:${port}`, '-sTCP:LISTEN', '-t']);
  if (lsof.code === 0) {
    for (const line of lsof.out.split(/\r?\n/)) {
      const pid = line.trim();
      if (pid) pids.add(pid);
    }
  }
  return pids;
}

// run executes a command to completion without blocking the event loop.
//
// Not blocking matters here specifically: the fixture feed server lives inside
// the Playwright process, so a `spawnSync` would stop it answering and every
// fetch would time out against a server that is definitely listening.
//
// A missing executable resolves rather than rejecting, with a non-zero code:
// asking for `ss` on a box that has none is an answer, not a fault.
function run(cmd, args) {
  return new Promise((resolve) => {
    const p = spawn(cmd, args, { cwd: here, stdio: ['ignore', 'pipe', 'pipe'] });
    let out = '';
    p.stdout.on('data', (d) => { out += d; });
    p.stderr.on('data', (d) => { out += d; });
    p.on('error', () => resolve({ code: 127, out }));
    p.on('close', (code) => resolve({ code, out }));
  });
}
