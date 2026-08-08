import { defineConfig, devices } from '@playwright/test';

import { BASE_URL } from './ports.mjs';

// The suite drives a REAL server against a REAL database seeded from fixture
// feeds served locally. No mocking: the point of e2e here is to catch the wiring
// between wasm, the gRPC tunnel, SQLite and FTS5 — which is exactly the seam
// unit tests cannot reach.
export default defineConfig({
  testDir: '.',
  // The wasm bundle is ~25 MB and compiles on first load, so the default 30s is
  // not enough on a cold run.
  timeout: 120_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,   // one server, one database
  workers: 1,
  // One retry, and the reason is to make flakiness VISIBLE rather than to hide
  // it.
  //
  // A test that fails and then passes is reported as `flaky`, in its own section
  // of the summary — it does not silently count as green. What retries buy is
  // the difference between a suite whose failure list means something and one
  // where two real failures arrive surrounded by three different timing casualties
  // each run, so nobody can tell which is which. Measured over four full runs on
  // this box: the same two specs fail every time, and one to three others rotate.
  //
  // This is a shared machine and headless Chromium is throttled hard when nothing
  // is being clicked (the rAF note in reader.spec applies to the whole suite), so
  // the rotating failures are load, not behaviour. If a test starts appearing in
  // the flaky list every run, that is the signal to fix it — which a permanent
  // `retries: 0` red never gave anyone, because the list was never stable enough
  // to notice a pattern in.
  retries: 1,
  reporter: [['list']],
  globalSetup: './global-setup.mjs',
  use: {
    // Ports come from ports.mjs, which derives them from the process id so two
    // concurrent runs cannot kill each other's server. See that file.
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    launchOptions: {
      // Stop Chromium throttling the render loop, which is the single cause
      // behind most of this suite's "flakiness".
      //
      // GWC drives every render from requestAnimationFrame, and headless
      // Chromium decides an unfocused, occluded, backgrounded page does not
      // need frames — measured in reader.spec at FIVE frames in twelve
      // seconds. Every state transition then waits seconds for a frame a real
      // tab paints immediately, so assertions time out on a page that is
      // functionally correct and merely not being painted.
      //
      // The individual `timeout: 45_000` allowances scattered through the specs
      // are that mechanism worked around one test at a time. These three flags
      // address it at the source, and they are the standard set for exactly
      // this: no timer throttling, no backgrounding when occluded, no renderer
      // backgrounding.
      //
      // It matters far more in CI than on a developer's box: a two-core runner
      // with no display throttles harder, which is why 95 tests failed there
      // against the two that fail here.
      args: [
        '--disable-background-timer-throttling',
        '--disable-backgrounding-occluded-windows',
        '--disable-renderer-backgrounding',
      ],
    },
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'mobile',  use: { ...devices['Pixel 7'] } },
  ],
});
