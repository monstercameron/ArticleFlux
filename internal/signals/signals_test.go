package signals

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := Event{ID: "e1", ItemID: "i1", Kind: Opened, Surface: SurfaceList, At: 1000}

	cases := []struct {
		name string
		mut  func(*Event)
		want error
	}{
		{"a well-formed event", func(*Event) {}, nil},
		{"no id", func(e *Event) { e.ID = "" }, ErrNoID},
		{"no timestamp", func(e *Event) { e.At = 0 }, ErrNoTime},
		{"invented kind", func(e *Event) { e.Kind = "vibes" }, ErrUnknownKind},
		{"invented surface", func(e *Event) { e.Surface = "elsewhere" }, ErrUnknownSurface},
		{"item-scoped kind with no item", func(e *Event) { e.ItemID = "" }, ErrItemRequired},
		{"ratio above one", func(e *Event) { e.Kind, e.Value = ScrollDepth, 1.5 }, ErrBadValue},
		{"ratio at one is fine", func(e *Event) { e.Kind, e.Value = ScrollDepth, 1 }, nil},
		{"negative duration", func(e *Event) { e.Kind, e.Value = Dwell, -5 }, ErrBadValue},
		{"context that is not an object", func(e *Event) { e.Context = `"just a string"` }, ErrBadContext},
		{"context object", func(e *Event) { e.Context = `{"pos":3,"of":12}` }, nil},
		{"oversized context", func(e *Event) { e.Context = "{" + string(make([]byte, MaxContextBytes)) + "}" }, ErrBadContext},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := ok
			c.mut(&e)
			if got := Validate(e); got != c.want {
				t.Fatalf("Validate() = %v, want %v", got, c.want)
			}
		})
	}
}

// A search is about a term, not an article, and must not be forced to name one.
func TestSearchNeedsNoItem(t *testing.T) {
	e := Event{ID: "e1", Kind: Searched, Surface: SurfaceSearch, At: 1000, Context: `{"q":"fts5"}`}
	if err := Validate(e); err != nil {
		t.Fatalf("a search with no item should validate, got %v", err)
	}
}

// The one that would quietly destroy weeks of signal if it regressed.
func TestBulkReadIsExcludedFromAffinity(t *testing.T) {
	for _, k := range []Kind{BulkRead, SyncRead} {
		s, ok := Lookup(k)
		if !ok {
			t.Fatalf("%s missing from the registry", k)
		}
		if s.Affinity {
			t.Errorf("%s must not feed affinity: one mark-all-read is not N rejections", k)
		}
		if s.Prior != 0 {
			t.Errorf("%s prior = %v, want 0 — it is neutral, not negative", k, s.Prior)
		}
	}
}

// R17 is enforced in two places that do not read each other: this package's
// Affinity flag, which every derive-level consumer trusts through
// engagedItems, and FeedSignals' own SQL literal
// (`kind NOT IN ('bulk_read','sync_read')`, internal/store/engagements.go),
// which excludes the same two kinds a second, independent way. Nothing stops
// them drifting apart — a kind added here with Affinity=false is not
// automatically excluded from that query, and vice versa.
//
// This reads the actual SQL source and pins its exclusion set against the
// registry's, rather than hard-coding the literal here (which would just be a
// THIRD copy to drift). It cannot touch internal/store — it only reads a file
// that happens to live there.
func TestBulkReadExclusionAgreesWithFeedSignalsSQL(t *testing.T) {
	registryExcluded := map[string]bool{}
	for _, k := range Kinds() {
		spec, ok := Lookup(k)
		if ok && !spec.Affinity {
			registryExcluded[string(k)] = true
		}
	}
	if len(registryExcluded) == 0 {
		t.Fatal("no kind in the registry is excluded from affinity; the registry side of this check is broken")
	}

	src, err := os.ReadFile(filepath.Join("..", "store", "engagements.go"))
	if err != nil {
		t.Fatalf("could not read FeedSignals' source to check its exclusion literal: %v", err)
	}
	m := regexp.MustCompile(`kind NOT IN \(([^)]*)\)`).FindSubmatch(src)
	if m == nil {
		t.Fatal("FeedSignals' `kind NOT IN (...)` exclusion literal was not found; " +
			"either it moved, was reworded, or the SQL-side exclusion was removed entirely")
	}
	sqlExcluded := map[string]bool{}
	for _, part := range strings.Split(string(m[1]), ",") {
		sqlExcluded[strings.Trim(strings.TrimSpace(part), "'")] = true
	}

	for k := range registryExcluded {
		if !sqlExcluded[k] {
			t.Errorf("%q is excluded from affinity in the Go registry but FeedSignals' SQL still reads it", k)
		}
	}
	for k := range sqlExcluded {
		if !registryExcluded[k] {
			t.Errorf("%q is excluded in FeedSignals' SQL but the Go registry still allows it to move affinity", k)
		}
	}
}

func TestEveryKindHasASpec(t *testing.T) {
	for _, k := range Kinds() {
		s, ok := Lookup(k)
		if !ok {
			t.Fatalf("%s has no spec", k)
		}
		if s.Kind != k {
			t.Errorf("%s registered under the wrong key (%s)", k, s.Kind)
		}
		if s.Doc == "" {
			t.Errorf("%s has no documented meaning", k)
		}
	}
}

// Priors are tunable, but their SIGN is the part that must never be wrong.
func TestNegativeSignalsAreNegative(t *testing.T) {
	for _, k := range []Kind{Disliked, Bounced, Skipped, NotInterested, Unsubscribed, RuleMuted} {
		if s, _ := Lookup(k); s.Prior >= 0 {
			t.Errorf("%s prior = %v, want negative", k, s.Prior)
		}
	}
	for _, k := range []Kind{Liked, Noted, Completed, ClickedOut, Reread, Chose} {
		if s, _ := Lookup(k); s.Prior <= 0 {
			t.Errorf("%s prior = %v, want positive", k, s.Prior)
		}
	}
	// An informed rejection outweighs a bare open (§18.1).
	bounce, _ := Lookup(Bounced)
	open, _ := Lookup(Opened)
	if -bounce.Prior <= open.Prior {
		t.Errorf("bounce (%v) should outweigh open (%v)", bounce.Prior, open.Prior)
	}
}

func TestExpectedReadMS(t *testing.T) {
	// 238 words is one minute by construction.
	if got := ExpectedReadMS(238); got < 59_000 || got > 61_000 {
		t.Errorf("ExpectedReadMS(238) = %d, want ~60000", got)
	}
	// An excerpt-only feed reporting nothing must not make every glance look
	// like deep reading.
	if ExpectedReadMS(0) != ExpectedReadMS(MinWords) {
		t.Errorf("a zero word count must floor to MinWords")
	}
}

// The heart of it: the same 30 seconds means opposite things on two articles.
func TestClassifyIsRelativeToLength(t *testing.T) {
	const thirtySeconds = 30_000

	// 100-word link post: expected ~25s, so 30s is a full read.
	if got := Classify(thirtySeconds, 100, 0); got != Read {
		t.Errorf("30s on a link post = %v, want read", got)
	}
	// 4,000-word essay: expected ~17min, so 30s is an informed rejection.
	if got := Classify(thirtySeconds, 4000, 0); got != Bounce {
		t.Errorf("30s on a longform essay = %v, want bounce", got)
	}
}

func TestClassify(t *testing.T) {
	const words = 1000 // expected ≈ 252s

	cases := []struct {
		name string
		ms   int64
		pace float64
		want Depth
	}{
		{"scrolled past", 800, 0, Glance},
		{"just above the floor, nowhere near read", 3_000, 0, Bounce},
		{"looked and left", 30_000, 0, Bounce},
		{"skimmed", 100_000, 0, Skim},
		{"read it", 200_000, 0, Read},
		{"re-read it", 600_000, 0, Read},
		{"a full read at an impossible pace is a skim", 200_000, 8, Skim},
		{"a full read at a plausible pace stays a read", 200_000, 1.2, Read},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.ms, words, c.pace); got != c.want {
				t.Errorf("Classify(%d, %d, %v) = %v, want %v", c.ms, words, c.pace, got, c.want)
			}
		})
	}
}

// One suspended laptop must not outweigh a month of honest reading.
func TestDwellIsCapped(t *testing.T) {
	nineHours := int64(9 * 60 * 60 * 1000)
	if r := Ratio(nineHours, 500); r > 4 {
		t.Errorf("Ratio capped = %v, want <= 4", r)
	}
	if Ratio(nineHours, 500) != Ratio(MaxDwellMS, 500) {
		t.Errorf("anything past MaxDwellMS should clamp to the same ratio")
	}
}

func TestPace(t *testing.T) {
	// 238 words in one minute is exactly average.
	if p := Pace(238, 60_000); p < 0.99 || p > 1.01 {
		t.Errorf("Pace(238, 60s) = %v, want ~1", p)
	}
	// A flick to the bottom.
	if p := Pace(4000, 3_000); p < 10 {
		t.Errorf("Pace(4000 words, 3s) = %v, want a large multiple", p)
	}
	// Unknown must be 0, and callers must not read it as slow.
	if Pace(0, 1000) != 0 || Pace(100, 0) != 0 {
		t.Errorf("Pace with missing inputs must be 0")
	}
}

func TestClamp(t *testing.T) {
	const now = 1_700_000_000_000

	cases := []struct {
		name string
		at   int64
		want int64
	}{
		{"a normal recent event is untouched", now - 5_000, now - 5_000},
		{"a phone offline for a week is kept", now - 7*24*3600*1000, now - 7*24*3600*1000},
		{"small forward skew is tolerated", now + 60_000, now + 60_000},
		{"next Tuesday collapses to now", now + 7*24*3600*1000, now},
		{"1970 clamps to the backlog floor", 1, now - MaxBacklogMS},
		{"a missing timestamp becomes now", 0, now},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Clamp(c.at, now); got != c.want {
				t.Errorf("Clamp(%d, %d) = %d, want %d", c.at, now, got, c.want)
			}
		})
	}
}

func TestSameSession(t *testing.T) {
	const t0 = 1_700_000_000_000
	if !SameSession(t0, t0+10*60*1000) {
		t.Error("ten minutes later is the same sitting")
	}
	if SameSession(t0, t0+45*60*1000) {
		t.Error("forty-five minutes later is a new sitting")
	}
	if SameSession(0, t0) {
		t.Error("an unknown previous time cannot be the same session")
	}
	// The gap is unsigned: an out-of-order pair (next observed before prev, as
	// the outbox can deliver) must be judged the same way as a forward one.
	if !SameSession(t0+10*60*1000, t0) {
		t.Error("a ten-minute gap read backwards should still be the same sitting")
	}
}

// Impression and Dwell are eligible (Affinity=true) but cannot move anything
// on their own — Impression is the denominator, Dwell must be classified
// first. Everything else eligible with a non-zero prior can.
func TestMovesAffinity(t *testing.T) {
	if MovesAffinity(Impression) {
		t.Error("Impression has prior 0 and is the DENOMINATOR; it must not move affinity")
	}
	// Dwell is the deliberate exception the doc comment calls out: prior 0.0
	// like Impression, but special-cased true because it is real signal once
	// classified — "the test is non-zero prior, OR Dwell".
	if !MovesAffinity(Dwell) {
		t.Error("Dwell must move affinity despite its zero prior; that is the special case this function exists for")
	}
	if !MovesAffinity(Liked) {
		t.Error("Liked has a non-zero prior and is eligible; it must move affinity")
	}
	if !MovesAffinity(Bounced) {
		t.Error("Bounced has a non-zero prior and is eligible; it must move affinity")
	}
	// Unsubscribed is source-level (NeedsItem=false) but still Affinity=true —
	// NeedsItem and Affinity are different axes, and a source-level negative
	// still moves affinity, it just isn't attributed to one item.
	if !MovesAffinity(Unsubscribed) {
		t.Error("Unsubscribed has a non-zero prior and Affinity=true; it must move affinity")
	}
	// Only BulkRead and SyncRead (Affinity=false) are excluded regardless of prior.
	if MovesAffinity(BulkRead) {
		t.Error("BulkRead is Affinity=false by design; it must not move affinity")
	}
	if MovesAffinity(Kind("invented")) {
		t.Error("an unknown kind must not move affinity")
	}
}

// The regression this guards: derive.affinityWeight once silently dropped
// reread, chose and clicked_out because they read as "contributes nothing"
// under a naive Prior==0 check that did not exist for them but could for a
// future kind — AffinityKinds is the list callers should use instead of
// writing it out by hand.
func TestAffinityKindsIsSortedAndMatchesMovesAffinity(t *testing.T) {
	got := AffinityKinds()
	if !slices.IsSorted(got) {
		t.Error("AffinityKinds must be sorted, so SQL built from it is stable across map iterations")
	}
	want := map[Kind]bool{}
	for _, k := range Kinds() {
		if MovesAffinity(k) {
			want[k] = true
		}
	}
	if len(got) != len(want) {
		t.Fatalf("AffinityKinds returned %d kinds, MovesAffinity agrees on %d", len(got), len(want))
	}
	for _, k := range got {
		if !want[k] {
			t.Errorf("%s is in AffinityKinds but MovesAffinity(%s) is false", k, k)
		}
	}
	for _, k := range []Kind{Reread, Chose, ClickedOut} {
		if !want[k] {
			t.Errorf("%s dropped out of the affinity set; this is the exact regression AffinityKinds exists to prevent", k)
		}
	}
}

func TestDepthString(t *testing.T) {
	cases := []struct {
		d    Depth
		want string
	}{
		{Glance, "glance"},
		{Bounce, "bounce"},
		{Skim, "skim"},
		{Read, "read"},
		{Depth(99), "glance"},
	}
	for _, c := range cases {
		if got := c.d.String(); got != c.want {
			t.Errorf("Depth(%d).String() = %q, want %q", c.d, got, c.want)
		}
	}
}

// A non-positive dwell is not "a very short read"; it is no observation at
// all, and must score as nothing rather than as a division producing 0/x.
func TestRatioOfNoDwellIsZero(t *testing.T) {
	if r := Ratio(0, 500); r != 0 {
		t.Errorf("Ratio(0, 500) = %v, want 0", r)
	}
	if r := Ratio(-100, 500); r != 0 {
		t.Errorf("Ratio(-100, 500) = %v, want 0 — a negative dwell is not a signal", r)
	}
}

// Weigh is the cold-start fallback (unused today, kept deliberately per its
// doc comment) — a linear-ish sum of priors, sublinear in repeat count so
// three impressions is not three times the information of one.
func TestWeigh(t *testing.T) {
	if got := Weigh(nil); got != 0 {
		t.Errorf("Weigh(nil) = %v, want 0", got)
	}
	if got := Weigh(map[Kind]int{}); got != 0 {
		t.Errorf("Weigh({}) = %v, want 0", got)
	}

	// Kinds excluded from affinity contribute nothing, which is the whole
	// point of BulkRead being in the taxonomy at all.
	if got := Weigh(map[Kind]int{BulkRead: 143}); got != 0 {
		t.Errorf("Weigh(bulk_read: 143) = %v, want 0 — one mark-all-read is not 143 rejections", got)
	}
	// An unknown kind is ignored rather than panicking on a missing spec.
	if got := Weigh(map[Kind]int{Kind("invented"): 5}); got != 0 {
		t.Errorf("Weigh(unknown kind) = %v, want 0", got)
	}
	// A non-positive count contributes nothing.
	if got := Weigh(map[Kind]int{Liked: 0}); got != 0 {
		t.Errorf("Weigh(liked: 0) = %v, want 0", got)
	}

	liked, _ := Lookup(Liked)
	one := Weigh(map[Kind]int{Liked: 1})
	if math.Abs(one-liked.Prior) > 1e-9 {
		t.Errorf("Weigh(liked: 1) = %v, want the bare prior %v", one, liked.Prior)
	}
	// Repeat observations count but sublinearly: five likes must add less than
	// five times a single like's contribution.
	five := Weigh(map[Kind]int{Liked: 5})
	if five <= one || five >= one*5 {
		t.Errorf("Weigh(liked: 5) = %v, want more than one (%v) but less than five times it (%v)",
			five, one, one*5)
	}

	// Negative and positive priors combine algebraically rather than being
	// clamped at zero, since a linear sum is exactly what this function claims to be.
	mixed := Weigh(map[Kind]int{Liked: 1, Disliked: 1})
	disliked, _ := Lookup(Disliked)
	if math.Abs(mixed-(liked.Prior+disliked.Prior)) > 1e-9 {
		t.Errorf("Weigh(liked:1, disliked:1) = %v, want %v", mixed, liked.Prior+disliked.Prior)
	}
}

func TestLogish(t *testing.T) {
	cases := []struct {
		n    int
		want float64
	}{
		{0, 0}, {-1, 0}, {1, 0.5}, {3, 0.75}, {9, 0.9},
	}
	for _, c := range cases {
		if got := logish(c.n); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("logish(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}
