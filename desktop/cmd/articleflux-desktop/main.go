//go:build windows

// Command articleflux-desktop is ArticleFlux as a Windows app: the real server,
// in this process, on a loopback port, shown in a native WebView2 window.
//
// See docs/DESKTOP.md for the architecture and plan.md A45 (§4a) for the
// decision. In short: everything with logic in it is in ../../host, which has
// no window toolkit and is tested headless; this file is the wiring between
// that and Wails — the native window engine GoWebComponents v7.0.8's desktop
// support is built on, at the revision GWC pins.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/monstercameron/ArticleFlux/desktop/host"
	"github.com/monstercameron/ArticleFlux/internal/buildver"
)

func main() {
	if err := run(); err != nil {
		fatal(err)
		os.Exit(1)
	}
}

func run() error {
	dataFlag := flag.String("data", "", "data folder (default %LOCALAPPDATA%\\ArticleFlux)")
	webFlag := flag.String("web", "", "assembled web root (default: the web folder beside this program)")
	poll := flag.Duration("poll", 0, "how often to poll feeds while open; 0 means the default (15m), negative disables")
	devtools := flag.Bool("devtools", false, "allow the WebView2 developer tools (F12)")
	flag.Parse()

	dir, err := host.ResolveDataDir(*dataFlag)
	if err != nil {
		return &host.StartError{Title: "ArticleFlux could not find its data folder", Err: err}
	}
	paths := host.PathsIn(dir)
	if err := paths.Ensure(); err != nil {
		return &host.StartError{Title: "ArticleFlux could not create its data folder", Err: err}
	}
	web := *webFlag
	if web == "" {
		exe, err := os.Executable()
		if err != nil {
			return &host.StartError{Title: "ArticleFlux could not find its own files", Err: err}
		}
		web = host.WebRootBeside(exe)
	}

	sh := &shell{paths: paths, web: web}

	// Wails FIRST, before the database: its single-instance check runs inside
	// application.New and exits a second copy after asking the first to come to
	// the front — which is what a person double-clicking the icon again wants.
	// The ID includes the data folder, so a second copy pointed at a different
	// one (-data, or a test's temporary LOCALAPPDATA) is a different app.
	sum := sha256.Sum256([]byte(strings.ToLower(paths.Dir)))
	sh.wails = application.New(application.Options{
		Name:        "ArticleFlux",
		Description: "A feed reader",
		Icon:        readIcon(web),
		Assets: application.AssetOptions{
			// The window never loads wails.localhost — it loads the reader's own
			// loopback origin — so the asset server has nothing to serve.
			Handler: http.NotFoundHandler(),
		},
		Windows: application.WindowsOptions{
			WebviewUserDataPath: paths.WebView,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "articleflux.desktop." + hex.EncodeToString(sum[:8]),
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { sh.showWindow() },
		},
		RawMessageHandler: sh.onMessage,
		OnShutdown:        sh.shutdown,
	})

	// The belt to Wails' braces, and the one that is tested (host/lock_test.go):
	// Wails' check is a named mutex keyed on our ID; this is a lock on the data
	// folder itself, which is what two copies must not share.
	lock, err := host.AcquireLock(paths.Lock)
	if err != nil {
		if errors.Is(err, host.ErrLocked) {
			return &host.StartError{Title: "ArticleFlux is already open", Err: err}
		}
		return &host.StartError{Title: "ArticleFlux could not start", Err: err}
	}
	sh.lock = lock

	// No deadline: a first launch against a large database can spend a while in
	// migrations, and abandoning one part-way is the one outcome worse than
	// waiting for it.
	srv, err := host.Start(context.Background(), host.Options{
		Paths:        paths,
		WebRoot:      web,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, nil)),
		PollInterval: *poll,
	})
	if err != nil {
		_ = lock.Release()
		return err
	}
	sh.srv = srv

	sh.buildWindow(*devtools)
	sh.buildMenus()

	if err := sh.wails.Run(); err != nil {
		sh.shutdown()
		return err
	}
	sh.shutdown()
	return nil
}

// shell holds the pieces the callbacks need.
type shell struct {
	paths host.Paths
	web   string
	wails *application.App
	srv   *host.Server
	lock  *host.Lock
	win   *application.WebviewWindow

	mu         sync.Mutex
	lastNormal application.Rect
	haveNormal bool
	saved      bool

	once sync.Once
}

func (sh *shell) buildWindow(devtools bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	saved, ok := sh.srv.Window(ctx)
	cancel()

	opts := application.WebviewWindowOptions{
		Name:      "main",
		Title:     "ArticleFlux",
		URL:       sh.srv.LaunchURL(),
		Width:     host.DefaultBounds.Width,
		Height:    host.DefaultBounds.Height,
		MinWidth:  host.MinWidth,
		MinHeight: host.MinHeight,
		// The client paints its own background on first frame; this is the
		// colour of the half-second before the wasm module has, matched to
		// the reader's default paper so a launch does not flash white.
		BackgroundColour:   application.NewRGB(0xF6, 0xF1, 0xE7),
		DevToolsEnabled:    devtools,
		ZoomControlEnabled: true,
		// Wails grants EVERY permission without asking when no policy is given
		// (SetGlobalPermission(Allow)). The reader asks for none of these, and
		// its origin also serves proxied publisher pages (§10.1b), so the window
		// must not hand a camera to whatever one of them requests. Naming any
		// kind turns the blanket grant off; the rest fall back to WebView2's
		// own prompt.
		Permissions: map[application.PermissionType]application.Permission{
			application.PermissionMicrophone:  application.PermissionDeny,
			application.PermissionCamera:      application.PermissionDeny,
			application.PermissionGeolocation: application.PermissionDeny,
		},
		Windows: application.WindowsWindow{
			// Autofill would offer to save the "password" of every text box in
			// Settings; the reader has no password to save.
			GeneralAutofillEnabled:  false,
			PasswordAutosaveEnabled: false,
		},
	}
	if ok && saved.Visible(sh.screens()) {
		opts.Width, opts.Height = saved.Width, saved.Height
		opts.InitialPosition = application.WindowXY
		opts.X, opts.Y = saved.X, saved.Y
		if saved.Maximised {
			opts.StartState = application.WindowStateMaximised
		}
		sh.lastNormal = application.Rect{X: saved.X, Y: saved.Y, Width: saved.Width, Height: saved.Height}
		sh.haveNormal = true
	} else if ok {
		// Remembered, but somewhere no screen is any more: keep the size and let
		// the window centre itself.
		opts.Width, opts.Height = saved.Width, saved.Height
	}
	sh.win = sh.wails.Window.NewWithOptions(opts)

	// After every navigation, the shell's page script (host/shim.js).
	//
	// The "runtime ready" message is sent on the page's behalf. Wails queues
	// ExecJS until the page's own @wailsio/runtime module announces itself, and
	// this page — the reader, served from its own origin — never loads that
	// module; the core Wails injects at navigation is all it has. Without this
	// line every ExecJS below would wait forever, silently.
	shim := host.Shim(sh.srv.Origin())
	sh.win.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		sh.win.HandleMessage("wails:runtime:ready")
		sh.win.ExecJS(shim)
	})

	// Remember the last NORMAL bounds: a maximised window's bounds are the
	// screen's, and restoring to them would open the next launch maximised
	// with no size to un-maximise to.
	track := func(*application.WindowEvent) {
		if sh.win.IsMaximised() || sh.win.IsMinimised() {
			return
		}
		b := sh.win.Bounds()
		sh.mu.Lock()
		sh.lastNormal, sh.haveNormal = b, true
		sh.mu.Unlock()
	}
	sh.win.OnWindowEvent(events.Common.WindowDidMove, track)
	sh.win.OnWindowEvent(events.Common.WindowDidResize, track)
	sh.win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { sh.saveWindow() })
}

func (sh *shell) screens() []host.Rect {
	var out []host.Rect
	for _, s := range sh.wails.Screen.GetAll() {
		if s == nil {
			continue
		}
		out = append(out, host.Rect{X: s.WorkArea.X, Y: s.WorkArea.Y, Width: s.WorkArea.Width, Height: s.WorkArea.Height})
	}
	return out
}

// saveWindow runs once: on WindowClosing when the window is closed, or on
// shutdown when the app is quit from a menu. The first caller still has a live
// window to ask whether it is maximised; a second, after the window is gone,
// would overwrite a good answer with a destroyed window's zeroes.
func (sh *shell) saveWindow() {
	if sh.win == nil || sh.srv == nil {
		return
	}
	sh.mu.Lock()
	if sh.saved {
		sh.mu.Unlock()
		return
	}
	sh.saved = true
	b, have := sh.lastNormal, sh.haveNormal
	sh.mu.Unlock()
	if !have {
		b = sh.win.Bounds()
	}
	saved := host.Bounds{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height, Maximised: sh.win.IsMaximised()}
	if _, ok := host.ParseBounds(saved.String()); !ok {
		return // nothing plausible to remember; next launch uses the default
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sh.srv.SaveWindow(ctx, saved); err != nil {
		sh.srv.Log().Warn("desktop: could not remember the window position", "err", err)
	}
}

func (sh *shell) buildMenus() {
	m := sh.wails.Menu.New()

	file := m.AddSubmenu("File")
	file.Add("Add feed…").SetAccelerator("CmdOrCtrl+N").OnClick(func(*application.Context) { sh.command(host.CmdAddFeed) })
	file.AddSeparator()
	file.Add("Import OPML…").OnClick(func(*application.Context) { go sh.importOPML() })
	file.Add("Export OPML…").OnClick(func(*application.Context) { go sh.exportOPML() })
	file.AddSeparator()
	file.Add("Quit").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) { sh.wails.Quit() })

	view := m.AddSubmenu("View")
	view.Add("Refresh feeds").OnClick(func(*application.Context) { sh.command(host.CmdRefresh) })
	view.Add("Settings").SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) { sh.command(host.CmdSettings) })
	view.AddSeparator()
	view.Add("Reload window").OnClick(func(*application.Context) { sh.win.Reload() })

	help := m.AddSubmenu("Help")
	help.Add("Open data folder").OnClick(func(*application.Context) { _ = sh.wails.Browser.OpenFile(sh.paths.Dir) })
	help.Add("About ArticleFlux").OnClick(func(*application.Context) {
		sh.wails.Dialog.Info().
			SetTitle("About ArticleFlux").
			SetMessage(fmt.Sprintf("ArticleFlux %s\n\nYour reading is stored in\n%s", buildver.Version, sh.paths.DB)).
			Show()
	})

	sh.wails.Menu.Set(m)
	sh.win.SetMenu(m)

	// The tray: the three things worth doing without the window in front.
	tm := sh.wails.Menu.New()
	tm.Add("Show ArticleFlux").OnClick(func(*application.Context) { sh.showWindow() })
	tm.Add("Refresh feeds").OnClick(func(*application.Context) { sh.command(host.CmdRefresh) })
	tm.AddSeparator()
	tm.Add("Quit").OnClick(func(*application.Context) { sh.wails.Quit() })
	tray := sh.wails.SystemTray.New()
	if icon := readIcon(sh.web); icon != nil {
		tray.SetIcon(icon)
	}
	tray.SetTooltip("ArticleFlux")
	tray.SetMenu(tm)
	tray.OnClick(sh.showWindow)
}

func (sh *shell) showWindow() {
	if sh.win == nil {
		return
	}
	if sh.win.IsMinimised() {
		sh.win.UnMinimise()
	}
	sh.win.Show()
	sh.win.Focus()
}

func (sh *shell) command(c host.Command) {
	sh.showWindow()
	sh.win.ExecJS(c.Script())
}

// onMessage receives the shim's posts. host.ParseMessage is the policy: it
// drops anything not from the reader's own origin and anything it cannot
// validate.
func (sh *shell) onMessage(w application.Window, raw string, info *application.OriginInfo) {
	sender := ""
	if info != nil {
		sender = info.Origin
	}
	msg := host.ParseMessage(sh.srv.Origin(), sender, raw)
	switch msg.Kind {
	case host.MsgOpenExternal:
		if err := sh.wails.Browser.OpenURL(msg.Value); err != nil {
			sh.srv.Log().Warn("desktop: could not open the link in the browser", "err", err)
		}
	case host.MsgTitle:
		w.SetTitle(msg.Value)
	}
}

func (sh *shell) importOPML() {
	path, err := sh.wails.Dialog.OpenFile().
		SetTitle("Import subscriptions").
		AddFilter("OPML subscription lists", "*.opml;*.xml").
		AddFilter("All files", "*.*").
		AttachToWindow(sh.win).
		PromptForSingleSelection()
	if err != nil || path == "" {
		return // cancelled
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := sh.srv.ImportOPML(ctx, path)
	if err != nil {
		sh.wails.Dialog.Error().SetTitle("Import failed").SetMessage(err.Error()).AttachToWindow(sh.win).Show()
		return
	}
	msg := fmt.Sprintf("Subscribed to %d feeds (%d already in your list).", res.Subscribed, res.AlreadySubscribed)
	if n := res.SkipCount(); n > 0 {
		msg += fmt.Sprintf("\n%d entries could not be imported.", n)
	}
	sh.wails.Dialog.Info().SetTitle("Import finished").SetMessage(msg).AttachToWindow(sh.win).Show()
	// The client learns about subscriptions by asking for them; the import
	// happened beside it rather than through it, so it asks again.
	sh.win.Reload()
}

func (sh *shell) exportOPML() {
	path, err := sh.wails.Dialog.SaveFile().
		SetFilename("articleflux-subscriptions.opml").
		AddFilter("OPML subscription lists", "*.opml").
		AttachToWindow(sh.win).
		PromptForSingleSelection()
	if err != nil || path == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	n, err := sh.srv.ExportOPML(ctx, path)
	if err != nil {
		sh.wails.Dialog.Error().SetTitle("Export failed").SetMessage(err.Error()).AttachToWindow(sh.win).Show()
		return
	}
	sh.wails.Dialog.Info().SetTitle("Export finished").
		SetMessage(fmt.Sprintf("Wrote %d feeds to\n%s", n, path)).AttachToWindow(sh.win).Show()
}

// shutdown is Wails' OnShutdown and also runs after Run returns; whichever
// comes first does the work. The window position first, while the database is
// still open; then the server, which checkpoints the WAL on its way out; then
// the folder lock, last, so a new copy cannot start until this one has let go
// of the file.
func (sh *shell) shutdown() {
	sh.once.Do(func() {
		if sh.srv != nil {
			sh.saveWindow()
			if err := sh.srv.Close(); err != nil {
				sh.srv.Log().Error("desktop: shutdown", "err", err)
			}
		}
		_ = sh.lock.Release()
	})
}

// readIcon loads the app icon from the web root. Not embedded: the bytes are
// the client's own (web/icons), already shipped beside the executable.
func readIcon(web string) []byte {
	b, err := os.ReadFile(filepath.Join(web, "icons", "icon-192.png"))
	if err != nil {
		return nil
	}
	return b
}
