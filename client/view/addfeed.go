//go:build js && wasm

package view

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

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
	// smartCategorizeOn is this reader's standing consent for the model to
	// suggest a category for the feed being added — the same shape as
	// smartOn/smartFollowPref, for a different request (subscribe.go's
	// smartCategorizePref). It has no busy/status of its own: unlike the
	// ladder, filing happens after Subscribe already succeeded, and its
	// result shows up as the list header's banner (panes.go), not here.
	smartCategorizeOn bool
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
	// actAddCategorizeSmart is the standing consent for the category
	// suggestion, its own lamp beside actAddSmart's (see smartLamp's callers).
	actAddCategorizeSmart = "add-feed-categorize-smart"
)

// The ids the dialogs' <label>s point at. See kitFieldProps.ID for why a field's id
// is a constant rather than derived.
const (
	fieldAddURL   = "af-url"
	fieldAddTitle = "af-title"
	fieldCatName  = "cat-name"
)

// catNewID is the category editor's id while it is making a folder rather than
// editing one (G6). A sentinel in the same slot a real id would occupy, for
// unfiledID's reason: the dialog is open exactly when the id is non-empty, and
// nothing has to exist on the server for it to be. It cannot collide with a
// folder id, which are uuids.
const catNewID = "+new"

// maxNameChars is the longest a folder name or a tag may be, as the field's
// maxlength (G19). It mirrors store.MaxFolderName and the tag cap in
// store.SetFeedTag — the client cannot import the store, so the number is
// repeated here, and a field that stops typing at the limit is kinder than a
// sentence after the fact. The server stays the authority.
const maxNameChars = 48

// maxlengthAttr is the Raw attribute that stops a field at the limit.
func maxlengthAttr() map[string]any {
	m := renameAttrs()
	m["maxlength"] = strconv.Itoa(maxNameChars)
	return m
}

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

// smartCategorizePref is the standing consent for the model to suggest a
// category for a newly-added feed, held server-side like smartFollowPref and
// duplicated here for the same reason: the client's copy decides what the
// lamp paints, the server's decides whether a request is ever sent.
const smartCategorizePref = "smart.categorize"

func addFeedDialog(tr i18n.Runtime, p addFeedProps) ui.Node {
	// The category picker: every category the reader has, plus none, plus a way
	// to make one. Chips rather than a select, for the same reason the feed
	// panel uses them — a dropdown hides the range of what is possible behind a
	// click, and this is a list a reader is meant to recognise, not search.
	chips := []ui.Node{
		kitPick(kitChipProps{Action: actAddFolder, Mini: true, Key: actAddFolder + "-" + "", Label: tr.T("addFeed", "noCategory")}, "", p.folderID == "" && !p.newOpen),
	}
	for _, f := range p.folders {
		chips = append(chips, kitPick(kitChipProps{Action: actAddFolder, Mini: true, Key: actAddFolder + "-" + f.GetId(), Label: f.GetName()}, f.GetId(),
			p.folderID == f.GetId() && !p.newOpen))
	}
	chips = append(chips, kitToggle(kitChipProps{Action: actAddNewCat, Extra: "chip-new", Key: "af-new", Label: tr.T("addFeed", "newCategory")}, p.newOpen))

	urlHint, nameHint := tr.T("addFeed", "urlHint"), tr.T("addFeed", "nameHint")
	body := []ui.Node{
		kitField(kitFieldProps{Look: kitFormDialog, Label: tr.T("addFeed", "urlLabel"), Hint: urlHint, ID: fieldAddURL,
			Aside: html.Div(html.Props{Class: "af-lamps"},
				smartLamp(tr, p.smartOn, actAddSmart,
					tr.T("addFeed", "smartName"), tr.T("addFeed", "smartAria")),
				smartLamp(tr, p.smartCategorizeOn, actAddCategorizeSmart,
					tr.T("addFeed", "categorizeSmartName"), tr.T("addFeed", "categorizeSmartAria")),
			),
			Control: html.Input(html.Props{
				Class: "field af-input", Type: "url", ID: fieldAddURL,
				Placeholder: tr.T("addFeed", "urlPlaceholder"),
				Value:       p.url,
				OnInput:     p.onURLInput,
				Data:        map[string]string{"role": "add-feed"},
				Aria:        invalidAria(fieldAria(fieldAddURL, urlHint), p.err != ""),
				// An address is not prose: a spell checker underlining every
				// hostname in red is noise on the one field the dialog is for.
				// "go" because Enter submits the dialog from here.
				Raw: map[string]any{"spellcheck": "false", "enterkeyhint": "go"},
			})}),
		kitSetting(kitSettingProps{Look: kitFormDialog, Label: tr.T("addFeed", "categoryLabel"), Hint: tr.T("addFeed", "categoryHint"),
			Control: html.Div(html.Props{Class: "af-chips"}, chips...)}),
	}
	if p.newOpen {
		body = append(body, html.Div(html.Props{Class: "af-sub"},
			html.Input(html.Props{
				Class: "field af-input", Type: "text",
				Placeholder: tr.T("addFeed", "newCategoryPlaceholder"),
				Value:       p.newCategory,
				OnInput:     p.onNewInput,
				Data:        map[string]string{"role": "add-feed-category"},
				// Its own name, because it has no visible label: it opens under
				// the folder chips as the answer to "＋ New".
				Aria: map[string]string{"label": tr.T("addFeed", "newCategoryAria")},
				Raw:  map[string]any{"autocomplete": "off", "enterkeyhint": "go"},
			})))
	}
	body = append(body,
		kitField(kitFieldProps{Look: kitFormDialog, Label: tr.T("addFeed", "nameLabel"), Hint: nameHint, ID: fieldAddTitle,
			Control: html.Input(html.Props{
				Class: "field af-input", Type: "text", ID: fieldAddTitle,
				Placeholder: tr.T("addFeed", "namePlaceholder"),
				Value:       p.title,
				OnInput:     p.onTitleInput,
				Data:        map[string]string{"role": "add-feed-title"},
				Aria:        fieldAria(fieldAddTitle, nameHint),
				// Off for renameAttrs' reason; "go" for the address field's.
				Raw: map[string]any{"autocomplete": "off", "enterkeyhint": "go"},
			})}),
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

	return kitDialog(kitDialogProps{Open: p.open, CloseAction: actAddClose, Class: "af", Title: tr.T("addFeed", "title")},
		// data-action on the dialog itself, for the reason feedSettings documents:
		// the delegated listener walks up to the nearest ancestor carrying one, so
		// without this a click on a text field resolves to the backdrop's close.
		kitDialogHead(kitDialogHeadProps{Prefix: "af", Title: tr.T("addFeed", "title"),
			Close: kitDialogClose("af", actAddClose, "", tr.T("addFeed", "close"))}),
		kitDialogBody("af", body...),
		kitDialogFoot("af", []ui.Node{kitFieldError(kitFormDialog, p.err)},
			kitAction(kitChipProps{Action: actAddClose, Look: kitChipBtn, Extra: "btn-ghost", Label: tr.T("addFeed", "cancel")}),
			kitAction(kitChipProps{Action: actAddSubmit, Look: kitChipBtn, Extra: "af-go", Busy: kitTriOf(p.busy), Label: submit}),
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
			// A feed the reader already follows is marked rather than offered: the
			// button would subscribe to what they have, and the receipt would say
			// "added" for something that was never missing.
			var act ui.Node = html.Button(html.Props{
				Class: "btn af-cand-go",
				Raw: map[string]any{
					"data-action": actAddCandidate,
					"data-value":  c.GetUrl(),
				},
			}, html.Text(tr.T("addFeed", "useThis")))
			if c.GetAlreadyFollowing() {
				act = html.Span(html.Props{Class: "af-cand-meta af-cand-have",
					Data: map[string]string{"role": "candidate-following"}},
					html.Text(tr.T("addFeed", "following")))
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
				act,
			))
		}
		return []ui.Node{html.Div(html.Props{Class: "af-ladder"},
			append([]ui.Node{
				// Every rung's title is an h3 under the dialog's h2: it is the
				// heading of what just appeared below a form the reader has
				// already been through, and a heading is what someone jumps to
				// to find out what changed.
				html.H3(html.Props{Class: "af-ladder-title"},
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

	// What this page can still offer depends on what this server can do, and the
	// hint says only that. It used to recommend Smart+ follow unconditionally, so a
	// reader on an instance with no key was told to try something that could not
	// run. no_key (the server has no key) and the two verdicts the model already
	// gave (refused, js_rendered) each have their own sentence below; the offer is
	// made only where the free rungs came back empty and the model was not ruled
	// out.
	hint := tr.T("addFeed", "noFeedHint")
	switch p.smartStatus {
	case "no_key", "refused", "js_rendered":
	default:
		hint += " " + tr.T("addFeed", "noFeedSmartOffer")
	}
	// A live region: the answer arrives after the reader pressed Add, and a
	// screen reader that heard nothing took "No feed here" for no answer at all.
	kids := []ui.Node{
		html.H3(html.Props{Class: "af-ladder-title"}, html.Text(tr.T("addFeed", "noFeedTitle"))),
		html.Div(html.Props{Class: "af-hint"}, html.Text(hint)),
	}
	switch p.smartStatus {
	case "no_key":
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartNoKey"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder", Role: "status"}, kids...)}
	case "refused":
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartRefused"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder", Role: "status"}, kids...)}
	case "js_rendered":
		// No lamp, no button, no retry: this one is not a failure to keep
		// pushing at. Offering "Try again" here would be offering to spend money
		// on the same answer.
		kids = append(kids, html.Div(html.Props{Class: "af-note"},
			html.Text(tr.T("addFeed", "smartJSRendered"))))
		return []ui.Node{html.Div(html.Props{Class: "af-ladder", Role: "status"}, kids...)}
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
		return []ui.Node{html.Div(html.Props{Class: "af-ladder", Role: "status"}, kids...)}
	}

	// On: the analysis has already run — pressing Add feed with the lamp lit is
	// the whole gesture. This button is the RETRY, which is why it only appears
	// once there is something to retry, and why it is quiet rather than primary.
	analyze := tr.T("addFeed", "smartAnalyze")
	if p.smartBusy {
		analyze = tr.T("addFeed", "smartWorking")
	}
	kids = append(kids, html.Div(html.Props{Class: "af-smart"},
		kitAction(kitChipProps{Action: actAddAnalyze, Look: kitChipBtn, Extra: "af-retry", Busy: kitTriOf(p.smartBusy), Label: analyze}),
	))
	return []ui.Node{html.Div(html.Props{Class: "af-ladder", Role: "status"}, kids...)}
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
		html.H3(html.Props{Class: "af-ladder-title"},
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
			kitAction(kitChipProps{Action: actAddFollow, Look: kitChipBtn, Extra: "af-go", Busy: kitTriOf(p.busy), Label: tr.T("addFeed", "proposalFollow")}),
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
func smartLamp(tr i18n.Runtime, on bool, action, name, aria string) ui.Node {
	// The label is the setting's NAME and does not change with its state —
	// "Smart+ follow", not "Smart+ on". A control whose label IS its state has to
	// be read twice: once to learn what it is, once to learn what it is doing.
	// The dot answers the second question, and answers it by SHAPE — hollow
	// against filled — as well as by colour, so it survives a glance and it
	// survives being colour-blind.
	//
	// Parameterised on action/name/aria rather than hard-coded to the follow
	// lamp, so the categorize lamp beside it (addFeedDialog's af-lamps row) is
	// the same control wearing different words rather than a second
	// near-identical function.
	//
	// The long sentence is the TITLE and nothing else. It used to be the
	// aria-label as well, which made a forty-word name for a two-word button: a
	// screen reader read the whole explanation every time focus passed through,
	// and the name somebody can see was buried at the front of it. As a title
	// alone it is the button's description — announced after the name and the
	// pressed state, once — and the name is the words on the lamp.
	return html.Button(html.Props{
		Class: "af-lamp",
		Raw:   map[string]any{"data-action": action},
		Title: aria,
		Aria:  map[string]string{"pressed": strconv.FormatBool(on)},
	},
		html.I(html.Props{Class: "af-lamp-dot", Aria: map[string]string{"hidden": "true"}}),
		html.Span(html.Props{Class: "af-lamp-label"}, html.Text(name)),
	)
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
	// creating is the same dialog in its "new folder" shape: no folder exists yet,
	// so there is nothing to delete and the primary button creates (G6).
	creating bool
	onInput  ui.Handler
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

	// The length is said in the hint, after what deleting costs (or alone when
	// there is nothing to delete yet), because the server's refusal arrives only
	// after the press (G19).
	limit := tr.T("category", "limit", i18n.Args{"max": strconv.Itoa(maxNameChars)})
	title, saveLabel, hint := p.name, tr.T("category", "save"), fate+" "+limit
	if p.creating {
		title, saveLabel, hint = tr.T("category", "titleNew"), tr.T("category", "create"), limit
	}

	// DataArmed stamps data-armed on the armed button, which is what the shared
	// Escape and press-outside rules look for (armedInlineSelector): Escape on the
	// armed delete now answers "no" and leaves the dialog open, and a press in the
	// name field disarms it (G15) — the first only closed the whole dialog, the
	// second left it armed.
	arm := kitArm(kitArmProps{Armed: p.confirm, DataArmed: true,
		Action: actCatDelete, ArmedAction: actCatConfirm,
		Label: tr.T("category", "delete"), ArmedLabel: tr.T("category", "confirm"),
		Look: kitChipBtn, Danger: true,
		Warn: tr.T("category", "confirmWarn", i18n.Args{"name": p.name}), WarnClass: "af-warn"})
	// Nothing to delete until the folder exists. nil, not omitted: the foot's
	// children are positional.
	var del ui.Node = arm.Button
	if p.creating {
		del = nil
	}

	fieldARIA := fieldAria(fieldCatName, hint)
	if p.err != "" {
		// The refused field says so itself, not only the sentence below it (G19).
		fieldARIA["invalid"] = "true"
	}

	return kitDialog(kitDialogProps{Open: p.open, CloseAction: actCatClose,
		Class: "af af-narrow", Title: tr.T("category", "title")},
		kitDialogHead(kitDialogHeadProps{Prefix: "af", Title: title,
			Close: kitDialogClose("af", actCatClose, "", tr.T("category", "close"))}),
		kitDialogBody("af",
			kitField(kitFieldProps{Look: kitFormDialog, Label: tr.T("category", "nameLabel"), Hint: hint, ID: fieldCatName,
				Control: html.Input(html.Props{
					Class: "field af-input", Type: "text", ID: fieldCatName,
					Placeholder: p.name,
					Value:       p.draft,
					OnInput:     p.onInput,
					Data:        map[string]string{"role": "category-name"},
					Aria:        fieldARIA,
					Raw:         maxlengthAttr(),
				})}),
		),
		kitDialogFoot("af", []ui.Node{kitFieldError(kitFormDialog, p.err),
			// An alert, for signOutGroup's reason: arming changes what the
			// next press on the red button does, and somebody who cannot see
			// its label change has to be told before they press it again.
			arm.Warn},
			del,
			html.Div(html.Props{Class: "af-spring"}),
			kitAction(kitChipProps{Action: actCatClose, Look: kitChipBtn, Extra: "btn-ghost", Label: tr.T("category", "cancel")}),
			kitAction(kitChipProps{Action: actCatSave, Look: kitChipBtn, Extra: "af-go", Busy: kitTriOf(p.busy), Label: saveLabel}),
		),
	)
}

// invalidAria marks a field as the one the last refusal was about. The add
// dialog has one field the server can refuse, so a refusal in the dialog is
// always about it; without the attribute a screen reader read the error text and
// then the address as though nothing were wrong with it.
func invalidAria(a map[string]string, bad bool) map[string]string {
	if bad {
		a["invalid"] = "true"
	}
	return a
}
