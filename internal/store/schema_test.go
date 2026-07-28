package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"
)

// Schema-level assertions for TODO 3.1. These are deliberately about *structure*
// rather than about behaviour: they are the things a later migration can break
// silently, where the application keeps working and only one query starts
// returning wrong rows.

// tableNames lists every non-internal table.
func tableNames(t *testing.T, db *DB) []string {
	t.Helper()
	rows, err := db.Read.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type='table'
		   AND name NOT LIKE 'sqlite_%'
		   AND name NOT LIKE '%_data' AND name NOT LIKE '%_idx'
		   AND name NOT LIKE '%_content' AND name NOT LIKE '%_docsize'
		   AND name NOT LIKE '%_config'
		 ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The §6 schema, enumerated. A test that just counts would pass when a table is
// renamed and another added; naming them is what makes a deletion visible.
func TestSchemaHasEverySpecifiedTable(t *testing.T) {
	db := openTest(t)
	have := map[string]bool{}
	for _, n := range tableNames(t, db) {
		have[n] = true
	}

	want := []string{
		// §6.2 core
		"tenants", "users", "sessions", "sources", "subscriptions", "folders",
		"items", "item_revisions", "user_item_state",
		// §6.6 tags
		"tags", "feed_tags", "item_tags",
		// user content (R8)
		"item_notes", "bookmarks", "bookmark_tags", "views",
		// §6.7 identity and sharing
		"roles", "user_roles", "invites", "devices", "api_tokens",
		"recovery_codes", "reset_tokens", "login_attempts", "audit_log",
		"shares", "public_shares",
		// §13, §14
		"rules", "rule_hits", "scrape_rules", "mailboxes",
		// §18 signals and derived interest
		"engagements", "topics", "item_topics", "feed_affinity", "term_affinity",
		"domain_affinity", "outlinks", "recommendations", "item_embeddings", "home_ranking",
		"item_clusters",
		// §12, §17, §22 platform
		"jobs", "settings", "meta", "idempotency_keys",
		"offline_packs", "pack_items", "outbox_conflicts",
		"push_subscriptions", "notification_log", "webhooks",
		"item_translations", "item_audio", "item_archives",
		// search
		"items_fts", "notes_fts", "bookmarks_fts",
		// housekeeping
		"schema_migrations", "user_prefs", "favicons",
	}

	var missing []string
	for _, n := range want {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("missing %d tables: %s", len(missing), strings.Join(missing, ", "))
	}
	if len(want) < 49 {
		t.Errorf("the expectation list itself is only %d tables; §6 specifies ~49", len(want))
	}
}

// §6.1's own rule: every FK-shaped column carries a REFERENCES clause.
//
// The failure this catches is not a crash. A column named user_id with no
// REFERENCES simply never enforces anything, so an orphan row is writable and
// nothing complains until a join quietly returns fewer rows than it should.
func TestForeignKeyShapedColumnsHaveReferences(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Columns that look like foreign keys and deliberately are not. Each
	// exclusion is a decision documented in the migration that introduced it.
	exempt := map[string]map[string]string{
		"engagements": {
			"item_id":    "a signal stays true about an item a restore no longer holds (§6.8)",
			"source_id":  "denormalised for rollups; same argument",
			"session_id": "client-assigned; the server sees one tunnel and never mints these",
		},
		"audit_log": {
			"actor_user_id":     "§7.9 tombstones an actor rather than erasing them",
			"acting_as_user_id": "same",
			"tenant_id":         "survives the tenant it describes",
			"object_id":         "polymorphic: the kind is in object_kind",
		},
		"login_attempts": {
			"tenant_id": "the interesting rows are attempts on accounts that do not exist",
		},
		"shares":           {"object_id": "polymorphic: the kind is in object_kind"},
		"published_scopes": {"target_id": "polymorphic: the kind is in kind, and a share whose folder is deleted publishes nothing rather than dangling"},
		"item_tags":        {"applied_by_rule_id": "kept after the rule is deleted, so the tags stay cleanable"},
		// Same argument as item_tags, and the direction of the surprise matters:
		// a cascade here would silently UNMUTE a backlog the moment a rule was
		// deleted, which is the opposite of what anyone deleting a rule expects.
		"user_item_state": {"muted_by_rule_id": "the rule may be deleted; the mute must stay recoverable by id"},
		// Was "a derivation-local grouping, not a row anywhere" until 0028: the
		// grouping corroborate computes is now item_clusters, and this column
		// holds the head item's id for the row that survived onto the page.
		// Still exempt rather than a declared FK, because this column predates
		// 0028 and SQLite cannot ALTER a foreign key in — the same reason
		// sessions.device_id below is exempt rather than fixed. item_clusters
		// itself carries the honest REFERENCES on the same value.
		"home_ranking": {"cluster_id": "predates item_clusters (0028); the FK-worthy copy of this value lives there"},
		"jobs": {
			"tenant_id": "a job may outlive the tenant it was queued for",
			// A log correlation id, not a reference to a row. Nothing stores
			// requests — there is no requests table and there should not be one,
			// since a table of every request a user made is a browsing history
			// under another name. The id exists only to join log lines.
			"origin_request_id": "the id of the request that queued the job (§22.11); requests are not rows anywhere",
		},
		"settings":         {"scope_id": "polymorphic: system has none, tenant and user differ"},
		"notification_log": {},
		// 0021, §27.3f. Most category assignments name one of the 26 BUILT-INS,
		// which ship in Go (internal/classify/lexicon) and have no row: the
		// `categories` table holds only a reader's delta, so someone who never
		// edited `security` has nothing there to point at. The column therefore
		// holds either a categories.id or a built-in slug.
		//
		// The alternative was seeding 26 rows per user at signup, and it was
		// rejected for the reason the delta table exists at all: those rows would
		// be copies, and a copy freezes that reader's taxonomy at the version they
		// were created from — so a lexicon improvement never reaches them.
		"item_categories": {"category_id": "a categories.id OR a built-in slug; the 26 built-ins ship in code and have no row"},
		// Same column, one level up, plus the reason this table exists at all: a
		// removal must OUTLIVE the label it is about. A reader who deletes a
		// custom category and later recreates one with the same name should not
		// have the old removals silently reattach — but neither should a cascade
		// hand back every label they ever removed the moment a row is tidied.
		// Keeping it unreferenced makes the ledger's independence structural.
		"label_removals": {"label_id": "the instruction outlives the label; a cascade here would re-apply what the reader removed"},
		"devices": {
			"family_id": "a grouping label shared by a token chain, not a row in any table — " +
				"revoking a family is one UPDATE over this column",
		},
		// A real gap rather than a design decision, recorded here so it stays
		// visible: sessions.device_id predates the devices table (0001 vs 0009)
		// and is currently a free-form browser-profile string, not a reference.
		// It should become one when 6.1 makes devices authoritative for refresh
		// families — which needs a table rebuild, since SQLite cannot ALTER a
		// foreign key in. Doing that now would rewrite the live sessions table to
		// serve a table nothing reads yet.
		"sessions": {
			"device_id": "predates devices (0009); becomes a real FK at 6.1 — TODO",
		},
	}

	for _, table := range tableNames(t, db) {
		if strings.HasSuffix(table, "_fts") || table == "schema_migrations" || table == "meta" {
			continue
		}

		cols, err := db.Read.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		var fkShaped []string
		for cols.Next() {
			var name string
			if err := cols.Scan(&name); err != nil {
				t.Fatal(err)
			}
			if strings.HasSuffix(name, "_id") {
				fkShaped = append(fkShaped, name)
			}
		}
		// This is the dangerous direction. Next() stops both when the columns
		// run out and when the query fails, so an unchecked loop hands the
		// assertion below a SHORTER list of columns — and a schema guard that
		// silently examines fewer columns passes while missing exactly the
		// undeclared foreign key it exists to catch.
		if err := cols.Err(); err != nil {
			t.Fatal(err)
		}
		_ = cols.Close()
		if len(fkShaped) == 0 {
			continue
		}

		declared := map[string]bool{}
		fks, err := db.Read.QueryContext(ctx, `SELECT "from" FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for fks.Next() {
			var from string
			if err := fks.Scan(&from); err != nil {
				t.Fatal(err)
			}
			declared[from] = true
		}
		// The other direction, and it fails loudly rather than quietly: a short
		// `declared` set makes the loop below report a foreign key as missing
		// when it is declared. Noisy is better than silent, but neither is the
		// answer the test is supposed to give.
		if err := fks.Err(); err != nil {
			t.Fatal(err)
		}
		_ = fks.Close()

		for _, col := range fkShaped {
			if declared[col] {
				continue
			}
			// The table's own primary key is not a foreign key.
			if col == "id" || col == table+"_id" {
				continue
			}
			if why, ok := exempt[table][col]; ok {
				t.Logf("%s.%s exempt: %s", table, col, why)
				continue
			}
			t.Errorf("%s.%s looks like a foreign key and has no REFERENCES", table, col)
		}
	}
}

// FTS5 external content does not observe writes to its base table on its own.
// Without the triggers the index is correct exactly once, at creation, and then
// drifts silently — searches return yesterday's rows and nothing errors. That is
// the failure mode these two assert against.
func TestNotesSearchIndexTracksWrites(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db)

	repo := NewReaderRepo(db)
	sc, err := repo.FirstUserScope(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, sc, ListQuery{Limit: 1})
	if err != nil || len(items) == 0 {
		t.Fatalf("no seeded item: %v", err)
	}

	if err := repo.SetNote(ctx, sc, items[0].ID, "photosynthesis is remarkable"); err != nil {
		t.Fatal(err)
	}
	if n := ftsCount(t, db, "notes_fts", "photosynthesis"); n != 1 {
		t.Errorf("after insert: %d hits, want 1", n)
	}

	// Update: the old text must stop matching, which is the half a naive
	// delete-then-insert trigger pair gets wrong.
	if err := repo.SetNote(ctx, sc, items[0].ID, "mitochondria instead"); err != nil {
		t.Fatal(err)
	}
	if n := ftsCount(t, db, "notes_fts", "photosynthesis"); n != 0 {
		t.Errorf("after update: %d stale hits, want 0", n)
	}
	if n := ftsCount(t, db, "notes_fts", "mitochondria"); n != 1 {
		t.Errorf("after update: %d hits for the new text, want 1", n)
	}

	// Delete.
	if err := repo.SetNote(ctx, sc, items[0].ID, ""); err != nil {
		t.Fatal(err)
	}
	if n := ftsCount(t, db, "notes_fts", "mitochondria"); n != 0 {
		t.Errorf("after delete: %d stale hits, want 0", n)
	}
}

func TestBookmarksSearchIndexTracksWrites(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	seedOne(t, db)

	repo := NewReaderRepo(db)
	sc, err := repo.FirstUserScope(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Written directly rather than through a repository: 5.6's bookmark methods
	// are not built yet, and the trigger is what is under test.
	exec := func(q string, args ...any) {
		t.Helper()
		if err := db.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, q, args...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO bookmarks (id, tenant_id, user_id, url, url_norm, title, description,
	        created_at, updated_at)
	      VALUES ('bm1', ?, ?, 'https://x.tld/a', 'https://x.tld/a', 'Zeppelins explained',
	              'a long read about airships', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		sc.TenantID, sc.UserID)

	if n := ftsCount(t, db, "bookmarks_fts", "zeppelins"); n != 1 {
		t.Errorf("after insert: %d hits, want 1", n)
	}

	exec(`UPDATE bookmarks SET title = 'Submarines explained' WHERE id = 'bm1'`)
	if n := ftsCount(t, db, "bookmarks_fts", "zeppelins"); n != 0 {
		t.Errorf("after update: %d stale hits, want 0", n)
	}
	if n := ftsCount(t, db, "bookmarks_fts", "submarines"); n != 1 {
		t.Errorf("after update: %d hits for the new title, want 1", n)
	}

	exec(`DELETE FROM bookmarks WHERE id = 'bm1'`)
	if n := ftsCount(t, db, "bookmarks_fts", "submarines"); n != 0 {
		t.Errorf("after delete: %d stale hits, want 0", n)
	}
}

func ftsCount(t *testing.T, db *DB, table, term string) int {
	t.Helper()
	var n int
	err := db.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE `+table+` MATCH ?`, term).Scan(&n)
	if err != nil {
		t.Fatalf("%s MATCH %q: %v", table, term, err)
	}
	return n
}
