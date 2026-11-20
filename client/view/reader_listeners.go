//go:build js && wasm

package view

import (
	"context"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/platform"
	"github.com/monstercameron/ArticleFlux/client/track"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// The reading surface's DOM listeners, and the effects that drive the list's
// virtualisation.
//
// Extracted from reader.go on delegatedClicks' terms (reader_clicks.go): it is
// a LEAF — every effect here reads Reader's handles and calls actions through
// the Ref, and nothing else in the component reads anything declared here — and
// its whole dependency surface is the struct below.
//
// # The hook contract this has to keep
//
// Twenty hooks, in the order they ran inline. wire() is called
// unconditionally from one place in Reader, so they keep their slots; nothing
// in here may become conditional, and no hook may be added in the middle of the
// sequence without accepting that every slot after it moves.
//
// The bodies moved verbatim. wire() opens by binding each field to the name the
// code used inside Reader, so what was reviewed there is what runs here, and a
// reader comparing the two sees the same lines.

// readerListeners is wire()'s dependency surface.
type readerListeners struct {
	act     ui.Ref[*actions]
	tracker ui.Ref[*track.Collector]
	// openAt is Reader's openAt for THIS render. It is a closure over the render's
	// state, which is exactly what the auto-open effect wants: an effect closure is
	// rebuilt every render, so the openAt it calls is always the current one.
	openAt func(it *pb.Item, focus bool)
	// client is what asks the server for an article the list does not hold.
	client ui.State[*data.Client]
	sel    ui.State[scope]

	current      ui.State[*pb.Item]
	stream       ui.State[[]*pb.Item]
	items        ui.State[[]*pb.Item]
	itemsLoading ui.State[bool]
	// resumeItem is the article to reopen once a list lands — see autoOpenPick.
	resumeItem ui.Ref[string]

	speakID      ui.State[string]
	speakVisible ui.State[bool]

	scrollTop   ui.State[float64]
	viewport    ui.State[float64]
	loadingMore ui.State[bool]

	// The Activity tab's log, virtualised the same way as the reading list.
	setTab       ui.State[string]
	serverLogs   ui.State[[]*pb.LogRecord]
	logScrollTop ui.State[float64]
	logViewport  ui.State[float64]
}

// wire installs the listeners. Called once per render, unconditionally, from
// Reader.
func (d readerListeners) wire() {
	act, tracker, openAt := d.act, d.tracker, d.openAt
	current, stream, items, itemsLoading := d.current, d.stream, d.items, d.itemsLoading
	resumeItem, speakID, speakVisible := d.resumeItem, d.speakID, d.speakVisible
	scrollTop, viewport, loadingMore := d.scrollTop, d.viewport, d.loadingMore
	setTab, serverLogs := d.setTab, d.serverLogs
	logScrollTop, logViewport := d.logScrollTop, d.logViewport

	// Note and tag fields, delegated.
	//
	// There is one of each per article in the stream now, so a handler that only
	// receives the typed text cannot tell which article it belongs to. These
	// listeners read the field's own data attribute, which is the id.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedInput("#app", "data-note-id", func(id, body string) {
			// Recorded on this stack, before the PostAsync, so a tab that closes in
			// the next frame still knows what was typed (G20).
			act.Get().noteTyped(id, body)
			ui.PostAsync(func() { act.Get().editNote(id, body) })
		})
		return l.Release
	}, []any{})

	// Closing or reloading the tab with a note still in its 800ms debounce used
	// to lose the text (G20). The outbox takes it synchronously instead.
	ui.UseEffect(func() func() {
		l := platform.OnPageHide(func() { act.Get().flushNotes() })
		return l.Release
	}, []any{})

	// Leaving a note field saves it immediately rather than waiting out the
	// debounce. Clicking away IS finishing, and the article the reader clicked
	// away to may well scroll this one out of the stream.
	//
	// The value comes from the event rather than from the draft map: focusout
	// can arrive before the last input event has been through PostAsync, and
	// reading state here would save the text one keystroke behind what the
	// reader can see in the field they just left.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedBlur("#app", "data-note-id", func(id, body string) {
			ui.PostAsync(func() {
				act.Get().editNote(id, body)
				act.Get().saveNote(id)
			})
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedInput("#app", "data-tag-source", func(src, name string) {
			ui.PostAsync(func() { act.Get().editTag(src, name) })
		})
		return l.Release
	}, []any{})

	// Continuous reading, in both directions.
	//
	// Approaching the bottom APPENDS the next article below the one being read;
	// approaching the top brings the previous one back above it. This is the whole
	// reading loop for a firehose: scroll until you stop caring, and everything
	// you scrolled past is read — without anything you have read being taken away
	// from under you.
	//
	// 800px of slack rather than 24: the next article has to be built and its body
	// fetched, and arriving at the exact bottom before starting means the reader
	// hits a wall and waits. A screenful of warning makes it seamless.
	//
	// Edge-triggered, so each fires once on arriving in the zone rather than
	// repeatedly while sitting in it — otherwise a short article would race
	// through the rest of the list on one flick of the wheel.
	ui.UseEffect(func() func() {
		l := platform.OnScrollNearEnd("#app", ".pane-article", 800, func() {
			ui.PostAsync(func() { act.Get().advance() })
		})
		return l.Release
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnScrollNearTop("#app", ".pane-article", 400, func() {
			ui.PostAsync(func() { act.Get().retreat() })
		})
		return l.Release
	}, []any{})

	// The palette's rows, on their own attribute so the one handler gets both the
	// kind and the id without a second lookup.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-pal", func(spec string) {
			ui.PostAsync(func() { act.Get().runPalette(spec) })
		})
		return l.Release
	}, []any{})

	// The two observers only a browser can answer (§18.1a).
	//
	// Registered once with no dependencies and reaching the collector through
	// the Ref, for the same reason as every other listener in this file: a
	// closure captured on the first render would hold a nil collector forever
	// and the signals would silently never be recorded.
	ui.UseEffect(func() func() {
		// Travelling back UP through a paragraph. Rare, and one of the strongest
		// positives a reader emits without meaning to.
		back := platform.OnBackScroll("#app", ".pane-article", func() {
			ui.PostAsync(func() {
				if t, c := tracker.Get(), current.Get(); t != nil && c != nil {
					t.Emit(signals.Reread, c.GetId(), c.GetSourceId(), 1,
						signals.SurfaceReader, "")
				}
			})
		})
		// Selecting a passage means quoting it or looking something up. The
		// LENGTH is recorded and the text is not — see the privacy note at the
		// foot of internal/signals.
		text := platform.OnTextSelection(func(chars int) {
			ui.PostAsync(func() {
				if t, c := tracker.Get(), current.Get(); t != nil && c != nil {
					t.Emit(signals.Selected, c.GetId(), c.GetSourceId(),
						float64(chars), signals.SurfaceReader, "")
				}
			})
		})
		return func() { back.Release(); text.Release() }
	}, []any{})

	// The attention gate, the page-hide flush, the impression poll and the
	// periodic ship. One effect, because all four share a lifetime and all four
	// are meaningless without a collector.
	//
	// The 500ms poll is what makes an impression mean "was on screen for about a
	// second" rather than "went past": track.ImpressionMinTicks requires two
	// consecutive sightings. It reads geometry and touches no component state,
	// so a tick that finds nothing new costs one querySelectorAll.
	ui.UseEffect(func() func() {
		t := tracker.Get()
		if t == nil {
			return nil
		}
		ls := t.Start()
		stop := make(chan struct{})

		go func() {
			tick := time.NewTicker(500 * time.Millisecond)
			defer tick.Stop()
			ticks := 0
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
					t.Saw(platform.VisibleAttrs(".pane-list", "data-item-id"),
						func(id string) string {
							if it := act.Get().itemByID(id); it != nil {
								return it.GetSourceId()
							}
							return ""
						}, signals.SurfaceList)
					// Ship about every ten seconds: often enough that closing a
					// laptop loses little, rare enough that reading is not a
					// stream of RPCs.
					if ticks++; ticks >= 20 {
						ticks = 0
						t.Tick()
					}
				}
			}
		}()

		return func() {
			close(stop)
			t.Stop(ls)
		}
	}, []any{tracker.Get() != nil})

	// Reaching the end of an article IS reading it.
	//
	// Distinct from the topmost-article handler: a reader who scrolls to the last
	// paragraph and stops has finished that article, and making them scroll into
	// the next one before it counts is the app arguing with them about what they
	// just did.
	ui.UseEffect(func() func() {
		l := platform.OnScrolledPast("#app", ".pane-article", "data-article-id",
			func(id string) {
				ui.PostAsync(func() { act.Get().readArticle(id) })
			})
		return l.Release
	}, []any{})

	// Watch whether the playing article's own transport is still on screen.
	//
	// Re-established whenever the article being read changes, because the thing
	// being watched changes with it — the observer is bound to one element, and
	// that element belongs to one article.
	//
	// Nothing playing means nothing to watch, and the state is forced back to
	// "visible" rather than left where it was: a stale false would keep the
	// floating transport up after the audio stopped.
	ui.UseEffect(func() func() {
		id := speakID.Get()
		if id == "" {
			speakVisible.Set(true)
			return nil
		}
		l := platform.WatchVisible(".pane-article", `[data-listen-for="`+id+`"]`,
			func(visible bool) {
				// Through the Ref, not the captured state: see actions.speakSeen.
				ui.PostAsync(func() {
					if f := act.Get().speakSeen; f != nil {
						f(visible)
					}
				})
			})
		return l.Release
	}, []any{speakID.Get()})

	// A click inside an article is also a claim to have read it. Cheap, obvious,
	// and the only way to say so without scrolling on a short article that never
	// leaves the viewport.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#app", "data-article-id", func(id string) {
			ui.PostAsync(func() { act.Get().readArticle(id) })
		})
		return l.Release
	}, []any{})

	// Which article the reader is on. In a stream that is a scroll position, not
	// a click — and it is what the title, the star, the highlighted row and
	// "this has been read" all follow.
	ui.UseEffect(func() func() {
		l := platform.OnTopmostChild("#app", ".pane-article", "data-article-id",
			func(id string, moved bool) {
				ui.PostAsync(func() { act.Get().focusArticle(id, moved) })
			})
		return l.Release
	}, []any{})

	// Open something as soon as a list arrives.
	//
	// "Pick something to read" is a correct description of an empty pane and a
	// waste of the pane: the reader chose a feed, so the thing they want is the
	// top of it. First UNREAD rather than first item, because in a feed you have
	// been through, the first row is usually something you have already read and
	// showing it again is the reader arguing with you about what is new.
	//
	// Driven by a render rather than by the fetch callback so it sees the list
	// that was actually committed, and guarded on an empty stream so it never
	// fights the reader — once anything is open, this stops having an opinion.
	ui.UseEffect(func() func() {
		if itemsLoading.Get() || len(stream.Get()) > 0 {
			return nil
		}
		list := items.Get()
		if len(list) == 0 {
			return nil
		}
		// The saved article is consumed whether or not it is still in this list:
		// it names where the reader WAS, and a later list that happens to hold it
		// is not that place.
		want := resumeItem.Get()
		if strings.HasPrefix(want, askingMark) {
			// Already asked for; the answer is on its way, and a second effect run
			// must not open the first unread underneath it.
			return nil
		}
		if want != "" {
			resumeItem.Set("")
		}
		if want != "" && !listHolds(list, want) {
			// A link to an article this page of the list does not hold — an older
			// one, or one named without its feed. Opening the first unread instead
			// lands the reader on a different article from the one in their hand,
			// so the one named is asked for, and the fallback is only for a server
			// that has no such article, or one that is not in this feed.
			first := autoOpenPick(list, "")
			feedID := d.sel.Get().SourceID
			resumeItem.Set(askingMark + want)
			go func() {
				var found *pb.Item
				if c := d.client.Get(); c != nil {
					if it, _, err := c.GetItemCached(context.Background(), want); err == nil &&
						(feedID == "" || it.GetSourceId() == feedID) {
						found = it
					}
				}
				// Through the Ref: the answer lands renders after the one that asked.
				ui.PostAsync(func() {
					resumeItem.Set("")
					if len(d.stream.Get()) > 0 {
						return
					}
					if found == nil {
						found = first
					}
					act.Get().openUnfocused(found)
				})
			}()
			return nil
		}
		openAt(autoOpenPick(list, want), false)
		return nil
	}, []any{len(items.Get()), len(stream.Get()), itemsLoading.Get()})

	// Keep the loaded prefix ahead of the viewport, once per render.
	//
	// The chain that carries a long drag has to be driven by RENDERS, not by
	// calling fill again from inside the response handler. A handler created in
	// render N still reads render N's state, so the second link in the chain
	// asked "how many items are loaded?" and got the answer from before the first
	// page arrived — it concluded it was already far enough ahead and stopped.
	// Dragging to row 150 worked (one page) and dragging to row 400 did not (two).
	//
	// An effect closure is rebuilt every render, so its reads are current by
	// construction. Each page that lands causes a render, that render asks again,
	// and the chain continues until the prefix covers the viewport or the cursor
	// runs out. loadMore refuses re-entry, so this costs two comparisons on the
	// renders where there is nothing to do.
	ui.UseEffect(func() func() {
		act.Get().fill(scrollTop.Get())
		return nil
	}, []any{len(items.Get()), scrollTop.Get(), viewport.Get(), loadingMore.Get()})

	// Virtualisation inputs. rAF-throttled, so this is at most one state write
	// per painted frame rather than one per scroll event.
	ui.UseEffect(func() func() {
		l := platform.OnScrollMetrics("#app", ".list-scroll", func(top, view, _ float64) {
			ui.PostAsync(func() {
				scrollTop.Set(top)
				if view > 0 {
					viewport.Set(view)
				}
				// Through the Ref, never the closure: this listener is registered
				// once and would otherwise hold the first render's fillTo forever.
				act.Get().fill(top)
			})
		})
		return l.Release
	}, []any{})

	// The same pair for the Activity tab's log, which is virtualised for the same
	// reason (view.settingsActivity).
	//
	// A second listener rather than a second selector on the first: the callback
	// writes to different state and the two lists are never scrolled at the same
	// time, so sharing one would mean asking "which element was that?" on every
	// frame of every scroll in the app.
	ui.UseEffect(func() func() {
		l := platform.OnScrollMetrics("#app", ".log-list", func(top, view, _ float64) {
			ui.PostAsync(func() {
				logScrollTop.Set(top)
				if view > 0 {
					logViewport.Set(view)
				}
			})
		})
		return l.Release
	}, []any{})

	// And its measurement. The log list is not in the document until the Activity
	// tab is open AND the records have landed, so this cannot be a mount-time
	// one-shot like the reading list's below — it re-runs on both, and re-arms
	// when the tab is left so that a different window size is picked up the next
	// time the tab is opened.
	logMeasured := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if setTab.Get() != string(setActivity) {
			logMeasured.Set(false)
			return nil
		}
		if logMeasured.Get() {
			return nil
		}
		if h := platform.ElementHeight(".log-list"); h > 0 {
			logMeasured.Set(true)
			logViewport.Set(h)
			// A tab opened at yesterday's scroll offset would show the middle of
			// the log with no way to tell that is where it started.
			logScrollTop.Set(0)
		}
		return nil
	}, []any{setTab.Get(), len(serverLogs.Get())})

	// The first render has no scroll event to learn the viewport height from, so
	// it is measured once after mount. Without this the initial window is sized
	// from a guess and the list renders too few or too many rows.
	measured := ui.UseRef(false)
	ui.UseEffect(func() func() {
		if measured.Get() {
			return nil
		}
		if h := platform.ElementHeight(".list-scroll"); h > 0 {
			measured.Set(true)
			viewport.Set(h)
		}
		return nil
	}, []any{len(items.Get()) > 0})

	// An armed confirmation does not outlive the reader's attention to it. Arming
	// is a question, and a press anywhere else is the reader having moved on:
	// left armed, "Confirm reset?" sits on the screen for as long as the tab does
	// and the next stray press on it is the destructive one. Anything outside the
	// armed controls themselves counts, which is what makes this one rule for all
	// of them rather than a clause per control.
	ui.UseEffect(func() func() {
		l := platform.OnPressOutside(armedInlineSelector, func() {
			ui.PostAsync(func() {
				if a := act.Get(); a != nil && a.disarmInline != nil {
					a.disarmInline()
				}
			})
		})
		return l.Release
	}, []any{})

	// The article list is ONE Tab stop, not one per row (H11, FEATURES §5): every
	// row is tabindex -1 in the markup (itemRowAttrs) and this gives tabindex 0 to
	// the current row after every render — or to the first rendered row when the
	// current article is outside the virtual window or there is none — so Tab
	// visits the list once and j/k and the arrows move inside it. The Go Blog's ten
	// rows were ten Tab presses between the list header and the article. After
	// EVERY render, no dependency list, because the window slides under a scroll
	// and a row that was the stop may no longer be in the document.
	ui.UseEffect(func() func() {
		platform.RovingSync(".list-scroll", ".item-row", ".item-row[aria-current='true']", ".item-row")
		return nil
	})
}

// The rail's keyboard vocabulary, in selectors. Primary items are what the up
// and down arrows visit; a group is a row with the controls beside it, which
// left and right step across.
const (
	railRovingItems   = ".rail-head button, .rail-scroll button"
	railPrimaryItems  = ".feed-row, .rail-band-toggle"
	railControlGroups = ".feed-slot, .rail-band"
)

// autoOpenPick is which article opens by itself when a list lands on an empty
// reading pane: the saved article if it is in this list, else the first unread
// one, else the first.
//
// The saved article wins because it is the reader picking up exactly where they
// left off, which no heuristic beats. First UNREAD rather than first otherwise,
// because in a feed you have been through the first row is usually something
// you have already read. Pulled out of the effect that calls it so the rule can
// be pinned without a DOM; the effect owns the side of it that is not a choice —
// consuming the saved id, and opening.
//
// nil only for an empty list, which the caller never passes.
func autoOpenPick(list []*pb.Item, saved string) *pb.Item {
	if len(list) == 0 {
		return nil
	}
	if saved != "" {
		for _, it := range list {
			if it.GetId() == saved {
				return it
			}
		}
	}
	for _, it := range list {
		if !it.GetRead() {
			return it
		}
	}
	return list[0]
}

// askingMark prefixes resumeItem while the article it names is being fetched, so
// the auto-open effect can tell "nothing asked for" from "asked, not answered".
const askingMark = "\x00"

// listHolds reports whether the loaded list has an article with this id.
func listHolds(list []*pb.Item, id string) bool {
	for _, it := range list {
		if it.GetId() == id {
			return true
		}
	}
	return false
}
