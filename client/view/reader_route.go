//go:build js && wasm

package view

import (
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// Keeping the address bar and the reader in step (§20.13b).
//
// route.go owns the grammar and is pure; platform/history_wasm.go owns the four
// browser calls and knows no grammar. This is the part in the middle: the two
// directions, and the boot-time precedence between an address and a saved place.
//
// Extracted from reader.go for delegatedClicks' reason — a small, named
// dependency surface and no other part of the component reading anything it
// declares — and it keeps the same hook contract, which for this file is not a
// formality:
//
// **wire() installs hooks, so Reader must call it unconditionally, once, in the
// same position on every render.** GWC matches hooks positionally. Nothing here
// may become conditional and neither may the call site.
//
// # Why the writer derives rather than being called
//
// There is one place the address is written — the effect in wire() — and it
// computes the address from state rather than being invoked by the twenty-odd
// actions that navigate. Hooking each call site would mean every future action
// that changes the scope, opens a dialog or switches a tab has to remember to
// update the URL too, and the one that forgets produces an address that lies.
// Deriving cannot forget: if the state changed, the address follows.
//
// # Push versus replace
//
// samePlace decides, and the decision is load-bearing rather than cosmetic. The
// reading pane is a continuous stream, so `current` changes as the reader
// scrolls (A28: which article is being read is a scroll position, not a click).
// One history entry per article scrolled past would bury the entry the reader
// actually wants under a hundred they never chose, and Back would stop meaning
// anything. So a change of place pushes and a change of article replaces.

// addressBar is wire()'s dependency surface: everything the address is derived
// from, plus what applying one needs to touch.
//
// A struct rather than fourteen parameters, for delegatedClicks' reason — adding
// one is a named field at the call site rather than a positional argument in the
// right slot among a dozen handles of the same type.
type addressBar struct {
	tr  i18n.Runtime
	act ui.Ref[*actions]
	// restoring marks the next address change as the app catching up with where
	// the reader already is, rather than the reader going somewhere. Set by the
	// prefs effect; consumed by the writer below, which replaces instead of
	// pushing. See its declaration in reader.go for the case it exists for.
	restoring ui.Ref[bool]

	// The place, and the article being read in it.
	sel     ui.State[scope]
	current ui.State[*pb.Item]
	// resumeItem is the article to reopen once a list lands. Written when a
	// popped address names an article that is not in the loaded list.
	resumeItem ui.Ref[string]

	// The settings surface: open iff pane is viewSettings.
	pane   ui.State[view]
	setTab ui.State[string]

	// The dialogs.
	addOpen  ui.State[bool]
	fsOpen   ui.State[string]
	tsOpen   ui.State[string]
	showOpen ui.State[bool]

	// The rail's data, for naming a scope that arrived as an id (titleForScope).
	feeds   ui.State[[]*pb.Feed]
	tags    ui.State[[]*pb.Tag]
	folders ui.State[[]*pb.Folder]

	// remember is Reader's rememberScope: it writes the saved place.
	//
	// Needed here for one case, and it is a coherence bug rather than a nicety.
	// A scope that arrived from a URL is not written back at mount, because at
	// mount it has no title — a path carries an id. Meanwhile reading in it DOES
	// keep writing `read.item` (reader.go's open). Leave it there and the stored
	// place is half of one scope and half of another: `read.kind` naming
	// yesterday's stream beside an article from today's feed, which resumes as
	// the old stream with a resume-item that is not in it. So the moment the
	// title lands, the place is recorded properly.
	remember func(scope)
}

// current is the address this render describes.
func (d addressBar) currentRoute() route {
	r := route{sel: d.sel.Get()}
	if it := d.current.Get(); it != nil {
		r.item = it.GetId()
	}
	if d.pane.Get() == viewSettings {
		r.tab = settingsTab(d.setTab.Get())
		if !knownSettingsTab(r.tab) {
			r.tab = setReading
		}
	}
	// At most one dialog. The order is which wins if two are somehow open at
	// once, and it is not arbitrary: the two that NAME a subject outrank the two
	// that do not, because an address that names a feed is more specific than one
	// that says a dialog is open somewhere.
	switch {
	case d.fsOpen.Get() != "":
		r.dlg, r.dlgID = dialogFeed, d.fsOpen.Get()
	case d.tsOpen.Get() != "":
		r.dlg, r.dlgID = dialogTag, d.tsOpen.Get()
	case d.addOpen.Get():
		r.dlg = dialogAdd
	case d.showOpen.Get():
		r.dlg = dialogShow
	}
	return r
}

// wire installs the two effects and refreshes the applyAddress action. Called
// once, unconditionally, from Reader.
func (d addressBar) wire() {
	base := platform.BasePath()

	// What was last WRITTEN to the address bar, and the route it described.
	//
	// Refs, not state: neither paints anything, and a render per address write
	// would be a render per scrolled article. They are also the loop guard —
	// popstate seeds them with what it just read, so applying a popped address
	// cannot turn round and push it back on.
	lastPath := ui.UseRef("")
	lastRoute := ui.UseRef(route{})

	now := d.currentRoute()
	path := routePath(base, now)

	// Reassigned on every render, and it has to be.
	//
	// The popstate listener below is registered ONCE, in a fixed-deps effect, so
	// a closure handed to it directly would read the state handles of the render
	// that mounted it — forever. That is the hazard documented at the top of the
	// `actions` struct and the one that made j-then-j reopen the same article:
	// the callback would apply a popped address against the scope, the item list
	// and the dialogs as they were on the first frame. Reached through the Ref,
	// it is always the newest render's closure.
	d.act.Get().applyAddress = func() { d.apply(base, lastPath, lastRoute) }

	// The writer.
	//
	// Deps are `path` and the body re-checks it against lastPath, which looks
	// redundant and is not: in this runtime effect deps are a hint rather than a
	// guarantee (an effect may re-run with unchanged deps), so the Ref comparison
	// is the actual guard and the deps are the optimisation. Writing the same
	// address twice would be harmless for replace and a duplicate history entry
	// for push, which is the version of this bug a reader notices — Back that
	// needs pressing twice.
	ui.UseEffect(func() func() {
		// Read and cleared BEFORE the early return, and in the deps, so the flag
		// cannot outlive the change it was raised for. A restoration that lands on
		// the address already shown — the common case, since preferences usually
		// agree with where the reader already is — writes nothing, and a flag left
		// standing after that would silently turn the reader's NEXT navigation
		// into a replace, costing them one Back with no way to tell why.
		restore := d.restoring.Get()
		if restore {
			d.restoring.Set(false)
		}
		if path == lastPath.Get() {
			return nil
		}
		first := lastPath.Get() == ""
		prev := lastRoute.Get()
		lastPath.Set(path)
		lastRoute.Set(now)
		switch {
		case first || restore:
			// The landing address, normalised — and REPLACED, never pushed.
			//
			// This is the write that makes a resumed place linkable: a reader who
			// opens the bare address lands on the feed they left off in, and the
			// bar now names it, so the link in their hands is the one they would
			// want to send. Pushing here would put a second entry on top of the
			// one the browser made for the page load, and Back would appear to do
			// nothing before finally leaving.
			//
			// `restore` is the same write arriving late — the prefs round trip, or
			// a manifest shortcut — and gets the same treatment for the same
			// reason: the reader has not gone anywhere.
			platform.ReplacePath(path)
		case samePlace(prev, now):
			platform.ReplacePath(path)
		default:
			platform.PushPath(path)
		}
		return nil
	}, []any{path, d.restoring.Get()})

	// The reader pressing Back or Forward.
	ui.UseEffect(func() func() {
		l := platform.OnPopState(func() {
			// Through PostAsync like every other listener callback here: this runs
			// outside a render, and applying a route sets several pieces of state.
			ui.PostAsync(func() {
				if a := d.act.Get(); a != nil && a.applyAddress != nil {
					a.applyAddress()
				}
			})
		})
		return l.Release
	}, []any{})

	// Naming a scope that arrived as an id.
	//
	// A path carries "/feed/01J2…" and never the feed's name, where the saved
	// place carries `read.title` precisely so the header is right on the first
	// frame. So a URL-seeded scope opens titleless and this fills it in the moment
	// the rail's data lands. titleForScope is a no-op once a title is set, so a
	// reader who resumed from preferences pays a comparison and nothing else — and
	// their saved header is never overwritten by a feed-list refresh.
	ui.UseEffect(func() func() {
		sc := d.sel.Get()
		if sc.Title != "" {
			return nil
		}
		if named := titleForScope(sc, d.feeds.Get(), d.tags.Get(), d.folders.Get()); named.Title != "" {
			d.sel.Set(named)
			// And record it, now that there is something to record. See the
			// `remember` field for the half-written preference this closes.
			if d.remember != nil {
				d.remember(named)
			}
		}
		return nil
	}, []any{d.sel.Get().Title == "", len(d.feeds.Get()), len(d.tags.Get()), len(d.folders.Get())})
}

// apply moves the reader to whatever the address now says, after a Back or a
// Forward.
//
// It is a diff rather than a reset: each of the four things an address carries is
// changed only if it actually differs from what is on screen. Reapplying them all
// unconditionally would refetch the list on every Back within one feed and would
// close and reopen a dialog the address did not change, both of which are visible.
func (d addressBar) apply(base string, lastPath ui.Ref[string], lastRoute ui.Ref[route]) {
	r := parseRoute(base, platform.Path(), platform.Query(), d.tr)

	// Seed the writer's guard with what was just READ, so settling into this route
	// does not push it back on. Without this, Back would apply the previous
	// address and then immediately push it as a new entry — Forward would vanish
	// and Back would walk the same two entries forever.
	lastPath.Set(routePath(base, r))
	lastRoute.Set(r)

	a := d.act.Get()
	if a == nil {
		return
	}

	// The settings surface.
	if r.tab != "" {
		if d.pane.Get() != viewSettings {
			a.showSettings()
		}
		if settingsTab(d.setTab.Get()) != r.tab {
			a.settingsTabTo(string(r.tab))
		}
	} else if d.pane.Get() == viewSettings {
		a.showTab(viewList)
	}

	// The place, and the article in it.
	//
	// Skipped entirely when the address is a SCREEN rather than a place. Those
	// three forms — /settings/<tab>, /feed/<id>/settings, /tag/<id>/settings —
	// take over the path while they are open (see routeSegments), so the scope
	// underneath them is not written down anywhere and parseRoute can only report
	// its default. Applying that default would mean going Back to the settings
	// screen silently moved the reader's feed to All, and Forward to a feed's
	// settings dragged them into that feed. Boot is the one case where these DO
	// choose the place, because at boot there is no place yet to preserve — that
	// is bootRoute's job and it uses the parsed scope directly.
	rk, rv := scopeKind(r.sel)
	ck, cv := scopeKind(d.sel.Get())
	screen := r.tab != "" || r.dlg == dialogFeed || r.dlg == dialogTag
	switch {
	case screen:
		// Leave the reader where they are.
	case rk != ck || rv != cv:
		// A different place: the list has to be refetched, so the article the
		// address named is left for the auto-open effect to reopen once that list
		// lands — the same path a resume takes.
		d.resumeItem.Set(r.item)
		a.pick(titleForScope(r.sel, d.feeds.Get(), d.tags.Get(), d.folders.Get()))
	case r.item != "":
		// The same place. If the article is already loaded this opens it now;
		// otherwise it waits for the list, which is what happens when somebody
		// goes Back past a page boundary.
		if it := a.itemByID(r.item); it != nil {
			a.open(it)
		} else {
			d.resumeItem.Set(r.item)
		}
	}

	// The dialogs: close whatever the address does not name, then open what it
	// does. Closing first, because at most one can be open and opening a second
	// over a first would leave the first behind the new one with no way back to it.
	if r.dlg != dialogAdd && d.addOpen.Get() {
		a.closeAddFeed()
	}
	if r.dlg != dialogFeed && d.fsOpen.Get() != "" {
		a.closeFeedSettings()
	}
	if r.dlg != dialogTag && d.tsOpen.Get() != "" {
		a.closeTagSettings()
	}
	if r.dlg != dialogShow && d.showOpen.Get() {
		a.slideStop()
	}
	switch r.dlg {
	case dialogAdd:
		if !d.addOpen.Get() {
			a.openAddFeed()
		}
	case dialogFeed:
		if d.fsOpen.Get() != r.dlgID {
			a.openFeedSettings(r.dlgID)
		}
	case dialogTag:
		if d.tsOpen.Get() != r.dlgID {
			a.openTagSettings(r.dlgID)
		}
	case dialogShow:
		// Started here and NOT at boot; see bootRoute.
		if !d.showOpen.Get() {
			a.slideStart()
		}
	}
}

// --- boot ---------------------------------------------------------------------

// bootState is what the address said when Reader MOUNTED.
//
// It exists because "read the address" is not the same question at render time as
// it is at mount time, and conflating the two cost a working share target.
// Reader's body runs on every render, so a bare `readLaunch()` there is re-read
// on every render — and the address writer strips the query on the first commit,
// so by the time the preferences effect's closure is built (a round trip later,
// when the client connects) the parameters it needs are gone. The symptom was a
// shared address opening the app and doing nothing, which is the exact failure
// §20.24 says is worse than not implementing the feature.
//
// Held in a Ref by readBootState below, so every consumer sees the same answer
// for the life of the component no matter how many times the body re-runs.
type bootState struct {
	// route is where the address says to open, or the resumed place.
	route route
	// addressed reports which of those two answered.
	addressed bool
	// launch is what an installed app was opened FOR (§20.24) — a manifest
	// shortcut or a share, which live in the query and not in the path.
	launch launch
}

// readBootState captures the opening address. Call it through a Ref (see
// Reader), never directly from a render body.
func readBootState(saved map[string]string, tr i18n.Runtime) bootState {
	r, addressed := bootRoute(saved, tr)
	return bootState{route: r, addressed: addressed, launch: readLaunch()}
}

// bootRoute decides where the reader opens: the address if it names somewhere,
// the saved place otherwise.
//
// This is §20.13b's whole precedence rule and it is one line of logic, which is
// the point of having settled it. A path other than the base path was typed,
// clicked or pasted moments ago; the saved place is where this reader was
// yesterday, possibly on another machine. The explicit, more recent request wins
// — the same call launch.go already makes for a manifest shortcut, for the same
// reason, and this leaves that behaviour untouched: a shortcut lands on the bare
// path, so its `?view=` is read exactly as before.
//
// A30 is not weakened by this. The preference keeps being written on every
// navigation (rememberScope), so the reader who opens the app fresh — the bare
// address, which is what a bookmark to the app itself and every launcher icon
// produce — still lands where they left off, on any machine.
// `addressed` reports which of the two answered. Callers need it because the
// preferences arrive a SECOND time, a round trip after mount, and the effect that
// applies them has to know not to overwrite a destination the address already
// chose — see reader.go's prefs effect.
func bootRoute(saved map[string]string, tr i18n.Runtime) (boot route, addressed bool) {
	base := platform.BasePath()
	path, query := platform.Path(), platform.Query()

	if len(pathSegments(base, path)) == 0 {
		// A bare address. Resume, exactly as before this file existed.
		return route{sel: resumeScope(saved, tr), item: saved["read.item"]}, false
	}

	r := parseRoute(base, path, query, tr)

	// The slideshow is addressed while it runs and is never STARTED by an address
	// at boot.
	//
	// Not an oversight and not a limitation of the codec — it is showOpen's own
	// documented decision, which this must not quietly reverse: a display that
	// seizes the screen and goes fullscreen before the reader has touched anything
	// is not what they asked for by opening a link, and the browser would refuse
	// the fullscreen request anyway, leaving a mode running with no visible way
	// out. Arriving at /slideshow therefore opens the place underneath it, and the
	// reader starts the show themselves. Back and Forward DO start it (see apply),
	// because by then the reader is present and pressing keys.
	if r.dlg == dialogShow {
		r.dlg = dialogNone
	}
	return r, true
}

// bootPane is which pane Reader mounts showing: the settings surface when the
// address named it, the item list otherwise.
//
// Settings REPLACES the reading panes rather than floating over them, so this is
// how /settings/appearance opens on Appearance instead of opening the reader and
// then visibly switching a frame later.
func bootPane(boot route) view {
	if boot.tab != "" {
		return viewSettings
	}
	return viewList
}

// bootTab is the settings tab Reader mounts with. The default is unchanged for
// every reader who did not arrive at a settings address.
func bootTab(boot route) string {
	if boot.tab != "" {
		return string(boot.tab)
	}
	return string(setReading)
}

// bootDialog opens the dialog an address named, once there is a client to fetch
// with.
//
// Separate from the rest of boot and applied later for the reason launch.go's
// `add` shortcut already is: openFeedSettings FETCHES, so seeding fsOpen at mount
// would render the panel with nothing in it and no request in flight. Applied
// after the item list has been asked for rather than instead of it, so dismissing
// the dialog leaves the reader somewhere rather than on an empty screen.
//
// dialogShow is absent by construction — bootRoute has already cleared it.
func bootDialog(boot route, a *actions) {
	if a == nil {
		return
	}
	switch boot.dlg {
	case dialogAdd:
		a.openAddFeed()
	case dialogFeed:
		a.openFeedSettings(boot.dlgID)
	case dialogTag:
		a.openTagSettings(boot.dlgID)
	}
}
