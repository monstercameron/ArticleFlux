package topics

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

// Three genuinely distinct subjects, which is §18.2's own example: a reader of
// SQLite internals, NPU inference and the Go runtime has a single centroid
// sitting in empty space between the three.
var subjects = map[string][]string{
	"sqlite": {
		"sqlite btree page cache write ahead log",
		"sqlite wal checkpoint page cache fsync",
		"sqlite btree index page vacuum fsync log",
		"sqlite virtual table fts5 tokenizer index",
		"sqlite wal mode page cache btree checkpoint",
	},
	"npu": {
		"npu quantization inference hexagon tensor kernel",
		"npu hexagon inference tensor quantization latency",
		"inference quantization tensor npu kernel throughput",
		"npu tensor kernel hexagon quantization latency",
	},
	"goruntime": {
		"goroutine scheduler preemption garbage collector runtime",
		"goroutine runtime scheduler garbage collector mark",
		"garbage collector goroutine runtime preemption sweep",
		"scheduler goroutine preemption runtime mark sweep",
	},
}

func corpusDocs(t *testing.T, ageOf func(subject string, i int) time.Duration) []Doc {
	t.Helper()
	corpus := textvec.NewCorpus()
	var texts []struct {
		subject, text string
		i             int
	}
	for subject, list := range subjects {
		for i, text := range list {
			corpus.Add(text)
			texts = append(texts, struct {
				subject, text string
				i             int
			}{subject, text, i})
		}
	}

	var docs []Doc
	for _, e := range texts {
		docs = append(docs, Doc{
			ItemID:    fmt.Sprintf("%s-%d", e.subject, e.i),
			Vector:    corpus.TFIDF(e.text),
			EngagedAt: now.Add(-ageOf(e.subject, e.i)),
		})
	}
	return docs
}

func recent(string, int) time.Duration { return 48 * time.Hour }

// §18.2's core claim, tested directly: three subjects must come out as three
// topics rather than one blur.
func TestDistinctSubjectsBecomeDistinctTopics(t *testing.T) {
	res := Build(corpusDocs(t, recent), Options{Now: now, MinMembers: 3})

	if len(res.Topics) < 2 {
		t.Fatalf("got %d topics from three distinct subjects: %+v", len(res.Topics), labels(res.Topics))
	}

	// No topic may mix subjects. A cluster containing both an sqlite and a
	// goruntime item is the "average of your interests" failure §18.2 rejects.
	for _, top := range res.Topics {
		seen := map[string]bool{}
		for _, id := range top.Members {
			seen[strings.SplitN(id, "-", 2)[0]] = true
		}
		if len(seen) > 1 {
			t.Errorf("topic %q mixes subjects %v — members %v", top.Label, keys(seen), top.Members)
		}
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	docs := corpusDocs(t, recent)
	first := Build(docs, Options{Now: now})

	// Same documents, different order. Nothing about the result may depend on
	// how rows came back from the database.
	shuffled := make([]Doc, len(docs))
	for i := range docs {
		shuffled[i] = docs[len(docs)-1-i]
	}
	second := Build(shuffled, Options{Now: now})

	if strings.Join(labels(first.Topics), "|") != strings.Join(labels(second.Topics), "|") {
		t.Errorf("input order changed the topics:\n  %v\n  %v",
			labels(first.Topics), labels(second.Topics))
	}
}

// A label that changes on recomputation renames the reader's interests behind
// their back every night.
func TestLabelsAreStable(t *testing.T) {
	docs := corpusDocs(t, recent)
	a := Build(docs, Options{Now: now})
	b := Build(docs, Options{Now: now.Add(72 * time.Hour)})

	if strings.Join(labels(a.Topics), "|") != strings.Join(labels(b.Topics), "|") {
		t.Errorf("labels moved with the clock:\n  %v\n  %v", labels(a.Topics), labels(b.Topics))
	}

	if got := Label([]string{"sqlite", "btree", "wal"}, 3); got != "Sqlite · Btree · Wal" {
		t.Errorf("Label = %q", got)
	}
	if got := Label(nil, 3); got != "Unnamed" {
		t.Errorf("an empty term list produced %q", got)
	}
	if got := Label([]string{"one"}, 5); got != "One" {
		t.Errorf("asking for more terms than exist produced %q", got)
	}
}

// Below the cold-start floor the app must say it is still learning rather than
// present three topics built from twelve articles as though they meant something.
func TestColdStartIsReported(t *testing.T) {
	if !Build(corpusDocs(t, recent), Options{Now: now}).ColdStart {
		t.Error("13 documents did not report cold start")
	}

	corpus := textvec.NewCorpus()
	var docs []Doc
	for i := 0; i < ColdStartItems+5; i++ {
		text := fmt.Sprintf("sqlite btree page cache log entry %d", i%7)
		corpus.Add(text)
		docs = append(docs, Doc{ItemID: fmt.Sprintf("i-%03d", i), EngagedAt: now})
	}
	for i := range docs {
		docs[i].Vector = corpus.TFIDF(fmt.Sprintf("sqlite btree page cache log entry %d", i%7))
	}
	if Build(docs, Options{Now: now}).ColdStart {
		t.Errorf("%d documents still reported cold start", len(docs))
	}
}

// Two items sharing a rare word are a coincidence, not a topic. A Trends screen
// full of one-item topics is noise that hides the real ones.
func TestSmallClustersAreNotTopics(t *testing.T) {
	corpus := textvec.NewCorpus()
	texts := []string{
		"sqlite btree page cache wal",
		"sqlite btree page cache wal checkpoint",
		"sqlite btree page cache wal fsync",
		"entirely unrelated subject about gardening tomatoes",
	}
	for _, s := range texts {
		corpus.Add(s)
	}
	var docs []Doc
	for i, s := range texts {
		docs = append(docs, Doc{ItemID: fmt.Sprintf("d%d", i), Vector: corpus.TFIDF(s), EngagedAt: now})
	}

	res := Build(docs, Options{Now: now, MinMembers: 3})
	for _, top := range res.Topics {
		if len(top.Members) < 3 {
			t.Errorf("topic %q has %d members, below the floor", top.Label, len(top.Members))
		}
	}
	// The gardening item joins nothing and must be reported rather than forced
	// into a topic — a model that clusters everything invents interests.
	found := false
	for _, id := range res.Unclustered {
		if id == "d3" {
			found = true
		}
	}
	if !found {
		t.Errorf("the unrelated item was not reported as unclustered: %v", res.Unclustered)
	}
}

func TestTrends(t *testing.T) {
	cases := []struct {
		name string
		ages []time.Duration
		want Trend
	}{
		{"all recent", []time.Duration{1, 2, 3}, TrendRising},
		{"all old but in window", []time.Duration{20 * 24, 21 * 24, 22 * 24}, TrendFading},
		{"outside the window entirely", []time.Duration{60 * 24, 70 * 24, 80 * 24}, TrendDormant},
		{"evenly spread", []time.Duration{2 * 24, 3 * 24, 20 * 24, 21 * 24}, TrendSteady},
		{"mostly recent", []time.Duration{1, 2, 3, 4, 20 * 24}, TrendRising},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			corpus := textvec.NewCorpus()
			base := "sqlite btree page cache wal checkpoint fsync"
			for range c.ages {
				corpus.Add(base)
			}
			var docs []Doc
			for i, age := range c.ages {
				docs = append(docs, Doc{
					ItemID:    fmt.Sprintf("d%02d", i),
					Vector:    corpus.TFIDF(base),
					EngagedAt: now.Add(-age * time.Hour),
				})
			}
			res := Build(docs, Options{Now: now, MinMembers: 1})
			if len(res.Topics) == 0 {
				t.Fatal("no topic")
			}
			if res.Topics[0].Trend != c.want {
				t.Errorf("trend = %s, want %s", res.Topics[0].Trend, c.want)
			}
		})
	}
}

// §18.2: an item scores against its NEAREST topic, never the average. This is
// what lets explainability say "matches your NPU inference reading".
func TestNearestPicksTheRightTopic(t *testing.T) {
	docs := corpusDocs(t, recent)
	res := Build(docs, Options{Now: now, MinMembers: 3})
	if len(res.Topics) < 2 {
		t.Skip("not enough topics to distinguish")
	}

	corpus := textvec.NewCorpus()
	for _, list := range subjects {
		for _, text := range list {
			corpus.Add(text)
		}
	}
	probe := corpus.TFIDF("sqlite wal checkpoint btree page cache")

	idx, score := Nearest(probe, res.Topics)
	if idx < 0 {
		t.Fatal("no nearest topic")
	}
	if score <= 0 {
		t.Errorf("similarity %v", score)
	}
	if !strings.Contains(strings.ToLower(res.Topics[idx].Label), "sqlite") &&
		!strings.Contains(strings.ToLower(strings.Join(res.Topics[idx].TopTerms, " ")), "sqlite") {
		t.Errorf("an sqlite probe matched %q (terms %v)", res.Topics[idx].Label, res.Topics[idx].TopTerms)
	}

	if _, s := Nearest(probe, nil); s != 0 {
		t.Error("Nearest over no topics returned a score")
	}
}

// Shares must sum to one, or "70% of your reading is one topic" is a made-up
// number.
func TestSharesSumToOne(t *testing.T) {
	res := Build(corpusDocs(t, recent), Options{Now: now, MinMembers: 3})
	var sum float64
	for _, top := range res.Topics {
		sum += top.Share
	}
	if len(res.Topics) > 0 && (sum < 0.999 || sum > 1.001) {
		t.Errorf("shares sum to %v", sum)
	}
	if c := Concentration(res.Topics); len(res.Topics) > 0 && (c <= 0 || c > 1) {
		t.Errorf("concentration = %v", c)
	}
}

// Explore serves starved topics deliberately rather than sampling at random,
// because pure affinity converges to a monoculture and fails invisibly.
func TestStarvedFindsTheCrowdedOut(t *testing.T) {
	ts := []Topic{
		{Label: "Big", Share: 0.70, Trend: TrendSteady},
		{Label: "Middle", Share: 0.22, Trend: TrendSteady},
		{Label: "Small", Share: 0.05, Trend: TrendRising},
		{Label: "Tiny", Share: 0.03, Trend: TrendSteady},
		{Label: "Over", Share: 0.00, Trend: TrendDormant},
	}
	got := Starved(ts, 0.5)

	var names []string
	for _, top := range got {
		names = append(names, top.Label)
	}
	// An even split across five is 0.20; half of that is 0.10.
	if len(got) != 2 || names[0] != "Tiny" || names[1] != "Small" {
		t.Errorf("starved = %v, want the two smallest live topics, most starved first", names)
	}
	// A dormant topic is over, not starved. Serving it is showing someone last
	// year's interests and calling it discovery.
	for _, top := range got {
		if top.Trend == TrendDormant {
			t.Error("a dormant topic was offered as starved")
		}
	}
}

func TestEmptyInput(t *testing.T) {
	res := Build(nil, Options{Now: now})
	if len(res.Topics) != 0 || !res.ColdStart {
		t.Errorf("empty input gave %+v", res)
	}
	if Concentration(nil) != 0 {
		t.Error("concentration of no topics is not zero")
	}
	if Starved(nil, 0.5) != nil {
		t.Error("starved of no topics is not nil")
	}
}

func labels(ts []Topic) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Label)
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
