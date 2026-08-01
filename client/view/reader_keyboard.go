//go:build js && wasm

package view

import (
	"strings"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// The keyboard map: every shortcut the reader pane answers to.
//
// Extracted from reader.go for the reason the other sections were — a single
// six-thousand-line component is not reviewable — and this one moves cleanly because
// its dependency surface is thirteen named handles and it declares nothing anyone else
// reads.
//
// # The hook contract
//
// GWC hooks are positional: unconditional, same order, every render. The UseEffect below
// is safe here only because Reader calls wire() unconditionally, in the position the
// effect used to occupy. Neither this function nor its call site may become conditional.
//
// # Why the shortcuts live together
//
// A key map is the one part of a UI that has to be read as a WHOLE: the question is never
// "what does j do" but "is anything else already using j". Scattering these across the
// features they drive is how two features quietly claim the same key.

type keyboardMap struct {
	tr            i18n.Runtime
	act           ui.Ref[*actions]
	pane          ui.State[view]
	current       ui.State[*pb.Item]
	items         ui.State[[]*pb.Item]
	stream        ui.State[[]*pb.Item]
	feeds         ui.State[[]*pb.Feed]
	tags          ui.State[[]*pb.Tag]
	focusMode     ui.State[bool]
	showOpen      ui.State[bool]
	fsOpen        ui.State[string]
	tsOpen        ui.State[string]
	paletteActive ui.State[int]
	// look is read for its motion setting only — the palette's toggle-motion
	// entry names both directions (N12), the way cmd.theme names every theme.
	look ui.State[appearance]
	// openItem and refresh are Reader's own closures, not state handles.
	//
	// A key map's whole job is to CALL things, and these two are the only things it calls
	// that are not on `actions`. Threading them as fields keeps the alternative off the
	// table: moving two closures out of the section that owns them, to satisfy this one.
	openItem func(*pb.Item)
	refresh  func()
}

// wire is called once, unconditionally, from Reader.
func (r keyboardMap) wire() {
	// --- keyboard -----------------------------------------------------------
	// Google Reader's map, unchanged. j/k/o/s/m transfer on day one, and
	// renaming them to something more "modern" would throw that away for nothing.

	// UseLayoutEffect, not UseEffect: a passive effect runs after the browser
	// paints, which leaves a real window — a few tens of milliseconds after the
	// list first paints — where the document has no key listener at all. A person
	// cannot type that fast, but a scripted `press()` fired the instant `.item-row`
	// is visible can and does land in it (motion.spec.mjs's Ctrl+K case measured
	// about one run in four). The layout effect runs synchronously right after
	// this commit, before paint, so the listener exists before the reader — or a
	// test — can ever see a frame to react to.
	ui.UseLayoutEffect(func() func() {
		l := platform.OnKeyDown(func(k platform.Key) {
			// Enter inside a field submits that field. This lives here rather
			// than on an OnKeyDown prop because GWC adapts a func(string)
			// handler to event.target.value — an input-level key handler can
			// never see "Enter" at all.
			// Ctrl-K / Cmd-K, from anywhere including inside a text field —
			// that is the point of a global palette, and it is the one binding
			// people try first.
			if k.Ctrl && (k.Name == "k" || k.Name == "K") {
				ui.PostAsync(func() { r.act.Get().openPalette() })
				return
			}
			// While the palette is open it owns the arrows, Enter and Escape,
			// even though focus is in a text field.
			if k.Role == "palette" {
				switch k.Name {
				case "ArrowDown":
					ui.PostAsync(func() { r.act.Get().movePalette(1) })
				case "ArrowUp":
					ui.PostAsync(func() { r.act.Get().movePalette(-1) })
				case "Escape":
					ui.PostAsync(func() { r.act.Get().closePalette() })
				case "Enter":
					// Read from the DOM: keydown fires before the value reaches
					// state, so state is one character behind and Enter would run
					// the previous query's top hit.
					q := platform.FieldValue("palette")
					ui.PostAsync(func() {
						a := r.act.Get()
						list := filterPalette(buildPalette(r.tr, r.feeds.Get(), r.tags.Get(), r.look.Get().motionOn()), q)
						if len(list) == 0 {
							return
						}
						i := r.paletteActive.Get()
						if i < 0 || i >= len(list) {
							i = 0
						}
						a.runPalette(kindClass(list[i].Kind) + ":" + list[i].ID)
					})
				}
				return
			}
			// Escape gets out of a text field, and it has to be handled BEFORE
			// the typing guard below — otherwise the guard swallows it and the
			// only way out of the search box is the mouse, which in a
			// keyboard-first app is the same as no way out.
			if k.Typing && k.Name == "Escape" && k.Role != "palette" {
				platform.Blur()
				ui.PostAsync(func() {
					a := r.act.Get()
					a.closeHelp()
					// A dialog's own fields are text fields, so Escape reaches
					// here rather than the branch below. Without this, Escape in
					// the add-a-feed URL box blurs it and leaves the dialog open
					// — a key that half-works reads as a broken key.
					a.closeAddFeed()
					a.closeCategory()
				})
				return
			}
			if k.Typing {
				// Notes save themselves on a pause now, so Ctrl+Enter is no
				// longer the way to keep one — it is "save it this instant",
				// for the reader who does not want to wait out the debounce and
				// for the muscle memory of everyone who learned the old rule.
				// Plain Enter must still insert a newline: a note is prose, and
				// a textarea that submits on Enter cannot hold a second
				// sentence.
				if k.Role == "note" {
					if k.Name == "Enter" && k.Ctrl {
						// Which note field: there is one per article in the
						// r.stream, and the focused one is the answer.
						id := platform.FocusedAttr("data-note-id")
						ui.PostAsync(func() { r.act.Get().saveNote(id) })
					}
					return
				}
				if k.Name != "Enter" {
					return
				}
				switch k.Role {
				case "search":
					// Read from the DOM, not from state: keydown fires before the
					// value reaches the component, so state is one character behind.
					q := strings.TrimSpace(platform.FieldValue("search"))
					ui.PostAsync(func() { r.act.Get().search(q) })
				case "add-feed", "add-feed-title", "add-feed-category":
					// Enter submits from any of the dialog's three fields. A form
					// where Enter works in one box and does nothing in the next is
					// a form people stop pressing Enter in.
					ui.PostAsync(func() { r.act.Get().addFeed() })
				case "category-name":
					ui.PostAsync(func() { r.act.Get().saveCategory() })
				case "feed-title":
					// Enter commits the rename. An empty value is meaningful —
					// it clears the override and goes back to the publisher's
					// title — so it is sent rather than ignored.
					v := platform.FieldValue("feed-title")
					ui.PostAsync(func() {
						if id := r.fsOpen.Get(); id != "" {
							r.act.Get().patchFeed(&pb.UpdateFeedSettingsRequest{
								SourceId: id, Title: &v})
						}
					})
				case "tag-label":
					// Enter commits the tag's rename, the same as the button. An
					// empty value clears the override and restores the tag's own
					// name, so it is sent rather than ignored.
					v := platform.FieldValue("tag-label")
					ui.PostAsync(func() {
						if id := r.tsOpen.Get(); id != "" {
							r.act.Get().patchTag(&pb.UpdateTagRequest{TagId: id, Label: &v})
						}
					})
				case "tag":
					ui.PostAsync(func() { r.act.Get().addTag("") })
				case roleThemePrompt:
					// Enter composes, the same as the button beside it. A prompt box
					// is a sentence someone typed, and a text field where Enter does
					// nothing is one they will press Enter in anyway.
					ui.PostAsync(func() { r.act.Get().composeTheme() })
				}
				return
			}

			// --- the slideshow owns the keyboard while it is running ---------
			//
			// Every key, not just the five it uses, and that is the point of
			// returning at the end of this block rather than falling through:
			// this is a MODE, and a reader who has handed the screen over to it
			// must not be able to open the command palette behind it, mark the
			// feed read, or navigate a list they cannot see. The five that do
			// something are the transport, plus the two ways out.
			//
			// Escape is here AND in design/slideshow.go's fullscreen listener,
			// because the two cases are different: the browser swallows Escape
			// while it owns the screen, so this branch is what serves a reader
			// whose fullscreen request was refused — which is the ordinary case
			// on a browser that will not go fullscreen outside a gesture.
			if r.showOpen.Get() {
				switch k.Name {
				case "Escape":
					ui.PostAsync(func() { r.act.Get().slideStop() })
				case " ", "Spacebar":
					// The universal key for "hold on a moment", in every player
					// anyone has ever used. It is the one binding here nobody
					// needs to be taught.
					ui.PostAsync(func() { r.act.Get().slidePause() })
				case "ArrowRight", "j", "n":
					ui.PostAsync(func() { r.act.Get().slideStep(1) })
				case "ArrowLeft", "k", "p":
					ui.PostAsync(func() { r.act.Get().slideStep(-1) })
				case "v":
					// v for voice. `l` is Like everywhere else in this
					// application, and a key that means one thing in the reader
					// and another in the slideshow is worse than an unbound one.
					ui.PostAsync(func() { r.act.Get().slideListen() })
				}
				return
			}

			// --- moving between panes ---------------------------------------
			//
			// Tab moves BETWEEN panes, the arrows move WITHIN one. That split is
			// what makes a 151-row sidebar navigable at all: 151 tab stops
			// between the rail and the article is not navigation.
			switch k.Name {
			case "1":
				platform.FocusFirst(".pane-rail", ".feed-row")
				return
			case "2":
				platform.FocusFirst(".list-scroll", ".item-row")
				return
			case "3":
				// The r.pane itself, not a control inside it: what a reader wants
				// from "go to the article" is to scroll it, and the arrows do
				// that natively once the scroll container has focus.
				platform.FocusFirst(".panes", ".pane-article")
				return
			case "?":
				ui.PostAsync(func() { r.act.Get().toggleHelp() })
				return
			case ",":
				// The convention every desktop app shares. Cheap to learn once
				// and impossible to discover otherwise, which is why the gear in
				// the toolbar exists too.
				ui.PostAsync(func() { r.act.Get().showSettings() })
				return
			case "/":
				// Focus, do not submit. "/" is the universal jump-to-search and
				// swallowing it into a search that has not been typed yet would
				// be worse than not binding it.
				platform.FocusField("search")
				return
			case "f":
				platform.FocusField("feed-filter")
				return
			case "Escape":
				ui.PostAsync(func() {
					a := r.act.Get()
					a.closeHelp()
					// Both settings panels, because Escape closing one dialog and
					// not the one beside it is the kind of inconsistency a reader
					// reads as a broken key rather than as a rule.
					a.closeTagSettings()
					a.closeFeedSettings()
					a.closeAddFeed()
					a.closeCategory()
					a.listenStop()
					// Escape peels one layer. In focus mode the outermost layer is
					// focus mode itself — and "back to the list" is meaningless
					// while the list is closed, so the key would read as dead.
					if r.focusMode.Get() {
						a.toggleFocus()
						return
					}
					r.pane.Set(viewList)
				})
				platform.Blur()
				return
			}

			// --- arrows, interpreted by whichever r.pane has focus -------------
			if k.Name == "ArrowDown" || k.Name == "ArrowUp" {
				delta := 1
				if k.Name == "ArrowUp" {
					delta = -1
				}
				switch {
				case platform.FocusedIn(".pane-rail"):
					platform.MoveFocus(".pane-rail", ".feed-row", delta)
					return
				case platform.FocusedIn(".list-scroll"):
					// The list's arrows OPEN as they move, because a reader
					// moving through a list is reading it — that is the whole
					// j/k idiom, and having arrows merely move focus while j/k
					// opens would be two behaviours for one gesture.
					platform.MoveFocus(".list-scroll", ".item-row", delta)
					// Through r.act.Get(), not r.items.Get()/r.current.Get() directly: see
					// the actions struct's navStep field for why.
					ui.PostAsync(func() {
						if next := r.act.Get().navStep(delta); next != nil {
							r.openItem(next)
						}
					})
					return
				}
				// Anywhere else the arrows belong to the browser: scrolling the
				// article is what they already do, and better than we would.
				return
			}

			switch k.Name {
			// j and k read the article to open through r.act.Get().navStep, not
			// r.items.Get()/r.current.Get() inline here — see the actions struct's
			// navStep field for why: this listener is registered once, and a
			// ui.State read directly inside its closure would forever answer
			// with whichever article was r.current at the render that mounted
			// it, not the one on screen when the key is actually pressed.
			case "j":
				ui.PostAsync(func() {
					if next := r.act.Get().navStep(1); next != nil {
						r.openItem(next)
					}
				})
			case "k":
				ui.PostAsync(func() {
					if next := r.act.Get().navStep(-1); next != nil {
						r.openItem(next)
					}
				})
			case "o", "Enter":
				ui.PostAsync(func() { r.act.Get().openExtern("") })
			// l and d rather than s: the shortcut names the thing it does, and
			// "star" is no longer a thing this reader does.
			case "t":
				ui.PostAsync(func() { r.act.Get().later("") })
			case "U":
				ui.PostAsync(func() { r.act.Get().markUnread("") })
			case "l":
				ui.PostAsync(func() { r.act.Get().rate("", 1) })
			case "d":
				ui.PostAsync(func() { r.act.Get().rate("", -1) })
			case "r":
				ui.PostAsync(r.refresh)
			// w for wide. f was taken by the rail's name filter, and a key that
			// silently does two things is worse than a key that is not the first
			// letter of the word.
			case "w":
				ui.PostAsync(func() { r.act.Get().toggleFocus() })
			// s for slideshow. It is the next step past w — w gives the article
			// the window, this gives it the screen — so the two sitting next to
			// each other in the help sheet is the whole explanation.
			case "s":
				ui.PostAsync(func() { r.act.Get().slideStart(false) })
			case "u":
				ui.PostAsync(func() { r.act.Get().toggleUnread() })
			}
		})
		return l.Release
	}, []any{})
}
