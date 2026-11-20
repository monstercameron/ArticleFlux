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
)

// Discover (§18.7, M16): sites the reader does not follow yet.
//
// # Mounted as a settings-style tab, not a route (Cam, 2026-08-01)
//
// route.go's single mounted-tree router is the application's address grammar,
// and /discover is deliberately NOT a new entry in it: this is a flip surface
// over whatever the reader is doing, the same way Smart+ and every other
// settings tab is, not a place with its own address. It renders inside
// settingsPane (settings.go's setDiscover case) when pane==viewSettings and
// setTab==setDiscover, reached by the "open-discover" delegated action
// (panes.go's chip-mini button, reader_clicks.go's dispatch) — the same
// showSettings+settingsTabTo chain slideNeeds uses to land on setPodcast.
//
// # Every card explains itself
//
// recommend.go's whole argument is that an unexplained suggestion is worth
// less than none, and that is why Evidence is rendered verbatim rather than
// re-derived from score/rung — the sentence the server built IS the reason,
// and a client that reconstructed its own would be able to disagree with it.

// DiscoverProps carries the already-connected client, exactly as ReaderProps
// and DemoProps do — the connection is dialled once, above this component.
type DiscoverProps struct {
	Client *data.Client
	// NoKey is the server reporting it has no model key, so Smart+ review cannot
	// run: the switch is not offered and the page says why, the way the add-a-feed
	// dialog stops recommending Smart+ follow once the server has said no_key.
	NoKey bool
}

// Delegated-click attributes (§ "GWC delegated click payload" — a per-row
// ui.UseEvent cannot exist here: hooks are positional and this list's length
// varies, so the cards below carry the domain as a data attribute and TWO
// listeners, registered once, read it — the same pattern reader_clicks.go
// uses for item rows, at client/platform.OnDelegatedClick.
const (
	attrDiscoverAccept = "data-discover-accept"
	// The data-roles focus is handed to when a card leaves (G14): the card that
	// takes its place, or Refresh when it was the last.
	roleDiscoverFollow    = "rec-follow-"
	roleDiscoverRefresh   = "discover-refresh"
	attrDiscoverReject    = "data-discover-reject"
	attrDiscoverUndo      = "data-discover-undo"
	attrDiscoverRefresh   = "data-discover-refresh"
	attrDiscoverSmartPlus = "data-discover-smartplus"
)

// discoverSmartPlusPrefKey mirrors recommendjob.SmartPlusPrefKey exactly — a
// literal rather than an import, because that constant lives in a
// server-only package (internal/recommendjob) this client build cannot pull
// in. A test in recommendjob asserts the string itself doesn't drift, so this
// copy has one place that would fail loudly if it did
// (TestSmartPlusPrefKeyMatchesClientCopy).
const discoverSmartPlusPrefKey = "discover.smartPlus"

// pollDiscoverInterval matches internal/jobs.Pool's own default Idle — polling
// faster than the job pool itself looks at its queue only burns requests for
// no earlier an answer.
const pollDiscoverInterval = 1500 * time.Millisecond

// pollDiscoverAttempts bounds the wait: 6 rounds is ~9s, enough head start for
// the job pool to pick the job up and score it against ALREADY-VALIDATED
// candidates (rungs 1-3 read stored outlink evidence, no new network fetch)
// without leaving the button disabled for a long time on the common case
// where nothing new turns up. A slow rung-5 run that validates a brand-new
// site can still finish after this window closes — the button re-enables
// showing whatever the last poll saw, and the reader can press Refresh again
// rather than stare at a spinner for 20+ seconds.
const pollDiscoverAttempts = 6

func domainSet(recs []*pb.Recommendation) map[string]bool {
	out := make(map[string]bool, len(recs))
	for _, r := range recs {
		out[r.GetDomain()] = true
	}
	return out
}

func sameDomainSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for d := range a {
		if !b[d] {
			return false
		}
	}
	return true
}

// pollDiscoverUntilChanged re-lists after a refresh until the open set
// actually differs from before, or the attempt ceiling is hit — see the
// Refresh click handler for why a single immediate re-list is not enough.
//
// Runs off the render loop (an ordinary goroutine sleeping between attempts),
// exactly like refresh's own network call — ui.PostAsync is what's required to
// touch state safely, not which goroutine gets there.
func pollDiscoverUntilChanged(
	c *data.Client, g int, before map[string]bool,
	gen ui.Ref[int], recs ui.State[[]*pb.Recommendation],
	refreshing, loading, reviewDown ui.State[bool],
) {
	for attempt := 0; ; attempt++ {
		time.Sleep(pollDiscoverInterval)
		res, err := c.Recommendations(context.Background())
		done := attempt >= pollDiscoverAttempts-1
		ui.PostAsync(func() {
			if gen.Get() != g {
				return // a newer load()/refresh has already superseded this poll
			}
			if err != nil {
				if done {
					refreshing.Set(false)
					loading.Set(false)
				}
				return
			}
			after := res.GetRecommendations()
			changed := !sameDomainSet(before, domainSet(after))
			if changed || done {
				recs.Set(after)
				reviewDown.Set(res.GetReviewUnavailable())
				refreshing.Set(false)
				loading.Set(false)
			}
		})
		if gen.Get() != g || done {
			return
		}
	}
}

// discoverLeaveAnimMS must outlast design/sheet.go's discover-leave-* keyframe
// duration (var(--t3), 300ms at full motion amplitude) so the card is still on
// screen when the animation finishes rather than vanishing mid-transition —
// the gap is deliberate slack, not a rounding accident. At --mo:0 (motion off)
// the keyframe is instant and this just holds the card an extra beat, which is
// harmless.
const discoverLeaveAnimMS = 380 * time.Millisecond

// playDiscoverLeave marks domain as leaving (paints the accept/reject exit
// animation via discoverCard's data-leaving attribute) and removes it from
// the list only after the animation has had time to play — an immediate
// removeDomain is what made Accept/Reject feel instant and un-animated (Cam,
// 2026-08-01).
func playDiscoverLeave(
	domain, kind string, leaving, leavingKind ui.State[string], removeDomain func(string),
) {
	// A card still mid-exit when another leaves would be left on the page for
	// good: the timer below gives up on a domain that is no longer the one
	// leaving, which was a rule for a refresh moving on and became a hole once
	// Not for me stopped waiting for the server (two dismissals inside 380ms).
	if prev := leaving.Get(); prev != "" && prev != domain {
		removeDomain(prev)
	}
	leavingKind.Set(kind)
	leaving.Set(domain)
	go func() {
		time.Sleep(discoverLeaveAnimMS)
		ui.PostAsync(func() {
			if leaving.Get() != domain {
				return // superseded — a refresh or a second action already moved on
			}
			removeDomain(domain)
			leaving.Set("")
			leavingKind.Set("")
		})
	}()
}

// discoverUndoWindow is how long "Not for me" can be taken back. Long enough to
// notice the sentence, find the button and press it with a keyboard or a
// pointer-slow hand; short enough that the answer still reaches the server while
// the reader remembers having given it.
const discoverUndoWindow = 10 * time.Second

// heldReject is a dismissal the server has not been told about yet. Not for me is
// permanent (§18.7) and the contract has no way back, so instead of sending it
// and offering an Undo that would need a new RPC, the card leaves the page at once
// and the RPC waits out the window; Undo is then just putting the card back.
type heldReject struct {
	domain, name string
	rec          *pb.Recommendation
	at           int // where the card sat, so it returns to the same place
	timer        *time.Timer
}

// Discover fetches and renders the open recommendation list.
func Discover(p DiscoverProps) ui.Node {
	tr := i18n.UseI18n()

	loading := ui.UseState(true)
	refreshing := ui.UseState(false)
	loadErr := ui.UseState(false)
	recs := ui.UseState([]*pb.Recommendation(nil))
	// reviewDown is the server saying the last pass could not run Smart+'s
	// review on some of what it scored, so those suggestions stand on rungs
	// 1-3's evidence alone (G9). Without it they read as reviewed — the toggle
	// is on, the cards say "posts reviewed" for the ones that were, and nothing
	// marks the rest.
	reviewDown := ui.UseState(false)
	// busy holds the domain currently mid-Accept/Reject, so its own two
	// buttons disable without freezing every other card on the list.
	busy := ui.UseState("")
	// leaving/leavingKind hold the card mid-exit-animation and which one it
	// is ("accept"/"reject") — see discoverCard's data-leaving doc. Kept
	// separate from busy: busy clears the moment the RPC lands so OTHER
	// cards are immediately actionable again, while leaving persists a beat
	// longer so THIS card's animation has time to actually play before
	// removeDomain drops it from recs.
	leaving := ui.UseState("")
	leavingKind := ui.UseState("")
	// said is the sentence for the last Follow or Not for me, in a live region
	// that is always in the document (G26). Cleared by the next action.
	said := ui.UseState("")
	// undoOffer is the banner's sentence while the last "Not for me" can still be
	// taken back (G26); empty when there is nothing to take back. held is that
	// dismissal itself, a Ref because the page-hide and unmount paths must read it
	// without a render, and clientRef/alive give those same two paths the live
	// client and whether this page is still mounted.
	undoOffer := ui.UseState("")
	held := ui.UseRef((*heldReject)(nil))
	clientRef := ui.UseRef((*data.Client)(nil))
	clientRef.Set(p.Client)
	alive := ui.UseRef(true)
	// gen guards against a stale response landing after a newer request —
	// the same discipline loadFolders uses in reader.go, needed here because
	// Refresh can be clicked again before the first load returns.
	gen := ui.UseRef(0)
	// loadedFor is the one-shot guard the "GWC UseEffect deps re-run" gotcha
	// requires: deps are a hint, not a guarantee, and Discover sits inside
	// Reader, which re-renders on every unrelated keystroke/busy-flag/unread
	// count. Without this, the effect below re-ran load() on every one of
	// those re-renders — same p.Client, but the dep-array compare is not
	// trustworthy — which repeatedly flipped loading true→false and was the
	// visible flicker (Cam, 2026-08-01). Comparing against the actual client
	// pointer, not just "have we ever loaded", so a real reconnect (a new
	// *data.Client) still triggers a fresh load.
	loadedFor := ui.UseRef((*data.Client)(nil))
	// smartPlus is the "2 posts reviewed" gate's per-user opt-in (Cam,
	// 2026-08-01: "add a smart+ toggle on that page so it only works when
	// the user wants it to work") — recommendjob.SmartPlusPrefKey, read and
	// written here so the switch is next to the feature it gates rather than
	// buried in the general Smart+ settings tab. Off is the correct default
	// while unread: a reader who hasn't loaded prefs yet should not see the
	// toggle flash on then off.
	//
	// Gates the WHOLE page, not just the LLM calls (Cam, 2026-08-01, second
	// pass: "it should [not] load if the toggle is off") — with the toggle
	// off, Discover shows nothing and does not call ListRecommendations at
	// all, rather than silently falling back to the rung 1-2 deterministic
	// list. A reader who has not opted in should not be shown a feature that
	// only makes sense once they have.
	smartPlus := ui.UseState(false)
	smartPlusBusy := ui.UseState(false)
	// prefsLoaded distinguishes "we don't know yet" from "we know it's off" —
	// without it the gate message would flash on before GetPrefs answers,
	// every single time the tab opens.
	prefsLoaded := ui.UseState(false)

	load := func() {
		c := p.Client
		if c == nil {
			return
		}
		g := gen.Get() + 1
		gen.Set(g)
		loading.Set(true)
		loadErr.Set(false)
		go func() {
			res, err := c.Recommendations(context.Background())
			ui.PostAsync(func() {
				if gen.Get() != g {
					return
				}
				loading.Set(false)
				if err != nil {
					loadErr.Set(true)
					return
				}
				recs.Set(res.GetRecommendations())
				reviewDown.Set(res.GetReviewUnavailable())
			})
		}()
	}

	// startPrefsLoad reads the opt-in once per client, and is driven from the
	// render body rather than from an effect.
	//
	// # Why not UseEffect
	//
	// Because on the path that matters it never runs. Instrumented on the real
	// app: a Discover mounted by CLICKING the tab ran its effect (repeatedly,
	// in fact), while a Discover mounted by a RELOAD that restored the tab ran
	// it exactly ZERO times. The page then sat on its spinner forever, with a
	// Smart+ toggle that could not be operated at all, until the reader
	// happened to switch tabs and back — which remounts, and worked every time.
	//
	// The render body always runs (that is what makes it the wrong place for
	// side effects and the right place for this one). loadedFor is a Ref, so
	// recording the attempt costs no render and cannot loop, and every state
	// write below still goes through ui.PostAsync exactly as it did inside the
	// effect. Nothing about the load changed; only what is trusted to start it.
	startPrefsLoad := func(c *data.Client) {
		// A nil client must not consume the one-shot. loadedFor starts as a nil
		// *data.Client, so comparing before checking for nil made a
		// still-connecting page decide it had already done the work.
		if c == nil || loadedFor.Get() == c {
			return
		}
		loadedFor.Set(c)
		// Prefs are read FIRST, and ListRecommendations is only called once
		// the answer says the reader has opted in — the gate has to hold
		// before the first request goes out, not just before the result
		// renders, or a reader who never opted in would still trigger a real
		// RPC every time they open this tab.
		go func() {
			// Retried, because one attempt is measurably not enough.
			//
			// This page mounts while the tunnel is still coming up whenever a
			// reload restores it — Discover is a settings tab, and the tab the
			// reader was on is restored before the socket finishes handshaking.
			// The single-shot version failed on EVERY such reload, and the
			// failure was rendered as "off" (see below), so a reader who had
			// switched Smart+ review on found it switched off every time they
			// came back, and switching it on again wrote a value it already
			// had. That is the "the toggle can't be turned on" report.
			//
			// Backing off rather than hammering: ~4s in total across six
			// attempts, which is longer than the handshake takes and short
			// enough that a genuinely dead tunnel is not waited on for long.
			// Sleeping here is safe — this is a goroutine, not the JS event
			// loop, and the loop keeps painting while it waits.
			var prefs map[string]string
			var err error
			for attempt := 0; attempt < 6; attempt++ {
				if prefs, err = c.GetPrefs(context.Background()); err == nil {
					break
				}
				time.Sleep(time.Duration(200*(attempt+1)) * time.Millisecond)
			}
			ui.PostAsync(func() {
				if loadedFor.Get() != c {
					return
				}
				if err != nil {
					// A failed read is NOT "off". It says nothing at all about
					// the stored value, and rendering it as off both tells the
					// reader their opt-in is gone and arms the toggle with a
					// baseline nobody chose. The page stays in its "we don't
					// know yet" state, which is the true one; a reconnect hands
					// this effect a new *data.Client and it asks again.
					loading.Set(false)
					return
				}
				on := prefs[discoverSmartPlusPrefKey] == "true"
				smartPlus.Set(on)
				prefsLoaded.Set(true)
				if on {
					load()
				} else {
					loading.Set(false)
				}
			})
		}()
	}
	startPrefsLoad(p.Client)

	removeDomain := func(domain string) {
		out := make([]*pb.Recommendation, 0, len(recs.Get()))
		at := -1
		for i, r := range recs.Get() {
			if r.GetDomain() != domain {
				out = append(out, r)
			} else {
				at = i
			}
		}
		// Focus was on the card's own button, and the card is about to leave: it
		// would fall to <body> and a keyboard reader would start from the top of
		// the page (G14). Hand it to the card that takes this one's place — or,
		// with none left, to Refresh. Only when focus IS lost: a reader who has
		// already moved on keeps where they went.
		lost := !platform.FocusedIn("#app") || platform.FocusedIn(".discover-card[data-leaving]")
		recs.Set(out)
		if lost && at >= 0 {
			role := roleDiscoverRefresh
			if len(out) > 0 {
				if at >= len(out) {
					at = len(out) - 1
				}
				role = roleDiscoverFollow + out[at].GetDomain()
			}
			platform.FocusField(role)
		}
	}
	// nameOf is how a sentence refers to a recommendation: its title, or its
	// domain when the site has none.
	nameOf := func(domain string) string {
		for _, r := range recs.Get() {
			if r.GetDomain() == domain {
				if t := r.GetTitle(); t != "" {
					return t
				}
			}
		}
		return domain
	}

	// sendHeld tells the server about a dismissal that was held back. Off the
	// render loop; onFail, when given, comes back through PostAsync.
	sendHeld := func(h *heldReject, onFail func()) {
		c := clientRef.Get()
		if c == nil {
			return
		}
		go func() {
			if err := c.RejectRecommendation(context.Background(), h.domain); err != nil && onFail != nil {
				ui.PostAsync(onFail)
			}
		}()
	}
	// restoreCard puts a held card back where it was, unless it never left (an
	// Undo pressed inside the exit animation).
	restoreCard := func(h *heldReject) {
		cur := recs.Get()
		for _, r := range cur {
			if r.GetDomain() == h.domain {
				return
			}
		}
		at := min(max(h.at, 0), len(cur))
		next := make([]*pb.Recommendation, 0, len(cur)+1)
		next = append(next, cur[:at]...)
		next = append(next, h.rec)
		next = append(next, cur[at:]...)
		recs.Set(next)
	}
	// flushHeld makes the held dismissal final without touching any state: it is
	// what leaving the tab and pagehide run, where a render is the last thing to
	// wait for. Reads refs only, so the copy the unmount cleanup holds from the
	// first render is as good as the latest.
	flushHeld := func() {
		h := held.Get()
		if h == nil {
			return
		}
		held.Set(nil)
		h.timer.Stop()
		sendHeld(h, nil)
	}
	// commitHeld is the same for a render-loop caller: the window ran out, or
	// something (another dismissal, Refresh, switching review off) means the
	// reader has moved on. keepOffer is the second dismissal, which is about to
	// replace the banner and must not hand focus away on the way.
	commitHeld := func(keepOffer bool) {
		h := held.Get()
		if h == nil {
			return
		}
		held.Set(nil)
		h.timer.Stop()
		if !keepOffer {
			// The Undo the reader may be sitting on is about to disappear: focus
			// would fall to <body> and a keyboard reader would start again from the
			// top of the page.
			if platform.FocusedIn(".discover-page .banner") {
				platform.FocusField(roleDiscoverRefresh)
			}
			undoOffer.Set("")
		}
		sendHeld(h, func() {
			// The server refused. The card is the truth again; say so, because the
			// banner that offered the Undo is long gone.
			if alive.Get() {
				restoreCard(h)
				said.Set(tr.T("discover", "dismissFailed", i18n.Args{"name": h.name}))
			}
		})
	}

	// Leaving the tab unmounts this page, and a backgrounded or closing tab may
	// never come back: either way the dismissal is sent now rather than lost with
	// the timer. A request started from pagehide is not guaranteed to leave; if
	// it does not, the failure is the benign one — the card is suggested again
	// next visit, never a dismissal the reader did not make.
	ui.UseEffect(func() func() {
		alive.Set(true)
		l := platform.OnPageHide(func() {
			if held.Get() == nil {
				return
			}
			flushHeld()
			ui.PostAsync(func() { undoOffer.Set("") })
		})
		return func() {
			alive.Set(false)
			l.Release()
			flushHeld()
		}
	}, []any{})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#discover-page", attrDiscoverUndo, func(string) {
			ui.PostAsync(func() {
				h := held.Get()
				if h == nil {
					return
				}
				held.Set(nil)
				h.timer.Stop()
				if leaving.Get() == h.domain {
					leaving.Set("")
					leavingKind.Set("")
				}
				restoreCard(h)
				undoOffer.Set("")
				said.Set(tr.T("discover", "restored", i18n.Args{"name": h.name}))
				// The Undo that was pressed has left the page; the card it brought
				// back is where the reader was.
				platform.FocusField(roleDiscoverFollow + h.domain)
			})
		})
		return l.Release
	}, []any{p.Client})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#discover-page", attrDiscoverSmartPlus, func(string) {
			ui.PostAsync(func() {
				c := p.Client
				if c == nil || smartPlusBusy.Get() {
					return
				}
				commitHeld(false)
				next := !smartPlus.Get()
				smartPlus.Set(next) // optimistic — the toggleFeedFilter pattern
				if next {
					// Turning it on for the first time this mount: nothing was
					// ever loaded (the gate held from the start), so this is
					// the first real ListRecommendations call, not a refresh.
					load()
				} else {
					// Turning it off: the list is no longer this reader's to
					// see — clear it rather than leaving stale cards behind a
					// gate that's back up.
					recs.Set(nil)
					loading.Set(false)
					loadErr.Set(false)
				}
				smartPlusBusy.Set(true)
				go func() {
					err := c.SetPrefs(context.Background(),
						map[string]string{discoverSmartPlusPrefKey: strconv.FormatBool(next)})
					ui.PostAsync(func() {
						smartPlusBusy.Set(false)
						if err != nil {
							// The write did not land — undo the optimism, both
							// the flag and whatever it triggered.
							reverted := !next
							smartPlus.Set(reverted)
							if reverted {
								load()
							} else {
								recs.Set(nil)
							}
						}
					})
				}()
			})
		})
		return l.Release
	}, []any{p.Client})

	// Three delegated listeners, registered once regardless of how many cards
	// exist — the pattern reader_clicks.go uses for item rows. Also, not just
	// per-row: a plain ui.UseEvent on the Refresh button panics under SSR
	// (GoUseFunc needs a live DOM adapter, which the settings-tab render test
	// — renderView / RenderToString — does not have, and every sibling
	// settings tab is SSR-safe because Reader builds its handlers at the top
	// and threads them down as plain props). Delegated clicks are effects,
	// which register post-mount and no-op harmlessly during SSR, so all three
	// buttons use the same mechanism rather than one being the odd one out.
	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#discover-page", attrDiscoverRefresh, func(string) {
			ui.PostAsync(func() {
				c := p.Client
				if c == nil || refreshing.Get() {
					return
				}
				// A refresh re-lists from the server, which would bring back a
				// card whose dismissal it has not been told about.
				commitHeld(false)
				refreshing.Set(true)
				// The stale list sitting there through a refresh read as "nothing
				// happened" (Cam, 2026-08-01) — loading.Set(true) here reuses
				// discoverBody's existing spinner branch (the same one the first
				// mount uses) rather than inventing a second "refreshing" visual,
				// so a refresh and a first load look like the same kind of wait.
				loading.Set(true)
				g := gen.Get() + 1
				gen.Set(g)
				// Refresh's own snapshot, not recs.Get() re-read after the fact —
				// a card the reader accepted/rejected mid-refresh must not count
				// as "the list changed" and end the poll early on the wrong signal.
				before := domainSet(recs.Get())
				go func() {
					if err := c.RefreshRecommendations(context.Background()); err != nil {
						ui.PostAsync(func() {
							refreshing.Set(false)
							loading.Set(false)
							loadErr.Set(true)
						})
						return
					}
					// RefreshRecommendations only ENQUEUES — recommendjob.Service
					// runs on the job pool's own 2s poll (internal/jobs.Pool's
					// Idle default), so a single immediate re-list races the job
					// and shows the exact same stale rows back, which is what Cam
					// was seeing (2026-08-01): the panel "just sits there".
					// Poll instead, until the open set actually changes or a
					// generous ceiling is hit — 2s * 10 covers a slow validating
					// fetch of someone else's server without hanging forever.
					pollDiscoverUntilChanged(c, g, before, gen, recs, refreshing, loading, reviewDown)
				}()
			})
		})
		return l.Release
	}, []any{p.Client})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#discover-page", attrDiscoverAccept, func(domain string) {
			ui.PostAsync(func() {
				c := p.Client
				if c == nil || busy.Get() != "" || domain == "" {
					return
				}
				busy.Set(domain)
				said.Set("")
				name := nameOf(domain)
				go func() {
					_, err := c.AcceptRecommendation(context.Background(), domain)
					ui.PostAsync(func() {
						busy.Set("")
						if err == nil {
							said.Set(tr.T("discover", "followed", i18n.Args{"name": name}))
							playDiscoverLeave(domain, "accept", leaving, leavingKind, removeDomain)
						}
					})
				}()
			})
		})
		return l.Release
	}, []any{p.Client})

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#discover-page", attrDiscoverReject, func(domain string) {
			ui.PostAsync(func() {
				c := p.Client
				if c == nil || busy.Get() != "" || domain == "" {
					return
				}
				var rec *pb.Recommendation
				at := -1
				for i, r := range recs.Get() {
					if r.GetDomain() == domain {
						rec, at = r, i
					}
				}
				if rec == nil {
					return
				}
				// Not for me is permanent and the contract has no way back, so it
				// is held here for the length of the Undo window and only then
				// sent (G26). Only the latest can be taken back: dismissing
				// another card makes the one before it final.
				commitHeld(true)
				name := nameOf(domain)
				h := &heldReject{domain: domain, name: name, rec: rec, at: at}
				h.timer = time.AfterFunc(discoverUndoWindow, func() {
					ui.PostAsync(func() {
						if held.Get() == h {
							commitHeld(false)
						}
					})
				})
				held.Set(h)
				said.Set("")
				undoOffer.Set(tr.T("discover", "dismissed", i18n.Args{"name": name}))
				playDiscoverLeave(domain, "reject", leaving, leavingKind, removeDomain)
			})
		})
		return l.Release
	}, []any{p.Client})

	return html.Div(html.Props{ID: "discover-page", Class: "discover-page"},
		html.Div(html.Props{Class: "discover-head"},
			// The same group heading every other Settings tab opens with, not a
			// display-size title of its own: Discover was the one tab that looked
			// like a different screen.
			kitHeading(glyphDiscover, tr.T("discover", "title"), tr.T("discover", "hint")),
			ui.If(!p.NoKey, func() ui.Node {
				return html.Div(html.Props{Class: "discover-head-actions"},
					discoverSmartPlusToggle(tr, smartPlus.Get(),
						smartPlusBusy.Get() || !prefsLoaded.Get()),
					html.Button(html.Props{
						Type: "button", Class: "discover-refresh",
						// Refreshing a list that isn't loaded is a no-op with a
						// spinner — disabled until the gate is open, not just until
						// prefs answer, so it can't be pressed mid-"we don't know
						// yet" either.
						Disabled: refreshing.Get() || !smartPlus.Get(),
						Data:     map[string]string{"role": roleDiscoverRefresh},
						Raw:      map[string]any{attrDiscoverRefresh: "1"},
					}, html.Text(refreshLabel(tr, refreshing.Get()))),
				)
			}),
		),
		ui.If(p.NoKey, func() ui.Node {
			// Nothing to switch on: this replaces the opt-in gate, whose copy points
			// at a switch that is not there.
			return kitEmpty(kitEmptyProps{Class: "discover-empty discover-gate", Title: tr.T("discover", "noKeyTitle"),
				TitleTag: "p", Lines: []string{tr.T("discover", "noKeyHint")}, LineClass: "discover-hint"})
		}),
		// Always present, so a screen reader hears the change rather than a region
		// that appeared with its text already in it.
		kitLive(kitLiveProps{Class: "discover-said", Role: kitRoleStatus}, html.Text(said.Get())),
		// The way back from Not for me (G26): the banner that says it was done
		// carries the Undo, as Mark all read's does in the list.
		ui.If(undoOffer.Get() != "", func() ui.Node {
			return discoverUndoBanner(tr, undoOffer.Get())
		}),
		// Said above the list it is about, and only while the list is shown: with
		// review off there is no list, and mid-load there is nothing to qualify.
		ui.If(!p.NoKey && smartPlus.Get() && reviewDown.Get() && !loading.Get() && !loadErr.Get(), func() ui.Node {
			return discoverReviewDown(tr)
		}),
		ui.If(!p.NoKey, func() ui.Node {
			return discoverGatedBody(tr, prefsLoaded.Get(), smartPlus.Get(),
				loading.Get(), loadErr.Get(), recs.Get(), busy.Get(), leaving.Get(), leavingKind.Get())
		}),
	)
}

// discoverUndoBanner is the list banner's shape (kitBanner, and the same
// "Undo" chip the list's Mark all read receipt carries) for a dismissal that can
// still be taken back. The chip names its own attribute rather than a reader
// action: it belongs to this page's state, which Reader does not hold.
func discoverUndoBanner(tr i18n.Runtime, text string) ui.Node {
	return kitBanner(kitBannerProps{Text: text},
		kitAction(kitChipProps{TypeButton: true, Mini: true, Extra: "banner-undo",
			Data:  []html.DataAttribute{{Name: strings.TrimPrefix(attrDiscoverUndo, "data-"), Value: "1"}},
			Label: tr.T("list", "undo")}))
}

// discoverReviewDown is the line above the list when Smart+'s review could not
// run on some of what it scored (G9): the suggestions below are real, they are
// scored, and they have not been reviewed — which is not what the toggle being
// on would lead a reader to assume.
func discoverReviewDown(tr i18n.Runtime) ui.Node {
	return kitLive(kitLiveProps{Class: "set-note discover-review-down", Role: kitRoleStatus},
		html.Text(tr.T("discover", "reviewDown")))
}

// discoverGatedBody decides between "we don't know yet", the opt-in gate, and
// the ordinary list — the toggle now gates the WHOLE page (Cam, 2026-08-01),
// not just the LLM calls inside it.
func discoverGatedBody(
	tr i18n.Runtime, prefsLoaded, smartPlus bool,
	loading, loadErr bool, recs []*pb.Recommendation, busy, leaving, leavingKind string,
) ui.Node {
	switch {
	case !prefsLoaded:
		// role=status on the waits and the failure, so the page says out loud
		// what it is doing: Refresh replaces the whole list with this, and a
		// reader who cannot see the spinner otherwise pressed a button and got
		// silence.
		return kitLive(kitLiveProps{Class: "discover-status", Role: kitRoleStatus},
			html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
			html.Span(html.Props{Class: "spin-label"}, html.Text(tr.T("discover", "loading"))),
		)
	case !smartPlus:
		// The toggle already lives in the header (one control, not two) —
		// this box explains what it's for and points at it rather than
		// duplicating it.
		return kitEmpty(kitEmptyProps{Class: "discover-empty discover-gate", Title: tr.T("discover", "gateTitle"),
			TitleTag: "p", Lines: []string{tr.T("discover", "gateHint")}, LineClass: "discover-hint"})
	default:
		return discoverBody(tr, loading, loadErr, recs, busy, leaving, leavingKind)
	}
}

// discoverSmartPlusToggle is the "2 posts reviewed" gate's on/off switch
// (Cam, 2026-08-01), living on the page it gates rather than the general
// Smart+ settings tab. .chip's shape, aria-pressed for the coloured state —
// the same convention verdicts.go's like/dislike chips already establish, so
// a reader who recognises that pairing does not have to learn a new control.
//
// busy covers "a write is in flight" AND "we have not read the stored value
// yet". The second one is the bug this control shipped with: the chip rendered
// aria-pressed="false" while GetPrefs was still in the air, because the state
// it reads starts false and only the BODY was gated on prefsLoaded. A reader
// whose opt-in was already ON saw a switch that said OFF, pressed it to turn it
// on, and got a click computed against that false — so the write said "true" to
// something already true (no change), and the prefs answer then landed and
// re-rendered the switch to what it always was. From the outside: a button that
// cannot be toggled on.
//
// A switch whose position is unknown must not be operable. Disabled is the
// honest render for that beat, and it is a short one.
func discoverSmartPlusToggle(tr i18n.Runtime, on, busy bool) ui.Node {
	return kitToggle(kitChipProps{TypeButton: true, Extra: "discover-smartplus", Disabled: busy,
		Data:      []html.DataAttribute{{Name: strings.TrimPrefix(attrDiscoverSmartPlus, "data-"), Value: "1"}},
		AriaLabel: tr.T("discover", "smartPlusToggleLabel"),
		Label:     tr.T("discover", "smartPlusToggle")}, on)
}

func refreshLabel(tr i18n.Runtime, refreshing bool) string {
	if refreshing {
		return tr.T("discover", "refreshing")
	}
	return tr.T("discover", "refresh")
}

func discoverBody(
	tr i18n.Runtime, loading, loadErr bool, recs []*pb.Recommendation,
	busy, leaving, leavingKind string,
) ui.Node {
	switch {
	case loading:
		// The stale list must not still be on screen through a refresh (Cam,
		// 2026-08-01) — the caller flips loading true for both the first mount
		// AND every Refresh, so this branch fully replaces whatever was there,
		// same .spin-ring panes.go's live-preview loading state already uses.
		return kitLive(kitLiveProps{Class: "discover-status", Role: kitRoleStatus},
			html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
			html.Span(html.Props{Class: "spin-label"}, html.Text(tr.T("discover", "loading"))),
		)
	case loadErr:
		return kitLive(kitLiveProps{Class: "discover-status discover-status-error", Role: kitRoleStatus},
			html.Text(tr.T("discover", "loadFailed")))
	case len(recs) == 0:
		return kitEmpty(kitEmptyProps{Class: "discover-empty", Title: tr.T("discover", "empty"), TitleTag: "p",
			Lines: []string{tr.T("discover", "emptyHint")}, LineClass: "discover-hint"})
	}

	cards := make([]ui.Node, 0, len(recs))
	for _, r := range recs {
		kind := ""
		if leaving != "" && r.GetDomain() == leaving {
			kind = leavingKind
		}
		cards = append(cards, discoverCard(tr, r, busy == r.GetDomain(), kind))
	}
	// A list of cards, each a listitem, so "8 items" is said before the first
	// card is read — the same thing the rail's sections say about themselves.
	return kitList(kitListProps{Class: "discover-list", Unnamed: true}, cards...)
}

func discoverCard(tr i18n.Runtime, r *pb.Recommendation, busy bool, leavingKind string) ui.Node {
	title := r.GetTitle()
	if title == "" {
		title = r.GetDomain()
	}
	item := kitItemProps{Class: "discover-card", Key: "rec-" + r.GetDomain()}
	// The title is the card's heading, and the description of both its
	// buttons. Every card has a Follow and a Not for me, so out of context — a
	// screen reader's list of buttons, or a Tab landing mid-page — they were
	// a column of identical pairs; described by the title, each says whose it is.
	titleID := "rec-title-" + r.GetDomain()
	if leavingKind != "" {
		// data-leaving drives the CSS keyframe animation (design/sheet.go) —
		// a positive (pos-tinted, "kept") exit for Follow, a negative
		// (neg-tinted) one for Not for me (Cam, 2026-08-01: "remove it from
		// the list with a positive animation that shows retention"). The Key
		// above is what makes this the SAME DOM node across the two renders
		// (attribute added, then the card removed from recs a beat later)
		// rather than a fresh element the animation could never play on.
		item.Data = html.DataAttribute{Name: "leaving", Value: leavingKind}
	}
	return kitItem(item,
		html.Div(html.Props{Class: "discover-card-head"},
			html.H2(html.Props{Class: "discover-card-title", ID: titleID}, html.Text(title)),
			// A live link, not just the domain as text — §18.7's whole model is
			// "the system's say-so", and the reader should be able to check that
			// say-so against the actual site before deciding, the same way
			// feedsettings.go's site-URL row lets a reader open a subscription's
			// own site (fs-link's target=_blank + external glyph convention).
			html.A(html.Props{
				Class: "discover-card-domain fs-link", Href: "https://" + r.GetDomain(),
				Target: "_blank", Rel: "noopener noreferrer",
			},
				html.Text(r.GetDomain()),
				html.Span(html.Props{Class: "gl-trail", Aria: map[string]string{"hidden": "true"}},
					html.Text(glyphExternal)),
			),
		),
		// Evidence is shown verbatim — see the package doc above.
		html.P(html.Props{Class: "discover-card-evidence"}, html.Text(r.GetEvidence())),
		html.Div(html.Props{Class: "discover-card-actions"},
			html.Button(html.Props{
				Type: "button", Class: "discover-accept",
				Disabled: busy,
				Data:     map[string]string{"role": roleDiscoverFollow + r.GetDomain()},
				Raw:      map[string]any{attrDiscoverAccept: r.GetDomain()},
				Aria:     map[string]string{"describedby": titleID},
			}, html.Text(acceptLabel(tr, busy))),
			html.Button(html.Props{
				Type: "button", Class: "discover-reject",
				Disabled: busy,
				Raw:      map[string]any{attrDiscoverReject: r.GetDomain()},
				Aria:     map[string]string{"describedby": titleID},
			}, html.Text(rejectLabel(tr, busy))),
		),
	)
}

func acceptLabel(tr i18n.Runtime, busy bool) string {
	if busy {
		return tr.T("discover", "accepting")
	}
	return tr.T("discover", "accept")
}

func rejectLabel(tr i18n.Runtime, busy bool) string {
	if busy {
		return tr.T("discover", "rejecting")
	}
	return tr.T("discover", "reject")
}
