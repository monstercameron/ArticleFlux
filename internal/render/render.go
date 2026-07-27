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
// # What this deliberately is not
//
// Not interactive. There is no input channel here, and adding one is a separate
// decision with its own consent story (§10.1d, R22). This package answers "show
// me what that page looks like", and a reader who needs to click something is
// told to open the real site.
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
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

var (
	// ErrNoBrowser is returned when no Chromium-family browser could be found.
	ErrNoBrowser = errors.New("render: no chromium-family browser found")
	// ErrBusy is returned when every render slot is taken.
	ErrBusy = errors.New("render: another page is already streaming")
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
}

// Renderer owns the browser.
type Renderer struct {
	opt  Options
	sem  chan struct{}
	once sync.Once
	// alloc is the shared browser process. Created on first use rather than at
	// boot: an instance where nobody opens a live view should never pay for a
	// browser, and on a small box that is 300 MB of not paying for it.
	alloc       context.Context
	allocCancel context.CancelFunc
	allocErr    error
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
	return &Renderer{opt: opt, sem: make(chan struct{}, opt.MaxSessions)}
}

// Available reports whether a browser could be found, without starting one.
func (r *Renderer) Available() bool { return FindBrowser(r.opt.ExecPath) != "" }

// Close shuts the browser down.
func (r *Renderer) Close() {
	if r.allocCancel != nil {
		r.allocCancel()
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

// browser starts the shared process once.
func (r *Renderer) browser() (context.Context, error) {
	r.once.Do(func() {
		exec := FindBrowser(r.opt.ExecPath)
		if exec == "" {
			r.allocErr = ErrNoBrowser
			return
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
	})
	if r.allocErr != nil {
		return nil, r.allocErr
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
func (r *Renderer) Stream(ctx context.Context, rawURL string, out chan<- Frame) error {
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
		emulation.SetDeviceMetricsOverride(int64(r.opt.Width), int64(r.opt.Height), 1, false),
		chromedp.Navigate(rawURL),
	); err != nil {
		return fmt.Errorf("render: navigate: %w", err)
	}

	if err := chromedp.Run(tabCtx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(int64(r.opt.Quality)).
			WithMaxWidth(int64(r.opt.Width)).
			WithMaxHeight(int64(r.opt.Height)).
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
