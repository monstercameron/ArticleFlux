//go:build js && wasm

package view

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	// phaseSetup is a server with no accounts at all: there is nothing to log
	// in to yet, so a password prompt would be a door with no room behind it.
	phaseSetup
	phaseReader
	// phaseHome is the front door at `/home` (see client/view/home.go).
	//
	// It is decided from the ADDRESS, before WhoAmI, and that ordering is the
	// whole point: a visitor who has never had an account should not wait on a
	// round trip that can only answer "nobody" before they are allowed to read
	// what this is. It is also why the page has no signed-in variant — it is a
	// document, and a signed-in reader who typed the address wanted the document.
	phaseHome
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
	// Who the WhoAmI above answered with, kept for the Account tab. A Ref beside
	// `saved` and for its reasons: read once when Reader mounts, and this is the
	// only place in the app that already knows the answer.
	whoami := ui.UseRef("")
	// Whether that identity is an owner, for the one decision the client makes
	// with it: whether this device may ask the server to translate the rest of a
	// language (a spend), or only read what is already cached. A Ref beside
	// whoami, for its reasons.
	owner := ui.UseRef(false)
	// catalogReady exists only to force one re-render after a translated catalog
	// lands. The locale has not changed at that point — it was already set — so
	// nothing else would invalidate the tree, and the reader would sit looking
	// at English until they touched something.
	catalogReady := ui.UseState(0)

	// The reset token, read from the address ONCE.
	//
	// A Ref rather than a value recomputed in the body, because the body reads
	// the browser on every render and the address is about to change: the effect
	// below strips the token out of it the moment it has been captured, so the
	// second render would find nothing and hand Login an empty string mid-typing.
	resetToken := ui.UseRef("")
	resetRead := ui.UseRef(false)
	if !resetRead.Get() {
		resetRead.Set(true)
		resetToken.Set(resetTokenFrom(platform.BasePath(), platform.Path(), platform.Query()))
	}
	// And out of the address bar, once it is held.
	//
	// A single-use credential in a URL survives in history, in a screenshot of
	// the window, and in whatever the reader pastes when they ask somebody for
	// help — and it survives there AFTER it has been spent, which is when it
	// stops being obviously secret and starts being obviously shareable.
	// Replace rather than push, so Back does not walk into the stripped address
	// and then the token again.
	ui.UseEffect(func() func() {
		if resetToken.Get() != "" {
			platform.ReplacePath(platform.BasePath())
		}
		return nil
	}, []any{})

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
			who, err := whoAmIAnswered(c)
			// An unclaimed instance answers WhoAmI without an error and without
			// an identity: nobody is signed in because nobody exists yet. That
			// is setup, not login — and not the reader either, since there is no
			// account whose preferences the fetch below could return.
			if err == nil && who.GetNeedsSetup() {
				ui.PostAsync(func() {
					authed.Set(c)
					phase.Set(phaseSetup)
				})
				return
			}
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
					// Only an AUTHENTICATION answer gets here (whoAmIAnswered
					// retries everything else): the server said this caller is
					// nobody, so the login screen is the truth.
					_ = c.Close()
					phase.Set(phaseLogin)
					return
				}
				authed.Set(c)
				saved.Set(prefs)
				// The identity this whole effect already asked for. Unreachable
				// unless WhoAmI answered — the error path above returns — and an
				// empty name is what the Account tab already handles anyway.
				whoami.Set(who.GetUsername())
				owner.Set(isOwnerRole(who.GetRole()))
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

	// The language in force, filled in on the fly (plan.md §22.16c).
	//
	// It used to be one blocking whole-catalog call, and two things were wrong
	// with that. The reader looked at nothing in their language until every
	// string had come back, and a member could not get even the cached half of
	// it, because that call is owner-only. Now English is on screen at once,
	// each cached batch replaces its keys as it lands, and only an owner's device
	// asks the server to translate what is still missing — a spend against the
	// instance, which is the owner's decision.
	//
	// Root runs the loop, not the language picker, because Root is the only
	// component that can make a merge VISIBLE: a batch changes no state the
	// reconciler can see, so the catalogReady nudge below is what re-renders the
	// Provider and with it every memoised pane. The picker reads where the loop
	// has got to from `fill`, so there is one loop per language and never two
	// racing each other for the same keys.
	//
	// Guarded by a Ref rather than trusting the dep list, for the WhoAmI effect's
	// reason; the previous loop is cancelled when the language changes, so a
	// reader flicking between three languages does not leave three running.
	fill := ui.UseState(langFill{})
	fillFor := ui.UseRef("")
	fillStop := ui.UseRef[context.CancelFunc](nil)
	ui.UseEffect(func() func() {
		want := locale.Get()
		c := authed.Get()
		key := want + "|" + strconv.FormatBool(owner.Get())
		if c == nil || fillFor.Get() == key {
			return nil
		}
		fillFor.Set(key)
		if stop := fillStop.Get(); stop != nil {
			stop()
			fillStop.Set(nil)
		}
		if want == "" || want == i18n.DefaultLocale {
			fill.Set(langFill{})
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		fillStop.Set(cancel)
		mine := owner.Get()
		done, total := langCoverage(want)
		// On the next frame, not in this effect.
		//
		// This effect first runs in the commit that MOUNTS the reader — `authed`
		// going non-nil is what both of them wait for. A Set here re-renders
		// Root in the middle of that commit's effect phase, and the reader's own
		// mount effects — the ones that open the stream and ask for the feeds —
		// never ran: no request left the page, the badge said "connecting"
		// until the tab was closed, and nothing was logged, because nothing had
		// failed. It only ever happened in a language other than English,
		// because for English the Set above is a Set to the value already held
		// and re-renders nothing. Every reader who chose a language met it on
		// their next load.
		ui.PostAsync(func() {
			if ctx.Err() != nil {
				return
			}
			fill.Set(langFill{locale: want, done: done, total: total, running: true, owner: mine})
		})
		go func() {
			res, err := c.FillUIStrings(ctx, want, mine, func(int) {
				// PostAsync so the re-render happens on the frame loop with the
				// batch already in the bundle.
				ui.PostAsync(func() {
					if ctx.Err() != nil {
						return
					}
					catalogReady.Set(catalogReady.Get() + 1)
					cur := fill.Get()
					cur.done, cur.total = langCoverage(want)
					fill.Set(cur)
				})
			})
			ui.PostAsync(func() {
				// A loop cancelled by a newer language reports nothing: the
				// state already describes the language that replaced it.
				if ctx.Err() != nil && fill.Get().locale != want {
					return
				}
				d, t := langCoverage(want)
				fill.Set(langFill{locale: want, done: d, total: t, owner: mine,
					idle: res.IdleReason, failed: err != nil && ctx.Err() == nil})
			})
		}()
		return nil
	}, []any{locale.Get(), authed.Get() != nil, owner.Get()})

	// The document's own language and direction. Not decoration: they decide
	// hyphenation, quotation marks, the spellchecker, and which way the logical
	// properties the whole sheet is written in actually point.
	ui.UseEffect(func() func() {
		// lang describes the words on screen; dir describes the layout. They part
		// company on a language that has been chosen and not yet translated (no
		// cache, no key): every string is the English fallback, so claiming
		// French makes a screen reader pronounce English with French rules and
		// the browser offer to "translate" a page it believes is already in the
		// reader's language — while the layout really is mirrored, so dir stays
		// the locale's. One attribute on the root is enough: a catalog that is
		// partly filled reads as the chosen language, which is most of the text.
		// A DOM attribute, not a State.Set, so it is safe inside an effect (see
		// the fill loop above).
		platform.SetRootAttr("lang", shownLang(locale.Get()))
		platform.SetRootAttr("dir", string(i18n.DirectionFor(locale.Get())))
		// And whether some of those words are still English in a layout that
		// reads right to left, which is what design/bidi.go keys on: it makes
		// each run take its direction from its own letters, so the fallback is
		// not turned inside out by the mirrored document around it.
		platform.SetRootAttr("data-fallback", strconv.FormatBool(fallbackBidi(locale.Get())))
		// And the splash's words, for the next load — see mirrorBootCopy. It
		// reads through i18n.At rather than a Runtime, because an effect body
		// runs outside the render and UseI18n is a hook.
		mirrorBootCopy(i18n.At(locale.Get()))
		return nil
	}, []any{locale.Get(), catalogReady.Get()})

	// Every hook above this line runs on every render, unconditionally. The
	// branch is below, inside the Provider's child, and only chooses which
	// component to mount — GWC matches hooks positionally, so a conditional hook
	// binds to the wrong slot, and a child mounted conditionally gets its own
	// slot sequence and is safe.
	var child ui.Node
	switch phase.Get() {
	case phaseHome:
		child = ui.CreateElement(Home)
	case phaseReader:
		child = ui.CreateElement(Reader, readerProps{client: authed.Get(), prefs: saved.Get(), whoami: whoami.Get(),
			owner: owner.Get(), langFill: fill.Get()})
	case phaseSetup:
		child = ui.CreateElement(Setup, setupProps{
			tunnel: tunnel,
			onSuccess: func(c *data.Client) {
				authed.Set(c)
				// Whoever claims the instance is its first administrator.
				owner.Set(true)
				// Straight to the reader, and deliberately without the prefs
				// round trip the login path takes: an account created ten
				// seconds ago has no saved view to wait for, and pausing on a
				// fetch that can only return empty would add a stall to the one
				// moment somebody is watching to see whether this thing works.
				phase.Set(phaseReader)
			},
		})
	case phaseLogin:
		child = ui.CreateElement(Login, loginProps{
			tunnel:     tunnel,
			resetToken: resetToken.Get(),
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
					// And who signed in, which the boot path learns from the WhoAmI
					// it already made. Without it a reader who signed in on this
					// screen was nobody to the Account tab and a member to the
					// language picker, until they reloaded. A failure leaves both
					// at their honest zero.
					who, werr := c.WhoAmI(context.Background())
					ui.PostAsync(func() {
						if werr == nil {
							whoami.Set(who.GetUsername())
							owner.Set(isOwnerRole(who.GetRole()))
						}
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
	// The one address that is not the reader. Checked here rather than in
	// parseRoute because it is not a place inside the reader — it decides which
	// application mounts, not what that application is looking at.
	if isHomePath(platform.BasePath(), platform.Path()) {
		return phaseHome
	}
	// A reset link goes straight to the credential screen, skipping WhoAmI.
	//
	// Whoever followed one is locked out by definition, so the round trip that
	// can only answer "nobody" is a wait for a fact already known. It also has
	// to bypass a session that IS valid: an admin who mints a link for somebody
	// still signed in on this browser means the reset, not the session.
	if resetTokenFrom(platform.BasePath(), platform.Path(), platform.Query()) != "" {
		return phaseLogin
	}
	return phaseChecking
}

// resetPath is the address `articleflux reset -origin` prints.
//
// The CLI has printed `<origin>/reset?token=…` since §7.2 and nothing served it:
// the client had no such route, so the link landed on the plain sign-in card
// with no recovery mode and no prefill. An admin who sent one had, in effect,
// sent an instruction to open a screen and paste a string out of the URL bar by
// hand — which is exactly the step the link exists to remove, delivered to
// somebody who is locked out and already annoyed.
//
// The constant lives here rather than in cmd/articleflux because this is the
// half that has to recognise it; the CLI's copy is one `fmt.Printf` and is
// tested against this grammar.
const resetPath = "reset"

// resetTokenParam is the query key the link carries.
const resetTokenParam = "token"

// resetTokenFrom pulls the token out of a reset address, or returns "".
//
// Base-relative like every other address in this client (see isHomePath): an
// instance mounted under /reader/ answers at /reader/reset. Pure — no
// syscall/js — so the grammar is testable without a browser, which is the same
// split client/view/route.go is built on.
func resetTokenFrom(base, path, query string) string {
	if base == "" {
		base = "/"
	}
	if base != "/" {
		if trimmed := strings.TrimPrefix(path, strings.TrimSuffix(base, "/")); trimmed != path {
			path = trimmed
		}
	}
	if strings.Trim(path, "/") != resetPath {
		return ""
	}
	// Parsed by hand rather than through net/url: the value is a hex token, so
	// there is nothing to unescape, and url.ParseQuery on a malformed string
	// returns an error alongside the values it did manage to read — which is a
	// branch to get wrong for no gain here.
	for _, part := range strings.Split(query, "&") {
		k, v, found := strings.Cut(part, "=")
		if found && k == resetTokenParam {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// isHomePath reports whether an address asks for the front door.
//
// Base-relative, like every other address in this client (see routePath): an
// instance mounted under /reader/ serves the page at /reader/home, and hard-coding
// "/home" would be correct on exactly one deployment shape. Pure, so it is
// testable without a browser.
func isHomePath(base, path string) bool {
	if base == "" {
		base = "/"
	}
	if base != "/" {
		if trimmed := strings.TrimPrefix(path, strings.TrimSuffix(base, "/")); trimmed != path {
			path = trimmed
		}
	}
	return strings.Trim(path, "/") == homePath
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

// whoAmIAnswered asks WhoAmI until the server has actually ANSWERED.
//
// Every error used to send the reader to the login screen, on the reasoning that
// it was harmless for a server that was not answering. It is not harmless for one
// that is merely slow: under load the first round trip is cancelled or times out,
// and a reader who is already signed in — on a -dev server, who has no login at
// all — was shown "Sign in to your reader" (H15). Only Unauthenticated means
// "sign in"; a transport error, a deadline or a cancellation is the splash
// continuing, so it retries, quietly, with a gap that grows to a few seconds. The
// token is not touched on the way: the interceptor only clears it on a refusal.
func whoAmIAnswered(c *data.Client) (*pb.WhoAmIResponse, error) {
	gap := 400 * time.Millisecond
	for {
		who, err := c.WhoAmI(context.Background())
		if err == nil || status.Code(err) == codes.Unauthenticated {
			return who, err
		}
		time.Sleep(gap)
		if gap < 4*time.Second {
			gap *= 2
		}
	}
}
