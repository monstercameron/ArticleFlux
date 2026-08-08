package smart

import (
	schemaflux "github.com/monstercameron/schemaflux"
)

// The output ceiling every Smart+ operation runs under, said out loud.
//
// # What went wrong
//
// Before the SchemaFlux migration each request in this package named its own
// `MaxOutputTokens`, chosen against what the feature actually emits: 6000 for a
// scrape rule, 8000 for a batch of sixty translated strings, 4200 for a podcast
// segment. The typed operations took none of those with them. SchemaFlux
// derives the ceiling from the SPEED TIER instead — 4000 for `Smart`, 2000 for
// `Fast`, 2000 when nothing is said — so every one of those numbers became a
// dead constant and every feature silently inherited a ceiling somebody chose
// for a different question.
//
// Three of them went DOWN, which is the part that breaks rather than costs:
// translation from 8000 to 2000, scraping from 6000 to 2000, podcast segments
// from 4200 to 2000. A ceiling below what a feature emits is `llm.ErrTruncated`
// — a refusal, not a short answer, because a partial structured reply is worse
// than none. And it is invisible here: the `*_llm_test.go` files that would
// catch it are env-gated behind a real paid provider, so the suite stays green
// while the feature is broken against OpenAI.
//
// # Why every call site sets one, including the ones that did not change
//
// A tier is an answer to "how hard should this think". A ceiling is an answer
// to "how long is the output". They were the same knob and are not any more
// (SchemaFlux I-09), and conflating them again is the failure being fixed — so
// the ceiling is stated at every operation rather than at the ones where the
// tier default currently happens to be wrong. Changing a tier is then a
// decision about reasoning alone, which is what it should have been all along.
//
// # Why some numbers are HIGHER than the ones they restore
//
// The rule applied is `max(hand-chosen, tier default)`. Three of the old
// ceilings were small — 150 for a category pick, 600 for a relevance verdict,
// 1200 for a topic label — and they were sized against a hand-written schema
// with one or two fields. The library derives its schema from a Go type and
// writes its own prompt, so the same answer costs more tokens now; restoring
// 150 would convert a working feature into `ErrTruncated` in the name of
// fidelity. Nothing is lowered, because a ceiling is a safety limit and the
// saving from tightening one is a rounding error against the call itself.

// The ceilings, one per operation, in the order the features run.
//
// Each is either the value that call site named before the migration or the
// tier default it already had, whichever is larger — see the package note
// above. The two that carry their own named constants (`digestMaxTokens`,
// `podcastMaxTokens`) keep them, because those are referenced from the prose
// that explains why they are what they are.
const (
	// categorizeMaxTokens covers a pick from a short list. Was 150 against a
	// hand-written two-field schema; the library's `Choosing` schema and prompt
	// are wider, and Fast's own default is the larger number.
	categorizeMaxTokens = 2000
	// inventMaxTokens covers naming one folder in two words. Never had an
	// explicit ceiling; stated here so a tier change cannot become a length
	// change.
	inventMaxTokens = 2000
	// rerankMaxTokens covers a ranked reply over a batch of items.
	rerankMaxTokens = 4000
	// entityMaxTokens covers the entities pulled out of one article. Was 4000
	// and became 2000 when the tier default took over — this is the restore.
	entityMaxTokens = 4000
	// topicLabelMaxTokens covers a short label. Was 1200 against a hand-written
	// schema; Fast's default is larger and nothing is lowered.
	topicLabelMaxTokens = 2000
	// relevanceMaxTokens covers a verdict and its reason. Was 600, same story.
	relevanceMaxTokens = 2000
	// analyzeMaxTokensSF is `analyzeMaxTokens` restored at the two scrape call
	// sites. Named separately only because the original constant carries the
	// account of how it was arrived at (scrape.go) and this file must not
	// duplicate it.
	analyzeMaxTokensSF = analyzeMaxTokens
	// paletteMaxTokens covers a full palette. Was 3000; Smart's default is 4000
	// and nothing is lowered.
	paletteMaxTokens = 4000
	// translateMaxTokens is the largest ceiling in the package and the one whose
	// loss was most expensive: sixty UI strings in one call, against a default
	// of 2000.
	translateMaxTokens = maxOutputTokens
)

// The four Configure closures below exist because the fluent builders are
// distinct generic types with no shared interface — `ExtractRequest[T]` and
// `SummarizeRequest` have identical `Configure` methods over different options
// structs, and Go cannot abstract over that. One helper per options type is the
// smallest honest way to say the same thing at thirteen call sites.
//
// `OpOptions.MaxOutputTokens` is the field `ops.CallLLM` actually reads: it
// overrides `config.GetMaxTokens(intelligence)` when non-zero and is ignored
// when zero, so setting it changes only the ceiling and leaves the tier's other
// effects — temperature, model class — alone.

func capExtract(n int) func(schemaflux.ExtractOptions) schemaflux.ExtractOptions {
	return func(o schemaflux.ExtractOptions) schemaflux.ExtractOptions {
		o.OpOptions.MaxOutputTokens = n
		return o
	}
}

func capGenerate(n int) func(schemaflux.GenerateOptions) schemaflux.GenerateOptions {
	return func(o schemaflux.GenerateOptions) schemaflux.GenerateOptions {
		o.OpOptions.MaxOutputTokens = n
		return o
	}
}

func capChoose(n int) func(schemaflux.ChooseOptions) schemaflux.ChooseOptions {
	return func(o schemaflux.ChooseOptions) schemaflux.ChooseOptions {
		o.OpOptions.MaxOutputTokens = n
		return o
	}
}

func capSummarize(n int) func(schemaflux.SummarizeOptions) schemaflux.SummarizeOptions {
	return func(o schemaflux.SummarizeOptions) schemaflux.SummarizeOptions {
		o.OpOptions.MaxOutputTokens = n
		return o
	}
}
