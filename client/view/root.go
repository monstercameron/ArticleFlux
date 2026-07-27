//go:build js && wasm

package view

import (
	"context"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// Root decides whether the page shows the reader or the login screen.
//
// It is the mount point, and Reader is now a child of it rather than the root
// itself. The reason is that a wasm client cannot know at boot whether the
// credential in local storage is still good — it may have expired, been revoked
// from another device, or been minted against a database that has since been
// restored from a backup. Only the server knows, so Root asks it.
//
// The three states below are all of them, and the middle one earns its place:
//
//	checking — WhoAmI has not answered yet
//	login    — the server said the caller is nobody
//	reader   — the server said who we are
//
// Without `checking`, a page with a good token would paint the login screen for
// a few hundred milliseconds and then replace it with the reader. That flash
// trains people to start typing a password they do not need, which is worse than
// a moment of nothing.
//
// **WhoAmI runs even when local storage holds no token, and that is the whole
// point of asking rather than assuming.** The first version short-circuited to
// the login screen whenever there was no stored credential, which is right for a
// deployed instance and wrong for `serve -dev`, where the server serves the local
// account with no login at all: it presented a password prompt for a server that
// did not want one. Whether a credential is required is a fact about the SERVER,
// so the server is what gets asked. The cost is one fast RPC before the login
// screen appears on a genuinely unauthenticated boot, which is the correct trade
// against a dev server that cannot be reached without typing a password into it.
type rootPhase int

const (
	phaseChecking rootPhase = iota
	phaseLogin
	phaseReader
)

func Root() ui.Node {
	// Always start in `checking`, and let the server decide. UseState's initial
	// value is only read on mount, which is exactly the semantics wanted here:
	// this must not be recomputed on every render from a value that Login is
	// about to change.
	//
	// LoadToken still runs first, and its ordering is load-bearing: it moves the
	// stored credential into the package where the outgoing interceptor reads it,
	// so the WhoAmI below carries it. Doing it after the dial would send that
	// first request anonymously and log a good session out on every reload.
	phase := ui.UseState(initialPhase())
	// The connection, once someone is authenticated. Login dials it and hands it
	// over so the reader does not open a second tunnel.
	authed := ui.UseRef[*data.Client](nil)

	tunnel := data.TunnelURL(platform.Origin())

	// A rejected credential anywhere in the app comes back here.
	//
	// Registered once, on mount. The interceptor in client/data clears the token
	// before calling this, so a reload lands on the login screen — see
	// platform.Reload for why the response is a reload rather than a state
	// change.
	installed := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if installed.Get() {
			return nil
		}
		installed.Set(true)
		data.SetUnauthenticatedHandler(func() {
			ui.PostAsync(func() { platform.Reload() })
		})
		return nil
	}, []any{})

	// Validate a stored token exactly once, on mount, and only when there is one.
	//
	// The Ref guard rather than a dependency list is the same lesson reader.go
	// records at length: a dependency list that looks like "run when X appears"
	// re-runs on renders where X has not changed, and a WhoAmI per render is a
	// round trip per keystroke.
	checked := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if checked.Get() || phase.Get() != phaseChecking {
			return nil
		}
		checked.Set(true)

		go func() {
			c, err := data.Dial(context.Background(), tunnel, nil)
			if err != nil {
				// Dial only fails on a target gRPC cannot parse, which is a build
				// problem rather than an unreachable server. Nothing to retry.
				ui.PostAsync(func() { phase.Set(phaseLogin) })
				return
			}
			_, err = c.WhoAmI(context.Background())
			// The interface language is restored HERE, before the reader is
			// mounted, and that placement is the whole design.
			//
			// i18n.T is a plain function reading a package-level catalog (see
			// client/i18n), so a component that has already rendered does not
			// re-render when the catalog changes. Loading a translation after
			// the reader mounts would leave the shell in English and only the
			// bits that happened to re-render in French — visibly worse than
			// not translating at all. Mounting after the catalog is in place
			// makes the first paint the right language.
			//
			// It is deliberately best-effort and non-blocking on failure: a
			// server that cannot translate today must still hand the reader
			// their feeds, in English, rather than a splash screen.
			if err == nil {
				restoreLocale(c)
			}
			ui.PostAsync(func() {
				if err != nil {
					// Two different failures land here and they are deliberately
					// treated the same: a rejected token, and a server that is
					// not answering. Showing the login screen is right for the
					// first and harmless for the second — the reader tries to
					// sign in, sees "can't reach the server", and knows more than
					// a spinner would have told them. The token is NOT cleared on
					// a transport failure (the interceptor only clears on
					// Unauthenticated), so a reload once the server is back goes
					// straight through.
					_ = c.Close()
					phase.Set(phaseLogin)
					return
				}
				authed.Set(c)
				phase.Set(phaseReader)
			})
		}()
		return nil
	}, []any{})

	// Every hook above this line runs on every render, unconditionally. The
	// branch is here, at the end, and only chooses which child to mount — GWC
	// matches hooks positionally, so a conditional hook binds to the wrong slot,
	// and a child component mounted conditionally gets its own slot sequence and
	// is safe.
	switch phase.Get() {
	case phaseReader:
		return ui.CreateElement(Reader, readerProps{client: authed.Get()})
	case phaseLogin:
		return ui.CreateElement(Login, loginProps{
			tunnel: tunnel,
			onSuccess: func(c *data.Client) {
				authed.Set(c)
				phase.Set(phaseReader)
			},
		})
	default:
		return bootSplash()
	}
}

// localeKey is where the chosen interface language is remembered on this
// device.
//
// localStorage rather than server prefs, and that is not laziness. The language
// has to be known BEFORE the reader mounts, and reading it from the server
// would put a round trip in front of the first paint for every reader —
// including the overwhelming majority who never leave English. A device-local
// value costs nothing to read and is the right scope anyway: which language you
// read this app in is a property of the machine you are sitting at, like the
// theme.
//
// It is written by the language picker, immediately before it reloads.
const localeKey = "articleflux.locale"

// restoreLocale loads the saved interface language, blocking until it is in
// place or has failed.
//
// Synchronous inside the boot goroutine on purpose: its caller has not mounted
// the reader yet, and the entire point is that the catalog is complete before
// the first component reads it. i18n.T is a plain function over a package-level
// catalog, so a component that has already rendered will NOT pick up a
// translation that lands later — loading it after the mount would leave the
// shell in English with only the re-rendered parts translated.
//
// Every failure is swallowed to English. A translation that cannot be fetched
// is a cosmetic loss; a reader who cannot see their feeds because a language
// model was unavailable is the app being broken by an optional feature.
func restoreLocale(c *data.Client) {
	want := platform.LocalGet(localeKey)
	if want == "" || want == i18n.DefaultLocale {
		return
	}
	// force=false, so this is a server-side cache read on every boot after the
	// first. A cold cache — the English changed in a new build — pays for one
	// re-translation and is then warm for every other device too.
	if _, err := c.TranslateUI(context.Background(), want, false); err != nil {
		return
	}
	i18n.SetLocale(want)
	// The document's own language and direction, which are not decoration: they
	// decide hyphenation, quotation marks, the spellchecker, and which way the
	// logical properties the whole sheet is written in actually point.
	platform.SetRootAttr("lang", want)
	platform.SetRootAttr("dir", string(i18n.Direction()))
}

// initialPhase loads the stored credential and hands the decision to the server.
//
// It always returns phaseChecking. The return value is kept rather than inlined
// because the ORDER matters and is easy to lose: LoadToken must run before the
// first RPC, or that RPC goes out anonymously.
func initialPhase() rootPhase {
	data.LoadToken()
	return phaseChecking
}

// bootSplash is what shows while the stored token is being validated.
//
// Deliberately the same wordmark on the same ground as web/index.html's pre-wasm
// state, with no spinner and no "Loading…" that would make a 200ms check look
// like a stall. From the reader's side the page simply has not finished arriving
// yet, which is true.
// data-phase, because the splash and the login screen share `.login-card` and
// are otherwise indistinguishable from outside. That is not a hypothetical
// tidiness point: it made a check for "is the login screen up?" match the splash
// mid-check and report the wrong answer. One attribute names the state outright.
func bootSplash() ui.Node {
	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "checking"}},
		html.Div(html.Props{Class: "login-card login-splash"},
			html.Div(html.Props{Class: "login-mark"}, html.Text(i18n.T("login.mark"))),
		),
	)
}
