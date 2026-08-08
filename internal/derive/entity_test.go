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

// Q5 (2026-07-31 QA pass): a headline mentioning "Wall Street Journal" matched
// BOTH "Wall Street" and "Street Journal" when a reader followed both as
// separate entities — deriveEntities has no step that merges one phrase's
// overlapping sub-phrases into it — and entityText then read "about Street
// Journal and Wall Street, which you follow": two attributions for one
// mention, neither of them the actual publication name. namedIn now resolves
// overlapping matches to the longest one, which is the more specific phrase.
func TestNamedInResolvesOverlappingEntitiesToTheLongestMatch(t *testing.T) {
	followed := []store.Entity{
		{Name: "wall street", Label: "Wall Street", Steer: store.SteerNormal},
		{Name: "street journal", Label: "Street Journal", Steer: store.SteerNormal},
	}
	names, _ := namedIn("The Wall Street Journal reports a slowdown", followed)
	if len(names) != 1 {
		t.Fatalf("names = %v, want exactly one entity for one overlapping mention", names)
	}
	if names[0] != "Street Journal" {
		t.Errorf("names[0] = %q, want the LONGER overlapping match (%q ends at the same "+
			"point \"wall street\" starts short of)", names[0], "Street Journal")
	}

	// Two entities that do NOT overlap must both still surface — the fix is
	// about overlap, not about capping the count at one.
	followed = append(followed, store.Entity{Name: "android auto", Label: "Android Auto", Steer: store.SteerNormal})
	names, _ = namedIn("Android Auto and the Wall Street Journal both had news today", followed)
	if len(names) != 2 {
		t.Fatalf("names = %v, want two entities: one non-overlapping pair", names)
	}
}

// A followed name must be a WORD in the headline, not a run of letters inside one.
//
// # Why the deterministic extractor hid this
//
// namedIn matched with strings.Index, a raw substring test with no notion of a
// word edge. pipeline's entityAnalyzer never exposed it: textvec.Phrases builds
// entities from token PAIRS with MinTermLen = 3, so its shortest name is a
// two-word phrase, and a string containing a space cannot sit inside a single
// word.
//
// The Smart+ path has no such shape. internal/smart's interest extractor
// normalises case and rejects only the empty string — no length floor, no token
// floor — and store.EntityLLM is a first-class kind beside EntityPhrase. So a
// model returning "arm", "meta" or "ai", which is exactly what a model asked
// for entities returns, lands a name short enough to hide inside ordinary
// words.
//
// # Why this is worse than a scoring wobble
//
// The entity term is weighted 0.9, behind only freshness and topic, and
// rank.go stakes it on being checkable: "'mentions Android Auto' is something
// the reader can agree or disagree with by looking at the row, which is what
// §18.9 means by explainability being the product." A reader following ARM,
// told a piece about a house fire "mentions Arm, which you follow", checks the
// row and finds the claim false. That is the one failure this term's design
// says it exists to avoid.
func TestAFollowedNameMustBeAWordNotASubstring(t *testing.T) {
	ent := func(name, label string) []store.Entity {
		return []store.Entity{{Name: name, Label: label, Steer: store.SteerNormal}}
	}

	// Names a model would plausibly return, inside words a headline would
	// plausibly contain.
	for _, c := range []struct{ name, label, title string }{
		{"arm", "Arm", "Alarm raised as the army warns of harm"},
		{"meta", "Meta", "The metal price and the metadata behind it"},
		{"ai", "AI", "He said the campaign was detailed"},
		{"arc", "Arc", "Researchers search the archive in March"},
		{"eff", "EFF", "A different approach to the effort"},
		{"ars", "Ars Technica", "Mars rover parses new stars data"},
	} {
		if got, _ := namedIn(c.title, ent(c.name, c.label)); len(got) != 0 {
			t.Errorf("%q in %q matched %v — the reader is told the row mentions "+
				"something they follow, and the row plainly does not", c.name, c.title, got)
		}
	}

	// The other half, and the one that makes this a boundary fix rather than a
	// length floor: a short name that really is a word must still match. A
	// reader following ARM wants the ARM stories.
	for _, c := range []struct{ name, label, title string }{
		{"arm", "Arm", "Arm licences a new core"},
		{"arm", "Arm", "The new arm-based laptops ship"}, // hyphen is an edge
		{"arm", "Arm", "Analysts price Arm's IPO"},       // apostrophe is an edge
		{"meta", "Meta", "Meta reports quarterly earnings"},
		{"ai", "AI", "The AI rules take effect"},
		{"ars", "Ars Technica", "Ars Technica reviews the handset"},
	} {
		got, _ := namedIn(c.title, ent(c.name, c.label))
		if len(got) != 1 || got[0] != c.label {
			t.Errorf("%q in %q gave %v, want [%s] — a real mention was dropped",
				c.name, c.title, got, c.label)
		}
	}

	// A false hit must not consume the entity: the real mention later in the
	// same headline still has to be found. This is why the scan cannot stop at
	// the first substring occurrence.
	if got, _ := namedIn("In March, Arc shipped a browser", ent("arc", "Arc")); len(got) != 1 {
		t.Errorf("got %v, want [Arc] — the match inside \"March\" came first and "+
			"must not hide the real mention after it", got)
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
