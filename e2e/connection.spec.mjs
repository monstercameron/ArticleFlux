import { test, expect, boot } from './fixtures.mjs';
import { startServer, stopServer } from './server.mjs';

/**
 * T21(e) — what the badge says when the connection is actually broken
 * (TODO 8c.17, §20.19, A40).
 *
 * The audit that produced these found that everything failing LOUDLY was
 * handled, so what remained was everything that fails quietly. The badge is the
 * one surface that tells a reader which kind of trouble they are in, and its
 * three answers mean genuinely different things:
 *
 *   down     there is a network and the server is not answering — a countdown,
 *            and a remedy that belongs to whoever runs the server
 *   offline  the browser says there is no network — no countdown, because one
 *            promising a reconnect that cannot happen is worse than silence
 *   live     calls are flowing
 *
 * Conflating `offline` with `down` is the failure worth testing for: it is a
 * countdown ticking toward nothing, on a train, next to a reader who can see
 * perfectly well that their wifi is off.
 *
 * These run in their own file because they KILL THE SHARED SERVER. Playwright
 * runs one worker here (`workers: 1`), so nothing else is mid-request when it
 * happens — and the server is restarted before the file ends whatever the
 * result, or every spec after this one fails for a reason that has nothing to
 * do with it.
 */
test.describe('the connection badge', () => {
  // Longer than the default: a kill, a health-check poll, a restart and a
  // reconnect do not fit in the ordinary budget, and a timeout here would read
  // as "the badge never changed" rather than "the server took a while to die".
  test.describe.configure({ timeout: 180_000 });

  test.afterEach(async () => {
    // Unconditional. A test that fails midway must not leave the suite without
    // a server: the next file would fail on reset-state and the real failure
    // would be buried under fifty ECONNREFUSEDs.
    try {
      await startServer();
    } catch { /* already running, which is the common case */ }
  });

  const badge = (page) => page.locator('.conn').first();

  test('a server that goes away says down, and coming back says live', async ({ page }) => {
    await boot(page);
    await expect(badge(page)).toHaveAttribute('data-state', 'live');

    // Killed rather than shut down: the state under test is a server that WENT
    // AWAY, not one that said goodbye. A clean shutdown closes the tunnel
    // politely, and the client would learn through a channel a crash does not
    // have.
    expect(await stopServer(), 'nothing was listening to kill').toBe(true);

    // `down`, not `offline`: the browser still has a network, so the remedy
    // belongs to whoever runs the server and the badge has to say which.
    await expect(badge(page)).toHaveAttribute('data-state', 'down', { timeout: 60_000 });

    await startServer();

    // Back to live on its own. No reload, no click: the retry loop is what is
    // being tested, and a reader who has to press something has not recovered,
    // they have restarted.
    await expect(badge(page)).toHaveAttribute('data-state', 'live', { timeout: 90_000 });

    // And the list came back rather than staying empty — the recovery refetch
    // (§20.19) is the half that makes `live` mean anything.
    await expect(page.locator('.item-row').first()).toBeVisible({ timeout: 60_000 });
  });

  test('no network says offline, not down', async ({ page, context }) => {
    await boot(page);
    await expect(badge(page)).toHaveAttribute('data-state', 'live');

    await context.setOffline(true);

    // The distinction this whole test exists for. `down` here would put a
    // countdown in front of somebody whose wifi is off, ticking toward a
    // reconnect that cannot happen until they do something the countdown does
    // not mention.
    await expect(badge(page)).toHaveAttribute('data-state', 'offline', { timeout: 60_000 });

    await context.setOffline(false);
    // Generous on purpose. Coming back from a severed socket is the slowest
    // recovery this application has: the browser restores the network
    // instantly, but the tunnel only learns its old socket is dead through a
    // keepalive cycle, and the redial then starts from wherever the backoff had
    // escalated to — up to its 20-second cap. Anything tighter measures the
    // backoff schedule rather than whether recovery happens.
    await expect(badge(page)).toHaveAttribute('data-state', 'live', { timeout: 150_000 });
  });
});
