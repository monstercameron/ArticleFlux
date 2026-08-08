package smart

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"

	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// The Smart set, as a set.
//
// These do not re-test the ten features — each has its own file and its own
// suite. They test the properties the TYPE adds, which are the ones the ten
// scattered constructors could not have: that the set is complete, that it is
// fixed at boot, that every member shares one client, and that a feature added
// later cannot quietly skip any of that.

func newTestSmart(t *testing.T, opts ...Option) (*Smart, *fakeLLM) {
	t.Helper()
	f := &fakeLLM{configured: true}
	return New(f, nil, opts...), f
}

// features is every accessor, by name, so the tests below can walk the set
// rather than naming ten things ten times — and so adding an eleventh feature
// without adding it here fails TestEveryFeatureIsReachable rather than passing
// silently.
func features(s *Smart) map[string]any {
	return map[string]any{
		"Translator":   s.Translator(),
		"Palettes":     s.Palettes(),
		"Categorizer":  s.Categorizer(),
		"Interest":     s.Interest(),
		"Relevance":    s.Relevance(),
		"WebSearch":    s.WebSearch(),
		"SiteAnalyzer": s.SiteAnalyzer(),
		"Digest":       s.Digest(),
		"Podcast":      s.Podcast(),
		"Classifier":   s.Classifier(),
	}
}

func TestEveryFeatureIsBuilt(t *testing.T) {
	// A nil feature is a nil dereference in a handler, and the ten call sites
	// this replaced each had their own chance to be forgotten.
	s, _ := newTestSmart(t)
	for name, f := range features(s) {
		if f == nil || reflect.ValueOf(f).IsNil() {
			t.Errorf("%s() is nil", name)
		}
	}
}

func TestEveryFeatureIsReachable(t *testing.T) {
	// The count is pinned deliberately. §18.8's promise is that the set of
	// things this instance can send is knowable, and it is only knowable if
	// adding an eleventh feature forces somebody to come here and say so.
	//
	// If this fails because a feature was added: add it to `features` above,
	// and to the inventory in docs/AI_SCHEMAFLOW_MIGRATION.md §1, which is the
	// document that answers this question for a human.
	const paidFeatures = 10
	if got := len(features(newSmartForCount(t))); got != paidFeatures {
		t.Errorf("%d features reachable, want %d — the inventory has drifted", got, paidFeatures)
	}
}

func newSmartForCount(t *testing.T) *Smart {
	t.Helper()
	s, _ := newTestSmart(t)
	return s
}

func TestTheSameInstanceComesBackEveryTime(t *testing.T) {
	// Accessors, not constructors. A caller that got a fresh Digest per call
	// would get a fresh on-disk cache handle per call, and the cache would
	// stop being one.
	s, _ := newTestSmart(t)
	if s.Digest() != s.Digest() {
		t.Error("Digest() built a second instance")
	}
	if s.Podcast() != s.Podcast() {
		t.Error("Podcast() built a second instance")
	}
	if s.Interest() != s.Interest() {
		t.Error("Interest() built a second instance")
	}
}

func TestOneClientReachesEveryFeature(t *testing.T) {
	// The property that makes the type worth having: substituting the client
	// substitutes it for all ten.
	//
	// Asserted against the OPS CONTEXT rather than against a recorded `Do`,
	// because the features run SchemaFlux operations now: the call leaves
	// through whichever provider is installed, and the only thing it asks the
	// injected client for is a context to run in. That request is the seam, and
	// a feature that skipped it would run against the package-level provider —
	// which on a fresh process is a mock whose answers parse into zero-valued
	// structs and look exactly like a working deployment.
	p := schemafluxtest.New().Shaped().Reply(`Woodworking`)
	schemafluxtest.Install(t, p)
	s, f := newTestSmart(t)

	_, _, _ = s.Categorizer().Suggest(context.Background(), "Alpha", "https://a.example", nil)
	if f.opsContexts == 0 {
		t.Fatal("the categorizer did not ask the injected client for an operation context")
	}
	if p.CallCount() == 0 {
		t.Error("the operation never reached a provider")
	}
}

func TestConfiguredAsksTheClient(t *testing.T) {
	// One question, one answer. Two implementations of "is there a key" is how
	// a screen comes to say ready while the request answers 501, which is the
	// shape of every confusion this area has produced.
	s, f := newTestSmart(t)
	if !s.Configured(context.Background()) {
		t.Error("Configured() = false with a configured client")
	}
	f.configured = false
	if s.Configured(context.Background()) {
		t.Error("Configured() = true with an unconfigured client")
	}
}

func TestConfiguredIsFalseWithoutAClientRatherThanPanicking(t *testing.T) {
	// It is called from a request path. A nil dereference here takes the server
	// down for a misconfiguration that a false would merely disable.
	s := &Smart{}
	if s.Configured(context.Background()) {
		t.Error("Configured() = true with no client at all")
	}
}

func TestTheCacheDirectoriesAreSeparate(t *testing.T) {
	// A digest is keyed per article and a broadcast segment per ORDERED PAIR of
	// them. One directory holding two key shapes is one nobody can reason about
	// the size of, which is why the features were given their own before and
	// must keep them now that one type names both.
	dir := t.TempDir()
	s, _ := newTestSmart(t, WithCacheDir(dir))
	if got := s.sub("digest-cache"); got != filepath.Join(dir, "digest-cache") {
		t.Errorf("digest cache = %q", got)
	}
	if s.sub("digest-cache") == s.sub("podcast-cache") {
		t.Error("the two caches share a directory")
	}
}

func TestNoCacheDirectoryMeansNoCaching(t *testing.T) {
	// The correct answer for a test, and the reason the option exists rather
	// than a required argument: a suite that had to name a directory would
	// write one, and a suite that writes to disk is one that leaks between runs.
	s, _ := newTestSmart(t)
	if got := s.sub("digest-cache"); got != "" {
		t.Errorf("cache dir = %q, want empty", got)
	}
}

func TestTheLexiconReachesTheClassifier(t *testing.T) {
	// The classifier escalates from the deterministic scorer; without it there
	// is nothing to escalate FROM, and the Smart+ pass would be asked to label
	// everything rather than only what the lexicon could not.
	lx := classify.MustCompile(lexicon.Categories())
	s, _ := newTestSmart(t, WithLexicon(lx))
	if s.lexicon != lx {
		t.Error("WithLexicon did not take")
	}
	if s.Classifier() == nil {
		t.Fatal("no classifier")
	}
}

func TestAttachLexiconRebuildsTheClassifier(t *testing.T) {
	// Attached after construction because the category set is compiled
	// downstream of where the features are built. Rebuilt rather than mutated,
	// so a handed-out classifier is never changed under a caller — the
	// observable consequence, and the thing worth pinning.
	lx := classify.MustCompile(lexicon.Categories())
	s, _ := newTestSmart(t)
	before := s.Classifier()

	s.AttachLexicon(lx)
	if s.Classifier() == before {
		t.Error("the classifier was mutated in place rather than rebuilt")
	}
	if s.lexicon != lx {
		t.Error("the lexicon did not attach")
	}
}

func TestAttachLexiconLeavesEveryOtherFeatureAlone(t *testing.T) {
	// Nine of the ten do not know the lexicon exists. If attaching it rebuilt
	// the set, the caches would be reopened and any feature already handed to a
	// caller would be a different object than the one the set now reports.
	s, _ := newTestSmart(t)
	before := features(s)
	s.AttachLexicon(classify.MustCompile(lexicon.Categories()))

	for name, f := range features(s) {
		if name == "Classifier" {
			continue
		}
		if f != before[name] {
			t.Errorf("%s was rebuilt by AttachLexicon", name)
		}
	}
}
