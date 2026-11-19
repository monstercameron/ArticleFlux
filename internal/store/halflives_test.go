package store

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// halfLifeFixture: 40 sources for one reader, engagements of every kind at
// varied ages — some sources under the ten-sample floor, some over, some with
// engagements that predate publication (filtered), and another reader's
// engagements on the same items (must not count).
func halfLifeFixture(t testing.TB) (*ReaderRepo, Scope, []string) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(Options{Path: t.TempDir() + "/hl.db"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewReaderRepo(db)
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	scopes := []Scope{{TenantID: "ta", UserID: "ua", Role: "member"}, {TenantID: "tb", UserID: "ub", Role: "member"}}
	for _, sc := range scopes {
		if err := repo.CreateTenantAndUser(ctx, NewTenant{TenantID: sc.TenantID, Name: sc.TenantID,
			UserID: sc.UserID, Username: sc.UserID, Hash: "x", Role: "member", Now: stamp(now)}); err != nil {
			t.Fatal(err)
		}
	}
	var sources []string
	kinds := []string{"opened", "dwell", "completed", "liked", "impression"}
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		for s := 0; s < 40; s++ {
			src := fmt.Sprintf("src%02d", s)
			sources = append(sources, src)
			if _, err := tx.ExecContext(ctx, `INSERT INTO sources (id,natural_key,feed_url,kind,created_at) VALUES (?,?,?,?,?)`,
				src, "nk:"+src, "https://x/"+src, "feed", stamp(now)); err != nil {
				return err
			}
			for i := 0; i < s; i++ { // source s has s items: 0..39 engagements
				id := fmt.Sprintf("%s-i%d", src, i)
				pub := now.Add(-time.Duration(i*7+s) * time.Hour)
				if _, err := tx.ExecContext(ctx, `INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at) VALUES (?,?,?,?,?,?)`,
					id, src, id, id, stamp(pub), stamp(now)); err != nil {
					return err
				}
				for k, sc := range scopes {
					at := pub.Add(time.Duration((i*13+k*5)%50) * time.Hour)
					if i%9 == 4 {
						at = pub.Add(-time.Hour) // before publication: filtered out
					}
					if _, err := tx.ExecContext(ctx, `INSERT INTO engagements (id,tenant_id,user_id,item_id,source_id,kind,surface,at,recorded_at)
						VALUES (?,?,?,?,?,?,?,?,?)`, fmt.Sprintf("e-%s-%d", id, k), sc.TenantID, sc.UserID, id, src,
						kinds[(i+k)%len(kinds)], "list", at.UnixMilli(), at.UnixMilli()); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return repo, scopes[0], sources
}

func TestHalfLivesMatchesHalfLifeFor(t *testing.T) {
	repo, sc, sources := halfLifeFixture(t)
	ctx := context.Background()
	got, err := repo.HalfLives(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	nonzero := 0
	for _, src := range append(sources, "no-such-source") {
		want, err := repo.HalfLifeFor(ctx, sc, src)
		if err != nil {
			t.Fatal(err)
		}
		if got[src] != want {
			t.Errorf("%s: HalfLives %v, HalfLifeFor %v", src, got[src], want)
		}
		if want != 0 {
			nonzero++
		}
	}
	if nonzero == 0 || nonzero == len(sources) {
		t.Fatalf("%d of %d sources have a half-life; the fixture must straddle the sample floor", nonzero, len(sources))
	}
	if _, err := repo.HalfLives(ctx, Scope{}); err != ErrNoScope {
		t.Errorf("invalid scope: %v, want ErrNoScope", err)
	}
}

// BenchmarkHalfLives is the derive step it replaced, per source and batched,
// over 40 sources.
func BenchmarkHalfLives(b *testing.B) {
	repo, sc, sources := halfLifeFixture(b)
	ctx := context.Background()
	b.Run("per source", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, src := range sources {
				if _, err := repo.HalfLifeFor(ctx, sc, src); err != nil {
					b.Fatal(err)
				}
			}
		}
	})
	b.Run("batched", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := repo.HalfLives(ctx, sc); err != nil {
				b.Fatal(err)
			}
		}
	})
}
