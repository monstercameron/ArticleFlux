package smart

import (
	"errors"
	"strings"

	schemaflux "github.com/monstercameron/schemaflux/v2"
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

// sfOpts is the options every typed call in this package states: the
// instance's model, the tier, the steering, and the token ceiling.
//
// SchemaFlux v2 replaced the fluent builders (`.Steer(...).Configure(...)
// .Model(...).Fast()`) with one Options value per call, so the four Configure
// closures that used to live here — one per builder type, because Go could not
// abstract over them — are one function now. The ceiling is still stated at
// every call site rather than inherited from the tier, for the reason above:
// a tier change must not become a length change.
//
// `MaxOutputTokens` is v2's own field for the ceiling and overrides the tier's;
// the tier still decides the temperature (Mode) and, when no model is pinned,
// the model. The model IS always pinned here (OpsModel), so the tier never
// chooses it.
func sfOpts(model string, tier schemaflux.Tier, steering string, maxTokens int) schemaflux.Options {
	return schemaflux.Options{
		Model:           model,
		Tier:            tier,
		Steering:        steering,
		MaxOutputTokens: maxTokens,
	}
}

// opErr keeps the cause's own words on an error from a SchemaFlux operation.
//
// v2's *schemaflux.Error deliberately leaves provider text out of Error() —
// "summarize: unavailable (articleflux/gpt-6-luna)" — and keeps it as the
// wrapped Err. Two things in this repository read that text: the Smart+
// refusal classifier in internal/app (an invalid key, a spent quota and an
// unknown model are told apart by the provider's words) and the "nothing to
// summarise" fallback, keyed on the library's own empty-completion message. v1
// put both in the message; without this, every refusal would read as
// "refused" and every empty digest as a provider fault.
//
// Only the innermost non-SchemaFlux cause is added, and only when the answer
// was not the problem: an invalid answer's cause can quote the model's output,
// and that never goes into an error string.
func opErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, schemaflux.ErrInvalidAnswer) && !strings.Contains(causeOf(err), "empty completion") {
		return err
	}
	cause := causeOf(err)
	if cause == "" || strings.Contains(err.Error(), cause) {
		return err
	}
	return &causedError{err: err, cause: cause}
}

type causedError struct {
	err   error
	cause string
}

func (e *causedError) Error() string { return e.err.Error() + ": " + e.cause }
func (e *causedError) Unwrap() error { return e.err }

// causeOf is the message of the outermost error in err's chain that is not a
// *schemaflux.Error, or "".
func causeOf(err error) string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if _, own := e.(*schemaflux.Error); !own {
			return e.Error()
		}
	}
	return ""
}
