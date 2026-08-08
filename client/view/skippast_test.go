//go:build js && wasm

package view

import "testing"

// The rule that decides whether a topmost-article report counts as reading.
//
// This is the deterministic half of TODO.md Q1 — "the jumping down race" — which
// the e2e suite reproduces about one run in three and which was mis-filed twice
// as a flaky test. It is not a flaky test. Opening an article three rows down
// seeds the one above it into the stream and scrolls the target to the top,
// which drags the seeded one past the fold; that is suppressed correctly at the
// moment of the jump. What was not suppressed was what happened NEXT: article
// bodies land asynchronously, an article above the reader grows when its body
// arrives, everything below it moves down, and at an unchanged scroll position
// the skipped article becomes the topmost one. The reader did nothing. The app
// marked it read and told the server so.
//
// It failed only after a reload, which is why it looked like a test observing
// too early: the optimistic local flag really did say unread, and the row really
// did come back read, because the write had gone out.

func TestScrollingIntoASkippedArticleReadsIt(t *testing.T) {
	// The behaviour the suppression must NOT break, and the reason this is a
	// rule rather than "never mark a skipped article read". The article a jump
	// passed over is still sitting in the stream above the reader; going back up
	// to it is reading it.
	if deliveredByLayout(true, true) {
		t.Error("the reader scrolled back into a skipped article and it did not count as reading it")
	}
}

func TestAReflowDoesNotReadASkippedArticle(t *testing.T) {
	// The bug. No scroll, and an article the jump had skipped.
	if !deliveredByLayout(false, true) {
		t.Error("an article delivered under the fold by a body loading above it was counted as read")
	}
}

func TestOrdinaryScrollingIsUnaffected(t *testing.T) {
	if deliveredByLayout(true, false) {
		t.Error("ordinary scrolling stopped counting as reading")
	}
}

func TestAReflowAroundAnUnskippedArticleIsUnaffected(t *testing.T) {
	// A reflow that changes the topmost article without a jump having happened
	// is the pane settling around something the reader is already on. Nothing
	// was skipped, so nothing is being protected, and the ordinary path applies
	// — narrowing the guard to the case it was written for rather than
	// suppressing every layout-driven report.
	if deliveredByLayout(false, false) {
		t.Error("a reflow with nothing skipped was treated as a delivery")
	}
}
