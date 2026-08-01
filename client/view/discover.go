//go:build js && wasm

package view

import (
	"context"

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
	attrDiscoverAccept  = "data-discover-accept"
	attrDiscoverReject  = "data-discover-reject"
	attrDiscoverRefresh = "data-discover-refresh"
)

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
	// gen guards against a stale response landing after a newer request —
	// the same discipline loadFolders uses in reader.go, needed here because
	// Refresh can be clicked again before the first load returns.
	gen := ui.UseRef(0)

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

	ui.UseEffect(func() func() {
		load()
		return nil
	}, []any{p.Client})

	removeDomain := func(domain string) {
		out := make([]*pb.Recommendation, 0, len(recs.Get()))
		for _, r := range recs.Get() {
			if r.GetDomain() != domain {
				out = append(out, r)
			}
		}
		recs.Set(out)
	}

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
				go func() {
					err := c.RefreshRecommendations(context.Background())
					ui.PostAsync(func() {
						refreshing.Set(false)
						if err == nil {
							load()
						}
					})
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
							removeDomain(domain)
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
							removeDomain(domain)
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
			html.Button(html.Props{
				Type: "button", Class: "discover-refresh",
				Disabled: refreshing.Get(),
				Raw:      map[string]any{attrDiscoverRefresh: "1"},
			}, html.Text(refreshLabel(tr, refreshing.Get()))),
		),
		discoverBody(tr, loading.Get(), loadErr.Get(), recs.Get(), busy.Get()),
	)
}

func refreshLabel(tr i18n.Runtime, refreshing bool) string {
	if refreshing {
		return tr.T("discover", "refreshing")
	}
	return tr.T("discover", "refresh")
}

func discoverBody(tr i18n.Runtime, loading, loadErr bool, recs []*pb.Recommendation, busy string) ui.Node {
	switch {
	case loading:
		return html.Div(html.Props{Class: "discover-status"}, html.Text(tr.T("discover", "loading")))
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
		cards = append(cards, discoverCard(tr, r, busy == r.GetDomain()))
	}
	return html.Div(html.Props{Class: "discover-list"}, cards...)
}

func discoverCard(tr i18n.Runtime, r *pb.Recommendation, busy bool) ui.Node {
	title := r.GetTitle()
	if title == "" {
		title = r.GetDomain()
	}
	return html.Div(html.Props{Class: "discover-card", Key: "rec-" + r.GetDomain()},
		html.Div(html.Props{Class: "discover-card-head"},
			html.Span(html.Props{Class: "discover-card-title"}, html.Text(title)),
			html.Span(html.Props{Class: "discover-card-domain"}, html.Text(r.GetDomain())),
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
