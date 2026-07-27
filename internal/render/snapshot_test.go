package render

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// `usable` is the escalate-on-empty rule, and it is the half that can be tested
// without a browser — so it is tested hardest.
//
// The measure is TEXT, not markup length. A framework that failed to mount still
// emits its entire shell: nav, footer, a spinner, several kilobytes of perfectly
// real HTML and no article. `len(html)` says that page rendered; a person
// looking at it says it did not.
func TestUsableCountsTextRatherThanMarkup(t *testing.T) {
	long := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 20)

	cases := []struct {
		name string
		html string
		want bool
	}{
		{"nothing at all", "", false},
		{"an empty root", `<html><body><div id="root"></div></body></html>`, false},
		{
			// The case this function exists for: a big document with no content.
			"a shell with no article",
			`<html><head><title>x</title></head><body>` +
				`<nav class="site-navigation-primary"><ul><li><a href="/">Home</a></li>` +
				`<li><a href="/about">About</a></li></ul></nav>` +
				`<div id="root"></div>` +
				`<footer><p>&copy; 2026</p></footer></body></html>`,
			false,
		},
		{
			// The same shell, with the article the script was supposed to fill in.
			"a shell that mounted",
			`<html><body><nav>Home About</nav><div id="root"><article>` + long +
				`</article></div><footer>c 2026</footer></body></html>`,
			true,
		},
		{
			// Markup alone must not carry it over the line, which is what a
			// length check on the raw HTML would do.
			"an enormous amount of markup and no words",
			`<html><body>` + strings.Repeat(`<div class="a b c d e f g h"></div>`, 200) + `</body></html>`,
			false,
		},
		{
			// The near-miss a tag-stripper alone gets wrong. Script and style
			// bodies are text BETWEEN tags, and every modern page ships a bundle,
			// a JSON-LD block and an inlined stylesheet before it renders a word
			// — so counting them makes every unmounted shell look full.
			"a shell whose only text is its own scripts and styles",
			`<html><head><style>` + long + `</style>` +
				`<script type="application/ld+json">{"headline":"` + long + `"}</script></head>` +
				`<body><div id="root"></div><script>` + long + `</script></body></html>`,
			false,
		},
		{
			// And the inverse, so the fix cannot be "return false more often":
			// scripts alongside real content must not suppress it.
			"an article that also ships a bundle",
			`<html><head><script>` + long + `</script></head><body><article>` + long +
				`</article><script>` + long + `</script></body></html>`,
			true,
		},
		{
			// A document truncated mid-script has no text after the cut, not all
			// of it.
			"an unclosed script",
			`<html><body><div id="root"></div><script>` + long,
			false,
		},
		{
			// `<scriptable>` merely starts the same way. Eating it would take
			// the rest of the document with it.
			"an element whose name begins like script",
			`<html><body><scriptable>` + long + `</scriptable></body></html>`,
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := usable(c.html); got != c.want {
				t.Errorf("usable = %v, want %v", got, c.want)
			}
		})
	}
}

// A guard on the guard: the fixture below has to be one the OLD behaviour would
// have got wrong, or the JS-only test proves nothing.
func TestTheJSFixtureIsEmptyBeforeItsScriptRuns(t *testing.T) {
	if usable(jsOnlyPage) {
		t.Error("the un-run fixture already counts as usable, so a snapshot of it " +
			"would pass whether or not the script ever executed")
	}
}

// The fixture 6.16 names: an empty `#root` plus a script that fills it.
//
// Deliberately more than 400 characters AFTER the script runs and fewer before,
// so the assertion is about the script having run rather than about the page
// being big.
const jsOnlyPage = `<!doctype html><html><head><title>Built in the browser</title></head>
<body><nav>Home About Archive</nav><div id="root"></div><footer>&copy; 2026</footer>
<script>
document.getElementById('root').innerHTML =
  '<article><h1>Speculative decoding without a draft model</h1>' +
  '<p>' + 'The n-gram table is built from the prompt itself, which is why it costs nothing to carry. '.repeat(8) + '</p></article>';
</script></body></html>`

func jsOnlyServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(jsOnlyPage))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func browserOrSkip(t *testing.T) *Renderer {
	t.Helper()
	// Same guard as live_test.go's skipOnUnprovenHostedCI, applied here too:
	// every test that reaches this helper starts a real chromedp session, and
	// a hosted windows-latest runner has been observed to find Edge, launch
	// it, and produce no frame within sixty seconds — an environment gap, not
	// a timing one. ARTICLEFLUX_BROWSER_TESTS=1 forces these anywhere,
	// including CI, once a runner is confirmed to actually paint.
	if os.Getenv("ARTICLEFLUX_BROWSER_TESTS") == "" && os.Getenv("CI") != "" {
		t.Skip("CI: set ARTICLEFLUX_BROWSER_TESTS=1 to run browser-driven render tests")
	}
	// AllowPrivate, because the fixture server binds loopback and the guard
	// would otherwise refuse it — the same reason the e2e seed passes the flag.
	r := New(Options{AllowPrivate: true, IdleTimeout: 60 * time.Second})
	if !r.Available() {
		t.Skip("no Chromium/Edge/Chrome on this machine")
	}
	t.Cleanup(r.Close)
	return r
}

// 6.16's stated exit criterion, first half: a JS-only page comes back FILLED.
func TestSnapshotRunsTheScriptsThatBuildThePage(t *testing.T) {
	r := browserOrSkip(t)
	srv := jsOnlyServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	snap, err := r.Snapshot(ctx, srv.URL, Viewport{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snap.HTML, "Speculative decoding") {
		t.Errorf("the script's output is not in the HTML — the snapshot is of the "+
			"page before it built itself, which is the case this rung exists for:\n%s",
			truncate(snap.HTML, 400))
	}
	if snap.Title != "Built in the browser" {
		t.Errorf("title = %q", snap.Title)
	}
	if snap.FinalURL == "" {
		t.Error("no final URL, so a rewriter cannot resolve relative references")
	}
	// The fallback artifact, from the SAME session — a screenshot taken from a
	// different load is a picture of a different page.
	if len(snap.Screenshot) == 0 {
		t.Error("no screenshot; it is the artifact the reader gets when the DOM " +
			"comes out unusable")
	}
}

// The page that fetches its article AFTER it mounts, which is what
// "network-idle" is for.
//
// `WaitReady("body")` is satisfied the instant the shell parses, and a fixed
// sleep is wrong in both directions at once — too short for this page and pure
// waste on the ~70% that had finished before the timer started. The fixture
// answers its second request slowly on purpose, so a snapshot that did not wait
// comes back with the shell and a spinner.
func TestSnapshotWaitsForContentFetchedAfterMount(t *testing.T) {
	r := browserOrSkip(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/article" {
			// Slow enough that arriving before it is a real failure, short
			// enough to stay under the idle cap.
			time.Sleep(1500 * time.Millisecond)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(strings.Repeat(
				"Fetched from the network after the page had already mounted. ", 12)))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Late</title></head>
<body><div id="root">Loading…</div>
<script>
fetch('/article').then(r => r.text()).then(t => {
  document.getElementById('root').textContent = t;
});
</script></body></html>`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	snap, err := r.Snapshot(ctx, srv.URL, Viewport{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snap.HTML, "after the page had already mounted") {
		t.Errorf("the late fetch is missing — the snapshot was taken while the page "+
			"was still loading:\n%s", truncate(snap.HTML, 300))
	}
	if strings.Contains(snap.HTML, "Loading…") {
		t.Error("the placeholder is still there")
	}
}

// A page that never goes idle must still produce a snapshot.
//
// Analytics heartbeats, open websockets and long-polls mean a substantial share
// of real pages never report network-idle at all — and they are
// disproportionately the heavy commercial sites this rung exists for. Waiting
// forever on them would be a renderer that wedges, which is the thing 6.16
// refuses.
func TestAPageThatNeverGoesIdleStillRenders(t *testing.T) {
	r := browserOrSkip(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/poll" {
			// The long-poll that never returns, so the network is never quiet.
			<-req.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Chatty</title></head>
<body><article>` + strings.Repeat("The article itself is right here in the markup. ", 12) + `</article>
<script>fetch('/poll');</script></body></html>`))
	}))
	t.Cleanup(srv.Close)

	// Comfortably longer than the idle cap, so the ONLY thing that can end this
	// wait successfully is the cap. That is what makes the error check below the
	// whole assertion.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	snap, err := r.Snapshot(ctx, srv.URL, Viewport{})

	// Deliberately no wall-clock assertion (TODO P2).
	//
	// An earlier version also checked that this finished within the cap plus
	// thirty seconds, and that check measured the MACHINE: it passed in 11–16s
	// alone and failed at 52.5s during `go test ./...` while two other sessions
	// were building on the same box. A gate that goes red under load is one
	// people learn to re-run rather than read, and the next real failure gets
	// waved through with it.
	//
	// Nothing is lost by dropping it. The page never goes idle, so if the cap
	// did not fire this call could only end at the caller's deadline or the
	// renderer's own budget — and both of those come back as an ERROR. A
	// successful snapshot IS the proof that the cap released it, on a machine of
	// any speed.
	if err != nil {
		t.Fatalf("snapshot: %v — on a page that never goes idle, an error means the "+
			"cap did not release the render and something else ended it", err)
	}
	if !strings.Contains(snap.HTML, "right here in the markup") {
		t.Error("the article is missing from a page that simply never went quiet")
	}
}

// 6.16's exit criterion, second half: killing the browser mid-render FAILS the
// job rather than hanging it. A renderer that wedges is worse than one that
// refuses.
func TestKillingTheBrowserFailsTheRenderRatherThanHanging(t *testing.T) {
	r := browserOrSkip(t)

	// A server that accepts the connection and never answers, so the render is
	// genuinely in flight when the deadline lands.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
	}))
	t.Cleanup(hang.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := r.Snapshot(ctx, hang.URL, Viewport{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a page that never answers returned a successful snapshot")
		}
		// And it says WHOSE deadline it was. Every path out of a render cancels
		// the same internal context, so a bare context.Canceled cannot tell a
		// caller who gave up from a browser that died — and one of those is
		// worth retrying.
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("= %v, want the caller's DeadlineExceeded", err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("Snapshot did not return after its context was cancelled — it is " +
			"holding the single render slot, and the next caller gets ErrBusy forever")
	}
}

// An empty render is a DIFFERENT fact from a failed one: the ladder escalates on
// one and retries the other, so a caller that cannot tell them apart either
// escalates on a crash or serves a blank page.
func TestAnEmptyRenderIsDistinguishableFromAFailure(t *testing.T) {
	r := browserOrSkip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>t</title></head>` +
			`<body><div id="root"></div></body></html>`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	snap, err := r.Snapshot(ctx, srv.URL, Viewport{})
	if !errors.Is(err, ErrEmptyRender) {
		t.Fatalf("= %v, want ErrEmptyRender", err)
	}
	// And it still hands back what it got: the screenshot is exactly what the
	// reader is shown when the DOM is unusable, so returning nothing alongside
	// the error would throw away the fallback.
	if snap.HTML == "" {
		t.Error("an empty render returned no HTML at all, so nothing can be inspected")
	}

	// The ordering trap, checked here because this is the fixture that springs
	// it: a blank page compresses to almost nothing, so a budget check running
	// BEFORE the empty check declares it comfortably within budget and returns
	// success. The ladder never escalates and the reader gets an empty article
	// that cost a browser.
	if len(snap.Compressed) > DefaultMaxBytes {
		t.Fatal("the fixture is over budget, so it cannot tell the two checks apart")
	}
}

// §10.1c: the artifact is precompressed once at render, and the budget is
// enforced against the COMPRESSED size.
//
// Judging by `len(HTML)` is the tempting mistake and it is wrong in the
// expensive direction: HTML is the most compressible thing this application
// handles, so a raw-size budget rejects pages that would have arrived
// comfortably — and it rejects them by degrading to text, which the reader
// notices.
func TestTheBudgetIsAgainstTheCompressedSize(t *testing.T) {
	// Repetitive on purpose: it is what a real page's markup looks like to a
	// compressor, and the gap between the two measures is the whole point.
	html := `<html><body>` +
		strings.Repeat(`<p class="body-copy">Every paragraph on this page is the same paragraph.</p>`, 400) +
		`</body></html>`

	snap := Snapshot{HTML: html, Compressed: gzipString(html)}
	if len(snap.Compressed) == 0 {
		t.Fatal("nothing was compressed")
	}
	if len(snap.Compressed) >= len(html) {
		t.Fatalf("compressed to %d from %d, so this fixture proves nothing",
			len(snap.Compressed), len(html))
	}
	if snap.Bytes() != len(snap.Compressed) {
		t.Errorf("Bytes = %d, want the compressed size %d — a usable document is "+
			"served compressed, so that is what it costs", snap.Bytes(), len(snap.Compressed))
	}
	// And it round-trips, because an artifact cached in a form nothing can read
	// is worse than no cache.
	zr, err := gzip.NewReader(bytes.NewReader(snap.Compressed))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	back, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if string(back) != html {
		t.Error("the compressed copy does not decompress to the document")
	}
}

// The two artifacts are alternatives, not a sum.
func TestBytesChargesForWhatIsActuallySent(t *testing.T) {
	long := strings.Repeat("a sentence that is long enough to count as prose. ", 20)
	shot := make([]byte, 5000)

	full := Snapshot{HTML: `<html><body><article>` + long + `</article></body></html>`}
	full.Compressed = gzipString(full.HTML)
	if full.Bytes() != len(full.Compressed) {
		t.Errorf("a usable page charges %d, want its document size %d — adding the "+
			"screenshot would charge every page for a fallback it never sends",
			full.Bytes(), len(full.Compressed))
	}

	empty := Snapshot{HTML: `<html><body><div id="root"></div></body></html>`, Screenshot: shot}
	empty.Compressed = gzipString(empty.HTML)
	if empty.Bytes() != len(shot) {
		t.Errorf("an unusable page charges %d, want the screenshot's %d — the "+
			"picture is what the reader gets", empty.Bytes(), len(shot))
	}
}

// Over budget is a THIRD outcome: the render worked and must not be sent.
func TestAnOversizeSnapshotSaysSoRatherThanSucceeding(t *testing.T) {
	r := browserOrSkip(t)
	// Genuinely varied, which took a second attempt to get right: repeating one
	// paragraph 300 times produces a document that gzips to a couple of hundred
	// bytes, and the budget check — correctly — waves it through. A fixture that
	// is merely LONG does not test a compressed-size budget.
	var body strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&body, "<p>%d %x %s</p>", i, i*2654435761,
			strings.Repeat(string(rune('a'+i%26)), 3+i%17))
	}
	page := `<!doctype html><html><head><title>Long</title></head><body><article>` +
		body.String() + `</article></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(srv.Close)

	// A budget this page cannot meet, rather than a page built to exceed a real
	// one: the enforcement is what is under test, not anyone's taste in numbers.
	r.opt.MaxBytes = 300

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	snap, err := r.Snapshot(ctx, srv.URL, Viewport{})
	if !errors.Is(err, ErrOverBudget) {
		t.Fatalf("= %v, want ErrOverBudget", err)
	}
	// Reported, per §10.1c — "too big" with no number cannot be tuned.
	if !strings.Contains(err.Error(), "300") {
		t.Errorf("the error does not name the budget it broke: %v", err)
	}
	// And handed back anyway: a caller with a laxer budget of its own should not
	// have to pay for the render a second time to look at it.
	if snap.HTML == "" || len(snap.Compressed) == 0 {
		t.Error("an over-budget snapshot came back empty, so the render was wasted")
	}
}

// The same guard as Stream's, pinned separately here: Snapshot has its own
// call to CheckURL/CheckURLPermissive ahead of the semaphore and the browser,
// and nothing enforces that the two call sites stay in sync just because one
// of them is tested.
func TestSnapshotRefusesBlockedAddress(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	start := time.Now()
	_, err := r.Snapshot(context.Background(), "http://169.254.169.254/latest/meta-data/", Viewport{})
	if err == nil {
		t.Fatal("the metadata endpoint must be refused before a browser is started")
	}
	// See TestStreamRefusesBlockedAddress in live_test.go for why this bound
	// matters and how it was verified: without it, a guard that stopped
	// running still passes as long as browser launch or navigation eventually
	// errors on its own, which a genuinely unreachable address will.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("took %v to refuse a blocked address — a browser appears to have been "+
			"started before the guard ran", elapsed)
	}
}

// A default Renderer must cap concurrency at one, not zero: an unbuffered or
// zero-length semaphore channel would either block forever on the first
// caller or, if the guard were ever weakened to "<0" instead of "<=0", let an
// unlimited number of renders through — a preservation pass that shells out to
// a browser per item is exactly the "cooks the box" failure §10.1c names.
func TestDefaultMaxSessionsIsOne(t *testing.T) {
	r := New(Options{})
	defer r.Close()
	if cap(r.sem) != 1 {
		t.Errorf("default MaxSessions slot capacity = %d, want 1", cap(r.sem))
	}
}

// Snapshot and Stream are documented (render.go's Stream comment, and the
// remark on Snapshot) as sharing ONE resource — a browser tab on a box with
// one reader — so a slot held by either must refuse the other, not just its
// own kind. Both branches are exercised without starting a real browser: the
// semaphore is acquired directly and the guard is checked before r.browser()
// is ever called, so this is a pure unit test of the queueing, not a
// browser-driven one.
func TestSnapshotAndStreamShareTheSameSlot(t *testing.T) {
	r := New(Options{MaxSessions: 1})
	defer r.Close()

	r.sem <- struct{}{} // occupy the only slot by hand
	defer func() { <-r.sem }()

	if _, err := r.Snapshot(context.Background(), "http://example.invalid/", Viewport{}); !errors.Is(err, ErrBusy) {
		t.Errorf("Snapshot while the slot is held by something else: err = %v, want ErrBusy", err)
	}
	err := r.Stream(context.Background(), "some-session", "http://example.invalid/", Viewport{}, make(chan Frame, 1))
	if !errors.Is(err, ErrBusy) {
		t.Errorf("Stream while the slot is held by something else: err = %v, want ErrBusy — "+
			"Stream and Snapshot are documented as the same resource", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
