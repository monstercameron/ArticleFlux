import { defineConfig, devices } from '@playwright/test';

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
  retries: 0,
  reporter: [['list']],
  globalSetup: './global-setup.mjs',
  use: {
    baseURL: process.env.ARTICLEFLUX_URL || 'http://127.0.0.1:9010',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
    { name: 'mobile',  use: { ...devices['Pixel 7'] } },
  ],
});
