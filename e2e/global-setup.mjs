import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { readFileSync, rmSync, existsSync, mkdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

import { APP_PORT, FEED_PORT, releaseSlot } from './ports.mjs';

const here = dirname(fileURLToPath(import.meta.url));
const repo = join(here, '..');

// Ports come from ports.mjs, which derives them from the process id.
//
// They used to be pinned at 9010/9011 with an environment variable as the escape
// hatch, and the comment here described the failure exactly: `killListener`
// below kills whatever holds these ports, the fixture feed server lives inside
// THIS node process, so a second suite on the same ports kills the first
// RUNNER — a run that prints "Running 22 tests" and exits after three with no
// result, no failure and no error.
//
// It happened. The remedy existed and was behind a variable nobody remembered,
// which is the same as not existing; the default is safe now and the variable is
// an override. Two runs at once is not hypothetical here: two agents share this
// machine.
// Unique per run: a lock we could not clear becomes an orphaned file rather
// than a suite that cannot start.
const DB = join(here, '.tmp', `e2e-${process.pid}.db`);

let feedServer;
let app;

/**
 * The e2e environment, built from scratch on every run:
 *
 *   1. a static server for the fixture feeds  (so ingestion is deterministic —
 *      a suite that polls the real Hacker News fails whenever the news changes)
 *   2. a fresh database seeded against those fixtures
 *   3. the real articleflux binary, serving the real wasm client
 *
 * Nothing is mocked. The whole point of e2e here is the seam unit tests cannot
 * reach: wasm ↔ gRPC-over-WebSocket ↔ SQLite ↔ FTS5.
 */
export default async function globalSetup() {
  mkdirSync(join(here, '.tmp'), { recursive: true });

  // A previous run that was killed rather than closed leaves a articleflux.exe
  // holding the database open, and Windows refuses to delete a locked file —
  // so the next run dies at rmSync with EPERM and blames the filesystem.
  //
  // Two defences, because either alone is insufficient:
  //   1. kill whatever is listening on the app port (by PID from netstat, never
  //      a blanket kill by image name — that would take out unrelated servers)
  //   2. name the database uniquely per run, so a lock we failed to clear is an
  //      orphaned file rather than a broken suite
  // The APP port only.
  //
  // The feed port is deliberately NOT killed, and that asymmetry is the whole
  // point. The app server is a child process this run spawned, so a stale one is
  // our own corpse holding our own database open — killing it is cleanup. The
  // fixture feed server runs INSIDE the Playwright process, so anything holding
  // that port is somebody's live test runner, and killing it does not free a
  // port: it destroys a run, silently, with no summary and no error, because the
  // process that dies is the one that would have reported.
  //
  // That is what kept happening. The port lock (ports.mjs) is what prevents the
  // collision; not swinging at the feed port is what makes a collision survivable
  // if the lock is ever bypassed — an uncoordinated run and a locked one now both
  // fail loudly on EADDRINUSE instead of one quietly killing the other.
  await killListener(APP_PORT);
  for (const suffix of ['', '-wal', '-shm']) {
    const p = DB + suffix;
    try { if (existsSync(p)) rmSync(p); } catch { /* superseded by the unique name */ }
  }

  // --- 1. fixture feeds ---------------------------------------------------
  const feeds = {
    '/alpha.xml': ['application/rss+xml', readFileSync(join(here, 'fixtures', 'alpha.xml'))],
    '/beta.xml': ['application/atom+xml', readFileSync(join(here, 'fixtures', 'beta.xml'))],
    // Two HTML pages, for the subscribe ladder (§11). One declares its feed the
    // way most sites do; the other publishes nothing at all, which is the case
    // Smart+ exists for. Both are fixtures rather than real sites for the same
    // reason the feeds are: a suite that depends on somebody's live homepage
    // fails whenever they redesign it.
    '/declares.html': ['text/html; charset=utf-8', Buffer.from(`<!doctype html>
<html><head><title>Alpha, the blog</title>
<link rel="alternate" type="application/rss+xml" href="/alpha.xml"></head>
<body><h1>Alpha</h1><p>The feed is declared in the head.</p></body></html>`)],
    '/nofeed.html': ['text/html; charset=utf-8', Buffer.from(`<!doctype html>
<html><head><title>Quiet Notes</title></head><body>
<main>
  <article class="post"><h2><a href="/posts/one">First note</a></h2>
    <time datetime="2026-07-20T09:00:00Z">20 July</time></article>
  <article class="post"><h2><a href="/posts/two">Second note</a></h2>
    <time datetime="2026-07-22T09:00:00Z">22 July</time></article>
</main></body></html>`)],
  };
  feedServer = createServer((req, res) => {
    const hit = feeds[req.url.split('?')[0]];
    if (!hit) { res.writeHead(404); res.end('no'); return; }
    res.writeHead(200, { 'Content-Type': hit[0] });
    res.end(hit[1]);
  });
  await new Promise((ok, fail) => {
    feedServer.once('error', (err) => {
      if (err.code === 'EADDRINUSE') {
        fail(new Error(
          `the fixture feed port ${FEED_PORT} is already in use.
` +
          'Another suite holds this slot. It is NOT killed on purpose: that ' +
          'server lives inside the other Playwright process, so killing ' +
          'it would destroy that run rather than free a port. Wait for it, or ' +
          'set AF_E2E_APP_PORT / AF_E2E_FEED_PORT.'));
        return;
      }
      fail(err);
    });
    feedServer.listen(FEED_PORT, '127.0.0.1', ok);
  });

  // --- 2. build + seed ----------------------------------------------------
  const bin = join(repo, 'bin', 'articleflux.exe');
  if (!existsSync(bin)) throw new Error(`build the server first: ${bin} is missing`);
  if (!existsSync(join(repo, 'bin', 'web', 'app.wasm'))) {
    throw new Error('build the client first: bin/web/app.wasm is missing (./scripts/make.ps1 wasm)');
  }

  const urls = [
    `http://127.0.0.1:${FEED_PORT}/alpha.xml`,
    `http://127.0.0.1:${FEED_PORT}/beta.xml`,
  ].join(',');

  // -allow-private is required: the fixture server binds loopback, which the
  // SSRF guard refuses by design. That refusal is correct and is itself covered
  // by a test; here it has to be opted out of explicitly.
  //
  // spawn + await, NOT spawnSync. The fixture feed server lives in *this* Node
  // process, and spawnSync blocks the event loop — so the seeder's HTTP requests
  // would never be answered and every fetch would time out. The symptom
  // ("context deadline exceeded" against a server that is definitely listening)
  // points at the network, and the cause is two lines up.
  const seeded = await run(bin, ['seed', '-db', DB, '-feeds', urls, '-allow-private'], repo);
  if (seeded.code !== 0) {
    throw new Error(`seed failed:\n${seeded.out}`);
  }
  // Assert here rather than thirty assertions later. A seed that subscribes but
  // ingests nothing renders a perfectly correct EMPTY reader, and every test then
  // fails with "no .item-row" — which points at the UI instead of at the seed.
  const ingested = /items=(\d+)/.exec(seeded.out);
  if (!ingested || Number(ingested[1]) === 0) {
    throw new Error(`seed ingested no items:\n${seeded.out}`);
  }
  console.log(`[setup] seeded ${ingested[1]} items from fixtures`);

  // --- 3. the server under test -------------------------------------------
  // Background polling is off: a poll firing mid-assertion would change unread
  // counts underneath the test, which is the classic source of a suite that
  // fails one run in twenty and gets blamed on "flakiness".
  // -dev is required now that a login is the default (A36): without it the
  // suite lands on the sign-in screen and every test fails waiting for .shell.
  // It is refused off loopback by the server itself, and this binds loopback.
  //
  // The Smart+ key is cleared, and that is a determinism fix rather than a
  // preference. The server loads `.env`, which on a developer's box has a real
  // OPENAI_API_KEY in it — so the suite behaved differently depending on
  // whether the person running it happened to have one, and a test asserting
  // "this instance has no OpenAI key" passed on CI and failed on the machine
  // that wrote it. A suite whose result depends on an untracked file is not a
  // gate. Setting it EMPTY rather than deleting it also beats the `.env` load,
  // which would otherwise put it back.
  app = spawn(bin, ['serve', '-db', DB, '-addr', `127.0.0.1:${APP_PORT}`, '-poll', '0', '-dev'], {
    cwd: repo,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, OPENAI_API_KEY: '', OPENAI_SDK_KEY: '' },
  });
  app.stdout.on('data', (d) => process.stdout.write(`[articleflux] ${d}`));
  app.stderr.on('data', (d) => process.stdout.write(`[articleflux] ${d}`));

  await waitForHealthy(`http://127.0.0.1:${APP_PORT}/healthz`);

  // What a WORKER needs to restart this server (8c.17).
  //
  // globalSetup runs in the main process and specs run in workers, which are
  // separate processes — so the handle above is not reachable from a test. The
  // environment is the only channel the two halves share, and workers inherit
  // it, so the binary and the database go through it. The same database
  // matters: the point of T21(e)'s restart is that the reader's list comes
  // BACK, not that a fresh empty one appears.
  process.env.AF_E2E_BIN = bin;
  process.env.AF_E2E_DB = DB;

  return async () => {
    // The slot goes back first, so a run that fails to tear the server down
    // still frees the port for the next one.
    releaseSlot();
    if (app) app.kill();
    // And whatever is on the port, which is not always `app`. T21(e) kills the
    // server and starts a replacement that deliberately outlives the worker
    // that spawned it (see server.mjs), so after that file the handle above
    // refers to a process that is already dead and the live server belongs to
    // nobody. Killing by port covers both, and covers the case where the
    // original died on its own.
    await killListener(APP_PORT);
    if (feedServer) await new Promise((ok) => feedServer.close(ok));
  };
}

// killListener terminates whatever holds a TCP port, identified by PID from
// netstat. Deliberately NOT `taskkill /IM articleflux.exe`: an image-name kill would
// also take out the dev server on :9000 that someone is watching.
async function killListener(port) {
  const netstat = await run('netstat', ['-ano'], here);
  const pids = new Set();
  for (const line of netstat.out.split(String.fromCharCode(10))) {
    if (!line.includes('LISTENING')) continue;
    const cols = line.trim().split(/\s+/);
    const local = cols[1] || '';
    if (!local.endsWith(`:${port}`)) continue;
    const pid = cols[cols.length - 1];
    if (pid && pid !== '0') pids.add(pid);
  }
  for (const pid of pids) {
    console.log(`[setup] killing stale listener on :${port} (pid ${pid})`);
    await run('taskkill', ['/PID', pid, '/F'], here);
  }
  if (pids.size) await new Promise((r) => setTimeout(r, 500));
}

// run executes a command to completion without blocking the event loop, so
// servers running in this process keep serving while it works.
function run(cmd, args, cwd) {
  return new Promise((resolve, reject) => {
    const p = spawn(cmd, args, { cwd, stdio: ['ignore', 'pipe', 'pipe'] });
    let out = '';
    p.stdout.on('data', (d) => { out += d; });
    p.stderr.on('data', (d) => { out += d; });
    p.on('error', reject);
    p.on('close', (code) => resolve({ code, out }));
  });
}

async function waitForHealthy(url, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch { /* not up yet */ }
    if (Date.now() > deadline) throw new Error(`server never became healthy at ${url}`);
    await new Promise((r) => setTimeout(r, 250));
  }
}
