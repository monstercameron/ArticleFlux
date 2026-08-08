package smart

import (
	"context"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

// llmClient is the seam between this package and internal/llm.
//
// Every Smart+ feature in this package held a concrete *llm.Client, which meant
// there was no way to substitute a fake in a test — any test exercising the
// paths past the Configured() gate would either skip (as live_test.go and the
// guard-only tests in interest_test.go/translate_test.go document doing) or
// risk a real, billable call to api.openai.com. Neither is acceptable, and the
// second is the one this interface exists to make impossible.
//
// The interface lives here, in the CONSUMER package, rather than in internal/llm
// — the idiomatic Go direction, and the one that keeps internal/llm from
// depending on the shape its callers happen to want. It is also deliberately
// narrow: these two methods, `Do` and `Configured`, are the only ones any type
// in this package calls on its *llm.Client field (verified by grepping every
// `.llm.` call site in internal/smart). A wider interface guessed up front would
// have been wrong the moment a caller here started using llm.Client.Usage() or
// some future method, and Go does not need the extra methods declared to make
// *llm.Client satisfy this — the assertion below is what keeps that true.
type llmClient interface {
	Do(ctx context.Context, r llm.Request) (string, error)
	Configured(ctx context.Context) bool
	// OpsContext hands back a context that runs SchemaFlux's typed operations
	// through this instance's provider.
	//
	// The features in this package are built on those operations now — the
	// library writes the prompt and derives the schema from a Go type — and an
	// operation given a bare context resolves its provider from a package
	// global instead. On a fresh process that global is a MOCK, whose answers
	// parse into zero-valued structs and are indistinguishable from a working
	// deployment until somebody reads the output. So every operation in this
	// package is given one of these, and the fake in the tests returns the
	// context untouched so `schemafluxtest.Install` is what answers there.
	OpsContext(ctx context.Context) context.Context
}

// Compile-time proof that the real client still satisfies the seam. Without
// this, a signature change to llm.Client made for an unrelated reason would
// surface as a confusing error deep in whichever caller happened to be edited
// next, rather than here.
var _ llmClient = (*llm.Client)(nil)
