package reader

import (
	"context"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// SetPrefs has to ask for a re-derivation when — and only when — the Smart+ opt-in moves.
//
// The defect these cover was invisible from every side. Flipping the switch wrote the
// preference correctly, the deriver read it correctly, and the ranking still did not change:
// nothing scheduled a run, so My Feed kept its free-tier order until some unrelated event
// happened to trigger one. Each layer was right and the feature was broken.
func TestSetPrefsAsksForAReRankWhenSmartPlusMoves(t *testing.T) {
	svc, _, sc := testService(t)

	var called []store.Scope
	svc.WithRankPrefHook(func(s store.Scope) { called = append(called, s) })

	if err := svc.SetPrefs(context.Background(), sc,
		map[string]string{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	if len(called) != 1 {
		t.Fatalf("the hook fired %d times, want 1 — the toggle saved and nothing re-ranked", len(called))
	}
	// The scope has to travel: a derivation is per-user, and one fired for the wrong
	// account would rebuild a stranger's ranking and leave this one untouched.
	if called[0].UserID != sc.UserID || called[0].TenantID != sc.TenantID {
		t.Errorf("hook got scope %+v, want %+v", called[0], sc)
	}
}

// Turning it OFF must re-rank too.
//
// The paid ordering is persisted, so leaving it in place after the switch reads off is worse
// than the on-direction of the same bug: the feed still looks like the feature is running,
// which reads as still being billed for it.
func TestSetPrefsReRanksWhenSmartPlusGoesOff(t *testing.T) {
	svc, _, sc := testService(t)

	n := 0
	svc.WithRankPrefHook(func(store.Scope) { n++ })

	if err := svc.SetPrefs(context.Background(), sc,
		map[string]string{SmartPlusPrefKey: "false"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	if n != 1 {
		t.Fatalf("the hook fired %d times on the way off, want 1", n)
	}
}

// Every other preference writes without scheduling anything.
//
// This is the half that keeps the feature affordable. Pane widths, theme and density are
// written constantly — dragging a divider is one write per drag — and a derivation on each
// one would mean an LLM call for every pane resize once Smart+ was on.
func TestSetPrefsIgnoresPreferencesThatDoNotChangeTheRanking(t *testing.T) {
	svc, _, sc := testService(t)

	n := 0
	svc.WithRankPrefHook(func(store.Scope) { n++ })

	if err := svc.SetPrefs(context.Background(), sc, map[string]string{
		"pane.list":  "420",
		"theme":      "midnight",
		"density":    "comfortable",
		"tts.speed":  "1.25",
		"unreadOnly": "true",
	}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	if n != 0 {
		t.Errorf("the hook fired %d times for preferences that do not affect the ranking", n)
	}
}

// A service with no hook installed still writes preferences.
//
// Which is most of them: the hook is nil on every instance without a job pool, and that
// includes the tests above this one and any deployment before the deriver was wired. A nil
// call here would turn "no background jobs" into "cannot save a preference".
func TestSetPrefsWorksWithNoHookInstalled(t *testing.T) {
	svc, repo, sc := testService(t)

	if err := svc.SetPrefs(context.Background(), sc,
		map[string]string{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatalf("SetPrefs with no hook: %v", err)
	}
	got, err := repo.GetPrefs(context.Background(), sc)
	if err != nil {
		t.Fatalf("GetPrefs: %v", err)
	}
	if got[SmartPlusPrefKey] != "true" {
		t.Errorf("the preference did not persist: %q", got[SmartPlusPrefKey])
	}
}
