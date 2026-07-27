// Package lexicon is the shipped default taxonomy for internal/classify
// (plan.md §27.3d, TODO 10.2).
//
// # Why this is Go, not a table
//
// Three reasons, and the third is the only one that matters a year from now:
//
//  1. It ships and versions with the build, so no instance can be running a
//     lexicon nobody can reproduce from the binary that produced it.
//  2. It is testable against the corpus (§27.11) with a normal `go test` and
//     no database — a term that regresses shows up in CI, not discovered by
//     a reader in the Unsorted view six weeks later.
//  3. **`git blame` on a line answers "why is this term here".** A 900-row
//     table is a table nobody can account for a year from now, and the
//     first thing anyone does with an unaccountable lexicon is stop editing
//     it — which is how a classifier calcifies into whatever shipped on day
//     one. A comment next to a term, reviewed in a pull request, is the only
//     version of "why" that survives contact with a second author.
//
// The reader's own edits are a delta stored in `user_categories`, never a
// copy (§27.3f) — this package is only ever the default that delta is
// computed against, so a lexicon fix here reaches every reader who has not
// touched the category it lives in.
package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// Categories returns the 26 built-in labels, in §27.3d's table order — the
// order the settings screen, the chip row and the trends histogram all
// render in, so reordering this slice is a product change, not a refactor.
func Categories() []classify.Label {
	return []classify.Label{
		software(),
		ai(),
		hardware(),
		security(),
		science(),
		health(),
		business(),
		finance(),
		politics(),
		world(),
		law(),
		climate(),
		space(),
		energy(),
		transport(),
		gaming(),
		filmTV(),
		music(),
		culture(),
		books(),
		sport(),
		food(),
		travel(),
		design(),
		work(),
		education(),
	}
}
