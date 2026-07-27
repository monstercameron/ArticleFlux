//go:build js && wasm

package view

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Article classification: the category chip on a list row (panes.go's
// itemRow) and in the article pane (panes.go's articleBlock), plus the pure
// lookups both of them and the Classification settings tab share.
//
// # Why this is its own file
//
// itemRow and articleBlock already exist in panes.go and only need one call
// each; the substance — 26 names, a colour lookup, the chip's own markup, the
// quieter secondary and genre variants — lives here so the two call sites stay
// small. See client/design/classify.go for the colour ramp and its CSS, and
// client/i18n/en_classify.go for the catalog these read from.

// categoryName resolves a slug to its short display name.
//
// A switch over literal keys, not tr.T("category", slug) with slug as a
// runtime value, and that is a deliberate cost: client/i18n/keycoverage_test.go
// walks client/view as SOURCE and can only see string LITERALS passed to
// T — a computed key is invisible to it, which is exactly why dynamicPrefixes
// exists as an escape hatch for keys that genuinely cannot be literals (see
// glyphName in tagsettings.go). This table is 26 fixed, compile-time-known
// slugs, so writing it as literals costs one switch and buys real coverage:
// deleting an entry from client/i18n/en_classify.go without deleting its case
// here fails TestEveryReferencedKeyExists instead of silently rendering
// "category.software" to a reader.
//
// An unrecognised slug — a category this build predates — falls back to the
// slug itself Title-cased-by-nothing rather than a translated string, which is
// honest: there is no English word for it on file yet, and "category.filmtv"
// leaking to screen would be worse than the raw slug.
func categoryName(tr i18n.Runtime, slug string) string {
	switch slug {
	case "software":
		return tr.T("category", "software")
	case "ai":
		return tr.T("category", "ai")
	case "hardware":
		return tr.T("category", "hardware")
	case "security":
		return tr.T("category", "security")
	case "science":
		return tr.T("category", "science")
	case "health":
		return tr.T("category", "health")
	case "business":
		return tr.T("category", "business")
	case "finance":
		return tr.T("category", "finance")
	case "politics":
		return tr.T("category", "politics")
	case "world":
		return tr.T("category", "world")
	case "law":
		return tr.T("category", "law")
	case "climate":
		return tr.T("category", "climate")
	case "space":
		return tr.T("category", "space")
	case "energy":
		return tr.T("category", "energy")
	case "transport":
		return tr.T("category", "transport")
	case "gaming":
		return tr.T("category", "gaming")
	case "filmtv":
		return tr.T("category", "filmtv")
	case "music":
		return tr.T("category", "music")
	case "culture":
		return tr.T("category", "culture")
	case "books":
		return tr.T("category", "books")
	case "sport":
		return tr.T("category", "sport")
	case "food":
		return tr.T("category", "food")
	case "travel":
		return tr.T("category", "travel")
	case "design":
		return tr.T("category", "design")
	case "work":
		return tr.T("category", "work")
	case "education":
		return tr.T("category", "education")
	default:
		return slug
	}
}

// genreName resolves one of the 10 genres the same way. See categoryName for
// why this is a switch rather than a dynamic key.
func genreName(tr i18n.Runtime, genre string) string {
	switch genre {
	case "news":
		return tr.T("genre", "news")
	case "analysis":
		return tr.T("genre", "analysis")
	case "opinion":
		return tr.T("genre", "opinion")
	case "tutorial":
		return tr.T("genre", "tutorial")
	case "release":
		return tr.T("genre", "release")
	case "review":
		return tr.T("genre", "review")
	case "interview":
		return tr.T("genre", "interview")
	case "roundup":
		return tr.T("genre", "roundup")
	case "research":
		return tr.T("genre", "research")
	case "announcement":
		return tr.T("genre", "announcement")
	default:
		return genre
	}
}

// catStyleFor is hueVarFor's counterpart for a category: the inline style that
// carries CategoryColor into a chip via --cat, using the same style-STRING
// mechanism hueVarFor documents (GWC's Style map silently drops custom
// properties; a Raw style attribute does not).
//
// "" for an unknown slug — see design.CategoryColor — which leaves --cat
// unset, and every rule in classifyCSS falls back to a neutral tone rather
// than an invalid colour.
func catStyleFor(slug string) map[string]any {
	c := design.CategoryColor(slug)
	if c == "" {
		return nil
	}
	return map[string]any{"style": "--cat:" + c}
}

// categoryChip is the primary category, drawn wherever it appears.
//
// nil for an empty category — not an "Uncategorised" placeholder, not a
// zero-width chip that still reserves a gap. Roughly half of a real feed
// clears no category (docs/FEATURES.md §76: "None is the important one"), and
// a row that renders nothing for it is the row rendering the correct fact.
// Every call site relies on this returning nil rather than checking
// it.GetCategory() itself, so the empty case is handled in exactly one place.
// hiddenCats is the comma-joined set from classifySettingsProps.hiddenCats /
// reader.go's catHidden — see classify.go's csvHas doc for what it is and why
// it is a client-only display preference rather than a classification control.
func categoryChip(tr i18n.Runtime, it *pb.Item, hiddenCats string) ui.Node {
	slug := it.GetCategory()
	if slug == "" || csvHas(hiddenCats, slug) {
		return nil
	}
	return catChipNode(tr, slug, "cat-chip", categoryTitle(tr, it))
}

// categoryTitle is the chip's tooltip: the terms that earned it, or a note
// that the model chose it instead. Both are audit trails a reader can use to
// judge whether the category is right — the same instinct §76's editable
// terms/threshold screen is for, just read-only here.
func categoryTitle(tr i18n.Runtime, it *pb.Item) string {
	if it.GetCategoryByModel() {
		return tr.T("classify", "catByModel")
	}
	if reason := it.GetCategoryReason(); len(reason) > 0 {
		return tr.T("classify", "catReason", i18n.Args{"terms": strings.Join(reason, ", ")})
	}
	return ""
}

// secondaryCategoryChips is the 0-2 extra categories, drawn quieter than the
// primary — see design.classifyCSS's ".cat-chip-sec" comment for why a
// secondary must not carry the same visual weight as the one the server is
// actually confident about.
func secondaryCategoryChips(tr i18n.Runtime, it *pb.Item, hiddenCats string) []ui.Node {
	secs := it.GetSecondaryCategories()
	if len(secs) == 0 {
		return nil
	}
	out := make([]ui.Node, 0, len(secs))
	for _, slug := range secs {
		if slug == "" || csvHas(hiddenCats, slug) {
			continue
		}
		out = append(out, catChipNode(tr, slug, "cat-chip cat-chip-sec", ""))
	}
	return out
}

func catChipNode(tr i18n.Runtime, slug, class, title string) ui.Node {
	props := catStyleFor(slug)
	if props == nil {
		props = map[string]any{}
	}
	p := html.Props{Class: class, Key: "cat-" + slug, Raw: props}
	if title != "" {
		p.Title = title
	}
	return html.Span(p, html.Text(categoryName(tr, slug)))
}

// genreTag is the genre, in the article pane's chip row. Plain text in the
// muted running colour — the same treatment the byline and word count already
// get — never a coloured pill: genre is real information (news vs. opinion vs.
// tutorial changes how you read a piece) but it is a WEAKER signal than
// category, and drawing it with the same filled mark would make the row say
// two things with equal confidence when only one of them is a placement the
// reader can act on.
func genreTag(tr i18n.Runtime, it *pb.Item) ui.Node {
	g := it.GetGenre()
	if g == "" {
		return nil
	}
	return html.Span(html.Props{Class: "genre-tag"}, html.Text(genreName(tr, g)))
}

// classifyChips is the article pane's whole classification group — category,
// secondaries, genre — as one Fragment, so it is one child in
// articleBlock's fixed article-actions child list even though the secondaries
// are a variable-length slice underneath.
//
// Returns a real (possibly empty) Fragment even when nothing classified this
// article, rather than nil: html.Fragment tolerates zero children fine, and a
// stable node here (vs. sometimes nil, sometimes not) keeps this call site as
// plain as its neighbours instead of needing its own ui.If wrapper.
func classifyChips(tr i18n.Runtime, it *pb.Item, hiddenCats string) ui.Node {
	kids := make([]ui.Node, 0, 4)
	if chip := categoryChip(tr, it, hiddenCats); chip != nil {
		kids = append(kids, chip)
	}
	kids = append(kids, secondaryCategoryChips(tr, it, hiddenCats)...)
	if g := genreTag(tr, it); g != nil {
		kids = append(kids, g)
	}
	return html.Fragment(kids...)
}

// --- the settings tab's hide preference ------------------------------------
//
// Categories are engine-computed and, today, the same 26 for every reader on
// an instance — there is no RPC to disable one server-side, and inventing a
// per-reader classification override would be promising a capability that
// does not exist. What a reader CAN decide, entirely client-side through the
// preferences store every other view toggle already uses (SetPrefs), is
// whether they want to SEE a given category's chip. csvSet/csvHas/csvToggle
// are the same comma-joined-string-as-a-set idiom panes.go's openCats already
// uses, for the reason documented there: railProps and listProps are compared
// by value to skip re-rendering hundreds of rows, and a map field would defeat
// that by comparing unequal on every render.

// csvHas reports whether id is one of the comma-joined values in csv.
func csvHas(csv, id string) bool {
	for _, s := range strings.Split(csv, ",") {
		if s == id && s != "" {
			return true
		}
	}
	return false
}

// csvToggle adds or removes id from csv.
func csvToggle(csv, id string) string {
	var kept []string
	found := false
	for _, s := range strings.Split(csv, ",") {
		switch {
		case s == "":
		case s == id:
			found = true
		default:
			kept = append(kept, s)
		}
	}
	if !found {
		kept = append(kept, id)
	}
	return strings.Join(kept, ",")
}
