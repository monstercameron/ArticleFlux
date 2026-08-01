package buildver

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Service Worker's cache version must equal this build's version.
//
// A stale cache version is the failure §22.10 exists to compensate for: the
// worker keeps serving last month's `app.wasm`, the reader runs old code against
// a new server, and NOTHING LOOKS WRONG — everything works, with old code, until
// something does not. The skew refusal catches it at the server; this catches it
// at the source, which is somebody forgetting to bump a constant in a JavaScript
// file they were not thinking about.
//
// Checked from Go because Go is where the version lives and where the test suite
// runs. The alternative is a comment in sw.js asking people to remember, which
// is the thing that fails.
func TestTheServiceWorkerCacheVersionMatchesThisBuild(t *testing.T) {
	src, err := os.ReadFile("../../web/sw.js")
	if err != nil {
		t.Fatalf("cannot read the service worker: %v", err)
	}

	re := regexp.MustCompile(`(?m)^const VERSION = '([^']*)';`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatal("sw.js has no `const VERSION = '...';` line — either it was " +
			"renamed, or this test has stopped checking anything")
	}
	if got := string(m[1]); got != Version {
		t.Errorf("sw.js caches under version %q and this build is %q.\n"+
			"The worker will go on serving the old app.wasm to everyone who has "+
			"already visited, and nothing will look wrong — it will just be old "+
			"code. Bump VERSION in web/sw.js.", got, Version)
	}
}

// The worker must not put itself in front of the tunnel or the probes.
//
// `/grpc` is a WebSocket upgrade a Service Worker cannot serve anyway, so
// intercepting it can only break things; a cached `/readyz` reports a healthy
// server that is not there, which is worse than no answer at all.
func TestTheServiceWorkerLeavesTheTunnelAndProbesAlone(t *testing.T) {
	src, err := os.ReadFile("../../web/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, path := range []string{"grpc", "readyz", "healthz", "pack", "asset"} {
		if !strings.Contains(text, path) {
			t.Errorf("sw.js does not mention %q, so it may be intercepting it", path)
		}
	}
	// And it must refuse anything that is not a plain same-origin GET: a cached
	// POST is a replayed mutation.
	if !strings.Contains(text, "req.method !== 'GET'") {
		t.Error("sw.js does not restrict itself to GET; a cached POST is a replayed mutation")
	}
	if !strings.Contains(text, "url.origin !== self.location.origin") {
		t.Error("sw.js does not restrict itself to this origin")
	}
}

// index.html must actually register it, and must do so after the app is
// running — registering earlier puts a cache in front of the fetch that is
// booting the page, so a bad worker could stop the app starting at all, and a
// reader who cannot start the app cannot reach the thing that would unregister
// it.
func TestTheWorkerIsRegisteredAfterTheAppStarts(t *testing.T) {
	src, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)

	reg := strings.Index(text, "serviceWorker.register")
	if reg < 0 {
		t.Fatal("index.html never registers the service worker, so it does nothing")
	}
	run := strings.Index(text, "go.run(result.instance)")
	if run < 0 {
		t.Fatal("index.html no longer calls go.run; this test has lost its anchor")
	}
	if reg < run {
		t.Error("the service worker is registered BEFORE the wasm module runs — a " +
			"bad worker could then stop the application from ever starting")
	}
}

// Spoken articles must not go through the worker at all.
//
// The handler below the exclusions is CACHE-FIRST, so a stored recording is
// returned without the network being consulted — and what a listen says depends
// on preferences the server reads at request time. Turning "summarise" on and
// pressing play answered from this cache with the words from before the switch,
// which is not a stale asset but a setting that appears not to work.
//
// The same Range argument the music beds already make applies here too: /speech
// is an <audio src>, and a worker answering a Range request from a cached 200
// hands the browser a whole file where it asked for a slice.
func TestTheServiceWorkerLeavesSpokenArticlesAlone(t *testing.T) {
	src, err := os.ReadFile("../../web/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `/^\/speech(\/|$)/.test(url.pathname)) return;`) {
		t.Error("sw.js no longer excludes /speech, so spoken articles are being " +
			"served cache-first: a preference that changes what is spoken will " +
			"appear to do nothing, and the stale fallback below can answer one " +
			"article's request with another's audio")
	}
}
