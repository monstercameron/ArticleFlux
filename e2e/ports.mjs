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
// # Derived from the process id, then PUT IN THE ENVIRONMENT
//
// The second half is not optional, and the first attempt got it wrong: Playwright
// runs each worker in its own process, so a bare `process.pid % 100` gives the
// config a different answer in the main process than in the workers. The server
// starts on one port and every test connects to another — 19 of 21 failed, which
// looks like a broken application and is a broken harness.
//
// So the value is computed once, by whichever process loads this first (the main
// one, when Playwright reads the config), and written into `process.env`. Workers
// inherit the environment, so they read the answer rather than recomputing it.
//
// Not `:0` and a free-port lookup, which would be more rigorous: the config needs
// a baseURL before globalSetup runs, so there is nowhere to put the answer. 100
// distinct pairs is ample for "two agents share this machine" and needs no
// coordination.
//
// The range starts at 9100 to stay clear of 9000 (the dev server Cam watches) and
// of 9010/9011, which older invocations and any running suite may still hold.
const slot = process.pid % 100;

if (!process.env.AF_E2E_APP_PORT) process.env.AF_E2E_APP_PORT = String(9100 + slot * 2);
if (!process.env.AF_E2E_FEED_PORT) process.env.AF_E2E_FEED_PORT = String(9101 + slot * 2);

export const APP_PORT = Number(process.env.AF_E2E_APP_PORT);
export const FEED_PORT = Number(process.env.AF_E2E_FEED_PORT);
export const BASE_URL = process.env.ARTICLEFLUX_URL || `http://127.0.0.1:${APP_PORT}`;

// Where the fixture feeds are served. Exported because SPECS need it too: a
// test that subscribes to a fixture page has to name it, and a literal 9011 in
// a spec is a test pointing at nothing the moment the ports move.
export const FEED_ORIGIN = `http://127.0.0.1:${FEED_PORT}`;
