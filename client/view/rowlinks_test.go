//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The category chip, the feed title, and a tag chip's label are all click
// targets that route elsewhere (category view, feed view, tag view) even
// though the category chip and feed title also sit inside an item row whose
// OWN click opens the article. These pin the markup those three targets and
// the row's guard against them all firing at once depend on.

func TestItemRowCategoryChipCarriesRoutingAttribute(t *testing.T) {
	it := &pb.Item{Id: "i1", Title: "Piece", SourceId: "s1", SourceTitle: "The Verge",
		Category: "tech"}
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return itemRow(tr, it, false, nil, -1, "")
	})

	chip := elementTag(t, out, `data-category-slug="tech"`)
	if !strings.Contains(chip, `class="cat-chip`) {
		t.Errorf("the category-slug attribute landed on something other than the cat-chip: %s", chip)
	}

	// The row itself still opens the article on its own click — the chip
	// riding inside it must not have replaced that attribute, only added its own.
	row := elementTag(t, out, `data-item-id="i1"`)
	if row == "" {
		t.Errorf("item row lost its own data-item-id once the chip inside it carried data-category-slug")
	}
}

func TestItemRowFeedTitleCarriesSourceID(t *testing.T) {
	it := &pb.Item{Id: "i1", Title: "Piece", SourceId: "s1", SourceTitle: "The Verge"}
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return itemRow(tr, it, false, nil, -1, "")
	})

	src := elementTag(t, out, `data-source-id="s1"`)
	if !strings.Contains(src, `class="item-source"`) {
		t.Errorf("data-source-id landed on something other than .item-source: %s", src)
	}
	if !strings.Contains(out, `data-item-id="i1"`) {
		t.Errorf("item row lost its own data-item-id once .item-source carried data-source-id")
	}
}

func TestArticleEyebrowFeedTitleCarriesSourceID(t *testing.T) {
	it := &pb.Item{Id: "i1", Title: "Piece", SourceId: "s1", SourceTitle: "The Verge"}
	p := articleProps{bodies: map[string]*pb.Item{"i1": it}}
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return articleBlock(tr, it, p)
	})

	src := elementTag(t, out, `data-source-id="s1"`)
	if !strings.Contains(src, `class="item-source"`) {
		t.Errorf("the article eyebrow's feed title lost data-source-id: %s", src)
	}
}

// The tag chip's label and its × are two distinct hit targets on purpose
// (see tagChip's doc comment): the label routes to the tag view, the × still
// removes the tag from the feed. They must not share one element, or a click
// on either one would fire both.
func TestTagChipSplitsLabelAndRemoveIntoSeparateTargets(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return tagChip(tr, tagRef{Label: "Rust", Name: "rust", ID: "tag-1"}, "item-1")
	})

	label := elementTag(t, out, `data-tag-id="tag-1"`)
	if strings.Contains(label, "data-action") {
		t.Errorf("the tag label carries data-action — a click on it would also remove the tag: %s", label)
	}

	x := elementTag(t, out, `data-action="remove-tag"`)
	if strings.Contains(x, "data-tag-id") {
		t.Errorf("the remove control carries data-tag-id — a click on it would also navigate: %s", x)
	}
	if !strings.Contains(x, `data-for-item="item-1"`) || !strings.Contains(x, `data-value="rust"`) {
		t.Errorf("the remove control lost data-for-item/data-value: %s", x)
	}
}

// A pending tag (not yet acknowledged by the server) offers no × and must
// not offer a navigable label either — there is nothing on the server to
// open yet.
func TestPendingTagChipHasNoRoutingOrRemoveAttribute(t *testing.T) {
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return tagChip(tr, tagRef{Label: "Rust", Name: "rust", Pending: true}, "item-1")
	})
	if strings.Contains(out, "data-tag-id") || strings.Contains(out, "data-action") {
		t.Errorf("a pending tag chip carries a routing or remove attribute: %s", out)
	}
}
