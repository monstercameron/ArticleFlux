//go:build js && wasm

package view

// The second UX audit's client-side fixes that can be proven without a browser:
// what the markup says. The behaviour that needs one — focus, Tab stops, the
// pointer — is in e2e/audit2.spec.mjs.

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// G3: an article's chips say the tag's NAME, not the label it wears in the
// sidebar. A renamed tag used to read "Systems stuff" under every article of its
// feed, and the remove button announced that, while the field beside it wanted
// "rust".
func TestArticleChipsDrawTheTagNameNotTheSidebarLabel(t *testing.T) {
	tags := []*pb.Tag{{Id: "t1", Name: "rust", Label: "Systems stuff"}}
	got := tagLabelsBySource(tags, map[string][]string{"src-1": {"t1"}}, nil)["src-1"]
	if len(got) != 1 || got[0].Label != "rust" || got[0].Name != "rust" {
		t.Fatalf("chip = %+v, want label and name both \"rust\"", got)
	}
}

// G4: a chip is two real buttons, so a keyboard can open a tag and can remove
// one, and each says what it is.
func TestTagChipIsTwoButtons(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return tagChip(tr, tagRef{Label: "rust", Name: "rust", ID: "t1"}, "item-1")
	})
	open := elementTag(t, out, `data-tag-id="t1"`)
	if !strings.HasPrefix(open, "<button") {
		t.Errorf("the open control is not a button: %s", open)
	}
	rm := elementTag(t, out, `data-action="remove-tag"`)
	if !strings.HasPrefix(rm, "<button") {
		t.Errorf("the remove control is not a button: %s", rm)
	}
	if strings.Contains(rm, `tabindex="-1"`) || strings.Contains(strings.ToLower(rm), `tabindex="-1"`) {
		t.Errorf("the remove control is out of the Tab order, which is what stranded a keyboard reader: %s", rm)
	}
	if !strings.Contains(rm, `aria-label="Remove tag rust"`) {
		t.Errorf("the remove control lost its name: %s", rm)
	}
}

// G6: the editor opened by + NEW has no folder behind it. It offers Create, not
// Delete, and it says it is new; nothing it does can leave a stray folder.
func TestCategoryDialogInCreateMode(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return categoryDialog(tr, categoryProps{open: true, id: catNewID, creating: true})
	})
	if strings.Contains(out, `data-action="category-delete"`) {
		t.Error("a folder that does not exist yet offers Delete")
	}
	if !strings.Contains(out, "Create folder") || !strings.Contains(out, "New folder") {
		t.Errorf("the create form lost its title or its button: %s", out)
	}
}

// G19: the name field stops at the cap, says the cap, and flags itself invalid
// when its input was refused.
func TestCategoryNameFieldStatesItsLimitAndFlagsARefusal(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return categoryDialog(tr, categoryProps{open: true, id: "f1", name: "Tech", draft: "Tech", err: "A folder needs a name."})
	})
	in := elementTag(t, out, `data-role="category-name"`)
	if !strings.Contains(in, `maxlength="48"`) {
		t.Errorf("the name field has no maxlength: %s", in)
	}
	if !strings.Contains(in, `aria-invalid="true"`) {
		t.Errorf("a refused name is not aria-invalid: %s", in)
	}
	if !strings.Contains(out, "Up to 48 characters.") {
		t.Errorf("the limit is not stated: %s", out)
	}
}

// G15: the folder dialog's armed delete carries data-armed, which is what the
// shared Escape and press-outside rules look for.
func TestCategoryDeleteArmsInPlaceLikeTheOthers(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return categoryDialog(tr, categoryProps{open: true, id: "f1", name: "Tech", confirm: true})
	})
	if !strings.Contains(out, `data-armed="true"`) {
		t.Errorf("the armed delete is not stamped data-armed: %s", out)
	}
}

// G5: a refused rename is said inside the panel, next to the field.
func TestTagSettingsShowsARefusedRenameInThePanel(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return tagSettings(tr, tagSettingsProps{open: true, t: &pb.Tag{Id: "t1", Name: "rust"},
			draftLabel: "x", err: "Couldn't save that tag: too long"})
	})
	if !strings.Contains(out, "Couldn&#39;t save that tag: too long") && !strings.Contains(out, "Couldn't save that tag: too long") {
		t.Errorf("the refusal is not in the panel: %s", out)
	}
	in := elementTag(t, out, `data-role="tag-label"`)
	if !strings.Contains(in, `maxlength="48"`) || !strings.Contains(in, `aria-invalid="true"`) {
		t.Errorf("the rename field lacks maxlength or aria-invalid: %s", in)
	}
}

// G11: a note whose save is waiting on the connection says so in words, in the
// same sentence a failed one uses — not a spinner reading "Saving…".
func TestNoteSyncMarkSaysNotSavedWhenQueued(t *testing.T) {
	for _, state := range []string{noteSyncQueued, noteSyncFailed} {
		out := renderView(t, func(tr i18n.Runtime) ui.Node { return noteSyncMark(tr, state) })
		if !strings.Contains(out, `class="sync-words"`) || !strings.Contains(out, "Not saved") {
			t.Errorf("%s: the mark does not say Not saved in visible words: %s", state, out)
		}
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return noteSyncMark(tr, noteSyncSaving) })
	if strings.Contains(out, "sync-words") {
		t.Errorf("saving speaks in words, which only the unsaved states may: %s", out)
	}
}

// G10: every control in the sidebar is out of the Tab order in the markup; the
// one stop is chosen after the render (platform.RovingSync).
func TestRailRowsAndGearsAreOutOfTheTabOrder(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return railPane(railProps{feeds: fixtureFeeds(3), tags: fixtureTags(), folders: fixtureFolders(),
			sel: scope{Title: "x"}})
	})
	for _, marker := range []string{`class="feed-gear"`, `class="rail-band-toggle"`} {
		tag := elementTag(t, out, marker)
		if !strings.Contains(strings.ToLower(tag), `tabindex="-1"`) {
			t.Errorf("%s is a Tab stop: %s", marker, tag)
		}
	}
	row := elementTag(t, out, `data-source-id="src-0"`)
	if !strings.Contains(strings.ToLower(row), `tabindex="-1"`) {
		t.Errorf("a feed row is a Tab stop: %s", row)
	}
	foot := elementTag(t, out, `data-action="add-feed-open"`)
	if strings.Contains(strings.ToLower(foot), `tabindex`) {
		t.Errorf("Add a feed was taken out of the Tab order: %s", foot)
	}
}
