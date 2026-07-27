package smart

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// interest.go had no test file at all before this one. Everything here is
// deliberately restricted to paths that return BEFORE llm.Client.Do is called:
// Interest holds a concrete *llm.Client with no exported way to swap its
// transport from this package (that seam only exists inside internal/llm
// itself, white-box, the way tts_test.go and the new llm_test.go use it). A
// test here that supplied a non-empty key and reached Do() would be a real
// network call to api.openai.com — exactly what this task forbids — so every
// case below uses an UNCONFIGURED client. llm.Client.Do refuses on its own the
// moment the key is empty, which is the second line of defence if the guards
// tested here were ever removed.
//
// What is NOT covered here, and why: the id-remapping and the caps in
// RerankCandidates/ExtractEntities (one-based→zero-based, MaxEntityTitles
// truncation) only run once Do() has already returned, so exercising them
// would require either a real call or a transport seam this package does not
// have. Left untested rather than faked into false confidence.

func unconfigured() *llm.Client {
	return llm.New(func(context.Context) string { return "" })
}

func newSettings(t *testing.T) *store.SettingsRepo {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store.NewSettingsRepo(db, nil)
}

// --- RerankCandidates guards --------------------------------------------------

func TestRerankCandidatesRejectsNoCandidates(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	if _, err := in.RerankCandidates(context.Background(), nil, derive.ProfileHint{}, 5); err == nil {
		t.Fatal("no candidates was accepted")
	}
}

// The key gate is checked before anything about the candidates is touched, so
// an instance with no key never even builds the payload.
func TestRerankCandidatesRequiresAConfiguredClient(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	cands := []derive.Candidate{{Title: "A"}, {Title: "B"}}
	_, err := in.RerankCandidates(context.Background(), cands, derive.ProfileHint{}, 5)
	if err == nil {
		t.Fatal("an unconfigured client attempted a rerank")
	}
	if !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want it to name the missing key", err)
	}
}

// --- ExtractEntities guards ----------------------------------------------------

func TestExtractEntitiesRejectsNoTitles(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	if _, err := in.ExtractEntities(context.Background(), nil); err == nil {
		t.Fatal("no titles was accepted")
	}
}

func TestExtractEntitiesRequiresAConfiguredClient(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	_, err := in.ExtractEntities(context.Background(), []string{"npm", "curl"})
	if err == nil {
		t.Fatal("an unconfigured client attempted extraction")
	}
	if !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want it to name the missing key", err)
	}
}

// --- LabelTopic guards ----------------------------------------------------------

func TestLabelTopicRejectsNoTerms(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	if _, err := in.LabelTopic(context.Background(), nil, "fallback"); err == nil {
		t.Fatal("no terms was accepted")
	}
}

func TestLabelTopicRequiresAConfiguredClient(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	_, err := in.LabelTopic(context.Background(), []string{"sqlite", "btree"}, "Databases")
	if err == nil {
		t.Fatal("an unconfigured client attempted labelling")
	}
	if !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want it to name the missing key", err)
	}
}

// --- model resolution (pure, no llm access at all) ---------------------------

// model() is read before the key gate would even matter in a real call, and it
// is exercised directly here since every public method's Configured() check
// would otherwise stand in the way of testing it without a network seam.
func TestInterestModelWithNilSettingsReturnsEmptyDefault(t *testing.T) {
	in := NewInterest(unconfigured(), nil)
	got, err := in.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "" {
		t.Errorf("model = %q with nil settings, want empty (caller falls back to llm.DefaultModel)", got)
	}
}

func TestInterestModelReadsTheConfiguredSetting(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), store.KeySmartModel, "gpt-5", ""); err != nil {
		t.Fatalf("seeding the model setting: %v", err)
	}
	in := NewInterest(unconfigured(), settings)
	got, err := in.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if got != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", got)
	}
}

// A setting that was never written must not surface store.ErrNoSetting as a
// hard failure — every caller here falls back to the built-in default, and an
// instance that never touched the Smart+ model dropdown is the common case,
// not an error condition.
func TestInterestModelWithUnsetSettingFallsBackSilently(t *testing.T) {
	settings := newSettings(t)
	in := NewInterest(unconfigured(), settings)
	got, err := in.model(context.Background())
	if err != nil {
		t.Fatalf("model: %v, want nil error even though the setting was never written", err)
	}
	if got != "" {
		t.Errorf("model = %q, want empty", got)
	}
}
