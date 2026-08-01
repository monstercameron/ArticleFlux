package degrade

import (
	"strings"
	"testing"
)

func at(freeFraction float64) State {
	const total = 100_000_000_000 // 100 GB, so the fractions are exact enough
	return State{TotalBytes: total, FreeBytes: int64(total * freeFraction)}
}

func TestTierBoundaries(t *testing.T) {
	cases := []struct {
		free float64
		want Tier
	}{
		{1.00, Normal}, {0.50, Normal}, {0.21, Normal}, {0.20, Normal},
		{0.199, Warn}, {0.15, Warn}, {0.10, Warn},
		{0.099, Shed}, {0.06, Shed}, {0.05, Shed},
		{0.049, ReadOnlyIngest}, {0.03, ReadOnlyIngest}, {0.02, ReadOnlyIngest},
		{0.019, Frozen}, {0.001, Frozen}, {0, Frozen},
	}
	for _, c := range cases {
		if got := TierFor(at(c.free)); got != c.want {
			t.Errorf("%.1f%% free = %s, want %s", c.free*100, got, c.want)
		}
	}
}

// A stat failure must degrade to "assume fine". An instance that froze because
// it could not read a filesystem would be unusable for a reason nobody could see.
func TestUnknownDiskSizeIsNotAnEmergency(t *testing.T) {
	if got := TierFor(State{}); got != Normal {
		t.Errorf("an unknown disk size produced tier %s", got)
	}
	if got := TierFor(State{FreeBytes: 5}); got != Normal {
		t.Errorf("a zero total produced tier %s", got)
	}
}

// §22.6's core promise, and the rung worth reading twice: at 5% free, polling
// stops but reading, marking read and notes keep working. New articles are
// re-fetchable from the publisher; a note is not.
func TestAtFivePercentReadingAndNotesSurviveWhilePollingStops(t *testing.T) {
	tier := TierFor(at(0.03))
	if tier != ReadOnlyIngest {
		t.Fatalf("tier = %s", tier)
	}

	for _, c := range []Capability{CapReadState, CapNotes, CapOutboxDrain} {
		if !Allowed(tier, c) {
			t.Errorf("%s was refused at the read-only-ingest rung; that is the "+
				"irreplaceable data being shed before the replaceable", c)
		}
	}
	for _, c := range []Capability{CapPoll, CapArchive, CapAudio, CapPackBuild} {
		if Allowed(tier, c) {
			t.Errorf("%s was still permitted at the read-only-ingest rung", c)
		}
	}
}

// Those writes already happened on a device. Refusing them destroys work the
// reader believes is saved, which is worse than any disk problem.
func TestTheOutboxDrainIsNeverRefused(t *testing.T) {
	for _, tier := range []Tier{Normal, Warn, Shed, ReadOnlyIngest, Frozen} {
		if !Allowed(tier, CapOutboxDrain) {
			t.Errorf("the outbox drain was refused at %s", tier)
		}
	}
}

// The ladder must be monotonic: nothing may become permitted again as space runs
// out. A capability that switched back on at a lower rung would be a table typo
// that no boundary test would catch.
func TestTheLadderOnlyEverTakesThingsAway(t *testing.T) {
	tiers := []Tier{Normal, Warn, Shed, ReadOnlyIngest, Frozen}
	caps := []Capability{
		CapAudio, CapSnapshot, CapRender, CapPackBuild, CapImageCache,
		CapArchive, CapPoll, CapEmbed, CapReadState, CapNotes, CapOutboxDrain,
	}
	for _, c := range caps {
		wasRefused := false
		for _, tier := range tiers {
			allowed := Allowed(tier, c)
			if wasRefused && allowed {
				t.Errorf("%s is refused at a higher rung but permitted at %s", c, tier)
			}
			if !allowed {
				wasRefused = true
			}
		}
	}
}

// Audio is tens of megabytes an article and fully regenerable, so it goes first.
// Notes are the last thing shed, and never at all.
func TestSheddingOrder(t *testing.T) {
	warn := TierFor(at(0.15))
	if Allowed(warn, CapAudio) {
		t.Error("audio survived the first rung; it is the most expensive optional thing there is")
	}
	if !Allowed(warn, CapPoll) || !Allowed(warn, CapArchive) {
		t.Error("the first rung stopped fetching content, which is too aggressive for 20%")
	}

	shed := TierFor(at(0.07))
	if Allowed(shed, CapPackBuild) || Allowed(shed, CapImageCache) || Allowed(shed, CapEmbed) {
		t.Error("caching and pack building survived the 10% rung")
	}
	if !Allowed(shed, CapPoll) {
		t.Error("polling stopped at 10%; §22.6 puts that at 5%")
	}

	// Notes survive everything except a genuinely full disk, and even then the
	// outbox drain does not.
	for _, tier := range []Tier{Normal, Warn, Shed, ReadOnlyIngest} {
		if !Allowed(tier, CapNotes) {
			t.Errorf("notes were refused at %s; they are the irreplaceable data (R8)", tier)
		}
	}
}

// §22.6 asks for failure that is legible rather than opaque, and legible means
// the message says what is wrong and what will fix it.
func TestRefusalsExplainThemselves(t *testing.T) {
	if got := Explain(Normal, CapAudio); got != "" {
		t.Errorf("a permitted capability produced an explanation: %q", got)
	}

	for _, tc := range []struct {
		tier Tier
		cap  Capability
		want string
	}{
		{Warn, CapAudio, "20%"},
		{Shed, CapPackBuild, "10%"},
		{ReadOnlyIngest, CapPoll, "5%"},
		{Frozen, CapReadState, "2%"},
	} {
		got := Explain(tc.tier, tc.cap)
		if got == "" {
			t.Errorf("%s at %s produced no explanation", tc.cap, tc.tier)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%q does not say how much space is left (want %q)", got, tc.want)
		}
		if !strings.Contains(got, tc.cap.String()) {
			t.Errorf("%q does not name the capability", got)
		}
	}

	// The 5% message has to say what still works, or it reads as "the
	// application is broken" rather than "it is protecting your notes".
	msg := Explain(ReadOnlyIngest, CapPoll)
	for _, want := range []string{"Reading", "notes"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the read-only-ingest message does not mention %q: %q", want, msg)
		}
	}
}

// Sweeping when there is plenty of space is deleting things nobody asked to
// delete.
func TestSweepTargetIsZeroUntilItMatters(t *testing.T) {
	if got := SweepTarget(at(0.5)); got != 0 {
		t.Errorf("a healthy disk asked to sweep %d bytes", got)
	}
	if got := SweepTarget(at(0.15)); got != 0 {
		t.Errorf("the warn rung asked to sweep %d bytes; it only stops new work", got)
	}
	shed := SweepTarget(at(0.07))
	critical := SweepTarget(at(0.03))
	frozen := SweepTarget(at(0.01))
	if shed <= 0 || critical <= shed || frozen <= critical {
		t.Errorf("sweep targets are not increasing with pressure: %d, %d, %d",
			shed, critical, frozen)
	}
	// Proportional, so a 20 GB VPS and a 2 TB box get sensible numbers from the
	// same code.
	small := SweepTarget(State{TotalBytes: 20_000_000_000, FreeBytes: 1_400_000_000})
	large := SweepTarget(State{TotalBytes: 2_000_000_000_000, FreeBytes: 140_000_000_000})
	if large <= small*10 {
		t.Errorf("the sweep target is not proportional to disk size: %d vs %d", small, large)
	}
}

// Shown from the warn rung rather than only when something breaks: an
// application that degrades silently and then refuses a write leaves the
// operator no runway.
func TestBannerAppearsBeforeAnythingBreaks(t *testing.T) {
	if Banner(Normal) != "" {
		t.Error("a healthy instance shows a banner")
	}
	for _, tier := range []Tier{Warn, Shed, ReadOnlyIngest, Frozen} {
		if Banner(tier) == "" {
			t.Errorf("no banner at %s", tier)
		}
	}
	if !strings.Contains(Banner(ReadOnlyIngest), "still work") {
		t.Errorf("the critical banner does not say what still works: %q", Banner(ReadOnlyIngest))
	}
}

// The String methods are what shows up in log lines and Explain/Banner
// messages, so every named rung and capability needs its own word rather
// than falling through to "unknown".
func TestTierString(t *testing.T) {
	cases := []struct {
		tier Tier
		want string
	}{
		{Normal, "normal"},
		{Warn, "warn"},
		{Shed, "shed"},
		{ReadOnlyIngest, "read-only-ingest"},
		{Frozen, "frozen"},
		{Tier(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.tier.String(); got != c.want {
			t.Errorf("Tier(%d).String() = %q, want %q", c.tier, got, c.want)
		}
	}
}

func TestCapabilityString(t *testing.T) {
	cases := []struct {
		cap  Capability
		want string
	}{
		{CapAudio, "audio synthesis"},
		{CapSnapshot, "page snapshots"},
		{CapRender, "renders"},
		{CapPackBuild, "pack building"},
		{CapImageCache, "image caching"},
		{CapArchive, "archiving"},
		{CapPoll, "polling"},
		{CapEmbed, "embeddings"},
		{CapReadState, "read state"},
		{CapNotes, "notes"},
		{CapOutboxDrain, "outbox drain"},
		{Capability(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.cap.String(); got != c.want {
			t.Errorf("Capability(%d).String() = %q, want %q", c.cap, got, c.want)
		}
	}
}

// Allowed and Explain both fall through a switch over every known Tier;
// an out-of-range value (which TierFor can never itself produce, but the
// exported API accepts any int) has to fail closed rather than panic or
// default to permitted.
func TestAllowedAndExplainFailClosedOnUnknownTier(t *testing.T) {
	if Allowed(Tier(99), CapReadState) {
		t.Error("an unknown tier permitted a capability; it must fail closed")
	}
	if Allowed(Tier(99), CapOutboxDrain) != true {
		t.Error("the outbox drain must still be allowed regardless of tier")
	}
	if got := Explain(Tier(99), CapReadState); got != "read state is unavailable" {
		t.Errorf("Explain(unknown tier) = %q", got)
	}
}
