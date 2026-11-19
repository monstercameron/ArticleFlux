package rank

import (
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// These pin RELATIONS rather than numbers: the coefficients are tunable (the
// tuning panel §18.9 owes is over ModelWeights), and a test that pinned 1.37
// would be rewritten on every tune. What must survive any tuning is the ordering
// each case states.

var t0 = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func ev(kind signals.Kind, at time.Time, value float64, ctx string) signals.Event {
	return signals.Event{ID: fmt.Sprint(kind, at.UnixNano(), value), Kind: kind, At: at.UnixMilli(), Value: value, Context: ctx}
}

func read(at time.Time) []signals.Event {
	return []signals.Event{
		ev(signals.Dwell, at, 400_000, ""),
		ev(signals.Completed, at, 0, ""),
		ev(signals.ScrollDepth, at, 1, ""),
	}
}

func bounce(at time.Time) []signals.Event {
	return []signals.Event{ev(signals.Dwell, at, 5_000, ""), ev(signals.Bounced, at, 5_000, "")}
}

func sumOf(r Result) float64 {
	s := 0.0
	for _, why := range r.Reasons {
		s += why.Delta
	}
	return s
}

// ---- Evidence ----------------------------------------------------------------

func TestDwellIsJudgedAgainstExpectedReadingTime(t *testing.T) {
	cases := []struct {
		name   string
		words  int
		ms     int64
		wantLE float64 // Implicit must be <= this
		wantGE float64 // and >= this
	}{
		{"10s on a 2,000-word essay is a bounce", 2000, 10_000, -0.01, -1},
		{"10s on a 40-word link post is a skim, not a rejection", 40, 10_000, 1, 0.01},
		{"a glance draws no conclusion either way", 2000, 1_500, 0, 0},
		{"unknown length never concludes rejection", 0, 10_000, 1, 0},
		{"a full-length read is strongly positive", 400, 400_000, 1, 0.5},
	}
	for _, c := range cases {
		e := Assess(Observation{Words: c.words, Events: []signals.Event{ev(signals.Dwell, t0, float64(c.ms), "")}})
		if e.Implicit > c.wantLE || e.Implicit < c.wantGE {
			t.Errorf("%s: Implicit = %.3f, want in [%.2f, %.2f]", c.name, e.Implicit, c.wantGE, c.wantLE)
		}
	}
}

// The client emits a `bounced` alongside the `dwell` it classified. The old
// derivation summed both priors and scored one bounce as -2.0.
func TestABounceIsCountedOnce(t *testing.T) {
	dwellOnly := Assess(Observation{Words: 2000, Events: []signals.Event{ev(signals.Dwell, t0, 10_000, "")}})
	both := Assess(Observation{Words: 2000, Events: bounce(t0)})
	both2 := Assess(Observation{Words: 2000, Events: []signals.Event{ev(signals.Dwell, t0, 10_000, ""), ev(signals.Bounced, t0, 10_000, "")}})
	if dwellOnly.Implicit != both2.Implicit {
		t.Errorf("dwell %.3f vs dwell+bounced %.3f: the bounce was counted twice", dwellOnly.Implicit, both2.Implicit)
	}
	if both.Implicit >= 0 {
		t.Errorf("a bounce is not negative: %.3f", both.Implicit)
	}
	// And the client's classification is not trusted over the server's word count.
	short := Assess(Observation{Words: 40, Events: []signals.Event{ev(signals.Bounced, t0, 10_000, "")}})
	if short.Implicit < 0 {
		t.Errorf("a client 'bounce' of 10s on a 40-word item stayed negative: %.3f", short.Implicit)
	}
}

func TestAFlungCompletionIsNotARead(t *testing.T) {
	flung := Assess(Observation{Words: 2000, Events: []signals.Event{
		ev(signals.Dwell, t0, 6_000, ""), ev(signals.Completed, t0, 0, ""), ev(signals.ScrollDepth, t0, 1, ""),
	}})
	if flung.Implicit >= 0 {
		t.Errorf("reaching the end of a 2,000-word article in 6s scored %.3f, want negative", flung.Implicit)
	}
}

func TestBulkReadMovesNothing(t *testing.T) {
	base := []signals.Event{ev(signals.Impression, t0, 1, ""), ev(signals.Dwell, t0, 60_000, "")}
	with := append(append([]signals.Event{}, base...),
		ev(signals.BulkRead, t0.Add(time.Hour), 143, ""), ev(signals.SyncRead, t0.Add(2*time.Hour), 0, ""))
	a := Assess(Observation{Words: 400, Events: base})
	b := Assess(Observation{Words: 400, Events: with})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("mark-all-read changed the evidence:\n%+v\n%+v", a, b)
	}
	// Not even as exposure: bulk reads alone never make a pass-over.
	var bulk []signals.Event
	for i := 0; i < 10; i++ {
		bulk = append(bulk, ev(signals.BulkRead, t0.Add(time.Duration(i)*24*time.Hour), 1, ""))
	}
	if e := Assess(Observation{Events: bulk}); e.PassedOver || e.Acted || e.Implicit != 0 || !e.At.IsZero() {
		t.Errorf("bulk reads produced evidence: %+v", e)
	}
}

// The log records that a like was GIVEN; clearing it records nothing. The state
// column is the authority, so a retracted like is no like at all.
func TestARetractedLikeIsNoLike(t *testing.T) {
	evs := []signals.Event{ev(signals.Liked, t0, 0, "")}
	if e := Assess(Observation{Events: evs, HasVerdictState: true, Verdict: 0}); e.Verdict != 0 {
		t.Errorf("a cleared like still counts: verdict %d", e.Verdict)
	}
	if e := Assess(Observation{Events: evs, HasVerdictState: false}); e.Verdict != 1 {
		t.Errorf("without state the event should stand in: verdict %d", e.Verdict)
	}
	flip := []signals.Event{ev(signals.Liked, t0, 0, ""), ev(signals.Disliked, t0.Add(time.Minute), 0, "")}
	if e := Assess(Observation{Events: flip}); e.Verdict != -1 {
		t.Errorf("the later verdict should win: %d", e.Verdict)
	}
}

func TestAChoiceFurtherDownTheListCountsForMore(t *testing.T) {
	top := Assess(Observation{Events: []signals.Event{ev(signals.Chose, t0, 0, `{"pos":0,"of":12}`)}})
	low := Assess(Observation{Events: []signals.Event{ev(signals.Chose, t0, 0, `{"pos":11,"of":12}`)}})
	if !(low.Implicit > top.Implicit && top.Implicit > 0) {
		t.Errorf("position debiasing: top %.3f, bottom %.3f", top.Implicit, low.Implicit)
	}
}

func TestRepeatedExposureAcrossSittingsIsAWeakNegative(t *testing.T) {
	var imps []signals.Event
	for i := 0; i < 3; i++ {
		imps = append(imps, ev(signals.Impression, t0.Add(time.Duration(i)*2*time.Hour), 1, ""))
	}
	e := Assess(Observation{Events: imps})
	if !e.PassedOver || e.Label() >= 0 {
		t.Errorf("three sittings of being ignored: %+v label %.3f", e, e.Label())
	}
	// And it is far weaker than an informed rejection.
	b := Assess(Observation{Words: 2000, Events: bounce(t0)})
	if !(b.Label() < e.Label()) {
		t.Errorf("a pass-over (%.3f) is not weaker than a bounce (%.3f)", e.Label(), b.Label())
	}
	sameSitting := Assess(Observation{Events: []signals.Event{
		ev(signals.Impression, t0, 1, ""), ev(signals.Impression, t0.Add(time.Minute), 1, ""), ev(signals.Impression, t0.Add(2*time.Minute), 1, ""),
	}})
	if sameSitting.PassedOver {
		t.Error("one long scroll was read as repeated rejection")
	}
}

func TestExplicitVerdictsAreAsymmetric(t *testing.T) {
	like := Evidence{Verdict: 1}
	dislike := Evidence{Verdict: -1}
	if !(math.Abs(dislike.Label()) > like.Label()) {
		t.Errorf("a dislike (%.2f) must outweigh a like (%.2f)", dislike.Label(), like.Label())
	}
	readOnly := Assess(Observation{Words: 400, Events: read(t0)})
	if !(like.Label() >= readOnly.Implicit) {
		t.Errorf("a like (%.2f) is weaker than a read (%.2f)", like.Label(), readOnly.Implicit)
	}
}

// ---- Affinity ----------------------------------------------------------------

func TestThreeClicksDoNotDefineAReader(t *testing.T) {
	hl, k := DefaultHalfLives(), DefaultPriors().Topic
	readEv := Assess(Observation{Words: 400, Events: read(t0)})
	var one, ten Affinity
	one.Observe(readEv, 1, t0, hl)
	for i := 0; i < 10; i++ {
		ten.Observe(readEv, 1, t0, hl)
	}
	if p := one.Preference(k); p <= 0 || p > 0.3 {
		t.Errorf("one read gave preference %.3f, want small and positive", p)
	}
	if !(ten.Preference(k) > one.Preference(k) && ten.Preference(k) < 1) {
		t.Errorf("ten reads %.3f vs one %.3f", ten.Preference(k), one.Preference(k))
	}
	var none Affinity
	if none.Preference(k) != 0 || none.Confidence(k) != 0 {
		t.Error("no evidence must be exactly the prior: zero")
	}
}

func TestAffinityDecays(t *testing.T) {
	hl, k := DefaultHalfLives(), DefaultPriors().Topic
	e := Assess(Observation{Words: 400, Events: read(t0)})
	var fresh, old Affinity
	for i := 0; i < 5; i++ {
		fresh.Observe(e, 1, t0, hl)
		old.Observe(e, 1, t0.Add(180*24*time.Hour), hl)
	}
	if !(old.Preference(k) < fresh.Preference(k)/2) {
		t.Errorf("six months on, preference %.3f vs %.3f fresh", old.Preference(k), fresh.Preference(k))
	}
	// Negative reading fades faster than a stated dislike.
	b := Assess(Observation{Words: 2000, Events: bounce(t0)})
	d := Evidence{Verdict: -1, At: t0}
	later := t0.Add(90 * 24 * time.Hour)
	var bounces, dislikes Affinity
	bounces.Observe(b, 1, later, hl)
	dislikes.Observe(d, 1, later, hl)
	bNow, dNow := Affinity{}, Affinity{}
	bNow.Observe(b, 1, t0, hl)
	dNow.Observe(d, 1, t0, hl)
	if !(bounces.Neg/bNow.Neg < dislikes.Disliked/dNow.Disliked) {
		t.Errorf("a bounce kept %.2f of its weight over 90 days and a dislike %.2f; the dislike must last longer",
			bounces.Neg/bNow.Neg, dislikes.Disliked/dNow.Disliked)
	}
}

func TestEarlyNoiseIsRecoverable(t *testing.T) {
	hl, k := DefaultHalfLives(), DefaultPriors().Topic
	now := t0.Add(120 * 24 * time.Hour)
	var a Affinity
	// Month one: five enthusiastic reads.
	for i := 0; i < 5; i++ {
		a.Observe(Evidence{Implicit: 0.8, At: t0.Add(time.Duration(i) * 24 * time.Hour)}, 1, now, hl)
	}
	// The last month: eight bounces.
	for i := 0; i < 8; i++ {
		a.Observe(Evidence{Implicit: -0.7, At: now.Add(-time.Duration(i) * 24 * time.Hour)}, 1, now, hl)
	}
	if a.Preference(k) >= 0 {
		t.Errorf("recent rejection did not overturn an old enthusiasm: %.3f", a.Preference(k))
	}
}

// ---- Scorer ------------------------------------------------------------------

func profileWith(topic, source map[string][]Evidence) Profile {
	var obs []Attributed
	for key, es := range topic {
		for _, e := range es {
			obs = append(obs, Attributed{Evidence: e, TopicKey: key, TopicSim: TopicMatchFull})
		}
	}
	for key, es := range source {
		for _, e := range es {
			e.SourceID = key
			obs = append(obs, Attributed{Evidence: e})
		}
	}
	return BuildProfile(obs, t0, DefaultHalfLives(), DefaultPriors())
}

func repeat(e Evidence, n int) []Evidence {
	out := make([]Evidence, n)
	for i := range out {
		out[i] = e
	}
	return out
}

func cand(id, src, topic string) Candidate {
	c := Candidate{ID: id, SourceID: src, PublishedAt: t0.Add(-3 * time.Hour), ManualWeight: 1}
	if topic != "" {
		c.TopicKey, c.TopicLabel, c.TopicSim = topic, topic, 0.4
	}
	return c
}

func TestScoringRelations(t *testing.T) {
	liked := Evidence{Implicit: 0.8, Verdict: 1, At: t0}
	readOK := Evidence{Implicit: 0.7, At: t0}
	disliked := Evidence{Implicit: -0.2, Verdict: -1, At: t0}
	p := profileWith(
		map[string][]Evidence{
			"sqlite": repeat(liked, 4),
			"racing": repeat(readOK, 1),
			"crypto": repeat(disliked, 4),
		},
		map[string][]Evidence{
			"good":  repeat(readOK, 6),
			"meh":   nil,
			"hated": repeat(disliked, 5),
		})
	w := DefaultModelWeights()
	score := func(c Candidate) Result { return ScoreCandidate(c, p, w, t0) }

	cases := []struct {
		name          string
		better, worse Candidate
	}{
		{"a liked topic outranks an unmatched item", cand("a", "meh", "sqlite"), cand("b", "meh", "")},
		{"a liked topic outranks a barely-known one", cand("a", "meh", "sqlite"), cand("b", "meh", "racing")},
		{"a disliked topic ranks below a barely-known one", cand("a", "meh", "racing"), cand("b", "meh", "crypto")},
		{"a source read closely outranks an unknown one", cand("a", "good", "racing"), cand("b", "meh", "racing")},
		{"a disliked source ranks below an unknown one", cand("a", "meh", "racing"), cand("b", "hated", "racing")},
		{"content beats freshness: yesterday's liked topic over this minute's barely-known one",
			func() Candidate { c := cand("a", "meh", "sqlite"); c.PublishedAt = t0.Add(-30 * time.Hour); return c }(),
			func() Candidate { c := cand("b", "meh", "racing"); c.PublishedAt = t0; return c }()},
	}
	for _, c := range cases {
		b, wr := score(c.better), score(c.worse)
		if !(b.Score > wr.Score) {
			t.Errorf("%s: %.3f (%v) not above %.3f (%v)", c.name, b.Score, b.Reasons, wr.Score, wr.Reasons)
		}
	}

	// A disliked source is suppressed, not erased: still eligible, still on the
	// page when it has content, and its reason says why it is low.
	h := score(cand("h", "hated", "sqlite"))
	if !h.Eligible || !HasContent(h) {
		t.Errorf("a disliked source's item about a liked topic was erased: %+v", h)
	}
	if !hasAnyTerm(h, "avoided") {
		t.Errorf("the demotion is unexplained: %v", h.Reasons)
	}
	if !p.Blocked(cand("h", "hated", "")) {
		t.Error("a repeatedly disliked source is still offered as exploration")
	}

	// Every result adds up.
	for _, c := range []Candidate{cand("a", "good", "sqlite"), cand("b", "hated", "crypto"), cand("c", "meh", "")} {
		r := score(c)
		if math.Abs(r.Score-sumOf(r)) > 1e-9 {
			t.Errorf("%s: score %.6f != sum of reasons %.6f", c.ID, r.Score, sumOf(r))
		}
	}
}

func TestSuppressedTopicIsDemotedNotRemoved(t *testing.T) {
	p := profileWith(map[string][]Evidence{"sqlite": repeat(Evidence{Implicit: 0.8, At: t0}, 4)}, nil)
	open := cand("a", "s", "sqlite")
	sup := open
	sup.TopicSuppressed = true
	ro, rs := ScoreCandidate(open, p, DefaultModelWeights(), t0), ScoreCandidate(sup, p, DefaultModelWeights(), t0)
	if !(rs.Score < ro.Score) || !rs.Eligible || !hasAnyTerm(rs, "negative") {
		t.Errorf("suppression: open %.3f, suppressed %.3f %v", ro.Score, rs.Score, rs.Reasons)
	}
	if !p.Blocked(sup) {
		t.Error("a suppressed topic is explorable")
	}
}

func TestALovedFirehoseIsNotPenalisedLikeAnIgnoredOne(t *testing.T) {
	p := profileWith(nil, map[string][]Evidence{"loved": repeat(Evidence{Implicit: 0.9, At: t0}, 12)})
	a, b := cand("a", "loved", "x"), cand("b", "ignored", "x")
	a.VolumePerDay, b.VolumePerDay = 60, 60
	ra, rb := ScoreCandidate(a, p, DefaultModelWeights(), t0), ScoreCandidate(b, p, DefaultModelWeights(), t0)
	if !(termDelta(ra, "volume") > termDelta(rb, "volume")) {
		t.Errorf("volume penalty: loved %.3f, ignored %.3f", termDelta(ra, "volume"), termDelta(rb, "volume"))
	}
}

func TestScoreCandidateIsPure(t *testing.T) {
	p := profileWith(map[string][]Evidence{"sqlite": repeat(Evidence{Implicit: 0.8, Verdict: 1, At: t0}, 3)}, nil)
	c := cand("a", "s", "sqlite")
	c.Entities = []EntityMatch{{Key: "wal", Label: "WAL"}}
	first := ScoreCandidate(c, p, DefaultModelWeights(), t0)
	for i := 0; i < 20; i++ {
		if got := ScoreCandidate(c, p, DefaultModelWeights(), t0); !reflect.DeepEqual(got, first) {
			t.Fatal("same input, different output")
		}
	}
}

// ---- Page --------------------------------------------------------------------

func entry(id, src, topic string, score float64, content bool, novelty float64) Entry {
	return Entry{ID: id, SourceID: src, TopicKey: topic, Content: content, Novelty: novelty,
		Result: Result{Score: score, Eligible: true, Reasons: []Reason{{Term: "topic", Text: "t", Delta: score}}}}
}

func TestTheExplorationBudgetIsRespected(t *testing.T) {
	var es []Entry
	for i := 0; i < 80; i++ {
		es = append(es, entry(fmt.Sprintf("c%03d", i), fmt.Sprintf("s%d", i%16), "", 3-float64(i)/100, true, 0))
	}
	for i := 0; i < 80; i++ {
		e := entry(fmt.Sprintf("x%03d", i), fmt.Sprintf("n%d", i%16), "", 0.5, false, 1)
		e.Result.Reasons = []Reason{{Term: "fresh", Text: "f", Delta: 0.5}}
		es = append(es, e)
	}
	o := DefaultPageOptions(100)
	page := Assemble(es, o)
	if len(page) != 100 {
		t.Fatalf("page of %d", len(page))
	}
	explored := 0
	for i, p := range page {
		if p.Slot == SlotExplore {
			explored++
			if (i+1)%o.ExploreEvery != 0 {
				t.Errorf("exploration at position %d, off its cadence", i)
			}
			if !hasAnyTerm(p.Result, "explore") {
				t.Errorf("exploration row %s does not say it is one", p.ID)
			}
		}
		if p.Rank != i+1 {
			t.Errorf("rank %d at %d", p.Rank, i)
		}
	}
	if want := o.ExploreCap(100, 1); explored != want {
		t.Errorf("explored %d rows, budget %d", explored, want)
	}
}

func TestBlockedItemsAreNeverExplored(t *testing.T) {
	var es []Entry
	for i := 0; i < 20; i++ {
		e := entry(fmt.Sprintf("b%02d", i), "hated", "", 1, false, 1)
		e.Blocked = true
		es = append(es, e)
	}
	for _, p := range Assemble(es, DefaultPageOptions(50)) {
		t.Errorf("blocked %s reached the page as %s", p.ID, p.Slot)
	}
}

func TestOneProlificFeedCannotFillThePage(t *testing.T) {
	var es []Entry
	for i := 0; i < 60; i++ {
		es = append(es, entry(fmt.Sprintf("fire%02d", i), "firehose", "", 5-float64(i)/100, true, 0))
	}
	for i := 0; i < 40; i++ {
		es = append(es, entry(fmt.Sprintf("other%02d", i), fmt.Sprintf("s%d", i), "", 1-float64(i)/100, true, 0))
	}
	o := DefaultPageOptions(60)
	page := Assemble(es, o)
	// In any window of 20 rows while alternatives remain, at most FeedCap.
	for start := 0; start+o.Window <= 40; start++ {
		n := 0
		for _, p := range page[start : start+o.Window] {
			if p.SourceID == "firehose" {
				n++
			}
		}
		if n > o.FeedCap {
			t.Fatalf("rows %d..%d hold %d firehose items, cap %d", start, start+o.Window, n, o.FeedCap)
		}
	}
	// Fatigue is explained and keeps the arithmetic true.
	for _, p := range page {
		if math.Abs(p.Result.Score-sumOf(p.Result)) > 1e-9 {
			t.Fatalf("%s: score %.4f != reasons %.4f after assembly", p.ID, p.Result.Score, sumOf(p.Result))
		}
	}
}

func TestColdStartExploresWithinBudget(t *testing.T) {
	var es []Entry
	for i := 0; i < 100; i++ {
		e := entry(fmt.Sprintf("u%03d", i), fmt.Sprintf("s%d", i%10), "", 0.5, false, 1)
		e.Result.Reasons = []Reason{{Term: "fresh", Text: "f", Delta: 0.5}}
		es = append(es, e)
	}
	page := Assemble(es, DefaultPageOptions(100))
	if len(page) != DefaultPageOptions(100).ExploreCap(100, 1) {
		t.Errorf("cold start page of %d, want exactly the exploration budget", len(page))
	}
	// Round-robin across sources falls out of the caps.
	seen := map[string]int{}
	for _, p := range page[:10] {
		seen[p.SourceID]++
	}
	if len(seen) < 5 {
		t.Errorf("the first ten cold-start rows came from %d sources", len(seen))
	}
}

func TestAssembleIsDeterministic(t *testing.T) {
	var es []Entry
	for i := 0; i < 50; i++ {
		es = append(es, entry(fmt.Sprintf("i%02d", i), fmt.Sprintf("s%d", i%7), fmt.Sprintf("t%d", i%3), 1, i%2 == 0, float64(i%4)/4))
	}
	a := Assemble(es, DefaultPageOptions(40))
	for i := 0; i < 5; i++ {
		if b := Assemble(es, DefaultPageOptions(40)); !reflect.DeepEqual(a, b) {
			t.Fatal("same entries, different page")
		}
	}
}

func hasAnyTerm(r Result, term string) bool {
	for _, why := range r.Reasons {
		if why.Term == term {
			return true
		}
	}
	return false
}

func termDelta(r Result, term string) float64 {
	for _, why := range r.Reasons {
		if why.Term == term {
			return why.Delta
		}
	}
	return 0
}

// Every item-level kind the registry gives a prior is read by Assess, with the
// registry's sign. The old derivation's hand-written switch silently dropped
// `reread`, `chose` and `clicked_out`; this makes a dropped or inverted kind a
// test failure instead of a quietly worse ranking.
func TestAssessReadsEveryKindWithTheRegistrysSign(t *testing.T) {
	for _, k := range signals.Kinds() {
		spec, _ := signals.Lookup(k)
		if !spec.Affinity || !spec.NeedsItem || spec.Prior == 0 {
			continue
		}
		value := 1.0
		if k == signals.Bounced {
			value = 5_000
		}
		e := Assess(Observation{Words: 2000, HasVerdictState: false,
			Events: []signals.Event{ev(k, t0, value, `{"pos":3,"of":10}`)}})
		got := e.Label()
		if (spec.Prior > 0) != (got > 0) || got == 0 {
			t.Errorf("%s: prior %v, assessed label %v", k, spec.Prior, got)
		}
	}
}
