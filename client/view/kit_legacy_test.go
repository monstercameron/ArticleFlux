//go:build js && wasm

package view

// Frozen copies of the helpers the kits replaced, kept ONLY so the adoption
// tests (kit_*_test.go) keep comparing each kit against the bytes the old
// code produced. Nothing outside tests may call these.

import (
	"strconv"
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// legacyActionChip is glyphChip for a press that DOES something — Fetch all, Reload,
// Export — rather than one that is on or off. It is panes.go's glyphItemAction
// without an item, for the same reason: any aria-pressed, "false" included,
// makes a button a toggle, and "Fetch all, toggle button, not pressed" is a
// switch somebody might reasonably try to turn on.
func legacyActionChip(action, glyph, label string) ui.Node {
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action},
	}, lead(glyph), html.Text(label))
}

// legacyGlyphItemAction is glyphItemChip for a control that DOES something rather
// than being on or off: Listen, Pause, Stop, Mark unread.
//
// The difference is the missing aria-pressed, and it is not cosmetic. Any
// aria-pressed at all, "false" included, turns a button into a toggle button,
// so Stop was announced as "Stop, toggle button, not pressed" — a switch that
// is off, which is a thing a reader might reasonably turn on to find out what
// it does. A plain button says it acts when pressed, which is the truth.
func legacyGlyphItemAction(action, glyph, label, itemID string) ui.Node {
	return html.Button(html.Props{
		Class:     "chip",
		DataAttrs: []html.DataAttribute{{Name: "action", Value: action}, {Name: "for-item", Value: itemID}},
	}, lead(glyph), html.Text(label))
}

// legacyGlyphChip is `chip` with a leading glyph.
func legacyGlyphChip(action, glyph, label string, pressed bool) ui.Node {
	return html.Button(html.Props{
		Class:    "chip",
		DataAttr: html.DataAttribute{Name: "action", Value: action},
		Aria:     map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, lead(glyph), html.Text(label))
}

// legacyGlyphItemChip is `itemChip` with a leading glyph, for the per-article controls.
func legacyGlyphItemChip(action, glyph, label string, pressed bool, itemID string) ui.Node {
	return html.Button(html.Props{
		Class:     "chip",
		DataAttrs: []html.DataAttribute{{Name: "action", Value: action}, {Name: "for-item", Value: itemID}},
		Aria:      map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, lead(glyph), html.Text(label))
}

// legacyItemChip is a chip that acts on a named article rather than on "the" article.
func legacyItemChip(action, label string, pressed bool, itemID string) ui.Node {
	return html.Button(html.Props{
		Class:     "chip",
		DataAttrs: []html.DataAttribute{{Name: "action", Value: action}, {Name: "for-item", Value: itemID}},
		Aria:      map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, html.Text(label))
}

// legacyChip renders one pill control, dispatched by data-action like every other
// button here so the pane stays hook-free.
func legacyChip(action, label string, pressed bool) ui.Node {
	return kitToggle(kitChipProps{Action: action, Label: label}, pressed)
}

// legacyPickChip is a chip whose payload is a string rather than fsChoices' int, which
// is what a folder id is.
func legacyPickChip(action, value, label string, pressed bool) ui.Node {
	return html.Button(html.Props{
		Class: "chip chip-mini",
		Key:   action + "-" + value,
		Raw:   map[string]any{"data-action": action, "data-value": value},
		Aria:  map[string]string{"pressed": strconv.FormatBool(pressed)},
	}, html.Text(label))
}

// legacyFsToggle is a two-state switch rendered as one button, so its label always
// states the CURRENT state rather than the action — which is what a reader
// glancing at a settings panel is trying to read off it.
func legacyFsToggle(action, id string, on bool, whenOn, whenOff string) ui.Node {
	label := whenOff
	if on {
		label = whenOn
	}
	return html.Button(html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action, "data-for-item": id},
		Aria:  map[string]string{"pressed": strconv.FormatBool(on)},
	}, html.Text(label))
}

// legacySmartToggle is fsToggle with a disabled state.
//
// A disabled switch rather than an absent one, because "off" and "unavailable" are
// different facts and a reader who cannot tell them apart concludes the feature is broken.
// The label still reports the STATE rather than the action, which is fsToggle's rule and
// the right one for something being glanced at.
func legacySmartToggle(action string, on, disabled bool, whenOn, whenOff string) ui.Node {
	label := whenOff
	if on {
		label = whenOn
	}
	props := html.Props{
		Class: "chip",
		Raw:   map[string]any{"data-action": action},
		Aria:  map[string]string{"pressed": strconv.FormatBool(on)},
	}
	if disabled {
		props.Disabled = true
	}
	return html.Button(props, html.Text(label))
}

// legacyStaticChip reports a fact rather than offering an action.
func legacyStaticChip(label string) ui.Node {
	return kitStatic(kitStaticProps{Label: label})
}

// legacyFsGroup heads one group of rows — in a dialog, and on every Settings tab.
//
// The title is a HEADING (h3) and it is given in the case it was written in.
//
// A heading, because that is the structure it draws: a Settings tab is five or
// six of these down a page, and as spans there was nothing between the page's
// h1 and its last control for anyone navigating by headings to land on. Level
// three in both places it is used: under a dialog's own title, and under the
// tab's name on the Settings page (see settingsPane's hidden h2).
//
// Not upper-cased here any more. `strings.ToUpper` put capitals in the DOCUMENT,
// where the stylesheet's `text-transform` puts them only on the screen — and the
// difference is audible: a screen reader meets "SYMBOL" and may spell it, and
// ToUpper is wrong outright in languages whose casing is not ASCII's (a Turkish
// "i" does not become "I"). The small-caps look is a decision about type, so it
// lives in design/sheet.go with the rest of them.
func legacyFsGroup(glyph, title, note string) ui.Node {
	return html.Div(html.Props{Class: "fs-group"},
		html.Div(html.Props{Class: "fs-group-head"},
			lead(glyph),
			html.H3(html.Props{Class: "fs-group-title"}, html.Text(title)),
		),
		ui.If(note != "", func() ui.Node {
			return html.Span(html.Props{Class: "fs-group-note"}, html.Text(note))
		}),
	)
}

func legacySetRow(label, hint string, control ui.Node) ui.Node {
	return legacyFsRow(label, hint, control)
}

// legacyFsRow is one setting: its name and the sentence under it on the left, the
// control on the right — and, for anyone not reading the layout, a GROUP named by
// that label and described by that hint.
//
// The group is what joins the two halves. Drawn side by side they were only
// neighbours: tabbing onto a row's chip announced "Unread only, toggle button,
// pressed" with nothing to say it answered "Articles", and the hint — usually
// the one sentence explaining what the switch costs — was never read at all
// unless somebody went looking for it. A group is the one association that fits
// every control a row holds: a single chip, a segmented row of them, a select, or
// a value that is only text. Which is also why a picker inside a row does not
// name itself any more — two nested groups with one name is the same sentence
// read twice.
//
// A text field uses fsField instead, because a field has a better association
// available than a group around it: a real <label>.
func legacyFsRow(label, hint string, control ui.Node) ui.Node {
	id := rowID(label, hint)
	return html.Div(html.Props{Class: "fs-row", Role: "group", Aria: labelledAria(id, hint)},
		legacyFsLabel(html.Span(html.Props{Class: "fs-label-name", ID: id + "-label"}, html.Text(label)),
			id, hint),
		html.Div(html.Props{Class: "fs-control"}, control),
	)
}

// legacyFsField is fsRow for a text field or a select: the same two columns, with the
// visible label a <label for> the control.
//
// The label then IS the field's accessible name, which retires the separate
// aria-label most of these used to carry — and those were worded differently
// from what was on screen ("Your name for this feed" beside a label reading
// "Name"), which fails the one person the visible word is for: somebody driving
// the page by voice says what they can see, and "click Name" matched nothing.
// Clicking the label now focuses the field too, which is the old browser
// convenience a span beside an input never had.
//
// The caller gives the control `ID: id` and `Aria: fieldAria(id, hint)`. The id
// is a constant per field rather than derived like fsRow's, because a label has
// to point at exactly one element and these are the elements e2e and the
// keyboard handler already know by name.
func legacyFsField(label, hint, id string, control ui.Node) ui.Node {
	return html.Div(html.Props{Class: "fs-row"},
		legacyFsLabel(html.Label(html.Props{Class: "fs-label-name", For: id}, html.Text(label)), id, hint),
		html.Div(html.Props{Class: "fs-control"}, control),
	)
}

func legacyFsLabel(name ui.Node, id, hint string) ui.Node {
	return html.Div(html.Props{Class: "fs-label"},
		name,
		ui.If(hint != "", func() ui.Node {
			return html.Span(html.Props{Class: "fs-hint", ID: id + "-hint"}, html.Text(hint))
		}),
	)
}

// legacyAfField is one labelled control: an eyebrow, the control, and the sentence
// that says what it is for.
//
// The hint sits UNDER the control rather than under the label, so the eye goes
// label → control → explanation, and a reader who already knows what to type
// never has to read past the field to get to it.
//
// fieldID says which kind of control this is, for fsRow's and fsField's reason.
// A field's id makes the eyebrow its <label> and the hint its description (the
// field itself carries `ID: fieldID` and `Aria: fieldAria(fieldID, hint)`); an
// empty one is a choice — the folder chips — and the whole block becomes a group
// named by the eyebrow and described by the hint.
func legacyAfField(label, hint, fieldID string, control ui.Node) ui.Node {
	return legacyAfFieldWith(label, hint, fieldID, nil, control)
}

// legacyAfFieldWith is afField with something on the far end of the label row.
//
// One caller: the address field, which carries the Smart+ lamp. It belongs on
// THAT row and not in the dialog's header, because the capability is about the
// address — what happens when the thing you typed turns out not to be a feed —
// and a control in the header would be making a claim about the whole dialog.
func legacyAfFieldWith(label, hint, fieldID string, aside, control ui.Node) ui.Node {
	if fieldID != "" {
		return html.Div(html.Props{Class: "af-field"},
			html.Div(html.Props{Class: "af-eyebrow-row"},
				html.Label(html.Props{Class: "af-eyebrow", For: fieldID}, html.Text(label)),
				aside,
			),
			control,
			html.Span(html.Props{Class: "af-hint", ID: fieldID + "-hint"}, html.Text(hint)),
		)
	}
	id := rowID(label, hint)
	return html.Div(html.Props{Class: "af-field", Role: "group", Aria: labelledAria(id, hint)},
		html.Div(html.Props{Class: "af-eyebrow-row"},
			html.Span(html.Props{Class: "af-eyebrow", ID: id + "-label"}, html.Text(label)),
			aside,
		),
		control,
		html.Span(html.Props{Class: "af-hint", ID: id + "-hint"}, html.Text(hint)),
	)
}

// legacyFsFacts and fsFact are the panel's name/value lines, as the description list
// they are.
//
// Read as loose spans, "Last fetched 3 minutes ago Next fetch in 12 minutes" ran
// together into one sentence with no way to tell where a name stopped and its
// value began. A <dl> says which is which, and a screen reader can announce how
// many there are. Each pair keeps its own div — the HTML spec allows a div
// around a dt and its dd inside a dl — so the flex row that lays a pair out is
// the same box it always was.
func legacyFsFacts(facts ...ui.Node) ui.Node {
	return html.Tag("dl", html.Props{Class: "fs-facts"}, facts...)
}

func legacyFsFact(label, value string) ui.Node {
	return html.Div(html.Props{Class: "fs-fact"},
		html.Tag("dt", html.Props{Class: "fs-fact-name"}, html.Text(label)),
		html.Tag("dd", html.Props{Class: "fs-fact-value"}, html.Text(value)),
	)
}

func legacySettingsSkeleton() []ui.Node {
	out := make([]ui.Node, 0, 6)
	for i := 0; i < 6; i++ {
		out = append(out, html.Div(html.Props{
			Class: "sk sk-line sk-w-90", Key: "set-sk-" + strconv.Itoa(i),
		}))
	}
	return out
}

func legacySkeletonRow(i int) ui.Node {
	titles := []string{"sk-w-90", "sk-w-70", "sk-w-90", "sk-w-45"}
	metas := []string{"sk-w-45", "sk-w-30", "sk-w-30", "sk-w-45"}
	if i < 0 {
		i = 0
	}
	return html.Div(html.Props{
		Class: "sk-row",
		Key:   "sk-" + strconv.Itoa(i),
		Aria:  map[string]string{"hidden": "true"},
	},
		html.Div(html.Props{Class: "sk sk-title " + titles[i%len(titles)]}),
		html.Div(html.Props{Class: "sk sk-line " + metas[i%len(metas)]}),
	)
}

// legacySourceMark is the small identity badge: the source's hue with its favicon on
// top, sized by the class the caller passes.
//
// The hue is underneath rather than replaced, everywhere it appears. A site with
// no icon is still identifiable at a glance by colour, and a reader recognises
// the icon of a site they already know faster than they read its name — so both
// carry, and neither is load-bearing alone.
func legacySourceMark(sourceID string, hosts map[string]string, class string) ui.Node {
	src := faviconSrc(hosts[sourceID])
	if src == "" {
		return html.I(html.Props{Class: class,
			Raw: map[string]any{"style": "background:" + design.HueFor(sourceID)}})
	}
	return html.Span(html.Props{Class: class + "-wrap"},
		html.I(html.Props{Class: class,
			Raw: map[string]any{"style": "background:" + design.HueFor(sourceID)}}),
		html.Img(html.Props{
			Class: class + "-img",
			Src:   src,
			// Through Raw, because `Alt: ""` emits NOTHING: an empty string is
			// the zero value, and GWC omits a zero-valued field. So the favicon
			// shipped with no alt attribute at all, and an <img> without one is
			// announced by its URL. Decorative means alt="" — present and empty —
			// and Raw is how an empty attribute is said on purpose.
			Raw: map[string]any{"alt": ""},
			// Lazy, because a virtualised list scrolling fast would otherwise
			// fire a request for every row it passes through.
			Loading: "lazy",
			Aria:    map[string]string{"hidden": "true"},
		}),
	)
}

func legacyFeedIcon(f *pb.Feed) ui.Node {
	src := faviconSrc(iconHost(f))
	if src == "" {
		return html.I(html.Props{Class: "feed-dot"})
	}
	return html.Span(html.Props{Class: "feed-icon-wrap"},
		html.I(html.Props{Class: "feed-dot"}),
		html.Img(html.Props{
			Class: "feed-icon",
			Src:   src,
			// Raw, not `Alt: ""` — see sourceMark: an empty Alt is omitted, and
			// decorative needs the attribute present and empty.
			Raw: map[string]any{"alt": ""},
			// Lazy, because a 150-feed sidebar would otherwise fire 150 image
			// requests before the first row is readable.
			Loading: "lazy",
			Aria:    map[string]string{"hidden": "true"},
		}),
	)
}

// legacyRailControls is the aria-controls a fold's toggle carries: the fold's id
// while it is open, and nothing while it is closed.
//
// Nothing, rather than an id that resolves to nothing. A closed fold is not in
// the document at all — that is the point of folding 151 rows away — and an
// aria-controls naming an absent element is a dangling reference. aria-expanded="false" already says everything there is
// to say about a section that is not there.
func legacyRailControls(aria map[string]string, key string, open bool) map[string]string {
	if open {
		aria["controls"] = legacyRailFoldID(key)
	}
	return aria
}

func legacyNoteControls(aria map[string]string, itemID string, open bool) map[string]string {
	if open {
		aria["controls"] = noteBodyID(itemID)
	}
	return aria
}

// legacyRailFoldID is the DOM id of a section's fold, derived from the same key
// railFold is keyed by. The category keys carry a colon (railCatKey), which is
// legal in an id but has to be escaped in every selector that names one, so it
// is swapped for a hyphen here rather than left as a trap.
func legacyRailFoldID(key string) string {
	return "rail-fold-" + strings.ReplaceAll(key, ":", "-")
}

// legacyActionButtonRole is actionButton with a data-role as well, which is what
// platform.FocusField looks for.
//
// It exists for the dialogs' close buttons. A panel that opens without moving
// focus into itself leaves a keyboard reader tabbing through the rail BEHIND
// the scrim — present in the tab order, covered on screen — and the focus trap
// inside the panel never engages, because focus never gets in. The palette
// solved this by focusing its search field on open (FocusField's doc comment
// tells that story); a settings panel has no field it would be safe to put a
// caret in, so the close button is the target: always present, unambiguous, and
// harmless to press by accident.
func legacyActionButtonRole(action, role, class, label string) ui.Node {
	return html.Button(html.Props{
		Class:    class,
		DataAttr: html.DataAttribute{Name: "action", Value: action},
		Data:     map[string]string{"role": role},
	}, html.Text(label))
}

// legacyActionButton renders a button whose click is handled by the shell's delegated
// listener, identified by data-action.
//
// The pane helpers below are PURE — no hooks at all. They are plain functions
// called from Reader, so any ui.UseEvent inside them would run in Reader's hook
// sequence; and because several of them return early or branch, those hooks
// would be CONDITIONAL. GWC matches hooks positionally, so a conditional hook
// silently binds to the wrong slot. The visible symptoms were a list that
// flickered on every feed change and a "Load more" button that did nothing.
func legacyActionButton(action, class, label string) ui.Node {
	return html.Button(html.Props{
		Class:    class,
		DataAttr: html.DataAttribute{Name: "action", Value: action},
	}, html.Text(label))
}

// legacyFeedRow is feedRow as it was before it was drawn with kitRow.
func legacyFeedRow(tr i18n.Runtime, f *pb.Feed, active, nested bool) ui.Node {
	// A feed that has failed repeatedly is dormant, and the reader has to be
	// able to say so — a dead feed and a quiet feed look identical otherwise.
	dormant := f.GetConsecutiveFailures() >= 3
	title := f.GetTitle()
	if dormant {
		title = tr.T("rail", "dormantSuffix", i18n.Args{"title": title})
	}

	children := []ui.Node{
		kitMark(kitMarkProps{Class: "feed-dot", WrapClass: "feed-icon-wrap", ImgClass: "feed-icon", Src: faviconSrc(iconHost(f))}),
		html.Span(html.Props{Class: "feed-name", Title: titleFor(tr, f, dormant)},
			html.Text(f.GetTitle())),
	}
	if n := f.GetUnreadCount(); n > 0 {
		children = append(children,
			html.Span(html.Props{Class: "feed-count"}, html.Text(thousands(tr, int(n)))))
		// And in the row's NAME, which is an aria-label and therefore replaces
		// everything the row contains. Without this the count above was on
		// screen and nowhere else: a feed with 128 unread was announced as its
		// title alone, while the topic rows two bands down said "Software, 48
		// unread". Same sentence, same key — it is one shape of fact, and the
		// reader hears it the same way wherever a row carries a number.
		title = tr.T("rail", "categoryUnreadAria", i18n.CountWith(int(n), i18n.Args{"name": title}))
	}

	// The gear.
	//
	// A sibling of the row rather than a child of it: nesting a button inside a
	// button is invalid HTML, and browsers resolve it by hoisting one out — after
	// which the click target is whichever one survived, not the one that was
	// drawn. The row and the gear sit side by side inside a wrapper instead.
	//
	// Hidden until hover or keyboard focus. 151 gears down a sidebar is a column
	// of hardware, and the one thing the rail is for — reading feed names — is
	// what it would be competing with.
	children = append(children, html.Span(html.Props{Class: "feed-gap"}))

	props := hueVarFor(f.GetSourceId())
	if props == nil {
		props = map[string]any{}
	}
	props["data-source-id"] = f.GetSourceId()

	slot, key := "feed-slot", feedRowKey(f.GetSourceId(), nested)
	if nested {
		slot = "feed-slot feed-nested"
	}

	// A listitem, because every caller puts it in a railList.
	return html.Div(html.Props{Class: slot, Role: "listitem", Key: key},
		html.Button(html.Props{
			Class:    "feed-row",
			TabIndex: -1,
			Raw:      props,
			Data:     map[string]string{"dormant": strconv.FormatBool(dormant)},
			Aria:     map[string]string{"current": strconv.FormatBool(active), "label": title},
		}, children...),
		html.Button(html.Props{
			Class:     "feed-gear",
			TabIndex:  -1,
			DataAttrs: []html.DataAttribute{{Name: "action", Value: "feed-settings"}, {Name: "for-item", Value: f.GetSourceId()}},
			Title:     tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()}),
			Aria:      map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": f.GetTitle()})},
		}, html.Text("\u2699")),
	)
}

// legacyTopicRow is topicRow as it was before it was drawn with kitRow.
func legacyTopicRow(tr i18n.Runtime, slug string, active bool, unread int) ui.Node {
	name := categoryName(tr, slug)
	mark := html.Props{Class: "feed-dot topic-dot"}
	if style := catStyleFor(slug); style != nil {
		mark.Raw = style
	}
	// The accessible name carries the count, because the number beside it is
	// rendered as decoration a screen reader would otherwise read as a bare
	// integer after a word — the same shape railCategories' own aria uses.
	label := name
	if unread > 0 {
		label = tr.T("rail", "categoryUnreadAria", i18n.CountWith(unread, i18n.Args{"name": name}))
	}
	return html.Div(html.Props{Class: "feed-slot", Role: "listitem", Key: "topic-" + slug},
		html.Button(html.Props{
			Class:    "feed-row topic-row",
			TabIndex: -1,
			DataAttr: html.DataAttribute{Name: "category-slug", Value: slug},
			Aria:     map[string]string{"current": strconv.FormatBool(active), "label": label},
		},
			html.I(mark),
			html.Span(html.Props{Class: "feed-name"}, html.Text(name)),
			// Zero renders nothing rather than "0". A label with nothing in it
			// is still worth showing — it is where an article will appear —
			// but a column of noughts is twenty-six pieces of no information.
			ui.If(unread > 0, func() ui.Node {
				return html.Span(html.Props{Class: "feed-count"},
					html.Text(thousands(tr, unread)))
			}),
		),
	)
}

// legacyTagRow is tagRow as it was before it was drawn with kitRow.
func legacyTagRow(tr i18n.Runtime, t *pb.Tag, active bool) ui.Node {
	name := tagDisplay(t)
	// The tooltip says both names when they differ. It is the cheapest possible
	// answer to "what is this row actually tagged as", and it costs nothing on
	// the majority of rows that were never renamed.
	hint := tr.T("rail", "tagged", i18n.Args{"name": name})
	if l := t.GetLabel(); l != "" && l != t.GetName() {
		hint = tr.T("rail", "taggedRenamed", i18n.Args{"label": name, "name": t.GetName()})
	}

	return html.Div(html.Props{Class: "feed-slot", Role: "listitem", Key: "tag-" + t.GetId()},
		html.Button(html.Props{
			Class:    "feed-row",
			TabIndex: -1,
			DataAttr: html.DataAttribute{Name: "tag-id", Value: t.GetId()},
			Title:    hint,
			Aria:     map[string]string{"current": strconv.FormatBool(active), "label": hint},
		},
			tagMark(t),
			html.Span(html.Props{Class: "feed-name"}, html.Text(name)),
			html.Span(html.Props{Class: "feed-count"},
				html.Text(thousands(tr, int(t.GetFeedCount())))),
			html.Span(html.Props{Class: "feed-gap"}),
		),
		html.Button(html.Props{
			Class:     "feed-gear",
			TabIndex:  -1,
			Data:      map[string]string{"role": tagGearRole(t.GetId())},
			DataAttrs: []html.DataAttribute{{Name: "action", Value: actTagSettings}, {Name: "for-item", Value: t.GetId()}},
			Title:     tr.T("rail", "settingsFor", i18n.Args{"name": name}),
			Aria:      map[string]string{"label": tr.T("rail", "settingsFor", i18n.Args{"name": name})},
		}, html.Text(glyphSettings)),
	)
}
