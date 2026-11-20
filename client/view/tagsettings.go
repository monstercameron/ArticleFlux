//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/tagglyph"
)

// The per-tag settings panel, opened from the gear on a tag row.
//
// A tag feed is a row in the rail like any other, but it is not a feed: there is
// no publisher, no poll interval, no health, and nothing shared with anyone else
// on the server. What it does have is the two things a tag never had a place to
// carry — a name for the ROW that is not the name of the TAG, and a mark to find
// it by. So this panel is deliberately short. It reuses the feed panel's `fs-*`
// vocabulary rather than inventing a second dialog language, because a reader
// who has opened one settings panel in this app should recognise the next.
//
// The distinction the panel exists to make, and which every string in it is
// working to keep straight:
//
//	the TAG    "rust" — what you type, what the chip under an article says, and
//	                    the handle SetFeedTag takes. Nothing here changes it.
//	the ROW    "Systems programming" ⚛ — what the rail draws. Yours to set.
//
// Getting that backwards is the one way this feature can do damage, because a
// reader who believes they renamed the tag will go looking for the new word in
// the tag field and not find it.

type tagSettingsProps struct {
	open bool
	// t is the tag itself. Held rather than fetched: ListTags already returned
	// everything on this panel, so there is no loading state to render — the
	// dialog opens with its content, which is what a settings dialog should do.
	t *pb.Tag
	// draftLabel is the rename field, separate from t.Label so that typing
	// survives the re-render a glyph click causes.
	draftLabel  string
	onLabelEdit ui.Handler
	// feeds are the subscriptions carrying this tag, by name. The answer to
	// "what is actually in here", which is the question a reader opening a tag's
	// settings most often has.
	feeds  []string
	saving bool
	// err is a refused save, said in the panel beside the field it is about (G5).
	err string
}

// The panel's actions, on the same delegated data-action path as everything
// else. Prefixed `ts-` beside the feed panel's `fs-`.
const (
	actTagSettings      = "tag-settings"
	actTagSettingsClose = "tag-settings-close"
	// What openTagSettings focuses when the panel opens; see kitDialogClose.
	roleTagSettingsClose = "tag-settings-close-btn"
	actTagRename         = "ts-rename"
	actTagGlyph          = "ts-glyph"
	// fieldTagLabel is the rename field's id, which its <label> points at.
	fieldTagLabel = "ts-tag-label"
)

// glyphNone is the sentinel for "no glyph — use the section default". A value
// rather than a separate action, so the default sits in the same grid as the
// fifty and is pressed and unpressed by the same rule. A picker whose "clear"
// lives somewhere else is a picker where clearing is a thing you have to find.
const glyphNone = "__none__"

// tagGearRole is the data-role of a tag row's gear.
func tagGearRole(id string) string { return "tag-gear-" + id }

func tagSettings(tr i18n.Runtime, p tagSettingsProps) ui.Node {
	// No early return on p.t == nil: like its five siblings (feed settings,
	// add-feed, category, palette, help), this panel is rendered
	// unconditionally and leans on `scrim`'s data-open attribute to carry
	// open/closed — that is what lets the CSS transition run on the way out.
	// An early return here would tear the whole ".fs" subtree out of the DOM
	// in the same render that clears tsOpen, which is exactly what happened
	// before: the dialog vanished instead of animating out. Every field below
	// goes through p.t's generated getters, which are nil-safe, so there is
	// nothing to guard against.
	return kitDialog(kitDialogProps{Open: p.open, CloseAction: actTagSettingsClose,
		Class: "fs", Title: tr.T("tagSettings", "title")},
		// data-action on the dialog itself stops the delegated walk before it
		// reaches the backdrop's close — the same no-op the feed panel needs,
		// for the same reason.
		kitDialogHead(kitDialogHeadProps{Prefix: "fs", Before: []ui.Node{lead(tagGlyphOf(p.t))},
			Title: tagDisplay(p.t), After: []ui.Node{kitDialogSaving(p.saving, tr.T("tagSettings", "saving"))},
			Close: kitDialogClose("fs", actTagSettingsClose, roleTagSettingsClose, tr.T("tagSettings", "close"))}),
		kitDialogBody("fs", tagSettingsBody(tr, p)...),
	)
}

func tagSettingsBody(tr i18n.Runtime, p tagSettingsProps) []ui.Node {
	t := p.t
	id := t.GetId()
	// The cap is said beside the field (G19), and the field stops typing there.
	nameHint := tr.T("tagSettings", "nameLimit", i18n.Args{"max": strconv.Itoa(maxNameChars)}) +
		" " + tr.T("tagSettings", "nameHint")
	nameAria := fieldAria(fieldTagLabel, nameHint)
	if p.err != "" {
		nameAria["invalid"] = "true"
	}

	out := []ui.Node{
		kitHeading(glyphYours, tr.T("tagSettings", "rowGroup"), tr.T("tagSettings", "rowGroupHint")),

		// The rename. Enter commits and so does the button, for the same reason
		// the feed panel's does: a field whose only commit is a keystroke nobody
		// mentioned is a field that looks broken.
		kitField(kitFieldProps{Label: tr.T("tagSettings", "nameLabel"), Hint: nameHint, ID: fieldTagLabel,
			Control: html.Div(html.Props{Class: "fs-rename"},
				html.Input(html.Props{
					Class: "field fs-field", Type: "text", ID: fieldTagLabel,
					Placeholder: t.GetName(),
					Value:       p.draftLabel,
					OnInput:     p.onLabelEdit,
					Data:        map[string]string{"role": "tag-label"},
					Aria:        nameAria,
					Raw:         maxlengthAttr(),
				}),
				itemAction(actTagRename, tr.T("tagSettings", "rename"), id),
			)}),
		kitFieldError(kitFormSettings, p.err),

		// The tag itself, stated as a FACT rather than as a disabled field.
		//
		// A greyed-out input says "you may not edit this", which invites the
		// question of who may. This says what the string is FOR, which answers
		// the question instead — and it is the only place in the app a reader
		// can see both names side by side and understand that renaming the row
		// left the tag where it was.
		kitSetting(kitSettingProps{Label: tr.T("tagSettings", "tagLabel"), Hint: tr.T("tagSettings", "tagHint"),
			Control: kitChipGroup(kitGroupTags, "", kitStatic(kitStaticProps{Label: t.GetName(), Mini: true}))}),

		kitHeading(glyphTags, tr.T("tagSettings", "glyphGroup"),
			tr.T("tagSettings", "glyphGroupHint")),
		tagGlyphPicker(tr, id, t.GetGlyph()),
	}

	// What is in the tag. Not a control — a tag's membership is edited from the
	// article or the feed panel, where a reader is looking at the feed they mean
	// — but a reader who has forgotten what they filed under a label should not
	// have to click through the list to find out.
	out = append(out, kitHeading(glyphFeeds, tr.T("tagSettings", "feedsGroup"), ""))
	switch {
	case len(p.feeds) == 0:
		out = append(out, html.Div(html.Props{Class: "fs-note"},
			html.Text(tr.T("tagSettings", "feedsEmpty"))))
	default:
		chips := make([]ui.Node, 0, len(p.feeds))
		for _, f := range p.feeds {
			chips = append(chips, kitStatic(kitStaticProps{Label: f, Key: "ts-feed-" + f, Mini: true}))
		}
		out = append(out,
			kitChipGroup(kitGroupTags, "", chips...),
			html.Div(html.Props{Class: "fs-note"},
				html.Text(tr.T("tagSettings", "feedsNote"))))
	}
	return out
}

// tagGlyphPicker is the fifty, grouped.
//
// A grid rather than a select: the whole value of a glyph is that it is
// recognised rather than read, and a control that shows one option at a time
// makes choosing between fifty of them a memory exercise. Grouped rather than
// flat because fifty ungrouped symbols is a wall — a reader scans until they
// tire and takes whatever is under the cursor, which is how every tag ends up
// wearing one of the first six.
//
// Each button carries data-value, which is the same attribute the segmented
// controls use, so no new plumbing: the delegated listener already reports it.
//
// # One Tab stop (H14)
//
// Fifty-one buttons were fifty-one Tab stops, so the dialog's footer was fifty
// presses away. Every cell is tabindex -1 except the stop, which is the chosen
// mark (or "None" when the tag wears the default, or its mark is not in the set):
// decided here, in the markup, from the same value that paints aria-pressed, so
// it cannot disagree with it and needs no effect after the render. The arrows
// move between cells (reader_keyboard.go, platform.GridMove).
func tagGlyphPicker(tr i18n.Runtime, tagID, current string) ui.Node {
	stop := glyphNone
	for _, group := range tagglyph.Groups() {
		for _, g := range tagglyph.In(group) {
			if g.Char == current {
				stop = current
			}
		}
	}
	rows := []ui.Node{
		// "None" first and inside the grid, so restoring the default is one of
		// the choices rather than an escape hatch beside them.
		html.Div(html.Props{Class: "ts-glyph-row"},
			tagGlyphButton(tagID, glyphNone, glyphTags, tr.T("tagSettings", "glyphDefault"), current == "", stop == glyphNone),
		),
	}
	for gi, group := range tagglyph.Groups() {
		kids := make([]ui.Node, 0, 12)
		for _, g := range tagglyph.In(group) {
			kids = append(kids, tagGlyphButton(tagID, g.Char, g.Char, glyphName(tr, g), g.Char == current, g.Char == stop))
		}
		// Each block of symbols is a group named by the title over it, so a
		// reader moving through fifty buttons hears "Arrows" as they enter the
		// arrows rather than meeting the word once as loose text and then twelve
		// buttons with no indication of where one family ends.
		// By position rather than by the group's id, which is English with
		// spaces in it ("Odds & ends") — and aria-labelledby reads a space as
		// the gap between two ids.
		titleID := "tsg-title-" + strconv.Itoa(gi)
		rows = append(rows,
			html.Div(html.Props{Class: "ts-glyph-group", Key: "tsg-" + group,
				Role: "group", Aria: map[string]string{"labelledby": titleID}},
				html.Span(html.Props{Class: "ts-glyph-title", ID: titleID},
					html.Text(glyphGroupName(tr, group))),
				html.Div(html.Props{Class: "ts-glyph-row"}, kids...),
			))
	}
	// The whole grid is one named group too: it is a single choice — one mark
	// per tag — spread over several families, and the name is what says so.
	return html.Div(html.Props{Class: "ts-glyphs", Role: "group",
		Aria: map[string]string{"label": tr.T("tagSettings", "glyphGroup")}}, rows...)
}

// tagGlyphButton is one cell. The NAME is the accessible label and the tooltip:
// a grid of fifty unlabelled symbols is unusable without a pointer and
// unreadable with a screen reader, and the name is the only thing that tells ◆
// from ◈ for anyone who cannot see the difference at 13px.
func tagGlyphButton(tagID, value, shown, name string, pressed, stop bool) ui.Node {
	tab := "-1"
	if stop {
		tab = "0"
	}
	return html.Button(html.Props{
		Class: "ts-glyph",
		Key:   "tsg-" + value,
		Raw: map[string]any{
			"data-action":   actTagGlyph,
			"data-for-item": tagID,
			"data-value":    value,
			"tabindex":      tab,
		},
		Title: name,
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed), "label": name},
	}, html.Text(shown))
}

// --- resolution ---------------------------------------------------------------
//
// One rule each, in one place. The fallback is the kind of thing every call site
// gets right for a month and then one of them does not, at which point a renamed
// tag shows its old name in exactly one corner of the app and nobody can say
// why.

// tagDisplay is what a tag's row, its scope title and its chips are labelled
// with: the reader's own name for it, or the tag's name when they have not set
// one.
func tagDisplay(t *pb.Tag) string {
	if t == nil {
		return ""
	}
	if l := strings.TrimSpace(t.GetLabel()); l != "" {
		return l
	}
	return t.GetName()
}

// tagGlyphOf is the mark a tag's row wears: its own, or the section's.
func tagGlyphOf(t *pb.Tag) string {
	if t == nil {
		return glyphTags
	}
	if g := t.GetGlyph(); g != "" {
		return g
	}
	return glyphTags
}

// tagByID finds a tag in the list the rail already holds. Linear over at most
// store.MaxTagsPerUser entries, which is cheaper than the map it would take to
// avoid it.
func tagByID(tags []*pb.Tag, id string) *pb.Tag {
	// The empty case is the common one — the panel is shut — and it is on the
	// render path, so it does not get to walk the list to discover that.
	if id == "" {
		return nil
	}
	for _, t := range tags {
		if t.GetId() == id {
			return t
		}
	}
	return nil
}

// glyphName and glyphGroupName resolve a mark's label out of the catalog,
// keyed by the character and the group id respectively.
//
// Both fall back to internal/tagglyph's own English rather than rendering the
// key: a glyph added to that package without a catalog entry should show a
// slightly wrong language, not "glyph.◆" in a tooltip. Neither key is a
// literal, so keycoverage cannot see them — which is precisely why the
// fallback is here and why "glyph." and "glyphGroup." are listed in
// dynamicPrefixes.
func glyphName(tr i18n.Runtime, g tagglyph.Glyph) string {
	if s := tr.T("glyph", g.Char); s != "glyph."+g.Char {
		return s
	}
	return g.Name
}

func glyphGroupName(tr i18n.Runtime, group string) string {
	if s := tr.T("glyphGroup", group); s != "glyphGroup."+group {
		return s
	}
	return group
}
