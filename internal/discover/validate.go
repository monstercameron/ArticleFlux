package discover

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/recommend"
)

// aggregatorHostThreshold is how many DISTINCT external hosts a feed's items
// must link to before it is treated as an aggregator (§18.7's "would mostly
// re-serve feeds you already have" rejection).
//
// Four, not one or two: a normal blog links out a few times without being an
// aggregator, and a feed whose items are themselves mostly links to other
// sites — a linkblog, a "best of the web" digest — clears this easily. A
// lower threshold would reject ordinary blogs that happen to cite sources;
// this is a heuristic, documented as one, not a claim of certainty.
const aggregatorHostThreshold = 4

// validateWindow is how far back items are counted for PostsPerWeek. Ninety
// days, matching recommendjob.Window — the same "how far back is this
// domain's activity" question, answered the same way in both places.
const validateWindow = 90 * 24 * time.Hour

// minDatedItemsForRate is the fewest dated items required before a cadence is
// reported at all. Below this, "posts ~weekly" from one or two items is a
// guess dressed as a measurement — see the package comment on Validate.
const minDatedItemsForRate = 3

// Validate discovers whether a domain publishes a feed and, if so, reads it
// to answer recommendjob.Validator's three questions: is it healthy, what is
// its feed URL, and what is it called. It satisfies recommendjob.Validator
// structurally — this package does not import recommendjob to avoid the
// import cycle recommendjob already has on internal/recommend.
//
// # Honest about what it does not know
//
// PostsPerWeek is left at zero rather than extrapolated from one or two dated
// items — recommend.gate treats a zero LastPostAt as "cannot be judged" and
// rejects, which is correct: a feed this validator could not date is not one
// this validator can vouch for as healthy, and pretending otherwise would let
// a candidate through the health gate on invented evidence. See §18.7's own
// argument for the health gate: a bad recommendation costs more than a
// missing one.
func (f *Fetcher) Validate(ctx context.Context, domain string) (recommend.Health, string, string) {
	// Real callers pass a bare domain from outlink harvesting ("example.com"),
	// which always means https. Tests pass a full http://127.0.0.1:port URL
	// against a local fixture server — recognised by the scheme already being
	// present — because forcing https here would make Validate untestable
	// without a TLS fixture for no gain in what is being verified.
	root := domain
	if !strings.Contains(domain, "://") {
		root = "https://" + domain
	}

	_, candidates, err := f.Feeds(ctx, root)
	if err != nil || len(candidates) == 0 {
		// Feeds() already distinguishes "unreachable" from "reachable, no feed"
		// only by returning an error for the former; a page that answered but
		// declared/probed nothing usable is the far more common case and is
		// reachable with no feed, not a dead site.
		reachable := err == nil
		return recommend.Health{Reachable: reachable}, "", ""
	}

	// Feeds() already orders by strength of evidence — declared over probed,
	// more items over fewer — so the first candidate is the one worth
	// validating further, exactly as the reader would be shown first.
	best := candidates[0]

	page, ferr := f.feeds.Fetch(ctx, best.URL, feed.Conditional{})
	if ferr != nil || page == nil {
		// The candidate parsed a moment ago inside Feeds() and failed to parse
		// again here — a transient failure on the second, independent fetch.
		// Reachable (the page answered before) but not vouched for.
		return recommend.Health{Reachable: true, HasFeed: false}, "", ""
	}

	health := recommend.Health{
		Reachable: true,
		HasFeed:   true,
		Samples:   sampleItems(page.Items),
	}
	health.LastPostAt, health.PostsPerWeek = cadence(page.Items)
	health.IsAggregator = looksLikeAggregator(root, page.Items)

	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = best.Title
	}
	return health, best.URL, title
}

// cadence returns the newest dated item and an honest publishing rate, or a
// zero LastPostAt when there is not enough dated evidence to report one — see
// minDatedItemsForRate.
func cadence(items []feed.Item) (time.Time, float64) {
	var newest time.Time
	var dated int
	oldest := time.Time{}
	for _, it := range items {
		if it.PublishedAt.IsZero() {
			continue
		}
		dated++
		if it.PublishedAt.After(newest) {
			newest = it.PublishedAt
		}
		if oldest.IsZero() || it.PublishedAt.Before(oldest) {
			oldest = it.PublishedAt
		}
	}
	if dated < minDatedItemsForRate {
		return newest, 0
	}
	span := newest.Sub(oldest)
	if span <= 0 {
		return newest, 0
	}
	if span > validateWindow {
		span = validateWindow
	}
	weeks := span.Hours() / (24 * 7)
	if weeks <= 0 {
		return newest, 0
	}
	return newest, float64(dated) / weeks
}

// looksLikeAggregator reports whether a feed's own items mostly point at
// other sites rather than pages on the domain being evaluated — a heuristic,
// not a certainty, and documented as such at aggregatorHostThreshold.
func looksLikeAggregator(rootURL string, items []feed.Item) bool {
	root, err := url.Parse(rootURL)
	if err != nil {
		return false
	}
	self := strings.TrimPrefix(strings.ToLower(root.Host), "www.")

	hosts := map[string]bool{}
	for _, it := range items {
		u, err := url.Parse(it.URL)
		if err != nil || u.Host == "" {
			continue
		}
		h := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
		if h == self {
			continue
		}
		hosts[h] = true
	}
	return len(hosts) >= aggregatorHostThreshold
}
