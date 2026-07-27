package render

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

// The SNAPSHOT half of §10.1c (TODO 6.16), the other half of `Stream`.
//
// # Why a snapshot when there is already a stream
//
// Streaming is the live rung: a tab, a screencast, and a reader driving it. A
// snapshot is one render, kept — the artifact tier 2r serves when a page builds
// itself in the browser and there is nothing static to fetch. §10.1c: **on
// demand, cached forever after.** Never a background sweep, because a
// preservation pass that shells out to a browser per item would cook the box and
// read as an attack from the publisher's side.
//
// # Two artifacts from ONE session, and that is the point
//
// The DOM comes back as `outerHTML`, and a full-page screenshot comes back
// beside it. Not because both are wanted — the HTML is what gets sanitized,
// rewritten and served — but because the HTML is what FAILS. A single-page
// application whose framework rendered into a shadow root, a page that detected
// automation, a site whose content is inside a canvas: all of those return
// well-formed HTML with nothing in it. The screenshot is what the reader gets
// when that happens, and it has to come from the same session or it is a picture
// of a different page load.
//
// # The scroll pass
//
// Lazy images resolve on intersection, so a page rendered at viewport height and
// screenshotted immediately has one screen of pictures and a column of
// placeholders. One pass to the bottom, then back to the top, then a settle —
// enough for `loading=lazy` and for the common IntersectionObserver recipe, and
// bounded so an infinite feed cannot be scrolled forever.

// ErrEmptyRender means the page produced no usable content.
//
// Distinguished from a failure because it is the ESCALATION signal: §10.1-R's
// ladder moves down a rung when the rung above comes back empty, and "empty" is
// a different fact from "the browser died". A caller that treats them alike
// either escalates on a crash it should have retried, or serves a blank page it
// should have escalated past.
var ErrEmptyRender = errors.New("render: the page rendered nothing usable")

// ErrOverBudget means the render succeeded and is too big to send.
//
// §10.1c: *a budget, enforced and reported — if the compressed artifact still
// exceeds it, degrade to rung 4 rather than sending it.* This rung is reached
// BECAUSE the link cannot carry a frame stream, so a snapshot that ships four
// megabytes has answered the wrong question: the reader asked for a page they
// could load, and half of one delivered slowly is worse than the text delivered
// now.
//
// A third outcome, distinct from both of the others, because the ladder does a
// third thing with it. Empty escalates. Failure retries. Over-budget degrades to
// the text — and the artifact is returned alongside the error, because a caller
// with a laxer budget of its own is entitled to look at what it got rather than
// pay for the render twice.
var ErrOverBudget = errors.New("render: the snapshot is larger than the budget")

// Snapshot is one page, rendered and kept.
type Snapshot struct {
	// HTML is the document after scripts have run — `outerHTML` of the root,
	// unsanitized. Sanitizing here would put a policy decision inside the
	// renderer; `sanitize.Snapshot` owns that.
	HTML string
	// Compressed is HTML gzipped, done once here and kept.
	//
	// §10.1c wants the artifact precompressed at render and cached that way, the
	// same trade §7.6 makes for the wasm bundle and for the same reason: it is
	// written once and served often. It is also the only honest way to enforce a
	// budget — the wire cost of a document is its COMPRESSED size, and HTML is
	// the most compressible thing this application handles. Judging a snapshot by
	// `len(HTML)` rejects pages that would have arrived comfortably.
	Compressed []byte
	// Screenshot is a full-page PNG from the same session.
	//
	// Empty if capture failed, which is not fatal: the HTML is the primary
	// artifact and a snapshot with no picture is still useful. The reverse is
	// not true, which is why an empty HTML is an error and an empty PNG is not.
	Screenshot []byte
	// Title is the document title, for the archive listing.
	Title string
	// FinalURL is where the browser ended up. A page that redirected is a page
	// whose relative URLs resolve against somewhere else, and the rewriter needs
	// the address that was actually loaded.
	FinalURL string
}

// Bytes is what this snapshot costs on the wire.
//
// The two artifacts are ALTERNATIVES, not a sum: the document is served when it
// is usable and the picture when it is not, and nothing sends both. Adding them
// would charge every page for a fallback it will never use, which is how a
// budget ends up rejecting artifacts that fit.
func (s Snapshot) Bytes() int {
	if usable(s.HTML) {
		return len(s.Compressed)
	}
	return len(s.Screenshot)
}

// DefaultMaxBytes is the wire budget for one snapshot.
//
// Sized against the rung below it rather than picked round: a screencast frame
// is 150–250 KB and continuous scrolling costs several megabytes a minute, so a
// link that cannot sustain tier 3 can still deliver one megabyte in a few
// seconds. Four cannot be justified — at that point the reader is waiting
// longer for a picture of an article than they would have waited for the
// article, which is the trade §10.1-R exists to refuse.
const DefaultMaxBytes = 1 << 20

// screenshotLadder is tried in order until one fits the budget.
//
// Re-encoding, not re-rendering: the page is still loaded, so each rung costs a
// JPEG encode rather than another three seconds of browser. It stops at 30
// because below that text goes mushy, and a screenshot nobody can read has
// spent bytes to deliver nothing.
var screenshotLadder = []int{80, 60, 45, 30}

// snapshotMinChars is how much text a render has to produce to count.
//
// Not zero, and not a word count: a framework that failed to mount leaves the
// nav, the footer and a spinner, which is several hundred characters of
// perfectly real HTML and no article. 400 is above that noise floor and below
// any page worth serving.
const snapshotMinChars = 400

// scrollPasses bounds the lazy-load walk.
//
// An infinite feed will keep growing for as long as it is scrolled, so this is a
// count rather than a "scroll until the height stops changing" loop — which on
// exactly the pages that need it never terminates.
const scrollPasses = 12

// Snapshot renders one page and returns what it produced.
//
// Bounded by the caller's context AND by the renderer's own timeout: a page that
// never settles must not hold the single render slot until the process
// restarts, and a caller that forgot a deadline must not be the reason it does.
func (r *Renderer) Snapshot(ctx context.Context, rawURL string, vp Viewport) (Snapshot, error) {
	// The same guard as Stream, with the same caveat recorded there: this stops
	// the obvious attempt, and the browser dials for itself so netguard's
	// socket-level Control never sees the connection. A page that redirects to a
	// private address after we hand it over is reachable BY THE BROWSER. What
	// makes that survivable here is the same thing that makes it survivable
	// there — nothing it reaches comes back as credentials, and this rung is
	// opt-in at the instance level.
	if r.opt.AllowPrivate {
		if err := netguard.CheckURLPermissive(rawURL); err != nil {
			return Snapshot{}, fmt.Errorf("render: %w", err)
		}
	} else if err := netguard.CheckURL(rawURL); err != nil {
		return Snapshot{}, fmt.Errorf("render: %w", err)
	}

	// One at a time, sharing the slot with Stream. A snapshot and a live view
	// are the same resource — a browser tab on the box that also serves reading
	// — and §22.7's queue is what turns "busy" into "later" for the caller.
	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return Snapshot{}, ErrBusy
	}

	alloc, err := r.browser()
	if err != nil {
		return Snapshot{}, err
	}

	tabCtx, cancelTab := chromedp.NewContext(alloc)
	defer cancelTab()

	budget := r.opt.IdleTimeout
	if budget <= 0 {
		budget = 3 * time.Minute
	}
	runCtx, cancelRun := context.WithTimeout(tabCtx, budget)
	defer cancelRun()

	// The caller's cancellation, wired in by hand.
	//
	// It cannot be the PARENT: chromedp carries the browser allocator in the
	// context, so a tab has to descend from `alloc` or there is nothing to open
	// it against. That is what makes this easy to get wrong — the context
	// argument is right there in the signature and looks like it is already
	// doing this, and it is not.
	//
	// Without it a caller who gives up waits for the RENDERER's budget instead of
	// their own, holding the single slot the whole time, and every other caller
	// gets ErrBusy for a render nobody is waiting for any more. Which is the
	// wedge 6.16 names: worse than refusing.
	stop := context.AfterFunc(ctx, cancelRun)
	defer stop()

	vp = vp.Clamp(Viewport{Width: r.opt.Width, Height: r.opt.Height})

	var (
		html  string
		title string
		final string
		shot  []byte
	)

	// Viewport and lifecycle reporting first, in their own Run.
	//
	// Two reasons it is not folded into the one below. Lifecycle events have to
	// be ENABLED before the navigation whose events we want, and `ListenTarget`
	// needs a target that exists — which the first Run is what creates.
	if err := chromedp.Run(runCtx,
		chromedp.EmulateViewport(int64(vp.Width), int64(vp.Height)),
		cdppage.SetLifecycleEventsEnabled(true),
	); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return Snapshot{}, fmt.Errorf("render: %w", cerr)
		}
		return Snapshot{}, fmt.Errorf("render: %w", err)
	}

	idle := watchIdle(tabCtx)

	err = chromedp.Run(runCtx,
		// Navigated by hand rather than through `chromedp.Navigate`, for the
		// loader id — which is what ties a lifecycle event to THIS navigation
		// rather than to the blank page that preceded it or a later in-page one.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, loader, errText, _, nerr := cdppage.Navigate(rawURL).Do(ctx)
			if nerr != nil {
				return nerr
			}
			if errText != "" {
				return fmt.Errorf("navigate: %s", errText)
			}
			idle.expect(loader)
			return nil
		}),
		// Ready, not Visible: a page whose body is empty until a script fills it
		// still has a body, and waiting for something visible would hang on the
		// exact pages this rung exists for.
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(idle.wait),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return settle(ctx, vp)
		}),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.Location(&final),
	)
	if err != nil {
		// A killed browser lands here, and it lands here as a FAILURE rather
		// than as a hang — which is the property 6.16 asks for by name: a
		// renderer that wedges is worse than one that refuses.
		//
		// The caller's own cancellation is reported AS ITSELF. Everything above
		// cancels through `cancelRun`, so chromedp reports a bare
		// context.Canceled whether the reader closed the tab or the deadline
		// passed, and a caller who cannot tell those apart retries a request
		// nobody is waiting for.
		if cerr := ctx.Err(); cerr != nil {
			return Snapshot{}, fmt.Errorf("render: %w", cerr)
		}
		return Snapshot{}, fmt.Errorf("render: %w", err)
	}

	max := r.opt.MaxBytes
	if max <= 0 {
		max = DefaultMaxBytes
	}

	// The screenshot is attempted after the DOM is in hand and its failure is
	// swallowed. It is the fallback artifact, so losing it costs the fallback;
	// losing the HTML costs everything, and that already succeeded.
	shot = r.capture(runCtx, max)

	snap := Snapshot{
		HTML:       html,
		Compressed: gzipString(html),
		Screenshot: shot,
		Title:      title,
		FinalURL:   final,
	}

	// Order matters, and this is the order: EMPTY BEFORE OVER-BUDGET. An empty
	// render's compressed document is tiny and its screenshot is the artifact, so
	// checking size first would wave through a blank page as comfortably within
	// budget and never escalate.
	if !usable(html) {
		return snap, ErrEmptyRender
	}
	if n := snap.Bytes(); n > max {
		// Reported, not just enforced: "too big" without a number cannot be
		// tuned, and this is the one decision an operator is likely to want to
		// argue with.
		return snap, fmt.Errorf("%w: %d bytes compressed against a %d budget", ErrOverBudget, n, max)
	}
	return snap, nil
}

// capture takes the full-page screenshot, walking the quality ladder until it
// fits.
//
// Returns whatever the last rung produced even when nothing fit: that is a
// decision for the caller, which knows whether it is about to serve this or has
// a usable document and never needed it. Deciding here would throw away the
// fallback for a page whose DOM also failed, which is the one case it exists
// for.
func (r *Renderer) capture(ctx context.Context, max int) []byte {
	var best []byte
	for _, q := range screenshotLadder {
		var shot []byte
		if err := chromedp.Run(ctx, chromedp.FullScreenshot(&shot, q)); err != nil {
			// A browser that died here has already given us the DOM, which is the
			// artifact that matters. Keep whatever an earlier rung produced.
			return best
		}
		best = shot
		if len(shot) <= max {
			return shot
		}
	}
	return best
}

// gzipString precompresses the document.
//
// BestCompression rather than the default: this runs once per page, on a rung
// that already spent seconds in a browser, and the artifact is cached forever
// after. Trading CPU here for bytes on every future read is the same bargain
// §7.6 makes for the wasm bundle.
func gzipString(s string) []byte {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil
	}
	if _, err := zw.Write([]byte(s)); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

// idleWait caps how long the network is given to go quiet.
//
// Reaching it is NOT a failure and does not stop the render. Analytics
// heartbeats, open websockets and long-polls mean a substantial share of real
// pages never report idle at all, and refusing those would refuse exactly the
// heavy commercial sites this rung exists for. The DOM at the cap is worse than
// the DOM at idle and enormously better than nothing.
const idleWait = 8 * time.Second

// idleWatch reports when Chrome considers this navigation's network quiet.
//
// The alternative — sleeping a fixed interval — is wrong in both directions at
// once: too short for a page that fetches its article after mount, and pure
// waste on the ~70% that were finished before the timer started.
type idleWatch struct {
	mu   sync.Mutex
	seen map[cdp.LoaderID]bool
	want cdp.LoaderID
}

// watchIdle starts listening. It must be called after the target exists and
// before the navigation whose events matter.
func watchIdle(tabCtx context.Context) *idleWatch {
	w := &idleWatch{seen: map[cdp.LoaderID]bool{}}
	chromedp.ListenTarget(tabCtx, func(ev any) {
		e, ok := ev.(*cdppage.EventLifecycleEvent)
		if !ok || e.Name != "networkIdle" {
			return
		}
		w.mu.Lock()
		w.seen[e.LoaderID] = true
		w.mu.Unlock()
	})
	return w
}

// expect names the navigation to wait for.
func (w *idleWatch) expect(loader cdp.LoaderID) {
	w.mu.Lock()
	w.want = loader
	w.mu.Unlock()
}

// wait blocks until this navigation's network is quiet, the cap passes, or the
// context ends.
//
// Events are RECORDED rather than signalled, and that is the whole reason this
// is a type instead of a channel: a local fixture can be idle before `expect`
// has stored the loader id, and a signal sent to nobody is a signal lost — the
// render would then wait out the full cap on the fastest pages in the suite.
func (w *idleWatch) wait(ctx context.Context) error {
	deadline := time.NewTimer(idleWait)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		w.mu.Lock()
		done := w.want != "" && w.seen[w.want]
		w.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			// The cap, deliberately not an error.
			return nil
		case <-tick.C:
		}
	}
}

// settle scrolls to the bottom and back, so lazy images resolve.
func settle(ctx context.Context, vp Viewport) error {
	for i := 0; i < scrollPasses; i++ {
		if err := chromedp.Evaluate(
			fmt.Sprintf("window.scrollBy(0, %d)", vp.Height), nil).Do(ctx); err != nil {
			return err
		}
		// A frame each, rather than one long wait at the end: an
		// IntersectionObserver fires when the element crosses the viewport, so
		// scrolling past it in one jump can miss it entirely.
		if err := chromedp.Sleep(120 * time.Millisecond).Do(ctx); err != nil {
			return err
		}
	}
	// Back to the top, because the screenshot is full-page and a page left
	// scrolled can have position:sticky chrome baked across the middle of it.
	if err := chromedp.Evaluate("window.scrollTo(0, 0)", nil).Do(ctx); err != nil {
		return err
	}
	return chromedp.Sleep(300 * time.Millisecond).Do(ctx)
}

// usable reports whether a render produced enough to be worth serving.
//
// Text, not markup length: a framework that failed to mount still emits its
// entire shell, so `len(html)` is large and meaningless. This strips tags
// crudely and counts what is left, which is the only measure that distinguishes
// "the page rendered" from "the page exists".
//
// Script and style bodies come out first, and that is not a refinement — it is
// the difference between this working and not. Their contents are TEXT between
// tags, so a tag-stripper counts them, and a modern page carries the framework
// bundle, a JSON-LD block and an inlined critical stylesheet before it has
// rendered one word. Counting those makes every unmounted shell look full,
// which is precisely the case this function exists to catch.
func usable(html string) bool {
	if html == "" {
		return false
	}
	return len(strings.Join(strings.Fields(stripTags(dropRawText(html))), " ")) >= snapshotMinChars
}

// rawText are the elements whose contents are code rather than prose.
var rawText = []string{"script", "style", "noscript", "template", "svg"}

// dropRawText removes those elements, contents and all.
//
// Case-insensitive on the tag name and tolerant of an unclosed one: markup this
// arrives as is whatever the page emitted, and a document that ends mid-script
// should count as having no text after it rather than as having all of it.
func dropRawText(html string) string {
	lower := strings.ToLower(html)
	var b strings.Builder
	i := 0
	for i < len(html) {
		start, name := nextRawOpen(lower, i)
		if start < 0 {
			b.WriteString(html[i:])
			break
		}
		b.WriteString(html[i:start])
		close := strings.Index(lower[start:], "</"+name)
		if close < 0 {
			// Unclosed: everything after it is inside the element.
			break
		}
		i = start + close
		if end := strings.Index(lower[i:], ">"); end >= 0 {
			i += end + 1
		} else {
			break
		}
	}
	return b.String()
}

// nextRawOpen finds the next raw-text opening tag at or after i.
func nextRawOpen(lower string, i int) (int, string) {
	best, bestName := -1, ""
	for _, name := range rawText {
		at := i
		for {
			j := strings.Index(lower[at:], "<"+name)
			if j < 0 {
				break
			}
			j += at
			// `<scriptable>` is not `<script>`: the character after the name has
			// to end it, or a tag that merely starts the same way is eaten along
			// with everything following it.
			if k := j + 1 + len(name); k < len(lower) {
				if c := lower[k]; c != '>' && c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '/' {
					at = j + 1
					continue
				}
			}
			if best < 0 || j < best {
				best, bestName = j, name
			}
			break
		}
	}
	return best, bestName
}

// stripTags removes markup and keeps what was between it.
func stripTags(html string) string {
	var b strings.Builder
	depth := 0
	for _, r := range html {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
