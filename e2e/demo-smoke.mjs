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
// It takes either a URL or a DIRECTORY:
//
//     node e2e/demo-smoke.mjs bin/demo              # serves it itself
//     node e2e/demo-smoke.mjs https://…/ArticleFlux/  # checks a deployed one
//
// Serving it here rather than starting a server beside it in the workflow is
// deliberate. A backgrounded server in a CI step needs a PID, a trap, a
// wait-for-the-port loop and a kill that works on every platform — four moving
// parts, all of them in YAML, none of them tested — to do what fifteen lines of
// node do inside the process that already has to exit cleanly.
//
// The filename ends in .mjs rather than .spec.mjs on purpose: playwright's
// default testMatch would otherwise pick it up and run it inside the suite,
// against the suite's server, which is not what it is for.
import { chromium } from '@playwright/test';
import { createServer } from 'node:http';
import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import { join, normalize, extname } from 'node:path';

const target = process.argv[2] || 'bin/demo';
const shot = process.argv[3] || '';

// A static file server as unhelpful as GitHub Pages: no compression
// negotiation, no directory listing, no SPA fallback, and a 404 for anything
// that is not on disk — which is what /favicon and /app.wasm have to be for the
// demo's own fallbacks to be the ones under test.
const TYPES = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.gz': 'application/gzip',
  '.wasm': 'application/wasm',
  '.woff2': 'font/woff2',
  '.webmanifest': 'application/manifest+json',
  '.png': 'image/png',
};

async function serve(root) {
  const server = createServer(async (req, res) => {
    const rel = decodeURIComponent(new URL(req.url, 'http://x').pathname);
    const file = join(root, normalize(rel === '/' ? '/index.html' : rel).replace(/^(\.\.[/\\])+/, ''));
    try {
      const info = await stat(file);
      if (!info.isFile()) throw new Error('not a file');
      res.writeHead(200, {
        'content-type': TYPES[extname(file)] || 'application/octet-stream',
        'content-length': info.size,
      });
      createReadStream(file).pipe(res);
    } catch {
      res.writeHead(404, { 'content-type': 'text/plain' }).end('not found\n');
    }
  });
  // Port 0: the operating system picks one that is free, so this cannot collide
  // with a development server, the e2e suite, or another copy of itself.
  await new Promise((ok) => server.listen(0, '127.0.0.1', ok));
  return { server, url: `http://127.0.0.1:${server.address().port}/` };
}

const local = /^https?:\/\//.test(target) ? null : await serve(target);
const url = local ? local.url : target;
if (local) console.log(`serving ${target} at ${url}`);

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
  const landed = await page.goto(url, { waitUntil: 'domcontentloaded' });

  // Is this the application at all? Without these two lines a directory with no
  // index.html in it waits out the full boot timeout and then reports that a
  // selector never appeared, which is true and says nothing. Both are instant on
  // a real page.
  if (!landed || !landed.ok()) {
    throw new Error(`${url} answered ${landed ? landed.status() : 'nothing'}`);
  }
  await page.waitForSelector('#boot', { state: 'attached', timeout: 10_000 })
    .catch(() => { throw new Error(`${url} served something that is not the reader`); });

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

    // Discover (§18.7), and the reason it is checked HERE rather than left to
    // the Go tests next door.
    //
    // Every RPC behind this screen shipped to the public URL answering
    // Unimplemented, and the screen sat on "Couldn't load recommendations" for
    // as long as it took somebody to open it. Nothing noticed: the bundle was
    // well-formed, it booted, the rail listed seven feeds and an article opened.
    // A demo failure that is invisible from the outside is the expensive kind,
    // and the cheap way to make this one visible is to open the screen.
    //
    // Last, because it replaces the reading pane the checks above are about.
    await page.locator('[data-action="open-discover"]').first().click();
    await page.locator('#discover-page').waitFor({ timeout: SETTLE_MS });
    await page.locator('.discover-card, .discover-gate, .discover-status-error').first()
      .waitFor({ timeout: SETTLE_MS })
      .catch(() => { fail('Discover never resolved — it is still saying "Loading…"'); });

    // The consent gate comes first, and a visitor gets past it by pressing the
    // toggle — so the check does too, rather than treating the gate as the
    // answer. Both halves matter: the gate must be what a fresh demo shows,
    // because a recommender working on somebody's behalf before they asked is
    // the thing it exists to prevent, and the list behind it must actually
    // arrive, because that is what proves the in-memory instance serves §18.7
    // at all. Checking only the first would pass on a demo whose Discover is
    // permanently empty.
    if (await page.locator('.discover-gate').count()) {
      console.log('discover: consent gate up, turning Smart+ review on');
      await page.locator('.discover-smartplus').first().click();
      await page.locator('.discover-card, .discover-status-error').first()
        .waitFor({ timeout: SETTLE_MS })
        .catch(() => { fail('Discover stayed on the gate after the toggle was pressed'); });
    }

    if (await page.locator('.discover-status-error').count()) {
      fail('Discover could not load its recommendations from the in-memory instance');
    } else {
      // Polled rather than sampled once, and the reason is not politeness about
      // slow rendering: this pane's body is REPLACED between the card list and
      // the loading placeholder while it settles, so a single `count()` lands on
      // whichever frame it happened to catch. A run that read 0 cards and, a
      // line later, read the evidence out of a card was what made that obvious.
      let cards = 0;
      let evidence = '';
      for (const deadline = Date.now() + SETTLE_MS; Date.now() < deadline; ) {
        cards = await page.locator('.discover-card').count();
        if (cards > 0) {
          evidence = (await page.locator('.discover-card-evidence').first()
            .innerText().catch(() => '')).trim();
          if (evidence) break;
        }
        await page.waitForTimeout(250);
      }
      console.log(`discover: ${cards} suggestions · first reads "${evidence.slice(0, 60)}…"`);
      // Empty is a legitimate state on a real instance and never on this one:
      // the fixtures seed a harvest, so no cards means the scorer refused
      // everything or the list never arrived.
      if (cards === 0) fail('Discover rendered no suggestions; the fixtures seed a harvest that survives the gate');
      // Every card explains itself, which is the whole argument of the feature
      // (§18.7) and the one thing a screenshot of it cannot prove.
      else if (!evidence) fail('a Discover card carries no evidence');
    }

    // The rail's numbers have to follow a bulk mark. Genuinely last, because it
    // empties the fixture.
    //
    // Every handler that bypasses loadFeeds() refetches the sidebar by hand,
    // and each of them remembered the feed list and forgot the two counts that
    // are not on it — so Mark all read emptied My Feed and left its own badge
    // reading eight, above a list with nothing unread in it. Every OTHER number
    // was right, which is what made it read as one odd badge instead of a
    // missing refresh, and is why this is checked by reading them all.
    const railCounts = () => page.evaluate(() => {
      const out = {};
      for (const row of document.querySelectorAll('.feed-row')) {
        const name = row.querySelector('.feed-name')?.textContent?.trim();
        const n = row.querySelector('.feed-count')?.textContent?.trim();
        if (name && n) out[name] = n;
      }
      return out;
    });

    await page.locator('.feed-row', { hasText: 'My Feed' }).first().click();
    await page.locator('[data-action="mark-all-arm"]').first().waitFor({ timeout: SETTLE_MS });
    const before = await railCounts();
    await page.locator('[data-action="mark-all-arm"]').first().click();
    await page.locator('[data-action="mark-all"]').first().click();

    let after = before;
    for (const deadline = Date.now() + SETTLE_MS; Date.now() < deadline; ) {
      after = await railCounts();
      if (!after['My Feed'] && !after['All articles']) break;
      await page.waitForTimeout(250);
    }
    // The tags are the control: their badge is how many FEEDS carry the tag, not
    // how many unread articles, so it is the one number that must NOT move — a
    // check that passed because everything went blank would prove nothing.
    const stale = Object.keys(after).filter((k) => !['longform', 'daily', 'keep'].includes(k));
    console.log(`mark all read: ${Object.keys(before).length} rail counts before, ${stale.length} unread counts left after`);
    if (stale.length) {
      fail(`these rail counts survived Mark all read: ${stale.map((k) => `${k}=${after[k]}`).join(', ')}`);
    }
    if (!after.longform) {
      fail('the tag counts vanished too — they count feeds, not unread articles, and must not move');
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
  // Closed explicitly rather than left to process exit: a listening socket is
  // the one thing that can keep node alive after the last line of a script, and
  // a workflow step that never returns is worse than one that fails.
  if (local) await new Promise((ok) => local.server.close(ok));
}

if (problems.length) {
  console.error('\nThe demo bundle does not work:');
  for (const p of problems) console.error(`  · ${p}`);
  process.exit(1);
}
console.log('the demo bundle works');
