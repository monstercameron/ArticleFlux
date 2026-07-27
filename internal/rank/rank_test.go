package rank

import (
	"math"
	"sort"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func hoursAgo(h float64) time.Time {
	return now.Add(-time.Duration(h * float64(time.Hour)))
}

// The score IS the sum of the reasons. §18.9 shows the reasons verbatim, so a
// reason list that has drifted from the arithmetic is a lie that looks like
// transparency — worse than showing nothing, because it invites trust.
func TestScoreIsTheSumOfItsReasons(t *testing.T) {
	cases := []struct {
		name string
		item Item
		sig  Signals
	}{
		{"everything at once", Item{
			PublishedAt: hoursAgo(3), TopicScore: 0.8, TargetDomain: "arxiv.org",
			Corroboration: 3, SimilarToRecent: 0.2, ExternalSignal: 400, ManualWeight: 1.5,
		}, Signals{
			FeedAffinity: 0.7, DomainAffinity: 0.6, NegativeAffinity: 0.1,
			VolumePerDay: 30, ExternalMax: 800, Mode: ModeFull,
		}},
		{"nothing at all", Item{PublishedAt: hoursAgo(500)}, Signals{Mode: ModeFull}},
		{"highlights mode", Item{
			PublishedAt: hoursAgo(2), TopicScore: 0.9, TargetDomain: "danluu.com", Corroboration: 2,
		}, Signals{DomainAffinity: 0.8, VolumePerDay: 94, Mode: ModeHighlights}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Score(c.item, c.sig, DefaultWeights(), now)
			var sum float64
			for _, r := range res.Reasons {
				sum += r.Delta
			}
			if math.Abs(sum-res.Score) > 1e-9 {
				t.Errorf("score %v but reasons sum to %v", res.Score, sum)
			}
			for _, r := range res.Reasons {
				if r.Text == "" || r.Term == "" {
					t.Errorf("a reason has no text: %+v", r)
				}
			}
		})
	}
}

// The golden fixture. Values pinned so a change to any weight, curve or term
// shows up as a diff someone has to look at rather than as a homepage that
// quietly reorders itself.
//
// The breakdown of each, recorded so a failing diff is readable without
// re-deriving it:
//
//	fresh item        2.6510 = fresh +1.111, topic +0.900, feed +0.640
//	three days old    1.6150 = topic +0.900, feed +0.640, fresh +0.075
//	off topic         1.2010 = fresh +1.111, topic +0.050, feed +0.040
//	corroborated x4   2.8944 = fresh +1.029, corroboration +0.966, topic +0.500, feed +0.400
//	suppressed topic  0.6947 = fresh +1.155, negative -1.080, feed +0.320, topic +0.300
//
// Two of these are the interesting ones. At 72 hours freshness has fallen to
// 0.075 — four half-lives — so the item survives on topic and feed alone, which
// is the intended behaviour for an essay and the wrong one for news, and is
// exactly why the half-life is per source. And in the suppressed case the
// negative term very nearly cancels a strong freshness bonus, which is what
// "not an interest" has to mean if it is to mean anything.
func TestGoldenFixture(t *testing.T) {
	golden := []struct {
		name string
		item Item
		sig  Signals
		want float64
	}{
		{
			name: "fresh item from a feed you read, on topic",
			item: Item{PublishedAt: hoursAgo(2), TopicScore: 0.9, ManualWeight: 1},
			sig:  Signals{FeedAffinity: 0.8, VolumePerDay: 2, Mode: ModeFull},
			want: 2.6510,
		},
		{
			name: "same item, three days old",
			item: Item{PublishedAt: hoursAgo(72), TopicScore: 0.9, ManualWeight: 1},
			sig:  Signals{FeedAffinity: 0.8, VolumePerDay: 2, Mode: ModeFull},
			want: 1.6150,
		},
		{
			name: "off topic, from a feed you ignore",
			item: Item{PublishedAt: hoursAgo(2), TopicScore: 0.05, ManualWeight: 1},
			sig:  Signals{FeedAffinity: 0.05, VolumePerDay: 2, Mode: ModeFull},
			want: 1.2010,
		},
		{
			name: "corroborated by four other feeds",
			item: Item{PublishedAt: hoursAgo(4), TopicScore: 0.5, Corroboration: 4, ManualWeight: 1},
			sig:  Signals{FeedAffinity: 0.5, VolumePerDay: 5, Mode: ModeFull},
			want: 2.8944,
		},
		{
			name: "in a suppressed topic",
			item: Item{PublishedAt: hoursAgo(1), TopicScore: 0.3, ManualWeight: 1},
			sig:  Signals{FeedAffinity: 0.4, NegativeAffinity: 0.9, VolumePerDay: 3, Mode: ModeFull},
			want: 0.6947,
		},
	}

	for _, g := range golden {
		t.Run(g.name, func(t *testing.T) {
			got := Score(g.item, g.sig, DefaultWeights(), now).Score
			if math.Abs(got-g.want) > 0.0002 {
				t.Errorf("score = %.4f, golden %.4f\n"+
					"If this change is intended, update the golden value and say why in the commit.",
					got, g.want)
			}
		})
	}
}

// §18.4's headline claim, tested as the ticket asks: a firehose must not be able
// to dominate the homepage.
//
// The setup is deliberately lopsided and deliberately favourable to the
// firehose: it publishes 94 items a day (Hacker News' real rate) and the
// essayist publishes one a week. If volume were unpenalised, the top of the page
// would be entirely firehose simply because it has 94 chances a day to be there.
func TestAFirehoseCannotDominate(t *testing.T) {
	weights := DefaultWeights()

	type scored struct {
		source string
		score  float64
	}
	var all []scored

	// The firehose: 94 items today, decent but unremarkable topic match.
	for i := 0; i < 94; i++ {
		res := Score(Item{
			SourceID:     "firehose",
			PublishedAt:  hoursAgo(float64(i) * 0.25),
			TopicScore:   0.55,
			ManualWeight: 1,
		}, Signals{
			FeedAffinity: 0.5, VolumePerDay: 94, Mode: ModeFull,
		}, weights, now)
		all = append(all, scored{"firehose", res.Score})
	}

	// The essayist: one item this week, strong topic match.
	res := Score(Item{
		SourceID:     "essayist",
		PublishedAt:  hoursAgo(20),
		TopicScore:   0.85,
		ManualWeight: 1,
	}, Signals{
		FeedAffinity: 0.8, VolumePerDay: 0.14, Mode: ModeFull,
	}, weights, now)
	all = append(all, scored{"essayist", res.Score})

	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })

	// The essayist must make the first screen. Ten is the homepage's Top slot on
	// a desktop breakpoint, so "did not make the top ten" means "invisible".
	pos := -1
	for i, s := range all[:10] {
		if s.source == "essayist" {
			pos = i
			break
		}
	}
	if pos == -1 {
		t.Errorf("the weekly essayist did not reach the top ten against a 94-a-day feed; "+
			"top score %.3f, essayist %.3f — VolumePenalty is not doing its job",
			all[0].score, res.Score)
	}

	// And the inverse failure: the penalty must not be so harsh that a
	// high-volume feed is effectively muted. The reader subscribed on purpose.
	firehoseInTop := 0
	for _, s := range all[:10] {
		if s.source == "firehose" {
			firehoseInTop++
		}
	}
	if firehoseInTop == 0 {
		t.Error("no firehose item reached the top ten at all — the penalty has become a mute")
	}
}

// §18.5: in highlights mode the feed term is dropped, because nobody likes
// Hacker News, they like specific things on Hacker News.
func TestHighlightsModeDropsTheFeedTerm(t *testing.T) {
	item := Item{PublishedAt: hoursAgo(2), TopicScore: 0.3, ManualWeight: 1}
	sig := Signals{FeedAffinity: 0.9, VolumePerDay: 94}

	sig.Mode = ModeFull
	full := Score(item, sig, DefaultWeights(), now)
	sig.Mode = ModeHighlights
	high := Score(item, sig, DefaultWeights(), now)

	for _, r := range high.Reasons {
		if r.Term == "feed" {
			t.Error("highlights mode still credited feed affinity")
		}
	}
	// A weak-topic item from a feed you love should score LOWER in highlights
	// mode: that is the whole point — the feed no longer carries it.
	if high.Score >= full.Score {
		t.Errorf("highlights %.3f >= full %.3f; the feed weight was not actually removed",
			high.Score, full.Score)
	}
}

// The other half of §18.5: the weight moves rather than disappearing, so an item
// that is strong on its own merits does better in highlights mode than the plain
// scorer would give it.
func TestHighlightsModeRedistributesToItemSignals(t *testing.T) {
	item := Item{
		PublishedAt: hoursAgo(2), TopicScore: 0.9,
		TargetDomain: "danluu.com", Corroboration: 3, ManualWeight: 1,
	}
	sig := Signals{FeedAffinity: 0.0, DomainAffinity: 0.9, VolumePerDay: 94}

	sig.Mode = ModeFull
	full := Score(item, sig, DefaultWeights(), now)
	sig.Mode = ModeHighlights
	high := Score(item, sig, DefaultWeights(), now)

	if high.Score <= full.Score {
		t.Errorf("highlights %.3f <= full %.3f; a strong item on a firehose should gain "+
			"the redistributed weight", high.Score, full.Score)
	}
}

func TestMutedIsIneligible(t *testing.T) {
	res := Score(Item{PublishedAt: now, TopicScore: 1}, Signals{Mode: ModeMuted}, DefaultWeights(), now)
	if res.Eligible {
		t.Error("a muted subscription produced an eligible item")
	}
	if res.Ineligible == "" {
		t.Error("no explanation, so the UI can only show a blank")
	}
	if res.Score != 0 {
		t.Errorf("a muted item scored %v; it should not be scored at all", res.Score)
	}
}

func TestDecayIsPerSource(t *testing.T) {
	item := Item{PublishedAt: hoursAgo(24), TopicScore: 0.5, ManualWeight: 1}

	news := Score(item, Signals{HalfLife: 3 * time.Hour, Mode: ModeFull}, DefaultWeights(), now)
	essay := Score(item, Signals{HalfLife: 96 * time.Hour, Mode: ModeFull}, DefaultWeights(), now)

	if news.Score >= essay.Score {
		t.Errorf("a day-old item scored %.3f as news and %.3f as an essay; "+
			"the per-source half-life is not being used", news.Score, essay.Score)
	}
}

// A publisher lying about pubDate must not be rewarded. ClampPublished should
// stop this upstream, but a scorer that hands out a bonus for a date nobody
// verified is a scorer that can be gamed by anyone who can write a feed.
func TestFutureDatesGetNoBonus(t *testing.T) {
	future := Score(Item{PublishedAt: now.Add(48 * time.Hour), ManualWeight: 1},
		Signals{Mode: ModeFull}, DefaultWeights(), now)
	present := Score(Item{PublishedAt: now, ManualWeight: 1},
		Signals{Mode: ModeFull}, DefaultWeights(), now)

	if future.Score > present.Score {
		t.Errorf("an item dated two days in the future scored %.3f against %.3f for now",
			future.Score, present.Score)
	}
}

func TestVolumePenaltyShape(t *testing.T) {
	cases := []struct {
		perDay float64
		want   string
	}{
		{0, "none"}, {1, "none"}, {5, "none"},
		{6, "some"}, {20, "some"}, {94, "some"},
	}
	for _, c := range cases {
		got := volumePenalty(c.perDay)
		if c.want == "none" && got != 0 {
			t.Errorf("%v/day was penalised %v; a daily blog is not the problem this term exists for",
				c.perDay, got)
		}
		if c.want == "some" && got <= 0 {
			t.Errorf("%v/day was not penalised at all", c.perDay)
		}
	}

	// Flattening at the top: the difference between 50 and 100 a day should be
	// small, because both are already firehoses. Linear growth here overshoots
	// into effectively muting feeds the reader chose.
	lowGap := volumePenalty(20) - volumePenalty(10)
	highGap := volumePenalty(100) - volumePenalty(90)
	if highGap >= lowGap {
		t.Errorf("the penalty is not flattening: 10→20 adds %.4f but 90→100 adds %.4f",
			lowGap, highGap)
	}
	if volumePenalty(1000) > 1.5 {
		t.Errorf("an absurd volume produced an unbounded penalty: %v", volumePenalty(1000))
	}
}

// §18.5: the setting is a RATE, not a score. Nobody can reason about
// "score > 0.62"; everybody can reason about three a week.
func TestHighlightsCutoffSolvesForARate(t *testing.T) {
	// A hundred scores spread evenly over 0..1.
	scores := make([]float64, 100)
	for i := range scores {
		scores[i] = float64(i) / 100
	}

	// 94 a day is 658 a week. Asking for ~7 a week is roughly the top 1%.
	cutoff, ok := HighlightsCutoff(scores, 94, 7)
	if !ok {
		t.Fatal("a 100-sample fit was refused")
	}
	kept := 0
	for _, s := range scores {
		if s >= cutoff {
			kept++
		}
	}
	// Out of a 100-item sample, keeping ~1% means one or two.
	if kept < 1 || kept > 4 {
		t.Errorf("cutoff %.3f keeps %d of 100; expected roughly 1", cutoff, kept)
	}

	// Asking for more than the feed publishes means no cutoff at all.
	if c, ok := HighlightsCutoff(scores, 1, 100); !ok || !math.IsInf(c, -1) {
		t.Errorf("asking for more than the feed publishes gave cutoff %v, ok=%v", c, ok)
	}

	// Too small a sample must refuse to fit rather than invent a threshold.
	if _, ok := HighlightsCutoff(scores[:5], 94, 3); ok {
		t.Error("a 5-sample fit was accepted; that is a threshold invented from nothing")
	}
}

// An unfitted cutoff must show everything, not hide everything. Getting this
// backwards means a newly-subscribed firehose appears completely empty, which
// reads as a broken feed rather than as a system still learning.
func TestUnfittedHighlightsShowsEverything(t *testing.T) {
	res := Result{Score: 0.1, Eligible: true}
	got := ApplyHighlights(res, math.Inf(-1), false)
	if !got.Eligible {
		t.Error("an unfitted highlights cutoff hid the item")
	}

	below := ApplyHighlights(Result{Score: 0.1, Eligible: true}, 0.5, true)
	if below.Eligible {
		t.Error("an item below a fitted cutoff was still eligible")
	}
	if below.Ineligible == "" {
		t.Error("no explanation for the exclusion")
	}
}

// Reasons are ordered by magnitude, so the UI can show the top one or two and
// have shown the ones that actually decided the placement.
func TestReasonsAreOrderedByInfluence(t *testing.T) {
	res := Score(Item{
		PublishedAt: hoursAgo(1), TopicScore: 0.95, Corroboration: 1,
		TargetDomain: "x.tld", ManualWeight: 1,
	}, Signals{
		FeedAffinity: 0.2, DomainAffinity: 0.1, VolumePerDay: 1, Mode: ModeFull,
	}, DefaultWeights(), now)

	for i := 1; i < len(res.Reasons); i++ {
		if math.Abs(res.Reasons[i-1].Delta) < math.Abs(res.Reasons[i].Delta) {
			t.Errorf("reasons out of order: %+v", res.Reasons)
			break
		}
	}
}

func TestScoringIsPure(t *testing.T) {
	item := Item{PublishedAt: hoursAgo(5), TopicScore: 0.6, Corroboration: 2, ManualWeight: 1}
	sig := Signals{FeedAffinity: 0.5, VolumePerDay: 12, Mode: ModeFull}

	a := Score(item, sig, DefaultWeights(), now)
	b := Score(item, sig, DefaultWeights(), now)
	if a.Score != b.Score || len(a.Reasons) != len(b.Reasons) {
		t.Error("two scorings of identical input disagree")
	}
}
