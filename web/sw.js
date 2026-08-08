// The one unavoidable JavaScript file (A26, TODO 8.4, plan.md §12.3).
//
// A Service Worker intercepts `fetch`. It cannot see WebSocket frames — and A5
// put every RPC on a tunnel, so the standard PWA recipe does not apply here at
// all. This caches the APP SHELL and nothing else: the page, the loader, and the
// wasm module, so the reader boots on a plane. Data lives in IndexedDB and
// offline packs arrive over plain HTTPS at M10. Anything cleverer belongs in the
// wasm app, where it can be tested.
//
// # index.html is network-FIRST, and that is the whole design
//
// Cache-first on the shell is the recipe everybody reaches for, and it is how a
// browser ends up running last month's application against this month's server
// forever. That failure is real enough that the server now refuses clients below
// a minimum version (§22.10) — this is the other half: when the network is
// there, the newest page wins, and the cache is a fallback rather than a source
// of truth.
//
// The wasm module is cache-first because it is megabytes and its URL does not
// change within a build. VERSION is what retires it: a new build changes this
// file, the browser notices the bytes differ, installs the new worker, and
// `activate` deletes every cache that is not the current one.

// VERSION must equal internal/buildver.Version.
//
// Checked by a Go test rather than by a comment asking people to remember —
// forgetting to bump it means the old module is served forever, which is
// exactly the failure this file has to avoid and the one nobody notices,
// because everything keeps working with old code.
const VERSION = '1.2.0';
const CACHE = `articleflux-shell-${VERSION}`;

// DEV is the hole in everything the comment above claims.
//
// The retirement story — "a new build changes VERSION, the browser installs the
// new worker, activate deletes the old cache" — is true of a RELEASE and false
// of every build anybody actually makes. A development version carries a `-dev`
// suffix and keeps the same number through a hundred rebuilds, so the cache
// keyed on it is never retired and `app.wasm` is served cache-first from
// whenever the browser first saw it.
//
// The result is the worst class of bug this file can produce, because it does
// not look like caching. It looks like the feature you just built not working:
// the client is old, the server is new, and every symptom points at the code
// that was just written. It cost two false bug reports on the slideshow — one
// "the podcast stopped playing", one "the cursor never comes back" — both of
// which were fixed code that the browser was refusing to load.
//
// So a dev build revalidates the shell and a tagged build does not. Offline
// still works either way: the fetch below falls through to the cache when the
// network is not there, which is the case this worker exists for.
const DEV = VERSION.endsWith('-dev');

// The shell. Deliberately short: every entry here is something the reader
// cannot boot without.
// Both module filenames, because the two deployments publish different ones: a
// server ships app.wasm and prefers app.wasm.gz by content negotiation, and the
// static demo publishes ONLY the gzip. Listing one meant the demo's install
// fetched a file that does not exist, cached nothing, and left the module
// uncached — a Service Worker whose entire job is booting offline, that could
// not boot the thing that matters. Misses are survivable (see below), so listing
// both costs a 404 on whichever host lacks one.
// The manifest and the icons are shell too (§20.24). An installed app whose
// launcher icon is a broken image the first time it opens on a plane is an
// installed app that looks broken — and the manifest is what the browser re-reads
// to decide whether the installation is still valid.
//
// The two purpose-"any" icons and the maskable one, not the apple-touch PNG: iOS
// copies its icon into the home screen at install time and never fetches it
// again, so caching it would spend bytes on a request that is never made.
const SHELL = [
  './', './index.html', './wasm_exec.js', './app.wasm', './app.wasm.gz',
  './manifest.webmanifest',
  './icons/icon-192.png', './icons/icon-512.png', './icons/maskable-512.png',
];

self.addEventListener('install', (e) => {
  e.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    // addAll is atomic and that is wrong here: one 404 — a static host that has
    // only app.wasm.gz, a deploy mid-upload — would fail the whole install and
    // leave no worker at all. Each entry is fetched on its own and a miss is
    // survivable, because a partially warm cache still beats none.
    await Promise.all(SHELL.map(async (url) => {
      try {
        const res = await fetch(url, { cache: 'reload' });
        if (res.ok) await cache.put(url, res);
      } catch (_) { /* offline during install; runtime will fill it in */ }
    }));
    // Take over without waiting for every tab to close. The alternative is a
    // fixed application that stays broken until the reader closes a tab they
    // have had open since Tuesday.
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    const names = await caches.keys();
    await Promise.all(names.map((n) =>
      n.startsWith('articleflux-shell-') && n !== CACHE ? caches.delete(n) : null));
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  const url = new URL(req.url);

  // Only this origin, only GET. A cached POST is a replayed mutation, and
  // another origin's response is not ours to serve.
  if (req.method !== 'GET' || url.origin !== self.location.origin) return;

  // Never the tunnel, the probes, or the proxies. `/grpc` is a WebSocket
  // upgrade that a Service Worker cannot serve anyway; a cached `/readyz` would
  // report a healthy server that is not there, which is worse than no answer.
  if (/^\/(grpc|readyz|healthz|debug|asset|p|pack|pub)(\/|$)/.test(url.pathname)) return;

  // Nor the broadcast's music (§19), and for two separate reasons.
  //
  // Size: the beds are twelve megabytes between them, which is twice the whole
  // shell. They are OPTIONAL — most readers will never turn one on, and one
  // reader will only ever play one — so putting them in a cache whose job is
  // "what you cannot boot without" would be spending an offline budget on
  // background music.
  //
  // Range requests: an <audio> element seeks with Range, and a Service Worker
  // that answers one from a cached 200 hands the browser a whole file where it
  // asked for a slice. Some browsers cope and some produce a track that will not
  // play at all. The browser's own HTTP cache handles these correctly and needs
  // no help from here.
  if (/^\/audio(\/|$)/.test(url.pathname)) return;

  // Nor spoken articles, and the Range argument above applies here verbatim —
  // /speech is an <audio src> too. Three more reasons of its own:
  //
  // It is CACHE-FIRST down there, so a stored recording is returned without the
  // network being consulted at all. What a listen says depends on preferences
  // the server reads at request time — the summary instead of the article — and
  // the URL cannot carry every one of them, so turning a switch on and pressing
  // play answered from this cache with the words from before the switch. That
  // is not a stale asset; it is a setting that appears not to work.
  //
  // The stale fallback below matches with `ignoreSearch`, which for a sealed
  // listening ticket means "any /speech response will do" — one article's audio
  // for another's, on a dropped connection.
  //
  // And it is one reader's article read aloud: private, per-account, megabytes
  // apiece, in a cache whose job is the shell you cannot boot without. The
  // browser's own HTTP cache holds it correctly for a day (see serveSpoken's
  // Cache-Control) and needs no help from here.
  if (/^\/speech(\/|$)/.test(url.pathname)) return;

  // Navigations and the page itself: network first, cache as the fallback.
  if (req.mode === 'navigate' || url.pathname === '/' || url.pathname.endsWith('/index.html')) {
    e.respondWith((async () => {
      try {
        const res = await fetch(req);
        if (res.ok) (await caches.open(CACHE)).put('./index.html', res.clone());
        return res;
      } catch (err) {
        // Same reasoning as below: a cached page if there is one, and otherwise
        // a response that SAYS something. Response.error() renders as a browser
        // network-failure page with no clue in it.
        const cached = await caches.match('./index.html');
        if (cached) return cached;
        return new Response(
          '<!doctype html><meta charset=utf-8><title>ArticleFlux</title>' +
          '<body style="font:16px system-ui;padding:3rem;max-width:34rem">' +
          '<h1>ArticleFlux is offline</h1><p>The page could not be fetched and ' +
          'nothing is cached yet. Reload once you are connected.</p>',
          { status: 504, statusText: 'Offline', headers: { 'Content-Type': 'text/html; charset=utf-8' } });
      }
    })());
    return;
  }

  // Everything else in the shell: cache first, and fill on the way past.
  e.respondWith((async () => {
    // Except while developing, where cache-first means "never see your own
    // build again" — see DEV. The cache is still written, and still answers
    // when the network does not, so the offline path is unchanged.
    if (DEV) {
      try {
        const res = await fetch(req);
        if (res.ok && res.type === 'basic') {
          (await caches.open(CACHE)).put(req, res.clone());
        }
        return res;
      } catch (err) {
        // Offline. Fall through to the cache below, which is the whole point.
      }
    }
    const hit = await caches.match(req);
    if (hit) return hit;
    try {
      const res = await fetch(req);
      // Only a real 200 is stored. Caching a 404 for app.wasm bricks the reader
      // offline, and an opaque response has a status of 0 — indistinguishable
      // from a success and impossible to validate.
      if (res.ok && res.type === 'basic') {
        (await caches.open(CACHE)).put(req, res.clone());
      }
      return res;
    } catch (err) {
      // A rejected respondWith is not "the request failed" — the browser turns
      // it into a synthetic **503**, and that is what a reader sees in the
      // console: `wasm_exec.js 503`, then `Go is not defined`, on a file the
      // server is serving perfectly well. One dropped connection during a boot
      // is enough, and a reload does not fix it while the worker keeps doing it.
      //
      // So the failure is answered rather than thrown: the cache if there is
      // anything, and otherwise a 504 that says which address failed and why.
      // The page's own error path can then report something true.
      const stale = await caches.match(req, { ignoreSearch: true });
      if (stale) return stale;
      return new Response(
        'ArticleFlux: could not reach ' + url.pathname + ' — ' + (err && err.message),
        { status: 504, statusText: 'Offline', headers: { 'Content-Type': 'text/plain' } });
    }
  })());
});
