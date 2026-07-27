//go:build js && wasm

package view

import (
	"context"
	"strconv"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// The two delegated click listeners, and why there are only two.
//
// Extracted from reader.go, which held a single six-thousand-line component. This
// section moves cleanly because its dependency surface is small — eleven of Reader's
// state handles, listed on delegatedClicks below — and because it is a LEAF: it reads
// handles and calls actions, and nothing else in the component reads anything it
// declares.
//
// # The hook contract this has to keep
//
// GWC hooks are positional: they must be called unconditionally, in the same order,
// on every render. Moving UseEffect calls into a function is therefore safe only if
// that function is itself called unconditionally from the same place in Reader — which
// is exactly how it is called. Nothing here may become conditional, and neither may the
// call site.
//
// The dependencies are a struct rather than eleven parameters so that adding one is a
// named field at the call site instead of a positional argument in the right slot among
// a dozen, which is the version of this that silently swaps two handles of the same type.

type delegatedClicks struct {
	tr      i18n.Runtime
	act     ui.Ref[*actions]
	client  ui.State[*data.Client]
	busy    ui.State[string]
	pane    ui.State[view]
	current ui.State[*pb.Item]
	stream  ui.State[[]*pb.Item]
	feeds   ui.State[[]*pb.Feed]
	folders ui.State[[]*pb.Folder]
	tags    ui.State[[]*pb.Tag]
	fsData  ui.State[*pb.FeedSettings]
}

// wire registers the listeners. Called once, unconditionally, from Reader.
func (d delegatedClicks) wire() {
	// --- delegated clicks ---------------------------------------------------
	//
	// Two listeners, total, regardless of how many rows exist. Registered once,
	// because the container elements outlive every row inside them — which is
	// also why this survives pagination without re-registering.

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-item-id", func(id string) {
			ui.PostAsync(func() {
				a := d.act.Get()
				if it := a.itemByID(id); it != nil {
					a.open(it)
				}
			})
		})
		return l.Release
	}, []any{})

	// Wheel over a live view scrolls the REMOTE page (§10.1d).
	//
	// One listener for every live view there will ever be, registered once, for
	// the same reason as the clicks above. The deltas arrive already coalesced
	// to one callback per animation frame; this fires them at the server and
	// deliberately does not wait for or re-render on the reply.
	//
	// Nothing here touches state. A scroll that changed state would re-render
	// the article, and re-rendering the article rebuilds the <img> — which
	// restarts the d.stream, on every notch of the wheel.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedWheel("#app", "data-live-session", func(session string, dx, dy float64) {
			if session == "" {
				return
			}
			c := d.client.Get()
			if c == nil {
				return
			}
			go func() {
				// Fire and forget. A dropped scroll is a scroll the reader will
				// simply do again, and surfacing an error for one would put a
				// banner on screen for something they have already corrected.
				_, _ = c.ScrollLiveView(context.Background(), session, dx, dy)
			}()
		})
		return l.Release
	}, []any{})

	// Everything the panes offer, dispatched by data-action. One listener for the
	// whole shell, and it keeps working for elements that did not exist when it
	// was registered — which is what lets the d.pane helpers stay hook-free.
	// Article actions carry their subject.
	//
	// One listener reports data-for-item, which only the per-article controls
	// set; it fires alongside the data-action listener below and simply records
	// which article was named. Splitting it out keeps OnDelegatedClick reporting
	// exactly one attribute, which is what makes it simple enough to trust.
	forItem := ui.UseRef("")
	// forValue is the segmented controls' payload. Same pattern as forItem: one
	// more attribute listener rather than teaching OnDelegatedClick to report
	// two attributes, which is what keeps that helper simple enough to trust.
	forValue := ui.UseRef("")
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-for-item", func(id string) {
			forItem.Set(id)
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-value", func(v string) {
			forValue.Set(v)
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-action", func(action string) {
			// The click's payload is captured HERE, on the click's own stack, and
			// not inside the PostAsync below.
			//
			// This is the whole reason the two sibling listeners can be trusted.
			// They fire synchronously, immediately before this one, and write into
			// refs that every click shares — so the refs are correct for exactly as
			// long as the d.current JS task. Reading them one frame later, from
			// inside the deferred body, is reading them after the next click may
			// already have overwritten them.
			//
			// That is not theoretical. Two clicks inside one frame both queue a
			// body; the first body reads the SECOND click's id and then clears the
			// ref, so the second body reads an empty string and its action
			// silently does nothing — no error, no save, no sign it was dropped.
			// Frames are not always 16ms either: a throttled or d.busy tab stretches
			// the window this races in to seconds.
			//
			// Cleared here too, for the original reason: the id belongs to THIS
			// click, and leaving it set would make the next keyboard shortcut d.act
			// on an article the reader has long since scrolled past.
			id, value := forItem.Get(), forValue.Get()
			forItem.Set("")
			forValue.Set("")

			// PostAsync, always. These handlers run outside GWC's own event
			// dispatch, so calling State.Set directly schedules the update on a
			// path the reconciler does not coalesce — the visible result was
			// selection flipping back and forth and a row greying out a beat
			// after the click instead of with it.
			ui.PostAsync(func() {
				a := d.act.Get()
				switch action {
				case "back-rail":
					a.backRail()
				case "back-list":
					a.backList()
				case "more":
					a.more()
				case "like":
					a.rate(id, 1)
				case "dislike":
					a.rate(id, -1)
				case "open-original":
					a.openExtern(id)
				case "refresh":
					a.refresh()
				case "mark-all":
					a.markAll()
				case "toggle-unread":
					a.toggleUnread()
				case actReconnect:
					// The reader is looking at the countdown and is more
					// certain than we are. Kick resets the backoff and
					// re-dials; without it Connect would be a no-op, because
					// in TRANSIENT_FAILURE the backoff timer owns the redial.
					if c := d.client.Get(); c != nil {
						c.Kick()
					}
				case actSignIn, actReload:
					// Both are a reload, and they are still two actions.
					//
					// §7.1b's interceptor has already cleared the token by the
					// time a session ends, so Root will mount Login rather than
					// the reader; a d.client the server will not talk to cannot
					// fix itself by asking again either (§22.10). What differs
					// is the LABEL, which is the part the reader acts on, and
					// what will differ later: skew owes a Service Worker cache
					// purge before the reload, and there is no Service Worker
					// yet (§12.3). Collapsing them now would bury that.
					platform.Reload()
				case actAddOpen:
					a.openAddFeed()
				case actAddClose:
					a.closeAddFeed()
				case actAddSubmit:
					a.addFeed()
				case actAddFolder:
					a.pickAddFolder(value)
				case actAddNewCat:
					a.toggleAddNewCat()
				case actAddCandidate:
					a.addCandidate(value)
				case actAddSmart:
					a.toggleSmartFollow()
				case actAddAnalyze:
					a.analyzeSite(true)
				case actAddFollow:
					a.followPage()
				case actCatToggle:
					a.toggleCategory(value)
				case actCatNew:
					a.newCategory()
				case actCatOpen:
					a.openCategory(value)
				case actCatClose:
					a.closeCategory()
				case actCatSave:
					a.saveCategory()
				case actCatDelete, actCatConfirm:
					a.deleteCategory()
				case "fs-folder":
					// The chip names the feed in data-for-item and the category in
					// data-value; an empty category is "No category", which is a
					// choice rather than a missing value.
					a.setFeedFolder(id, value)
				case "toggle-feed-filter":
					a.toggleFeedFilter()
				case actClassifyCatToggle:
					// The chip names the category slug in data-for-item, the same
					// attribute the per-feed and per-tag settings gears use.
					a.toggleCategoryVisible(id)
				case actStreams, actFeeds, actTags, actCats:
					a.toggleRailSection(action)
				case "add-tag":
					a.addTag(id)
				case "remove-tag":
					// The chip carries the name in data-value; the article it was
					// clicked in names the feed.
					a.removeTag(id, value)
				case "expand":
					a.expand(id)
				case "toggle-page":
					a.togglePage(id)
				case "toggle-page-wide":
					a.togglePageWide(id)
				case "page-mode-doc":
					a.setPageMode(id, false)
				case "page-mode-live":
					a.setPageMode(id, true)
				case "toggle-note":
					a.toggleNote(id)
				case "toggle-revisions":
					a.toggleRevisions(id)
				case "modal-keep":
					// A click inside an open dialog. It exists only to stop the
					// delegated walk reaching the backdrop's close action.
				case "palette-close":
					a.closePalette()
				case "help-close":
					a.closeHelp()
				case "help-open":
					a.toggleHelp()
				case "undo-mark-all":
					a.undoMarkAll()
				case "open-settings":
					a.showSettings()
				case "settings-tab":
					a.settingsTabTo(value)
				case "settings-refresh":
					a.loadStats()
				case "settings-loglevel":
					a.setLogLevel(value)
				case actSmartKeySave:
					a.saveSmartKey()
				case actSmartKeyClear:
					a.clearSmartKey()
				case actSmartModel:
					a.saveSmartModel()
				case actSmartFeedPlus:
					a.toggleFeedPlus()
				case actSmartLang:
					// "" is a real value: it is English.
					a.setLocale(value)
				case actSmartRetranslate:
					a.retranslate()
				case actFocus:
					a.toggleFocus()
				case "toggle-mark-past":
					a.toggleMarkPast()
				case "set-theme":
					a.setTheme(value)
				case "set-accent":
					// "" is a real value here — it means "the theme's own
					// accent" — so it is passed through rather than guarded.
					a.setAccent(value)
				case "set-reading":
					a.setReading(value)
				case "toggle-motion":
					a.toggleMotion()
				case "motion-system":
					a.motionSystem()
				case actComposeTheme:
					a.composeTheme()
				case actDropCustom:
					a.dropCustom()
				case actAttune:
					a.toggleAttune()
				case actAttuneSmart:
					a.toggleAttuneSmart()
				case actAttuneReset:
					a.resetAttune()
				case "feed-settings":
					a.openFeedSettings(id)
				case "feed-settings-close":
					a.closeFeedSettings()
				case "fs-rename":
					// Read from the DOM rather than from state: this is the same
					// field Enter commits, and both paths must send the same
					// value. An empty string is meaningful — it clears the
					// override — so it is sent rather than skipped.
					v := platform.FieldValue("feed-title")
					a.patchFeed(&pb.UpdateFeedSettingsRequest{SourceId: id, Title: &v})
				case "fs-unsubscribe":
					a.unsubscribe(id)
				case actTagSettings:
					a.openTagSettings(id)
				case actTagSettingsClose:
					a.closeTagSettings()
				case actTagRename:
					// Read from the DOM for the same reason fs-rename does: this
					// is the field Enter also commits, and both paths have to
					// send the same value. An empty string is meaningful — it
					// clears the override — so it is sent rather than skipped.
					v := platform.FieldValue("tag-label")
					a.patchTag(&pb.UpdateTagRequest{TagId: id, Label: &v})
				case actTagGlyph:
					// The picker's "default" cell carries a sentinel rather than
					// an empty data-value: an empty attribute is indistinguishable
					// from a missing one once it has been through closest() and
					// getAttribute, and "the reader chose the default" must not
					// arrive as "the reader clicked nothing".
					g := value
					if g == glyphNone {
						g = ""
					}
					a.patchTag(&pb.UpdateTagRequest{TagId: id, Glyph: &g})
				case "fs-refresh":
					// The same refresh the toolbar runs, scoped to this feed.
					a.pick(scope{SourceID: id, Title: fsName(a, id)})
					a.refresh()
				case "fs-markall":
					a.pick(scope{SourceID: id, Title: fsName(a, id)})
					a.markAll()
				case "fs-megafeed":
					if s := d.fsData.Get(); s != nil {
						v := !s.GetInMegafeed()
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, InMegafeed: &v})
					}
				case "fs-poll":
					if v, err := strconv.Atoi(value); err == nil {
						n := int32(v)
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, FetchIntervalS: &n})
					}
				case "fs-cache":
					if v, err := strconv.Atoi(value); err == nil {
						n := int32(v)
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, CacheDepth: &n})
					}
				case "fs-mute":
					if v, err := strconv.Atoi(value); err == nil {
						until := ""
						if v > 0 {
							until = hoursFromNow(v)
						}
						a.patchFeed(&pb.UpdateFeedSettingsRequest{
							SourceId: id, MutedUntil: &until})
					}
				case "listen":
					a.listen(id)
				case "listen-pause":
					a.listenPause()
				case "listen-stop":
					a.listenStop()
				case "listen-jump":
					a.listenJump(id)
				case "toggle-smart-voice":
					a.smartVoice()
				case "toggle-digest":
					a.digestVoice()
				case actSlideOpen:
					a.slideStart()
				case actSlideLeave:
					a.slideStop()
				case actSlidePause:
					a.slidePause()
				case actSlideNext:
					a.slideStep(1)
				case actSlidePrev:
					a.slideStep(-1)
				case actSlideListen:
					a.slideListen()
				case actSlideDwell:
					a.slideSetDwell(value)
				case actVibe:
					a.setVibe(value)
				case actSlideNeeds:
					a.slideNeeds()
				case actSlideNeedsFix:
					a.slideNeedsFix(value)
				case actSlideNeedsStart:
					a.slideNeedsStart()
				case actSlideNeedsClose:
					a.slideNeedsClose()
				case "toggle-podcast":
					a.podcastVoice()
				case "toggle-autoplay":
					a.autoPlay()
				case "read-later":
					a.later(id)
				case "mark-unread":
					a.markUnread(id)
				case "tab-home":
					// Home is the reading surface, and on a phone that means the
					// list — not the article, which would drop you back into
					// whatever you last read rather than into today.
					a.pick(scope{Title: d.tr.T("stream", "all")})
				case "tab-feeds":
					a.showTab(viewRail)
				case "tab-notes":
					a.pick(scope{Title: d.tr.T("stream", "notes"), Notes: true})
				case "tab-settings":
					a.showTab(viewSettings)
				}
			})
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-tag-id", func(id string) {
			ui.PostAsync(func() {
				for _, t := range d.tags.Get() {
					if t.GetId() == id {
						d.act.Get().pickTag(id, t.GetName())
						return
					}
				}
			})
		})
		return l.Release
	}, []any{})

	// A category's own row. Its own attribute rather than reusing data-tag-id,
	// because the two resolve to different scopes and a shared attribute would
	// make "which kind of thing is this" a guess at the click.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-folder-id", func(id string) {
			ui.PostAsync(func() {
				a := d.act.Get()
				if id == unfiledID {
					a.pickFolder(id, d.tr.T("stream", "unfiled"))
					return
				}
				for _, f := range d.folders.Get() {
					if f.GetId() == id {
						a.pickFolder(id, f.GetName())
						return
					}
				}
			})
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-source-id", func(id string) {
			ui.PostAsync(func() {
				a := d.act.Get()
				switch id {
				case streamAll:
					a.pick(scope{Title: d.tr.T("stream", "all")})
				case streamMyFeed:
					a.pick(scope{Title: d.tr.T("stream", "myFeed"), MyFeed: true})
				case streamUnread:
					a.pick(scope{Title: d.tr.T("stream", "unread"), Unread: true})
				case streamLiked:
					a.pick(scope{Title: d.tr.T("stream", "liked"), Rating: 1})
				case streamLater:
					a.pick(scope{Title: d.tr.T("stream", "later"), Later: true})
				case streamNotes:
					a.pick(scope{Title: d.tr.T("stream", "notes"), Notes: true})
				default:
					if f := a.feedByID(id); f != nil {
						a.pick(scope{SourceID: id, Title: f.GetTitle()})
					}
				}
			})
		})
		return l.Release
	}, []any{})

}
