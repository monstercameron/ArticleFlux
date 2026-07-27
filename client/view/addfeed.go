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

	// --- the ladder (§11) ---------------------------------------------------
	//
	// Everything below appears only after "Add feed" found that the address is
	// not a feed. The form stays on screen above it: the category and the name
	// the reader already chose still apply to whatever they pick here, and
	// clearing the dialog to show a result would make them enter it twice.

	// looking is the free rungs running — a page fetch and a few probes.
	looking bool
	// candidates are feeds that were found AND parsed. Empty after a search
	// means the page genuinely publishes none.
	candidates []*pb.FeedCandidate
	// searched is whether a search has happened at all, which is what separates
	// "no feeds" from "not asked yet".
	searched bool
	// smartOn is this reader's standing consent for the model to read a page.
	// It is a preference rather than a per-press confirmation for the same
	// reason the Smart+ voice toggle is: the decision is about a capability,
	// and re-asking on every use trains people to click through it.
	smartOn bool
	// smartBusy is the model reading the page. Its own flag, not `busy`,
	// because the two say different things and can be true in sequence.
	smartBusy bool
	// smartStatus is why there is no proposal — see the proto's smart_status.
	smartStatus string
	proposal    *pb.ScrapeProposal
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
	// The ladder's controls. actAddCandidate carries the feed's URL in
	// data-value; actAddSmart is the standing consent; actAddAnalyze spends it.
	actAddCandidate = "add-feed-candidate"
	actAddSmart     = "add-feed-smart"
	actAddAnalyze   = "add-feed-analyze"
	actAddFollow    = "add-feed-follow"
)

// unfiledID is the sentinel for feeds with no category.
//
// A sentinel rather than a nil group, because "Unfiled" is a place a reader
// goes: it is the list of things they have not decided about, and a rail that
// only shows the decided ones hides exactly the feeds that need attention. It
// cannot collide with a real id — those are idgen's, not English.
const unfiledID = "__unfiled__"

// smartFollowPref is the standing consent for the model to read a page, held
// server-side with every other preference.
//
// The string is duplicated in internal/transport/grpcsrv because that is where
// it is ENFORCED — the client's copy decides what a toggle paints, and the
// server's decides whether anything egresses. A shared constant would be one
// import of the server package into the client to save nine characters, and the
// two are different jobs that happen to agree on a key.
const smartFollowPref = "smart.follow"

func addFeedDialog(tr i18n.Runtime, p addFeedProps) ui.Node {
	// The category picker: every category the reader has, plus none, plus a way
	// to make one. Chips rather than a select, for the same reason the feed
	// panel uses them — a dropdown hides the range of what is possible behind a
	// click, and this is a list a reader is meant to recognise, not search.
	chips := []ui.Node{
		pickChip(actAddFolder, "", tr.T("addFeed", "noCategory"), p.folderID == "" && !p.newOpen),
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
	}, html.Text(tr.T("addFeed", "newCategory"))))

	body := []ui.Node{
		afFieldWith(tr.T("addFeed", "urlLabel"),
			tr.T("addFeed", "urlHint"),
			smartLamp(tr, p.smartOn),
			html.Input(html.Props{
				Class: "field af-input", Type: "url",
				Placeholder: tr.T("addFeed", "urlPlaceholder"),
				Value:       p.url,
				OnInput:     p.onURLInput,
				Data:        map[string]string{"role": "add-feed"},
				Aria:        map[string]string{"label": tr.T("addFeed", "urlLabel")},
			})),
		afField(tr.T("addFeed", "categoryLabel"), tr.T("addFeed", "categoryHint"),
			html.Div(html.Props{Class: "af-chips"}, chips...)),
	}
	if p.newOpen {
		body = append(body, html.Div(html.Props{Class: "af-sub"},
			html.Input(html.Props{
				Class: "field af-input", Type: "text",
				Placeholder: tr.T("addFeed", "newCategoryPlaceholder"),
				Value:       p.newCategory,
				OnInput:     p.onNewInput,
				Data:        map[string]string{"role": "add-feed-category"},
				Aria:        map[string]string{"label": tr.T("addFeed", "newCategoryAria")},
			})))
	}
	body = append(body,
		afField(tr.T("addFeed", "nameLabel"), tr.T("addFeed", "nameHint"),
			html.Input(html.Props{
				Class: "field af-input", Type: "text",
				Placeholder: tr.T("addFeed", "namePlaceholder"),
				Value:       p.title,
				OnInput:     p.onTitleInput,
				Data:        map[string]string{"role": "add-feed-title"},
				Aria:        map[string]string{"label": tr.T("addFeed", "nameAria")},
			})),
	)

	body = append(body, ladder(tr, p)...)

	// The button says what it does and keeps saying it while it works: "Adding…"
	// is the same verb as "Add feed", so the state is legible as progress rather
	// than as a different control appearing.
	submit := tr.T("addFeed", "submit")
	switch {
	case p.looking:
		submit = tr.T("addFeed", "looking")
	case p.busy:
		submit = tr.T("addFeed", "working")
	}

	return scrim(p.open, actAddClose,
		// data-action on the dialog itself, for the reason feedSettings documents:
		// the delegated listener walks up to the nearest ancestor carrying one, so
		// without this a click on a text field resolves to the backdrop's close.
		html.Div(html.Props{Class: "af", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": tr.T("addFeed", "title")}},
			html.Div(html.Props{Class: "af-head"},
				html.Span(html.Props{Class: "af-mark"}, html.Text(tr.T("addFeed", "title"))),
				actionButton(actAddClose, "btn btn-ghost af-close", tr.T("addFeed", "close")),
			),
			html.Div(html.Props{Class: "af-body"}, body...),
			html.Div(html.Props{Class: "af-foot"},
				ui.If(p.err != "", func() ui.Node {
					return html.Div(html.Props{Class: "af-error", Role: "alert"},
						html.Text(p.err))
				}),
				html.Div(html.Props{Class: "af-actions"},
					actionButton(actAddClose, "btn btn-ghost", tr.T("addFeed", "cancel")),
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

// ladder renders what happens after "Add feed" finds no feed at the address.
//
// Nothing here exists until it has to. A reader who pastes a feed URL — the
// overwhelmingly common case — never sees any of it, and that is the design: the
// ladder is a recovery path, and a recovery path that occupies the form before
// anything has gone wrong is an interface explaining itself to people who did
// not need it.
func ladder(tr i18n.Runtime, p addFeedProps) []ui.Node {
	if !p.searched && !p.looking {
		return nil
	}
	if p.looking {
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"},
			html.Div(html.Props{Class: "af-eyebrow"}, html.Text(tr.T("addFeed", "looking"))),
			html.Div(html.Props{Class: "sk sk-line sk-w-70"}),
			html.Div(html.Props{Class: "sk sk-line sk-w-90"}),
		)}
	}

	// Rungs 1-3 found something. The reader chooses, rather than the first hit
	// being subscribed for them: a site's comments feed and its articles feed
	// are both "found", and picking the wrong one is a subscription they have to
	// notice and undo.
	if len(p.candidates) > 0 {
		rows := make([]ui.Node, 0, len(p.candidates))
		for _, c := range p.candidates {
			how := tr.T("addFeed", "probed")
			if c.GetHow() == "declared" {
				how = tr.T("addFeed", "declared")
			}
			rows = append(rows, html.Div(html.Props{
				Class: "af-cand", Key: "cand-" + c.GetUrl(),
			},
				html.Div(html.Props{Class: "af-cand-text"},
					html.Span(html.Props{Class: "af-cand-title"}, html.Text(c.GetTitle())),
					html.Span(html.Props{Class: "af-cand-meta"},
						html.Text(tr.T("addFeed", "candidateItems", i18n.Count(int(c.GetItemCount())))+
							" · "+how)),
				),
				html.Button(html.Props{
					Class: "btn af-cand-go",
					Raw: map[string]any{
						"data-action": actAddCandidate,
						"data-value":  c.GetUrl(),
					},
				}, html.Text(tr.T("addFeed", "useThis"))),
			))
		}
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"},
			append([]ui.Node{
				html.Div(html.Props{Class: "af-ladder-title"},
					html.Text(tr.T("addFeed", "foundTitle"))),
				html.Div(html.Props{Class: "af-hint"}, html.Text(tr.T("addFeed", "foundHint"))),
			}, rows...)...,
		)}
	}

	// Rung 5. The proposal wins the space when there is one, because at that
	// point the question is no longer "can this be followed" but "is this it".
	if p.proposal != nil {
		return []ui.Node{proposalBlock(tr, p)}
	}

	kids := []ui.Node{
		html.Div(html.Props{Class: "af-ladder-title"}, html.Text(tr.T("addFeed", "noFeedTitle"))),
		html.Div(html.Props{Class: "af-hint"}, html.Text(tr.T("addFeed", "noFeedHint"))),
	}
	switch p.smartStatus {
	case "no_key":
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartNoKey"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"}, kids...)}
	case "refused":
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartRefused"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"}, kids...)}
	case "js_rendered":
		// No lamp, no button, no retry: this one is not a failure to keep
		// pushing at. Offering "Try again" here would be offering to spend money
		// on the same answer.
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartJSRendered"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"}, kids...)}
	case "failed":
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartFailed"))))
	}

	// Off: no button. The control that unblocks this is the lamp on the address
	// row, and a disabled button here would be a second place to press, one of
	// which does nothing. The sentence points at the one that does — and it is
	// the sentence that says what leaving the machine means, at the moment the
	// decision is made.
	if !p.smartOn {
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartOffHint"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"}, kids...)}
	}

	// On: the analysis has already run — pressing Add feed with the lamp lit is
	// the whole gesture. This button is the RETRY, which is why it only appears
	// once there is something to retry, and why it is quiet rather than primary.
	analyze := tr.T("addFeed", "smartAnalyze")
	if p.smartBusy {
		analyze = tr.T("addFeed", "smartWorking")
	}
	kids = append(kids, html.Div(html.Props{Class: "af-smart"},
		html.Button(html.Props{
			Class: "btn af-retry",
			Raw:   map[string]any{"data-action": actAddAnalyze},
			Aria:  map[string]string{"busy": strconv.FormatBool(p.smartBusy)},
		}, html.Text(analyze)),
	))
	return []ui.Node{html.Div(html.Props{Class: "af-ladder"}, kids...)}
}

// proposalBlock is the model's answer, presented as evidence rather than as a
// claim.
//
// The order is deliberate: what it found, then what it extracted, then — last
// and quietest — the selectors it used. A reader who does not read CSS gets an
// answer from the samples alone; one who does can check the rule without asking
// anybody. Neither is asked to trust a confidence score, which is why there is
// none: a number a model assigns to its own answer is not evidence.
func proposalBlock(tr i18n.Runtime, p addFeedProps) ui.Node {
	prop := p.proposal
	samples := make([]ui.Node, 0, len(prop.GetSamples()))
	for i, s := range prop.GetSamples() {
		when := tr.T("addFeed", "proposalNoDate")
		if s.GetPublishedAt() != "" {
			if rel := relTime(tr, s.GetPublishedAt()); rel != "" {
				when = rel
			}
		}
		samples = append(samples, html.Div(html.Props{
			Class: "af-sample", Key: "sample-" + strconv.Itoa(i),
		},
			html.Span(html.Props{Class: "af-sample-title"}, html.Text(s.GetTitle())),
			html.Span(html.Props{Class: "af-sample-when"}, html.Text(when)),
		))
	}

	rule := prop.GetRule()
	// The two dialects read differently and are labelled differently: CSS
	// selectors MATCH elements, paths READ fields. A reader checking the rule is
	// checking a different kind of claim in each case, and one word of
	// vocabulary is cheaper than explaining that.
	isJSON := rule.GetKind() == "json"
	label := tr.T("addFeed", "proposalRule")
	if isJSON {
		label = tr.T("addFeed", "proposalPaths")
	}
	return html.Div(html.Props{Class: "af-ladder"},
		html.Div(html.Props{Class: "af-ladder-title"},
			html.Text(tr.T("addFeed", "proposalTitle"))),
		html.Div(html.Props{Class: "af-hint"},
			html.Text(tr.T("addFeed", "proposalFound", i18n.Count(int(prop.GetFound()))))),
		ui.If(isJSON, func() ui.Node {
			return html.Div(html.Props{Class: "af-note"},
				html.Text(tr.T("addFeed", "proposalJSON")))
		}),
		ui.If(prop.GetNotes() != "", func() ui.Node {
			return html.Div(html.Props{Class: "af-note"}, html.Text(prop.GetNotes()))
		}),
		html.Div(html.Props{Class: "af-samples"}, samples...),
		// The rule itself, last and small. It is the audit trail: a reader who
		// suspects the wrong list was picked can see `nav a@href` sitting there
		// and know immediately.
		html.Div(html.Props{Class: "af-rule"},
			html.Span(html.Props{Class: "af-rule-label"}, html.Text(label)),
			html.Code(html.Props{Class: "af-rule-code"},
				html.Text(rule.GetItemSelector()+" › "+rule.GetTitleSelector()+
					" · "+linkOf(rule))),
		),
		// Where a json rule fetches from, shown because it is a second address
		// the reader is agreeing to — the sidebar will say the page, and this is
		// what gets polled.
		ui.If(isJSON && rule.GetDataUrl() != "", func() ui.Node {
			return html.Div(html.Props{Class: "af-rule"},
				html.Span(html.Props{Class: "af-rule-label"},
					html.Text(tr.T("addFeed", "proposalData"))),
				html.Code(html.Props{Class: "af-rule-code"}, html.Text(rule.GetDataUrl())),
			)
		}),
		html.Div(html.Props{Class: "af-actions"},
			html.Button(html.Props{
				Class: "btn af-go",
				Raw:   map[string]any{"data-action": actAddFollow},
				Aria:  map[string]string{"busy": strconv.FormatBool(p.busy)},
			}, html.Text(tr.T("addFeed", "proposalFollow"))),
		),
	)
}

// linkOf is the rule's link, which a json rule may express as a template rather
// than as a path.
func linkOf(r *pb.ScrapeRule) string {
	if r.GetLinkSelector() != "" {
		return r.GetLinkSelector()
	}
	return r.GetLinkTemplate()
}

// afField is one labelled control: an eyebrow, the control, and the sentence
// that says what it is for.
//
// The hint sits UNDER the control rather than under the label, so the eye goes
// label → control → explanation, and a reader who already knows what to type
// never has to read past the field to get to it.
func afField(label, hint string, control ui.Node) ui.Node {
	return afFieldWith(label, hint, nil, control)
}

// afFieldWith is afField with something on the far end of the label row.
//
// One caller: the address field, which carries the Smart+ lamp. It belongs on
// THAT row and not in the dialog's header, because the capability is about the
// address — what happens when the thing you typed turns out not to be a feed —
// and a control in the header would be making a claim about the whole dialog.
func afFieldWith(label, hint string, aside, control ui.Node) ui.Node {
	return html.Div(html.Props{Class: "af-field"},
		html.Div(html.Props{Class: "af-eyebrow-row"},
			html.Span(html.Props{Class: "af-eyebrow"}, html.Text(strings.ToUpper(label))),
			aside,
		),
		control,
		html.Span(html.Props{Class: "af-hint"}, html.Text(hint)),
	)
}

// smartLamp is the Smart+ state, on the address row, always visible.
//
// A LAMP rather than a switch, and the distinction is the design: it reports
// first and toggles second. Off is a hollow ring in the muted grey; on fills it
// with the app's amber and lifts the label to cream — the same "a lit dot means
// this capability is live" idea the connection indicator already teaches, rather
// than a second vocabulary for the same fact.
//
// It is deliberately not a chip. Chips in this app are choices among peers, and
// this is not one of several options — it is a capability that is either armed
// or not, and arming it changes what the button below does.
func smartLamp(tr i18n.Runtime, on bool) ui.Node {
	// The label is the setting's NAME and does not change with its state —
	// "Smart+ follow", not "Smart+ on". A control whose label IS its state has to
	// be read twice: once to learn what it is, once to learn what it is doing.
	// The dot answers the second question, and answers it by SHAPE — hollow
	// against filled — as well as by colour, so it survives a glance and it
	// survives being colour-blind.
	return html.Button(html.Props{
		Class: "af-lamp",
		Raw:   map[string]any{"data-action": actAddSmart},
		Title: tr.T("addFeed", "smartAria"),
		Aria: map[string]string{
			"pressed": strconv.FormatBool(on),
			"label":   tr.T("addFeed", "smartAria"),
		},
	},
		html.I(html.Props{Class: "af-lamp-dot", Aria: map[string]string{"hidden": "true"}}),
		html.Span(html.Props{Class: "af-lamp-label"}, html.Text(tr.T("addFeed", "smartName"))),
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

func categoryDialog(tr i18n.Runtime, p categoryProps) ui.Node {
	// What deleting costs, stated in the number that makes it concrete. "4 feeds
	// move to Unfiled" is checkable; "this cannot be undone" is a formula that
	// says nothing about this particular category.
	// Zero is its own message, not the plural's zero form: English has no
	// plural "zero" category, and "It has no feeds in it" is a different
	// sentence rather than a different inflection of the same one.
	fate := tr.T("category", "fateEmpty")
	if p.feeds > 0 {
		fate = tr.T("category", "fate", i18n.Count(p.feeds))
	}

	danger := actionButton(actCatDelete, "btn fs-danger", tr.T("category", "delete"))
	if p.confirm {
		danger = actionButton(actCatConfirm, "btn fs-danger", tr.T("category", "confirm"))
	}

	return scrim(p.open, actCatClose,
		html.Div(html.Props{Class: "af af-narrow", Role: "dialog",
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": tr.T("category", "title")}},
			html.Div(html.Props{Class: "af-head"},
				html.Span(html.Props{Class: "af-mark"}, html.Text(p.name)),
				actionButton(actCatClose, "btn btn-ghost af-close", tr.T("category", "close")),
			),
			html.Div(html.Props{Class: "af-body"},
				afField(tr.T("category", "nameLabel"), fate,
					html.Input(html.Props{
						Class: "field af-input", Type: "text",
						Placeholder: p.name,
						Value:       p.draft,
						OnInput:     p.onInput,
						Data:        map[string]string{"role": "category-name"},
						Aria:        map[string]string{"label": tr.T("category", "nameAria")},
					})),
			),
			html.Div(html.Props{Class: "af-foot"},
				ui.If(p.err != "", func() ui.Node {
					return html.Div(html.Props{Class: "af-error", Role: "alert"},
						html.Text(p.err))
				}),
				ui.If(p.confirm, func() ui.Node {
					return html.Div(html.Props{Class: "af-warn"},
						html.Text(tr.T("category", "confirmWarn", i18n.Args{"name": p.name})))
				}),
				html.Div(html.Props{Class: "af-actions"},
					danger,
					html.Div(html.Props{Class: "af-spring"}),
					actionButton(actCatClose, "btn btn-ghost", tr.T("category", "cancel")),
					html.Button(html.Props{
						Class: "btn af-go",
						Raw:   map[string]any{"data-action": actCatSave},
						Aria:  map[string]string{"busy": strconv.FormatBool(p.busy)},
					}, html.Text(tr.T("category", "save"))),
				),
			),
		),
	)
}
