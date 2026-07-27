import { test, expect, boot } from './fixtures.mjs';
import { APP_PORT } from './ports.mjs';

/**
 * Live updates arrive without a refresh (TODO F2, 8.7, §20.3).
 *
 * The pump, the bus and the streaming RPC all existed and passed their own
 * tests; what nobody had asserted is the only claim a reader cares about —
 * something happened on the server, and the screen changed. That is the
 * assertion here, and it is deliberately made from the OUTSIDE: no evaluate(),
 * no reaching into client state, nothing but a poll on the server and a list on
 * the screen.
 *
 * Deliberately its own file. It drives the fixture feed and then asks the server
 * to poll, which changes the shared database in a way other specs do not expect
 * — and the reset before each test is per-USER state, not per-source.
 */
test.describe('live updates', () => {
  test.describe.configure({ timeout: 180_000 });

  test('an item arriving on the server appears without a refresh', async ({ page, request }) => {
    await boot(page);

    // The rail's unread count for the fixture feed, before anything happens.
    // The COUNT rather than the row: an item arriving changes it, and it is
    // rendered from the sidebar query the pump invalidates.
    const rows = page.locator('.item-row');
    const before = await rows.count();
    expect(before).toBeGreaterThan(0);

    // Ask the server to ingest. `/debug/ingest-one` is DevMode-only, like
    // reset-state — it writes one item into the fixture source, which is what a
    // poll finding something new does, without waiting for a poll interval.
    const res = await request.post(`http://127.0.0.1:${APP_PORT}/debug/ingest-one`);
    expect(res.ok(), 'the debug ingest endpoint answered ' + res.status()).toBeTruthy();

    // No reload, no click, no keypress. If this passes only after a refresh,
    // the pump is not running — which is exactly the state F2 describes, and it
    // is invisible from every other test in this suite.
    await expect(rows).toHaveCount(before + 1, { timeout: 60_000 });
  });
});
