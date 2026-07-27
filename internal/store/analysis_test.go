package store

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"
)

// seedAnalysisItems inserts n bare items directly (no source/subscription
// ceremony needed — item_analysis only ever references items.id) and returns
// their ids in insertion order, oldest first.
func seedAnalysisItems(t *testing.T, db *DB, n int, basePublished time.Time) []string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES ('asrc','feed:asrc','https://a.example/feed',?)`,
		now); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := "aitem" + strconv.Itoa(i)
		ids[i] = id
		published := basePublished.Add(time.Duration(i) * time.Hour).UTC().Format(time.RFC3339Nano)
		if _, err := db.Write.ExecContext(ctx, `
			INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at)
			VALUES (?,'asrc',?,?,?,?)`,
			id, id, "title-"+id, published, now); err != nil {
			t.Fatalf("seed item %s: %v", id, err)
		}
	}
	return ids
}

func TestUpsertAnalysisRoundTrip(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	analyzedAt := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	llmAt := time.Date(2026, 7, 20, 10, 5, 0, 0, time.UTC)
	want := ItemAnalysis{
		ItemID:          ids[0],
		AnalyzerVersion: 3,
		LexiconHash:     "abc123",
		Lang:            "en",
		Genre:           "news",
		CategoryScores:  map[string]float64{"security": 4.5, "software": 1.2},
		Keyphrases:      []string{"zero day", "patch notes"},
		Entities: []AnalysisEntity{
			{Name: "android auto", Label: "Android Auto"},
			{Name: "openai", Label: "OpenAI"},
		},
		Abstract:   "A short summary of the piece.",
		Vector:     map[string]float64{"kubernetes": 0.8, "cluster": 0.3},
		AnalyzedAt: analyzedAt,
		LLMAt:      llmAt,
		LLMModel:   "gpt-5-mini",
	}

	repo := NewReaderRepo(db)
	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{want}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	got, err := repo.AnalysisByIDs(ctx, []string{ids[0]})
	if err != nil {
		t.Fatalf("AnalysisByIDs: %v", err)
	}
	row, ok := got[ids[0]]
	if !ok {
		t.Fatalf("row missing from AnalysisByIDs result")
	}

	if row.ItemID != want.ItemID || row.AnalyzerVersion != want.AnalyzerVersion ||
		row.LexiconHash != want.LexiconHash || row.Lang != want.Lang || row.Genre != want.Genre ||
		row.Abstract != want.Abstract || row.LLMModel != want.LLMModel {
		t.Errorf("scalar fields = %+v, want %+v", row, want)
	}
	if !reflect.DeepEqual(row.CategoryScores, want.CategoryScores) {
		t.Errorf("CategoryScores = %v, want %v", row.CategoryScores, want.CategoryScores)
	}
	if !reflect.DeepEqual(row.Keyphrases, want.Keyphrases) {
		t.Errorf("Keyphrases = %v, want %v", row.Keyphrases, want.Keyphrases)
	}
	if !reflect.DeepEqual(row.Entities, want.Entities) {
		t.Errorf("Entities = %v, want %v", row.Entities, want.Entities)
	}
	if !reflect.DeepEqual(row.Vector, want.Vector) {
		t.Errorf("Vector = %v, want %v", row.Vector, want.Vector)
	}
	if !row.AnalyzedAt.Equal(want.AnalyzedAt) {
		t.Errorf("AnalyzedAt = %v, want %v", row.AnalyzedAt, want.AnalyzedAt)
	}
	if !row.LLMAt.Equal(want.LLMAt) {
		t.Errorf("LLMAt = %v, want %v", row.LLMAt, want.LLMAt)
	}
}

// The empty cases: nil map, nil slice, empty string, zero LLMAt must round-trip
// as SQL NULL — not as the literal string "null" — and read back as nil/zero,
// not as a value that merely looks empty.
func TestUpsertAnalysisEmptyFieldsAreSQLNull(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())

	row := ItemAnalysis{
		ItemID:          ids[0],
		AnalyzerVersion: 1,
		LexiconHash:     "h1",
		// Lang, Genre, Abstract, LLMModel: zero value "".
		// CategoryScores, Keyphrases, Entities, Vector: nil.
		AnalyzedAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
		// LLMAt: zero value.
	}
	repo := NewReaderRepo(db)
	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{row}); err != nil {
		t.Fatalf("UpsertAnalysis: %v", err)
	}

	// Assert the columns are SQL NULL directly, so a bug that stores the string
	// "null" (which is NOT NULL) cannot hide behind a lenient Go-side read.
	var lang, genre, keyphrases, entities, abstract, vector, llmAt, llmModel any
	err := db.Read.QueryRowContext(ctx, `
		SELECT lang, genre, keyphrases, entities, abstract, vector, llm_at, llm_model
		  FROM item_analysis WHERE item_id = ?`, ids[0]).
		Scan(&lang, &genre, &keyphrases, &entities, &abstract, &vector, &llmAt, &llmModel)
	if err != nil {
		t.Fatalf("raw scan: %v", err)
	}
	for name, v := range map[string]any{
		"lang": lang, "genre": genre, "keyphrases": keyphrases, "entities": entities,
		"abstract": abstract, "vector": vector, "llm_at": llmAt, "llm_model": llmModel,
	} {
		if v != nil {
			t.Errorf("%s = %#v (%T), want SQL NULL", name, v, v)
		}
	}
	// category_scores is NOT NULL by schema (§27.7): a nil map still has to
	// produce a valid JSON object, not the string "null".
	var scores string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT category_scores FROM item_analysis WHERE item_id = ?`, ids[0]).Scan(&scores); err != nil {
		t.Fatalf("raw scan category_scores: %v", err)
	}
	if scores != "{}" {
		t.Errorf("category_scores = %q, want %q", scores, "{}")
	}

	got, err := repo.AnalysisByIDs(ctx, []string{ids[0]})
	if err != nil {
		t.Fatalf("AnalysisByIDs: %v", err)
	}
	back := got[ids[0]]
	if back.Lang != "" || back.Genre != "" || back.Abstract != "" || back.LLMModel != "" {
		t.Errorf("string fields not empty: %+v", back)
	}
	if back.Keyphrases != nil {
		t.Errorf("Keyphrases = %#v, want nil", back.Keyphrases)
	}
	if back.Entities != nil {
		t.Errorf("Entities = %#v, want nil", back.Entities)
	}
	if back.Vector != nil {
		t.Errorf("Vector = %#v, want nil", back.Vector)
	}
	if !back.LLMAt.IsZero() {
		t.Errorf("LLMAt = %v, want zero", back.LLMAt)
	}
	if len(back.CategoryScores) != 0 {
		t.Errorf("CategoryScores = %v, want empty", back.CategoryScores)
	}
}

func TestUpsertAnalysisIsAnUpsert(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 1, time.Now().UTC())
	repo := NewReaderRepo(db)

	first := ItemAnalysis{
		ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h1",
		CategoryScores: map[string]float64{"security": 1},
		AnalyzedAt:     time.Now().UTC(),
	}
	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{first}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	second := ItemAnalysis{
		ItemID: ids[0], AnalyzerVersion: 2, LexiconHash: "h2",
		CategoryScores: map[string]float64{"software": 9},
		Genre:          "opinion",
		AnalyzedAt:     time.Now().UTC(),
	}
	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{second}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM item_analysis WHERE item_id = ?`, ids[0]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}

	got, err := repo.AnalysisByIDs(ctx, []string{ids[0]})
	if err != nil {
		t.Fatal(err)
	}
	row := got[ids[0]]
	if row.AnalyzerVersion != 2 || row.LexiconHash != "h2" || row.Genre != "opinion" {
		t.Errorf("row = %+v, want the second upsert's values", row)
	}
	if _, ok := row.CategoryScores["security"]; ok {
		t.Error("the first upsert's category score survived the second")
	}
	if row.CategoryScores["software"] != 9 {
		t.Errorf("CategoryScores[software] = %v, want 9", row.CategoryScores["software"])
	}
}

func TestAnalysisByIDsMixedPresence(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 2, time.Now().UTC())
	repo := NewReaderRepo(db)

	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{{
		ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h1",
		CategoryScores: map[string]float64{"security": 1},
		AnalyzedAt:     time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	// ids[1] never gets a row. "definitely-not-an-item" is not even a real item.
	got, err := repo.AnalysisByIDs(ctx, []string{ids[0], ids[1], "definitely-not-an-item"})
	if err != nil {
		t.Fatalf("AnalysisByIDs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %v", len(got), got)
	}
	if _, ok := got[ids[0]]; !ok {
		t.Errorf("missing the one row that exists")
	}
}

func TestStaleAnalysisFindsMissingAndOutOfDateRows(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := seedAnalysisItems(t, db, 4, base)
	repo := NewReaderRepo(db)
	// ids[0]: no row at all — published oldest.
	// ids[1]: current version and lexicon — must NOT come back.
	// ids[2]: wrong analyzer_version — published newer than ids[0]/[1].
	// ids[3]: wrong lexicon_hash — published newest of all.
	const wantVersion = 5
	const wantLexicon = "current-hash"

	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{
		{ItemID: ids[1], AnalyzerVersion: wantVersion, LexiconHash: wantLexicon,
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC()},
		{ItemID: ids[2], AnalyzerVersion: wantVersion - 1, LexiconHash: wantLexicon,
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC()},
		{ItemID: ids[3], AnalyzerVersion: wantVersion, LexiconHash: "stale-hash",
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}

	stale, err := repo.StaleAnalysis(ctx, wantVersion, wantLexicon, 100)
	if err != nil {
		t.Fatalf("StaleAnalysis: %v", err)
	}

	want := []string{ids[3], ids[2], ids[0]} // newest published_at first
	if !reflect.DeepEqual(stale, want) {
		t.Errorf("StaleAnalysis = %v, want %v", stale, want)
	}
}

func TestClearAnalysisWithIDsAndWithout(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 3, time.Now().UTC())
	repo := NewReaderRepo(db)

	var rows []ItemAnalysis
	for _, id := range ids {
		rows = append(rows, ItemAnalysis{
			ItemID: id, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC(),
		})
	}
	if err := repo.UpsertAnalysis(ctx, rows); err != nil {
		t.Fatal(err)
	}

	n, err := repo.ClearAnalysis(ctx, []string{ids[0], ids[1]})
	if err != nil {
		t.Fatalf("ClearAnalysis(ids): %v", err)
	}
	if n != 2 {
		t.Errorf("ClearAnalysis(ids) = %d, want 2", n)
	}
	remaining, err := repo.AnalysisByIDs(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("remaining = %v, want exactly ids[2]", remaining)
	}

	n, err = repo.ClearAnalysis(ctx, nil)
	if err != nil {
		t.Fatalf("ClearAnalysis(nil): %v", err)
	}
	if n != 1 {
		t.Errorf("ClearAnalysis(nil) = %d, want 1", n)
	}
	var count int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM item_analysis`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("item_analysis still has %d rows after clearing all", count)
	}
}

func TestPendingSmartPlusOnlyNullLLMAt(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 3, time.Now().UTC())
	repo := NewReaderRepo(db)

	now := time.Now().UTC()
	if err := repo.UpsertAnalysis(ctx, []ItemAnalysis{
		{ItemID: ids[0], AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{}, AnalyzedAt: now},
		{ItemID: ids[1], AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{}, AnalyzedAt: now.Add(time.Minute),
			LLMAt: now, LLMModel: "gpt-5-mini"},
		{ItemID: ids[2], AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{}, AnalyzedAt: now.Add(2 * time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := repo.PendingSmartPlus(ctx, 100)
	if err != nil {
		t.Fatalf("PendingSmartPlus: %v", err)
	}
	want := []string{ids[2], ids[0]} // newest analyzed_at first, ids[1] excluded
	if !reflect.DeepEqual(pending, want) {
		t.Errorf("PendingSmartPlus = %v, want %v", pending, want)
	}
}

func TestUpsertAnalysisBatchOfTwoHundred(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ids := seedAnalysisItems(t, db, 200, time.Now().UTC())
	repo := NewReaderRepo(db)

	rows := make([]ItemAnalysis, len(ids))
	for i, id := range ids {
		rows[i] = ItemAnalysis{
			ItemID: id, AnalyzerVersion: 1, LexiconHash: "h",
			CategoryScores: map[string]float64{"security": float64(i)},
			AnalyzedAt:     time.Now().UTC(),
		}
	}
	if err := repo.UpsertAnalysis(ctx, rows); err != nil {
		t.Fatalf("batch upsert: %v", err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM item_analysis`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Errorf("item_analysis has %d rows, want 200", n)
	}
}

func TestUpsertAnalysisRejectsEmptyItemID(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	repo := NewReaderRepo(db)

	err := repo.UpsertAnalysis(ctx, []ItemAnalysis{{
		AnalyzerVersion: 1, LexiconHash: "h",
		CategoryScores: map[string]float64{}, AnalyzedAt: time.Now().UTC(),
	}})
	if err == nil {
		t.Fatal("expected an error for a row with no item id")
	}
}

func TestUpsertAnalysisEmptyBatchIsANoOp(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	repo := NewReaderRepo(db)

	if err := repo.UpsertAnalysis(ctx, nil); err != nil {
		t.Fatalf("UpsertAnalysis(nil) = %v, want nil", err)
	}
}
