package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkDeriveWrites measures the derivation's whole-picture rewrites at
// their real ceilings (derive.MaxTerms = 2000 terms, MaxEntities = 150
// feeds). Every row is an INSERT inside the ONE write connection, so this is
// time every other mutation on the instance spends waiting.
func BenchmarkDeriveWrites(b *testing.B) {
	ctx := context.Background()
	db, err := Open(Options{Path: b.TempDir() + "/dw.db"})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	repo := NewReaderRepo(db)
	sc := Scope{TenantID: "t", UserID: "u", Role: "member"}
	if err := repo.CreateTenantAndUser(ctx, NewTenant{TenantID: "t", Name: "t", UserID: "u",
		Username: "u", Hash: "x", Role: "member", Now: stamp(time.Now())}); err != nil {
		b.Fatal(err)
	}

	terms := make([]TermWeight, 2000)
	for i := range terms {
		terms[i] = TermWeight{Term: fmt.Sprintf("term%04d", i), Weight: float64(i) / 2000, DocFreq: i % 17}
	}
	ents := make([]Entity, 150)
	for i := range ents {
		ents[i] = Entity{Name: fmt.Sprintf("entity %d", i), Label: fmt.Sprintf("Entity %d", i), Kind: EntityPhrase, Weight: 1, Mentions: 3}
	}

	b.Run("term affinity 2000", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := repo.ReplaceTermAffinity(ctx, sc, terms); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("entity affinity 150", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := repo.ReplaceEntityAffinity(ctx, sc, ents); err != nil {
				b.Fatal(err)
			}
		}
	})
}
