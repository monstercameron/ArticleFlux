package demodata

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Instance is one demo reader's whole world: the subscriptions, the articles,
// the categories, the tags and the preferences.
//
// It lives for as long as the tab does and is never written anywhere. That is
// the demo's central promise and the reason it can be handed to strangers: a
// reader poking at it is poking at their own copy, and closing the tab is the
// undo button for everything.
//
// One mutex over the whole thing, for the reason client/data gives for the same
// choice: the callers are a browser's callbacks on a single-threaded scheduler,
// where an uncontended lock costs approximately nothing, and "nearly free and
// obviously correct" beats reasoning about which handlers can interleave.
type Instance struct {
	mu sync.Mutex

	now     func() time.Time
	started time.Time

	folders []*folder
	feeds   []*feed
	items   []*item
	tags    []*tag
	prefs   map[string]string

	// incoming is the articles Refresh has not handed over yet.
	//
	// A demo where the refresh button does nothing teaches the wrong thing about
	// the application: polling is most of what a reader does for you. So a few
	// items are held back, stamped with the moment they are released, and
	// arriving unread — which is what a real poll looks like from the inside.
	incoming []*item

	// undo maps a MarkAllRead token to exactly the items that call marked, which
	// is what makes the undo offer honest rather than "mark everything unread".
	undo map[string][]string

	// interest is the dial over the interest profile (§18.2) — the one part of
	// that screen a demo reader can change. See interest.go for what is fixture
	// and what is computed.
	interest *interest

	// applied is the idempotency store §20.7 describes, small enough here to be
	// a map. The outbox replays writes it could not confirm, so the same key
	// legitimately arrives twice and the second one must change nothing.
	applied map[string]bool

	// recDismissed and recPasses are Discover's whole state (§18.7) — see
	// discover.go, which keeps the fixtures and runs the real scorer over them.
	//
	// A dismissal is permanent and lives here rather than on the fixture,
	// because the fixture is the harvest and this is the reader's answer to it;
	// recPasses is which harvest has run, so Refresh has something to find.
	recDismissed map[string]bool
	recPasses    int

	// revisions is the edit history behind §10.1's "this changed" disclosure,
	// keyed by item id. Empty for all but one article, which is what it looks
	// like on a real instance: an empty history means "no edit seen", never
	// "no edit happened".
	revisions map[string][]*pb.ItemRevision

	rev   int64
	seq   int64
	polls int32

	// calls and logs are what the Settings screen's Server and Activity
	// sections read. They are the cheapest possible version of the real thing —
	// a counter per method and a capped ring of records — and they exist because
	// those two screens are otherwise the only ones in the demo with nothing in
	// them, which reads as broken rather than as unavailable.
	calls map[string]int64
	logs  []*pb.LogRecord
}

func (in *Instance) clock() time.Time {
	if in.now == nil {
		return time.Now()
	}
	return in.now()
}

// count records one RPC against the method stats the settings screen reads.
func (in *Instance) count(method string) {
	in.mu.Lock()
	in.calls[method]++
	in.mu.Unlock()
}

// nextID mints a demo-local identifier.
//
// Sequential rather than random: the ids show up in the DOM as data attributes
// and in the read cache's keys, and a stable sequence makes a demo run
// reproducible for anyone debugging one.
func (in *Instance) nextID(prefix string) string {
	in.seq++
	return prefix + "-" + strconv.FormatInt(in.seq, 10)
}

// --- the model ----------------------------------------------------------------

type folder struct {
	id       string
	name     string
	position int32
}

func (f *folder) message() *pb.Folder {
	return &pb.Folder{Id: f.id, Name: f.name, Position: f.position}
}

// feed is one subscription and the source underneath it, collapsed into one
// struct.
//
// The server keeps those apart because a source is global and shared between
// tenants (A14) and a subscription is one person's. The demo has exactly one
// person, so the distinction has nothing to distinguish — but the split is
// still visible where it matters, in the feed settings panel, which reads the
// shared fields off this same struct and labels them shared.
type feed struct {
	id       string
	sourceID string
	// pubTitle is the publisher's own title; override is the reader's name for
	// it. Kept separate rather than resolved on write, because the settings
	// panel offers "clear this and use the publisher's" and a single field
	// cannot answer that.
	pubTitle string
	override string
	feedURL  string
	siteURL  string
	folderID string
	kind     string

	inMegafeed    bool
	cacheDepth    int32
	fetchInterval int32
	mutedUntil    string

	lastFetch   time.Time
	lastSuccess time.Time
	failures    int32
	lastError   string
}

func (f *feed) title() string {
	if f.override != "" {
		return f.override
	}
	return f.pubTitle
}

func (f *feed) message(unread int32) *pb.Feed {
	m := &pb.Feed{
		Id:                  f.id,
		SourceId:            f.sourceID,
		Title:               f.title(),
		FeedUrl:             f.feedURL,
		SiteUrl:             f.siteURL,
		UnreadCount:         unread,
		FolderId:            f.folderID,
		ConsecutiveFailures: f.failures,
		LastError:           f.lastError,
	}
	if !f.lastSuccess.IsZero() {
		m.LastSuccessAt = stamp(f.lastSuccess)
	}
	return m
}

type item struct {
	id     string
	feed   *feed
	title  string
	author string
	// summary is the list row's second line. Stored separately from the body
	// rather than derived from it, because a truncated first paragraph is a bad
	// summary and every real feed supplies one.
	summary   string
	url       string
	published time.Time
	body      string
	words     int32

	read    bool
	readAt  string
	starred bool
	rating  int32
	note    string
	noteAt  time.Time

	// weight is the megafeed's interest score before recency is applied. The
	// ranked homepage is one of the things worth showing off, and a demo where
	// every scope returns the same order in the same sequence shows nothing.
	weight float64
	// reason is what the ranked list says about why this is here. Every ranked
	// item carries one, because a reason is what makes the ranking arguable.
	reason string
}

// message renders an item for the wire.
//
// full is GetItem and the notes list; everything else gets the list shape. The
// difference is not cosmetic — a 50-item page carrying article bodies is
// megabytes over a tunnel for text nobody has scrolled to, and a demo that sent
// them anyway would be quietly measuring a different application.
func (i *item) message(full bool) *pb.Item {
	m := &pb.Item{
		Id:          i.id,
		SourceId:    i.feed.sourceID,
		SourceTitle: i.feed.title(),
		Title:       i.title,
		Author:      i.author,
		Summary:     i.summary,
		Url:         i.url,
		PublishedAt: stamp(i.published),
		Read:        i.read,
		Starred:     i.starred,
		WordCount:   i.words,
		Rating:      i.rating,
	}
	if full {
		m.ContentHtml = i.body
		m.Note = i.note
	}
	return m
}

// rankReason is why this article is in the ranked list.
//
// **Every ranked row carries one, and the fallback below is what guarantees
// that.** A reason is what makes a ranking arguable — a reader who disagrees
// with a row needs to know what the ranker thought it knew, or the only
// available response is to distrust the whole list. The fixtures hand-write the
// interesting ones; this derives the rest from the same inputs the score used,
// so a derived reason is never a claim the score did not make.
func (i *item) rankReason(now time.Time) string {
	if i.reason != "" {
		return i.reason
	}
	switch {
	case i.starred:
		return "You saved this"
	case i.rating > 0:
		return "You liked this"
	case now.Sub(i.published) < 6*time.Hour:
		return "New from " + i.feed.title()
	case i.words > 900:
		return "A long read from " + i.feed.title()
	default:
		return "From " + i.feed.title() + ", which you keep up with"
	}
}

type tag struct {
	id      string
	name    string
	label   string
	glyph   string
	sources map[string]bool
}

func (t *tag) message() *pb.Tag {
	return &pb.Tag{
		Id: t.id, Name: t.name, Label: t.label, Glyph: t.glyph,
		FeedCount: int32(len(t.sources)),
	}
}

// --- lookups ------------------------------------------------------------------

func (in *Instance) feedByID(sourceID string) *feed {
	for _, f := range in.feeds {
		if f.sourceID == sourceID || f.id == sourceID {
			return f
		}
	}
	return nil
}

func (in *Instance) feedByURL(url string) *feed {
	for _, f := range in.feeds {
		if strings.EqualFold(f.feedURL, url) || strings.EqualFold(f.siteURL, url) {
			return f
		}
	}
	return nil
}

func (in *Instance) itemByID(id string) *item {
	for _, it := range in.items {
		if it.id == id {
			return it
		}
	}
	return nil
}

func (in *Instance) folderByID(id string) *folder {
	for _, f := range in.folders {
		if f.id == id {
			return f
		}
	}
	return nil
}

// folderByName is the import's lookup (opml.go): an OPML group names a folder
// rather than identifying one, so a re-run has to recognise the categories it
// created last time instead of making a second set with the same names.
func (in *Instance) folderByName(name string) *folder {
	for _, f := range in.folders {
		if strings.EqualFold(f.name, name) {
			return f
		}
	}
	return nil
}

func (in *Instance) tagByName(name string) *tag {
	for _, t := range in.tags {
		if strings.EqualFold(t.name, name) {
			return t
		}
	}
	return nil
}

func (in *Instance) unreadFor(sourceID string) int32 {
	var n int32
	for _, it := range in.items {
		if !it.read && it.feed.sourceID == sourceID {
			n++
		}
	}
	return n
}

func (in *Instance) itemsFor(sourceID string) int32 {
	var n int32
	for _, it := range in.items {
		if it.feed.sourceID == sourceID {
			n++
		}
	}
	return n
}

// resort puts the item list back in newest-first order.
//
// Called after anything that adds items. The list is the order everything else
// assumes, so keeping it sorted in one place is cheaper than remembering to
// sort at every read — and at demo scale the sort is a few dozen elements.
func (in *Instance) resort() {
	sort.SliceStable(in.items, func(a, b int) bool {
		return in.items[a].published.After(in.items[b].published)
	})
}

// stamp formats a time the way every timestamp on this API is formatted:
// RFC3339, UTC. The client parses these, and a local-zone stamp from a browser
// in Bangkok would sort correctly and display wrong.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
