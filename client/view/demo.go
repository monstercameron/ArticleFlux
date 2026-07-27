//go:build js && wasm

package view

import (
	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// The demo build's root.
//
// It is Root with the authentication removed and nothing else changed. That is
// worth being explicit about, because the temptation with a demo is to build a
// second, simpler application that is easier to make look good — and a demo of
// an application that does not exist is worse than no demo. Everything below
// this component is the shipping reader: the same Reader, the same panes, the
// same client, talking over the same generated stubs. Only the answers on the
// other end are different (client/demodata).
//
// What is genuinely absent, and why:
//
//   - The login screen and the WhoAmI check. There are no accounts, so there is
//     nothing to check and nobody to check it against. Root's three phases
//     collapse to one.
//   - The rejected-credential handler. Nothing can reject a credential here.
//
// Everything else Root does — the locale hook, the Provider's placement, the
// document's lang and dir, the splash copy mirrored for the next load — is kept,
// because all of it is about the reader rather than about the server.
//
// This file is compiled into the demo binary only. Nothing in client/app
// references it, so the linker drops it from the shipping bundle and the G5 size
// ratchet is unaffected.

// DemoProps carries the already-connected client.
//
// The demo's connection cannot fail and does not need dialling, so it is built
// by main before anything renders and handed in — rather than being opened in
// an effect the way Root has to open the real one.
type DemoProps struct {
	Client *data.Client
}

// DemoRoot mounts the reader against a backend that answers in this tab.
func DemoRoot(p DemoProps) ui.Node {
	// There is no HTTP server behind this build — the instance answers RPCs from
	// memory — so the icon endpoint does not exist. Said once, here, rather than
	// letting every feed row discover it as a 404.
	HasFaviconService = false

	// The interface language, exactly as Root does it: read from localStorage,
	// clamped to the supported set, written back on change.
	//
	// Note what the demo cannot do with it — TranslateUI needs a server with an
	// OpenAI key, so switching language will fail here and say why. The hook
	// stays anyway: the choice is remembered, and a picker that refuses is a
	// truthful demonstration of a feature that costs money. Pretending it worked
	// would be the worst option of the three.
	locale := i18n.UseLocale(i18n.LocaleOptions{
		InitialLocale:    i18n.DefaultLocale,
		FallbackLocale:   i18n.DefaultLocale,
		SupportedLocales: i18n.SupportedLocales(),
		PersistenceKey:   i18n.PersistenceKey,
	})

	// The document's own language and direction, which decide hyphenation,
	// quotation marks, the spellchecker and which way every logical property in
	// the sheet points. Plus the splash's words for the next load.
	ui.UseEffect(func() func() {
		platform.SetRootAttr("lang", locale.Get())
		platform.SetRootAttr("dir", string(i18n.DirectionFor(locale.Get())))
		mirrorBootCopy(i18n.At(locale.Get()))
		return nil
	}, []any{locale.Get()})

	// The Provider is mounted HERE for the reason root.go records at length: a
	// Provider re-render marks its whole subtree dirty before any memo bailout
	// can run, and i18n.Runtime is a struct of func fields that never compares
	// equal — so it belongs in the component that re-renders least. This one
	// re-renders when the language changes, and never otherwise.
	return i18n.Provider(i18n.ProviderProps{
		Locale: locale,
		Bundle: i18n.Bundle(),
		Child:  ui.CreateElement(demoShell, p),
	})
}

// demoShell is the reader with the demo's own note over it.
//
// A component rather than a helper called inline, for the same reason
// bootSplash is one: Root builds its child BEFORE handing it to the Provider,
// so anything invoked at that point runs outside the context and would read an
// empty catalog.
func demoShell(p DemoProps) ui.Node {
	tr := i18n.UseI18n()
	hidden := ui.UseState(false)
	onDismiss := ui.UseEvent(func() { hidden.Set(true) })

	return html.Fragment(
		// readerProps carries a pointer and nothing else, so dismissing the note
		// re-renders this component and the reader memoises straight through it.
		ui.CreateElement(Reader, readerProps{client: p.Client}),
		ui.If(!hidden.Get(), func() ui.Node { return demoNote(tr, onDismiss) }),
	)
}

// demoNote is the one thing on screen that the shipping application does not
// have.
//
// It says three things, and each of them is a promise somebody evaluating this
// would otherwise have to take on trust: the data is invented, the server is
// this tab, and nothing is being kept. It is dismissible because a note that
// cannot be got rid of is furniture, and it is styled inline rather than through
// client/design because it is not part of the product — a class in the sheet
// would be a demo-shaped hole in the application's own stylesheet.
// It takes its translator and its dismiss handler rather than calling the hooks
// itself: hooks are positional, and a component that is mounted only while it is
// visible would bind its own slots on some renders and not others.
func demoNote(tr i18n.Runtime, onDismiss ui.Handler) ui.Node {
	return html.Div(html.Props{
		Class: "demo-note",
		Raw: map[string]any{
			"style": "position:fixed;left:14px;bottom:14px;z-index:40;" +
				"max-width:min(46ch,calc(100vw - 28px));display:grid;gap:.25rem;" +
				"padding:.55rem .7rem;border-radius:10px;border:1px solid var(--line);" +
				"background:var(--sur);box-shadow:var(--shadow);" +
				"font-size:.74rem;line-height:1.45;color:var(--mute);" +
				// The application's own motion switch, honoured: --mo is 0 when
				// the reader has asked for less motion, and this fades in with
				// everything else or with nothing.
				"transition:opacity calc(var(--mo) * .2s) ease",
		},
	},
		html.Div(html.Props{Raw: map[string]any{"style": "color:var(--cream);font-weight:600"}},
			html.Text(tr.T("demo", "title"))),
		html.Div(html.Props{}, html.Text(tr.T("demo", "blurb"))),
		html.Div(html.Props{Raw: map[string]any{"style": "display:flex;gap:.8rem;margin-top:.15rem"}},
			html.A(html.Props{
				Href:   demoSourceURL,
				Target: "_blank",
				Rel:    "noopener noreferrer",
				Raw:    map[string]any{"style": "color:var(--cc);text-decoration:none"},
			}, html.Text(tr.T("demo", "source"))),
			html.Button(html.Props{
				Type: "button",
				Raw: map[string]any{
					"style": "background:none;border:0;padding:0;cursor:pointer;" +
						"color:var(--mute);font:inherit;text-decoration:underline",
				},
				OnClick: onDismiss,
			}, html.Text(tr.T("demo", "dismiss"))),
		),
	)
}

// demoSourceURL is where somebody who likes what they are looking at goes next.
// The whole point of a demo for a self-hosted application is the step after it.
const demoSourceURL = "https://github.com/monstercameron/ArticleFlux"
