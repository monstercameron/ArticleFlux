// Package seedread simulates a reading history, so the interest layer has something to
// derive from on a development instance.
//
// # Why this exists
//
// My Feed only shows items with a CONTENT-level reason — a topic match, a followed brand, a
// corroborated story (see derive.hasContentMatch). That is deliberate: a ranked page whose
// every row says "this is new and you subscribe to that feed" is the unread list reordered,
// and it is better to be empty and say so.
//
// The consequence is that a fresh database has an empty My Feed, correctly, and stays that
// way until somebody reads for a while. That makes the whole feature unobservable in
// development: you cannot look at a page that is honestly blank and tell working code from
// broken code. This produces the history that would otherwise take weeks.
//
// # What it is NOT
//
// Not test fixtures — internal/derive's own tests build their corpora in-process, where
// they can assert on exact vectors. This runs against a real database with real ingested
// items, whatever they happen to be, which is the situation the thresholds actually have to
// survive.
//
// And not random. Given the same items and the same seed value it produces the same
// history, because "the ranking looks wrong" is only diagnosable if the input can be
// reproduced. Every timestamp is derived from the item's own publication date rather than
// from time.Now, so a database seeded today and the same one seeded next week both produce a
// reader who behaved consistently relative to what they were reading.
package seedread

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Options configure a simulated history.
type Options struct {
	// Focus are substrings that make an item interesting to this reader, matched
	// case-insensitively against the title. Empty means "derive the focus from the corpus"
	// — see Run.
	//
	// Substrings rather than feeds, because the point is to produce a reader with SUBJECTS
	// rather than one with favourite sources. A history built by picking whole feeds
	// produces feed affinity and nothing else, which is exactly the signal My Feed already
	// refuses to treat as a recommendation.
	Focus []string
	// Read is roughly what fraction of the interesting items get opened, 0..1. Zero means
	// DefaultRead.
	Read float64
	// Seed makes the run reproducible. Any value; the same one gives the same history.
	Seed uint64
	// Now anchors the simulated timeline. Zero means time.Now().
	//
	// A parameter because the whole history is placed relative to it, and a caller
	// replaying an old database wants the engagements to land where the articles are rather
	// than in a cluster at the present moment.
	Now time.Time
}

// DefaultRead is the share of interesting items an enthusiastic reader opens.
//
// Sixty percent. Not a hundred: a reader who opens everything they find interesting gives
// the scorer no negative signal at all, and the `skipped` and `bounced` paths would then be
// dead in every development database — which is how they came to be unexercised in the first
// place.
const DefaultRead = 0.6

// Result reports what was written.
type Result struct {
	Items       int
	Impressions int
	Opened      int
	Completed   int
	Reread      int
	Liked       int
	Bounced     int
	Skipped     int
	Focus       []string
}

// Run writes a simulated history over the items already in the database.
//
// It reads items rather than creating them, so the history is about the reader's REAL
// subscriptions — the same feeds, the same headlines, the same publication dates that the
// ranking will be scored against. Inventing articles would produce a profile the ranker
// never sees again.
func Run(ctx context.Context, repo *store.ReaderRepo, sc store.Scope, opt Options) (Result, error) {
	var res Result
	if opt.Read <= 0 {
		opt.Read = DefaultRead
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now().UTC()
	}

	// Everything, read or not: a history is written over what the reader HAS, and an item
	// already marked read is one they plausibly read.
	//
	// PAGED, because ListItems silently clamps its limit to store.MaxLimit. Asking for ten
	// thousand and getting two hundred is not an error — it returns a perfectly good first
	// page — so the first version of this seeded a history over 200 of 3,848 items and
	// reported success. The symptom was a thin profile that looked like a threshold problem.
	var items []store.Item
	for cursor := ""; len(items) < MaxItems; {
		page, next, err := repo.ListItems(ctx, sc, store.ListQuery{
			Limit: store.MaxLimit, Cursor: cursor,
		})
		if err != nil {
			return res, err
		}
		items = append(items, page...)
		if next == "" || len(page) == 0 {
			break
		}
		cursor = next
	}
	if len(items) == 0 {
		return res, fmt.Errorf("seedread: no items; poll some feeds first")
	}
	// Sorted so the walk order does not depend on the query plan. By TITLE rather than by
	// id, for the same reason the decisions below hash the title: an id is generated per
	// database, so ordering by it would make two instances holding the same articles walk
	// them differently.
	sort.Slice(items, func(i, j int) bool {
		if items[i].Title != items[j].Title {
			return items[i].Title < items[j].Title
		}
		return items[i].ID < items[j].ID
	})

	focus := opt.Focus
	if len(focus) == 0 {
		focus = deriveFocus(items, opt.Seed)
	}
	res.Focus = focus

	var evs []signals.Event
	emit := func(kind signals.Kind, itemID string, at time.Time, value float64, surface signals.Surface) {
		evs = append(evs, signals.Event{
			ID: idgen.New(), ItemID: itemID, Kind: kind, Value: value,
			Surface: surface, At: at.UnixMilli(),
			// One session id per simulated day, so SameSession and the skip derivation
			// see a plausible sitting structure rather than one enormous session — which
			// would make every skip fail the cross-sitting test.
			SessionID: at.Format("2006-01-02"),
		})
	}

	for _, it := range items {
		published, err := time.Parse(time.RFC3339Nano, it.PublishedAt)
		if err != nil || published.After(opt.Now) {
			continue
		}
		// Every per-item decision hashes the TITLE, not the item id.
		//
		// The id is generated per database, so hashing it made the history reproducible
		// only within one instance — two dev databases holding the same articles produced
		// different readers, which the reproducibility test caught by disagreeing on one
		// `liked`. A title is stable across instances, so a seed value can be shared and
		// two people can look at the same reader over the same corpus.
		//
		// Collisions are harmless: two items with identical headlines behave identically,
		// which is exactly what one would want.
		key := it.Title
		// Seen a little after it was published — the reader is not sitting on the feed.
		seen := published.Add(time.Duration(40+hashMod(key, "seen", 200)) * time.Minute)
		if seen.After(opt.Now) {
			seen = opt.Now
		}
		res.Items++

		interesting := matchesFocus(it.Title, focus)
		roll := hashUnit(key, "read", opt.Seed)

		// Everyone sees everything. Impressions are the denominator (§18.1) and a history
		// without them makes every rate below a number computed against nothing.
		emit(signals.Impression, it.ID, seen, 1, signals.SurfaceList)
		res.Impressions++

		switch {
		case interesting && roll < opt.Read:
			// Opened, dwelt on, and mostly finished.
			opened := seen.Add(time.Duration(1+hashMod(key, "open", 8)) * time.Minute)
			emit(signals.Chose, it.ID, opened, 0, signals.SurfaceList)
			emit(signals.Opened, it.ID, opened, 0, signals.SurfaceReader)
			res.Opened++

			// Dwell as attentive milliseconds, scaled to the article's own length so
			// signals.Classify can reach "Read" rather than "Bounce". A fixed duration
			// would classify as a full read of a link post and an abandonment of an essay.
			words := it.WordCount
			if words <= 0 {
				words = 500
			}
			ms := signals.ExpectedReadMS(words)
			ms += ms / 4 * int64(hashMod(key, "dwell", 3)) // 100%–150% of expected
			emit(signals.Dwell, it.ID, opened.Add(time.Minute), float64(ms), signals.SurfaceReader)

			done := opened.Add(time.Duration(ms) * time.Millisecond)
			if done.After(opt.Now) {
				done = opt.Now
			}
			emit(signals.Completed, it.ID, done, 0, signals.SurfaceReader)
			res.Completed++

			// The deliberate acts, on a minority. They are sparse BY NATURE — that
			// sparsity is D18's whole argument for the precision stage — so a seeder
			// that liked everything would erase the distinction it exists to create.
			if hashMod(key, "like", 4) == 0 {
				emit(signals.Liked, it.ID, done, 0, signals.SurfaceReader)
				res.Liked++
			}
			if hashMod(key, "reread", 5) == 0 {
				emit(signals.Reread, it.ID, done, 1, signals.SurfaceReader)
				res.Reread++
			}

		case interesting:
			// Interesting but passed over more than once: this is what produces the
			// `skipped` signal, which needs repeat exposure across separate sittings.
			emit(signals.Impression, it.ID, seen.Add(26*time.Hour), 1, signals.SurfaceList)
			emit(signals.Impression, it.ID, seen.Add(50*time.Hour), 1, signals.SurfaceList)
			res.Impressions += 2
			res.Skipped++

		case hashMod(key, "bounce", 12) == 0:
			// Occasionally opens something outside their interests and leaves at once.
			// An informed rejection, and the only source of negative affinity here.
			opened := seen.Add(3 * time.Minute)
			emit(signals.Opened, it.ID, opened, 0, signals.SurfaceReader)
			emit(signals.Bounced, it.ID, opened.Add(8*time.Second), 8000, signals.SurfaceReader)
			res.Opened++
			res.Bounced++
		}
	}

	if err := write(ctx, repo, sc, evs); err != nil {
		return res, err
	}
	return res, nil
}

// MaxItems bounds one seeding run. Ten thousand items is far more reading history than the
// ninety-day derivation window will look at, so anything beyond it is work with no effect.
const MaxItems = 10000

// write stores the events in batches the storage layer will accept.
//
// store.RecordEngagements caps a batch to keep the single writer's transactions short
// (A24), and a seeding run produces thousands of rows, so the split belongs here rather
// than being a surprise at the boundary.
func write(ctx context.Context, repo *store.ReaderRepo, sc store.Scope, evs []signals.Event) error {
	for len(evs) > 0 {
		n := len(evs)
		if n > store.MaxEngagementBatch {
			n = store.MaxEngagementBatch
		}
		if _, err := repo.RecordEngagements(ctx, sc, evs[:n]); err != nil {
			return err
		}
		evs = evs[n:]
	}
	return nil
}

// FocusTerms is how many subjects the simulated reader cares about.
//
// Four. Enough that topic clustering has more than one cluster to find and that the
// diversity measures in §18.4 have something to measure, few enough that each one collects
// the several engaged items a topic needs to exist at all. A reader interested in twenty
// things spreads their history so thin that no cluster reaches MinMembers — which is the
// same failure a real reader with eclectic taste hits, and not the one a development
// database should be demonstrating.
const FocusTerms = 4

// deriveFocus picks the subjects this reader will care about, from the corpus itself.
//
// # Why from the corpus rather than a hardcoded list
//
// A fixed list like {"android", "sqlite"} produces nothing on a database of cycling feeds:
// no title matches, no item is interesting, and the seeder silently writes a history of
// pure impressions. Taking the most common substantial words in the actual titles
// guarantees the focus HITS, whatever the reader happens to subscribe to.
//
// The seed shifts which of the candidates are chosen, so two dev databases can be given
// different readers over identical items.
func deriveFocus(items []store.Item, seed uint64) []string {
	counts := map[string]int{}
	for _, it := range items {
		seen := map[string]bool{}
		for _, w := range words(it.Title) {
			// Six letters or more: short words are dominated by the ordinary vocabulary
			// of headlines, and a "focus" of "with" makes every item interesting, which
			// is the same as having no focus.
			if len(w) < 6 || seen[w] {
				continue
			}
			seen[w] = true
			counts[w]++
		}
	}
	type wc struct {
		w string
		n int
	}
	var all []wc
	for w, n := range counts {
		// At least three headlines. A word appearing twice cannot support a topic and
		// would give this reader an interest with no evidence behind it.
		if n >= 3 {
			all = append(all, wc{w, n})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].w < all[j].w
	})
	if len(all) == 0 {
		return nil
	}

	// Taken from a window rather than strictly the top, so the seed can choose a different
	// reader. The window is bounded because the tail is words appearing exactly three
	// times, which produce a reader with almost no history.
	window := len(all)
	if window > FocusTerms*4 {
		window = FocusTerms * 4
	}
	out := make([]string, 0, FocusTerms)
	step := int(seed % uint64(window))
	for i := 0; i < window && len(out) < FocusTerms; i++ {
		out = append(out, all[(i+step)%window].w)
	}
	sort.Strings(out)
	return out
}

func words(s string) []string {
	var out []string
	cur := make([]rune, 0, 16)
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			cur = append(cur, r)
		case r >= 'A' && r <= 'Z':
			cur = append(cur, r+('a'-'A'))
		case r >= '0' && r <= '9':
			cur = append(cur, r)
		default:
			if len(cur) > 0 {
				out = append(out, string(cur))
				cur = cur[:0]
			}
		}
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

func matchesFocus(title string, focus []string) bool {
	if len(focus) == 0 {
		return false
	}
	for _, w := range words(title) {
		for _, f := range focus {
			if w == f {
				return true
			}
		}
	}
	return false
}

// hashUnit maps an item id to a stable value in [0,1).
//
// A hash rather than a random source, so the same database seeds to the same history every
// time — "the ranking looks wrong" is only diagnosable when the input can be reproduced.
func hashUnit(key, salt string, seed uint64) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(salt))
	var b [8]byte
	for i := range b {
		b[i] = byte(seed >> (8 * i))
	}
	_, _ = h.Write(b[:])
	return float64(h.Sum64()%10000) / 10000
}

// hashMod is hashUnit for the cases that want a small integer.
func hashMod(key, salt string, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte(salt))
	return int(h.Sum64() % uint64(n))
}
