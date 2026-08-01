//go:build js && wasm

package view

import (
	"context"
	"strconv"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

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
}

// Delegated-click attributes (§ "GWC delegated click payload" — a per-row
// ui.UseEvent cannot exist here: hooks are positional and this list's length
// varies, so the cards below carry the domain as a data attribute and TWO
// listeners, registered once, read it — the same pattern reader_clicks.go
// uses for item rows, at client/platform.OnDelegatedClick.
const (
	attrDiscoverAccept    = "data-discover-accept"
	attrDiscoverReject    = "data-discover-reject"
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
	refreshing, loading ui.State[bool],
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

// Discover fetches and renders the open recommendation list.
func Discover(p DiscoverProps) ui.Node {
	tr := i18n.UseI18n()

	loading := ui.UseState(true)
	refreshing := ui.UseState(false)
	loadErr := ui.UseState(false)
	recs := ui.UseState([]*pb.Recommendation(nil))
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
		for _, r := range recs.Get() {
			if r.GetDomain() != domain {
				out = append(out, r)
			}
		}
		recs.Set(out)
	}

	ui.UseEffect(func() func() {
		l := platform.OnDelegatedClick("#discover-page", attrDiscoverSmartPlus, func(string) {
			ui.PostAsync(func() {
				c := p.Client
				if c == nil || smartPlusBusy.Get() {
					return
				}
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
					pollDiscoverUntilChanged(c, g, before, gen, recs, refreshing, loading)
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
				go func() {
					_, err := c.AcceptRecommendation(context.Background(), domain)
					ui.PostAsync(func() {
						busy.Set("")
						if err == nil {
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
				busy.Set(domain)
				go func() {
					err := c.RejectRecommendation(context.Background(), domain)
					ui.PostAsync(func() {
						busy.Set("")
						if err == nil {
							playDiscoverLeave(domain, "reject", leaving, leavingKind, removeDomain)
						}
					})
				}()
			})
		})
		return l.Release
	}, []any{p.Client})

	return html.Div(html.Props{ID: "discover-page", Class: "discover-page"},
		html.Div(html.Props{Class: "discover-head"},
			html.Div(html.Props{},
				html.H1(html.Props{Class: "discover-title"}, html.Text(tr.T("discover", "title"))),
				html.P(html.Props{Class: "discover-hint"}, html.Text(tr.T("discover", "hint"))),
			),
			html.Div(html.Props{Class: "discover-head-actions"},
				discoverSmartPlusToggle(tr, smartPlus.Get(),
					smartPlusBusy.Get() || !prefsLoaded.Get()),
				html.Button(html.Props{
					Type: "button", Class: "discover-refresh",
					// Refreshing a list that isn't loaded is a no-op with a
					// spinner — disabled until the gate is open, not just until
					// prefs answer, so it can't be pressed mid-"we don't know
					// yet" either.
					Disabled: refreshing.Get() || !smartPlus.Get(),
					Raw:      map[string]any{attrDiscoverRefresh: "1"},
				}, html.Text(refreshLabel(tr, refreshing.Get()))),
			),
		),
		discoverGatedBody(tr, prefsLoaded.Get(), smartPlus.Get(),
			loading.Get(), loadErr.Get(), recs.Get(), busy.Get(), leaving.Get(), leavingKind.Get()),
	)
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
		return html.Div(html.Props{Class: "discover-status"},
			html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
			html.Span(html.Props{Class: "spin-label"}, html.Text(tr.T("discover", "loading"))),
		)
	case !smartPlus:
		// The toggle already lives in the header (one control, not two) —
		// this box explains what it's for and points at it rather than
		// duplicating it.
		return html.Div(html.Props{Class: "discover-empty discover-gate"},
			html.P(html.Props{}, html.Text(tr.T("discover", "gateTitle"))),
			html.P(html.Props{Class: "discover-hint"}, html.Text(tr.T("discover", "gateHint"))),
		)
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
	return html.Button(html.Props{
		Type: "button", Class: "chip discover-smartplus",
		Disabled: busy,
		Raw:      map[string]any{attrDiscoverSmartPlus: "1"},
		Aria: map[string]string{
			"pressed": strconv.FormatBool(on),
			"label":   tr.T("discover", "smartPlusToggleLabel"),
		},
	}, html.Text(tr.T("discover", "smartPlusToggle")))
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
		return html.Div(html.Props{Class: "discover-status"},
			html.Div(html.Props{Class: "spin-ring", Aria: map[string]string{"hidden": "true"}}),
			html.Span(html.Props{Class: "spin-label"}, html.Text(tr.T("discover", "loading"))),
		)
	case loadErr:
		return html.Div(html.Props{Class: "discover-status discover-status-error"},
			html.Text(tr.T("discover", "loadFailed")))
	case len(recs) == 0:
		return html.Div(html.Props{Class: "discover-empty"},
			html.P(html.Props{}, html.Text(tr.T("discover", "empty"))),
			html.P(html.Props{Class: "discover-hint"}, html.Text(tr.T("discover", "emptyHint"))),
		)
	}

	cards := make([]ui.Node, 0, len(recs))
	for _, r := range recs {
		kind := ""
		if leaving != "" && r.GetDomain() == leaving {
			kind = leavingKind
		}
		cards = append(cards, discoverCard(tr, r, busy == r.GetDomain(), kind))
	}
	return html.Div(html.Props{Class: "discover-list"}, cards...)
}

func discoverCard(tr i18n.Runtime, r *pb.Recommendation, busy bool, leavingKind string) ui.Node {
	title := r.GetTitle()
	if title == "" {
		title = r.GetDomain()
	}
	cardProps := html.Props{Class: "discover-card", Key: "rec-" + r.GetDomain()}
	if leavingKind != "" {
		// data-leaving drives the CSS keyframe animation (design/sheet.go) —
		// a positive (pos-tinted, "kept") exit for Follow, a negative
		// (neg-tinted) one for Not for me (Cam, 2026-08-01: "remove it from
		// the list with a positive animation that shows retention"). The Key
		// above is what makes this the SAME DOM node across the two renders
		// (attribute added, then the card removed from recs a beat later)
		// rather than a fresh element the animation could never play on.
		cardProps.Raw = map[string]any{"data-leaving": leavingKind}
	}
	return html.Div(cardProps,
		html.Div(html.Props{Class: "discover-card-head"},
			html.Span(html.Props{Class: "discover-card-title"}, html.Text(title)),
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
				Raw:      map[string]any{attrDiscoverAccept: r.GetDomain()},
			}, html.Text(acceptLabel(tr, busy))),
			html.Button(html.Props{
				Type: "button", Class: "discover-reject",
				Disabled: busy,
				Raw:      map[string]any{attrDiscoverReject: r.GetDomain()},
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
