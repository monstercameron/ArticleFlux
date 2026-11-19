//go:build js && wasm

package view

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The chosen design has NO top bar. Search, refresh and the connection state
// live in the list pane's header; adding a feed lives at the foot of the rail.
// The bar that used to be here was a utilitarian strip bolted across a design
// that had deliberately done without one.

// connLabel names the connection state in words, because a coloured dot alone
// does not survive being colour-blind or being glanced at.
//
// Five words rather than three (§20.19.2). One of them previously covered a
// dropped Wi-Fi, an unplugged server and an expired session at once — three
// states with three different things for the reader to go and do. The word names
// the state; the chip beside it offers the remedy.
func connLabel(tr i18n.Runtime, s data.ConnState) string {
	switch s {
	case data.Live:
		return tr.T("list", "connLive")
	case data.Connecting:
		return tr.T("list", "connConnecting")
	case data.Offline:
		return tr.T("list", "connOffline")
	case data.Blocked:
		return tr.T("list", "connBlocked")
	default:
		return tr.T("list", "connDown")
	}
}

// readingTime turns a word count into what a reader actually wants to know
// before committing: how long this will take.
//
// 230 wpm is the usual figure for adult silent reading of ordinary prose. It is
// approximate by nature, so it rounds to a minute and never shows "0".
func readingTime(tr i18n.Runtime, words int32) string {
	mins := int(words) / 230
	if mins < 1 {
		mins = 1
	}
	// Memoised: one of the two argument-taking lookups on every list row. See
	// phrases.
	k := phraseKey{locale: tr.Locale(), kind: phraseReadingTime, a: mins}
	if s, ok := cachedPhrase(k); ok {
		return s
	}
	return storePhrase(k, tr.T("list", "readingTime", i18n.Count(mins)))
}

// Age thresholds for a list row.
//
// Three states rather than two, because "unread" and "worth reading" stop being
// the same thing in a firehose. Something published in the last day is news;
// something a month old that is still unread is not going to become news, and a
// reader scanning 3,600 items needs to see that without doing the arithmetic.
const (
	freshWindow = 24 * time.Hour
	staleWindow = 30 * 24 * time.Hour
)

// ageOf classifies an item by when it was published and whether it has been
// read. Read wins over both: once you have read it, how old it is stops being
// the useful fact about it.
func ageOf(publishedAt string, read bool) string {
	if read {
		return "read"
	}
	t, err := time.Parse(time.RFC3339Nano, publishedAt)
	if err != nil {
		if t, err = time.Parse(time.RFC3339, publishedAt); err != nil {
			return "unread"
		}
	}
	switch d := time.Since(t); {
	case d < freshWindow:
		return "new"
	case d > staleWindow:
		return "stale"
	default:
		return "unread"
	}
}

// firstWords is the opening of a note, for the list row.
//
// Cut on a word boundary rather than mid-word: "the argument about compil…" is
// readable and "the argument about compi…" is a typo with an ellipsis. Newlines
// collapse to spaces because the row is one line high and a note is prose that
// may well start with a line break.
//
// max counts RUNES, not bytes — it is a display budget ("how many characters to
// show"), and a budget in characters is what that phrase actually means for a
// line of CJK text as much as a line of English. This used to slice s[:max] by
// raw byte offset, which for a multi-byte rune straddling that offset produced
// a cut mid-rune and an invalid UTF-8 result — corrupted text on every Notes-row
// preview written in a non-Latin script. Every existing call site (90, 12, 220)
// was tuned against effectively-ASCII copy, where rune count and byte count are
// the same number, so this is not a behaviour change for them.
func firstWords(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := string([]rune(s)[:max])
	if i := strings.LastIndexByte(cut, ' '); i > max/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// thousands formats a count with separators, because "1,420 words" reads and
// "1420 words" is parsed.
//
// The grouping itself moved to client/i18n, because the separator is a comma in
// English, a period in German and a narrow space in French — and this is called
// from every count in the app, so getting it from one place is what stops the
// server screen and the list header disagreeing.
func thousands(tr i18n.Runtime, n int) string { return i18n.Number(tr.Locale(), n) }

// Glyphs.
//
// Two positions, two meanings, and the split is the rule:
//
//	LEFT   — what a thing IS. A leading glyph on a heading or a control names its
//	         kind, and it is the same glyph everywhere that kind appears, so the
//	         palette, the sidebar and the tab bar teach one vocabulary instead of
//	         three.
//	RIGHT  — what will HAPPEN. A trailing mark is reserved for consequences the
//	         reader should know about before clicking: \u2197 leaves the app,
//	         \u2192 goes somewhere else in it. Nothing decorative goes on the right,
//	         because that is what makes \u2197 mean something.
//
// Text glyphs rather than icons: they inherit colour and weight, they scale with
// the type, they need no sprite sheet, and A26 has no room for an icon build step.
const (
	// My Feed — a four-pointed sparkle, and the one mark in this set that says
	// "chosen" rather than "contains".
	//
	// It has to read as different in KIND from the rows beneath it: those are places
	// items ARE — everything, unread, saved — and this is a judgement ABOUT them. A
	// third circle or another list glyph would file the ranked stream alongside the
	// containers and lose exactly the distinction that makes it worth a row.
	glyphMyFeed   = "✦"
	glyphAll      = "\u25c8" // all feeds — everything, one object
	glyphUnread   = "\u25cf" // unread — a thing waiting
	glyphLater    = "\u23f1" // read later — time set aside
	glyphLiked    = "\u25b2" // liked — the verdict's own mark
	glyphDisliked = "\u25bc"
	glyphNotes    = "\u270e" // notes — a pencil, your own writing
	glyphFeeds    = "\u2263" // a list of sources
	glyphTags     = "\u2317" // a label
	// A folder is a container, where a tag is a label stuck on one \u2014 so it is
	// a box shape rather than the tag's ticket, and the two sections stay
	// distinguishable at 12px in a column of 151 rows.
	//
	// The tab in the corner is doing the work, and it is why this is not the
	// plainer box it used to be. \u2337 (APL SQUISH QUAD) is a narrow empty
	// rectangle in every font that has it: at rail size it is the shape a browser
	// draws for a character it CANNOT draw, so the one row in the rail that named
	// a container looked instead like a rendering fault \u2014 and it measured ~26%
	// narrower than its neighbours, so it sat outside their optical column as
	// well. Reported on sight, 2026-08-08. The replacement is a container that
	// could not be mistaken for a missing glyph, at the same width as \u25c8 and
	// \u25a6 either side of it.
	glyphCats = "\u25f0"
	// The Categories settings tab (client/view/classifysettings.go) gets its own
	// mark rather than reusing glyphCats \u2014 D23 (plan.md \u00a727.0a) gave "category" to
	// the classifier's 26 article sections and the rail's containers became
	// Folders, but the two glyphs still sit side by side in the rail and sharing
	// one would undo the distinction the rename just drew. A crosshatched grid
	// rather than glyphCats' solid box: many small cells, for a taxonomy with 26
	// members, instead of one container.
	glyphClassify = "\u25a6"
	glyphAdd      = "\uff0b"
	glyphSearch   = "\u2315"
	glyphRefresh  = "\u21bb"
	glyphMarkRead = "\u2713"
	glyphSettings = "\u2699"
	glyphHelp     = "?"
	glyphDiscover = "\u2733" // section 18.7, M16: sites you don't follow yet
	glyphListen   = "\u25b6"
	glyphPause    = "\u23f8"
	glyphStop     = "\u25a0"
	glyphHealth   = "\u2665"
	glyphAction   = "\u26a1"
	glyphYours    = "\u25cd"
	glyphShared   = "\u25cc"
	// Signing out, on the Account tab. The open counterpart of that tab's own
	// \u25d1: half a disc is somebody signed in, an empty ring is the same seat with
	// nobody in it. Deliberately NOT an arrow \u2014 the document's direction flips with
	// the locale (Root sets `dir`), so a glyph that means "out" by pointing right
	// means "in" in Arabic. And deliberately not \u23fb, which on the settings screen
	// of a self-hosted server reads as "shut the server down".
	glyphSignOut = "\u25cb"
	// The note autosave's marks. \u21bb is the sync glyph in every application
	// that has one, which is exactly why it is here: a field that saves itself
	// has to report that in a form nobody needs a legend for.
	glyphSyncing    = "\u21bb"
	glyphSynced     = "\u2713"
	glyphSyncFailed = "\u26a0"
	// Removal, on a chip that carries something you can take off.
	glyphRemove = "\u00d7"
	// The note panel's disclosure caret. A chevron rather than +/\u2212: a plus
	// promises a new thing, and this reveals one that is already there. One
	// glyph, not two \u2014 the sheet rotates it when the panel opens, so the control
	// reads as one thing changing state rather than being swapped for another.
	glyphClosed = "\u203a"
	// Right-hand side only.
	glyphExternal = "\u2197"
	// Translate: there and back, which is what the control does — it toggles
	// between the two versions. Not a flag (a flag names a country, not a
	// language) and not a globe (that already means "the web"). Symmetric, so
	// it needs no mirroring under dir="rtl".
	glyphTranslate = "\u2194"
	// The proxied page. A framed window rather than a globe: a globe says "the
	// internet", and the thing this control actually offers is *the page in a
	// frame here*, which is the distinction the reader is choosing between.
	glyphPage = "\u25a3"
	// Widen. Arrows pointing apart, which is what the control does to the
	// frame \u2014 not a maximise square, because nothing here goes fullscreen and
	// promising that would be a lie about where the view ends up.
	glyphWide = "\u21f9"
	// The slideshow (\u00a719). A film frame: a rectangle with the sprocket bar down
	// its side, which is the one mark in this set that says "this plays by
	// itself". Deliberately NOT \u25b6, which is Listen here \u2014 pressing play on a
	// feed and pressing play on an article have to look like different acts,
	// because in this application they genuinely are.
	glyphSlideshow = "\u25a4"
	// The podcast (§19). A speaker cone, because the difference between the two
	// entry points is sound: one shows the feed and one reads it. Deliberately
	// not a microphone, which is recording rather than playback, and not the
	// listen glyph, for the reason the slideshow's own comment gives — pressing
	// play on a feed and pressing play on an article are different acts.
	glyphPodcast = "🔊"
)

// lead renders a leading glyph. Hidden from assistive tech: the label beside it
// already says what it is, and a screen reader announcing "black up-pointing
// triangle Like" is worse than "Like".
func lead(g string) ui.Node {
	return html.Span(html.Props{Class: "gl",
		Aria: map[string]string{"hidden": "true"}}, html.Text(g))
}

// podcastChip is the second entry point, and its state is the prerequisites.
//
// The four conditions read-to-me needs used to be discovered AFTER entering the
// mode — which is why there is a button on the slide explaining the silence and
// a settings tab behind it. Said here they are answerable before anything is
// pressed, and the slide's explanation goes back to being what it should be: the
// fallback for a failure that happens mid-programme, which a check at the door
// cannot see.
//
// Blocked is a data attribute rather than `disabled`. A disabled control cannot
// be focused, cannot be read by a screen reader that walks the tab order, and
// cannot tell anybody why — and every condition here except the server's key is
// one the reader can change from the screen this chip opens.
func podcastChip(tr i18n.Runtime, blocked string) ui.Node {
	if blocked != "" {
		return kitToggle(kitChipProps{Action: actSlideNeeds, Glyph: glyphPodcast,
			Data:  []html.DataAttribute{{Name: "blocked", Value: blocked}},
			Label: tr.T("list", "podcastBlocked")}, false)
	}
	return kitToggle(kitChipProps{Action: actPodcastOpen, Glyph: glyphPodcast,
		Label: tr.T("list", "podcast")}, false)
}

// markAllChip is Mark all read: the control that OPENS the confirmation, and
// never the one that performs the mark.
//
// The first version of this confirmation put both states on this one chip —
// press once to arm, press again to mark — and that is the version this
// replaces, because it put the confirming press exactly where the arming
// press had just been. A double-click, which is a thing people do to buttons,
// went straight through it: two clicks in the same spot destroyed a backlog
// with a confirmation step that never had a frame in which it could be read.
// It also changed the chip's own width and colour, so the toolbar jumped
// under the pointer mid-press.
//
// So the chip's label never changes and it never turns red. It stays a
// question being asked; markAllConfirm (below) is where it is answered, in a
// different place, which is what makes the second press deliberate.
func markAllChip(tr i18n.Runtime, armed bool) ui.Node {
	return kitDisclosure(kitChipProps{Action: "mark-all-arm", Glyph: glyphMarkRead,
		Label: tr.T("list", "markAllRead")},
		// The chip reports that it OPENED something, which is what
		// aria-expanded means and what a reader with the banner on screen
		// needs to know about the control that put it there.
		armed)
}

// markAllConfirm is the confirmation, in the banner row under the toolbar.
//
// It names the SCOPE rather than counting the articles, and that is the whole
// point of it: the question a reader needs answered before this press is
// "which list is this about" — the bug it guards is a mark that reaches feeds
// they were not looking at — and a count answers a different, smaller
// question. The number they marked is reported afterwards, by the banner that
// carries the undo, where it is a fact rather than a promise.
//
// Same shape as the category-suggestion banner above it: a line of text and
// two chips, no icon and no colour of its own. Only the confirming chip is
// destructive-styled, because it is the only thing here that does anything.
//
// # Why focus moves into it
//
// It is an alertdialog, and an alertdialog is a promise that focus is inside
// it: that is how a screen reader comes to read the question at all, and how a
// keyboard reader reaches the answer without tabbing past the rest of the
// toolbar to find it. It used to be an alertdialog that nothing ever focused,
// with aria-live standing in for the focus — which announced a question whose
// buttons were somewhere else in the tab order, and then, when either button
// was pressed, unmounted the focused element and dropped the caret on <body>.
//
// So arming focuses Cancel, and closing hands focus back to the chip
// (reader.go's armMarkAll and disarmMarkAll). Cancel rather than Mark read:
// the press that opened this is still under the finger, and a second Enter or
// a key held a beat too long must land on the answer that does nothing — the
// same reason the confirmation is in a different place from the chip.
func markAllConfirm(tr i18n.Runtime, title string) ui.Node {
	return kitBanner(kitBannerProps{Confirm: true,
		Text:      tr.T("list", "markAllConfirm", i18n.Args{"scope": title}),
		Title:     tr.T("list", "markAllConfirmAria"),
		TextID:    markAllConfirmTextID,
		FocusRole: markAllConfirmRole},
		kitBannerChip(kitBannerChipProps{Action: "mark-all", Label: tr.T("list", "markAllConfirmGo"), Danger: true}),
		kitBannerChip(kitBannerChipProps{Action: "mark-all-cancel", Label: tr.T("list", "markAllConfirmCancel"), FocusRole: markAllCancelRole}),
	)
}

// The handles reader.go moves focus by. data-role, like every other element
// the platform layer is asked to find, so the selectors are not built from
// class names a restyle could change.
const (
	markAllConfirmRole   = "mark-all-confirm"
	markAllCancelRole    = "mark-all-cancel"
	markAllConfirmTextID = "mark-all-confirm-text"
)

// itemAction is a chip that acts on a named item, without a pressed state for
// kitChipProps' reason: Read the rest and Add are things you do, not switches.
//
// It writes data-for-item even when the id is empty, which kitAction does not.
// The delegated click handler finds the id with closest("[data-for-item]"), so
// a button that omitted the attribute would pick up whatever id an ancestor
// carries. The tag panel's rename is one: the panel is rendered while closed,
// with no tag in it.
func itemAction(action, label, itemID string) ui.Node {
	return html.Button(html.Props{
		Class:     "chip",
		DataAttrs: []html.DataAttribute{{Name: "action", Value: action}, {Name: "for-item", Value: itemID}},
	}, html.Text(label))
}
