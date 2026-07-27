// Does the published demo actually work?
//
// Not a Playwright *test* — deliberately. The suite in this directory drives a
// real server against a real database through global-setup.mjs, and the demo has
// neither. This is a standalone script over the same browser, so it can be
// pointed at anything serving the three files `make demo` produces:
//
//     node e2e/demo-smoke.mjs http://127.0.0.1:9310/ [screenshot.png]
//
// It is what stands between a green build and a published page that boots to a
// blank screen. Every other check — the unit tests, the guards, the size
// ratchet, the artifact verification in the workflow — can pass while the bundle
// they produced does not start, and the demo is the one build of this
// application that nobody is watching when it breaks.
//
// The filename ends in .mjs rather than .spec.mjs on purpose: playwright's
// default testMatch would otherwise pick it up and run it inside the suite,
// against the suite's server, which is not what it is for.
import { chromium } from '@playwright/test';

const url = process.argv[2] || 'http://127.0.0.1:9310/';
const shot = process.argv[3] || '';

// Generous, and it has to be. A headless browser throttles requestAnimationFrame
// to a fraction of the normal frame rate, so a client that renders in two frames
// interactively can take seconds here — and the module is six megabytes that
// have to be fetched, decompressed and compiled before any of that. The number
// is an upper bound on a slow cold runner, not an expectation: a failed boot
// does not wait it out, because the shim reports failure and this watches for it.
const BOOT_MS = 180_000;
const SETTLE_MS = Number(process.env.AF_DEMO_SETTLE_MS || 20_000);

const problems = [];
const fail = (m) => problems.push(m);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.on('pageerror', (e) => fail(`uncaught error in the page: ${e.message}`));

// The favicon endpoint belongs to the server, and a static host does not have
// one — those 404s are expected and documented (docs/DEMO.md). So is exactly one
// 404 for app.wasm: the boot shim tries the uncompressed module first, which
// only a server has. Anything else that fails is a broken build.
const failedRequests = [];
page.on('response', (r) => {
  if (r.status() < 400) return;
  const u = new URL(r.url());
  const path = u.pathname + u.search;
  if (path.includes('/favicon') || u.pathname.endsWith('/app.wasm')) return;
  failedRequests.push(`${r.status()} ${path}`);
});

const started = Date.now();
try {
  await page.goto(url, { waitUntil: 'domcontentloaded' });

  // Two ways this ends, and waiting for both is what turns a broken module from
  // a three-minute timeout into a three-second sentence. The shim hides the
  // splash once main() has rendered, and paints `.failed` with a message when it
  // cannot start the module at all.
  await page.waitForSelector('#boot[hidden], #boot.failed', { state: 'attached', timeout: BOOT_MS });

  if (await page.locator('#boot.failed').count()) {
    const said = (await page.locator('#boot-msg').innerText().catch(() => '')).trim();
    fail(`the boot shim could not start the module: ${said || '(no message)'}`);
  } else {
    const bootSeconds = ((Date.now() - started) / 1000).toFixed(1);

    // The instance loads over the (in-memory) gRPC connection AFTER the first
    // render, so nothing below is true at hand-over.
    await page.locator('.feed-row').first().waitFor({ timeout: SETTLE_MS });
    await page.locator('.item-row').first().waitFor({ timeout: SETTLE_MS });

    const rail = (await page.locator('.masthead-sub').innerText()).trim().replace(/\s+/g, ' ');
    // `.feed-row` is the rail's row shape and the streams, categories and tags
    // wear it too; the data attribute is what distinguishes a subscription from
    // the eleven other things in that column.
    const feeds = await page.locator('.rail-scroll .feed-row[data-source-id]').count();
    const rows = await page.locator('.item-row').count();
    console.log(`booted in ${bootSeconds}s · masthead "${rail}" · ${feeds} feeds · ${rows} rows`);

    // Seven sources is the fixture set (client/demodata/seed.go). The exact
    // number rather than "more than none", because a partial rail is the failure
    // that looks most like success.
    if (feeds !== 7) fail(`the rail lists ${feeds} subscriptions; the fixtures seed 7`);
    if (rows === 0) fail('the item list rendered no rows');
    if (/\b0 feeds\b/.test(rail)) fail(`the masthead says "${rail}"`);

    // An article, end to end: a click on a list row, a GetItem over the
    // in-memory connection, and the reading pane rendering a body that is not
    // the one it was already showing.
    //
    // The reading pane is a STREAM — the article being read plus its prefetched
    // neighbours — so "the first .article-body on the page" is not the one being
    // read and does not change when a row is clicked. `data-current` is the
    // marker that says which one is, and reading the wrong element here would be
    // a check that passes for a page where nothing responds to a click.
    const current = '.article[data-current="true"]';
    await page.locator(`${current} .article-body`).waitFor({ timeout: SETTLE_MS });
    const opened = (await page.locator(current).getAttribute('data-article-id')) || '';

    await page.locator('.item-row').nth(2).click();
    let id = opened;
    let body = '';
    for (const deadline = Date.now() + SETTLE_MS; Date.now() < deadline; ) {
      id = (await page.locator(current).getAttribute('data-article-id').catch(() => opened)) || opened;
      body = (await page.locator(`${current} .article-body`).innerText().catch(() => '')).trim();
      if (id !== opened && body) break;
      await page.waitForTimeout(250);
    }
    console.log(`article opened: ${body.slice(0, 70).replace(/\s+/g, ' ')}…`);
    if (id === opened) {
      fail('clicking a list row did not change the article being read');
    } else if (body.length < 200) {
      fail(`the article body is only ${body.length} characters long`);
    }

    // The demo says what it is. A build that lost the note is a page presenting
    // invented articles with nothing on it to say they are invented.
    if ((await page.locator('.demo-note').count()) !== 1) {
      fail('the demo note is not on the page');
    }
  }

  if (failedRequests.length) {
    fail(`requests failed that should not have:\n      ${failedRequests.slice(0, 8).join('\n      ')}`);
  }
} catch (err) {
  fail(`${err.name}: ${err.message.split('\n')[0]}`);
} finally {
  if (shot) await page.screenshot({ path: shot }).catch(() => {});
  await browser.close();
}

if (problems.length) {
  console.error('\nThe demo bundle does not work:');
  for (const p of problems) console.error(`  · ${p}`);
  process.exit(1);
}
console.log('the demo bundle works');
