import { closeSync, mkdirSync, openSync, rmSync, statSync, writeSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// The ports this run owns.
//
// # Why this is its own module
//
// `global-setup.mjs` kills whatever holds these ports, and the fixture feed
// server lives inside the Playwright process itself — so two runs on the same
// ports do not merely collide, the second one KILLS THE FIRST RUNNER. The
// symptom is a run that prints "Running 22 tests" and then exits after three of
// them with no result, no failure and no error, which reads as a crashed test
// or a flaky app and is neither.
//
// That was documented, and the remedy was an environment variable. A remedy you
// have to remember is a remedy that does not get used: the suite spent a while
// being unable to complete on this machine, and the diagnosis cost more than the
// fix. So the DEFAULT is now safe and the environment variable is an override
// rather than the fix.
//
// # A slot is CLAIMED, not guessed
//
// The first version derived the slot from `process.pid % 100`. That is a
// guess, and a guess is not enough here because the penalty for colliding is
// not "two runs share a port" — `killListener` below kills whatever holds it,
// and the fixture feed server lives inside the Playwright process, so a
// collision KILLS THE OTHER RUN. It happened: a suite ran seventeen tests and
// then every remaining one failed with ECONNREFUSED, with no panic and no log
// line, because the process that died was not the one writing the log.
//
// So the slot is claimed with an exclusive file create (`wx`), which the OS
// makes atomic — the same primitive a lock file has always been. Scan upward
// until one succeeds; that is the slot, and no concurrent run can have it.
//
// A run that is killed rather than closed leaves its lock behind, so a lock
// older than STALE_MS is stolen. Thirty minutes is longer than any run here
// (the full suite is about ten) and short enough that a crashed run does not
// cost a slot for the day.
//
// The answer then goes into `process.env`, because Playwright runs each worker
// in its own process: a worker that re-derived would get a different slot, the
// server would be on one port and the tests on another, and every test would
// fail. That was the second version's bug — 19 of 21 failed, which looks like a
// broken application and is a broken harness.
//
// # The lock directory has to be checkout-independent, not just process-wide
//
// This used to live at `<repo>/e2e/.tmp/ports`, derived from this file's own
// path. That coordinates workers within one run, but the claim in the header
// above — "no concurrent run can have it" — was false across two checkouts, or
// even one checkout reached by two different paths (a symlink, a mapped drive,
// a worktree): each resolves to a *different* `LOCK_DIR`, so two runs can both
// win an exclusive create and both believe they own the same 9400-range port.
// The port numbers are a machine-wide resource; the lock coordinating them has
// to be too, so it now lives under the OS temp directory rather than under this
// file, keyed by name rather than by path.
const LOCK_DIR = join(tmpdir(), 'articleflux-e2e-ports');

// 9400, not 9100, and the reason is migration rather than taste.
//
// The lock coordinates runs that share this directory. A run started from an
// older checkout does not have the lock and computes `9100 + (pid % 100) * 2`,
// which covers 9100-9298 — so while any of those are still in flight they can
// still kill a locked run that landed in the same range, and the symptom is the
// one this whole file exists to stop. Moving the base outside the range the old
// formula can produce makes that impossible rather than unlikely.
//
// Once nothing is running the old code the base is arbitrary again.
const BASE = 9400;
const SLOTS = 50;
const STALE_MS = 30 * 60 * 1000;

function claimSlot() {
  mkdirSync(LOCK_DIR, { recursive: true });
  for (let slot = 0; slot < SLOTS; slot++) {
    const lock = join(LOCK_DIR, `${slot}.lock`);
    try {
      // 'wx' fails if the file exists. That failure IS the mutual exclusion.
      const fd = openSync(lock, 'wx');
      writeSync(fd, String(process.pid));
      closeSync(fd);
      return slot;
    } catch (e) {
      if (e.code !== 'EEXIST') throw e;
      try {
        if (Date.now() - statSync(lock).mtimeMs > STALE_MS) {
          rmSync(lock, { force: true });
          slot--; // retry this slot, now free
        }
      } catch { /* someone else took it between the stat and the unlink */ }
    }
  }
  // Every slot is held. Falling back to the pid rather than refusing to run:
  // fifty concurrent suites is not a state worth failing for, and the pid is
  // what this used to do.
  return process.pid % SLOTS;
}

if (!process.env.AF_E2E_APP_PORT) {
  const slot = claimSlot();
  process.env.AF_E2E_PORT_SLOT = String(slot);
  process.env.AF_E2E_APP_PORT = String(BASE + slot * 2);
  process.env.AF_E2E_FEED_PORT = String(BASE + 1 + slot * 2);
}

export const APP_PORT = Number(process.env.AF_E2E_APP_PORT);
export const FEED_PORT = Number(process.env.AF_E2E_FEED_PORT);
export const BASE_URL = process.env.ARTICLEFLUX_URL || `http://127.0.0.1:${APP_PORT}`;

// Where the fixture feeds are served. Exported because SPECS need it too: a
// test that subscribes to a fixture page has to name it, and a literal 9011 in
// a spec is a test pointing at nothing the moment the ports move.
export const FEED_ORIGIN = `http://127.0.0.1:${FEED_PORT}`;

// releaseSlot gives the lock back, so a machine that runs the suite all day
// does not walk the slot range. Called from globalSetup's teardown; a run that
// is killed instead relies on the staleness sweep above.
export function releaseSlot() {
  const slot = process.env.AF_E2E_PORT_SLOT;
  if (slot === undefined) return;
  try { rmSync(join(LOCK_DIR, `${slot}.lock`), { force: true }); } catch { /* already gone */ }
}
