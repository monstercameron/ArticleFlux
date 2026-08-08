// Package render drives a headless browser and streams what it paints.
//
// This is §10.1d — the rung the ladder reaches when a page cannot be shown any
// other way. Everything below it hands the reader *markup*: tier 2 fetches a
// page and re-serves it, which works right up until the page is a program
// rather than a document. Above that line the only honest answer is to run the
// program somewhere that can reach it and send back what it looks like.
//
// # Why frames instead of a snapshot
//
// A screenshot is one moment. A page that is still loading, lazily filling in,
// or waiting on a font produces a screenshot of a half-built page and no way to
// tell. Chrome's screencast is damage-driven: it emits a frame when pixels
// change and stays silent when they do not, so a static article costs roughly
// one frame and a page that is still settling corrects itself as it goes.
//
// # How far the input channel goes
//
// Scroll, and only scroll. That is not where the line was meant to be — the
// first version had no input at all — and it moved because a view you cannot
// scroll is a poster of the top of a page, which answers almost nothing anyone
// opens a live view for.
//
// Clicking is deliberately still absent. A wheel event moves a viewport and can
// do nothing else; a click can follow a link, submit something, or start a
// download, and each of those needs an answer about what a remote page is
// allowed to reach from this box. Scroll needed no such answer, which is why it
// could ship on its own.
package render

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

var (
	// ErrNoBrowser is returned when no Chromium-family browser could be found.
	ErrNoBrowser = errors.New("render: no chromium-family browser found")
	// ErrBusy is returned when every render slot is taken.
	ErrBusy = errors.New("render: another page is already streaming")
	// ErrNoSession is returned when input arrives for a stream that is not
	// running — usually one that timed out or whose viewer already left.
	ErrNoSession = errors.New("render: no live session")
)

// Frame is one painted image.
type Frame struct {
	// JPEG is a complete image, not a delta. Screencast has no inter-frame
	// compression, which is the format's main cost and the reason §10.1d wants
	// tile diffing eventually.
	JPEG []byte
	// Seq counts frames in this session, for logging and for a client that
	// wants to know whether anything is arriving at all.
	Seq int
}

// Options configure a Renderer.
type Options struct {
	// ExecPath is the browser binary. Empty means "look in the usual places",
	// which is what makes this work on a Windows laptop and an Ubuntu server
	// without a per-host config file.
	ExecPath string
	// AllowPrivate relaxes the SSRF pre-check. The browser itself is NOT
	// governed by netguard's dialer — see Stream.
	AllowPrivate bool
	// Width and Height are the viewport. Zero means 1280x800.
	Width, Height int
	// Quality is the JPEG quality, 1-100. Zero means 62, which is where text
	// stops looking chewed and the bytes are still affordable over a tunnel.
	Quality int
	// MaxSessions caps concurrent streams. Zero means 1: each one is a browser
	// tab holding a live page, and this is a reader on a home box, not a
	// rendering farm.
	MaxSessions int
	// IdleTimeout ends a session that has produced nothing. Zero means 3m.
	IdleTimeout time.Duration
	// MaxBytes is the wire budget for one snapshot (§10.1c). Zero means
	// DefaultMaxBytes. Exceeding it is not a failure — it is the signal to
	// degrade to the text rung, which is why it is reported rather than silently
	// truncated.
	MaxBytes int
}

// Renderer owns the browser.
type Renderer struct {
	opt Options
	sem chan struct{}
	// live maps a session key to the tab currently serving it, so input can be
	// delivered to a stream that is already running. Guarded by mu.
	mu   sync.Mutex
	live map[string]*liveSession
	// alloc is the shared browser process. Created on first use rather than at
	// boot: an instance where nobody opens a live view should never pay for a
	// browser, and on a small box that is 300 MB of not paying for it.
	// allocMu guards the three fields below. Its own lock rather than mu,
	// which is held while the live-session map is touched.
	allocMu     sync.Mutex
	alloc       context.Context
	allocCancel context.CancelFunc
}

// New builds a Renderer. It does not start a browser.
func New(opt Options) *Renderer {
	if opt.Width <= 0 {
		opt.Width = 1280
	}
	if opt.Height <= 0 {
		opt.Height = 800
	}
	if opt.Quality <= 0 {
		opt.Quality = 62
	}
	if opt.MaxSessions <= 0 {
		opt.MaxSessions = 1
	}
	if opt.IdleTimeout <= 0 {
		opt.IdleTimeout = 3 * time.Minute
	}
	return &Renderer{
		opt:  opt,
		sem:  make(chan struct{}, opt.MaxSessions),
		live: map[string]*liveSession{},
	}
}

// Available reports whether a browser could be found, without starting one.
func (r *Renderer) Available() bool { return FindBrowser(r.opt.ExecPath) != "" }

// Close shuts the browser down.
func (r *Renderer) Close() {
	// Under allocMu, like every other reader of these fields. browser() can now
	// be called concurrently with a shutdown, and an unguarded read of
	// allocCancel while another goroutine writes it is a race the Once used to
	// hide.
	r.allocMu.Lock()
	defer r.allocMu.Unlock()
	if r.allocCancel != nil {
		r.allocCancel()
		r.allocCancel, r.alloc = nil, nil
	}
}

// FindBrowser locates a Chromium-family binary.
//
// The list is ordered by "most likely to be the one the operator expects".
// Windows is the development box and ships Edge in a fixed location; Ubuntu is
// the deployment target and puts Chrome or Chromium on PATH. Both are covered
// without asking anyone to configure a path, which matters because the failure
// mode of getting it wrong is a feature that silently does not exist.
func FindBrowser(override string) string {
	if override != "" {
		if _, err := os.Stat(override); err == nil {
			return override
		}
		return ""
	}

	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{
			os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("LocalAppData"),
		} {
			if base == "" {
				continue
			}
			candidates = append(candidates,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "Chromium", "Application", "chrome.exe"),
			)
		}
	default:
		candidates = []string{
			"/usr/bin/google-chrome", "/usr/bin/google-chrome-stable",
			"/usr/bin/chromium", "/usr/bin/chromium-browser",
			"/snap/bin/chromium", "/usr/bin/microsoft-edge",
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// browser starts the shared process once, and retries the LOOKUP every time.
//
// # Why this is not a sync.Once
//
// It was one, and a Once caches whatever the first call decided — including
// "there is no browser on this machine". That is the one outcome here that
// changes without a restart: a self-hosted operator opens the live view, is told
// no browser was found, installs Chromium, and tries again. With the result
// latched, every attempt after that failed identically until the process was
// restarted, and nothing on screen suggested restarting was the remedy.
//
// It also put two methods on this type into open disagreement. `Available`
// calls FindBrowser fresh on every call, so the surface reported the feature as
// available while Stream refused it with ErrNoBrowser — the same question
// answered two ways by the same object.
//
// So the ALLOCATOR is created once and cached, which is what the Once was
// actually for — an instance where nobody opens a live view still never pays for
// a browser — and the lookup is retried on every call that has not yet
// succeeded. Guarded by its own mutex rather than r.mu: that one is held while
// the live-session map is touched, and this holds its lock across allocator
// construction.
func (r *Renderer) browser() (context.Context, error) {
	r.allocMu.Lock()
	defer r.allocMu.Unlock()
	if r.alloc != nil {
		return r.alloc, nil
	}
	{
		exec := FindBrowser(r.opt.ExecPath)
		if exec == "" {
			// Deliberately NOT recorded. The next call looks again.
			return nil, ErrNoBrowser
		}
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(exec),
			chromedp.Flag("headless", "new"),
			// A disposable profile in the OS temp directory, never the data
			// directory: this browser opens hostile pages, and it must not be
			// able to reach the database sitting next to it (§21).
			chromedp.UserDataDir(""),
			chromedp.Flag("incognito", true),
			chromedp.Flag("disable-extensions", true),
			chromedp.Flag("disable-background-networking", true),
			chromedp.Flag("disable-sync", true),
			chromedp.Flag("no-first-run", true),
			chromedp.Flag("no-default-browser-check", true),
			// Nothing here is a person, so nothing here should be logged into
			// anything or asked to accept anything.
			chromedp.Flag("disable-features", "Translate,MediaRouter,OptimizationHints"),
			chromedp.Flag("mute-audio", true),
			chromedp.WindowSize(r.opt.Width, r.opt.Height),
		)
		ctx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
		r.alloc, r.allocCancel = ctx, cancel
	}
	return r.alloc, nil
}

// Stream navigates to url and sends frames until ctx ends.
//
// # The guard, and what it can and cannot do here
//
// CheckURL runs before navigation and is a real rejection: it stops the obvious
// "stream me http://169.254.169.254" outright. What it cannot do is govern the
// browser's own socket, because the browser dials for itself and netguard's
// Dialer.Control never sees it. A page that redirects to a private address
// after we have handed it over is therefore reachable by the BROWSER, and the
// mitigations are that nothing it fetches comes back as data — only as pixels —
// and that the browser holds no credentials for anything.
//
// That is a genuinely weaker position than every other fetch in this codebase,
// and it is why this rung is opt-in at the instance level rather than on by
// default like the other two.
// liveSession is a running stream: the tab it lives in, and the size it is
// rendering at. The size is here rather than read from Options because input
// has to be aimed at the middle of THIS session's viewport, and two sessions
// can be different sizes.
type liveSession struct {
	tab context.Context
	vp  Viewport
	// lastInput is when the reader last acted on this session. Guarded by
	// Renderer.mu, like the map that holds it.
	//
	// It exists because the idle timeout measures FRAMES, and frames are
	// damage-driven: a settled article emits none. The loop's own comment says
	// so — "on a settled article is the normal state" — and then relies on the
	// timeout being generous to cover it. Three minutes is not generous next to
	// reading a dense page, so a reader who was present but not scrolling had
	// their session reclaimed underneath them.
	//
	// A scroll is the one signal that says "somebody is still here" without
	// needing the page to change, and it already arrives at the Renderer. This
	// is what lets the loop tell a quiet reader from an abandoned tab.
	lastInput time.Time
}

// Scroll delivers a wheel event to a running session.
//
// The key is whatever the caller registered the stream under; §10.1d's handler
// uses the capability signature, which is already unguessable and already
// unique per stream. Unknown keys are an error rather than a silent no-op: a
// scroll that goes nowhere looks exactly like a page that will not move, and
// the reader would keep trying.
func (r *Renderer) Scroll(key string, dx, dy float64) error {
	r.mu.Lock()
	s, ok := r.live[key]
	if ok {
		// Stamped under the same lock that found it, so the idle loop cannot
		// read a half-updated session.
		s.lastInput = time.Now()
	}
	r.mu.Unlock()
	if !ok {
		return ErrNoSession
	}
	// Dispatched at the middle of the viewport. A wheel event goes to whatever
	// is under the pointer, and the pointer is on the reader's screen, not
	// ours — the centre is the only position that reliably lands on the
	// document rather than on a sidebar that happens to sit under the corner.
	x, y := float64(s.vp.Width)/2, float64(s.vp.Height)/2
	return chromedp.Run(s.tab,
		input.DispatchMouseEvent(input.MouseWheel, x, y).
			WithDeltaX(dx).WithDeltaY(dy))
}

// register records a session and returns the handle unregister needs.
//
// # The key is not unique per stream, whatever Scroll's comment says
//
// It says the caller "uses the capability signature, which is already
// unguessable and already unique per stream". The first half is true. The second
// is not: §10.1d's handler passes `q.Get("s")`, which is the HMAC over
// (url, expiry) — deterministic, so the SAME link produces the SAME key every
// time it is used. Reloading the live view, opening it in a second tab, or an
// <img> re-establishing itself after a network blip all arrive here with a key
// that is already in the map.
//
// Last-in wins, which is right: the newest stream is the one on screen, and it
// is the one a scroll should reach.
func (r *Renderer) register(key string, tab context.Context, vp Viewport) *liveSession {
	s := &liveSession{tab: tab, vp: vp}
	r.mu.Lock()
	r.live[key] = s
	r.mu.Unlock()
	return s
}

// unregister removes a session only if it is still the registered one.
//
// A plain `delete(r.live, key)` was wrong for the reason above. Two streams on
// one capability interleave like this:
//
//	A registers   live[K] = A
//	B registers   live[K] = B      (A's entry replaced; A's tab still running)
//	A ends        delete(live, K)  ← removes B, which is the LIVE session
//	B scrolls     ErrNoSession
//
// So the reader whose view is actually on screen loses scrolling, silently and
// permanently, because a different stream finished. That is precisely the
// outcome Scroll's own comment argues against: "a scroll that goes nowhere looks
// exactly like a page that will not move, and the reader would keep trying."
//
// Comparing the pointer is enough. Sessions are heap-allocated per stream and
// never copied, so identity is exactly the question being asked.
// lastInputAt reports when the reader last acted on a session, or the zero time.
func (r *Renderer) lastInputAt(s *liveSession) time.Time {
	if s == nil {
		return time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return s.lastInput
}

func (r *Renderer) unregister(key string, s *liveSession) {
	r.mu.Lock()
	if r.live[key] == s {
		delete(r.live, key)
	}
	r.mu.Unlock()
}

// Viewport is the size a session renders at.
//
// Per-session rather than per-Renderer because the reader can widen the view,
// and a wider view wants more pixels — a 1280-wide render scaled up to fill a
// 1800-wide pane is a blurry page, and the whole point of this rung is showing
// the page as it really looks.
type Viewport struct{ Width, Height int }

// Clamp bounds a requested viewport to something sane.
//
// The request arrives from the client unsigned, which is fine because this is
// the only thing it can influence and the ceiling is what makes that safe: a
// client asking for 16000x16000 would have the browser lay out and JPEG-encode
// an enormous surface, several times a second, on a box with one reader.
func (v Viewport) Clamp(def Viewport) Viewport {
	if v.Width <= 0 {
		v.Width = def.Width
	}
	if v.Height <= 0 {
		v.Height = def.Height
	}
	v.Width = clampInt(v.Width, 320, 1920)
	v.Height = clampInt(v.Height, 240, 1200)
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (r *Renderer) Stream(ctx context.Context, key, rawURL string, vp Viewport, out chan<- Frame) error {
	if r.opt.AllowPrivate {
		if err := netguard.CheckURLPermissive(rawURL); err != nil {
			return fmt.Errorf("render: %w", err)
		}
	} else if err := netguard.CheckURL(rawURL); err != nil {
		return fmt.Errorf("render: %w", err)
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return ErrBusy
	}

	alloc, err := r.browser()
	if err != nil {
		return err
	}

	tabCtx, cancelTab := chromedp.NewContext(alloc)
	defer cancelTab()

	// The caller's cancellation reaches the TAB, not just the frame loop below.
	//
	// The loop already watches `runCtx`, which made this look handled — but the
	// navigate and screencast-start calls ahead of it run on `tabCtx`, and a
	// server that accepts the connection and never answers blocks there. The
	// reader has closed the view, their context is cancelled, and the session
	// holds the single render slot anyway, because the code that would notice is
	// downstream of the call that is stuck.
	stopTab := context.AfterFunc(ctx, cancelTab)
	defer stopTab()

	// Registered for the tab's whole life and removed on the way out, so input
	// arriving a moment after the reader closed the view is an honest
	// ErrNoSession rather than a panic on a dead context.
	vp = vp.Clamp(Viewport{Width: r.opt.Width, Height: r.opt.Height})
	sess := r.register(key, tabCtx, vp)
	defer r.unregister(key, sess)

	// The whole session is bounded. A page that never settles must not hold a
	// tab open until the process restarts.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	seq := 0
	last := time.Now()
	var mu sync.Mutex

	chromedp.ListenTarget(tabCtx, func(ev any) {
		fr, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		// Every frame must be acknowledged or the stream stops after a couple.
		// It runs in its own goroutine because this callback holds the event
		// loop and the ack is itself a round trip.
		go func() {
			_ = chromedp.Run(tabCtx, page.ScreencastFrameAck(fr.SessionID))
		}()

		// The protocol carries the image base64-encoded in a JSON string, so it
		// is decoded once here rather than by every consumer.
		img, derr := base64.StdEncoding.DecodeString(fr.Data)
		if derr != nil || len(img) == 0 {
			return
		}

		mu.Lock()
		seq++
		n := seq
		last = time.Now()
		mu.Unlock()

		select {
		case out <- Frame{JPEG: img, Seq: n}:
		case <-runCtx.Done():
		default:
			// The reader's link cannot keep up. Dropping the frame is correct:
			// these are whole images, so the next one supersedes this one
			// entirely and queueing would only add latency to a stream that is
			// already behind.
		}
	})

	if err := chromedp.Run(tabCtx,
		emulation.SetDeviceMetricsOverride(int64(vp.Width), int64(vp.Height), 1, false),
		chromedp.Navigate(rawURL),
	); err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return fmt.Errorf("render: %w", cerr)
		}
		return fmt.Errorf("render: navigate: %w", err)
	}

	if err := chromedp.Run(tabCtx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(int64(r.opt.Quality)).
			WithMaxWidth(int64(vp.Width)).
			WithMaxHeight(int64(vp.Height)).
			WithEveryNthFrame(1),
	); err != nil {
		return fmt.Errorf("render: screencast: %w", err)
	}

	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-runCtx.Done():
			return nil
		case <-tick.C:
			mu.Lock()
			idle := time.Since(last)
			mu.Unlock()
			// A scroll counts as activity, because a frame is not the only
			// evidence a reader is there — and on a static article it is
			// evidence that never arrives. Without this the timeout below
			// reclaims the session of somebody who is looking straight at it.
			if in := r.lastInputAt(sess); !in.IsZero() {
				if since := time.Since(in); since < idle {
					idle = since
				}
			}
			// Idle means "no NEW frames", which on a settled article is the
			// normal state — the reader is looking at a page that is not
			// changing. So the timeout is generous and exists to reclaim a tab
			// whose viewer wandered off, not to police a quiet page.
			if idle > r.opt.IdleTimeout {
				return nil
			}
		}
	}
}
