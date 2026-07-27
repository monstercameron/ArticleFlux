//go:build js && wasm

package view

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/GoWebComponents/v5/ui"
	"google.golang.org/protobuf/proto"
)

// The reader's pure helpers: list edits, tag bookkeeping, and formatting.
//
// Every function here is a value in and a value out, with two deliberate exceptions
// — setLocalRead, setLocalStarred, setLocalRating and adjustUnread/adjustRanked take
// state handles, because an optimistic update has to touch both the list and the
// article stream and doing it in one place is what keeps them agreeing.
//
// They were the tail of a seven-thousand-line reader.go. Moving them is safe in a way
// that moving the component is not: nothing here calls a hook, so nothing here can be
// broken by a change in call ORDER (GWC hooks are positional). That property is the
// reason this file exists and the reason it was the first thing extracted.

func withRead(cur []*pb.Item, id string, read bool) []*pb.Item {
	next := make([]*pb.Item, len(cur))
	for i, it := range cur {
		if it.GetId() == id {
			c := cloneItem(it)
			c.Read = read
			next[i] = c
			continue
		}
		next[i] = it
	}
	return next
}

// folderOf is which category a feed is filed under, "" for none.
func folderOf(feeds []*pb.Feed, sourceID string) string {
	for _, f := range feeds {
		if f.GetSourceId() == sourceID {
			return f.GetFolderId()
		}
	}
	return ""
}

// folderName resolves a category id to its name for the dialog's heading.
func folderName(folders []*pb.Folder, id string) string {
	for _, f := range folders {
		if f.GetId() == id {
			return f.GetName()
		}
	}
	return ""
}

// feedsInFolder is what a category currently holds, which is what makes the
// delete warning concrete: "moves 4 feeds to Unfiled" is checkable where "this
// cannot be undone" says nothing about this particular category.
func feedsInFolder(feeds []*pb.Feed, folderID string) []*pb.Feed {
	if folderID == "" {
		return nil
	}
	var out []*pb.Feed
	for _, f := range feeds {
		if f.GetFolderId() == folderID {
			out = append(out, f)
		}
	}
	return out
}

// withFolder returns the sidebar's feeds with one of them refiled.
//
// A copy with a cloned feed, for the reason withRead documents: the reconciler
// compares props, and a mutation in place leaves the old and new values
// indistinguishable, so the row that moved would not repaint.
func withFolder(cur []*pb.Feed, sourceID, folderID string) []*pb.Feed {
	next := make([]*pb.Feed, len(cur))
	for i, f := range cur {
		if f.GetSourceId() == sourceID {
			c := cloneFeed(f)
			c.FolderId = folderID
			next[i] = c
			continue
		}
		next[i] = f
	}
	return next
}

// setLocalRating applies a verdict to the reading stream and the fetched body.
// The item LIST is updated by the caller through setItems, because that one has
// to go through the Ref that survives renders.
func setLocalRating(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	id string, rating int) {
	stream.Set(withRating(stream.Get(), id, rating))

	if b, ok := bodies.Get()[id]; ok {
		c := cloneItem(b)
		c.Rating = int32(rating)
		bodies.Set(withEntry(bodies.Get(), id, c))
	}
}

// setLocalStarred and setLocalRead mirror setLocalRating: the stream and the
// fetched body, with the item LIST handled by the caller through setItems.
func setLocalStarred(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	id string, starred bool) {
	stream.Set(withStarred(stream.Get(), id, starred))
	if b, ok := bodies.Get()[id]; ok {
		c := cloneItem(b)
		c.Starred = starred
		bodies.Set(withEntry(bodies.Get(), id, c))
	}
}

func setLocalRead(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	id string, read bool) {
	stream.Set(withRead(stream.Get(), id, read))
	if b, ok := bodies.Get()[id]; ok {
		c := cloneItem(b)
		c.Read = read
		bodies.Set(withEntry(bodies.Get(), id, c))
	}
}

func withStarred(cur []*pb.Item, id string, starred bool) []*pb.Item {
	next := make([]*pb.Item, len(cur))
	for i, it := range cur {
		if it.GetId() == id {
			c := cloneItem(it)
			c.Starred = starred
			next[i] = c
			continue
		}
		next[i] = it
	}
	return next
}

func withRating(cur []*pb.Item, id string, rating int) []*pb.Item {
	next := make([]*pb.Item, len(cur))
	for i, it := range cur {
		if it.GetId() == id {
			c := cloneItem(it)
			c.Rating = int32(rating)
			next[i] = c
			continue
		}
		next[i] = it
	}
	return next
}

// cloneItem copies an item so an optimistic update produces a NEW pointer.
//
// proto.Clone rather than `c := *it`: a generated message embeds a
// protoimpl.MessageState containing a mutex, and copying it by value is a real
// bug (go vet flags it) that can corrupt the message's internal state the next
// time the proto runtime touches it.
//
// The new pointer is the point. The reconciler compares props by identity, so
// mutating in place would leave old and new indistinguishable and the row would
// never repaint.
func cloneItem(it *pb.Item) *pb.Item {
	return proto.Clone(it).(*pb.Item)
}

func cloneFeed(f *pb.Feed) *pb.Feed {
	return proto.Clone(f).(*pb.Feed)
}

// adjustRanked moves My Feed's badge optimistically, clamped at zero.
//
// Its own tiny function rather than an inline Set, for the reason adjustUnread is one: the
// clamp is the whole content. A badge that can go negative is a badge that has been
// double-decremented somewhere, and "-1" on screen is how that bug reports itself — which is
// better than silently wrapping, and better still if it cannot happen.
//
// The authoritative value arrives with the next sidebar fetch (ListFeedsResponse.ranked_count),
// so drift here is corrected rather than permanent. This exists so the number moves under the
// reader's hand instead of a round trip later.
func adjustRanked(ranked ui.State[int], delta int) {
	if n := ranked.Get() + delta; n >= 0 {
		ranked.Set(n)
	}
}

func adjustUnread(feeds ui.State[[]*pb.Feed], total ui.State[int], sourceID string, delta int) {
	cur := feeds.Get()
	next := make([]*pb.Feed, len(cur))
	for i, f := range cur {
		if f.GetSourceId() == sourceID {
			c := cloneFeed(f)
			n := c.GetUnreadCount() + int32(delta)
			if n < 0 {
				n = 0
			}
			c.UnreadCount = n
			next[i] = c
			continue
		}
		next[i] = f
	}
	feeds.Set(next)
	if t := total.Get() + delta; t >= 0 {
		total.Set(t)
	}
}

// withEntry returns a copy of a map with one key set.
//
// A copy, not a mutation: the reconciler compares props by identity, and a map
// mutated in place is indistinguishable from the one it replaced, so the field
// that was typed into never repaints.
func withEntry[V any](m map[string]V, k string, v V) map[string]V {
	next := make(map[string]V, len(m)+1)
	for kk, vv := range m {
		next[kk] = vv
	}
	next[k] = v
	return next
}

// itemOrCurrent resolves an article action to the article it acts on.
//
// The fetched body wins over the stream entry when there is one, because that is
// the copy carrying the authoritative starred flag and URL. An empty id means
// "whatever is being read", which is what the keyboard shortcuts pass — they
// have no element to name.
// itemAfter is the next article in the LIST, which is the order a continuous
// session plays in.
//
// The list and not the reading stream, deliberately. The stream is a window
// that has been paged around and may not extend past what is loaded; the list
// is what the reader is looking at and what "until the end of the list" means
// to them. An id that is not in the list returns nil rather than the first
// item — silently restarting at the top would turn a finished queue into a loop
// nobody asked for.
func itemAfter(list []*pb.Item, id string) *pb.Item {
	if id == "" {
		return nil
	}
	for i, it := range list {
		if it.GetId() == id {
			if i+1 < len(list) {
				return list[i+1]
			}
			return nil
		}
	}
	return nil
}

func itemOrCurrent(stream ui.State[[]*pb.Item], bodies ui.State[map[string]*pb.Item],
	current ui.State[*pb.Item], id string) *pb.Item {
	if id == "" {
		if c := current.Get(); c != nil {
			id = c.GetId()
		}
	}
	if id == "" {
		return nil
	}
	if b, ok := bodies.Get()[id]; ok && b != nil {
		return b
	}
	for _, it := range stream.Get() {
		if it.GetId() == id {
			return it
		}
	}
	return nil
}

// iconHostsOf maps source id -> favicon host, derived from the sidebar's feed
// list. Built on the client because the sidebar already has the site URLs, and
// repeating them on every item of every page would be bytes on the wire for
// something already present.
func iconHostsOf(feeds []*pb.Feed) map[string]string {
	m := make(map[string]string, len(feeds))
	for _, f := range feeds {
		if h := iconHost(f); h != "" {
			m[f.GetSourceId()] = h
		}
	}
	return m
}

// paletteEntriesIf builds the palette only when it is open.
//
// Assembling and sorting ~170 entries is cheap once and absurd sixty times a
// second, which is what it was costing while the reader scrolled the item list
// with the dialog closed.
func paletteEntriesIf(tr i18n.Runtime, open bool, feeds []*pb.Feed, tags []*pb.Tag, q string) []paletteEntry {
	if !open {
		return nil
	}
	return filterPalette(buildPalette(tr, feeds, tags), q)
}

// fsName resolves a source id to its title for the scope the action switches to.
func fsName(a *actions, id string) string {
	if f := a.feedByID(id); f != nil {
		return f.GetTitle()
	}
	return "Feed"
}

// hoursFromNow is the mute expiry, in the RFC3339 the column stores.
func hoursFromNow(h int) string {
	return time.Now().UTC().Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
}

// hoursUntil is its inverse, for showing which mute choice is active.
func hoursUntil(ts string) int {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		if t, err = time.Parse(time.RFC3339Nano, ts); err != nil {
			return 0
		}
	}
	return int(time.Until(t).Hours())
}

// tagLabelsBySource is tagsForSource for every feed at once, for the reading
// stream — which shows articles from several feeds at a time and would otherwise
// resolve the same id→tag table once per article on screen.
//
// Both names, not one: the chip DRAWS the label and ACTS on the name. SetFeedTag
// is keyed by name (a tag is created on first use and removed with its last
// association, so the name is the handle a reader actually has), while the label
// is what the same tag reads as everywhere else once it has been renamed for the
// rail. Collapsing the two into one string would mean either chips that
// contradict the sidebar or a remove that asks the server to take off a tag it
// has never heard of.
func tagLabelsBySource(tags []*pb.Tag, bySource map[string][]string,
	pending map[string][]string) map[string][]tagRef {

	if len(bySource) == 0 && len(pending) == 0 {
		return nil
	}
	byID := make(map[string]*pb.Tag, len(tags))
	for _, t := range tags {
		byID[t.GetId()] = t
	}
	out := make(map[string][]tagRef, len(bySource)+len(pending))
	for src, ids := range bySource {
		refs := make([]tagRef, 0, len(ids))
		for _, id := range ids {
			if t := byID[id]; t != nil {
				refs = append(refs, tagRef{Label: tagDisplay(t), Name: t.GetName()})
			}
		}
		// Stable order, because these are chips with a destructive control on
		// them: a row that reshuffles between renders moves the × out from under
		// the pointer, and the tag that gets removed is the one that slid into
		// its place. Map iteration above orders the FEEDS, which nothing reads;
		// this orders what is drawn — so it sorts on the LABEL, which is what is
		// drawn, rather than on the name, which is not.
		sort.Slice(refs, func(i, j int) bool { return refs[i].Label < refs[j].Label })
		if len(refs) > 0 {
			out[src] = refs
		}
	}

	// The in-flight ones go on the END rather than into the sort, so a chip does
	// not jump position at the moment it stops being pending — which is the
	// frame the reader is looking at. Appended after the settled ones for the
	// same reason: the newest thing they did belongs where they left the cursor.
	for src, names := range pending {
		for _, n := range names {
			out[src] = append(out[src], tagRef{Label: n, Name: n, Pending: true})
		}
	}
	return out
}

// tagsForSource lists the tags on one feed, from the map the sidebar already
// holds. No round trip for something already in memory.
//
// Display names, because these chips are read and not acted on — the feed
// panel's tag list is a statement of what this feed is filed under. The article
// chips, which ARE acted on, need the handle as well and go through
// tagLabelsBySource instead.
func tagsForSource(tags []*pb.Tag, bySource map[string][]string, sourceID string) []string {
	if sourceID == "" {
		return nil
	}
	ids := bySource[sourceID]
	if len(ids) == 0 {
		return nil
	}
	byID := make(map[string]*pb.Tag, len(tags))
	for _, t := range tags {
		byID[t.GetId()] = t
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := byID[id]; t != nil {
			out = append(out, tagDisplay(t))
		}
	}
	return out
}

// withTagRemoved is the optimistic half of removeTag: the state as it will be
// once the server agrees.
//
// It applies the server's own two rules rather than approximating them, because
// an optimistic update that guesses differently from the server produces a
// screen that CORRECTS ITSELF a second later, which is more alarming than the
// wait it saved:
//
//   - the association goes;
//   - and a tag with no associations left goes with it, since a tag is removed
//     with its last feed (see store.SetFeedTag).
//
// Copies throughout, including a clone of the tag whose count changes. The
// reconciler compares props, so a tag mutated in place is indistinguishable from
// the old one and the row never repaints — and the caller is holding the
// originals as its rollback, which an in-place edit would have destroyed.
func withTagRemoved(tags []*pb.Tag, bySource map[string][]string, sourceID, name string) ([]*pb.Tag, map[string][]string) {
	var tagID string
	for _, t := range tags {
		if strings.EqualFold(t.GetName(), name) {
			tagID = t.GetId()
			break
		}
	}
	if tagID == "" {
		return tags, bySource
	}

	next := make(map[string][]string, len(bySource))
	remaining := 0
	for src, ids := range bySource {
		kept := make([]string, 0, len(ids))
		for _, id := range ids {
			if id == tagID && src == sourceID {
				continue
			}
			if id == tagID {
				remaining++
			}
			kept = append(kept, id)
		}
		if len(kept) > 0 {
			next[src] = kept
		}
	}

	outTags := make([]*pb.Tag, 0, len(tags))
	for _, t := range tags {
		if t.GetId() != tagID {
			outTags = append(outTags, t)
			continue
		}
		if remaining == 0 {
			continue // its last feed: the tag goes too
		}
		outTags = append(outTags, &pb.Tag{
			Id: t.GetId(), Name: t.GetName(), Label: t.GetLabel(),
			Glyph: t.GetGlyph(), FeedCount: int32(remaining),
		})
	}
	return outTags, next
}

// withPending and withoutPending maintain the in-flight tag map. Copy-on-write
// like every other map in this component, for the reason above: a mutation the
// reconciler cannot see is a render that does not happen.
func withPending(m map[string][]string, sourceID, name string) map[string][]string {
	out := make(map[string][]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	for _, n := range out[sourceID] {
		if strings.EqualFold(n, name) {
			return out // already waiting on this one
		}
	}
	out[sourceID] = append(append([]string{}, out[sourceID]...), name)
	return out
}

func withoutPending(m map[string][]string, sourceID, name string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		if k != sourceID {
			out[k] = v
			continue
		}
		kept := make([]string, 0, len(v))
		for _, n := range v {
			if !strings.EqualFold(n, name) {
				kept = append(kept, n)
			}
		}
		if len(kept) > 0 {
			out[k] = kept
		}
	}
	return out
}

// feedsForTag names the feeds carrying a tag, for the tag panel's "what is in
// here" list. The inverse of tagsForSource, over the same association map — the
// sidebar is already holding both halves, so neither direction costs a request.
//
// Sorted, because it is a list a reader reads rather than a list a machine
// consumes, and map iteration would reorder it on every render.
func feedsForTag(feeds []*pb.Feed, bySource map[string][]string, tagID string) []string {
	if tagID == "" || len(bySource) == 0 {
		return nil
	}
	titles := make(map[string]string, len(feeds))
	for _, f := range feeds {
		titles[f.GetSourceId()] = f.GetTitle()
	}
	var out []string
	for src, ids := range bySource {
		for _, id := range ids {
			if id != tagID {
				continue
			}
			if n := titles[src]; n != "" {
				out = append(out, n)
			}
			break
		}
	}
	sort.Strings(out)
	return out
}

func currentID(it *pb.Item) string {
	if it == nil {
		return ""
	}
	return it.GetId()
}

// streamAtStart reports whether the reading stream has reached the top of the
// list — the point at which scrolling up has nothing more to bring in.
//
// An id comparison, not an indexOf. Both edge checks run on every render, and a
// linear scan of a 3,621-item list twice per scroll frame is exactly the kind of
// cost that does not show up until the list is real.
func streamAtStart(list, stream []*pb.Item) bool {
	if len(stream) == 0 || len(list) == 0 {
		return false
	}
	return stream[0].GetId() == list[0].GetId()
}

// streamAtEnd reports whether the stream has reached the end of the FEED, not
// merely the end of what has been paged. A cursor that is still live means there
// is more to come, so "that's everything" would be a lie.
func streamAtEnd(list, stream []*pb.Item, cursor string) bool {
	if len(stream) == 0 || len(list) == 0 || cursor != "" {
		return false
	}
	return stream[len(stream)-1].GetId() == list[len(list)-1].GetId()
}

func indexOf(list []*pb.Item, it *pb.Item) int {
	if it == nil {
		return -1
	}
	for i, x := range list {
		if x.GetId() == it.GetId() {
			return i
		}
	}
	return -1
}

// navItem is the pure math behind j/k and the list's up/down arrows: the item
// `delta` positions from current in list, or nil when there is nowhere to go.
//
// current not found in list (including current == nil, e.g. nothing opened
// yet) is treated as "one before the first item" — indexOf already returns
// -1 for that — so advancing (delta > 0) from it lands on list[0], which is
// what lets j open the first article before anything has been read, and
// retreating (delta < 0) from it goes nowhere, since there is no "before the
// first item, and nothing current either" to retreat from.
//
// This is deliberately free of ui.State: it is called through the actions
// Ref's navStep field (see the actions struct) precisely so the keyboard
// listener — registered once, well before this function is ever reached —
// never reads a State handle itself.
func navItem(list []*pb.Item, current *pb.Item, delta int) *pb.Item {
	i := indexOf(list, current) + delta
	if i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
}

// relTime renders a timestamp the way a reader wants it: how long ago, not when.
//
// "3h" answers "is this new?" at a glance; "2026-07-26T15:04:05Z" does not.
func relTime(tr i18n.Runtime, rfc3339 string) string {
	t, err := time.Parse(time.RFC3339Nano, rfc3339)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, rfc3339); err != nil {
			return ""
		}
	}
	// The unit abbreviations are the same keys the settings screen uses, so
	// "5m" here and "5m" there cannot drift into two different translations of
	// the same idea.
	unit := tr.NS("unit")
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return tr.T("time", "now")
	case d < time.Hour:
		return unit.T("minutes", i18n.Args{"n": strconv.Itoa(int(d.Minutes()))})
	case d < 24*time.Hour:
		return unit.T("hours", i18n.Args{"n": strconv.Itoa(int(d.Hours()))})
	case d < 7*24*time.Hour:
		return unit.T("days", i18n.Args{"n": strconv.Itoa(int(d.Hours() / 24))})
	default:
		// NOT t.Format("2 Jan"). Go's layouts emit English month names in every
		// locale, and this is the single most-rendered string in the app.
		return tr.T("time", "dayMonth", i18n.Args{
			"day":   strconv.Itoa(t.Day()),
			"month": tr.T("month", strconv.Itoa(int(t.Month()))),
		})
	}
}

// clampPane keeps a pane between a usable minimum and a sensible maximum.
func clampPane(w, min, max int) int {
	switch {
	case w < min:
		return min
	case w > max:
		return max
	default:
		return w
	}
}

// parsePx reads a "272px" custom property back into a number, falling back when
// the value is missing or in units we did not write.
func parsePx(v string, fallback int) int {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// hueVarFor returns the inline style that carries a source's hue into a subtree.
// One pure function feeds the dot, the row edge, the article wash and the link
// underline, so all four always agree.
//
// It returns a Raw prop holding a style STRING rather than html.Props.Style,
// and that is not a style preference — it is the only thing that works.
//
// GWC's Style map goes through WASMDOMAdapter.SetStyles, which does
// `element.style[name] = value`. JS property assignment **silently ignores CSS
// custom properties**: setting style["--c"] is a no-op, because custom
// properties are only reachable through style.setProperty(). The whole hue
// system rendered grey until this was traced.
//
// A style attribute string takes a different path in the reconciler
// (propKindStyle with a string value -> setAttribute), and the attribute parser
// does honour custom properties.
func hueVarFor(sourceID string) map[string]any {
	if sourceID == "" {
		return nil
	}
	return map[string]any{"style": "--c:" + design.HueFor(sourceID)}
}
