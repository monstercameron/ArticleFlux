package rundown

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
)

// fixture200 builds a 200-candidate pool spread across every shipped
// category, with six duplicate clusters (four sources each) mixed in among
// otherwise-singleton stories, and a spread of scores, corroboration counts
// and slots so role banding, segment grouping and the minute-budget fill all
// have real work to do.
func fixture200(t *testing.T) []Candidate {
	t.Helper()
	cats := lexicon.Categories()
	if len(cats) == 0 {
		t.Fatal("lexicon.Categories() returned nothing")
	}
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)

	var cands []Candidate
	// Six duplicate clusters, four sources apiece (24 candidates), spread
	// across the first six categories so segment-level dedup is exercised in
	// more than one segment.
	sources := []string{"AP", "Reuters", "BBC", "Local Wire"}
	for c := 0; c < 6; c++ {
		clusterID := fmt.Sprintf("cluster-%d", c)
		slug := cats[c%len(cats)].Slug
		for s, src := range sources {
			cands = append(cands, Candidate{
				ItemID:        fmt.Sprintf("dup-%d-%d", c, s),
				ClusterID:     clusterID,
				Source:        src,
				Title:         fmt.Sprintf("Duplicate story %d from %s", c, src),
				Categories:    []string{slug},
				Genre:         "news",
				WordCount:     600,
				Score:         20 - float64(c) - float64(s)*0.1,
				Slot:          SlotTop,
				Corroboration: 3,
				Published:     base.Add(time.Duration(s) * time.Hour),
			})
		}
	}

	// The remaining 176 are singletons, spread across categories and slots,
	// with a descending score so there is a real percentile spread.
	slots := []string{SlotTop, SlotExplore, SlotClusterHead}
	remaining := 200 - len(cands)
	for i := 0; i < remaining; i++ {
		slug := cats[i%len(cats)].Slug
		slot := slots[i%len(slots)]
		genre := "news"
		if i%11 == 0 {
			genre = genreAnalysis
		}
		corrob := 0
		if i%4 == 0 {
			corrob = 1
		}
		if i%13 == 0 {
			corrob = 2
		}
		cands = append(cands, Candidate{
			ItemID:        fmt.Sprintf("solo-%d", i),
			Source:        fmt.Sprintf("Source %d", i%17),
			Title:         fmt.Sprintf("Story %d", i),
			Categories:    []string{slug},
			Genre:         genre,
			WordCount:     400 + i,
			Score:         15 - float64(i)*0.05,
			Slot:          slot,
			Corroboration: corrob,
			Published:     base.Add(time.Duration(i) * time.Minute),
		})
	}
	return cands
}

func TestRundownIsDeterministic(t *testing.T) {
	cands := fixture200(t)
	opts := Options{Title: "Test", Target: 20 * time.Minute, Rate: 1.0, Style: StyleBalanced, AllowQuickHits: true}

	first := Build(cands, opts)
	second := Build(cands, opts)

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same input built two different rundowns:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestTargetIsHit(t *testing.T) {
	cands := fixture200(t)
	for _, minutes := range []int{5, 10, 20, 40} {
		t.Run(fmt.Sprintf("%dmin", minutes), func(t *testing.T) {
			target := time.Duration(minutes) * time.Minute
			rd := Build(cands, Options{Target: target, Rate: 1.0, AllowQuickHits: true})

			got := rd.Duration(1.0)
			tolerance := 0.10 * float64(target)
			diff := math.Abs(float64(got - target))
			if diff > tolerance {
				t.Fatalf("target %s: built %s, off by %s (tolerance %s)",
					target, got, time.Duration(diff), time.Duration(tolerance))
			}
		})
	}
}

func TestRateChangesTheStoryCount(t *testing.T) {
	cands := fixture200(t)
	target := 20 * time.Minute

	slow := Build(cands, Options{Target: target, Rate: 1.0, AllowQuickHits: true})
	fast := Build(cands, Options{Target: target, Rate: 1.5, AllowQuickHits: true})

	if countStories(fast) <= countStories(slow) {
		t.Fatalf("1.5x rate did not yield more stories in the same %s: got %d at 1.0x, %d at 1.5x",
			target, countStories(slow), countStories(fast))
	}
}

func TestNoTwoStoriesShareACluster(t *testing.T) {
	cands := fixture200(t)
	rd := Build(cands, Options{Target: 60 * time.Minute, Rate: 1.0, AllowQuickHits: true})

	seen := map[string]bool{}
	for _, seg := range rd.Segments {
		for _, st := range seg.Stories {
			if st.ClusterID == "" {
				continue
			}
			if seen[st.ClusterID] {
				t.Fatalf("cluster %q appears more than once in the rundown", st.ClusterID)
			}
			seen[st.ClusterID] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("fixture has six clusters but none of them survived into the rundown")
	}
}

func TestNoAPIKeyStillProducesAPlayableRundown(t *testing.T) {
	// "An instance with no API key produces a complete, playable rundown" —
	// Build never touches a model, so this is really a restatement of purity,
	// checked by asserting the free-tier path (no Smart+ fields exist to
	// even omit) yields real segments and stories from an ordinary pool.
	cands := fixture200(t)
	rd := Build(cands, Options{Target: 20 * time.Minute, Rate: 1.0, AllowQuickHits: true})

	if len(rd.Segments) == 0 {
		t.Fatal("no segments produced")
	}
	total := 0
	for _, seg := range rd.Segments {
		total += len(seg.Stories)
	}
	if total == 0 {
		t.Fatal("no stories produced")
	}
}

func TestShortPoolIsHonestNotPadded(t *testing.T) {
	cands := []Candidate{
		{ItemID: "a", Source: "AP", Categories: []string{"world"}, Genre: "news", Score: 10, Slot: SlotTop, Published: time.Now()},
		{ItemID: "b", Source: "Reuters", Categories: []string{"business"}, Genre: "news", Score: 9, Slot: SlotTop, Published: time.Now()},
		{ItemID: "c", Source: "BBC", Categories: []string{"science"}, Genre: "news", Score: 8, Slot: SlotExplore, Published: time.Now()},
	}
	rd := Build(cands, Options{Target: 60 * time.Minute, Rate: 1.0, AllowQuickHits: true})

	if got := countStories(rd); got != len(cands) {
		t.Fatalf("expected all %d candidates in a short pool, got %d stories", len(cands), got)
	}
	if rd.Duration(1.0) >= 60*time.Minute {
		t.Fatalf("a 3-item pool should fall well short of a 60-minute target, got %s", rd.Duration(1.0))
	}

	// And building with zero candidates must not panic or error, just
	// produce an empty rundown.
	empty := Build(nil, Options{Target: 20 * time.Minute, Rate: 1.0})
	if len(empty.Segments) != 0 {
		t.Fatalf("expected no segments from no candidates, got %d", len(empty.Segments))
	}
}

func countStories(rd Rundown) int {
	n := 0
	for _, seg := range rd.Segments {
		n += len(seg.Stories)
	}
	return n
}

func TestWordsToDurationUnsetRate(t *testing.T) {
	// rate <= 0 must fall back to 1 (normal speed) rather than div-by-zero or
	// a negative duration — see wordsToDuration's own comment.
	for _, rate := range []float64{0, -1, -0.5} {
		got := wordsToDuration(300, rate)
		want := wordsToDuration(300, 1)
		if got != want {
			t.Fatalf("rate %v: got %s, want the rate=1 fallback %s", rate, got, want)
		}
	}
	if d := wordsToDuration(0, 1); d != 0 {
		t.Fatalf("0 words should be 0 duration, got %s", d)
	}
}

func TestWordBudgetUnsetRate(t *testing.T) {
	for _, rate := range []float64{0, -1, -0.5} {
		got := wordBudget(10*time.Minute, rate)
		want := wordBudget(10*time.Minute, 1)
		if got != want {
			t.Fatalf("rate %v: got %v, want the rate=1 fallback %v", rate, got, want)
		}
	}
	if got := wordBudget(10*time.Minute, 2.0); got != 3000 {
		t.Fatalf("10min at rate 2.0: got %v words, want 3000 (10 * 150 * 2)", got)
	}
}

func TestOptionsNormalizedDefaults(t *testing.T) {
	cases := []struct {
		name      string
		in        Options
		wantRate  float64
		wantStyle Style
	}{
		{"zero rate falls back to 1", Options{Rate: 0}, 1, StyleBalanced},
		{"negative rate falls back to 1", Options{Rate: -2}, 1, StyleBalanced},
		{"unrecognised style falls back to balanced", Options{Rate: 1, Style: Style("shouty")}, 1, StyleBalanced},
		{"empty style falls back to balanced", Options{Rate: 1}, 1, StyleBalanced},
		{"valid rate and style pass through unchanged", Options{Rate: 1.5, Style: StyleExplore}, 1.5, StyleExplore},
		{"focused passes through unchanged", Options{Rate: 1, Style: StyleFocused}, 1, StyleFocused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.normalized()
			if got.Rate != tc.wantRate {
				t.Errorf("Rate = %v, want %v", got.Rate, tc.wantRate)
			}
			if got.Style != tc.wantStyle {
				t.Errorf("Style = %v, want %v", got.Style, tc.wantStyle)
			}
		})
	}
}

func TestThemeForUnknownAndEmpty(t *testing.T) {
	cats := lexicon.Categories()
	if len(cats) == 0 {
		t.Fatal("lexicon.Categories() returned nothing")
	}
	valid := cats[0].Slug

	cases := []struct {
		name string
		cand Candidate
		want string
	}{
		{"no categories at all", Candidate{Categories: nil}, ""},
		{"empty categories slice", Candidate{Categories: []string{}}, ""},
		{"unrecognised slug", Candidate{Categories: []string{"not-a-real-slug"}}, ""},
		{"recognised slug", Candidate{Categories: []string{valid}}, valid},
		{"only the first category counts", Candidate{Categories: []string{valid, "not-a-real-slug"}}, valid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := themeFor(tc.cand); got != tc.want {
				t.Errorf("themeFor(%+v) = %q, want %q", tc.cand, got, tc.want)
			}
		})
	}
}

func TestSegmentWeightUnrecognisedTheme(t *testing.T) {
	cats := lexicon.Categories()
	if len(cats) < 2 {
		t.Fatal("need at least two shipped categories to check relative ordering")
	}
	first := segmentWeight(cats[0].Slug)
	last := segmentWeight(cats[len(cats)-1].Slug)
	if first <= last {
		t.Fatalf("first-in-table slug should outweigh the last one: first=%d last=%d", first, last)
	}

	// Unrecognised and empty themes both fall to the floor, below every
	// recognised slug, so a rundown never opens on stories nothing could place.
	for _, theme := range []string{"", "not-a-real-slug"} {
		if got := segmentWeight(theme); got != 0 {
			t.Errorf("segmentWeight(%q) = %d, want 0", theme, got)
		}
		if segmentWeight(theme) >= last {
			t.Errorf("segmentWeight(%q) = %d should be strictly below the lowest recognised slug's %d", theme, segmentWeight(theme), last)
		}
	}
}

func TestBetterRepresentativeTiebreaks(t *testing.T) {
	t0 := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)

	// Equal score: earlier Published wins.
	a := Candidate{ItemID: "a", Score: 5, Published: t0}
	b := Candidate{ItemID: "b", Score: 5, Published: t1}
	if !betterRepresentative(a, b) {
		t.Error("equal score, a published earlier: expected a to beat b")
	}
	if betterRepresentative(b, a) {
		t.Error("equal score, b published later: expected b not to beat a")
	}

	// Equal score and published: lower ItemID wins.
	c := Candidate{ItemID: "aaa", Score: 5, Published: t0}
	d := Candidate{ItemID: "bbb", Score: 5, Published: t0}
	if !betterRepresentative(c, d) {
		t.Error("equal score and published, c has the lower ItemID: expected c to beat d")
	}
	if betterRepresentative(d, c) {
		t.Error("equal score and published, d has the higher ItemID: expected d not to beat c")
	}
}

func TestStyleWeightAllCombinations(t *testing.T) {
	cases := []struct {
		style Style
		slot  string
		want  float64
	}{
		{StyleFocused, SlotExplore, 0.5},
		{StyleFocused, SlotClusterHead, 0.25},
		{StyleFocused, SlotTop, 1.0},
		{StyleExplore, SlotExplore, 1.6},
		{StyleExplore, SlotClusterHead, 1.3},
		{StyleExplore, SlotTop, 1.0},
		{StyleBalanced, SlotTop, 1.0},
		{StyleBalanced, SlotExplore, 1.0},
		{StyleBalanced, SlotClusterHead, 1.0},
	}
	for _, tc := range cases {
		t.Run(string(tc.style)+"/"+tc.slot, func(t *testing.T) {
			if got := styleWeight(tc.style, tc.slot); got != tc.want {
				t.Errorf("styleWeight(%q, %q) = %v, want %v", tc.style, tc.slot, got, tc.want)
			}
		})
	}
}

func TestDedupeClustersPicksBestRepresentativeAndMergesOtherSources(t *testing.T) {
	t0 := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	cands := []Candidate{
		// Lower score, appears FIRST — dedupeClusters must still promote the
		// higher-scoring member that arrives later, not just keep members[0].
		{ItemID: "a1", ClusterID: "c1", Source: "AP", Score: 5, Published: t0},
		{ItemID: "a2", ClusterID: "c1", Source: "Reuters", Score: 9, Published: t0.Add(time.Hour),
			OtherSources: []string{"BBC", "AP"}}, // "AP" duplicates a1.Source and must not double-add
	}
	clusters := dedupeClusters(cands)
	if len(clusters) != 1 {
		t.Fatalf("expected one cluster, got %d", len(clusters))
	}
	got := clusters[0]
	if got.rep.ItemID != "a2" {
		t.Fatalf("expected the higher-scoring later member a2 to be the representative, got %q", got.rep.ItemID)
	}
	want := []string{"Reuters", "AP", "BBC"}
	if !reflect.DeepEqual(got.sources, want) {
		t.Fatalf("sources = %v, want %v (rep first, then dedup across Source and OtherSources)", got.sources, want)
	}
}

func TestBuildDropsQuickHitsWhenDisallowed(t *testing.T) {
	cands := fixture200(t)
	// Large enough that every deduplicated candidate fits the budget
	// regardless of role — otherwise the minute budget, not AllowQuickHits,
	// would be the thing excluding QUICK_HIT stories, and the test would be
	// checking the wrong mechanism.
	target := 300 * time.Minute

	allowed := Build(cands, Options{Target: target, Rate: 1.0, AllowQuickHits: true})
	disallowed := Build(cands, Options{Target: target, Rate: 1.0, AllowQuickHits: false})

	hasQuickHit := func(rd Rundown) bool {
		for _, seg := range rd.Segments {
			for _, st := range seg.Stories {
				if st.Role == RoleQuickHit {
					return true
				}
			}
		}
		return false
	}

	if !hasQuickHit(allowed) {
		t.Fatal("fixture200 should band at least one candidate to QUICK_HIT when allowed — test is vacuous otherwise")
	}
	if hasQuickHit(disallowed) {
		t.Fatal("AllowQuickHits: false should drop every QUICK_HIT-banded candidate entirely, not promote it")
	}
	if countStories(disallowed) >= countStories(allowed) {
		t.Fatalf("disallowing quick hits should shrink the rundown (dropped candidates are never promoted to fill the gap): allowed=%d disallowed=%d",
			countStories(allowed), countStories(disallowed))
	}
}

// TestBuildSegmentsStoryScoreTie exercises buildSegments' explicit ItemID
// tiebreak for equal-score stories in the same segment. Note: the sibling
// theme-order tiebreak (themes[i] < themes[j]) is not exercised anywhere —
// segmentWeight is injective over categoryOrder's index (and "" is always
// the sole zero-weight theme), so two distinct themes can never tie in
// practice; a test forcing that branch would require faking an impossible
// segmentWeight collision.
// fixtureAntiCorrelated reproduces TODO 11.24's real-feed shape: the
// highest-scoring story is a single-source exclusive with NO corroboration,
// and every corroborated story scores lower — the shape that made the old
// "top-decile score AND >=2 corroborating sources" gate almost never open,
// because on real data freshness, topic and entity terms score highly
// without needing a second outlet to confirm them.
func fixtureAntiCorrelated() []Candidate {
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	cands := []Candidate{
		{ItemID: "exclusive", Source: "Local Wire", Categories: []string{"world"},
			Genre: "news", Score: 20, Corroboration: 0, Published: base},
	}
	for i := 0; i < 20; i++ {
		cands = append(cands, Candidate{
			ItemID:        fmt.Sprintf("mid-%d", i),
			Source:        fmt.Sprintf("Source %d", i),
			Categories:    []string{"world"},
			Genre:         "news",
			Score:         10 - float64(i)*0.1,
			Corroboration: 2,
			Published:     base.Add(time.Duration(i) * time.Minute),
		})
	}
	return cands
}

func leadIDs(rd Rundown) []string {
	var ids []string
	for _, seg := range rd.Segments {
		for _, st := range seg.Stories {
			if st.Role == RoleLead {
				ids = append(ids, st.ItemID)
			}
		}
	}
	return ids
}

// TestTopScoringStoryIsAlwaysLead is TODO 11.24's fix: the top-scoring story
// is the LEAD whether or not it has any corroboration, because gating it on
// corroboration is exactly what produced a real twenty-minute rundown with
// zero LEADs in it.
func TestTopScoringStoryIsAlwaysLead(t *testing.T) {
	cands := fixtureAntiCorrelated()
	rd := Build(cands, Options{Target: 20 * time.Minute, Rate: 1.0, AllowQuickHits: true})

	leads := leadIDs(rd)
	if len(leads) == 0 {
		t.Fatal("no LEAD in a 20-minute rundown built from a non-trivial pool")
	}
	found := false
	for _, id := range leads {
		if id == "exclusive" {
			found = true
		}
	}
	if !found {
		t.Errorf("the single highest-scoring, uncorroborated story was not LEAD; leads = %v", leads)
	}
}

// TestShortShowNeverPromotesASecondLead: promoting a second story to LEAD on
// corroboration is only for a show long enough to carry two (TODO 11.24) —
// below leadPromoteMinutes there must be exactly one, the top story itself.
func TestShortShowNeverPromotesASecondLead(t *testing.T) {
	cands := fixtureAntiCorrelated()
	rd := Build(cands, Options{Target: 10 * time.Minute, Rate: 1.0, AllowQuickHits: true})

	if got := len(leadIDs(rd)); got != 1 {
		t.Errorf("a 10-minute show has %d LEADs, want exactly 1: %v", got, leadIDs(rd))
	}
}

// TestLongShowPromotesAWellCorroboratedSecondLead: the OTHER half of 11.24 —
// corroboration ADDS a lead in a long show rather than gating the one the
// score already earned.
func TestLongShowPromotesAWellCorroboratedSecondLead(t *testing.T) {
	cands := fixtureAntiCorrelated()
	rd := Build(cands, Options{Target: 40 * time.Minute, Rate: 1.0, AllowQuickHits: true})

	leads := leadIDs(rd)
	if len(leads) < 2 {
		t.Fatalf("a 40-minute show over a well-corroborated pool has %d LEADs, want at least 2: %v", len(leads), leads)
	}
}

// TestNoCategoryExceeds40PercentOfTheShow is TODO 11.25 / 11.18(a): the
// first real rundown against a live feed put 46% of a twenty-minute show in
// one category (hardware, 13 of 28 stories back to back). A cap now demotes
// or drops the overflow instead of appending it.
func TestNoCategoryExceeds40PercentOfTheShow(t *testing.T) {
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	var cands []Candidate
	for i := 0; i < 15; i++ {
		cands = append(cands, Candidate{
			ItemID: fmt.Sprintf("hw-%d", i), Source: fmt.Sprintf("HW Source %d", i),
			Categories: []string{"hardware"}, Genre: "news",
			Score: 20 - float64(i)*0.1, Published: base.Add(time.Duration(i) * time.Minute),
		})
	}
	// A deep enough alternative pool that the budget actually fills close to
	// its target — otherwise a thin pool undershoots the budget and the
	// share-of-BUDGET cap (which is what fillToBudget enforces, since the
	// eventual total is not known in advance) would not match a
	// share-of-ACTUAL-total assertion for reasons that have nothing to do
	// with the cap itself.
	cats := lexicon.Categories()
	added := 0
	for _, c := range cats {
		if c.Slug == "hardware" || added >= 10 {
			continue
		}
		for j := 0; j < 4; j++ {
			cands = append(cands, Candidate{
				ItemID: fmt.Sprintf("other-%s-%d", c.Slug, j), Source: fmt.Sprintf("Wire %s %d", c.Slug, j),
				Categories: []string{c.Slug}, Genre: "news", Score: 8 - float64(j)*0.1,
				Published: base,
			})
		}
		added++
	}

	rd := Build(cands, Options{Target: 20 * time.Minute, Rate: 1.0, AllowQuickHits: true})
	total := rd.Words()
	if total == 0 {
		t.Fatal("empty rundown — test is vacuous")
	}
	for _, seg := range rd.Segments {
		segWords := 0
		for _, st := range seg.Stories {
			segWords += st.Words
		}
		if share := float64(segWords) / float64(total); share > 0.40+1e-9 {
			t.Errorf("segment %q is %.1f%% of the show, want <= 40%%", seg.Theme, share*100)
		}
	}
}

// TestUnsortedSegmentIsAlwaysLast is TODO 11.26: unclassified stories are a
// real, honest segment (themeFor's own comment), but they are an "and also"
// and never an opener, whatever their score.
func TestUnsortedSegmentIsAlwaysLast(t *testing.T) {
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	cands := []Candidate{
		// Highest score of the whole pool, and no recognised category.
		{ItemID: "unsorted-1", Source: "A", Categories: nil, Genre: "news", Score: 50, Published: base},
		{ItemID: "world-1", Source: "B", Categories: []string{"world"}, Genre: "news", Score: 5, Published: base},
	}
	rd := Build(cands, Options{Target: 20 * time.Minute, Rate: 1.0, AllowQuickHits: true})
	if len(rd.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(rd.Segments))
	}
	if rd.Segments[len(rd.Segments)-1].Theme != "" {
		t.Errorf("last segment theme = %q, want \"\" (unsorted last regardless of score)", rd.Segments[len(rd.Segments)-1].Theme)
	}
}

// TestSegmentsOrderedByBestStoryScore is TODO 11.27: the first real rundown
// opened on `software` (one middling story) ahead of `ai` (a $250B financing
// story) purely because lexicon.Categories() lists software first. Segment
// order must follow the strongest story in each segment, not table position.
func TestSegmentsOrderedByBestStoryScore(t *testing.T) {
	cats := lexicon.Categories()
	// Find two slugs where the taxonomy's own table order is the OPPOSITE of
	// what this test wants score order to produce, so a test that silently
	// fell back to table order would fail rather than pass by coincidence.
	var lowTableHighScore, highTableLowScore string
	for i, c := range cats {
		if i == 0 {
			highTableLowScore = c.Slug
		}
		if i == len(cats)-1 {
			lowTableHighScore = c.Slug
		}
	}
	base := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	cands := []Candidate{
		{ItemID: "big", Source: "A", Categories: []string{lowTableHighScore}, Genre: "news", Score: 50, Published: base},
		{ItemID: "small", Source: "B", Categories: []string{highTableLowScore}, Genre: "news", Score: 5, Published: base},
	}
	rd := Build(cands, Options{Target: 20 * time.Minute, Rate: 1.0, AllowQuickHits: true})
	if len(rd.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(rd.Segments))
	}
	if rd.Segments[0].Theme != lowTableHighScore {
		t.Errorf("first segment = %q, want %q (the segment with the higher-scoring story), got order %v",
			rd.Segments[0].Theme, lowTableHighScore, []string{rd.Segments[0].Theme, rd.Segments[1].Theme})
	}
}

func TestBuildSegmentsStoryScoreTie(t *testing.T) {
	cats := lexicon.Categories()
	if len(cats) == 0 {
		t.Fatal("lexicon.Categories() returned nothing")
	}
	slug := cats[0].Slug

	mk := func(id string) banded {
		return banded{
			cluster: cluster{rep: Candidate{ItemID: id, Score: 5, Categories: []string{slug}}},
			role:    RoleStandard,
		}
	}
	// Input order (zzz before aaa) is the reverse of the expected tiebreak
	// order, so a stable sort that just preserved input order would pass this
	// test wrongly — only the explicit ItemID tiebreak makes it pass for real.
	segs := buildSegments([]banded{mk("zzz"), mk("aaa")})

	if len(segs) != 1 {
		t.Fatalf("expected one segment, got %d", len(segs))
	}
	if len(segs[0].Stories) != 2 {
		t.Fatalf("expected two stories, got %d", len(segs[0].Stories))
	}
	if segs[0].Stories[0].ItemID != "aaa" || segs[0].Stories[1].ItemID != "zzz" {
		t.Fatalf("equal-score stories should tiebreak by ascending ItemID, got order [%s, %s]",
			segs[0].Stories[0].ItemID, segs[0].Stories[1].ItemID)
	}
}
