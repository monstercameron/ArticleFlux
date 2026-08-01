//go:build js && wasm

package view

import (
	"strings"
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Deleting one row, resetting a category (§18.9's own correction UI, extended
// with an explicit delete/reset rather than only the dial).
//
// These pin the markup the two-press confirm depends on: an unarmed row/group
// offers only the first press, an armed one relabels into confirm+cancel and
// marks itself data-armed for the stylesheet, and nothing here fires a write
// on its own — settingsMyFeed only ever renders what reader.go's state says,
// the click handlers (reader_clicks.go) are what turn a press into an RPC.
//
// # cleanSSR
//
// A row here renders its target id twice — once in each of the dial's four
// buttons, again in the delete control — and `ui.RenderToString` (this
// package's own test helper, not the wasm client) inserts a U+001F unit
// separator into the SECOND rendering of an identical string value within
// one tree; a diffing artifact from its own memoization, confirmed absent
// from the real DOM by inspecting the actual browser output in a running
// instance (the live wasm client renders real elements, never a diffed
// string, so the artifact has nowhere to come from there). cleanSSR strips
// it so these tests assert what a reader's browser actually shows.

// cleanSSR removes the U+001F artifact described above. See this file's
// header comment.
func cleanSSR(s string) string {
	return strings.ReplaceAll(s, "\x1f", "")
}

func TestMyFeedRowDeleteControlUnarmedOffersOnlyRemove(t *testing.T) {
	out := cleanSSR(renderMyFeed(t, myFeedProps{profile: demoProfile()}))

	// The topic row (key "maxpro90s"), one entity row ("pro max") and one
	// feed row ("s1") each carry an unarmed remove control naming their own
	// target/kind. Matched on the delete-arm action specifically — the
	// dial's own chips carry the same data-for-item, so a marker that did
	// not also pin data-action would find one of those instead.
	for _, want := range []struct{ target, kind string }{
		{"maxpro90s", myFeedRowTopic},
		{"pro max", myFeedRowEntity},
		{"s1", myFeedRowFeed},
	} {
		marker := `data-action="` + actMyFeedDeleteArm + `" data-armed="false" data-for-item="` + want.target + `"`
		tag := elementTag(t, out, marker)
		if !strings.Contains(tag, `data-value="`+want.kind+`"`) {
			t.Errorf("%s row's delete control lost its kind: %s", want.target, tag)
		}
	}
	// No confirm/cancel copy anywhere yet.
	if strings.Contains(out, "Confirm remove?") {
		t.Errorf("delete confirm copy is on screen with nothing armed:\n%s", out)
	}
}

func TestMyFeedRowDeleteControlArmedOffersConfirmAndCancel(t *testing.T) {
	// The entity row ("pro max"), not the topic row: a topic's key is built
	// by the fixture from the same term list its evidence line is (both
	// "maxpro90s" and "max · pro · 90s..." trace back to demoProfile's Terms),
	// and comparing the two anywhere near each other is what triggers the
	// U+001F artifact cleanSSR strips — avoiding it here rather than relying
	// solely on the strip keeps this test's own setup honest, not just its
	// assertions.
	p := myFeedProps{profile: demoProfile(), confirmDelete: myFeedRowEntity + ":pro max"}
	out := cleanSSR(renderMyFeed(t, p))

	confirmTag := elementTag(t, out, `data-action="`+actMyFeedDeleteConfirm+`"`)
	if !strings.Contains(confirmTag, `data-for-item="pro max"`) {
		t.Errorf("the armed entity's confirm control lost its target: %s", confirmTag)
	}
	if !strings.Contains(confirmTag, `data-armed="true"`) {
		t.Errorf("the armed entity's confirm control is not marked data-armed: %s", confirmTag)
	}
	if !strings.Contains(out, `data-action="`+actMyFeedDeleteCancel+`"`) {
		t.Errorf("an armed row offers no cancel:\n%s", out)
	}
	// Only the ARMED row's control confirms; the topic row must still offer
	// the arm action, not confirm — one row armed does not arm the tab.
	if !strings.Contains(out,
		`data-action="`+actMyFeedDeleteArm+`" data-armed="false" data-for-item="maxpro90s"`) {
		t.Errorf("an unrelated row was armed (or lost its arm control) alongside the entity:\n%s", out)
	}
}

func TestMyFeedCategoryResetUnarmedOffersOnlyReset(t *testing.T) {
	out := cleanSSR(renderMyFeed(t, myFeedProps{profile: demoProfile()}))

	for _, cat := range []string{myFeedCatTopics, myFeedCatEntities, myFeedCatFeeds} {
		tag := elementTag(t, out, `data-value="`+cat+`"`)
		if !strings.Contains(tag, `data-action="`+actMyFeedResetArm+`"`) {
			t.Errorf("category %q's reset control is not the unarmed arm action: %s", cat, tag)
		}
		if strings.Contains(tag, `data-armed="true"`) {
			t.Errorf("category %q's reset control is armed with nothing armed in props: %s", cat, tag)
		}
	}
	if strings.Contains(out, "Confirm reset?") {
		t.Errorf("reset confirm copy is on screen with nothing armed:\n%s", out)
	}
}

func TestMyFeedCategoryResetArmedOffersConfirmCancelAndWarning(t *testing.T) {
	p := myFeedProps{profile: demoProfile(), confirmReset: myFeedCatTopics}
	out := cleanSSR(renderMyFeed(t, p))

	if !strings.Contains(out, actMyFeedResetConfirm) {
		t.Errorf("armed topics category offers no confirm action:\n%s", out)
	}
	if !strings.Contains(out, actMyFeedResetCancel) {
		t.Errorf("armed topics category offers no cancel action:\n%s", out)
	}
	if !strings.Contains(out, `role="alert"`) {
		t.Errorf("armed category reset carries no live-region warning:\n%s", out)
	}
	// Arming Topics must not also arm Entities or Feeds — only one category
	// confirms at a time, the same one-target-armed shape as row delete.
	if strings.Contains(out, actMyFeedResetConfirm+`" data-value="`+myFeedCatEntities) ||
		strings.Contains(out, actMyFeedResetConfirm+`" data-value="`+myFeedCatFeeds) {
		t.Errorf("arming the topics category also armed another category:\n%s", out)
	}
}

// A row with no target (defensive: a malformed profile entry) renders no
// delete control at all rather than a control that would arm on an empty id
// and later act on "kind:" with nothing to find.
func TestMfDeleteControlRendersNothingForAnEmptyTarget(t *testing.T) {
	tr := mustRuntime(t)
	nodes := mfDeleteControl(myFeedRowFeed, "", false, tr)
	if nodes != nil {
		t.Errorf("mfDeleteControl(kind, \"\", ...) = %v, want nil", nodes)
	}
}

// An empty category — the common case for a fresh account, or any reader
// this fixture's own cold-start describes — offers no reset button at all.
// "Reset this list" over an empty list is an offer to undo nothing, and a
// button that does nothing when pressed is worse than no button.
func TestMyFeedEmptyCategoriesOfferNoResetButton(t *testing.T) {
	out := cleanSSR(renderMyFeed(t, myFeedProps{profile: &pb.GetInterestProfileResponse{}}))
	if strings.Contains(out, actMyFeedResetArm) {
		t.Errorf("an empty profile still offers a category reset button:\n%s", out)
	}
	if strings.Contains(out, actMyFeedDeleteArm) {
		t.Errorf("an empty profile still offers a row delete button:\n%s", out)
	}
}
