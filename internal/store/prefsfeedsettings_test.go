package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// SetPrefs and UpdateFeedSettings are both write paths that take values straight
// from a client, and both were covered almost entirely by their happy path — the
// bounds each one documents were unexercised. The bounds are the interesting
// part: they are what stops one user's request from becoming this server's
// behaviour towards a publisher, or a preferences table from being a place to
// store a megabyte.

func prefsRepo(t *testing.T) (*ReaderRepo, Scope, context.Context) {
	t.Helper()
	repo, sc := seedReader(t, openTest(t))
	return repo, sc, context.Background()
}

// --- SetPrefs -----------------------------------------------------------------

func TestSetPrefsRoundTrips(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	if err := repo.SetPrefs(ctx, sc, Prefs{"theme": "dark", "density": "cosy"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	got, err := repo.GetPrefs(ctx, sc)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if got["theme"] != "dark" || got["density"] != "cosy" {
		t.Errorf("prefs = %v", got)
	}
}

// The upsert half: writing the same key again replaces the value rather than
// failing on the unique index or leaving the old one live.
func TestSetPrefsOverwritesAnExistingKey(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	if err := repo.SetPrefs(ctx, sc, Prefs{"theme": "dark"}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := repo.SetPrefs(ctx, sc, Prefs{"theme": "light"}); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, err := repo.GetPrefs(ctx, sc)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if got["theme"] != "light" {
		t.Errorf("theme = %q, want light", got["theme"])
	}
}

func TestSetPrefsRefusesAnInvalidScope(t *testing.T) {
	repo, _, ctx := prefsRepo(t)
	if err := repo.SetPrefs(ctx, Scope{}, Prefs{"theme": "dark"}); !errors.Is(err, ErrNoScope) {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}

// An empty patch is a no-op, not an error — the client sends whatever changed,
// and "nothing changed" is a thing that happens.
func TestSetPrefsWithNothingToSetIsANoOp(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	if err := repo.SetPrefs(ctx, sc, Prefs{}); err != nil {
		t.Errorf("an empty patch: %v", err)
	}
	if err := repo.SetPrefs(ctx, sc, nil); err != nil {
		t.Errorf("a nil patch: %v", err)
	}
}

// The bounds. A preferences table is a place a client can write to, so each of
// these is a size limit standing between it and the disk.
func TestSetPrefsRefusesOutOfBoundsKeysAndValues(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)

	if err := repo.SetPrefs(ctx, sc, Prefs{"": "value"}); err == nil {
		t.Error("an empty key was accepted")
	}
	if err := repo.SetPrefs(ctx, sc, Prefs{strings.Repeat("k", 65): "v"}); err == nil {
		t.Error("a 65-character key was accepted; the documented limit is 64")
	}
	if err := repo.SetPrefs(ctx, sc, Prefs{"k": strings.Repeat("v", 4097)}); err == nil {
		t.Error("a 4097-byte value was accepted; the documented limit is 4096")
	}

	// And the boundary itself is allowed, or the limit is off by one.
	if err := repo.SetPrefs(ctx, sc, Prefs{strings.Repeat("k", 64): strings.Repeat("v", 4096)}); err != nil {
		t.Errorf("a key and value exactly at the limit were refused: %v", err)
	}
}

// MaxPrefKeys caps how many keys one user may hold. The rule is deliberately
// asymmetric — a NEW key past the cap is refused, an EXISTING one may still be
// updated — because otherwise hitting the cap would also freeze the settings the
// reader already has.
func TestSetPrefsCapsNewKeysButStillUpdatesExistingOnes(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)

	full := Prefs{}
	for i := range MaxPrefKeys {
		full[keyN(i)] = "v"
	}
	if err := repo.SetPrefs(ctx, sc, full); err != nil {
		t.Fatalf("filling to the cap: %v", err)
	}

	if err := repo.SetPrefs(ctx, sc, Prefs{"one-too-many": "v"}); err == nil {
		t.Error("a new key past MaxPrefKeys was accepted")
	}

	// The reader's existing settings must still be changeable.
	if err := repo.SetPrefs(ctx, sc, Prefs{keyN(0): "changed"}); err != nil {
		t.Errorf("an existing key could not be updated at the cap: %v", err)
	}
	got, err := repo.GetPrefs(ctx, sc)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if got[keyN(0)] != "changed" {
		t.Errorf("the update did not land: %q", got[keyN(0)])
	}
}

func keyN(i int) string {
	return "pref-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

// --- UpdateFeedSettings -------------------------------------------------------

func firstSource(t *testing.T, repo *ReaderRepo, ctx context.Context, sc Scope) string {
	t.Helper()
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(feeds) == 0 {
		t.Fatal("the fixture has no feeds")
	}
	return feeds[0].SourceID
}

// Membership first. Without it a user could retune the poll interval of a source
// they do not subscribe to — a global row they have no relationship with.
func TestUpdateFeedSettingsRefusesASourceTheUserDoesNotSubscribeTo(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	title := "hijacked"
	_, err := repo.UpdateFeedSettings(ctx, sc, "no-such-source", FeedSettingsPatch{Title: &title})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

func TestUpdateFeedSettingsRefusesAnInvalidScope(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)
	title := "x"
	if _, err := repo.UpdateFeedSettings(ctx, Scope{}, id, FeedSettingsPatch{Title: &title}); !errors.Is(err, ErrNoScope) {
		t.Errorf("= %v, want ErrNoScope", err)
	}
}

func TestUpdateFeedSettingsAppliesEachFieldIndependently(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)

	title := "  Renamed Journal  "
	inMega := false
	depth := 25
	got, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{
		Title: &title, InMegafeed: &inMega, CacheDepth: &depth,
	})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	// The title is trimmed on the way in, so a name padded by a text field does
	// not sort under a space.
	if got.Title != "Renamed Journal" {
		t.Errorf("title = %q, want it trimmed", got.Title)
	}
	if got.InMegafeed {
		t.Error("in_megafeed was not cleared")
	}
	if got.CacheDepth != 25 {
		t.Errorf("cache depth = %d, want 25", got.CacheDepth)
	}
}

// A patch with no fields set must leave everything alone rather than blanking
// the row — every field is a pointer precisely so "not mentioned" is expressible.
func TestUpdateFeedSettingsWithAnEmptyPatchChangesNothing(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)

	before, err := repo.GetFeedSettings(ctx, sc, id)
	if err != nil {
		t.Fatalf("GetFeedSettings: %v", err)
	}
	after, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if after.Title != before.Title || after.InMegafeed != before.InMegafeed ||
		after.CacheDepth != before.CacheDepth {
		t.Errorf("an empty patch changed the row: %+v -> %+v", before, after)
	}
}

// A negative cache depth is clamped rather than refused: it is a slider, and the
// honest response to a nonsensical position is the nearest sensible one.
func TestUpdateFeedSettingsClampsANegativeCacheDepth(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)

	depth := -5
	got, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{CacheDepth: &depth})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if got.CacheDepth < 0 {
		t.Errorf("cache depth = %d, want it clamped to 0 or above", got.CacheDepth)
	}
}

// The politeness limit, and the reason it is clamped at the WRITE rather than
// trusted from the client: fetch_interval_s is a GLOBAL column, so an unbounded
// value from one user is this server's behaviour towards a publisher on
// everyone's behalf.
func TestUpdateFeedSettingsClampsTheFetchIntervalAtBothEnds(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)

	tooFast := 1
	got, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{FetchIntervalS: &tooFast})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if got.FetchIntervalS < minFetchInterval {
		t.Errorf("a one-second interval was stored as %d; the floor is %d",
			got.FetchIntervalS, minFetchInterval)
	}

	tooSlow := maxFetchInterval * 10
	got, err = repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{FetchIntervalS: &tooSlow})
	if err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if got.FetchIntervalS > maxFetchInterval {
		t.Errorf("interval = %d, above the %d ceiling — past that a feed is not "+
			"being polled, it is being kept", got.FetchIntervalS, maxFetchInterval)
	}
}

// An empty MutedUntil CLEARS the mute rather than storing "", so
// "muted_until IS NULL" stays the single test for "not muted".
func TestUpdateFeedSettingsClearsTheMuteWithAnEmptyString(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)

	until := "2027-01-01T00:00:00Z"
	got, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{MutedUntil: &until})
	if err != nil {
		t.Fatalf("muting: %v", err)
	}
	if got.MutedUntil == "" {
		t.Fatal("the mute did not take")
	}

	clear := "   "
	got, err = repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{MutedUntil: &clear})
	if err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if got.MutedUntil != "" {
		t.Errorf("muted_until = %q; whitespace should clear the mute, not store it", got.MutedUntil)
	}
}

// A mute has to be a time, because the only thing that ever ends one is a
// string comparison against the clock.
//
// # The shape of the failure
//
// MegafeedSources decides whether a feed is muted with
// `sub.muted_until <= ?`, where the parameter is stamp(now) — a byte-for-byte
// comparison of two timestamp STRINGS. That is sound only while everything
// stored in the column is in the one canonical format. Nothing enforced it:
// the gRPC layer copies req.MutedUntil through untouched and the repo wrote it
// verbatim, so the column held whatever the caller sent.
//
// Send something that is not a timestamp and the comparison does not error, it
// just answers wrongly forever. "zzz" is greater than every timestamp this
// century, so `muted_until <= now` is false at every future instant and the
// feed leaves the megafeed permanently. There is no expiry that can pass, and
// nothing on the settings screen can explain it, because the screen parses the
// same value and cannot read it either.
//
// A numeric UTC offset is the same bug wearing a valid costume. "+02:00" is
// legal RFC3339 and the shipped UI never emits it, but this field is on the
// sync API too, and lexicographically "T12:00:00+02:00" sorts after
// "T10:00:00Z" — so a mute that expired two hours ago is still in force.
func TestAMutedUntilThatIsNotATimestampDoesNotMuteForever(t *testing.T) {
	for _, bad := range []string{"zzz", "later", "9999", "not-a-date"} {
		t.Run(bad, func(t *testing.T) {
			repo, sc, ctx := prefsRepo(t)
			id := firstSource(t, repo, ctx, sc)
			v := bad
			if _, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{MutedUntil: &v}); err == nil {
				t.Fatalf("%q was accepted as a mute expiry", bad)
			}
			// Refused, so the feed is still on the megafeed. The alternative —
			// storing it — is the permanent mute described above.
			live, err := repo.MegafeedSources(ctx, sc)
			if err != nil {
				t.Fatalf("MegafeedSources: %v", err)
			}
			if !live[id] {
				t.Errorf("the feed left the megafeed on an unparseable mute expiry, "+
					"and no future instant can bring it back: %q", bad)
			}
		})
	}
}

// A mute in a legal but non-canonical spelling has to expire when it says it
// does, not when its bytes happen to sort past the clock.
func TestAMutedUntilIsStoredInTheFormatTheComparisonAssumes(t *testing.T) {
	repo, sc, ctx := prefsRepo(t)
	id := firstSource(t, repo, ctx, sc)

	// Two hours in the past, written with a +05:00 offset. As an instant it is
	// long expired; rendered in that zone its wall clock reads three hours
	// AHEAD of now, so as bytes it sorts after stamp(now). The offset has to
	// exceed the age for the digits to diverge — at +02:00 on a two-hour-old
	// time they cancel exactly, which is a coincidence and not a guarantee.
	past := time.Now().UTC().Add(-2 * time.Hour).In(time.FixedZone("UTC+5", 5*60*60))
	v := past.Format(time.RFC3339)
	if _, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{MutedUntil: &v}); err != nil {
		t.Fatalf("a valid RFC3339 timestamp was refused: %v", err)
	}
	live, err := repo.MegafeedSources(ctx, sc)
	if err != nil {
		t.Fatalf("MegafeedSources: %v", err)
	}
	if !live[id] {
		t.Errorf("a mute that expired two hours ago is still in force: the stored "+
			"value %q was compared as bytes against the clock rather than as a time", v)
	}

	// And the ordinary path still works: a mute in the future mutes.
	fut := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	if _, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{MutedUntil: &fut}); err != nil {
		t.Fatalf("UpdateFeedSettings: %v", err)
	}
	if live, err = repo.MegafeedSources(ctx, sc); err != nil {
		t.Fatalf("MegafeedSources: %v", err)
	}
	if live[id] {
		t.Error("a feed muted for the next two hours is still on the megafeed")
	}

	// Clearing still works.
	empty := ""
	if _, err := repo.UpdateFeedSettings(ctx, sc, id, FeedSettingsPatch{MutedUntil: &empty}); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if live, err = repo.MegafeedSources(ctx, sc); err != nil {
		t.Fatalf("MegafeedSources: %v", err)
	}
	if !live[id] {
		t.Error("an unmuted feed did not come back to the megafeed")
	}
}
