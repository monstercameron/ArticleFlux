package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/retention"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Retention, as this instance runs it (TODO F36).
//
// The properties here are about the WIRING rather than the policy: that the
// default is keep-forever end to end, that the registry's key and the store's
// key are the same string, and that a corrupt value cannot be read as a
// schedule.

func retentionApp(t *testing.T) (*App, store.Scope) {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "retention.db"),
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	// A real account, because `SetSystemValue` records WHO set it and that
	// column is a foreign key — a settings row with no author is not a row this
	// schema allows, which is the audit trail doing its job.
	sc, err := a.EnsureDevUser(t.Context(), "cam", "articleflux-retention")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	return a, sc
}

// One setting, one name. Two names for one window is how a screen ends up
// writing a value nothing reads.
func TestTheRegistryAndTheStoreNameTheSameSetting(t *testing.T) {
	if string(store.KeyRetentionItemDays) != retention.KeyItemDays {
		t.Errorf("store key %q and registry key %q are different settings",
			store.KeyRetentionItemDays, retention.KeyItemDays)
	}
}

// An instance nobody configured keeps everything.
func TestAnUnconfiguredInstanceKeepsEverything(t *testing.T) {
	a, _ := retentionApp(t)
	if got := a.retentionDays(context.Background()); got != retention.DefaultItemDays {
		t.Errorf("window = %d on a fresh instance, want the keep-forever default", got)
	}
}

// A window that was set is honoured.
func TestAConfiguredWindowIsRead(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()
	if err := a.settings.SetSystemValue(ctx, store.KeyRetentionItemDays, "90", sc.UserID); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := a.retentionDays(ctx); got != 90 {
		t.Errorf("window = %d, want 90", got)
	}
}

// A value that is not a number must read as keep-forever, not as zero by
// accident. They are the same number, and that is luck rather than design.
func TestACorruptWindowKeepsEverything(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()
	if err := a.settings.SetSystemValue(ctx, store.KeyRetentionItemDays, "ninety", sc.UserID); err != nil {
		t.Fatal(err)
	}
	if got := a.retentionDays(ctx); got != retention.DefaultItemDays {
		t.Errorf("window = %d for a value that is not a number", got)
	}
}

// The same question for the windows that DELETE, which is where it stops being
// luck.
//
// The test above is about the item window, whose default is zero — so it passes
// whether a corrupt value reads as "the default" or as "keep everything",
// because those are the same number. Its own comment says as much. The two
// security windows are the case that distinguishes them: their defaults are
// ninety days and a year, and reading a corrupt row as "the default" means
// deleting an audit log on a schedule nobody chose.
//
// A row EXISTS here, which is the whole point. Somebody set this window, so the
// stated default is specifically not their intent, and the value they set may
// well have been the zero that means keep forever.
func TestACorruptSecurityWindowDeletesNothing(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()

	for _, key := range []store.SystemKey{
		retention.KeyAttemptDays,
		retention.KeyAuditDays,
	} {
		if err := a.settings.SetSystemValue(ctx, key, "ninety", sc.UserID); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
		// The default is deliberately passed in, exactly as the real callers
		// pass it, so a regression that reinstates it fails here.
		def := retention.DefaultAuditDays
		if key == retention.KeyAttemptDays {
			def = retention.DefaultAttemptDays
		}
		got := a.windowDays(ctx, key, def)
		if got != 0 {
			t.Errorf("%s = %d for a value that is not a number; want 0 so the sweep "+
				"is skipped. %d would delete on a schedule nobody chose", key, got, got)
		}
	}
}

// An unreadable settings store must not delete either, and this is the path
// that used to be indistinguishable from "nobody configured it".
//
// Closing the database is the honest simulation: it is what a locked file, a
// cancelled context or a closed pool looks like from here. Before the fix this
// returned the deleting default, so a transient database error was enough to
// purge a year of audit rows an operator had set to keep forever.
func TestAnUnreadableSecurityWindowDeletesNothing(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()

	// Set it to keep-forever FIRST, so the stored intent is the opposite of the
	// default. That is the combination that loses data.
	if err := a.settings.SetSystemValue(ctx, retention.KeyAuditDays, "0", sc.UserID); err != nil {
		t.Fatal(err)
	}
	if got := a.windowDays(ctx, retention.KeyAuditDays, retention.DefaultAuditDays); got != 0 {
		t.Fatalf("window = %d before closing; the stored value is 0", got)
	}

	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := a.windowDays(ctx, retention.KeyAuditDays, retention.DefaultAuditDays); got != 0 {
		t.Errorf("window = %d when the settings store cannot be read; want 0. "+
			"Answering %d here deletes a year of audit rows because a query failed",
			got, got)
	}
}

// An App with no settings repo at all (built by hand, before Open finishes
// wiring it) must still answer with the keep-forever default rather than
// dereferencing a nil.
func TestRetentionDaysWithNoSettingsRepoIsTheDefault(t *testing.T) {
	a := &App{}
	if got := a.retentionDays(context.Background()); got != retention.DefaultItemDays {
		t.Errorf("window = %d with no settings repo, want the default", got)
	}
}

// The sweep with a real window configured actually removes what falls outside
// it — the no-op test above only proves the OFF switch; this is the switch
// turned on.
func TestTheSweepRemovesWhatFallsOutsideTheWindow(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()
	if err := a.settings.SetSystemValue(ctx, store.KeyRetentionItemDays, "30", sc.UserID); err != nil {
		t.Fatalf("set: %v", err)
	}

	feed, _, err := a.Repo().Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:old", FeedURL: "https://old.example/f", Title: "Old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Repo().IngestItems(ctx, feed.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://old.example/1", DupeKey: "d1", Title: "Ancient",
		PublishedAt: time.Now().UTC().Add(-5 * 365 * 24 * time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}

	a.sweepRetention(ctx)

	items, _, err := a.Repo().ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("a five-year-old article survived a 30-day retention window (%d items left)", len(items))
	}
}

// And the sweep itself does nothing when nothing is configured — asserted
// through the app rather than the service, because this is the path the poll
// cycle actually takes.
func TestTheSweepIsANoOpByDefault(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()

	feed, _, err := a.Repo().Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:old", FeedURL: "https://old.example/f", Title: "Old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Repo().IngestItems(ctx, feed.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://old.example/1", DupeKey: "d1", Title: "Ancient",
		PublishedAt: time.Now().UTC().Add(-5 * 365 * 24 * time.Hour),
	}}); err != nil {
		t.Fatal(err)
	}

	a.sweepRetention(ctx)

	items, _, err := a.Repo().ListItems(ctx, sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("a five-year-old article was removed by an instance with no retention "+
			"policy (%d items left)", len(items))
	}
}
