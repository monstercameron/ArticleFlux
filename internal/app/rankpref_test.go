package app

import (
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/reader"
)

// The two halves of the Smart+ opt-in have to name the same preference.
//
// `reader` writes the key and decides whether to ask for a re-derivation; `derive` reads it
// and decides whether to spend money. Neither imports the other — deliberately, so the
// service layer carries no dependency on a background job (reader.WithSignalHook explains
// why) — which leaves the string duplicated.
//
// This package imports both, so it is the one place the duplication can be checked. Without
// this test, renaming the key on one side leaves a toggle that saves a preference nothing
// reads: the switch moves, the ranking never changes, and every layer looks correct on its
// own. That is a failure with no error message anywhere, which is the kind worth a test
// this small.
func TestRankPrefKeyMatchesTheDeriver(t *testing.T) {
	if reader.SmartPlusPrefKey != derive.SmartPlusPrefKey {
		t.Fatalf("the service writes %q and the deriver reads %q — the toggle is inert",
			reader.SmartPlusPrefKey, derive.SmartPlusPrefKey)
	}
}
