//go:build js && wasm

package view

import (
	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// keyHint is the one door for a sentence that tells the reader to press a key.
//
// A phone has no keyboard, so "Press u to show everything" is an instruction to
// do something the reader cannot — and it is the empty state they are looking at
// when they are stuck. Every such string has a sibling named <key>Touch that says
// the same thing in terms a finger can follow (or drops the key clause), and this
// serves it on a coarse pointer. Reading the keys straight through tr.T is the
// mistake this exists to make visible: TestKeyHintsHaveTouchVariants fails a
// catalog string that names a key but has no sibling, and a call site that
// bypasses this one.
//
// Only where there IS a sibling. Most hints name no key and have none — "Try
// fewer words, or clear the search box." is as true under a finger — and every
// empty state comes through here, so asking for a sibling that does not exist
// put the key's own name on the screen in place of the sentence.
func keyHint(tr i18n.Runtime, ns, key string) string {
	if platform.CoarsePointer() && i18n.Has(ns, key+"Touch") {
		return tr.T(ns, key+"Touch")
	}
	return tr.T(ns, key)
}
