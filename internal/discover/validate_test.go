package discover

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/feed"
)

var vnow = time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

// cadence must not extrapolate a rate from fewer than minDatedItemsForRate
// dated items — recommend.gate reads a zero LastPostAt as "cannot be judged"
// and rejects, which is the honest outcome for a feed this validator could
// not actually date, not a bug to work around.
func TestCadenceRefusesToGuessFromTooFewDatedItems(t *testing.T) {
	items := []feed.Item{
		{Title: "one", PublishedAt: vnow.Add(-1 * 24 * time.Hour)},
		{Title: "two", PublishedAt: vnow.Add(-2 * 24 * time.Hour)},
		// A third, undated item — common on real feeds — must not count
		// toward minDatedItemsForRate.
		{Title: "three"},
	}
	newest, rate := cadence(items)
	if rate != 0 {
		t.Errorf("rate = %v, want 0 with only 2 dated items", rate)
	}
	if newest.IsZero() {
		t.Error("newest = zero, want the newest dated item's time even when rate is refused")
	}
}

func TestCadenceComputesARateFromEnoughDatedItems(t *testing.T) {
	items := []feed.Item{
		{Title: "one", PublishedAt: vnow},
		{Title: "two", PublishedAt: vnow.Add(-7 * 24 * time.Hour)},
		{Title: "three", PublishedAt: vnow.Add(-14 * 24 * time.Hour)},
	}
	newest, rate := cadence(items)
	if !newest.Equal(vnow) {
		t.Errorf("newest = %v, want %v", newest, vnow)
	}
	// 3 items over a 2-week span is 1.5/week.
	if rate < 1.4 || rate > 1.6 {
		t.Errorf("rate = %v, want ~1.5/week", rate)
	}
}

func TestCadenceHandlesAllItemsOnTheSameDay(t *testing.T) {
	items := []feed.Item{
		{Title: "one", PublishedAt: vnow},
		{Title: "two", PublishedAt: vnow},
		{Title: "three", PublishedAt: vnow},
	}
	_, rate := cadence(items)
	if rate != 0 {
		t.Errorf("rate = %v, want 0 when every item shares one timestamp (zero span)", rate)
	}
}

// looksLikeAggregator: a normal blog that cites a couple of sources must not
// be flagged, but a feed whose items mostly point elsewhere must be — this is
// the health-gate signal for "would mostly re-serve feeds you already have".
func TestLooksLikeAggregatorNeedsSeveralDistinctExternalHosts(t *testing.T) {
	blog := []feed.Item{
		{URL: "https://blog.example/post-1"},
		{URL: "https://blog.example/post-2"},
		{URL: "https://cited-once.example/x"}, // one citation, not a pattern
	}
	if looksLikeAggregator("https://blog.example", blog) {
		t.Error("an ordinary blog citing one external source was flagged as an aggregator")
	}

	aggregator := []feed.Item{
		{URL: "https://a.example/1"},
		{URL: "https://b.example/1"},
		{URL: "https://c.example/1"},
		{URL: "https://d.example/1"},
	}
	if !looksLikeAggregator("https://linkblog.example", aggregator) {
		t.Error("a feed of links to 4 distinct external hosts was not flagged as an aggregator")
	}
}

func TestLooksLikeAggregatorIgnoresWWWWhenComparingToSelf(t *testing.T) {
	items := []feed.Item{
		{URL: "https://www.blog.example/post-1"},
		{URL: "https://blog.example/post-2"},
	}
	if looksLikeAggregator("https://www.blog.example", items) {
		t.Error("the feed's own domain (with/without www) was counted as an external host")
	}
}

func TestSampleItemsCapsAtMaxSamplesAndKeepsOnlyTitleAndSummary(t *testing.T) {
	items := []feed.Item{
		{Title: "one", Summary: "s1", URL: "https://x.example/1", Author: "a"},
		{Title: "two", Summary: "s2", URL: "https://x.example/2"},
		{Title: "three", Summary: "s3", URL: "https://x.example/3"},
	}
	got := sampleItems(items)
	if len(got) != MaxSamples {
		t.Fatalf("len(samples) = %d, want %d", len(got), MaxSamples)
	}
	if got[0].Title != "one" || got[0].Summary != "s1" {
		t.Errorf("samples[0] = %+v, want the first item's title/summary only", got[0])
	}
}

// Validate is recommendjob.Validator's real implementation, end to end
// against a fixture server: it must discover the feed, report it healthy, and
// carry the "2 posts reviewed" gate's samples off the same fetch.
func TestValidateFindsAHealthyFeedWithSamples(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Notes</title>
			<link rel="alternate" type="application/atom+xml" href="/atom.xml"></head>
			<body>hi</body></html>`))
	})
	mux.HandleFunc("/atom.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(fmt.Sprintf(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Notes</title>
  <entry><title>Post A</title><id>a</id><link href="https://x.example/a"/><summary>sa</summary>
    <updated>%s</updated></entry>
  <entry><title>Post B</title><id>b</id><link href="https://x.example/b"/><summary>sb</summary>
    <updated>%s</updated></entry>
  <entry><title>Post C</title><id>c</id><link href="https://x.example/c"/><summary>sc</summary>
    <updated>%s</updated></entry>
</feed>`,
			vnow.Format(time.RFC3339), vnow.Add(-7*24*time.Hour).Format(time.RFC3339),
			vnow.Add(-14*24*time.Hour).Format(time.RFC3339))))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	health, feedURL, title := local().Validate(context.Background(), srv.URL)

	if !health.Reachable || !health.HasFeed {
		t.Fatalf("health = %+v, want Reachable and HasFeed", health)
	}
	if title != "Notes" {
		t.Errorf("title = %q, want Notes", title)
	}
	if feedURL == "" {
		t.Error("feedURL is empty")
	}
	if health.PostsPerWeek == 0 {
		t.Error("PostsPerWeek = 0, want a rate computed from 3 dated items")
	}
	if len(health.Samples) != MaxSamples {
		t.Fatalf("len(Samples) = %d, want %d", len(health.Samples), MaxSamples)
	}
	if health.Samples[0].Title != "Post A" {
		t.Errorf("Samples[0].Title = %q, want Post A", health.Samples[0].Title)
	}
}

// A domain that answers but declares and probes to nothing usable is
// reachable with no feed — not a dead site, and not an error either.
func TestValidateReportsReachableWithNoFeedForAPageWithNoLinks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Plain</title></head><body>no feed here</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	health, feedURL, title := local().Validate(context.Background(), srv.URL)
	if !health.Reachable {
		t.Error("Reachable = false, want true — the page answered")
	}
	if health.HasFeed {
		t.Error("HasFeed = true, want false — nothing was declared or probed")
	}
	if feedURL != "" || title != "" {
		t.Errorf("feedURL=%q title=%q, want both empty with no feed found", feedURL, title)
	}
}

// A domain that never answers must not be reported as HasFeed, or the health
// gate would score a dead site as validated.
func TestValidateReportsUnreachableForADeadDomain(t *testing.T) {
	health, _, _ := local().Validate(context.Background(), "http://127.0.0.1:1")
	if health.Reachable {
		t.Error("Reachable = true for a connection that could not be made")
	}
	if health.HasFeed {
		t.Error("HasFeed = true for an unreachable domain")
	}
}
