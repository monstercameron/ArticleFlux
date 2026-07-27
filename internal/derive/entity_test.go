package derive

import (
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The gap this closes: the interest layer could name a SUBJECT and not a THING.
//
// Term affinity is a bag of words and topics are clusters of them, so "cameras" was
// expressible and "the Lumix line" was not — on a real database `lumix`, `powershot` and
// `vivo` sat in the vocabulary as unconnected unigrams.
func TestEntitiesNameRecurringProductsAndBrands(t *testing.T) {
	f := setup(t)

	// A reader who keeps opening articles about one product line. The phrase has to
	// recur across separate ITEMS to survive EntityMinMentions, which is what separates
	// a brand from a one-off capitalisation.
	ids := f.ingestEntityItems(t, map[string]string{
		"e1": "Android Auto gains a new split screen layout",
		"e2": "Android Auto rolls out to more cars this month",
		"e3": "Android Auto adds offline maps",
		// Mentioned once. Must not survive: a single capitalised pair is exactly the
		// false positive textvec.Phrases documents and defers to aggregation to filter.
		"e4": "Released Pixel review of the year",
	})
	f.record(t, signals.Completed, ids, now.Add(-time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	got, err := f.repo.EntityAffinity(f.ctx, f.scope, 100)
	if err != nil {
		t.Fatalf("EntityAffinity: %v", err)
	}
	byName := map[string]store.Entity{}
	for _, e := range got {
		byName[e.Name] = e
	}

	e, ok := byName["android auto"]
	if !ok {
		t.Fatalf("a product named in three engaged articles was not recorded; got %v", names(got))
	}
	if e.Mentions != 3 {
		t.Errorf("mentions = %d, want 3", e.Mentions)
	}
	// The casing has to be recovered from the source text: title-casing by rule gives
	// "Iphone" and "Tcl", which is wrong in a way a reader notices at once.
	if e.Label != "Android Auto" {
		t.Errorf("label = %q, want %q", e.Label, "Android Auto")
	}
	if e.Kind != store.EntityPhrase {
		t.Errorf("kind = %q, want %q", e.Kind, store.EntityPhrase)
	}
	if _, ok := byName["released pixel"]; ok {
		t.Error("a phrase mentioned in one article was recorded as an entity")
	}
}

// Weight is engagement, not frequency. A brand named in articles the reader never
// finished must not outrank one in articles they read to the end.
func TestEntityWeightFollowsEngagementNotMentionCount(t *testing.T) {
	f := setup(t)

	loved := f.ingestEntityItems(t, map[string]string{
		"l1": "Framework Laptop teardown and repair notes",
		"l2": "Framework Laptop mainboard upgrade path",
	})
	ignored := f.ingestEntityItems(t, map[string]string{
		"i1": "Generic Gadget roundup one",
		"i2": "Generic Gadget roundup two",
		"i3": "Generic Gadget roundup three",
		"i4": "Generic Gadget roundup four",
	})

	// Read to the end, versus merely opened. Both clear EntityMinMentions, so the only
	// thing separating them is how much the reader actually engaged.
	f.record(t, signals.Completed, loved, now.Add(-time.Hour))
	f.record(t, signals.Liked, loved, now.Add(-time.Hour))
	f.record(t, signals.Opened, ignored, now.Add(-time.Hour))

	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}
	got, err := f.repo.EntityAffinity(f.ctx, f.scope, 100)
	if err != nil {
		t.Fatalf("EntityAffinity: %v", err)
	}

	var framework, generic float64
	for _, e := range got {
		switch e.Name {
		case "framework laptop":
			framework = e.Weight
		case "generic gadget":
			generic = e.Weight
		}
	}
	if framework == 0 || generic == 0 {
		t.Fatalf("expected both entities; got %v", names(got))
	}
	if framework <= generic {
		t.Errorf("two well-read mentions (%v) did not outweigh four skimmed ones (%v) — "+
			"the weight is counting frequency rather than engagement", framework, generic)
	}
}

// A suppression is an instruction, so it has to survive the next derivation.
//
// Without this the control is a gesture: the reader says "not something I follow", the
// poller runs, and it is back fifteen minutes later. §18.2 — a model you can correct is
// one you will trust.
func TestSuppressedEntitiesStaySuppressedAcrossADerivation(t *testing.T) {
	f := setup(t)
	ids := f.ingestEntityItems(t, map[string]string{
		"s1": "Android Auto gains a new layout",
		"s2": "Android Auto rolls out widely",
	})
	f.record(t, signals.Completed, ids, now.Add(-time.Hour))
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}
	if err := f.repo.SteerEntity(f.ctx, f.scope, "android auto", store.SteerNormal, true); err != nil {
		t.Fatalf("SteerEntity: %v", err)
	}

	// Re-derive over the same evidence, which would otherwise put it straight back.
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting again: %v", err)
	}
	for _, e := range mustEntities(t, f) {
		if e.Name == "android auto" {
			t.Fatal("a suppressed entity came back after the next derivation")
		}
	}
}

func mustEntities(t *testing.T, f *fixture) []store.Entity {
	t.Helper()
	got, err := f.repo.EntityAffinity(f.ctx, f.scope, 100)
	if err != nil {
		t.Fatalf("EntityAffinity: %v", err)
	}
	return got
}

func names(es []store.Entity) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

// ingestEntityItems adds items to the fixture's first feed and returns their ids, so a
// test can control exactly which text the phrase extractor sees.
func (f *fixture) ingestEntityItems(t *testing.T, texts map[string]string) []string {
	t.Helper()
	var sourceID string
	for _, id := range f.feeds {
		if sourceID == "" || id < sourceID {
			sourceID = id
		}
	}
	// Sorted, so the ingest order — and therefore any tie-break downstream — is stable.
	guids := make([]string, 0, len(texts))
	for guid := range texts {
		guids = append(guids, guid)
	}
	sortStrings(guids)

	var ingest []store.IngestItem
	for i, guid := range guids {
		ingest = append(ingest, store.IngestItem{
			GUID:        guid,
			URL:         "https://entity.example/" + guid,
			Title:       texts[guid],
			Summary:     texts[guid],
			ContentHTML: "<p>" + texts[guid] + "</p>",
			PublishedAt: now.Add(-time.Duration(i+1) * time.Hour),
			WordCount:   600,
		})
	}
	if _, err := f.repo.IngestItems(f.ctx, sourceID, ingest); err != nil {
		t.Fatalf("IngestItems: %v", err)
	}

	// Paginated: store.MaxLimit caps a single ListItems page at 200, well below
	// what a truncation-boundary test needs (see
	// TestEntityTruncationIsStableAcrossRederivation), so one page is not
	// enough once a caller ingests more than that many items at once.
	want := map[string]bool{}
	for _, guid := range guids {
		want[texts[guid]] = true
	}
	var ids []string
	cursor := ""
	for {
		items, next, err := f.repo.ListItems(f.ctx, f.scope, store.ListQuery{
			SourceID: sourceID, Limit: 200, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("ListItems: %v", err)
		}
		for _, it := range items {
			if want[it.Title] {
				ids = append(ids, it.ID)
			}
		}
		if next == "" || len(items) == 0 {
			break
		}
		cursor = next
	}
	if len(ids) != len(texts) {
		t.Fatalf("ingested %d items but found %d back", len(texts), len(ids))
	}
	return ids
}
