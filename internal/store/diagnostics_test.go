package store

import (
	"context"
	"testing"
)

func TestDiagnosticProjectionsOnMigratedDatabase(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	proof, err := db.LoadAnalysisProof(ctx, 14)
	if err != nil {
		t.Fatalf("load analysis proof: %v", err)
	}
	if proof.Items != 0 || proof.Analysed != 0 || len(proof.Genres) != 0 || len(proof.Recent) != 0 {
		t.Fatalf("proof for empty database = %+v", proof)
	}

	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	checksum, err := MigrationChecksum(ctx, db.Path(), migrations[0].version)
	if err != nil {
		t.Fatalf("migration checksum: %v", err)
	}
	if checksum != migrations[0].checksum {
		t.Fatalf("migration checksum = %q, want %q", checksum, migrations[0].checksum)
	}

	seed, err := LoadIngestSeed(ctx, db.Path(), 10)
	if err != nil {
		t.Fatalf("load ingest seed: %v", err)
	}
	if len(seed) != 0 {
		t.Fatalf("ingest seed has %d rows, want 0", len(seed))
	}
}
