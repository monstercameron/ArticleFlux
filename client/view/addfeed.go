//go:build js && wasm

package view

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Adding a feed, and the categories it can be added to.
//
// It used to be a URL box and an Add button pinned to the foot of the rail. That
// is the fastest possible path for the one thing it could do — paste, Enter —
// and it had no room for the other two things a reader decides when they add a
// feed: what to call it, and where it goes. Both were reachable only afterwards,
// from a different panel, on a row they then had to find.
//
// So it is a dialog now, and the rail keeps a button where the box was. The cost
// is one click on the fast path; what it buys is a form where naming and filing
// happen at the moment the reader is already thinking about them, rather than
// being deferred to a tidy-up pass that never happens.
//
// Deliberately three fields and no more. Poll interval, cache depth and mute all
// belong to a feed you already have and have formed an opinion about — they live
// in the per-feed panel, and putting them here would make adding a feed a
// configuration exercise.

// addFeedProps is the dialog's whole state, lifted into Reader like every other
// pane's is. Hooks must be created unconditionally at the top level, and this
// component returns nil when closed.
type addFeedProps struct {
	open bool
	// url, title and newCategory are drafts held by Reader so they survive the
	// re-renders that typing in any one of them causes.
	url         string
	title       string
	newCategory string
	// folderID is the chosen category, empty for none. newOpen is whether the
	// reader is naming a new one, which is a separate state from having typed
	// into it — an empty new-category field with the row open still means "I am
	// making a category", and closing the row is how they change their mind.
	folderID string
	newOpen  bool
	folders  []*pb.Folder
	busy     bool
	// err is the server's refusal, in the dialog rather than in the toast: the
	// reader is looking here, the field they must fix is here, and a message
	// that appears somewhere else is a message that gets missed.
	err          string
	onURLInput   ui.Handler
	onTitleInput ui.Handler
	onNewInput   ui.Handler
}

// The delegated actions this dialog dispatches.
const (
	actAddOpen   = "add-feed-open"
	actAddClose  = "add-feed-close"
	actAddSubmit = "add-feed"
	// actAddFolder carries the chosen category in data-value; the empty value is
	// "No category", which is a choice like any other rather than an absence.
	actAddFolder = "add-feed-folder"
	actAddNewCat = "add-feed-new-category"
)

// unfiledID is the sentinel for feeds with no category.
//
// A sentinel rather than a nil group, because "Unfiled" is a place a reader
// goes: it is the list of things they have not decided about, and a rail that
// only shows the decided ones hides exactly the feeds that need attention. It
// cannot collide with a real id — those are idgen's, not English.
const unfiledID = "__unfiled__"

func addFeedDialog(p addFeedProps) ui.Node {
	if !p.open {
		return nil
	}

	// The category picker: every category the reader has, plus none, plus a way
	// to make one. Chips rather than a select, for the same reason the feed
	// panel uses them — a dropdown hides the range of what is possible behind a
	// click, and this is a list a reader is meant to recognise, not search.
	chips := []ui.Node{
		pickChip(actAddFolder, "", i18n.T("addFeed.noCategory"), p.folderID == "" && !p.newOpen),
	}
	for _, f := range p.folders {
		chips = append(chips, pickChip(actAddFolder, f.GetId(), f.GetName(),
			p.folderID == f.GetId() && !p.newOpen))
	}
	chips = append(chips, html.Button(html.Props{
		Class: "chip chip-new",
		Key:   "af-new",
		Raw:   map[string]any{"data-action": actAddNewCat},
		Aria:  map[string]string{"pressed": strconv.FormatBool(p.newOpen)},
	}, html.Text(i18n.T("addFeed.newCategory"))))

	body := []ui.Node{
		afField(i18n.T("addFeed.urlLabel"),
			i18n.T("addFeed.urlHint"),
			html.Input(html.Props{
				Class: "field af-input", Type: "url",
				Placeholder: i18n.T("addFeed.urlPlaceholder"),
				Value:       p.url,
				OnInput:     p.onURLInput,
				Data:        map[string]string{"role": "add-feed"},
				Aria:        map[string]string{"label": i18n.T("addFeed.urlLabel")},
			})),
		afField(i18n.T("addFeed.categoryLabel"), i18n.T("addFeed.categoryHint"),
			html.Div(html.Props{Class: "af-chips"}, chips...)),
	}
	if p.newOpen {
		body = append(body, html.Div(html.Props{Class: "af-sub"},
			html.Input(html.Props{
				Class: "field af-input", Type: "text",
				Placeholder: i18n.T("addFeed.newCategoryPlaceholder"),
				Value:       p.newCategory,
				OnInput:     p.onNewInput,
				Data:        map[string]string{"role": "add-feed-category"},
				Aria:        map[string]string{"label": i18n.T("addFeed.newCategoryAria")},
			})))
	}
	body = append(body,
		afField(i18n.T("addFeed.nameLabel"), i18n.T("addFeed.nameHint"),
			html.Input(html.Props{
				Class: "field af-input", Type: "text",
				Placeholder: i18n.T("addFeed.namePlaceholder"),
				Value:       p.title,
				OnInput:     p.onTitleInput,
				Data:        map[string]string{"role": "add-feed-title"},
				Aria:        map[string]string{"label": i18n.T("addFeed.nameAria")},
			})),
	)

	// The button says what it does and keeps saying it while it works: "Adding…"
	// is the same verb as "Add feed", so the state is legible as progress rather
	// than as a different control appearing.
	submit := i18n.T("addFeed.submit")
	if p.busy {
		submit = i18n.T("addFeed.working")
	}

	return html.Div(html.Props{
		Class: "pal-scrim",
		Raw:   map[string]any{"data-action": actAddClose},
	},
		// data-action on the dialog itself, for the reason feedSettings documents:
		// the delegated listener walks up to the nearest ancestor carrying one, so
		// without this a click on a text field resolves to the backdrop's close.
		html.Div(html.Props{Class: "af", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": i18n.T("addFeed.title")}},
			html.Div(html.Props{Class: "af-head"},
				html.Span(html.Props{Class: "af-mark"}, html.Text(i18n.T("addFeed.title"))),
				actionButton(actAddClose, "btn btn-ghost af-close", i18n.T("addFeed.close")),
			),
			html.Div(html.Props{Class: "af-body"}, body...),
			html.Div(html.Props{Class: "af-foot"},
				ui.If(p.err != "", func() ui.Node {
					return html.Div(html.Props{Class: "af-error", Role: "alert"},
						html.Text(p.err))
				}),
				html.Div(html.Props{Class: "af-actions"},
					actionButton(actAddClose, "btn btn-ghost", i18n.T("addFeed.cancel")),
					html.Button(html.Props{
						Class: "btn af-go",
						Raw:   map[string]any{"data-action": actAddSubmit},
						Aria:  map[string]string{"busy": strconv.FormatBool(p.busy)},
					}, html.Text(submit)),
				),
			),
		),
	)
}

// afField is one labelled control: an eyebrow, the control, and the sentence
// that says what it is for.
//
// The hint sits UNDER the control rather than under the label, so the eye goes
// label → control → explanation, and a reader who already knows what to type
// never has to read past the field to get to it.
func afField(label, hint string, control ui.Node) ui.Node {
	return html.Div(html.Props{Class: "af-field"},
		html.Span(html.Props{Class: "af-eyebrow"}, html.Text(strings.ToUpper(label))),
		control,
		html.Span(html.Props{Class: "af-hint"}, html.Text(hint)),
	)
}

// pickChip is a chip whose payload is a string rather than fsChoices' int, which
// is what a folder id is.
func pickChip(action, value, label string, pressed bool) ui.Node {
	return html.Button(html.Props{
		Class: "chip chip-mini",
		Key:   action + "-" + value,
		Raw:   map[string]any{"data-action": action, "data-value": value},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, html.Text(label))
}

// --- the category editor -----------------------------------------------------

// categoryProps is the rename-or-delete dialog, opened from a category's row.
//
// Creating a category is NOT here: it happens where a reader is already thinking
// about one — in the add-a-feed form, and from the ＋ on the rail's Categories
// band. A dialog whose only job is to name an empty container is a step between
// deciding and doing.
type categoryProps struct {
	open bool
	// id and name identify what is being edited; draft is the field.
	id      string
	name    string
	draft   string
	feeds   int
	busy    bool
	err     string
	confirm bool
	onInput ui.Handler
}

const (
	actCatClose   = "category-close"
	actCatSave    = "category-save"
	actCatDelete  = "category-delete"
	actCatConfirm = "category-delete-confirm"
)

func categoryDialog(p categoryProps) ui.Node {
	if !p.open {
		return nil
	}

	// What deleting costs, stated in the number that makes it concrete. "4 feeds
	// move to Unfiled" is checkable; "this cannot be undone" is a formula that
	// says nothing about this particular category.
	// Zero is its own message, not the plural's zero form: English has no
	// plural "zero" category, and "It has no feeds in it" is a different
	// sentence rather than a different inflection of the same one.
	fate := i18n.T("category.fateEmpty")
	if p.feeds > 0 {
		fate = i18n.N("category.fate", p.feeds)
	}

	danger := actionButton(actCatDelete, "btn fs-danger", i18n.T("category.delete"))
	if p.confirm {
		danger = actionButton(actCatConfirm, "btn fs-danger", i18n.T("category.confirm"))
	}

	return html.Div(html.Props{
		Class: "pal-scrim",
		Raw:   map[string]any{"data-action": actCatClose},
	},
		html.Div(html.Props{Class: "af af-narrow", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": i18n.T("category.title")}},
			html.Div(html.Props{Class: "af-head"},
				html.Span(html.Props{Class: "af-mark"}, html.Text(p.name)),
				actionButton(actCatClose, "btn btn-ghost af-close", i18n.T("category.close")),
			),
			html.Div(html.Props{Class: "af-body"},
				afField(i18n.T("category.nameLabel"), fate,
					html.Input(html.Props{
						Class: "field af-input", Type: "text",
						Placeholder: p.name,
						Value:       p.draft,
						OnInput:     p.onInput,
						Data:        map[string]string{"role": "category-name"},
						Aria:        map[string]string{"label": i18n.T("category.nameAria")},
					})),
			),
			html.Div(html.Props{Class: "af-foot"},
				ui.If(p.err != "", func() ui.Node {
					return html.Div(html.Props{Class: "af-error", Role: "alert"},
						html.Text(p.err))
				}),
				ui.If(p.confirm, func() ui.Node {
					return html.Div(html.Props{Class: "af-warn"},
						html.Text(i18n.T("category.confirmWarn", i18n.Args{"name": p.name})))
				}),
				html.Div(html.Props{Class: "af-actions"},
					danger,
					html.Div(html.Props{Class: "af-spring"}),
					actionButton(actCatClose, "btn btn-ghost", i18n.T("category.cancel")),
					html.Button(html.Props{
						Class: "btn af-go",
						Raw:   map[string]any{"data-action": actCatSave},
						Aria:  map[string]string{"busy": strconv.FormatBool(p.busy)},
					}, html.Text(i18n.T("category.save"))),
				),
			),
		),
	)
}
