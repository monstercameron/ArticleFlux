package store

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/rules"
)

func seedRulesRepo(t *testing.T) (*ReaderRepo, Scope, *DB) {
	t.Helper()
	db := openTest(t)
	repo := NewReaderRepo(db)
	ctx := t.Context()
	if err := repo.CreateTenantAndUser(ctx, NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "alice",
		Hash: "h", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	return repo, Scope{TenantID: "t1", UserID: "u1", Role: "member"}, db
}

func sampleRule(name string) rules.Rule {
	return rules.Rule{
		Name: name, Enabled: true,
		Match: rules.Match{Conditions: []rules.Condition{
			{Field: rules.FieldTitle, Op: rules.OpContains, Value: "foo"},
		}},
		Actions: []rules.Action{{Kind: rules.ActionMarkRead}},
	}
}

func TestCreateRuleValidatesBeforeStoring(t *testing.T) {
	repo, sc, _ := seedRulesRepo(t)
	bad := rules.Rule{Name: "", Match: rules.Match{}, Actions: nil}
	if _, err := repo.CreateRule(t.Context(), sc, bad); err == nil {
		t.Error("an invalid rule (no name, no conditions, no actions) was accepted")
	}
}

func TestCreateRuleDefaultsPositionToEndOfList(t *testing.T) {
	repo, sc, _ := seedRulesRepo(t)
	ctx := t.Context()
	id1, err := repo.CreateRule(ctx, sc, sampleRule("first"))
	if err != nil {
		t.Fatal(err)
	}
	id2, err := repo.CreateRule(ctx, sc, sampleRule("second"))
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := repo.ListRules(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].ID != id1 || out[1].ID != id2 {
		t.Errorf("order = %v, want [first, second] by insertion position", out)
	}
	if out[0].Position >= out[1].Position {
		t.Errorf("positions = %d, %d — want strictly increasing", out[0].Position, out[1].Position)
	}
}

// decodeRule's error path: ListRules surfaces a corrupt row (via Unreadable)
// rather than dropping it — the rules screen has to be where a broken rule is
// visible and deletable.
func TestListRulesSurfacesUnreadableRowsInsteadOfHidingThem(t *testing.T) {
	repo, sc, db := seedRulesRepo(t)
	ctx := t.Context()
	id, err := repo.CreateRule(ctx, sc, sampleRule("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE rules SET match_json = 'not json' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	out, stats, err := repo.ListRules(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(stats) != 1 {
		t.Fatalf("got %d rules / %d stats, want 1 each (the corrupt row is still listed)", len(out), len(stats))
	}
	if stats[0].Unreadable == "" {
		t.Error("a rule with unparsable match_json was not flagged Unreadable")
	}
}

// RulesFor (fanout's loader), unlike ListRules, silently skips a corrupt row:
// one bad rule must not stop every other rule in the set from evaluating.
func TestRulesForSkipsUnreadableRowsSilently(t *testing.T) {
	repo, sc, db := seedRulesRepo(t)
	ctx := t.Context()
	if _, err := repo.CreateRule(ctx, sc, sampleRule("good")); err != nil {
		t.Fatal(err)
	}
	badID, err := repo.CreateRule(ctx, sc, sampleRule("bad"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE rules SET actions_json = 'not json' WHERE id = ?`, badID); err != nil {
		t.Fatal(err)
	}
	out, err := repo.RulesFor(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Name != "good" {
		t.Errorf("RulesFor = %v, want only the good rule", out)
	}
}

func TestDeleteRuleCascadesHitsButNotItemTags(t *testing.T) {
	repo, sc, db := seedRulesRepo(t)
	ctx := t.Context()
	id, err := repo.CreateRule(ctx, sc, sampleRule("to-delete"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO sources (id,natural_key,feed_url,created_at) VALUES ('src1','feed:a','https://a.example/',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO items (id,source_id,guid,title,published_at,first_seen_at) VALUES ('i1','src1','g1','T',?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO rule_hits (id,rule_id,item_id,user_id,actions_json,at) VALUES ('h1',?,'i1','u1','[]',?)`, id, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO tags (id,tenant_id,user_id,name,created_at) VALUES ('tag1','t1','u1','x',?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO item_tags (tenant_id,user_id,tag_id,item_id,applied_by_rule_id,added_at) VALUES ('t1','u1','tag1','i1',?,?)`, id, now); err != nil {
		t.Fatal(err)
	}

	if err := repo.DeleteRule(ctx, sc, id); err != nil {
		t.Fatal(err)
	}

	var hits int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM rule_hits WHERE rule_id = ?`, id).Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 0 {
		t.Errorf("%d rule_hits remain after deleting the rule, want 0 (cascade)", hits)
	}
	var tagName string
	if err := db.Read.QueryRowContext(ctx, `SELECT applied_by_rule_id FROM item_tags WHERE item_id='i1'`).Scan(&tagName); err != nil {
		t.Fatal(err)
	}
	if tagName != id {
		t.Errorf("the applied tag's applied_by_rule_id changed to %q; item_tags must survive rule deletion untouched", tagName)
	}
}

func TestDeleteRuleNotFoundForAnotherTenantsRule(t *testing.T) {
	repo, sc, _ := seedRulesRepo(t)
	ctx := t.Context()
	id, err := repo.CreateRule(ctx, sc, sampleRule("mine"))
	if err != nil {
		t.Fatal(err)
	}
	other := Scope{TenantID: "t2", UserID: "u2", Role: "member"}
	if err := repo.DeleteRule(ctx, other, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting another tenant's rule = %v, want ErrNotFound", err)
	}
}

func TestSetRuleEnabledTogglesWithoutLosingTheRule(t *testing.T) {
	repo, sc, _ := seedRulesRepo(t)
	ctx := t.Context()
	id, err := repo.CreateRule(ctx, sc, sampleRule("toggle-me"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetRuleEnabled(ctx, sc, id, false); err != nil {
		t.Fatal(err)
	}
	out, _, err := repo.ListRules(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Enabled {
		t.Errorf("rule after disabling = %+v", out)
	}
	if err := repo.SetRuleEnabled(ctx, sc, "nonexistent", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("enabling an unknown rule = %v, want ErrNotFound", err)
	}
}

func TestScopedRuleMethodsRequireAValidScope(t *testing.T) {
	repo, _, _ := seedRulesRepo(t)
	ctx := t.Context()
	if _, err := repo.CreateRule(ctx, Scope{}, sampleRule("x")); !errors.Is(err, ErrNoScope) {
		t.Errorf("CreateRule unscoped = %v, want ErrNoScope", err)
	}
	if _, _, err := repo.ListRules(ctx, Scope{}); !errors.Is(err, ErrNoScope) {
		t.Errorf("ListRules unscoped = %v, want ErrNoScope", err)
	}
	if err := repo.DeleteRule(ctx, Scope{}, "x"); !errors.Is(err, ErrNoScope) {
		t.Errorf("DeleteRule unscoped = %v, want ErrNoScope", err)
	}
	if err := repo.SetRuleEnabled(ctx, Scope{}, "x", true); !errors.Is(err, ErrNoScope) {
		t.Errorf("SetRuleEnabled unscoped = %v, want ErrNoScope", err)
	}
}
