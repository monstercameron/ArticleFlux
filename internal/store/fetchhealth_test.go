package store

import (
	"context"
	"testing"
	"time"
)

// RecordFetch is written on every poll of every feed and had no test.
//
// It is the bookkeeping the whole poller runs on, and both halves of it fail
// quietly. The backoff half decides how often a dead feed is retried, and
// getting it wrong is invisible until an instance is hammering a host that
// stopped answering months ago. The success half writes the conditional-GET
// state, and getting THAT wrong is invisible in a different way: the reader
// keeps working, it just re-downloads every feed in full forever, which looks
// like nothing at all from the inside.

// sourceHealth is the row RecordFetch writes, read back for assertions.
type sourceHealth struct {
	lastFetch    string
	lastSuccess  string
	lastError    string
	failures     int
	nextFetch    string
	etag         string
	lastModified string
	title        string
	siteURL      string
	iconURL      string
	interval     int
}

func healthOf(t *testing.T, db *DB, sourceID string) sourceHealth {
	t.Helper()
	var h sourceHealth
	err := db.Read.QueryRow(`
		SELECT COALESCE(last_fetch_at,''), COALESCE(last_success_at,''),
		       COALESCE(last_error,''), consecutive_failures,
		       COALESCE(next_fetch_at,''), COALESCE(etag,''),
		       COALESCE(last_modified,''), COALESCE(title,''),
		       COALESCE(site_url,''), COALESCE(icon_url,''), fetch_interval_s
		  FROM sources WHERE id = ?`, sourceID).
		Scan(&h.lastFetch, &h.lastSuccess, &h.lastError, &h.failures, &h.nextFetch,
			&h.etag, &h.lastModified, &h.title, &h.siteURL, &h.iconURL, &h.interval)
	if err != nil {
		t.Fatalf("reading source health: %v", err)
	}
	return h
}

func (h sourceHealth) nextFetchIn(t *testing.T) time.Duration {
	t.Helper()
	next, err := time.Parse(time.RFC3339Nano, h.nextFetch)
	if err != nil {
		t.Fatalf("next_fetch_at = %q, which does not parse", h.nextFetch)
	}
	last, err := time.Parse(time.RFC3339Nano, h.lastFetch)
	if err != nil {
		t.Fatalf("last_fetch_at = %q, which does not parse", h.lastFetch)
	}
	return next.Sub(last)
}

// oneSource returns a subscribed feed's source id.
func oneSource(t *testing.T, db *DB) (*ReaderRepo, string) {
	t.Helper()
	repo := NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(context.Background(), NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	f, _, err := repo.Subscribe(context.Background(),
		Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"},
		NewSubscription{NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	return repo, f.SourceID
}

// The backoff doubles, and it stops doubling. A feed that has failed ten times
// in a row is very likely gone; continuing to hit it every fifteen minutes is
// rude to whatever is still answering and a waste of the poller.
func TestAFailedFetchBacksOffAndTheDoublingHasACeiling(t *testing.T) {
	db := openTest(t)
	repo, sourceID := oneSource(t, db)
	ctx := context.Background()

	base := time.Duration(healthOf(t, db, sourceID).interval) * time.Second
	if base <= 0 {
		t.Fatalf("the fixture source has a fetch interval of %v", base)
	}

	var last time.Duration
	for attempt := 1; attempt <= 8; attempt++ {
		if err := repo.RecordFetch(ctx, FetchOutcome{SourceID: sourceID, Err: "connection refused"}); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		h := healthOf(t, db, sourceID)

		if h.failures != attempt {
			t.Fatalf("after %d failures the counter reads %d", attempt, h.failures)
		}
		if h.lastError != "connection refused" {
			t.Errorf("last_error = %q after a failure", h.lastError)
		}

		gap := h.nextFetchIn(t)
		switch {
		case attempt <= 6:
			// interval << attempt, so each failure doubles the wait.
			want := base * (1 << attempt)
			if want > 86400*time.Second {
				want = 86400 * time.Second
			}
			if gap != want {
				t.Errorf("failure %d scheduled the retry in %v, want %v", attempt, gap, want)
			}
		default:
			// Past six the shift is clamped, so the wait stops growing. Without
			// the clamp the shift keeps going and the interval eventually
			// overflows into a negative — a retry scheduled in the past, which
			// turns a dead feed into a hot loop.
			if gap != last {
				t.Errorf("failure %d moved the retry from %v to %v; the doubling was "+
					"supposed to stop at six", attempt, last, gap)
			}
		}
		if gap > 86400*time.Second {
			t.Errorf("failure %d scheduled the retry in %v, past the one-day ceiling", attempt, gap)
		}
		last = gap
	}
}

// One bad afternoon must not leave a healthy feed on a day-long backoff.
func TestASuccessfulFetchClearsTheFailureStateEntirely(t *testing.T) {
	db := openTest(t)
	repo, sourceID := oneSource(t, db)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if err := repo.RecordFetch(ctx, FetchOutcome{SourceID: sourceID, Err: "timeout"}); err != nil {
			t.Fatal(err)
		}
	}
	if h := healthOf(t, db, sourceID); h.failures != 5 {
		t.Fatalf("the fixture did not accumulate failures: %+v", h)
	}

	if err := repo.RecordFetch(ctx, FetchOutcome{SourceID: sourceID}); err != nil {
		t.Fatal(err)
	}
	h := healthOf(t, db, sourceID)

	if h.failures != 0 {
		t.Errorf("consecutive_failures = %d after a success", h.failures)
	}
	if h.lastError != "" {
		t.Errorf("last_error = %q after a success", h.lastError)
	}
	if h.lastSuccess == "" {
		t.Error("last_success_at was not stamped")
	}
	if gap := h.nextFetchIn(t); gap != 30*time.Minute {
		t.Errorf("the next poll is in %v, want the 30 minutes a healthy feed gets", gap)
	}
}

// The conditional-GET state, and the rule that makes it work: an EMPTY value
// leaves the stored one alone.
//
// This is the quiet one. A server that answers 304 sends no ETag, and a
// RecordFetch that wrote the empty string over the stored one would clear the
// validator the next request depends on — so the following poll asks
// unconditionally, gets 200 and the whole body, and stores the ETag again.
// Every other poll downloads the feed in full, forever, and nothing about the
// reader looks broken.
func TestAnEmptyValidatorDoesNotWipeTheStoredOne(t *testing.T) {
	db := openTest(t)
	repo, sourceID := oneSource(t, db)
	ctx := context.Background()

	if err := repo.RecordFetch(ctx, FetchOutcome{
		SourceID: sourceID, ETag: `W/"abc123"`, LastModified: "Wed, 21 Oct 2026 07:28:00 GMT",
		Title: "Alpha Journal", SiteURL: "https://a.example", IconURL: "https://a.example/icon.png",
	}); err != nil {
		t.Fatal(err)
	}
	full := healthOf(t, db, sourceID)
	if full.etag != `W/"abc123"` || full.lastModified == "" {
		t.Fatalf("the first fetch did not store its validators: %+v", full)
	}

	// The 304 case: a bare success carrying nothing.
	if err := repo.RecordFetch(ctx, FetchOutcome{SourceID: sourceID}); err != nil {
		t.Fatal(err)
	}
	got := healthOf(t, db, sourceID)

	if got.etag != full.etag {
		t.Errorf("etag = %q after an empty outcome, want the stored %q — the next "+
			"poll can no longer be conditional", got.etag, full.etag)
	}
	if got.lastModified != full.lastModified {
		t.Errorf("last_modified = %q, want the stored %q", got.lastModified, full.lastModified)
	}
	if got.title != full.title {
		t.Errorf("title = %q, want the stored %q — a feed would lose its name on "+
			"any poll that did not re-send it", got.title, full.title)
	}
	if got.siteURL != full.siteURL || got.iconURL != full.iconURL {
		t.Errorf("site_url/icon_url were cleared: %q/%q", got.siteURL, got.iconURL)
	}
}

// A non-empty value does overwrite. The rule above must not become "never
// update", or a publisher who moves their site or changes their title is
// recorded at the value they had the first time anybody polled them.
func TestANewValidatorReplacesTheStoredOne(t *testing.T) {
	db := openTest(t)
	repo, sourceID := oneSource(t, db)
	ctx := context.Background()

	for _, o := range []FetchOutcome{
		{SourceID: sourceID, ETag: `"one"`, Title: "Old Name", SiteURL: "https://old.example"},
		{SourceID: sourceID, ETag: `"two"`, Title: "New Name", SiteURL: "https://new.example"},
	} {
		if err := repo.RecordFetch(ctx, o); err != nil {
			t.Fatal(err)
		}
	}

	h := healthOf(t, db, sourceID)
	if h.etag != `"two"` {
		t.Errorf("etag = %q, want the newest", h.etag)
	}
	if h.title != "New Name" {
		t.Errorf("title = %q, want the newest", h.title)
	}
	if h.siteURL != "https://new.example" {
		t.Errorf("site_url = %q, want the newest", h.siteURL)
	}
}

// A failure against a source that is not there has to come back as an error.
// The failure path reads the row before it writes, and swallowing the miss
// would report a successful poll of a feed that does not exist.
func TestRecordingAFailureForAnUnknownSourceIsAnError(t *testing.T) {
	db := openTest(t)
	repo, _ := oneSource(t, db)

	if err := repo.RecordFetch(context.Background(), FetchOutcome{
		SourceID: "no-such-source", Err: "connection refused",
	}); err == nil {
		t.Error("a failure was recorded against a source that does not exist")
	}
}

// A successful poll has to schedule the next one at the source's own interval.
//
// # The setting and the scheduler disagreed
//
// The reader picks from "Fetch every" — 5 min, 15 min, 30 min, 1 hour, 6 hours,
// daily — and the hint under it reads "How often the server polls it." That
// choice is clamped and written to sources.fetch_interval_s by
// UpdateFeedSettings, which also recomputes next_fetch_at as last-fetch plus the
// new interval. Correct arithmetic, in one place.
//
// RecordFetch then overwrote it on the very next successful poll with a flat
// `now.Add(30*time.Minute)`, reading nothing from the column. The interval
// survived only as the FAILURE backoff base (`interval << failures`) and as the
// divisor in DueSources' staleness ratio — never as the period, which is the one
// thing the control claims to set.
//
// So the two ends of the same feature disagreed: a feed set to "daily" became
// eligible again half an hour after each poll, and a feed set to "5 min" could
// not be polled sooner than thirty regardless. DueSources' own worked example
// ("A: 15-minute interval, due at 10:00 / B: 24-hour interval, due at 09:00")
// is only expressible if due times come from each source's interval, so the
// scheduler's design assumed the write this test pins down.
//
// The default column value is 1800 — exactly the thirty minutes that was
// hardcoded — so a source nobody has retuned behaves identically either way.
// That is what made the literal survive: it was right for every source in the
// fixture and wrong for every source a reader had touched.
func TestASuccessfulFetchSchedulesTheNextOneAtTheSourcesInterval(t *testing.T) {
	db := openTest(t)
	repo, sourceID := oneSource(t, db)
	ctx := context.Background()

	for _, interval := range []int{300, 3600, 21600, 86400} {
		if _, err := db.Write.Exec(
			`UPDATE sources SET fetch_interval_s = ? WHERE id = ?`, interval, sourceID); err != nil {
			t.Fatal(err)
		}
		if err := repo.RecordFetch(ctx, FetchOutcome{SourceID: sourceID}); err != nil {
			t.Fatalf("RecordFetch: %v", err)
		}
		h := healthOf(t, db, sourceID)
		got := h.nextFetchIn(t)
		want := time.Duration(interval) * time.Second

		// A generous window: the point is which interval was used, not clock skew.
		if got < want-time.Minute || got > want+time.Minute {
			t.Errorf("interval %ds: next poll scheduled in %v, want about %v — the "+
				"reader chose %v under a control labelled \"How often the server polls it\"",
				interval, got.Round(time.Second), want, want)
		}
	}

	// And the scheduler agrees: a source just polled on a six-hour interval is
	// not due, where before it became due after thirty minutes.
	if _, err := db.Write.Exec(
		`UPDATE sources SET fetch_interval_s = 21600 WHERE id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordFetch(ctx, FetchOutcome{SourceID: sourceID}); err != nil {
		t.Fatalf("RecordFetch: %v", err)
	}
	due, err := repo.DueSources(ctx, 50)
	if err != nil {
		t.Fatalf("DueSources: %v", err)
	}
	for _, s := range due {
		if s.ID == sourceID {
			t.Error("a source polled moments ago on a six-hour interval is already due")
		}
	}
}
