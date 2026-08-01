package store

import (
	"errors"
	"testing"
	"time"
)

func seedScrapeSource(t *testing.T, db *DB, sourceID string, subscribe bool) Scope {
	t.Helper()
	ctx := t.Context()
	repo := NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice",
		Hash: "h", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES (?,?,?,?)`,
		sourceID, "feed:"+sourceID, "https://"+sourceID+".example/", now); err != nil {
		t.Fatal(err)
	}
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	if subscribe {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO subscriptions (id,tenant_id,user_id,source_id,created_at) VALUES (?,?,?,?,?)`,
			"sub-"+sourceID, "t1", "u1", sourceID, now); err != nil {
			t.Fatal(err)
		}
	}
	return sc
}

func validScrapeRule(sourceID string) ScrapeRule {
	return ScrapeRule{
		SourceID: sourceID, IndexURL: "https://" + sourceID + ".example/",
		ItemSelector: ".post", TitleSelector: ".title", LinkSelector: "a",
	}
}

func TestPutScrapeRuleThenScrapeRuleForRoundTrips(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	sc := seedScrapeSource(t, db, "src1", true)

	if err := repo.PutScrapeRule(ctx, sc, validScrapeRule("src1")); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ScrapeRuleFor(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ItemSelector != ".post" || got.TitleSelector != ".title" || got.LinkSelector != "a" {
		t.Errorf("got %+v", got)
	}
	if got.Kind != "html" {
		t.Errorf("Kind = %q, want the html default", got.Kind)
	}
}

func TestScrapeRuleForNotFound(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	if _, err := repo.ScrapeRuleFor(t.Context(), "nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
}

// PutScrapeRule is scoped: the subscription check stops one tenant rewriting
// another's rule for a source they do not follow.
func TestPutScrapeRuleRequiresASubscription(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	sc := seedScrapeSource(t, db, "src1", false)

	if err := repo.PutScrapeRule(ctx, sc, validScrapeRule("src1")); !errors.Is(err, ErrNotFound) {
		t.Errorf("PutScrapeRule with no subscription = %v, want ErrNotFound", err)
	}
}

func TestPutScrapeRuleValidatesRequiredFields(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	sc := seedScrapeSource(t, db, "src1", true)

	cases := map[string]ScrapeRule{
		"no source":  {IndexURL: "https://x.example", ItemSelector: ".p", TitleSelector: ".t", LinkSelector: "a"},
		"no index":   {SourceID: "src1", ItemSelector: ".p", TitleSelector: ".t", LinkSelector: "a"},
		"no item":    {SourceID: "src1", IndexURL: "https://x.example", TitleSelector: ".t", LinkSelector: "a"},
		"no title":   {SourceID: "src1", IndexURL: "https://x.example", ItemSelector: ".p", LinkSelector: "a"},
		"no link":    {SourceID: "src1", IndexURL: "https://x.example", ItemSelector: ".p", TitleSelector: ".t"},
		"json no url": {SourceID: "src1", IndexURL: "https://x.example", ItemSelector: ".p", TitleSelector: ".t",
			LinkTemplate: "https://x.example/{id}", Kind: "json"},
	}
	for name, rule := range cases {
		if err := repo.PutScrapeRule(ctx, sc, rule); err == nil {
			t.Errorf("%s: expected a validation error, got nil", name)
		}
	}

	// An html rule with no link selector but no data url either still needs a
	// data url only when json — a html rule needing a link selector is covered
	// above; this exercises the json+link-template escape hatch succeeding.
	jsonRule := ScrapeRule{
		SourceID: "src1", IndexURL: "https://x.example", ItemSelector: ".p", TitleSelector: ".t",
		LinkTemplate: "https://x.example/{id}", Kind: "json", DataURL: "https://x.example/api",
	}
	if err := repo.PutScrapeRule(ctx, sc, jsonRule); err != nil {
		t.Errorf("a valid json rule with a link template was rejected: %v", err)
	}
}

func TestPutScrapeRuleRejectsOverlongSelector(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	sc := seedScrapeSource(t, db, "src1", true)
	rule := validScrapeRule("src1")
	rule.TitleSelector = ""
	for i := 0; i < MaxSelector+1; i++ {
		rule.TitleSelector += "a"
	}
	if err := repo.PutScrapeRule(ctx, sc, rule); err == nil {
		t.Error("an overlong selector was accepted")
	}
}

// A rewritten rule's health starts over — the old empty_polls count belonged
// to the old selectors.
func TestPutScrapeRuleResetsEmptyPollsOnUpdate(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	sc := seedScrapeSource(t, db, "src1", true)
	if err := repo.PutScrapeRule(ctx, sc, validScrapeRule("src1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordScrapeOutcome(ctx, "src1", 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordScrapeOutcome(ctx, "src1", 0); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ScrapeRuleFor(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if got.EmptyPolls != 2 {
		t.Fatalf("EmptyPolls = %d, want 2 before the rewrite", got.EmptyPolls)
	}

	rule2 := validScrapeRule("src1")
	rule2.TitleSelector = ".headline"
	if err := repo.PutScrapeRule(ctx, sc, rule2); err != nil {
		t.Fatal(err)
	}
	got2, err := repo.ScrapeRuleFor(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if got2.EmptyPolls != 0 {
		t.Errorf("EmptyPolls = %d after a rewrite, want 0", got2.EmptyPolls)
	}
}

func TestRecordScrapeOutcomeTracksSuccessAndFailureCounters(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	sc := seedScrapeSource(t, db, "src1", true)
	if err := repo.PutScrapeRule(ctx, sc, validScrapeRule("src1")); err != nil {
		t.Fatal(err)
	}

	if err := repo.RecordScrapeOutcome(ctx, "src1", 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordScrapeOutcome(ctx, "src1", 0); err != nil {
		t.Fatal(err)
	}
	got, err := repo.ScrapeRuleFor(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if got.EmptyPolls != 2 {
		t.Errorf("EmptyPolls = %d, want 2", got.EmptyPolls)
	}
	if got.LastOKAt != "" {
		t.Errorf("LastOKAt = %q, want empty (no successful poll yet)", got.LastOKAt)
	}

	// A successful poll (found > 0) resets the streak and stamps last_ok_at,
	// which is exactly the signal that distinguishes "the site redesigned" from
	// "a bad week" (§14.2, RULE_BROKEN).
	if err := repo.RecordScrapeOutcome(ctx, "src1", 5); err != nil {
		t.Fatal(err)
	}
	got2, err := repo.ScrapeRuleFor(ctx, "src1")
	if err != nil {
		t.Fatal(err)
	}
	if got2.EmptyPolls != 0 {
		t.Errorf("EmptyPolls = %d after a success, want reset to 0", got2.EmptyPolls)
	}
	if got2.LastOKAt == "" {
		t.Error("LastOKAt was not stamped by a successful poll")
	}
}

func TestKnownGUIDsReportsOnlyExistingOnes(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	sc := seedScrapeSource(t, db, "src1", true)
	_ = sc
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at) VALUES ('i1','src1','guid-known','T',?,?)`,
		now, now); err != nil {
		t.Fatal(err)
	}

	got, err := repo.KnownGUIDs(ctx, "src1", []string{"guid-known", "guid-unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if !got["guid-known"] {
		t.Error("guid-known was not reported as known")
	}
	if got["guid-unknown"] {
		t.Error("guid-unknown was reported as known")
	}
}

func TestKnownGUIDsEmptyInput(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	got, err := repo.KnownGUIDs(t.Context(), "src1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// A GUID from a DIFFERENT source must not count as known — guids are only
// unique within a source.
func TestKnownGUIDsIsScopedToOneSource(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	now := time.Now().UTC().Format(time.RFC3339)
	seedScrapeSource(t, db, "src1", true)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES ('src2','feed:src2','https://src2.example/',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at) VALUES ('i1','src2','shared-guid','T',?,?)`,
		now, now); err != nil {
		t.Fatal(err)
	}
	got, err := repo.KnownGUIDs(ctx, "src1", []string{"shared-guid"})
	if err != nil {
		t.Fatal(err)
	}
	if got["shared-guid"] {
		t.Error("a guid from another source was reported as known")
	}
}

func TestRetireUnusableSourceRefusesWhenSubscribedOrEverSucceeded(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	seedScrapeSource(t, db, "src1", true)

	// Still subscribed: must refuse.
	if err := repo.RetireUnusableSource(ctx, "src1"); err != nil {
		t.Fatal(err)
	}
	var deactivated any
	if err := db.Read.QueryRowContext(ctx, `SELECT deactivated_at FROM sources WHERE id='src1'`).Scan(&deactivated); err != nil {
		t.Fatal(err)
	}
	if deactivated != nil {
		t.Error("a subscribed source was retired")
	}

	// Unsubscribe, but mark it as having succeeded once: still must refuse.
	if _, err := db.Write.ExecContext(ctx, `DELETE FROM subscriptions WHERE source_id='src1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE sources SET last_success_at = ? WHERE id='src1'`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := repo.RetireUnusableSource(ctx, "src1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Read.QueryRowContext(ctx, `SELECT deactivated_at FROM sources WHERE id='src1'`).Scan(&deactivated); err != nil {
		t.Fatal(err)
	}
	if deactivated != nil {
		t.Error("a source that once succeeded was retired")
	}
}

func TestRetireUnusableSourceSucceedsWhenNeitherAppliesAndEmptyIDIsRejected(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	seedScrapeSource(t, db, "src1", false)

	if err := repo.RetireUnusableSource(ctx, "src1"); err != nil {
		t.Fatal(err)
	}
	var deactivated any
	if err := db.Read.QueryRowContext(ctx, `SELECT deactivated_at FROM sources WHERE id='src1'`).Scan(&deactivated); err != nil {
		t.Fatal(err)
	}
	if deactivated == nil {
		t.Error("an unsubscribed, never-succeeded source was not retired")
	}

	if err := repo.RetireUnusableSource(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty sourceID = %v, want ErrNotFound", err)
	}
}
