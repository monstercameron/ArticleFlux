package store

import (
	"context"
	"testing"
	"time"
)

// The layers against the real table, because the precedence unit tests use an
// in-memory fake and the one thing a fake cannot check is that the SQL matches
// the schema.
func TestSettingLayersRoundTrip(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	seedTenant(t, repo)
	sc := Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	const key = "reading.font_size"

	got, err := repo.ResolveSettings(ctx, sc, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[key]) != 0 {
		t.Errorf("an unset key returned %v", got[key])
	}

	if err := repo.SetTenantSetting(ctx, sc, key, "20"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetUserSetting(ctx, sc, key, "24"); err != nil {
		t.Fatal(err)
	}

	got, err = repo.ResolveSettings(ctx, sc, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	if got[key][LayerUser] != "24" || got[key][LayerTenant] != "20" {
		t.Errorf("layers = %v", got[key])
	}

	// Rewriting must replace rather than duplicate: the uniqueness is an
	// expression index, so a broken upsert would silently store two rows and the
	// resolution would pick one at random.
	if err := repo.SetUserSetting(ctx, sc, key, "26"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM settings WHERE scope='user' AND scope_id=? AND key=?`,
		sc.UserID, key).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows after rewriting one setting", n)
	}

	if err := repo.ClearUserSetting(ctx, sc, key); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.ResolveSettings(ctx, sc, []string{key})
	if _, still := got[key][LayerUser]; still {
		t.Error("the user override survived being cleared")
	}
	if got[key][LayerTenant] != "20" {
		t.Error("clearing the user layer disturbed the tenant layer")
	}
}

// Another tenant's settings must be invisible, which is the reason these take a
// Scope at all.
func TestSettingsAreScoped(t *testing.T) {
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := context.Background()
	seedTenant(t, repo)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "other",
		Hash: "x", Role: "member", Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	a := Scope{TenantID: "t1", UserID: "u1", Role: "member"}
	b := Scope{TenantID: "t2", UserID: "u2", Role: "member"}

	if err := repo.SetUserSetting(ctx, a, "reading.theme", `"dark"`); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetTenantSetting(ctx, a, "retention.days", "30"); err != nil {
		t.Fatal(err)
	}

	got, err := repo.ResolveSettings(ctx, b, []string{"reading.theme", "retention.days"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("tenant B sees tenant A's settings: %v", got)
	}

	if _, err := repo.ResolveSettings(ctx, Scope{}, []string{"reading.theme"}); err != ErrNoScope {
		t.Errorf("an unscoped resolve = %v, want ErrNoScope", err)
	}
}
