//go:build js && wasm

package view

import "testing"

// queueAfter — advancing through a queue that moves underneath you.
//
// This is the arithmetic behind two reported failures with one cause. A
// background poll replaces the item list; the story currently playing has
// already been marked read by the show that opened it, so it drops out of a
// ranked or unread-only page. queueNext then answered "" — the same answer it
// gives for the genuine end — and the player believed it.
//
// Symptom one: the sign-off played after story one of sixty, and the display
// restarted from the top. Symptom two: with everything enabled and audio being
// synthesised successfully on the server, the slide announced that the voice
// had not started. It had.

func TestQueueAfterIsOrdinaryWhenNothingMoved(t *testing.T) {
	q := []string{"a", "b", "c"}
	if got := queueAfter(q, "a", 0); got != "b" {
		t.Errorf("after a = %q, want b", got)
	}
	if got := queueAfter(q, "b", 1); got != "c" {
		t.Errorf("after b = %q, want c", got)
	}
}

func TestQueueAfterNeverWraps(t *testing.T) {
	// A programme has an end because somebody chose one. Going round again is a
	// second reading of what was just played, and it is why this is not
	// queueStep with a flag.
	q := []string{"a", "b", "c"}
	if got := queueAfter(q, "c", 2); got != "" {
		t.Errorf("after the last story = %q, want the end", got)
	}
}

func TestAStoryThatLeftTheListDoesNotEndTheProgramme(t *testing.T) {
	// The bug, exactly. "b" was playing at position 1; a reload dropped it
	// because the show had marked it read. The programme has four more stories
	// and must play them.
	before := []string{"a", "b", "c", "d", "e"}
	after := []string{"a", "c", "d", "e"} // b is gone

	was := 1
	for i, id := range before {
		if id == "b" {
			was = i
		}
	}
	got := queueAfter(after, "b", was)
	if got == "" {
		t.Fatal("a story leaving the list ended the programme")
	}
	if got != "c" {
		t.Errorf("after the vanished story = %q, want c — the one that followed it", got)
	}
}

func TestWithNothingToFallBackOnTheEndIsHonest(t *testing.T) {
	// A caller that never recorded a position has no basis for a guess, and
	// inventing one would be worse than stopping: it would play an arbitrary
	// story and call it the running order.
	q := []string{"a", "b", "c"}
	if got := queueAfter(q, "gone", -1); got != "" {
		t.Errorf("with no remembered position = %q, want the end", got)
	}
}

func TestAListThatShrankPastThePositionIsTheEnd(t *testing.T) {
	// Everything after that point went too. Reaching for an index the queue no
	// longer has would either panic or wrap, and wrapping is the behaviour this
	// whole function exists to refuse.
	if got := queueAfter([]string{"a"}, "gone", 7); got != "" {
		t.Errorf("past the end of a shrunken queue = %q, want the end", got)
	}
}

func TestAnEmptyQueueIsTheEnd(t *testing.T) {
	if got := queueAfter(nil, "a", 0); got != "" {
		t.Errorf("empty queue = %q", got)
	}
}
