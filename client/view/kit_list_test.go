//go:build js && wasm

package view

// The collection kit's proof: every shape kit_list.go claims to replace,
// rendered the old way and the kit way, and required to come out the same.
//
// "The same" is two checks, because the SSR string the goldens compare does
// not carry everything the DOM depends on:
//
//   - the rendered bytes, through renderView — classes, data-* hooks, roles,
//     aria values, element types, text, child order;
//   - the KEY tree (kitShape), because reconciliation keys are not attributes
//     and SSR never prints them, yet a row keyed differently is a row the
//     reconciler rebuilds instead of patching.
//
// The "after" builders below (kitAfter*) are also the adoption templates: each
// is what the existing helper becomes when it is rewritten on the kit, so the
// agent doing the adoption copies a function whose equivalence is already
// proven rather than deriving one.

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// --- the comparison ------------------------------------------------------------

// kitShape is a node's element tree as tag and key alone. Components print as
// "component": what they render is the SSR comparison's business, and which
// function they are is exactly what an adoption is allowed to change. Nil
// children are skipped — a held nil slot renders no DOM either way.
func kitShape(n ui.Node) string {
	var b strings.Builder
	var walk func(n ui.Node)
	walk = func(n ui.Node) {
		if n == nil {
			return
		}
		key := n.Key
		if key == "" {
			if k, ok := n.Props["key"]; ok && k != nil {
				key = toKeyString(k)
			}
		}
		if tag, ok := n.Type.(string); ok {
			b.WriteString("<")
			b.WriteString(tag)
		} else {
			b.WriteString("<component")
		}
		if key != "" {
			b.WriteString(" key=")
			b.WriteString(key)
		}
		b.WriteString(">")
		for _, c := range n.Children {
			if cn, ok := c.(ui.Node); ok {
				walk(cn)
			}
		}
		b.WriteString("</>")
	}
	walk(n)
	return b.String()
}

func toKeyString(v any) string {
	switch k := v.(type) {
	case string:
		return k
	case int:
		return strconv.Itoa(k)
	default:
		return reflect.ValueOf(v).String()
	}
}

// kitSame renders before and after and fails on the first difference in
// either the bytes or the key tree. The shape is taken as the nodes are built,
// before the renderer has had them.
func kitSame(t *testing.T, name string, before, after func(tr i18n.Runtime) ui.Node) {
	t.Helper()
	var bs, as string
	b := renderView(t, func(tr i18n.Runtime) ui.Node { n := before(tr); bs = kitShape(n); return n })
	a := renderView(t, func(tr i18n.Runtime) ui.Node { n := after(tr); as = kitShape(n); return n })
	if a != b {
		t.Errorf("%s: kit markup differs from today's\n%s", name, kitFirstDiff(b, a))
	}
	if as != bs {
		t.Errorf("%s: kit key tree differs from today's\n%s", name, kitFirstDiff(bs, as))
	}
}

// kitSameNodes is kitSame for helpers that return a slice of siblings.
func kitSameNodes(t *testing.T, name string, before, after func(tr i18n.Runtime) []ui.Node) {
	t.Helper()
	wrap := func(f func(tr i18n.Runtime) []ui.Node) func(tr i18n.Runtime) ui.Node {
		return func(tr i18n.Runtime) ui.Node { return html.Div(html.Props{Class: "kit-probe"}, f(tr)...) }
	}
	kitSame(t, name, wrap(before), wrap(after))
}

func kitFirstDiff(want, got string) string {
	i := 0
	for i < len(want) && i < len(got) && want[i] == got[i] {
		i++
	}
	lo := i - 120
	if lo < 0 {
		lo = 0
	}
	clip := func(s string) string {
		hi := i + 120
		if hi > len(s) {
			hi = len(s)
		}
		if lo > len(s) {
			return ""
		}
		return s[lo:hi]
	}
	return "at byte " + strconv.Itoa(i) + "\n before: …" + clip(want) + "…\n  after: …" + clip(got) + "…"
}

// TestKitShapeSeesKeys keeps the key comparison honest: a comparison that
// printed nothing for every node would pass every test in this file.
func TestKitShapeSeesKeys(t *testing.T) {
	a := kitItem(kitItemProps{Class: "rail-cell", Key: "a"}, html.Button(html.Props{}))
	b := kitItem(kitItemProps{Class: "rail-cell", Key: "b"}, html.Button(html.Props{}))
	c := html.WithKey(ui.CreateElement(feedRowView, feedRowProps{}), "c")
	if kitShape(a) == kitShape(b) || !strings.Contains(kitShape(a), "key=a") {
		t.Errorf("kitShape does not distinguish keys: %q vs %q", kitShape(a), kitShape(b))
	}
	if !strings.Contains(kitShape(c), "<component key=c>") {
		t.Errorf("kitShape does not see a component's key: %q", kitShape(c))
	}
}

// --- rail rows, the kit way ------------------------------------------------------

func kitAfterSpecialRow(tr i18n.Runtime, glyph, label, id string, count int, active bool) ui.Node {
	return kitRow(kitRowProps{
		NoTab:   true,
		Class:   "feed-row stream-row",
		Hook:    html.DataAttribute{Name: "source-id", Value: id},
		Current: active,
		Mark:    lead(glyph),
		Name:    label,
		Count:   kitCount(tr, count),
		Slot:    "rail-cell", Item: true, Key: id,
	})
}

func kitAfterFeedRow(tr i18n.Runtime, f *pb.Feed, active, nested bool) ui.Node {
	dormant := f.GetConsecutiveFailures() >= 3
	label := f.GetTitle()
	if dormant {
		label = tr.T("rail", "dormantSuffix", i18n.Args{"title": label})
	}
	n := int(f.GetUnreadCount())
	count := kitCount(tr, n)
	if count != "" {
		label = tr.T("rail", "categoryUnreadAria", i18n.CountWith(n, i18n.Args{"name": label}))
	}
	slot := "feed-slot"
	if nested {
		slot = "feed-slot feed-nested"
	}
	return kitRow(kitRowProps{
		NoTab:   true,
		Class:   "feed-row",
		Hook:    html.DataAttribute{Name: "source-id", Value: f.GetSourceId()},
		Raw:     hueVarFor(f.GetSourceId()),
		Data:    map[string]string{"dormant": strconv.FormatBool(dormant)},
		Current: active, Label: label,
		Mark: kitMark(kitMarkProps{Class: "feed-dot", WrapClass: "feed-icon-wrap", ImgClass: "feed-icon",
			Src: faviconSrc(iconHost(f))}),
		Name: f.GetTitle(), NameTitle: titleFor(tr, f, dormant),
		Count: count, Gap: true,
		Slot: slot, Item: true, Key: feedRowKey(f.GetSourceId(), nested),
		After: html.Button(html.Props{
			Class:     "feed-gear",
			TabIndex:  -1,
			DataAttrs: []html.DataAttribute{{Name: "action", Value: "feed-settings"}, {Name: "for-item", Value: f.GetSourceId()}},
			Title:     tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()}),
			Aria:      map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()})},
		}, html.Text("⚙")),
	})
}

func kitAfterFeedRowView(p feedRowProps) ui.Node {
	return kitAfterFeedRow(i18n.UseI18n(), p.f, p.active, p.nested)
}

func kitAfterRailFeedRow(f *pb.Feed, active, nested bool) ui.Node {
	return html.WithKey(ui.CreateElement(kitAfterFeedRowView, feedRowProps{f: f, active: active, nested: nested}),
		feedRowKey(f.GetSourceId(), nested))
}

func kitAfterTopicRow(tr i18n.Runtime, slug string, active bool, unread int) ui.Node {
	name := categoryName(tr, slug)
	label := name
	if unread > 0 {
		label = tr.T("rail", "categoryUnreadAria", i18n.CountWith(unread, i18n.Args{"name": name}))
	}
	return kitRow(kitRowProps{
		NoTab:   true,
		Class:   "feed-row topic-row",
		Hook:    html.DataAttribute{Name: "category-slug", Value: slug},
		Current: active, Label: label,
		Mark:  html.I(html.Props{Class: "feed-dot topic-dot", Raw: catStyleFor(slug)}),
		Name:  name,
		Count: kitCount(tr, unread),
		Slot:  "feed-slot", Item: true, Key: "topic-" + slug,
	})
}

func kitAfterUncategorisedRow(tr i18n.Runtime, active bool, unread int) ui.Node {
	name := tr.T("rail", "uncategorised")
	label := name
	if unread > 0 {
		label = tr.T("rail", "categoryUnreadAria", i18n.CountWith(unread, i18n.Args{"name": name}))
	}
	return kitRow(kitRowProps{
		NoTab:   true,
		Class:   "feed-row topic-row",
		Hook:    html.DataAttribute{Name: "category-none", Value: "1"},
		Current: active, Label: label,
		Mark:  html.I(html.Props{Class: "feed-dot topic-dot topic-dot-none"}),
		Name:  name,
		Count: kitCount(tr, unread),
		Slot:  "feed-slot", Item: true, Key: "topic-none",
	})
}

func kitAfterTagRow(tr i18n.Runtime, t *pb.Tag, active bool) ui.Node {
	name := tagDisplay(t)
	hint := tr.T("rail", "tagged", i18n.Args{"name": name})
	if l := t.GetLabel(); l != "" && l != t.GetName() {
		hint = tr.T("rail", "taggedRenamed", i18n.Args{"label": name, "name": t.GetName()})
	}
	return kitRow(kitRowProps{
		NoTab:   true,
		Class:   "feed-row",
		Hook:    html.DataAttribute{Name: "tag-id", Value: t.GetId()},
		Title:   hint,
		Current: active, Label: hint,
		Mark: tagMark(t),
		Name: name,
		// The one row that shows its zero; see kitCount.
		Count: thousands(tr, int(t.GetFeedCount())),
		Gap:   true,
		Slot:  "feed-slot", Item: true, Key: "tag-" + t.GetId(),
		After: html.Button(html.Props{
			Class:     "feed-gear",
			TabIndex:  -1,
			Data:      map[string]string{"role": tagGearRole(t.GetId())},
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actTagSettings}, {Name: "for-item", Value: t.GetId()}},
			Title:     tr.T("rail", "settingsFor", i18n.Args{"name": name}),
			Aria:      map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": name})},
		}, html.Text(glyphSettings)),
	})
}

func kitAfterCategoryRows(tr i18n.Runtime, p railProps, id, name string, feeds []*pb.Feed, editable bool) ui.Node {
	open := catOpen(p.openCats, id)
	unread := 0
	for _, f := range feeds {
		unread += int(f.GetUnreadCount())
	}
	key := railCatKey(id)
	var gear ui.Node
	if editable {
		gear = html.Button(html.Props{
			Class:     "feed-gear cat-gear",
			TabIndex:  -1,
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actCatOpen}, {Name: "value", Value: id}},
			Title:     tr.T("rail", "editCategory", i18n.Args{"name": name}),
			Aria:      map[string]string{"label": tr.T("rail", "editCategory", i18n.Args{"name": name})},
		}, html.Text("✎"))
	}
	slot := kitRow(kitRowProps{
		NoTab:     true,
		Class:     "feed-row cat-row",
		Hook:      html.DataAttribute{Name: "folder-id", Value: id},
		Current:   p.sel.FolderID == id,
		Label:     tr.T("rail", "categoryAria", i18n.CountWith(len(feeds), i18n.Args{"name": name})),
		Name:      name,
		NameTitle: name,
		Count:     kitCount(tr, unread),
		Gap:       true,
		Slot:      "feed-slot cat-slot", Key: "cat-" + id,
		SlotData: map[string]string{"open": strconv.FormatBool(open)},
		Before: html.Button(html.Props{
			Class:     "cat-caret",
			TabIndex:  -1,
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actCatToggle}, {Name: "value", Value: id}},
			Aria:      kitDisclosureAria(tr.T("rail", "showCategory", i18n.Args{"name": name}), open, kitFoldID(key)),
		}, html.Text(glyphClosed)),
		After: gear,
	})
	cell := kitItemProps{Class: "rail-cell", Key: "cell-cat-" + id}
	if !open && p.closing != key {
		return kitItem(cell, slot)
	}
	var inside []ui.Node
	if len(feeds) == 0 {
		inside = append(inside, kitEmpty(kitEmptyProps{Class: "rail-section cat-empty", Key: "cat-empty-" + id,
			Text: tr.T("rail", "categoryEmpty")}))
	}
	var rows []ui.Node
	for _, f := range feeds {
		rows = append(rows, kitAfterRailFeedRow(f, p.sel.SourceID == f.GetSourceId(), true))
	}
	inside = append(inside, kitListIn(kitListProps{Class: "rail-list", Label: name, OmitEmpty: true}, rows)...)
	return kitItem(cell, append([]ui.Node{slot}, kitFold(key, open, p.closing, inside...)...)...)
}

// --- the rail, the kit way ---------------------------------------------------------

func kitAfterRailPane(tr i18n.Runtime, p railProps) ui.Node {
	var streams []ui.Node
	if !p.streamsClosed || p.closing == actStreams {
		streams = []ui.Node{
			kitAfterSpecialRow(tr, glyphMyFeed, tr.T("stream", "myFeed"), streamMyFeed, p.ranked, p.sel.MyFeed),
			kitAfterSpecialRow(tr, glyphAll, tr.T("stream", "all"), streamAll, p.total, isAllScope(p.sel)),
			kitAfterSpecialRow(tr, glyphUnread, tr.T("stream", "unread"), streamUnread, p.total, p.sel.Unread),
			kitAfterSpecialRow(tr, glyphLater, tr.T("stream", "later"), streamLater, -1, p.sel.Later),
			kitAfterSpecialRow(tr, glyphLiked, tr.T("stream", "liked"), streamLiked, -1, p.sel.Rating > 0),
			kitAfterSpecialRow(tr, glyphNotes, tr.T("stream", "notes"), streamNotes, -1, p.sel.Notes),
		}
	}
	head := append([]ui.Node{railMasthead(tr, p)}, kitSection(kitSectionProps{
		Band:    kitBandProps{Glyph: glyphAll, Label: tr.T("rail", "bandStreams"), Action: actStreams, Open: !p.streamsClosed},
		Closing: p.closing,
	}, streams...)...)

	feedsBand := kitBandProps{Glyph: glyphFeeds, Label: tr.T("rail", "bandFeeds"), Action: actFeeds,
		Open: !p.feedsClosed, Hidden: len(p.feeds)}
	var bandExtra []ui.Node
	if len(p.feeds) > 0 {
		label := tr.T("rail", "feedFilterAll")
		if p.unreadFeedsOnly {
			label = tr.T("rail", "feedFilterNew")
		}
		bandExtra = append(bandExtra, html.Button(html.Props{
			Class:    "rail-band-act",
			TabIndex: -1,
			DataAttr: html.DataAttribute{Name: "action", Value: "toggle-feed-filter"},
			Aria: map[string]string{
				"pressed": strconv.FormatBool(p.unreadFeedsOnly),
				"label":   tr.T("rail", "showFeedFilter", i18n.Args{"label": label}),
			},
		}, html.Text(strings.ToLower(label))))
	}
	feedsOpen := !p.feedsClosed || p.closing == actFeeds

	if p.loading && len(p.feeds) == 0 {
		var sk []ui.Node
		if feedsOpen {
			for i := 0; i < 8; i++ {
				sk = append(sk, kitSkFeedRow(i))
			}
		}
		body := append([]ui.Node{kitBand(feedsBand, bandExtra...)}, kitFold(actFeeds, !p.feedsClosed, p.closing, sk...)...)
		return kitAfterRailShell(tr, head, body, railFoot(tr, p), true)
	}

	var before, rows, after []ui.Node
	if feedsOpen {
		if len(p.feeds) > 8 {
			before = append(before, html.Div(html.Props{Class: "rail-filter"},
				html.Input(html.Props{
					Class: "field", Type: "search", Placeholder: tr.T("rail", "filterPlaceholder"),
					Value:   p.filterSeed,
					OnInput: p.onFilterInput.Get(),
					Data:    map[string]string{"role": "feed-filter"},
					Aria:    map[string]string{"label": tr.T("rail", "filterAria")},
				})))
		}
		needle := strings.ToLower(strings.TrimSpace(p.filter))
		for _, f := range p.feeds {
			if railFeedShown(p, f, needle) {
				rows = append(rows, kitAfterRailFeedRow(f, p.sel.SourceID == f.GetSourceId(), false))
			}
		}
		note := ""
		switch {
		case len(p.feeds) == 0:
			note = tr.T("rail", "emptyNoFeeds")
		case len(rows) == 0 && needle != "":
			note = tr.T("rail", "emptyNoMatch", i18n.Args{"query": strings.TrimSpace(p.filter)})
		case len(rows) == 0:
			note = tr.T("rail", "emptyNoUnread")
		}
		if note != "" {
			after = append(after, kitEmpty(kitEmptyProps{Class: "rail-section", Text: note}))
		}
	}
	body := kitSection(kitSectionProps{Band: feedsBand, BandExtra: bandExtra, Closing: p.closing,
		Before: before, After: after}, rows...)

	body = append(body, kitAfterCategories(tr, p)...)

	if len(p.tags) > 0 {
		var tags []ui.Node
		if !p.tagsClosed || p.closing == actTags {
			for _, t := range p.tags {
				tags = append(tags, kitAfterTagRow(tr, t, p.sel.TagID == t.GetId()))
			}
		}
		body = append(body, kitSection(kitSectionProps{
			Band: kitBandProps{Glyph: glyphTags, Label: tr.T("rail", "bandTags"), Action: actTags,
				Open: !p.tagsClosed, Hidden: len(p.tags)},
			Closing: p.closing,
		}, tags...)...)
	}
	return kitAfterRailShell(tr, head, body, railFoot(tr, p), false)
}

func kitAfterCategories(tr i18n.Runtime, p railProps) []ui.Node {
	if len(p.feeds) == 0 && len(p.folders) == 0 && p.total == 0 {
		return nil
	}
	newCat := html.Button(html.Props{
		Class:    "rail-band-act",
		TabIndex: -1,
		DataAttr: html.DataAttribute{Name: "action", Value: actCatNew},
		Aria:     map[string]string{"label": tr.T("rail", "addCategoryA")},
	}, html.Text(tr.T("rail", "addCategory")))
	sec := kitSectionProps{
		Band: kitBandProps{Glyph: glyphCats, Label: tr.T("rail", "bandCategories"), Action: actCats,
			Open: !p.catsClosed, Hidden: len(p.folders)},
		BandExtra: []ui.Node{newCat},
		Closing:   p.closing,
	}
	if p.catsClosed && p.closing != actCats {
		return kitSection(sec)
	}
	groups := feedsByFolder(p.feeds)
	var cats []ui.Node
	for _, f := range p.folders {
		cats = append(cats, kitAfterCategoryRows(tr, p, f.GetId(), f.GetName(), groups[f.GetId()], true))
	}
	if rest := groups[unfiledID]; len(rest) > 0 && anyFolderHasFeeds(p.folders, groups) {
		cats = append(cats, kitAfterCategoryRows(tr, p, unfiledID, tr.T("stream", "unfiled"), rest, false))
	}
	for _, c := range design.Categories {
		cats = append(cats, kitAfterTopicRow(tr, c.Slug, p.sel.CategorySlug == c.Slug, int(p.topicUnread[c.Slug])))
	}
	cats = append(cats, kitAfterUncategorisedRow(tr, p.sel.Uncategorised, p.uncatUnread))
	return kitSection(sec, cats...)
}

func kitAfterRailShell(tr i18n.Runtime, head, body, foot []ui.Node, busy bool) ui.Node {
	return kitNav(kitPaneProps{Class: "pane pane-rail", Label: tr.T("rail", "feedsAria"), Busy: busy},
		html.Div(html.Props{Class: "rail-head"}, head...),
		html.Div(html.Props{Class: "rail-scroll"}, body...),
		html.Div(html.Props{Class: "rail-foot"}, foot...),
	)
}

// kitRailCases are the rail's golden states, plus the benchmark's 150-feed
// rail and a dormant, erroring feed, as props rather than closures so each can
// be rendered both ways.
func kitRailCases() map[string]railProps {
	base := func() railProps {
		return railProps{feeds: fixtureFeeds(12), tags: fixtureTags(), folders: fixtureFolders(), total: 36, ranked: 9}
	}
	with := func(edit func(*railProps)) railProps { p := base(); edit(&p); return p }
	slugs := categorySlugs()
	dormant := fixtureFeeds(12)
	dormant[4].ConsecutiveFailures = 5
	dormant[4].LastError = "connection refused"
	return map[string]railProps{
		"rail-all":               base(),
		"rail-feed-current":      with(func(p *railProps) { p.sel = scope{SourceID: "src-2", Title: "Feed number 2"} }),
		"rail-category-open":     with(func(p *railProps) { p.openCats = "cat-1"; p.sel = scope{FolderID: "cat-1", Title: "Tech"} }),
		"rail-tag-current":       with(func(p *railProps) { p.sel = scope{TagID: "t2", Title: "Long reads"} }),
		"rail-myfeed":            with(func(p *railProps) { p.sel = scope{MyFeed: true, Title: "My Feed"} }),
		"rail-filtered":          with(func(p *railProps) { p.filter = "number 1"; p.filterSeed = "number 1" }),
		"rail-filtered-nothing":  with(func(p *railProps) { p.filter = "zzz" }),
		"rail-unread-feeds-only": with(func(p *railProps) { p.unreadFeedsOnly = true }),
		"rail-all-read-only":     with(func(p *railProps) { p.unreadFeedsOnly = true; p.feeds = p.feeds[:1] }),
		"rail-closing":           with(func(p *railProps) { p.closing = railCatKey("cat-0"); p.openCats = "cat-0" }),
		"rail-closing-tags":      with(func(p *railProps) { p.closing = actTags; p.tagsClosed = true }),
		"rail-closing-streams":   with(func(p *railProps) { p.closing = actStreams; p.streamsClosed = true }),
		"rail-closing-cats":      with(func(p *railProps) { p.closing = actCats; p.catsClosed = true }),
		"rail-empty-category":    with(func(p *railProps) { p.openCats = "cat-0,cat-2" }),
		"rail-dormant":           with(func(p *railProps) { p.feeds = dormant }),
		"rail-folded": with(func(p *railProps) {
			p.streamsClosed, p.feedsClosed, p.tagsClosed, p.catsClosed = true, true, true, true
		}),
		"rail-topics": with(func(p *railProps) {
			p.topicUnread = map[string]int32{slugs[0]: 4, slugs[len(slugs)-1]: 1, slugs[1]: 12345}
			p.uncatUnread = 3
			p.sel = scope{CategorySlug: slugs[0], Title: "x"}
		}),
		"rail-loading-empty":  {loading: true},
		"rail-loading-folded": {loading: true, feedsClosed: true},
		"rail-empty":          {},
		"rail-150": {feeds: fixtureFeeds(150), tags: fixtureTags(), folders: fixtureFolders(),
			total: 400, ranked: 30, openCats: "cat-1", sel: scope{SourceID: "src-40", Title: "Feed number 40"}},
	}
}

func TestKitRailMatchesRailPane(t *testing.T) {
	for name, p := range kitRailCases() {
		p := p
		kitSame(t, name,
			func(tr i18n.Runtime) ui.Node { return railPane(p) },
			func(tr i18n.Runtime) ui.Node { return kitAfterRailPane(tr, p) })
	}
}

func TestKitRowMatchesFeedRow(t *testing.T) {
	feeds := fixtureFeeds(12)
	feeds[3].ConsecutiveFailures = 3
	feeds[3].LastError = "timeout"
	feeds[5].ConsecutiveFailures = 4
	feeds[7].UnreadCount = 12345
	feeds = append(feeds, &pb.Feed{Title: "No source id"})
	for _, f := range feeds {
		for _, active := range []bool{false, true} {
			for _, nested := range []bool{false, true} {
				f, active, nested := f, active, nested
				kitSame(t, "feed "+f.GetSourceId(),
					func(tr i18n.Runtime) ui.Node { return legacyFeedRow(tr, f, active, nested) },
					func(tr i18n.Runtime) ui.Node { return kitAfterFeedRow(tr, f, active, nested) })
			}
		}
	}
}

func TestKitRowMatchesTheOtherRailRows(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 1234} {
		for _, active := range []bool{false, true} {
			n, active := n, active
			kitSame(t, "stream",
				func(tr i18n.Runtime) ui.Node { return specialRow(tr, glyphMyFeed, "My Feed", streamMyFeed, n, active) },
				func(tr i18n.Runtime) ui.Node {
					return kitAfterSpecialRow(tr, glyphMyFeed, "My Feed", streamMyFeed, n, active)
				})
			kitSame(t, "uncategorised",
				func(tr i18n.Runtime) ui.Node { return uncategorisedRow(tr, active, n) },
				func(tr i18n.Runtime) ui.Node { return kitAfterUncategorisedRow(tr, active, n) })
			for _, slug := range append(categorySlugs(), "not-a-slug") {
				slug := slug
				kitSame(t, "topic "+slug,
					func(tr i18n.Runtime) ui.Node { return legacyTopicRow(tr, slug, active, n) },
					func(tr i18n.Runtime) ui.Node { return kitAfterTopicRow(tr, slug, active, n) })
			}
		}
	}
	tags := append(fixtureTags(),
		&pb.Tag{Id: "t3", Name: "starred", Glyph: "★", FeedCount: 0},
		&pb.Tag{Id: "t4", Name: "same", Label: "same", FeedCount: 4321})
	for _, tg := range tags {
		for _, active := range []bool{false, true} {
			tg, active := tg, active
			kitSame(t, "tag "+tg.GetId(),
				func(tr i18n.Runtime) ui.Node { return legacyTagRow(tr, tg, active) },
				func(tr i18n.Runtime) ui.Node { return kitAfterTagRow(tr, tg, active) })
		}
	}
}

// --- the item list, the kit way -------------------------------------------------

func kitAfterItemRow(tr i18n.Runtime, it *pb.Item, active bool, hosts map[string]string, spawn int, hiddenCats string) ui.Node {
	age := ageOf(it.GetPublishedAt(), it.GetRead())
	return kitItem(kitItemProps{Class: "item-cell", Key: it.GetId()}, html.Button(html.Props{
		Class: "item-row",
		Raw:   itemRowAttrs(it, spawn),
		Data: map[string]string{
			"read":  strconv.FormatBool(it.GetRead()),
			"age":   age,
			"fresh": strconv.FormatBool(spawn >= 0),
		},
		Aria: map[string]string{"current": strconv.FormatBool(active)},
	},
		html.Span(html.Props{Class: "item-title"}, html.Text(it.GetTitle())),
		kitAfterItemMeta(tr, it, hosts, age, hiddenCats),
		itemGloss(tr, it),
		rankBlurb(tr, it),
	))
}

func kitAfterItemMeta(tr i18n.Runtime, it *pb.Item, hosts map[string]string, age, hiddenCats string) ui.Node {
	long := it.GetWordCount() >= 50
	return kitMeta("item-meta", false,
		kitSourceMark(it.GetSourceId(), hosts, "item-mark"),
		kitSource(it.GetSourceId(), it.GetSourceTitle()),
		kitSep(),
		timeTag(tr, it.GetPublishedAt()),
		ageTag(tr, age),
		categoryChip(tr, it, hiddenCats),
		smartPlusMark(tr, it),
		kitSepIf(long),
		ui.If(long, func() ui.Node {
			return html.Span(html.Props{Class: "item-read"}, html.Text(readingTime(tr, it.GetWordCount())))
		}),
		ui.If(it.GetStarred(), func() ui.Node {
			return html.Span(html.Props{Class: "item-later",
				Aria: map[string]string{"label": tr.T("list", "savedFor")}}, html.Text("⏱"))
		}),
		ui.Match().
			When(it.GetRating() > 0, func() ui.Node {
				return html.Span(html.Props{Class: "item-verdict verdict-up",
					Aria: map[string]string{"label": tr.T("list", "liked")}}, html.Text("▲"))
			}).
			When(it.GetRating() < 0, func() ui.Node {
				return html.Span(html.Props{Class: "item-verdict verdict-down",
					Aria: map[string]string{"label": tr.T("list", "disliked")}}, html.Text("▼"))
			}).
			Default(nil),
	)
}

func kitAfterEmptyState(tr i18n.Runtime, titleKey, hintKey string) ui.Node {
	ns := tr.NS("list")
	return kitEmpty(kitEmptyProps{Title: ns.T(titleKey), Lines: []string{ns.T(hintKey)}})
}

func kitAfterSkeletonList(tr i18n.Runtime) ui.Node {
	rows := make([]ui.Node, 0, 12)
	for i := 0; i < 12; i++ {
		rows = append(rows, kitSkItemRow(i))
	}
	return kitSkRegion("list-scroll", tr.T("list", "loadingArticles"), rows...)
}

// kitAfterListPane is listPane on the kit, with emptyList's switch reduced to
// the one call it makes; only the shapes in this family are rewritten.
func kitAfterListPane(tr i18n.Runtime, p listProps) ui.Node {
	head := listHead(tr, p)
	pane := kitPaneProps{Class: "pane pane-list", Data: paneScope(p.sel), Label: tr.T("list", "paneLabel")}
	if !p.connected {
		return kitRegion(pane, head, kitEmpty(kitEmptyProps{Text: tr.T("list", "connecting")}))
	}
	if p.loading && len(p.items) == 0 {
		return kitRegion(pane, head, kitAfterSkeletonList(tr))
	}
	if len(p.items) == 0 {
		return kitRegion(pane, head, kitAfterEmptyList(tr, p))
	}
	if band := learningBand(tr, p); band != nil {
		head = html.Fragment(head, band)
	}
	loaded := len(p.items)
	rows := loaded
	if p.total > rows {
		rows = p.total
	}
	cursor := -1
	if p.current != nil {
		for i, it := range p.items {
			if it.GetId() == p.current.GetId() {
				cursor = i
				break
			}
		}
	}
	scroller := kitListAttrs(kitListProps{Class: "list-scroll", Label: p.sel.Title})
	scroller.Data = map[string]string{"cursor": strconv.FormatBool(cursor >= 0)}
	scroller.Raw = map[string]any{"style": "--cursor:" + cursorY(cursor, rowHeight(p.sel))}
	return kitRegion(pane, head, html.VirtualList(html.VirtualListProps{
		ItemCount:      rows + 1,
		ItemHeight:     rowHeight(p.sel),
		ViewportHeight: p.viewport,
		ScrollTop:      p.scrollTop,
		Key: func(i int) any {
			switch {
			case i < loaded:
				return p.items[i].GetId()
			case i < rows:
				return "sk-" + strconv.Itoa(i)
			default:
				return "__tail__"
			}
		},
		Render: func(i int) ui.Node {
			switch {
			case i < loaded:
				it := p.items[i]
				step := -1
				if p.fresh[it.GetId()] {
					from := loaded - len(p.fresh)
					if seen := int(p.scrollTop / ItemRowHeight); seen > from {
						from = seen
					}
					step = i - from
					if step < 0 {
						step = 0
					}
					if step > spawnStaggerCap {
						step = spawnStaggerCap
					}
				}
				return kitAfterItemRow(tr, it, p.current != nil && p.current.GetId() == it.GetId(),
					p.iconHosts, step, p.hiddenCats)
			case i < rows:
				return kitSkItemRow(i)
			default:
				return listTail(tr, p)
			}
		},
		Props: scroller,
	}))
}

func kitAfterEmptyList(tr i18n.Runtime, p listProps) ui.Node {
	key := ""
	switch {
	case p.loadFailed:
		key = "emptyFailed"
	case p.sel.Search != "":
		key = "emptySearch"
	case p.sel.MyFeed:
		key = "emptyMyFeed"
	case p.unreadOnly, p.sel.Unread:
		key = "emptyUnread"
	case p.sel.Later:
		key = "emptyLater"
	case p.sel.Rating > 0:
		key = "emptyLiked"
	case p.sel.Rating < 0:
		key = "emptyDisliked"
	case p.sel.Notes:
		key = "emptyNotes"
	case p.sel.TagID != "":
		key = "emptyTag"
	case p.sel.FolderID != "":
		key = "emptyCategory"
	case p.feedsKnown && p.feeds > 0:
		return kitAfterEmptyState(tr, "emptyNoArticles", "emptyNoArticlesWaiting")
	default:
		return kitAfterEmptyFirstRun(tr)
	}
	return kitAfterEmptyState(tr, key, key+"Hint")
}

// kitAfterEmptyFirstRun is emptyFirstRun on the kit: the empty state with its
// one action inside it.
func kitAfterEmptyFirstRun(tr i18n.Runtime) ui.Node {
	ns := tr.NS("list")
	return kitEmpty(kitEmptyProps{Class: "empty empty-first", Data: map[string]string{"role": "first-run"},
		Title: ns.T("emptyNoArticles"), Lines: []string{ns.T("emptyNoArticlesHint")},
		Action: kitAction(kitChipProps{Action: actAddOpen, Look: kitChipBtn, Extra: "empty-add",
			Glyph: glyphAdd, Label: tr.T("rail", "addFeed")})})
}

func TestKitListMatchesListPane(t *testing.T) {
	items := fixtureItems(12)
	base := func() listProps {
		return listProps{items: items, connected: true, total: 40, unread: 120, hasMore: true,
			sel: scope{Title: "All feeds"}, iconHosts: iconHostsOf(fixtureFeeds(12)), conn: data.Live}
	}
	with := func(edit func(*listProps)) listProps { p := base(); edit(&p); return p }
	cases := map[string]listProps{
		"connecting":   {sel: scope{Title: "All feeds"}},
		"loading":      {connected: true, loading: true, sel: scope{Title: "All feeds"}},
		"all":          base(),
		"untitled":     with(func(p *listProps) { p.sel = scope{} }),
		"current":      with(func(p *listProps) { p.current = items[3]; p.fresh = map[string]bool{"i10": true, "i11": true} }),
		"window":       with(func(p *listProps) { p.viewport = 400; p.scrollTop = ItemRowHeight * 3; p.current = items[4] }),
		"myfeed":       with(func(p *listProps) { p.sel = scope{MyFeed: true, Title: "My Feed"}; p.ranked = 9 }),
		"hidden-cats":  with(func(p *listProps) { p.hiddenCats = items[0].GetCategory() + "," + items[4].GetCategory() }),
		"loading-more": with(func(p *listProps) { p.loadingMore = true }),
	}
	for name, sel := range map[string]listProps{
		"empty-failed":   {loadFailed: true},
		"empty-search":   {sel: scope{Search: "x"}},
		"empty-myfeed":   {sel: scope{MyFeed: true}},
		"empty-unread":   {unreadOnly: true},
		"empty-unread-s": {sel: scope{Unread: true}},
		"empty-later":    {sel: scope{Later: true}},
		"empty-liked":    {sel: scope{Rating: 1}},
		"empty-disliked": {sel: scope{Rating: -1}},
		"empty-notes":    {sel: scope{Notes: true}},
		"empty-tag":      {sel: scope{TagID: "t"}},
		"empty-cat":      {sel: scope{FolderID: "f"}},
		"empty-none":     {},
		"empty-waiting":  {feedsKnown: true, feeds: 3},
	} {
		sel.connected = true
		cases[name] = sel
	}
	for name, p := range cases {
		p := p
		kitSame(t, "list "+name,
			func(tr i18n.Runtime) ui.Node { return listPane(tr, p) },
			func(tr i18n.Runtime) ui.Node { return kitAfterListPane(tr, p) })
	}
}

func TestKitItemMatchesItemRow(t *testing.T) {
	items := fixtureItems(30)
	withIcons := iconHostsOf(fixtureFeeds(12))
	hidden := items[0].GetCategory()
	for _, it := range items {
		for _, hosts := range []map[string]string{nil, withIcons} {
			for _, spawn := range []int{-1, 0, 4} {
				for _, hc := range []string{"", hidden} {
					it, hosts, spawn, hc := it, hosts, spawn, hc
					kitSame(t, "item "+it.GetId(),
						func(tr i18n.Runtime) ui.Node { return itemRow(tr, it, spawn == 4, hosts, spawn, hc) },
						func(tr i18n.Runtime) ui.Node { return kitAfterItemRow(tr, it, spawn == 4, hosts, spawn, hc) })
				}
			}
		}
	}
}

// --- marks ------------------------------------------------------------------

func TestKitMarkMatchesSourceMarkAndFeedIcon(t *testing.T) {
	hosts := iconHostsOf(fixtureFeeds(12))
	for _, svc := range []bool{true, false} {
		HasFaviconService = svc
		for _, id := range []string{"src-1", "src-3", "", "src-unknown"} {
			for _, class := range []string{"item-mark", "article-dot"} {
				id, class := id, class
				kitSame(t, "sourceMark "+id+" "+class,
					func(tr i18n.Runtime) ui.Node { return legacySourceMark(id, hosts, class) },
					func(tr i18n.Runtime) ui.Node { return kitSourceMark(id, hosts, class) })
			}
		}
		for _, f := range fixtureFeeds(4) {
			f := f
			kitSame(t, "feedIcon "+f.GetSourceId(),
				func(tr i18n.Runtime) ui.Node { return legacyFeedIcon(f) },
				func(tr i18n.Runtime) ui.Node {
					return kitMark(kitMarkProps{Class: "feed-dot", WrapClass: "feed-icon-wrap", ImgClass: "feed-icon",
						Src: faviconSrc(iconHost(f))})
				})
		}
	}
	HasFaviconService = true
}

// --- the palette and the tab bar ---------------------------------------------------

// kitBeforePaletteRows is palette()'s row loop, copied as it stands.
func kitBeforePaletteRows(tr i18n.Runtime, p paletteProps) []ui.Node {
	rows := make([]ui.Node, 0, len(p.entries)+1)
	for i, e := range p.entries {
		mark := html.I(html.Props{Class: "pal-mark pal-mark-" + kindClass(e.Kind)})
		if e.Hue != "" {
			mark = html.I(html.Props{Class: "pal-mark",
				Raw: map[string]any{"style": "background:" + e.Hue}})
		}
		rows = append(rows, html.Button(html.Props{
			Class:    "pal-row",
			Key:      "pal-" + strconv.Itoa(int(e.Kind)) + "-" + e.ID,
			DataAttr: html.DataAttribute{Name: "pal", Value: kindClass(e.Kind) + ":" + e.ID},
			ID:       paletteOptionID(i),
			Role:     "option",
			Aria: map[string]string{
				"current":  strconv.FormatBool(i == p.active),
				"selected": strconv.FormatBool(i == p.active),
			},
		},
			mark,
			html.Span(html.Props{Class: "pal-label"}, html.Text(e.Label)),
			ui.If(e.Hint != "", func() ui.Node {
				return html.Span(html.Props{Class: "pal-hint"}, html.Text(e.Hint))
			}),
			html.Span(html.Props{Class: "pal-kind"}, html.Text(kindLabel(tr, e.Kind))),
		))
	}
	return rows
}

func kitAfterPaletteRows(tr i18n.Runtime, p paletteProps) []ui.Node {
	rows := make([]ui.Node, 0, len(p.entries)+1)
	for i, e := range p.entries {
		class := "pal-mark pal-mark-" + kindClass(e.Kind)
		if e.Hue != "" {
			class = "pal-mark"
		}
		var hint ui.Node
		if e.Hint != "" {
			hint = html.Span(html.Props{Class: "pal-hint"}, html.Text(e.Hint))
		}
		rows = append(rows, kitRow(kitRowProps{
			Class:   "pal-row",
			Key:     "pal-" + strconv.Itoa(int(e.Kind)) + "-" + e.ID,
			Hook:    html.DataAttribute{Name: "pal", Value: kindClass(e.Kind) + ":" + e.ID},
			ID:      paletteOptionID(i),
			Role:    "option",
			Current: i == p.active,
			Mark:    kitMark(kitMarkProps{Class: class, Hue: e.Hue}),
			Name:    e.Label, NameClass: "pal-label",
			Tail: []ui.Node{hint, html.Span(html.Props{Class: "pal-kind"}, html.Text(kindLabel(tr, e.Kind)))},
		}))
	}
	return rows
}

func TestKitRowMatchesPaletteRows(t *testing.T) {
	entries := buildPalette(mustRuntime(t), fixtureFeeds(6), fixtureTags(), true)
	for _, q := range []string{"", "fe", "long", "zzz"} {
		p := paletteProps{open: true, query: q, active: 1, entries: filterPalette(entries, q)}
		kitSameNodes(t, "palette "+q,
			func(tr i18n.Runtime) []ui.Node { return kitBeforePaletteRows(tr, p) },
			func(tr i18n.Runtime) []ui.Node { return kitAfterPaletteRows(tr, p) })
	}
	// The empty palette's sentence, which sits outside the listbox.
	kitSame(t, "palette empty",
		func(tr i18n.Runtime) ui.Node {
			return html.Div(html.Props{Class: "pal-empty", Role: "status"},
				html.Text(tr.T("palette", "empty", i18n.Args{"query": "zzz"})))
		},
		func(tr i18n.Runtime) ui.Node {
			return kitEmpty(kitEmptyProps{Class: "pal-empty", Role: "status",
				Text: tr.T("palette", "empty", i18n.Args{"query": "zzz"})})
		})
}

func kitAfterTabBar(tr i18n.Runtime, active view, sel scope) ui.Node {
	tab := func(action, glyph, label string, on bool) ui.Node {
		return kitRow(kitRowProps{
			Class:   "tab",
			Hook:    html.DataAttribute{Name: "action", Value: action},
			Current: on, Label: label,
			Mark: html.Span(html.Props{Class: "tab-glyph"}, html.Text(glyph)),
			Name: label, NameClass: "tab-label",
		})
	}
	home := active != viewRail && active != viewSettings && !sel.Notes && !sel.MyFeed
	return kitNav(kitPaneProps{Class: "tabbar", Label: tr.T("tabs", "aria")},
		tab("tab-home", "◈", tr.T("tabs", "read"), home),
		tab("tab-feeds", "≣", tr.T("tabs", "feeds"), active == viewRail),
		tab("tab-myfeed", glyphMyFeed, tr.T("tabs", "myFeed"), sel.MyFeed && active != viewSettings),
		tab("tab-settings", "⚙", tr.T("tabs", "settings"), active == viewSettings),
	)
}

func TestKitRowMatchesTabBar(t *testing.T) {
	for _, c := range []struct {
		v   view
		sel scope
	}{{viewList, scope{Title: "All"}}, {viewRail, scope{}}, {viewList, scope{MyFeed: true}}, {viewSettings, scope{Notes: true}}} {
		c := c
		kitSame(t, "tabbar",
			func(tr i18n.Runtime) ui.Node { return tabBar(tr, c.v, c.sel) },
			func(tr i18n.Runtime) ui.Node { return kitAfterTabBar(tr, c.v, c.sel) })
	}
}

// --- discover -----------------------------------------------------------------

func kitAfterDiscoverCard(tr i18n.Runtime, r *pb.Recommendation, busy bool, leavingKind string) ui.Node {
	title := r.GetTitle()
	if title == "" {
		title = r.GetDomain()
	}
	titleID := "rec-title-" + r.GetDomain()
	item := kitItemProps{Class: "discover-card", Key: "rec-" + r.GetDomain()}
	if leavingKind != "" {
		item.Data = html.DataAttribute{Name: "leaving", Value: leavingKind}
	}
	return kitItem(item,
		html.Div(html.Props{Class: "discover-card-head"},
			html.H2(html.Props{Class: "discover-card-title", ID: titleID}, html.Text(title)),
			html.A(html.Props{
				Class: "discover-card-domain fs-link", Href: "https://" + r.GetDomain(),
				Target: "_blank", Rel: "noopener noreferrer",
			},
				html.Text(r.GetDomain()),
				html.Span(html.Props{Class: "gl-trail", Aria: map[string]string{"hidden": "true"}},
					html.Text(glyphExternal)),
			),
		),
		html.P(html.Props{Class: "discover-card-evidence"}, html.Text(r.GetEvidence())),
		html.Div(html.Props{Class: "discover-card-actions"},
			html.Button(html.Props{
				Type: "button", Class: "discover-accept", Disabled: busy,
				Data: map[string]string{"role": roleDiscoverFollow + r.GetDomain()},
				Raw:  map[string]any{attrDiscoverAccept: r.GetDomain()},
				Aria: map[string]string{"describedby": titleID},
			}, html.Text(acceptLabel(tr, busy))),
			html.Button(html.Props{
				Type: "button", Class: "discover-reject", Disabled: busy,
				Raw:  map[string]any{attrDiscoverReject: r.GetDomain()},
				Aria: map[string]string{"describedby": titleID},
			}, html.Text(rejectLabel(tr, busy))),
		),
	)
}

func TestKitListMatchesDiscover(t *testing.T) {
	recs := []*pb.Recommendation{
		{Domain: "a.example", Title: "A", Evidence: "seen twice"},
		{Domain: "b.example", Evidence: "linked"},
		{Domain: "c.example", Title: "C"},
	}
	kitSame(t, "discover list",
		func(tr i18n.Runtime) ui.Node {
			return discoverBody(tr, false, false, recs, "b.example", "c.example", "kept")
		},
		func(tr i18n.Runtime) ui.Node {
			cards := make([]ui.Node, 0, len(recs))
			for _, r := range recs {
				kind := ""
				if r.GetDomain() == "c.example" {
					kind = "kept"
				}
				cards = append(cards, kitAfterDiscoverCard(tr, r, r.GetDomain() == "b.example", kind))
			}
			return kitList(kitListProps{Class: "discover-list", Unnamed: true}, cards...)
		})
	kitSame(t, "discover empty",
		func(tr i18n.Runtime) ui.Node { return discoverBody(tr, false, false, nil, "", "", "") },
		func(tr i18n.Runtime) ui.Node {
			return kitEmpty(kitEmptyProps{Class: "discover-empty", Title: tr.T("discover", "empty"), TitleTag: "p",
				Lines: []string{tr.T("discover", "emptyHint")}, LineClass: "discover-hint"})
		})
	kitSame(t, "discover gate",
		func(tr i18n.Runtime) ui.Node {
			return discoverGatedBody(tr, true, false, false, false, nil, "", "", "")
		},
		func(tr i18n.Runtime) ui.Node {
			return kitEmpty(kitEmptyProps{Class: "discover-empty discover-gate", Title: tr.T("discover", "gateTitle"),
				TitleTag: "p", Lines: []string{tr.T("discover", "gateHint")}, LineClass: "discover-hint"})
		})
}

// --- the other empty states ----------------------------------------------------------

func TestKitEmptyMatchesTheOtherEmptyStates(t *testing.T) {
	kitSame(t, "article empty",
		func(tr i18n.Runtime) ui.Node { return articlePane(tr, articleProps{}) },
		func(tr i18n.Runtime) ui.Node {
			return html.Main(html.Props{Class: "pane pane-article"},
				focusPerch(tr, false),
				kitEmpty(kitEmptyProps{Title: tr.T("article", "pickTitle"), Lines: []string{tr.T("article", "pickHint")}}))
		})
	// Reader's fatal screen, copied from reader.go's render as it stands after
	// the UX pass: a <main role="alert"> with an h1 and a Reload action.
	kitSame(t, "fatal",
		func(tr i18n.Runtime) ui.Node {
			return html.Main(html.Props{Class: "empty empty-fatal", Role: "alert"},
				html.H1(html.Props{}, html.Text(tr.T("reader", "fatalTitle"))),
				html.P(html.Props{}, html.Text("boom")),
				html.P(html.Props{}, html.Text(tr.T("reader", "fatalHint"))),
				html.Button(html.Props{
					Class:    "btn empty-add",
					DataAttr: html.DataAttribute{Name: "action", Value: actReload},
				}, html.Text(tr.T("reader", "fatalReload"))),
			)
		},
		func(tr i18n.Runtime) ui.Node {
			return kitEmpty(kitEmptyProps{Main: true, Class: "empty empty-fatal", Role: "alert",
				Title: tr.T("reader", "fatalTitle"), TitleTag: "h1",
				Lines: []string{"boom", tr.T("reader", "fatalHint")},
				Action: kitAction(kitChipProps{Action: actReload, Look: kitChipBtn, Extra: "empty-add",
					Label: tr.T("reader", "fatalReload")})})
		})
	// The first-run list, against the real function (list.go emptyFirstRun).
	kitSame(t, "first run",
		func(tr i18n.Runtime) ui.Node { return emptyFirstRun(tr) },
		func(tr i18n.Runtime) ui.Node { return kitAfterEmptyFirstRun(tr) })
	// A settings tab with nothing in it (settingsActivity, settingsSpeed).
	kitSame(t, "set-note",
		func(tr i18n.Runtime) ui.Node {
			return html.Div(html.Props{Class: "set-note"}, html.Text(tr.T("settings", "activityEmpty")))
		},
		func(tr i18n.Runtime) ui.Node {
			return kitEmpty(kitEmptyProps{Class: "set-note", Text: tr.T("settings", "activityEmpty")})
		})
}

// --- skeletons ------------------------------------------------------------------

func TestKitSkeletonsMatch(t *testing.T) {
	for i := -1; i < 10; i++ {
		i := i
		kitSame(t, "skeletonRow "+strconv.Itoa(i),
			func(tr i18n.Runtime) ui.Node { return legacySkeletonRow(i) },
			func(tr i18n.Runtime) ui.Node { return kitSkItemRow(i) })
	}
	kitSame(t, "skeletonList",
		func(tr i18n.Runtime) ui.Node { return skeletonList(tr) },
		func(tr i18n.Runtime) ui.Node { return kitAfterSkeletonList(tr) })
	kitSameNodes(t, "settingsSkeleton",
		func(tr i18n.Runtime) []ui.Node { return legacySettingsSkeleton() },
		func(tr i18n.Runtime) []ui.Node { return kitSkLines("set-sk-", 0, 6, "sk-w-90") })
	// feedSettings' loading loop and the add-feed ladder's pair, copied.
	kitSameNodes(t, "feedSettings skeleton",
		func(tr i18n.Runtime) []ui.Node {
			var body []ui.Node
			for i := 0; i < 5; i++ {
				body = append(body, html.Div(html.Props{Class: "sk sk-line sk-w-90", Key: "fs-sk-" + strconv.Itoa(i)}))
			}
			return body
		},
		func(tr i18n.Runtime) []ui.Node { return kitSkLines("fs-sk-", 0, 5, "sk-w-90") })
	kitSameNodes(t, "addfeed skeleton",
		func(tr i18n.Runtime) []ui.Node {
			return []ui.Node{html.Div(html.Props{Class: "sk sk-line sk-w-70"}), html.Div(html.Props{Class: "sk sk-line sk-w-90"})}
		},
		func(tr i18n.Runtime) []ui.Node { return kitSkLines("", 0, 2, "sk-w-70", "sk-w-90") })
	kitSame(t, "skeletonArticle",
		func(tr i18n.Runtime) ui.Node { return skeletonArticle(tr) },
		func(tr i18n.Runtime) ui.Node {
			ws := []string{"sk-w-90", "sk-w-90", "sk-w-70", "sk-w-45"}
			kids := kitSkLines("sk-a-", 0, 4, ws...)
			kids = append(kids, html.Div(html.Props{Class: "sk sk-img", Key: "sk-a-img"}))
			kids = append(kids, kitSkLines("sk-a-", 4, 4, ws...)...)
			return kitSkRegion("article-body", tr.T("article", "loadingBody"), kids...)
		})
}

// --- the activity log ----------------------------------------------------------------

func kitBeforeLogRow(tr i18n.Runtime, r *pb.LogRecord) ui.Node {
	full := r.GetMessage()
	if r.GetAttrs() != "" {
		full += "\n" + r.GetAttrs()
	}
	return html.Div(html.Props{
		Class: "log-row",
		Title: full,
		Data:  map[string]string{"level": strings.ToLower(r.GetLevel())},
	},
		html.Span(html.Props{Class: "log-time"}, html.Text(relOrNever(tr, r.GetTime()))),
		html.Span(html.Props{Class: "log-level"}, html.Text(strings.ToLower(r.GetLevel()))),
		html.Div(html.Props{Class: "log-msg"},
			html.Span(html.Props{Class: "log-text"}, html.Text(r.GetMessage())),
			ui.If(r.GetAttrs() != "", func() ui.Node {
				return html.Span(html.Props{Class: "log-attrs"}, html.Text(r.GetAttrs()))
			}),
		),
	)
}

func kitAfterLogRow(tr i18n.Runtime, r *pb.LogRecord) ui.Node {
	full := r.GetMessage()
	var attrs ui.Node
	if r.GetAttrs() != "" {
		full += "\n" + r.GetAttrs()
		attrs = html.Span(html.Props{Class: "log-attrs"}, html.Text(r.GetAttrs()))
	}
	return kitRow(kitRowProps{
		Class: "log-row", Static: true, Title: full,
		Data: map[string]string{"level": strings.ToLower(r.GetLevel())},
		Mark: html.Span(html.Props{Class: "log-time"}, html.Text(relOrNever(tr, r.GetTime()))),
		Name: strings.ToLower(r.GetLevel()), NameClass: "log-level",
		Tail: []ui.Node{html.Div(html.Props{Class: "log-msg"},
			html.Span(html.Props{Class: "log-text"}, html.Text(r.GetMessage())), attrs)},
	})
}

func TestKitRowMatchesLogRow(t *testing.T) {
	for _, r := range []*pb.LogRecord{
		{Time: fixtureOld, Level: "ERROR", Message: "dial failed", Attrs: "host=x"},
		{Time: "", Level: "INFO", Message: "started"},
	} {
		r := r
		kitSame(t, "log "+r.GetLevel(),
			func(tr i18n.Runtime) ui.Node { return kitBeforeLogRow(tr, r) },
			func(tr i18n.Runtime) ui.Node { return kitAfterLogRow(tr, r) })
	}
}

// --- the article eyebrow, landmarks, disclosure ------------------------------------

func TestKitMetaMatchesArticleEyebrow(t *testing.T) {
	p := fixtureArticle(3)
	for _, it := range []*pb.Item{p.stream[0], p.stream[1]} {
		it := it
		// articleBlock's eyebrow, copied.
		kitSame(t, "eyebrow "+it.GetId(),
			func(tr i18n.Runtime) ui.Node {
				return html.Div(html.Props{Class: "article-eyebrow"},
					legacySourceMark(it.GetSourceId(), p.iconHosts, "article-dot"),
					html.Span(html.Props{Class: "item-source", DataAttr: html.DataAttribute{Name: "source-id", Value: it.GetSourceId()}},
						html.Text(it.GetSourceTitle())),
					html.I(html.Props{Class: "item-sep"}),
					timeTag(tr, it.GetPublishedAt()),
					ui.If(it.GetWordCount() >= 50, func() ui.Node { return html.I(html.Props{Class: "item-sep"}) }),
					ui.If(it.GetWordCount() >= 50, func() ui.Node {
						return html.Span(html.Props{}, html.Text(readingTime(tr, it.GetWordCount())))
					}),
					ui.If(it.GetRevision() > 0, func() ui.Node { return html.I(html.Props{Class: "item-sep"}) }),
					ui.If(it.GetRevision() > 0, func() ui.Node { return editedMark(tr, it, p) }),
				)
			},
			func(tr i18n.Runtime) ui.Node {
				long, edited := it.GetWordCount() >= 50, it.GetRevision() > 0
				return kitMeta("article-eyebrow", true,
					kitSourceMark(it.GetSourceId(), p.iconHosts, "article-dot"),
					kitSource(it.GetSourceId(), it.GetSourceTitle()),
					kitSep(),
					timeTag(tr, it.GetPublishedAt()),
					kitSepIf(long),
					ui.If(long, func() ui.Node { return html.Span(html.Props{}, html.Text(readingTime(tr, it.GetWordCount()))) }),
					kitSepIf(edited),
					ui.If(edited, func() ui.Node { return editedMark(tr, it, p) }),
				)
			})
	}
}

func TestKitLandmarksMatch(t *testing.T) {
	p := fixtureArticle(3)
	p.revisionsErr = map[string]bool{"a1": true}
	kitSame(t, "revisions region",
		func(tr i18n.Runtime) ui.Node { return revisionsPanel(tr, "a1", p) },
		func(tr i18n.Runtime) ui.Node {
			return kitRegion(kitPaneProps{Class: "article-revisions", Key: "revs-a1", Label: tr.T("article", "previousTitle")},
				html.P(html.Props{Class: "rev-empty"}, html.Text(tr.T("article", "revisionsError"))))
		})
	// settingsPane's tab strip, copied.
	kitSame(t, "settings nav",
		func(tr i18n.Runtime) ui.Node {
			return html.Nav(html.Props{Class: "set-tabs", Aria: map[string]string{"label": tr.T("settings", "navLabel")}})
		},
		func(tr i18n.Runtime) ui.Node {
			return kitNav(kitPaneProps{Class: "set-tabs", Label: tr.T("settings", "navLabel")})
		})
}

func TestKitDisclosureAriaMatchesBothControlsHelpers(t *testing.T) {
	for _, open := range []bool{false, true} {
		rail := legacyRailControls(map[string]string{"expanded": strconv.FormatBool(open), "label": "x"}, railCatKey("c1"), open)
		if got := kitDisclosureAria("x", open, kitFoldID(railCatKey("c1"))); !reflect.DeepEqual(got, rail) {
			t.Errorf("open=%v: kit %v, railControls %v", open, got, rail)
		}
		note := legacyNoteControls(map[string]string{"expanded": strconv.FormatBool(open), "label": "x"}, "i1", open)
		if got := kitDisclosureAria("x", open, noteBodyID("i1")); !reflect.DeepEqual(got, note) {
			t.Errorf("open=%v: kit %v, noteControls %v", open, got, note)
		}
	}
}

// --- invariants ------------------------------------------------------------------

var kitVoid = map[string]bool{"img": true, "input": true, "br": true, "hr": true, "meta": true, "link": true, "source": true, "wbr": true}

// kitChildren returns the opening tags of the direct children of the first
// element whose opening tag contains marker. Attribute values are escaped by
// the SSR writer, so a '>' never appears inside a tag.
func kitChildren(t *testing.T, doc, marker string) []string {
	t.Helper()
	at := strings.Index(doc, marker)
	if at < 0 {
		t.Fatalf("marker %q not in:\n%s", marker, doc)
	}
	i := at + strings.IndexByte(doc[at:], '>') + 1
	depth := 0
	var out []string
	for {
		lt := strings.IndexByte(doc[i:], '<')
		if lt < 0 {
			return out
		}
		i += lt
		gt := strings.IndexByte(doc[i:], '>')
		tag := doc[i : i+gt+1]
		i += gt + 1
		if strings.HasPrefix(tag, "</") {
			if depth == 0 {
				return out
			}
			depth--
			continue
		}
		if depth == 0 {
			out = append(out, tag)
		}
		name := strings.TrimLeft(tag, "<")
		if j := strings.IndexAny(name, " >/"); j >= 0 {
			name = name[:j]
		}
		if !kitVoid[name] && !strings.HasSuffix(tag, "/>") {
			depth++
		}
	}
}

// TestKitListChildrenAreListItems: every member of every kit list is a
// listitem, across the rail at full size (every rail row and category the kit
// builds), the item list, and Discover.
func TestKitListChildrenAreListItems(t *testing.T) {
	check := func(name, doc, listMarker string, allow func(tag string) bool) {
		lists := 0
		for rest := doc; ; {
			j := strings.Index(rest, listMarker)
			if j < 0 {
				break
			}
			lists++
			kids := kitChildren(t, rest[j:], listMarker)
			if len(kids) == 0 {
				t.Errorf("%s: a list with no items was rendered (OmitEmpty should drop it)", name)
			}
			for _, k := range kids {
				if !strings.Contains(k, `role="listitem"`) && (allow == nil || !allow(k)) {
					t.Errorf("%s: list child is not a listitem: %s", name, k)
				}
			}
			rest = rest[j+len(listMarker):]
		}
		if lists == 0 {
			t.Errorf("%s: no list found", name)
		}
	}
	for name, p := range kitRailCases() {
		if name == "rail-loading-empty" || name == "rail-loading-folded" || name == "rail-folded" {
			continue
		}
		p := p
		check(name, renderView(t, func(tr i18n.Runtime) ui.Node { return kitAfterRailPane(tr, p) }), `role="list"`, nil)
	}
	// The item list is the one kit list with non-items in it, and both are kept
	// as they are and named. A placeholder row is aria-hidden, so it is out of
	// the accessibility tree and the list stays valid. The paging tail
	// (listTail's `.list-more`) is not: a button or a line of text as a direct
	// child of role=list — a unification candidate, wanting a listitem of its
	// own or a place outside the scroller.
	items := fixtureItems(12)
	lp := listProps{items: items, connected: true, total: 20, hasMore: true, sel: scope{Title: "All"}, viewport: 4000}
	check("list", renderView(t, func(tr i18n.Runtime) ui.Node { return kitAfterListPane(tr, lp) }), `role="list"`,
		func(tag string) bool {
			return strings.Contains(tag, `aria-hidden="true"`) || strings.Contains(tag, "list-more")
		})
	check("kitList", renderView(t, func(tr i18n.Runtime) ui.Node {
		return kitList(kitListProps{Class: "rail-list", Label: "x"},
			kitItem(kitItemProps{Class: "rail-cell", Key: "a"}, html.Button(html.Props{})),
			kitRow(kitRowProps{Class: "feed-row", Name: "b", Slot: "feed-slot", Item: true, Key: "b"}))
	}), `role="list"`, nil)
	if n := kitList(kitListProps{Class: "rail-list", OmitEmpty: true}); n != nil {
		t.Errorf("an empty OmitEmpty list rendered an element")
	}
}

// TestKitSectionControlsTheFoldItOpens: a band's toggle names its fold in
// aria-controls exactly when the section is open, and the id it names is in
// the document exactly once. Closed, the fold is absent; closing, it is
// present for its exit but no longer controlled.
func TestKitSectionControlsTheFoldItOpens(t *testing.T) {
	for _, c := range []struct {
		name         string
		open         bool
		closing      string
		wantFold     bool
		wantControls bool
	}{
		{"open", true, "", true, true},
		{"closed", false, "", false, false},
		{"closing", false, actTags, true, false},
		{"closing another", false, actFeeds, false, false},
	} {
		doc := renderView(t, func(tr i18n.Runtime) ui.Node {
			return html.Div(html.Props{}, kitSection(kitSectionProps{
				Band:    kitBandProps{Glyph: glyphTags, Label: "Tags", Action: actTags, Open: c.open, Hidden: 3},
				Closing: c.closing,
			}, kitItem(kitItemProps{Class: "rail-cell", Key: "a"}, html.Button(html.Props{})))...)
		})
		id := kitFoldID(actTags)
		toggle := elementTag(t, doc, `class="rail-band-toggle"`)
		hasControls := strings.Contains(toggle, `aria-controls="`+id+`"`)
		ids := strings.Count(doc, `id="`+id+`"`)
		if hasControls != c.wantControls {
			t.Errorf("%s: aria-controls present = %v, want %v: %s", c.name, hasControls, c.wantControls, toggle)
		}
		if (ids == 1) != c.wantFold || ids > 1 {
			t.Errorf("%s: fold id appears %d times, want fold=%v", c.name, ids, c.wantFold)
		}
		if hasControls && ids != 1 {
			t.Errorf("%s: aria-controls names an id that is not in the document exactly once", c.name)
		}
		if !strings.Contains(toggle, `aria-expanded="`+strconv.FormatBool(c.open)+`"`) {
			t.Errorf("%s: aria-expanded does not say %v: %s", c.name, c.open, toggle)
		}
		if strings.Contains(doc, `class="rail-band-count"`) == c.open {
			t.Errorf("%s: the hidden count shows exactly when the section is closed", c.name)
		}
	}
}

// TestKitRowNameCarriesItsCount: a row whose count is on screen says it in its
// accessible name, as "{name}, {n} unread". Three rows do not, today, and are
// pinned here as they are so that unifying them is a visible decision: the
// stream rows have no aria-label (the button's name is its content, the count
// a bare integer at the end of it), the tag row's label is "Tagged {name}"
// with its feed count left out, and the category row's label counts FEEDS
// while the number on screen counts unread articles.
//
// The spoken count is the catalog's plural interpolation, which does not group
// digits: the row SHOWS "1,234" and SAYS "1234 unread". A screen reader reads
// both as the same number, so this is noted rather than failed — but it is two
// formattings of one fact, which is what thousands() exists to prevent.
func TestKitRowNameCarriesItsCount(t *testing.T) {
	tr := mustRuntime(t)
	n, words := 1234, strconv.Itoa(1234)+" unread"
	f := &pb.Feed{SourceId: "s1", Title: "The Feed", UnreadCount: int32(n)}
	carry := map[string]ui.Node{
		"feed":          kitAfterFeedRow(tr, f, false, false),
		"topic":         kitAfterTopicRow(tr, categorySlugs()[0], false, n),
		"uncategorised": kitAfterUncategorisedRow(tr, false, n),
	}
	names := map[string]string{"feed": "The Feed", "topic": categoryName(tr, categorySlugs()[0]),
		"uncategorised": tr.T("rail", "uncategorised")}
	for row, node := range carry {
		doc := renderView(t, func(i18n.Runtime) ui.Node { return node })
		tag := elementTag(t, doc, `class="feed-row`)
		if !strings.Contains(doc, `class="feed-count">`+thousands(tr, n)+`<`) {
			t.Fatalf("%s: fixture drift, no count on screen: %s", row, doc)
		}
		if !strings.Contains(tag, `aria-label="`+names[row]+", "+words+`"`) {
			t.Errorf("%s: accessible name does not carry the name and the count in words: %s", row, tag)
		}
	}

	// The three that do not, pinned.
	stream := renderView(t, func(tr i18n.Runtime) ui.Node {
		return kitAfterSpecialRow(tr, glyphAll, "All", streamAll, n, false)
	})
	if strings.Contains(elementTag(t, stream, `class="feed-row stream-row"`), "aria-label") {
		t.Errorf("stream rows now carry a label: update this pin and the kit's unification note")
	}
	tagDoc := renderView(t, func(tr i18n.Runtime) ui.Node {
		return kitAfterTagRow(tr, &pb.Tag{Id: "t", Name: "go", FeedCount: int32(n)}, false)
	})
	if strings.Contains(elementTag(t, tagDoc, `data-tag-id="t"`), thousands(tr, n)) {
		t.Errorf("tag rows now carry their count: update this pin")
	}
	cat := renderView(t, func(tr i18n.Runtime) ui.Node {
		return kitAfterCategoryRows(tr, railProps{}, "c1", "News", []*pb.Feed{f}, true)
	})
	if !strings.Contains(elementTag(t, cat, `data-folder-id="c1"`), "News, 1 feed") {
		t.Errorf("category rows no longer name their feed count: update this pin")
	}
}

// --- benchmarks ------------------------------------------------------------------
//
// Build only, the shape bench_test.go calls Build: what a render pays to
// construct the rows, without serialising them. Compare allocs/op and B/op
// between each Before and After; ns/op on a shared machine is the neighbours.

func BenchmarkKitItemRowBefore(b *testing.B) {
	tr := benchRuntime(b)
	items := fixtureItems(10)
	hosts := iconHostsOf(fixtureFeeds(150))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = itemRow(tr, items[i%len(items)], i%10 == 0, hosts, -1, "")
	}
}

func BenchmarkKitItemRowAfter(b *testing.B) {
	tr := benchRuntime(b)
	items := fixtureItems(10)
	hosts := iconHostsOf(fixtureFeeds(150))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kitAfterItemRow(tr, items[i%len(items)], i%10 == 0, hosts, -1, "")
	}
}

func BenchmarkKitFeedRowBefore(b *testing.B) {
	tr := benchRuntime(b)
	feeds := fixtureFeeds(150)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = legacyFeedRow(tr, feeds[i%len(feeds)], i%10 == 0, false)
	}
}

func BenchmarkKitFeedRowAfter(b *testing.B) {
	tr := benchRuntime(b)
	feeds := fixtureFeeds(150)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kitAfterFeedRow(tr, feeds[i%len(feeds)], i%10 == 0, false)
	}
}

func BenchmarkKitTopicRowBefore(b *testing.B) {
	tr := benchRuntime(b)
	slugs := categorySlugs()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = legacyTopicRow(tr, slugs[i%len(slugs)], false, i%3)
	}
}

func BenchmarkKitTopicRowAfter(b *testing.B) {
	tr := benchRuntime(b)
	slugs := categorySlugs()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = kitAfterTopicRow(tr, slugs[i%len(slugs)], false, i%3)
	}
}

func BenchmarkKitSkeletonRowBefore(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = legacySkeletonRow(i % 64)
	}
}

func BenchmarkKitSkeletonRowAfter(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = kitSkItemRow(i % 64)
	}
}

// The whole rail through SSR, before and after, at the size this instance
// runs at — the row savings as the rail actually spends them.
func BenchmarkKitRailSSR150Before(b *testing.B) {
	p := kitRailCases()["rail-150"]
	benchSSR(b, func(tr i18n.Runtime) ui.Node { return railPane(p) })
}

func BenchmarkKitRailSSR150After(b *testing.B) {
	p := kitRailCases()["rail-150"]
	benchSSR(b, func(tr i18n.Runtime) ui.Node { return kitAfterRailPane(tr, p) })
}
