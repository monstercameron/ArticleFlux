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
