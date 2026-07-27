package store

import (
	"context"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// The property the steer dial lives or dies on (0027): a correction survives the
// derivation that rewrites everything around it.
//
// Both tables are replaced wholesale on every pass — that is the design, and it
// is what makes the derived state disposable. `steer` is the exception, for the
// same reason `suppressed` is, and an exception that only holds until the next
// poll is the same as not having the control: fifteen minutes after a reader
// tells the model it has misread them, it would have forgotten.

func TestTopicSteerSurvivesARebuild(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))

	build := func() []TopicRow {
		t.Helper()
		if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
			{TopTerms: []string{"schema", "migration", "index"}, Label: "Migrations",
				Members: []string{}, MemberScores: map[string]float64{}, Trend: topics.TrendSteady},
			{TopTerms: []string{"orbit", "station", "downlink"}, Label: "Orbits",
				Members: []string{}, MemberScores: map[string]float64{}, Trend: topics.TrendSteady},
		}); err != nil {
			t.Fatalf("ReplaceTopics: %v", err)
		}
		rows, err := repo.Topics(ctx, sc)
		if err != nil {
			t.Fatalf("Topics: %v", err)
		}
		return rows
	}

	rows := build()
	if len(rows) != 2 {
		t.Fatalf("got %d topics, want 2", len(rows))
	}
	// Every fresh row starts at the default, and it is 1.0 rather than 0 — a
	// zero here would mute the topic the migration ran on.
	for _, r := range rows {
		if r.Steer != SteerNormal {
			t.Fatalf("fresh topic %q has steer %v, want %v", r.Label, r.Steer, SteerNormal)
		}
	}

	var less, never string
	for _, r := range rows {
		switch r.Label {
		case "Migrations":
			less = r.ID
		case "Orbits":
			never = r.ID
		}
	}
	if err := repo.SteerTopic(ctx, sc, less, SteerLess, false); err != nil {
		t.Fatalf("SteerTopic less: %v", err)
	}
	if err := repo.SteerTopic(ctx, sc, never, SteerNormal, true); err != nil {
		t.Fatalf("SteerTopic never: %v", err)
	}

	// The rebuild. Ids are regenerated inside ReplaceTopics, so this is also the
	// assertion that the fingerprint — not the id — is what carries a correction
	// across.
	for _, r := range build() {
		switch r.Label {
		case "Migrations":
			if r.Steer != SteerLess || r.Suppressed {
				t.Errorf("after rebuild: %q steer=%v suppressed=%v, want %v/false",
					r.Label, r.Steer, r.Suppressed, SteerLess)
			}
		case "Orbits":
			if !r.Suppressed {
				t.Errorf("after rebuild: %q lost its suppression", r.Label)
			}
		}
	}
}

func TestEntitySteerSurvivesARebuildAndNeverStaysVisible(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))

	rebuild := func() {
		t.Helper()
		if err := repo.ReplaceEntityAffinity(ctx, sc, []Entity{
			{Name: "pro max", Label: "Pro Max", Kind: EntityPhrase, Weight: 37, Mentions: 2},
			{Name: "apple watch", Label: "Apple Watch", Kind: EntityPhrase, Weight: 1.8, Mentions: 2},
		}); err != nil {
			t.Fatalf("ReplaceEntityAffinity: %v", err)
		}
	}
	rebuild()

	if err := repo.SteerEntity(ctx, sc, "pro max", SteerNormal, true); err != nil {
		t.Fatalf("SteerEntity: %v", err)
	}
	if err := repo.SteerEntity(ctx, sc, "apple watch", SteerLess, false); err != nil {
		t.Fatalf("SteerEntity: %v", err)
	}
	rebuild()

	// The RANKING must not see the struck-out one. This is the half that changes
	// what the reader gets.
	followed, err := repo.EntityAffinity(ctx, sc, 50)
	if err != nil {
		t.Fatalf("EntityAffinity: %v", err)
	}
	for _, e := range followed {
		if e.Name == "pro max" {
			t.Error("a suppressed entity is still being offered to the ranker")
		}
		if e.Name == "apple watch" && e.Steer != SteerLess {
			t.Errorf("apple watch steer = %v, want %v", e.Steer, SteerLess)
		}
	}

	// The SCREEN must still see it, or the correction cannot be undone — which
	// is the difference between a control and a trapdoor.
	all, err := repo.AllEntities(ctx, sc, 50)
	if err != nil {
		t.Fatalf("AllEntities: %v", err)
	}
	var found bool
	for _, e := range all {
		if e.Name == "pro max" {
			found = true
			if !e.Suppressed {
				t.Error("AllEntities lost the suppression flag")
			}
		}
	}
	if !found {
		t.Fatal("AllEntities hid the row the reader struck out; nothing can bring it back")
	}
}

// A dial outside the range is refused before SQLite has to refuse it, and a zero
// — which is what every row written before 0027 reads back as — means the
// default rather than silence.
func TestSteerIsClamped(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{0, SteerNormal},
		{-3, SteerNormal},
		{0.01, SteerMin},
		{99, SteerMax},
		{SteerLess, SteerLess},
		{SteerMore, SteerMore},
	} {
		if got := clampSteer(tc.in); got != tc.want {
			t.Errorf("clampSteer(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// Steering something that is not this reader's is ErrNotFound rather than a
// silent no-op, so the transport can answer honestly instead of reporting a save
// that never happened.
func TestSteeringSomethingThatIsNotThereIsNotFound(t *testing.T) {
	ctx := context.Background()
	repo, sc := seedReader(t, openTest(t))

	if err := repo.SteerTopic(ctx, sc, "topic-that-never-existed", SteerLess, false); err != ErrNotFound {
		t.Errorf("SteerTopic on a missing row = %v, want ErrNotFound", err)
	}
	if err := repo.SteerEntity(ctx, sc, "nothing at all", SteerLess, false); err != ErrNotFound {
		t.Errorf("SteerEntity on a missing row = %v, want ErrNotFound", err)
	}
}
