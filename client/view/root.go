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
	// The saved view, fetched during `checking` and handed to the reader as a
	// prop. A Ref rather than state: it is read once, when Reader mounts, and a
	// second render of Root is not a reason to re-read it.
	//
	// Nil is meaningful and is not the same as empty: nil means "not fetched"
	// (a fresh login, or a preferences call that failed), and the reader falls
	// back to its own defaults. An empty non-nil map means the server has no
	// preferences for this account, which is a first-ever boot and looks
	// identical from the reader's side — the distinction only matters here.
	saved := ui.UseRef[map[string]string](nil)
	// catalogReady exists only to force one re-render after a translated catalog
	// lands. The locale has not changed at that point — it was already set — so
	// nothing else would invalidate the tree, and the reader would sit looking
	// at English until they touched something.
	catalogReady := ui.UseState(0)

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
			// The saved view, fetched HERE rather than after the reader mounts.
			//
			// It used to be the reader's own first effect, which meant the reader
			// mounted with its defaults — the All stream, an expanded rail, the
			// house theme, default pane widths — painted them, and then snapped
			// into the saved state a round trip later. Cam saw exactly that: "it
			// is instantly the default view and then flashes to the past state."
			//
			// A30 says where you were is account state. State that has to be
			// fetched cannot be a component's initial value unless something
			// holds the screen while it arrives, and the splash is already up and
			// already holding it for WhoAmI. So this rides along inside the same
			// wait: one more round trip against a check that is already
			// happening, and the reader's first frame is the right one instead of
			// the second one.
			//
			// Errors are swallowed on purpose. Losing the saved place is a small
			// regression; refusing to show the reader because a preferences call
			// failed is the app not working. A nil map reads as "no preferences",
			// which is exactly what a first-ever boot looks like.
			var prefs map[string]string
			if err == nil {
				if p, perr := c.GetPrefs(context.Background()); perr == nil {
					prefs = p
					// Applied before the phase flips, so the very first painted
					// frame of the reader is already themed and already the right
					// width. These are custom properties on documentElement, not
					// component state — applying them costs no render, and doing
					// it a render later is a visible repaint of the whole app.
					applyBootAppearance(p)
				}
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
				saved.Set(prefs)
				phase.Set(phaseReader)
			})
		}()
		return nil
	}, []any{})

	// The interface language.
	//
	// GWC's own hook does the whole job: reads localStorage under
	// i18n.PersistenceKey, clamps to the supported set, writes back on change,
	// and — because it is built on ui.UseState — re-renders when Set is called.
	// That last property is why switching language is a re-render and not a
	// page reload.
	//
	// DetectBrowser is off. Guessing from navigator.language would put a reader
	// whose browser is set to French into a French UI on first boot, which
	// costs a Smart+ translation nobody asked for and bills the instance. The
	// language is a choice someone makes, once, on the Smart+ tab.
	locale := i18n.UseLocale(i18n.LocaleOptions{
		InitialLocale:    i18n.DefaultLocale,
		FallbackLocale:   i18n.DefaultLocale,
		SupportedLocales: i18n.SupportedLocales(),
		PersistenceKey:   i18n.PersistenceKey,
	})

	// A saved non-English locale needs its catalog fetched before anything below
	// renders in it. Guarded by a Ref rather than a dep list, for the reason the
	// WhoAmI effect above records; re-runs when the locale changes because that
	// is exactly when a different catalog is wanted.
	//
	// Best-effort: a server that cannot translate today must still hand the
	// reader their feeds, in English, rather than a splash screen. Import is
	// idempotent, and a locale whose catalog never arrives simply falls back to
	// English key by key.
	fetched := ui.UseRef("")
	ui.UseEffect(func() func() {
		want := locale.Get()
		c := authed.Get()
		if c == nil || want == "" || want == i18n.DefaultLocale || fetched.Get() == want {
			return nil
		}
		fetched.Set(want)
		go func() {
			// force=false, so this is a server-side cache read on every boot
			// after the first. A cold cache — the English changed in a new
			// build — pays for one re-translation and is then warm for every
			// other device too.
			_, _ = c.TranslateUI(context.Background(), want, false)
			// PostAsync so the re-render happens on the frame loop with the
			// catalog already in the bundle. Setting the locale to what it
			// already is would not re-render, so this nudges the ref instead.
			ui.PostAsync(func() { catalogReady.Set(catalogReady.Get() + 1) })
		}()
		return nil
	}, []any{locale.Get(), authed.Get() != nil})

	// The document's own language and direction. Not decoration: they decide
	// hyphenation, quotation marks, the spellchecker, and which way the logical
	// properties the whole sheet is written in actually point.
	ui.UseEffect(func() func() {
		platform.SetRootAttr("lang", locale.Get())
		platform.SetRootAttr("dir", string(i18n.DirectionFor(locale.Get())))
		// And the splash's words, for the next load — see mirrorBootCopy. It
		// reads through i18n.At rather than a Runtime, because an effect body
		// runs outside the render and UseI18n is a hook.
		mirrorBootCopy(i18n.At(locale.Get()))
		return nil
	}, []any{locale.Get()})

	// Every hook above this line runs on every render, unconditionally. The
	// branch is below, inside the Provider's child, and only chooses which
	// component to mount — GWC matches hooks positionally, so a conditional hook
	// binds to the wrong slot, and a child mounted conditionally gets its own
	// slot sequence and is safe.
	var child ui.Node
	switch phase.Get() {
	case phaseReader:
		child = ui.CreateElement(Reader, readerProps{client: authed.Get(), prefs: saved.Get()})
	case phaseLogin:
		child = ui.CreateElement(Login, loginProps{
			tunnel: tunnel,
			onSuccess: func(c *data.Client) {
				authed.Set(c)
				// The same wait as the boot path, for the same reason — a reader
				// who signs in on a second machine should land in the view they
				// left, not watch it assemble itself. The login card stays up for
				// one more round trip, which is the cheapest place in the whole
				// session to spend one: they are already watching a button they
				// just pressed.
				go func() {
					p, err := c.GetPrefs(context.Background())
					if err == nil {
						applyBootAppearance(p)
					}
					ui.PostAsync(func() {
						saved.Set(p)
						phase.Set(phaseReader)
					})
				}()
			},
		})
	default:
		child = ui.CreateElement(bootSplash)
	}

	// **The Provider is mounted HERE, in Root, and that placement is load-bearing.**
	//
	// When a Provider fiber re-renders, the reconciler compares the old and new
	// context values and, if they differ, calls markSubtreeNeedsUpdate on the
	// whole subtree — which is checked BEFORE any props comparison, so it
	// defeats every memo bailout below it. i18n.Runtime is a struct of func
	// fields, so it is not Comparable and the comparison falls through to
	// reflect.DeepEqual, where two non-nil funcs are NEVER equal. The value
	// therefore reads as "changed" on every single render of this component.
	//
	// That is exactly what we want on a language switch and a disaster anywhere
	// else, so it goes in the component that re-renders least: Root has two
	// pieces of state, `phase` and the locale, and both are events the entire
	// screen should redraw for. Mounting it inside Reader instead would mark the
	// 151-row rail and the virtualised 3,600-item list dirty on every keystroke.
	return i18n.Provider(i18n.ProviderProps{
		Locale: locale,
		Bundle: i18n.Bundle(),
		Child:  child,
	})
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
// A component, not a helper called inline. Root builds `child` BEFORE handing it
// to the Provider, so anything invoked at that point runs outside the context
// and would read an empty catalog. Mounting it as an element defers the call to
// reconciliation, under the Provider, where UseI18n resolves.
func bootSplash() ui.Node {
	tr := i18n.UseI18n()
	return html.Div(html.Props{Class: "login", Data: map[string]string{"phase": "checking"}},
		html.Div(html.Props{Class: "login-card login-splash"},
			html.Div(html.Props{Class: "login-mark"}, html.Text(tr.T("login", "mark"))),
		),
	)
}
