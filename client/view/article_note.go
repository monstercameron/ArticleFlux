//go:build js && wasm

package view

// The per-article note panel and the tag chips inside it. Split out of
// panes.go unchanged.

import (
	"strconv"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// articleNote is the quick-note field, behind a disclosure.
//
// At the end of the article rather than the top: a note is what you write after
// reading, and putting an empty textarea above the text you came to read is
// asking a question before there is anything to answer.
//
// **Closed by default**, which matters more here than it would anywhere else.
// The reading pane is a continuous stream (A28), so this is not one panel at the
// bottom of one page — it is a bordered card with a three-row textarea BETWEEN
// every article and the next one, and scrolling through ten pieces means
// scrolling past ten empty forms nobody asked for. Writing a note is a
// deliberate act, so it can afford a deliberate click; reading is not, and it
// should not have to step over the furniture.
//
// What survives the collapse is the part that is INFORMATION rather than input:
// the first words of an existing note, the tags already filed, and the autosave
// mark. Hiding those would make the panel dishonest — a reader would have to
// open every article's note to find out whether they had written one.
func articleNote(tr i18n.Runtime, it *pb.Item, p articleProps) ui.Node {
	id := it.GetId()
	draft := p.notes[id]
	open := p.noteOpen[id]

	kids := []ui.Node{noteSummary(tr, it, p, open)}
	if open {
		kids = append(kids, noteEditor(tr, it, p, draft))
	}
	return html.Div(html.Props{
		Class: "article-note",
		Key:   "note-" + id,
		Data:  map[string]string{"open": strconv.FormatBool(open)},
	}, kids...)
}

// noteSummary is the always-visible row: the toggle, what is already there, and
// the autosave mark.
func noteSummary(tr i18n.Runtime, it *pb.Item, p articleProps, open bool) ui.Node {
	id, src := it.GetId(), it.GetSourceId()
	draft, tags := p.notes[id], p.feedTags[src]

	// One caret in both states; the sheet rotates it. The label carries the rest,
	// and says something different when there is nothing on the row to explain
	// what the control is for.
	label := tr.T("note", "label")
	if !open && draft == "" && len(tags) == 0 {
		label = tr.T("note", "labelEmpty")
	}

	row := []ui.Node{
		html.Button(html.Props{
			Class:     "note-toggle",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: "toggle-note"}, {Name: "for-item", Value: id}},
			// aria-controls only while the editor exists, for the reason
			// kitDisclosureAria gives: closed, there is nothing to point at.
			Aria: kitDisclosureAria(label, open, noteBodyID(id)),
		},
			html.Span(html.Props{Class: "note-caret",
				Aria: map[string]string{"hidden": "true"}}, html.Text(glyphClosed)),
			html.Span(html.Props{}, html.Text(label)),
		),
	}

	// The note itself, in miniature, so a closed panel still answers "did I write
	// something about this one". Only when closed — open, the textarea below is
	// already showing it in full and a preview would be the same words twice.
	if !open && draft != "" {
		// The cap is a ceiling for a very long note, not the width of the preview: it
		// was twelve characters, which cut "Before outage…" short on a 820px tablet
		// with 400px of the row empty beside the chips. The row's own width decides
		// where the line ends (.note-peek's ellipsis), and a wider cap is what lets
		// it.
		row = append(row, html.Span(html.Props{Class: "note-peek"},
			html.Text(firstWords(draft, 160))))
	}

	// Tags stay on the row in BOTH states. They are one compact line, they are
	// the answer to "is this already filed", and they carry their own way off —
	// putting them behind the disclosure would mean opening a form to discover a
	// fact. Each chip's title says "from this feed", which is the scope, and the
	// heading in the editor below says it again in full.
	if len(tags) > 0 {
		chips := make([]ui.Node, 0, len(tags))
		for _, t := range tags {
			chips = append(chips, tagChip(tr, t, id))
		}
		row = append(row, html.Div(html.Props{Class: "note-tagged"}, chips...))
	}

	row = append(row, noteSyncMark(tr, p.sync[id]))
	return html.Div(html.Props{Class: "note-summary"}, row...)
}

// noteBodyID is the id of an article's open note editor, which its toggle's
// aria-controls names. Per article, because the stream holds many.
func noteBodyID(itemID string) string { return "note-body-" + itemID }

// noteEditor is everything you only need once you have decided to write.
func noteEditor(tr i18n.Runtime, it *pb.Item, p articleProps, draft string) ui.Node {
	id, src := it.GetId(), it.GetSourceId()

	// The fields carry their own identity rather than relying on being the only
	// one on the page. Input is delegated through a single listener that reads
	// data-note-id / data-tag-source off the element, because GWC's typed
	// handlers only ever deliver event.target.value — with one note field that
	// was enough, and with one per article it is not.
	return html.Div(html.Props{Class: "note-body", ID: noteBodyID(id)},
		html.Textarea(html.Props{
			Class:       "note-field",
			Placeholder: tr.T("note", "placeholder"),
			Value:       draft,
			Rows:        3,
			DataAttr:    html.DataAttribute{Name: "note-id", Value: id},
			Data:        map[string]string{"role": "note"},
			Aria:        map[string]string{"label": tr.T("note", "aria")},
			// The server's own cap, in characters (store.MaxNoteChars): a note past
			// it was refused with "Couldn't save that note." after the writing was
			// done (G19). The field stops where the server would.
			Raw: map[string]any{"maxlength": "8000"},
		}),
		// They are the FEED's tags, said plainly here, because a reader removing
		// one from an article should not be surprised to find it gone from every
		// article in the feed.
		html.Div(html.Props{Class: "note-head"},
			html.Span(html.Props{},
				html.Text(tr.T("note", "tagHeading", i18n.Args{"source": it.GetSourceTitle()}))),
		),
		html.Div(html.Props{Class: "note-tags"},
			html.Input(html.Props{
				Class: "field", Type: "text", Placeholder: tr.T("note", "tagPlaceholder"),
				Value:    p.tags[src],
				DataAttr: html.DataAttribute{Name: "tag-source", Value: src},
				Data:     map[string]string{"role": "tag"},
				Aria:     map[string]string{"label": tr.T("note", "tagAria")},
				// Stops at the store's cap instead of letting the reader type 105
				// characters into a field that then says only "Couldn't add that
				// tag." (G19).
				Raw: map[string]any{"maxlength": strconv.Itoa(maxNameChars)},
			}),
			kitAction(kitChipProps{Action: "add-tag", Label: tr.T("note", "addTag"), ItemID: id}),
		),
	)
}

// noteSyncMark is the autosave indicator: one glyph, no instructions.
//
// It replaced "Unsaved — press Ctrl+Enter". That line was a demand disguised as
// a status — it told the reader their writing was at risk and made them do
// something about it. A field that saves itself only has to report, so this says
// what is happening and nothing more, and it says it in the space the old
// sentence occupied so the layout does not move when it appears.
//
// Failure is the one state that speaks: a glyph alone cannot tell a reader their
// note is not on the server, and that is the only case where they might need to
// do something (copy it out, try again) before leaving the page.
func noteSyncMark(tr i18n.Runtime, state string) ui.Node {
	if state == "" {
		return nil
	}
	glyph, label := glyphSyncing, tr.T("note", "saving")
	// The two states that are NOT on the server say so in visible words (G11),
	// where they were a glyph and a screen-reader-only sentence: during an outage
	// the mark was a small spinner reading "Saving…" for as long as it lasted,
	// which is the opposite of the truth. Failed (the server refused) and queued
	// (kept on this device, going out when the connection returns) share the
	// sentence, because what a reader may need to do about either is the same.
	words := false
	switch state {
	case noteSyncSaved:
		glyph, label = glyphSynced, tr.T("note", "saved")
	case noteSyncFailed, noteSyncQueued:
		glyph, label = glyphSyncFailed, tr.T("note", "notSaved")
		words = true
	}
	wordsClass := "sr"
	if words {
		wordsClass = "sync-words"
	}
	return html.Span(html.Props{
		Class: "note-sync",
		Data:  map[string]string{"sync": state},
		Title: label,
		// A live region, because this is the only confirmation there is: a
		// reader who cannot see the glyph has no other way to learn that what
		// they typed was kept. Polite, so it waits for a pause in typing.
		Aria: map[string]string{"live": "polite"},
	},
		html.Span(html.Props{Class: "sync-gl",
			Aria: map[string]string{"hidden": "true"}}, html.Text(glyph)),
		html.Span(html.Props{Class: wordsClass}, html.Text(label)),
	)
}

// tagRef is a tag as a chip needs it: the name it is DRAWN with and the name it
// is ACTED ON with.
//
// Two fields because a tag can be renamed for the rail while keeping its own
// name (see tagsettings.go), and a chip needs both halves — the label, so the
// same tag reads the same everywhere, and the name, because SetFeedTag is
// keyed by it and sending the label would ask the server to remove a tag that
// does not exist. Passing one string and hoping was how that would have gone
// wrong, silently, for exactly the readers who had used the feature.
type tagRef struct {
	Label string
	Name  string
	// ID is the tag's server id — what data-tag-id needs to resolve back to a
	// *pb.Tag in d.tags (reader_clicks.go's pickTag lookup is by id, not name).
	// Empty while Pending, since the server has not minted one yet.
	ID string
	// Pending means the server has not confirmed it yet. The chip is drawn and
	// the × is not, because the removal it offers would be of something that
	// does not exist on the server to remove.
	Pending bool
}

// tagChip is a tag already on a feed, with the way to take it off.
//
// A chip is TWO controls, and this comment used to say the opposite.
//
// It once read "the whole chip is the remove button rather than a label with a
// small × beside it", which stopped being true when the label became a link to
// the tag's own stream — see the arrangement at the bottom of this function,
// which is deliberate and newer. The stale sentence is worth recording rather
// than just deleting: a test clicked the whole chip on its authority and
// asserted the tag was gone, and what it actually did was navigate. A removal
// that silently does nothing is indistinguishable from one that failed.
//
// The × is the smaller target of the two and that is the accepted cost of the
// label doing something useful. It carries its own accessible name.
func tagChip(tr i18n.Runtime, t tagRef, itemID string) ui.Node {
	// Waiting on the server. A SPAN, not a disabled button: a disabled control is
	// still a control, and this is a statement — "this tag is going on" — for the
	// second or two before it becomes one.
	//
	// The spinner is the app's existing sync glyph rather than a new marker. The
	// note field already spends that glyph to mean "your change is on its way",
	// and a second symbol for the same idea would be two vocabularies for one
	// fact. Keyed the same as the settled chip so the swap is a patch rather than
	// a remove-and-insert, which is what stops it flickering as it lands.
	if t.Pending {
		return html.Span(html.Props{
			Class: "chip chip-mini tag-chip tag-chip-pending",
			Key:   "tag-" + t.Name,
			Title: tr.T("note", "tagAdding", i18n.Args{"tag": t.Label}),
			Aria: map[string]string{"busy": "true",
				"label": tr.T("note", "tagAddingAria", i18n.Args{"tag": t.Label})},
		},
			html.Span(html.Props{}, html.Text(t.Label)),
			html.Span(html.Props{Class: "tag-wait",
				Aria: map[string]string{"hidden": "true"}}, html.Text(glyphSyncing)),
		)
	}
	// Keyed by the NAME, which is the stable identity: keying by the label would
	// remount every chip for a tag the moment it was renamed.
	//
	// TWO sibling BUTTONS inside a span, not one button holding a span-×. That
	// first shape had Enter on the chip do nothing (data-tag-id sat on a child
	// span, and a key press only clicks the focused element), left a keyboard
	// reader no way to remove a tag at all (the × was tabindex -1), and gave the
	// chip the accessible name "label×". Now each half is a real control with its
	// own name and its own Tab stop, and the × is a 24px square (G4).
	//
	// data-tag-id sits on the OPEN button, data-action="remove-tag" on the remove
	// button — siblings, never one inside the other's ancestor chain. Both are
	// delegated via closest(), and a data-action on an ANCESTOR of the label
	// would fire the remove alongside the navigate.
	return html.Span(html.Props{
		Class: "chip chip-mini tag-chip",
		Key:   "tag-" + t.Name,
	},
		html.Button(html.Props{
			Class:    "tag-open",
			Type:     "button",
			Title:    tr.T("note", "tagOpen", i18n.Args{"tag": t.Label}),
			DataAttr: html.DataAttribute{Name: "tag-id", Value: t.ID},
		}, html.Text(t.Label)),
		html.Button(html.Props{
			Class:     "tag-x",
			Type:      "button",
			DataAttrs: []html.DataAttribute{{Name: "action", Value: "remove-tag"}, {Name: "for-item", Value: itemID}, {Name: "value", Value: t.Name}},
			Aria:      map[string]string{"label": tr.T("note", "tagRemoveAria", i18n.Args{"tag": t.Label})},
		}, html.Span(html.Props{Aria: map[string]string{"hidden": "true"}}, html.Text(glyphRemove))),
	)
}
