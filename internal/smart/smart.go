package smart

import (
	"context"
	"path/filepath"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Smart is every Smart+ feature, built once and asked for by name.
//
// # Why one type rather than ten constructors
//
// There were ten, and `internal/app` called all ten with the same two
// arguments — the LLM client and the settings repository — scattered across two
// hundred lines of wiring, in among the deriver, the job pool and the
// recommender. Nothing was wrong with any single line of it. What was wrong is
// what the arrangement made easy:
//
//   - **An eleventh feature is an eleventh call site**, and the wiring is where
//     an egress-capable feature would be added without anybody reviewing it as
//     one. A new Smart+ feature should be a method here, next to the ten that
//     already answer to the same key and the same consent rules.
//   - **There was no answer to "what can this instance send?"** short of
//     grepping for `smart.New`. That question has to be answerable in one place
//     for §18.8 to mean anything, and now it is: this file.
//   - **Nothing could be substituted for all of them at once.** A test that
//     wanted every feature pointed at a fake had to know all ten constructors.
//
// # What it deliberately is not
//
// It is not a facade over the features' own methods. `Interest`, `Podcast`,
// `Classifier` and the rest keep their types, their documentation and their
// tests exactly as they are, and callers keep taking the narrow type they
// actually use rather than a god object — `recommendjob` wants a
// `*RelevanceChecker`, not "smart". This owns CONSTRUCTION and nothing else,
// which is the part that was duplicated.
//
// It is also not a consent layer. Every feature still checks its own toggle on
// every call, because the toggles are per-reader and per-request and this is
// built once at boot. Being wired is not consent, and this type must never
// become the place that looks like it decides.
type Smart struct {
	// client is the seam, not a *llm.Client, so a test can build the whole set
	// against a fake in one line — see llmclient.go for why this interface is
	// declared in the consumer.
	client   llmClient
	settings *store.SettingsRepo
	// cacheDir is the parent of the two on-disk caches. The features that own
	// one get their own subdirectory, because a digest is keyed per article and
	// a broadcast segment per ordered PAIR of them, and one directory holding
	// two key shapes is one nobody can reason about the size of.
	cacheDir string
	// lexicon is the deterministic 26-category scorer the classifier escalates
	// from. It is the free tier's work, and the classifier's Smart+ pass exists
	// to answer only what the lexicon could not.
	lexicon *classify.Lexicon

	translator  *Translator
	palettes    *Palettes
	categorizer *Categorizer
	interest    *Interest
	relevance   *RelevanceChecker
	websearch   *WebSearchFinder
	analyzer    *SiteAnalyzer
	digest      *Digest
	podcast     *Podcast
	classifier  *Classifier
}

// Option configures a Smart at construction.
type Option func(*Smart)

// WithCacheDir sets the parent directory for the on-disk caches. Without it the
// two features that cache write nowhere, which is correct for a test and wrong
// for a server.
func WithCacheDir(dir string) Option {
	return func(s *Smart) { s.cacheDir = dir }
}

// WithLexicon supplies the deterministic scorer the classifier escalates from.
//
// Separate from the constructor because it is the one dependency that is not
// shared: nine of the ten features do not know the lexicon exists, and
// threading it through all of them to reach one is how a constructor grows a
// parameter list nobody can read.
func WithLexicon(lx *classify.Lexicon) Option {
	return func(s *Smart) { s.lexicon = lx }
}

// New builds every Smart+ feature.
//
// Eagerly, all ten, at boot. None of them opens a connection or reads a
// setting when constructed — they resolve the key and the reader's consent per
// call, from `ctx` — so building them costs a handful of allocations and buys
// the property that matters: the set is FIXED at startup, and a feature cannot
// be conjured later by code that happens to hold a client.
func New(client llmClient, settings *store.SettingsRepo, opts ...Option) *Smart {
	s := &Smart{client: client, settings: settings}
	for _, o := range opts {
		o(s)
	}

	s.translator = NewTranslator(client, settings)
	s.palettes = NewPalettes(client, settings)
	s.categorizer = NewCategorizer(client, settings)
	s.interest = NewInterest(client, settings)
	s.relevance = NewRelevanceChecker(client, settings)
	s.websearch = NewWebSearchFinder(client, settings)
	s.analyzer = NewSiteAnalyzer(client, settings)
	s.classifier = NewClassifier(client, settings, s.lexicon)
	s.digest = NewDigest(client, settings, s.sub("digest-cache"))
	s.podcast = NewPodcast(client, settings, s.sub("podcast-cache"))
	return s
}

// AttachLexicon gives the classifier the deterministic scorer it escalates from,
// after construction.
//
// It exists because of an ordering fact rather than a design preference: the
// category set is compiled where the analysis pipeline is assembled, which is
// downstream of where the Smart+ features are built. The alternatives were to
// move the whole set's construction down there — putting the answer to "what
// can this instance send" in the middle of the job wiring — or to build the
// classifier lazily, which would make the set no longer fixed at boot. This is
// the smaller compromise, and it is confined to the one feature with a
// dependency the other nine do not share.
//
// Rebuilds the classifier rather than mutating it, so the type stays immutable
// once handed out. Safe to call once, at startup, before anything is serving.
func (s *Smart) AttachLexicon(lx *classify.Lexicon) {
	s.lexicon = lx
	s.classifier = NewClassifier(s.client, s.settings, lx)
}

// sub names a cache directory under the configured parent, or "" when there is
// no parent — which the caching features already read as "do not cache".
func (s *Smart) sub(name string) string {
	if s.cacheDir == "" {
		return ""
	}
	return filepath.Join(s.cacheDir, name)
}

// The ten features, by name.
//
// Accessors rather than exported fields, so the set is read-only after
// construction: a caller that could reassign one could point a single feature
// at a different client, and the question "what can this instance send" would
// stop having one answer.

// Translator translates the interface catalog (A6, §10.5).
func (s *Smart) Translator() *Translator { return s.translator }

// Palettes composes and attunes themes (A4/A5, §20.16.3).
func (s *Smart) Palettes() *Palettes { return s.palettes }

// Categorizer suggests a folder for a newly added feed (A7).
func (s *Smart) Categorizer() *Categorizer { return s.categorizer }

// Interest reranks My Feed, extracts entities and names topics (A1/A2/A3, §18.8b).
func (s *Smart) Interest() *Interest { return s.interest }

// Relevance checks a discovered candidate against the reader's taste (A11, §18.7).
func (s *Smart) Relevance() *RelevanceChecker { return s.relevance }

// WebSearch is discovery rung 5, the hosted-tool one (A10, §18.7).
func (s *Smart) WebSearch() *WebSearchFinder { return s.websearch }

// SiteAnalyzer proposes a scrape rule from a page (A8/A9, §11 rung 5).
func (s *Smart) SiteAnalyzer() *SiteAnalyzer { return s.analyzer }

// Digest summarises an article before it is read aloud (A12, §10.7).
func (s *Smart) Digest() *Digest { return s.digest }

// Podcast scripts a broadcast (A13, §29.7).
func (s *Smart) Podcast() *Podcast { return s.podcast }

// Classifier is the per-reader labelling pass (A14, §27.4).
func (s *Smart) Classifier() *Classifier { return s.classifier }

// Configured reports whether this instance can egress at all.
//
// One question with one answer, which is the point. It was previously asked by
// whichever feature happened to be in front of the reader, and the doctor asked
// it a second way — the shape of every confusion this area has produced, and
// the reason `internal/app` keeps its own copy of the key function to this day.
//
// It is NOT a consent check. A configured instance still sends nothing until a
// reader turns something on; see the type's doc.
func (s *Smart) Configured(ctx context.Context) bool {
	return s.client != nil && s.client.Configured(ctx)
}
