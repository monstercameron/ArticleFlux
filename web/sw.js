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
const VERSION = '0.1.0-dev';
const CACHE = `articleflux-shell-${VERSION}`;

// The shell. Deliberately short: every entry here is something the reader
// cannot boot without.
const SHELL = ['./', './index.html', './wasm_exec.js', './app.wasm'];

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

  // Navigations and the page itself: network first, cache as the fallback.
  if (req.mode === 'navigate' || url.pathname === '/' || url.pathname.endsWith('/index.html')) {
    e.respondWith((async () => {
      try {
        const res = await fetch(req);
        if (res.ok) (await caches.open(CACHE)).put('./index.html', res.clone());
        return res;
      } catch (_) {
        return (await caches.match('./index.html')) || Response.error();
      }
    })());
    return;
  }

  // Everything else in the shell: cache first, and fill on the way past.
  e.respondWith((async () => {
    const hit = await caches.match(req);
    if (hit) return hit;
    const res = await fetch(req);
    // Only a real 200 is stored. Caching a 404 for app.wasm bricks the reader
    // offline, and an opaque response has a status of 0 — indistinguishable
    // from a success and impossible to validate.
    if (res.ok && res.type === 'basic') {
      (await caches.open(CACHE)).put(req, res.clone());
    }
    return res;
  })());
});
