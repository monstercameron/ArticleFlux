package store

import (
	"context"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// ScopesToDerive schedules the deriver only for users who logged an
// affinity-moving signal since `since` — see interest.go's long comment on why
// impression/bulk_read must not be able to schedule the job even though they
// are common.
func TestScopesToDeriveFindsUsersWithRecentAffinitySignals(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	since := time.Now().Add(-time.Hour)
	if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "e1", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: time.Now().UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}

	scopes, err := repo.ScopesToDerive(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0].TenantID != sc.TenantID || scopes[0].UserID != sc.UserID {
		t.Errorf("scopes = %+v, want exactly [%+v]", scopes, sc)
	}
}

// Impression and bulk_read are excluded from the affinity registry by design
// (R17): they must not move a score, and by the same rule they must not be
// able to schedule the job that computes one.
func TestScopesToDeriveIgnoresNonAffinitySignals(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "e1", ItemID: it.ID, Kind: signals.Impression, Surface: signals.SurfaceList, At: time.Now().UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}

	scopes, err := repo.ScopesToDerive(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Errorf("scopes = %+v, want none (impression alone must not schedule derive)", scopes)
	}
}

func TestScopesToDeriveRespectsTheSinceWindow(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	old := time.Now().Add(-48 * time.Hour)
	if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "e1", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: old.UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}

	scopes, err := repo.ScopesToDerive(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Errorf("scopes = %+v, want none (the only signal is outside the window)", scopes)
	}
}

func TestScopesToDeriveExcludesDeactivatedUsers(t *testing.T) {
	db := openTest(t)
	repo, sc := seedReader(t, db)
	ctx := context.Background()
	it := firstItem(t, repo, sc)

	if _, err := repo.RecordEngagements(ctx, sc, []signals.Event{
		{ID: "e1", ItemID: it.ID, Kind: signals.Opened, Surface: signals.SurfaceList, At: time.Now().UnixMilli()},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE users SET deactivated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), sc.UserID); err != nil {
		t.Fatal(err)
	}

	scopes, err := repo.ScopesToDerive(ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 0 {
		t.Errorf("scopes = %+v, want none (the only engaged user is deactivated)", scopes)
	}
}
