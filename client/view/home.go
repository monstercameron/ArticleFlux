//go:build js && wasm

package view

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// Home is the front door: what somebody sees at `/home` before they have an
// account, or when they want to know what this is.
//
// # Why it is a component and not a page
//
// It was a static file first — web/home.html, hand-written, with its own copy of
// the palette. Two things were wrong with that and only one of them was the rule.
// The rule is A26: the stylesheet is Go, and there is no .html marketing page
// exempt from it. The other is that the server's own CSP caught it immediately —
// `script-src 'self' 'wasm-unsafe-eval' <hash of the shell's inline boot script>`
// hashes the SHELL, so the page's inline script was blocked on first load. The
// constraints this application is built under do not stop at the reader's door,
// and a front door that has to be excused from them is advertising something the
// product is not.
//
// What it buys, now that it is here: the page paints from design's tokens, so it
// follows the visitor's theme, accent, reading size and motion setting. A reader
// who has set Daylight sees a Daylight homepage. That is not a trick a separate
// stylesheet could have done.
//
// # Mounted by Root, not by the router
//
// route.go's grammar describes places INSIDE the reader; this is not one. Root
// branches on the path before it asks the server anything, so a visitor gets the
// page immediately rather than after a WhoAmI they have no credential for. See
// homePath and Root's phaseHome.
//
// # The keys
//
// It obeys the reader's key map — `j` and `k` move between sections, `o` opens
// the demo, `?` shows the sheet. The claim on this page is that muscle memory
// transfers on day one, and a page that asserts that while ignoring `j` is
// arguing against itself.

// homePath is the address that shows this instead of the reader.
//
// A single segment, matched by Root before the WhoAmI round trip. It deliberately
// does NOT appear in route.go's grammar: parseRoute would degrade it to All (see
// its note on unknown paths), which is the right behaviour for a path the READER
// does not recognise, and this one never reaches it.
const homePath = "home"

// homeBands is the page, in order: the scroll anchor for each section, which is
// also its topmost-child key and the value its rail row clicks with.
//
// Index 0 is the hero. It is a stop for `k` and for the wordmark but not a rail
// row — the wordmark above the list is already the way back to the top, and a row
// that duplicates it is a second answer to a question with one.
//
// The hues are not here: they come from design.HomeHues by position, so the seven
// sections wear the seven named source hues in the order the reader hands them
// out to feeds. Neither are the labels — see homeRail, and the note on homeHead
// for why every string on this page is resolved at a literal call site.
var homeBands = []string{
	"top", "why", "reading", "finding", "ranking", "reach", "listen", "boundary", "built",
}

const (
	homeDemoURL   = "https://monstercameron.github.io/ArticleFlux/"
	homeSourceURL = "https://github.com/monstercameron/ArticleFlux"
	homeGwcURL    = "https://github.com/monstercameron/GoWebComponents"
	homeBridgeURL = "https://github.com/monstercameron/GoGRPCBridge"
)

func Home() ui.Node {
	tr := i18n.UseI18n()
	ns := tr.NS("home")

	// Which band the reader is on, as an index into homeBands. Driven by the
	// scroll position rather than by the last thing clicked, for the same reason
	// the reader's list highlight is (A28): where you are is a scroll position.
	band := ui.UseState(0)
	sheet := ui.UseState(false)
	// The last key pressed, so its cap lights up. No timer and no fade: this is
	// a key LOG, not a flash, and a reader pressing j four times should see the
	// cap that is answering them stay lit.
	lastKey := ui.UseState("")

	// Refs so the listeners below — registered once, on mount — read the current
	// value rather than the one that existed when they were created.
	bandRef := ui.UseRef(0)
	bandRef.Set(band.Get())
	sheetRef := ui.UseRef(false)
	sheetRef.Set(sheet.Get())

	goTo := func(i int) {
		if i < 0 {
			i = 0
		}
		if i >= len(homeBands) {
			i = len(homeBands) - 1
		}
		band.Set(i)
		platform.ScrollChildToTop(".hm", "[data-band='"+homeBands[i]+"']", true, nil)
	}

	// --- delegated clicks ----------------------------------------------------
	//
	// One listener for the whole page, on `#app` rather than on any element this
	// component renders: a rail row, the hint, the sheet's close button and the
	// backdrop are all the same gesture arriving at different boxes, and one
	// attribute names which.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-hm", func(v string) {
			ui.PostAsync(func() {
				switch {
				case v == "sheet-open":
					sheet.Set(true)
				case v == "sheet-close":
					sheet.Set(false)
				// The sheet's own box carries this so a click INSIDE it resolves
				// here instead of walking up to the backdrop. closest() takes the
				// innermost match, which is what makes one attribute enough.
				case v == "sheet-keep":
				case strings.HasPrefix(v, "go:"):
					goTo(homeBandIndex(strings.TrimPrefix(v, "go:")))
				}
			})
		})
		return l.Release
	}, []any{})

	// --- the keys ------------------------------------------------------------
	ui.UseEffect(func() func() {
		l := platform.OnKeyDown(func(k platform.Key) {
			// Typing is set when focus is in a text field. There is no field on
			// this page today, and the guard stays because the next version of
			// this page will have one.
			if k.Typing || k.Ctrl {
				return
			}
			switch k.Name {
			case "j":
				ui.PostAsync(func() { lastKey.Set("j"); goTo(bandRef.Get() + 1) })
			case "k":
				ui.PostAsync(func() { lastKey.Set("k"); goTo(bandRef.Get() - 1) })
			case "g":
				ui.PostAsync(func() { goTo(0) })
			case "o":
				ui.PostAsync(func() { lastKey.Set("o") })
				platform.OpenExternal(homeDemoURL)
			case "?":
				ui.PostAsync(func() { lastKey.Set("?"); sheet.Set(!sheetRef.Get()) })
			case "Escape":
				ui.PostAsync(func() { sheet.Set(false) })
			}
		})
		return l.Release
	}, []any{})

	// --- where am I ----------------------------------------------------------
	//
	// The same helper the reading pane uses to decide which article is being
	// read. A homepage and a continuous article stream turn out to be the same
	// problem: a long scroller whose current item is a position, not a click.
	ui.UseEffect(func() func() {
		// The `moved` flag is ignored here, and only here. It exists so the
		// reading pane can tell a reader arriving at an article from a body
		// loading above one and pushing it under the fold — a distinction that
		// matters because arriving marks something READ. Nothing on this page
		// records anything; the rail marker follows whatever is at the top,
		// however it got there.
		l := platform.OnTopmostChild(".hm", ".hm", "data-band", func(id string, _ bool) {
			ui.PostAsync(func() { band.Set(homeBandIndex(id)) })
		})
		return l.Release
	}, []any{})

	hues := design.HomeHues()

	kids := []ui.Node{
		homeRail(ns, band.Get(), hues),
		html.Main(html.Props{Class: "hm-wrap"},
			homeHero(ns),
			homeWhy(ns, hues[0]),
			homeReading(ns, hues[1], lastKey.Get()),
			homeFinding(ns, hues[2]),
			homeRanking(ns, hues[3]),
			homeReach(ns, hues[4]),
			homeListen(ns, hues[5]),
			homeBoundary(ns, hues[6]),
			homeBuilt(ns, hues[7]),
			homeClose(ns),
		),
	}
	if sheet.Get() {
		kids = append(kids, homeSheet(tr, ns))
	}

	return html.Div(html.Props{Class: "hm"}, kids...)
}

// homeBandIndex maps a band id to its position, defaulting to the hero.
//
// Total on purpose: the id arrives from a DOM attribute and from a click value,
// and neither is worth a panic. An unknown one means the page and this list have
// drifted, and the top is the answer that is never wrong.
func homeBandIndex(id string) int {
	for i, b := range homeBands {
		if b == id {
			return i
		}
	}
	return 0
}

// --- the rail ---------------------------------------------------------------

func homeRail(ns i18n.Namespace, current int, hues []string) ui.Node {
	// The seven labels, in homeBands order and written out one literal at a time
	// so client/i18n's coverage test can see them (see homeHead).
	labels := []string{
		ns.T("whyNav"), ns.T("readNav"), ns.T("findNav"), ns.T("rankNav"),
		ns.T("reachNav"), ns.T("listenNav"), ns.T("edgeNav"), ns.T("builtNav"),
	}
	rows := make([]ui.Node, 0, len(homeBands)-1)
	for i, b := range homeBands[1:] {
		rows = append(rows, html.Button(html.Props{
			Class: "hm-link",
			Raw: map[string]any{
				"data-hm":      "go:" + b,
				"style":        "--c:" + hues[i],
				"aria-current": boolAttr(current == i+1),
			},
		},
			html.Span(html.Props{Class: "hm-dot"}),
			html.Text(labels[i]),
		))
	}

	return html.Nav(html.Props{Class: "hm-rail", Aria: map[string]string{"label": ns.T("railBand")}},
		// The wordmark is the way back to the top. Titled, because a wordmark
		// that is also a control is not obviously either.
		html.Button(html.Props{Class: "hm-mark", Title: ns.T("skipToTop"),
			Raw: map[string]any{"data-hm": "go:top"}},
			html.Text(ns.T("markA")), html.B(html.Props{}, html.Text(ns.T("markB"))),
		),
		html.Div(html.Props{Class: "hm-band"}, html.Text(ns.T("railBand"))),
		html.Div(html.Props{Class: "hm-list"}, rows...),
		html.Div(html.Props{Class: "hm-rail-foot"},
			homeLink("hm-rail-foot-a", homeDemoURL, ns.T("linkDemo")),
			homeLink("hm-rail-foot-a", homeSourceURL, ns.T("linkSrc")),
			html.A(html.Props{Href: "/"}, html.Text(ns.T("linkIn"))),
			homeHint(ns),
		),
	)
}

// homeLink is an external link, opened in a new tab.
//
// A real anchor rather than a click handler, so middle-click, ctrl-click and
// "open in new window" all work — the same rule the article's Open original
// follows (§30b).
func homeLink(class, href, label string) ui.Node {
	return html.A(html.Props{
		Class: class, Href: href, Target: "_blank", Rel: "noopener noreferrer",
	}, html.Text(label))
}

// --- the hero ---------------------------------------------------------------

func homeHero(ns i18n.Namespace) ui.Node {
	return html.Header(html.Props{Class: "hm-hero", Raw: map[string]any{"data-band": "top"}},
		html.P(html.Props{Class: "hm-eyebrow hm-rise"},
			html.Text(ns.T("heroEyebrow")+" · "),
			html.Em(html.Props{}, html.Text(ns.T("heroLicence"))),
			html.Text(" · "+ns.T("heroOneBin")),
		),
		html.H1(html.Props{Class: "hm-h1 hm-rise", Raw: map[string]any{"style": "--i:1"}},
			html.Text(ns.T("heroTitleA")),
			html.Em(html.Props{}, html.Text(ns.T("heroTitleEm"))),
			html.Text(ns.T("heroTitleB")),
		),
		html.P(html.Props{Class: "hm-lede hm-rise", Raw: map[string]any{"style": "--i:2"}},
			html.Text(ns.T("heroLede")),
		),
		html.Div(html.Props{Class: "hm-cta hm-rise", Raw: map[string]any{"style": "--i:3"}},
			homeLink("hm-btn", homeDemoURL, ns.T("ctaDemo")),
			homeLink("hm-btn hm-ghost", homeSourceURL, ns.T("ctaSrc")),
		),
		html.P(html.Props{Class: "hm-note"}, html.Text(ns.T("ctaNote"))),

		// The dateline. The reader puts one under every headline; this one counts
		// what the thing is made of.
		html.P(html.Props{Class: "hm-dateline"},
			homeFact(ns.T("bomGoN"), ns.T("bomGo")),
			homeFact(ns.T("bomRpcN"), ns.T("bomRpc")),
			homeFact(ns.T("bomJsN"), ns.T("bomJs")),
			homeFact(ns.T("bomCssN"), ns.T("bomCss")),
			homeFact(ns.T("bomBinN"), ns.T("bomBin")),
		),

		homeFigArt("", "shots/reader-desktop.jpg", "shots/reader-phone.jpg", ns.T("shotDesktopAlt"), ns.T("shotDesktopCap"), "3200", "2000"),
	)
}

func homeFact(n, what string) ui.Node {
	return html.Span(html.Props{}, html.B(html.Props{}, html.Text(n)), html.Text(what))
}

// --- section furniture -------------------------------------------------------

// homeSection is the shell every band shares: the hue, the anchor, and the head.
func homeSection(id, hue string, head ui.Node, body ...ui.Node) ui.Node {
	kids := append([]ui.Node{head}, body...)
	return html.Section(html.Props{
		Class: "hm-sec",
		Raw:   map[string]any{"data-band": id, "style": "--c:" + hue},
	}, kids...)
}

// homeHead is the eyebrow, the headline and the standfirst.
//
// The status beside the eyebrow is the unusual part and it is deliberate: it says
// what is finished and what is not, at the top of the section rather than in a
// footnote nobody reaches. A homepage that only lists what works is a homepage
// somebody discovers the gaps in afterwards.
// Every string arrives RESOLVED rather than as a catalog key, and that shape is
// not a style choice: client/i18n's coverage test reads client/view as source and
// only sees literal `ns.T("…")` call sites. A helper taking a key would hide every
// key on this page from the ratchet that exists to notice an orphaned one.
func homeHead(nav, status, title, lede string) ui.Node {
	eyebrow := []ui.Node{html.Span(html.Props{}, html.Text(nav))}
	if status != "" {
		eyebrow = append(eyebrow, html.Span(html.Props{Class: "hm-status"}, html.Text(status)))
	}
	return html.Div(html.Props{Class: "hm-head"},
		html.P(html.Props{Class: "hm-sec-eyebrow"}, eyebrow...),
		html.H2(html.Props{Class: "hm-h2"}, html.Text(title)),
		html.P(html.Props{}, html.Text(lede)),
	)
}

// homeClaim is one claim: a hue tick, a heading, and one or two paragraphs.
func homeClaim(head string, body ...string) ui.Node {
	kids := []ui.Node{html.H3(html.Props{}, html.Text(head))}
	for _, t := range body {
		kids = append(kids, html.P(html.Props{}, html.Text(t)))
	}
	return html.Div(html.Props{Class: "hm-claim"}, kids...)
}

func homeClaims(kids ...ui.Node) ui.Node {
	return html.Div(html.Props{Class: "hm-claims"}, kids...)
}

// homeFig is one screenshot of the running application.
//
// Every picture on this page is the real reader against real feeds, captured by
// e2e/home-shots.mjs. Width and height are set so the space is reserved before
// the bytes arrive: a page that reflows as its images land is the layout shift
// the reader itself refuses to ship.
func homeFig(class, src, alt, caption, w, h string) ui.Node {
	return homeFigArt(class, src, "", alt, caption, w, h)
}

// homeFigArt is a screenshot with a PHONE capture to swap in on a narrow screen.
//
// # Why a second photograph and not a smaller one
//
// A 1600px-logical picture of a three-pane application, shown in a 350px column,
// renders at 0.21x. The reader's 14.5px body text lands at three pixels. Every
// wide screenshot on this page was doing exactly that on a phone, and it read as
// a smudge with a caption under it — which is worse than no picture, because it
// occupies the space where an explanation could have been.
//
// Scaling cannot fix it: legibility needs roughly 1:1, and 1:1 of a desktop
// capture does not fit. So the phone gets a photograph OF A PHONE — the same
// journey, captured at 390px, where the app's own type is already sized for the
// screen it is being shown on.
//
// `<picture>` rather than two `<img>`s toggled with CSS: display:none does not
// stop a browser downloading an image, so the CSS version would ship both files
// to everyone. The source's own width and height are set for the same reason the
// img's are — the two have different aspect ratios, and without them the swap is
// a layout shift.
func homeFigArt(class, src, narrow, alt, caption, w, h string) ui.Node {
	img := html.Img(html.Props{Src: src, Alt: alt, Width: w, Height: h, Loading: "lazy"})

	var shot ui.Node = img
	if narrow != "" {
		class += " hm-fig-wide"
		shot = html.Picture(html.Props{},
			html.Source(html.Props{Raw: map[string]any{
				// 1000px and not 700: the swap has to happen while there is still
				// enough width for the phone capture to sit at its own size, not
				// at the moment the desktop one becomes unreadable — by then it
				// has been unreadable for 300px.
				"media":  "(max-width: 1000px)",
				"srcset": narrow,
				"width":  "1170",
				"height": "2532",
			}}),
			img,
		)
	}

	return html.Figure(html.Props{Class: "hm-fig " + class},
		html.Div(html.Props{Class: "hm-shot"}, shot),
		html.Figcaption(html.Props{Class: "hm-cap"}, html.Text(caption)),
	)
}

// homeFigRow puts two shots side by side, for the pairs that only mean something
// together.
func homeFigRow(kids ...ui.Node) ui.Node {
	return html.Div(html.Props{Class: "hm-figrow"}, kids...)
}

// homeHonest is the aside for what a homepage usually leaves out.
func homeHonest(head string, body ...string) ui.Node {
	kids := []ui.Node{html.H3(html.Props{}, html.Text(head))}
	for _, t := range body {
		kids = append(kids, html.P(html.Props{}, html.Text(t)))
	}
	return html.Div(html.Props{Class: "hm-honest"}, kids...)
}

// --- the seven bands ----------------------------------------------------------

func homeWhy(ns i18n.Namespace, hue string) ui.Node {
	return homeSection("why", hue,
		homeHead(ns.T("whyNav"), "", ns.T("whyTitle"), ns.T("whyLede")),
		homeClaims(
			homeClaim(ns.T("whyFileH"), ns.T("whyFileP")),
			homeClaim(ns.T("whyKeysH"), ns.T("whyKeysP")),
			homeClaim(ns.T("whyFastH"), ns.T("whyFastP")),
		),
	)
}

func homeReading(ns i18n.Namespace, hue, lastKey string) ui.Node {
	// The keycaps are the section's hero, and they are live: the cap of the key
	// you just pressed lights up, including the ones this page does not act on,
	// because the point being made is that the map is the same one.
	caps := []struct{ key, label string }{
		{"j", ns.T("capNext")},
		{"k", ns.T("capPrev")},
		{"o", ns.T("capOpen")},
		{"l", ns.T("capLike")},
		{"t", ns.T("capLater")},
		{"w", ns.T("capFocus")},
		{"/", ns.T("capSearch")},
		{"?", ns.T("capMap")},
	}
	items := make([]ui.Node, 0, len(caps))
	for _, c := range caps {
		items = append(items, html.Li(html.Props{},
			html.Span(html.Props{
				Class: "hm-keycap",
				Raw:   map[string]any{"data-key": c.key, "data-hit": boolAttr(lastKey == c.key)},
			}, html.Text(c.key)),
			html.B(html.Props{}, html.Text(c.label)),
		))
	}

	return homeSection("reading", hue,
		homeHead(ns.T("readNav"), ns.T("readStatus"), ns.T("readTitle"), ns.T("readLede")),
		html.Ul(html.Props{Class: "hm-keys"}, items...),
		homeClaims(
			homeClaim(ns.T("readPanesH"), ns.T("readPanesP")),
			homeClaim(ns.T("readStreamH"), ns.T("readStreamP")),
			homeClaim(ns.T("readHueH"), ns.T("readHueP1"), ns.T("readHueP2")),
			homeClaim(ns.T("readRowH"), ns.T("readRowP")),
			homeClaim(ns.T("readSkelH"), ns.T("readSkelP")),
			homeClaim(ns.T("readFocusH"), ns.T("readFocusP")),
		),
		homeFigArt("", "shots/appearance.jpg", "shots/appearance-phone.jpg", ns.T("shotThemesAlt"), ns.T("figThemesCap"), "3200", "2000"),
		homeFigArt("", "shots/focus.jpg", "shots/focus-phone.jpg", ns.T("shotFocusAlt"), ns.T("figFocusCap"), "3200", "2000"),
		homeClaims(
			homeClaim(ns.T("readThemeH"), ns.T("readThemeP1"), ns.T("readThemeP2")),
			homeClaim(ns.T("readDescH"), ns.T("readDescP")),
			homeClaim(ns.T("readDriftH"), ns.T("readDriftP")),
			homeClaim(ns.T("readPhoneH"), ns.T("readPhoneP")),
			homeClaim(ns.T("readInstallH"), ns.T("readInstallP")),
			homeClaim(ns.T("readLangH"), ns.T("readLangP")),
		),
		homeFigRow(
			homeFig("hm-fig-phone", "shots/reader-phone.jpg", ns.T("shotPhoneAlt"), ns.T("figPhoneListCap"), "1170", "2532"),
			homeFig("hm-fig-phone", "shots/phone-article.jpg", ns.T("shotPhoneArticleAlt"), ns.T("figPhoneArticleCap"), "1170", "2532"),
		),
	)
}

func homeFinding(ns i18n.Namespace, hue string) ui.Node {
	return homeSection("finding", hue,
		homeHead(ns.T("findNav"), ns.T("findStatus"), ns.T("findTitle"), ns.T("findLede")),
		homeClaims(
			homeClaim(ns.T("findSearchH"), ns.T("findSearchP")),
			homeClaim(ns.T("findPalH"), ns.T("findPalP1"), ns.T("findPalP2")),
			homeClaim(ns.T("findTagH"), ns.T("findTagP")),
			homeClaim(ns.T("findOpmlH"), ns.T("findOpmlP")),
			homeClaim(ns.T("findUndoH"), ns.T("findUndoP")),
			homeClaim(ns.T("findDiscoverH"), ns.T("findDiscoverP")),
			homeClaim(ns.T("findFeedSetH"), ns.T("findFeedSetP")),
			homeClaim(ns.T("findClassH"), ns.T("findClassP")),
			homeClaim(ns.T("findSetH"), ns.T("findSetP")),
		),
		homeFigArt("", "shots/palette.jpg", "shots/palette-phone.jpg", ns.T("shotPaletteAlt"), ns.T("figPaletteCap"), "3200", "2000"),
		homeFigArt("", "shots/search.jpg", "shots/search-phone.jpg", ns.T("shotSearchAlt"), ns.T("figSearchCap"), "3200", "2000"),
		homeFigArt("", "shots/addfeed.jpg", "shots/addfeed-phone.jpg", ns.T("shotAddAlt"), ns.T("figAddCap"), "3200", "2000"),
		homeFigArt("", "shots/keys.jpg", "shots/keys-phone.jpg", ns.T("shotKeysAlt"), ns.T("figKeysCap"), "3200", "2000"),
	)
}

// homeRanking is My Feed and the layer under it.
//
// It gets its own band rather than a claim inside Reading because it is the one
// thing here that is not a feature of a reader — it is a claim about what the
// application is allowed to know about you, and that deserves the room to say
// what it measures and what it refuses to.
func homeRanking(ns i18n.Namespace, hue string) ui.Node {
	return homeSection("ranking", hue,
		homeHead(ns.T("rankNav"), ns.T("rankStatus"), ns.T("rankTitle"), ns.T("rankLede")),
		homeFigArt("", "shots/myfeed.jpg", "shots/myfeed-phone.jpg", ns.T("shotMyFeedAlt"), ns.T("rankFigCap"), "3200", "2000"),
		homeClaims(
			homeClaim(ns.T("rankSignalH"), ns.T("rankSignalP1"), ns.T("rankSignalP2")),
			homeClaim(ns.T("rankWrongH"), ns.T("rankWrongP1"), ns.T("rankWrongP2")),
			homeClaim(ns.T("rankNeverH"), ns.T("rankNeverP")),
			homeClaim(ns.T("rankOutboxH"), ns.T("rankOutboxP")),
			homeClaim(ns.T("rankArgueH"), ns.T("rankArgueP")),
		),
	)
}

func homeReach(ns i18n.Namespace, hue string) ui.Node {
	return homeSection("reach", hue,
		homeHead(ns.T("reachNav"), ns.T("reachStatus"), ns.T("reachTitle"), ns.T("reachLede")),
		homeClaims(
			homeClaim(ns.T("reachRealH"), ns.T("reachRealP")),
			homeClaim(ns.T("reachViewH"), ns.T("reachViewP")),
			homeClaim(ns.T("reachTextH"), ns.T("reachTextP1"), ns.T("reachTextP2")),
			homeClaim(ns.T("reachConnH"), ns.T("reachConnP")),
		),
		homeHonest(ns.T("reachHonestH"), ns.T("reachHonestP1"), ns.T("reachHonestP2")),
	)
}

func homeListen(ns i18n.Namespace, hue string) ui.Node {
	return homeSection("listen", hue,
		homeHead(ns.T("listenNav"), ns.T("listenStatus"), ns.T("listenTitle"), ns.T("listenLede")),
		homeClaims(
			homeClaim(ns.T("listenFreeH"), ns.T("listenFreeP")),
			homeClaim(ns.T("listenCastH"), ns.T("listenCastP1"), ns.T("listenCastP2")),
			homeClaim(ns.T("listenMeanH"), ns.T("listenMeanP1"), ns.T("listenMeanP2")),
			homeClaim(ns.T("listenPaidH"), ns.T("listenPaidP")),
			homeClaim(ns.T("listenKeepH"), ns.T("listenKeepP")),
		),
		homeFigArt("", "shots/slideshow.jpg", "shots/slideshow-phone.jpg", ns.T("shotSlideAlt"), ns.T("listenSlideCap"), "3200", "2000"),
		homeFigArt("", "shots/listening.jpg", "shots/listening-phone.jpg", ns.T("shotListenAlt"), ns.T("listenSetCap"), "3200", "2000"),
	)
}

func homeBoundary(ns i18n.Namespace, hue string) ui.Node {
	return homeSection("boundary", hue,
		homeHead(ns.T("edgeNav"), ns.T("edgeStatus"), ns.T("edgeTitle"), ns.T("edgeLede")),
		homeClaims(
			homeClaim(ns.T("edgeKeyH"), ns.T("edgeKeyP")),
			homeClaim(ns.T("edgeFontH"), ns.T("edgeFontP")),
			homeClaim(ns.T("edgeSsrfH"), ns.T("edgeSsrfP")),
			homeClaim(ns.T("edgeDedupH"), ns.T("edgeDedupP")),
			homeClaim(ns.T("edgeAuthH"), ns.T("edgeAuthP")),
			homeClaim(ns.T("edgeBootH"), ns.T("edgeBootP")),
		),
	)
}

func homeBuilt(ns i18n.Namespace, hue string) ui.Node {
	// The commands are not copy and are deliberately not in the catalog: a
	// translated `make.ps1 build` is a command that does not run.
	return homeSection("built", hue,
		homeHead(ns.T("builtNav"), ns.T("builtStatus"), ns.T("builtTitle"), ns.T("builtLede")),
		homeClaims(
			homeClaim(ns.T("builtProtoH"), ns.T("builtProtoP")),
			homeClaim(ns.T("builtRenameH"), ns.T("builtRenameP")),
			homeClaim(ns.T("builtGuardH"), ns.T("builtGuardP")),
			homeClaim(ns.T("builtTestH"), ns.T("builtTestP")),
			homeClaim(ns.T("builtOpsH"), ns.T("builtOpsP")),
			homeClaim(ns.T("builtDemoH"), ns.T("builtDemoP")),
		),
		homeFigArt("", "shots/server.jpg", "shots/server-phone.jpg", ns.T("shotServerAlt"), ns.T("builtOpsCap"), "3200", "2000"),
		homeHonest(ns.T("builtHonestH"), ns.T("builtHonestP1"), ns.T("builtHonestP2")),
		html.P(html.Props{Class: "hm-head", Raw: map[string]any{"style": "border:0"}},
			html.B(html.Props{}, html.Text(ns.T("builtRunH"))),
			html.Text(" "+ns.T("builtRunP")),
		),
		html.Pre(html.Props{Class: "hm-pre"},
			html.Span(html.Props{Class: "hm-p"}, html.Text("./scripts/make.ps1")),
			html.Text(" build\n"),
			html.Span(html.Props{Class: "hm-p"}, html.Text("./bin/articleflux.exe")),
			html.Text(" seed\n"),
			html.Span(html.Props{Class: "hm-p"}, html.Text("./scripts/make.ps1")),
			html.Text(" dev            "),
			html.Span(html.Props{Class: "hm-c"}, html.Text("# http://127.0.0.1:9000\n\n")),
			html.Span(html.Props{Class: "hm-p"}, html.Text("articleflux")),
			html.Text(" init -db /var/lib/articleflux/articleflux.db -user cam"),
		),
		html.P(html.Props{Class: "hm-note"}, html.Text(ns.T("builtDeployP"))),
	)
}

func homeClose(ns i18n.Namespace) ui.Node {
	return html.Section(html.Props{Class: "hm-close"},
		html.H2(html.Props{Class: "hm-h2"}, html.Text(ns.T("closeTitle"))),
		html.P(html.Props{Class: "hm-lede"}, html.Text(ns.T("closeLede"))),
		html.Div(html.Props{Class: "hm-cta"},
			homeLink("hm-btn", homeDemoURL, ns.T("ctaDemo")),
			homeLink("hm-btn hm-ghost", homeSourceURL, ns.T("ctaSrc")),
			html.A(html.Props{Class: "hm-btn hm-ghost", Href: "/"}, html.Text(ns.T("ctaIn"))),
		),
		html.Footer(html.Props{Class: "hm-foot"},
			html.Span(html.Props{}, html.Text(ns.T("footLicence"))),
			homeLink("", homeSourceURL, ns.T("footSource")),
			homeLink("", homeGwcURL, ns.T("footGwc")),
			homeLink("", homeBridgeURL, ns.T("footBridge")),
			html.Button(html.Props{Raw: map[string]any{"data-hm": "sheet-open"}},
				html.Text(ns.T("footKeys")),
			),
		),
	)
}

// --- the key hint, and the sheet ----------------------------------------------

func homeHint(ns i18n.Namespace) ui.Node {
	return html.Button(html.Props{
		Class: "hm-hint",
		Raw:   map[string]any{"data-hm": "sheet-open"},
		Aria:  map[string]string{"haspopup": "dialog"},
	},
		html.Kbd(html.Props{Class: "hm-k"}, html.Text("j")),
		html.Kbd(html.Props{Class: "hm-k"}, html.Text("k")),
		html.Text(ns.T("hintMove")+" · "),
		html.Kbd(html.Props{Class: "hm-k"}, html.Text("?")),
		html.Text(ns.T("hintKeys")),
	)
}

// homeSheet is the reader's own key map, on the page that claims it transfers.
//
// The bindings and their descriptions come from the `help` namespace rather than
// from a second copy under `home`: this IS the reader's sheet, and two catalogs
// describing one key map is how they end up describing different ones.
func homeSheet(tr i18n.Runtime, ns i18n.Namespace) ui.Node {
	type binding struct{ keys, does string }
	groups := []struct {
		title string
		items []binding
	}{
		{tr.T("help", "groupAnywhere"), []binding{
			{"Ctrl K", tr.T("help", "palette")},
			{"?", tr.T("help", "toggleHelp")},
			{"1 2 3", tr.T("help", "jumpPanes")},
			{"/", tr.T("help", "search")},
			{"f", tr.T("help", "filterFeeds")},
			{"r", tr.T("help", "refresh")},
			{"u", tr.T("help", "unreadOnly")},
			{"Esc", tr.T("help", "escape")},
		}},
		{tr.T("help", "groupRail"), []binding{
			{"↑ ↓", tr.T("help", "moveFeeds")},
			{"Enter", tr.T("help", "openFeed")},
		}},
		{tr.T("help", "groupList"), []binding{
			{"↑ ↓", tr.T("help", "moveAndOpen")},
			{"j k", tr.T("help", "nextPrev")},
		}},
		{tr.T("help", "groupArticle"), []binding{
			{"o", tr.T("help", "openOriginal")},
			{"l", tr.T("help", "like")},
			{"d", tr.T("help", "dislike")},
			{"t", tr.T("help", "readLater")},
			{"U", tr.T("help", "markUnread")},
			{"w", tr.T("help", "focusMode")},
			{"Ctrl Enter", tr.T("help", "saveNote")},
		}},
	}

	rows := make([]ui.Node, 0, len(groups)*2)
	for _, g := range groups {
		rows = append(rows, html.Div(html.Props{Class: "hm-dt"}, html.Text(g.title)))
		cells := make([]ui.Node, 0, len(g.items)*3)
		for i, b := range g.items {
			if i > 0 {
				cells = append(cells, html.Text(" · "))
			}
			for _, k := range strings.Fields(b.keys) {
				cells = append(cells, html.Kbd(html.Props{Class: "hm-k"}, html.Text(k)))
			}
			cells = append(cells, html.Text(b.does))
		}
		rows = append(rows, html.Div(html.Props{Class: "hm-dd"}, cells...))
	}

	return html.Div(html.Props{
		Class: "hm-scrim",
		Raw:   map[string]any{"data-hm": "sheet-close"},
	},
		html.Div(html.Props{
			Class: "hm-sheet", Role: "dialog",
			Raw:  map[string]any{"data-hm": "sheet-keep"},
			Aria: map[string]string{"modal": "true", "label": ns.T("sheetTitle")},
		},
			html.H2(html.Props{Class: "hm-h2"}, html.Text(ns.T("sheetTitle"))),
			html.P(html.Props{Class: "hm-sheet-sub"}, html.Text(ns.T("sheetSub"))),
			html.Div(html.Props{Class: "hm-dl"}, rows...),
			html.Button(html.Props{
				Class: "hm-sheet-close",
				Raw:   map[string]any{"data-hm": "sheet-close"},
			}, html.Text(ns.T("sheetClose"))),
		),
	)
}
