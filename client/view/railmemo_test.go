//go:build js && wasm

package view

import (
	"reflect"
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/GoWebComponents/v5/ui"
)

// Does railPane actually bail out of re-rendering?
//
// # Why this test exists
//
// railProps is built to be compared by value, and a lot of design rests on that
// one predicate. The three fold-away sections are booleans rather than a map
// because "a map field would compare by identity and defeat that on every
// render". openCats is a comma-joined string rather than a set for the same
// stated reason. The call site says the rail "is a real component so GWC can
// bail out of re-rendering it when its props are unchanged — that is what
// stopped 150 sidebar rows from being rebuilt every time an item was marked
// read."
//
// Nothing checked the predicate, and it was false. A `ui.Handler` field —
// added under a comment saying a Handler "is a value and compares fine" — made
// the comparison fail every single time, because the value it wraps is a func
// and reflect.DeepEqual descends into it.
//
// # How GWC compares them, so the test asserts the right thing
//
// Component props reach fastEqual (internal/runtime/hooks.go). railProps has
// slice fields, so reflect.Type.Comparable is false, so fastEqual takes its last
// branch: `return reflect.DeepEqual(parseA, parseB)`. That is the predicate, and
// it is what these tests call directly. Asserting on DeepEqual rather than on
// render counts keeps this a test of the property the design depends on rather
// than a test of GWC's scheduler.

func railTestFeeds() []*pb.Feed {
	return []*pb.Feed{
		{Id: "1", SourceId: "s1", Title: "One", UnreadCount: 3},
		{Id: "2", SourceId: "s2", Title: "Two"},
	}
}

// TestRailPropsCompareEqualWhenNothingChanged is the property everything else
// assumes.
//
// The props are built the way Reader builds them — same slices, same handler
// ref — and must compare equal, or the rail re-renders every one of its rows on
// every render of its parent.
func TestRailPropsCompareEqualWhenNothingChanged(t *testing.T) {
	feeds := railTestFeeds()
	// A Ref is what Reader passes. Its zero value is what every other caller
	// passes, and both have to compare equal to themselves.
	h := ui.Ref[ui.Handler]{}

	build := func() railProps {
		return railProps{
			feeds:         feeds,
			total:         7,
			filter:        "",
			openCats:      "news,tech",
			onFilterInput: h,
		}
	}

	if !reflect.DeepEqual(build(), build()) {
		t.Fatal("two railProps built from identical inputs compare UNEQUAL, so railPane " +
			"cannot bail out and re-renders every sidebar row on every render of Reader — " +
			"during a scroll that is once per painted frame")
	}
}

// TestRailPropsRejectABareHandler is the regression guard.
//
// It does not test railProps; it tests the reason railProps may not hold a bare
// ui.Handler again. Re-adding one is a two-word change that looks harmless and
// silently restores a sixty-times-a-second re-render of 151 rows, with nothing
// failing — which is exactly how it got there the first time.
func TestRailPropsRejectABareHandler(t *testing.T) {
	type withBareHandler struct {
		feeds   []*pb.Feed
		handler ui.Handler
	}
	feeds := railTestFeeds()
	// The SAME handler value on both sides. Not two closures — the point is that
	// even a perfectly stable reference does not survive the comparison.
	h := ui.WrapHandler(func(string) {})

	a := withBareHandler{feeds: feeds, handler: h}
	b := withBareHandler{feeds: feeds, handler: h}

	if reflect.DeepEqual(a, b) {
		t.Skip("reflect.DeepEqual now sees two identical ui.Handlers as equal; the " +
			"hazard this guards is gone and the Ref indirection in railProps could " +
			"be simplified away")
	}
	// Confirmed unequal. Show that the Ref indirection is what fixes it, so the
	// remedy is next to the diagnosis.
	type withHandlerRef struct {
		feeds   []*pb.Feed
		handler ui.Ref[ui.Handler]
	}
	ref := ui.Ref[ui.Handler]{}
	if !reflect.DeepEqual(
		withHandlerRef{feeds: feeds, handler: ref},
		withHandlerRef{feeds: feeds, handler: ref},
	) {
		t.Fatal("a Ref field does not compare equal either, so the fix in railProps " +
			"does not actually restore the bailout")
	}
}
