package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

// AnalysisProof is the small, instance-wide projection used by cmd/e2eproof.
// It deliberately exposes no database handle so diagnostic commands cannot
// grow their own knowledge of the schema.
type AnalysisProof struct {
	Items    int
	Analysed int
	Genres   []AnalysisProofGenre
	Recent   []AnalysisProofItem
}

type AnalysisProofGenre struct {
	Name  string
	Count int
}

type AnalysisProofItem struct {
	Title          string
	CategoryScores string
}

// LoadAnalysisProof returns aggregate analysis evidence from the global item
// tables. It is for the offline e2eproof command, not a tenant-facing request.
func (db *DB) LoadAnalysisProof(ctx context.Context, recentLimit int) (AnalysisProof, error) {
	var proof AnalysisProof
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&proof.Items); err != nil {
		return AnalysisProof{}, fmt.Errorf("store: count proof items: %w", err)
	}
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM item_analysis`).Scan(&proof.Analysed); err != nil {
		return AnalysisProof{}, fmt.Errorf("store: count proof analyses: %w", err)
	}

	rows, err := db.Read.QueryContext(ctx, `
		SELECT genre, count(*) FROM item_analysis WHERE genre IS NOT NULL
		 GROUP BY genre ORDER BY count(*) DESC LIMIT 6`)
	if err != nil {
		return AnalysisProof{}, fmt.Errorf("store: load proof genres: %w", err)
	}
	for rows.Next() {
		var genre AnalysisProofGenre
		if err := rows.Scan(&genre.Name, &genre.Count); err != nil {
			rows.Close()
			return AnalysisProof{}, fmt.Errorf("store: scan proof genre: %w", err)
		}
		proof.Genres = append(proof.Genres, genre)
	}
	if err := rows.Close(); err != nil {
		return AnalysisProof{}, fmt.Errorf("store: close proof genres: %w", err)
	}
	if err := rows.Err(); err != nil {
		return AnalysisProof{}, fmt.Errorf("store: iterate proof genres: %w", err)
	}

	rows, err = db.Read.QueryContext(ctx, `
		SELECT i.title, a.category_scores
		  FROM item_analysis a JOIN items i ON i.id = a.item_id
		 ORDER BY i.published_at DESC LIMIT ?`, recentLimit)
	if err != nil {
		return AnalysisProof{}, fmt.Errorf("store: load recent proof items: %w", err)
	}
	for rows.Next() {
		var item AnalysisProofItem
		if err := rows.Scan(&item.Title, &item.CategoryScores); err != nil {
			rows.Close()
			return AnalysisProof{}, fmt.Errorf("store: scan recent proof item: %w", err)
		}
		proof.Recent = append(proof.Recent, item)
	}
	if err := rows.Close(); err != nil {
		return AnalysisProof{}, fmt.Errorf("store: close recent proof items: %w", err)
	}
	if err := rows.Err(); err != nil {
		return AnalysisProof{}, fmt.Errorf("store: iterate recent proof items: %w", err)
	}
	return proof, nil
}

// MigrationChecksum reads one recorded migration checksum without migrating or
// otherwise modifying the database.
func MigrationChecksum(ctx context.Context, path string, version int) (string, error) {
	db, err := openDiagnosticSource(path)
	if err != nil {
		return "", err
	}
	defer db.Close()

	var checksum string
	if err := db.QueryRowContext(ctx,
		`SELECT checksum FROM schema_migrations WHERE version = ?`, version,
	).Scan(&checksum); err != nil {
		return "", fmt.Errorf("store: migration %d checksum: %w", version, err)
	}
	return checksum, nil
}

// IngestSeedItem is a source plus one article copied by cmd/e2eproof through
// the normal ingest path.
type IngestSeedItem struct {
	SourceTitle string
	FeedURL     string
	Item        IngestItem
}

// LoadIngestSeed reads recent global articles from a live database in strict
// read-only mode. The destination command still writes them through IngestItems.
func LoadIngestSeed(ctx context.Context, path string, limit int) ([]IngestSeedItem, error) {
	db, err := openDiagnosticSource(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT ifnull(s.title,'Feed'), ifnull(s.feed_url,'https://x/'||s.id),
		       i.guid, ifnull(i.url,''), i.title, ifnull(i.summary,''),
		       ifnull(i.content_html,''), i.published_at, i.word_count
		  FROM items i JOIN sources s ON s.id = i.source_id
		 ORDER BY i.published_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: load ingest seed: %w", err)
	}
	defer rows.Close()

	out := make([]IngestSeedItem, 0, limit)
	for rows.Next() {
		var row IngestSeedItem
		var published string
		if err := rows.Scan(
			&row.SourceTitle, &row.FeedURL,
			&row.Item.GUID, &row.Item.URL, &row.Item.Title, &row.Item.Summary,
			&row.Item.ContentHTML, &published, &row.Item.WordCount,
		); err != nil {
			return nil, fmt.Errorf("store: scan ingest seed: %w", err)
		}
		row.Item.PublishedAt, _ = time.Parse(time.RFC3339Nano, published)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate ingest seed: %w", err)
	}
	return out, nil
}

func openDiagnosticSource(path string) (*sql.DB, error) {
	db, err := driver.Open("file:"+path+"?mode=ro&_pragma=query_only(1)", fts5.Register)
	if err != nil {
		return nil, fmt.Errorf("store: open diagnostic source: %w", err)
	}
	return db, nil
}
