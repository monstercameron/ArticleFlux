package signals

import "testing"

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
}
