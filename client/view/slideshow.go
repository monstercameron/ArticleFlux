//go:build js && wasm

package view

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The slideshow (§19): the reader with nothing else on the screen.
//
// One story at a time, full width, in type big enough to read from the other
// side of a room — a title card that holds, then the story itself scrolling
// slowly past, then the next one. It is meant to be LEFT RUNNING, which is the
// fact every decision in this file and in design/slideshow.go answers to.
//
// # The two modes, and why one of them is not just "with sound"
//
//	silent   a clock advances the slides. The reader chooses the pace.
//	read     the NARRATOR advances the slides. The voice decides the pace, and
//	         the picture follows it.
//
// The second one is the point of the feature rather than an accessory to it.
// With the Smart+ voice and podcast narration on (see internal/smart/podcast.go)
// the segments are written to hand off to each other, so what comes out is a
// continuous broadcast rather than a queue of articles read in a row — and the
// visual has to be driven by the same clock or the two drift apart within three
// stories. So in that mode nothing here runs on a timer at all: `--fill` comes
// from the <audio> element's own playhead, and a slide ends when its track does.
//
// # Everything on screen is derived, and this file holds no state
//
// Like every other pane here, this is a pure function of props. The state, the
// timers and the wake lock live in Reader, because they have to survive this
// function being called sixty times.

// The shape of one slide, in time.
//
// These are the numbers that make it feel like a broadcast rather than a
// carousel, and each is chosen against a specific failure:
//
//   - slideCardHold — the title card. Under about two seconds the headline is
//     replaced before it has been READ, which makes the whole display feel like
//     it is rushing you; much over three and a reader who is actually watching
//     is left waiting for the story to start.
//   - slideExit — the cross-fade. Long, because a cut every twenty seconds in
//     the corner of your eye is the thing that makes an ambient display
//     unbearable to sit next to.
//   - slideSettle — the pause at the END of the scroll, so the last paragraph is
//     still on screen when the slide begins to leave rather than sliding off the
//     top as it goes.
//   - slideTick — how often Go writes the progress. Four times a second, smoothed
//     by a 420ms transition in CSS (design/slideshow.go), which is continuous to
//     the eye and costs nothing: a tick sets two custom properties and does not
//     re-render anything.
const (
	slideCardHold = 2600 * time.Millisecond
	slideExit     = 900 * time.Millisecond
	slideSettle   = 1200 * time.Millisecond
	slideTick     = 220 * time.Millisecond
)

// slideScrollRate is how fast the story may scroll, in CSS pixels per second.
//
// **This is the number that decides whether the slideshow is readable**, and it
// is a rate rather than "however far the article is" on purpose. Fitting a
// four-thousand-word essay into a forty-second slide means scrolling it at a
// speed no one can read, which produces the worst possible outcome: text moving
// past too fast to follow, which is more irritating than no text at all.
//
// So the slide scrolls at a READING pace and simply does not reach the end of a
// long article — the reader gets the opening of it, at a speed they can actually
// take in, and the story ends where the time ran out. That is what an ambient
// display is for; the whole article is one keystroke away in the reader itself.
//
// 18px/s is about one line every two seconds at this file's type size, which is
// roughly the pace of reading aloud — and reading aloud is the right reference,
// because in the other mode that is literally what is happening.
const slideScrollRate = 18.0

// The bounds on an automatic dwell. A stub with a two-line body still needs long
// enough to read the headline and register the source; a long read still has to
// give way, because a display that sits on one story for four minutes has
// stopped being a news display.
const (
	slideMinDwell = 20 * time.Second
	slideMaxDwell = 60 * time.Second
)

// slideVoiceWait is how long read-to-me waits for the narrator before giving up
// on it and running the story on the clock instead.
//
// Generous, because the wait is legitimate: the server has to write the segment
// and then synthesise it, and on a long article both take real seconds. Twenty
// is past the point where any healthy path has produced sound, and short enough
// that a display which is never going to speak starts working within one story
// rather than looking broken forever.
// Raised from 20s, which was wrong for the case it most had to serve. A cold
// broadcast segment is a model call that writes ~210 words and then a synthesis
// of them; twenty seconds is inside the normal range for that, so the backstop
// was firing on healthy instances and announcing that their voice did not exist.
// This is now a backstop against a genuine hang, not a judgement about the
// server — the player's own `error` state is what reports a real failure, and it
// arrives immediately.
const slideVoiceWait = 90 * time.Second

// Why read-to-me is not speaking, as the string the surface renders and the
// stylesheet reads. Empty means it is, or is still expected to.
//
// Three values, because they have three different remedies and the remedy is the
// whole point of saying anything at all: a switch this reader can flip, a
// deployment fact they cannot, and a failure that is neither.
//
// `failed` is worded as an OBSERVATION, and that is a correction rather than a
// nicety. It used to say "the Smart+ voice isn't available on this server",
// inferred from a twenty-second timeout — on an instance whose key was working
// perfectly, and where the first broadcast segment is legitimately slow because
// it is two paid round trips on a cold cache: write the segment, then synthesise
// it. Asserting a configuration fact from a stopwatch is how software tells
// confident lies about itself.
const (
	slideVoiceOff    = "off"
	slideVoiceNoKey  = "nokey"
	slideVoiceFailed = "failed"
)

// --- what read-to-me needs before it can speak --------------------------------

// The prerequisites, in the order the dialog lists them. The order is the order
// they MATTER in: the one that gates everything, then the one that turns a queue
// into a broadcast, then the two that are consequences rather than choices.
const (
	prereqSmartVoice  = "smartVoice"
	prereqPodcast     = "podcast"
	prereqKeepPlaying = "keepPlaying"
	prereqServerKey   = "serverKey"
)

// slidePrereq is one line of the dialog: a thing that has to be true, whether it
// is, and whether this reader can make it so from here.
type slidePrereq struct {
	Key string
	On  bool
	// Fixable is whether a control in the dialog can change it. The server's key
	// is not — it is somebody's deployment, and offering a switch that cannot
	// work is worse than stating the fact.
	Fixable bool
	// Required separates "read-to-me cannot speak without this" from "this is
	// what makes it a broadcast rather than a queue read in a row". Conflating
	// the two would either block a reader who is happy with plain narration, or
	// let someone turn the mode on and wonder why it does not sound like the
	// thing they were promised.
	Required bool
}

// slidePrereqs is the whole dependency graph of read-to-me, in one place.
//
// It exists as a pure function because this list is the thing that was WRONG
// before: the dependency was real, undocumented, and discoverable only by
// turning the mode on and getting silence. A reader should be able to see every
// condition at once, with its current state, and fix the ones that are theirs to
// fix — which is what the dialog this feeds renders.
func slidePrereqs(smartVoice, podcast, keepPlaying, serverKey bool) []slidePrereq {
	return []slidePrereq{
		// The gate. The browser's own synthesiser reads the DOM, so it cannot
		// speak a written segment, cannot hand over between stories, and reports
		// no position for the display to follow.
		{Key: prereqSmartVoice, On: smartVoice, Fixable: true, Required: true},
		// Not required, and deliberately so: read-to-me with plain Smart+ voice
		// is a perfectly good narrated slideshow. This is what makes it a
		// programme.
		{Key: prereqPodcast, On: podcast, Fixable: true},
		// Required, and switched on for the reader when the show starts — listed
		// anyway, because the dialog is a complete picture of what turning this
		// on CHANGES, and a setting that flips itself without appearing anywhere
		// is the kind of surprise that erodes trust in the rest of the screen.
		{Key: prereqKeepPlaying, On: keepPlaying, Fixable: true, Required: true},
		// A fact about the deployment rather than a choice. Read from whether the
		// server minted a listening ticket for the story on screen, which is the
		// same question asked in the only way that cannot go stale.
		{Key: prereqServerKey, On: serverKey, Required: true},
	}
}

// slidePrereqsMet reports whether read-to-me can actually speak.
func slidePrereqsMet(list []slidePrereq) bool {
	for _, p := range list {
		if p.Required && !p.On {
			return false
		}
	}
	return true
}

// slidePrereqBlocked returns the first REQUIRED prerequisite that is missing, or
// "". It is what decides the wording of the line on the slide: a reader who has
// to flip a switch and a reader whose server cannot do this at all are owed
// different sentences.
func slidePrereqBlocked(list []slidePrereq) string {
	for _, p := range list {
		if p.Required && !p.On {
			return p.Key
		}
	}
	return ""
}

// slideAuto is the stored value that means "work it out from the story".
const slideAuto = "auto"

// Where the slideshow's two preferences live.
//
// Server-side like every other preference here, and that is worth stating for
// this one in particular: someone who set a thirty-second pace on the laptop in
// the kitchen has decided how they like the news, not how this browser behaves.
const (
	slidesDwellPref = "slides.dwell"
	slidesAudioPref = "slides.readToMe"
)

// dwellPrefFrom reads the stored pace, defaulting to auto.
//
// A stored value that is not one of the offered choices is kept rather than
// discarded — it is a number of seconds and dwellFor will honour it — because a
// preference set through the API or hand-edited is still a preference, and
// silently replacing it with the default is the behaviour that makes people
// think a setting did not save.
func dwellPrefFrom(prefs map[string]string) string {
	if v := strings.TrimSpace(prefs[slidesDwellPref]); v != "" {
		return v
	}
	return slideAuto
}

// slideDwellChoices are what the settings screen offers, in the order it offers
// them. Strings because that is what a preference is; "auto" is not a number and
// making the others numbers would mean two types for one setting.
var slideDwellChoices = []string{slideAuto, "20", "30", "45", "60", "90"}

// dwellFor is how long one story stays up.
//
// Auto is the default and it is a real answer rather than a hedge: the time a
// story is worth is the time it takes to read it, so this is the card, the
// cross-fade, and the body at about 215 words a minute — bounded at both ends by
// the constants above. A reader who wants a fixed rhythm can have one; a reader
// who has not thought about it gets twenty seconds for a headline-and-a-stub and
// a minute for something with an argument in it.
//
// An unparseable preference falls back to auto rather than to a number, because
// the failure this protects against is a hand-edited or half-migrated pref, and
// landing on the computed answer is better than landing on whatever the first
// choice in a list happens to be.
func dwellFor(words int32, pref string) time.Duration {
	if pref != "" && pref != slideAuto {
		if secs, err := strconv.Atoi(pref); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if words < 0 {
		words = 0
	}
	// 215 words a minute is an unhurried reading pace — slower than the 230 the
	// reader's own "N min read" estimate uses, because that one is for someone
	// sitting at the screen and this is for someone glancing at it.
	read := time.Duration(float64(words) / 215 * float64(time.Minute))
	d := slideCardHold + slideExit + read
	if d < slideMinDwell {
		return slideMinDwell
	}
	if d > slideMaxDwell {
		return slideMaxDwell
	}
	return d
}

// slidePhase names what the slide is doing, and it is the string the stylesheet
// reads off `data-phase`.
//
// Three states and no more: the card is up, the story is open, the slide is
// leaving. The transition between the first two is the whole visual idea (the
// card rises and becomes the header — see design/slideshow.go), so it has to be
// a state rather than an animation keyframe: CSS can interpolate between two
// declared states on demand, and cannot be asked to hold one indefinitely.
//
// `ready` is what makes a slow article look deliberate. A story whose body has
// not landed yet stays on its title card instead of opening onto an empty
// column — so a bad connection reads as a longer title card, which is
// indistinguishable from a design choice, rather than as a blank screen.
func slidePhase(elapsed, dwell time.Duration, ready bool) string {
	switch {
	case elapsed >= dwell-slideExit:
		return "out"
	case elapsed >= slideCardHold && ready:
		return "read"
	default:
		return "card"
	}
}

// slideFill is how far through the slide we are, 0 to 1. It drives the rule
// along the foot of the screen and nothing else.
func slideFill(elapsed, dwell time.Duration) float64 {
	if dwell <= 0 {
		return 0
	}
	return clamp01(float64(elapsed) / float64(dwell))
}

// slideScan is how far through the SCROLL we are, which is not the same thing.
//
// The story does not start moving until the card has gone, and it stops moving
// before the slide leaves — so the last paragraph is still on screen during the
// cross-fade rather than sliding away underneath it. Remapping here rather than
// in CSS keeps both ends adjustable by one number each and keeps the stylesheet
// to one multiplication.
//
// `opened` is when the story actually appeared, which is usually slideCardHold
// and is not always: a body that arrives late opens late. Passing it rather than
// assuming it is what stops a slow fetch from dropping the reader into the
// middle of the first paragraph — the scroll starts from where the text started,
// not from where the clock had got to.
//
// A dwell too short to hold the card and the tail cannot scroll at all, and says
// so by answering 0 rather than by dividing by a negative.
func slideScan(elapsed, opened, dwell time.Duration) float64 {
	span := slideScanSpan(dwell, opened)
	if span <= 0 {
		return 0
	}
	return clamp01(float64(elapsed-opened) / float64(span))
}

// slideShift is how far the story travels, in CSS pixels, as a NEGATIVE offset
// ready to go straight into a transform.
//
// The smaller of "everything there is" and "as much as can be read in the time",
// which is the whole of the argument at slideScrollRate: a slide never scrolls
// faster than it can be read, and a long article simply does not finish.
//
// scanSecs is the time the scroll actually has — the dwell less the card and the
// tail in silent mode, and the narrated length in read mode.
func slideShift(overflow float64, scanSecs float64) float64 {
	if overflow <= 0 || scanSecs <= 0 {
		return 0
	}
	return -math.Min(overflow, scanSecs*slideScrollRate)
}

// slideScanSpan is how long the scroll has: the dwell, less the title card at
// the front and the settle and cross-fade at the back.
//
// One function rather than the same subtraction in three places, because
// slideScan and slideShift have to agree about it exactly — the first decides
// how far through the travel we are and the second decides how long the travel
// is, and a disagreement between them is a story that either stops short or runs
// off the end.
func slideScanSpan(dwell, opened time.Duration) time.Duration {
	return dwell - opened - slideExit - slideSettle
}

// slideScanSeconds is the same span in the units slideShift wants. Zero when
// there is no room to scroll at all.
func slideScanSeconds(dwell, opened time.Duration) float64 {
	span := slideScanSpan(dwell, opened)
	if span <= 0 {
		return 0
	}
	return span.Seconds()
}

func clamp01(v float64) float64 {
	switch {
	case v < 0 || math.IsNaN(v):
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

// speechFrom names the story a broadcast segment hands over from.
//
// The listening ticket is minted by GetItem, long before anyone knows what the
// reader will play this article AFTER — so the predecessor cannot be inside the
// sealed URL and travels beside it instead. The server resolves it through the
// same scope as the item being spoken, so this is a hint about ORDER rather than
// a credential (see internal/app/speech.go, prevItemParam).
//
// `&` unconditionally, because a listening ticket always already carries `?t=`.
// That is worth stating rather than assuming: this would be silently wrong for a
// bare path, and the failure — a URL with two query strings — 404s rather than
// falling back, so it would look like the voice breaking.
//
// Returns the URL untouched unless broadcast mode is on and there is something
// to hand over from. Untouched matters: the browser caches audio by URL, so
// appending an empty or pointless parameter would re-download every segment a
// reader has already heard.
func speechFrom(src, prevID string, podcast bool) string {
	if !podcast || src == "" || prevID == "" {
		return src
	}
	return src + "&p=" + prevID
}

// --- the surface ---------------------------------------------------------------

// The slideshow's own data-action ids. Declared as constants because each is
// written in three places — the button, the click dispatcher and the keyboard
// map — and a typo in any one of them is a control that silently does nothing.
const (
	// The way IN, which lives in the list header rather than in the slideshow —
	// it is an action on the feed you are looking at, and that is where the other
	// actions on the feed are.
	actSlideOpen   = "slide-open"
	actSlideLeave  = "slide-leave"
	actSlidePause  = "slide-pause"
	actSlideNext   = "slide-next"
	actSlidePrev   = "slide-prev"
	actSlideListen = "slide-listen"
	// The pace, from the settings screen. Carries its value in data-value like
	// every other segmented control here.
	actSlideDwell = "slide-dwell"
	// The prerequisites dialog: opening it from the line on the slide, flipping
	// one of the switches it lists, starting once they are met, and leaving.
	actSlideNeeds      = "slide-needs"
	actSlideNeedsFix   = "slide-needs-fix"
	actSlideNeedsStart = "slide-needs-start"
	actSlideNeedsClose = "slide-needs-close"
)

// The transport glyphs. ‹ and › rather than ◀ and ▶, because ▶ is already the
// listen glyph in this application and a second meaning for one mark is how a
// control gets pressed by mistake.
const (
	glyphSlidePrev = "‹"
	glyphSlideNext = "›"
)

type slideProps struct {
	open bool
	// it is the story on screen. nil with open true is the moment between the
	// mode starting and the list resolving, and renders the surface empty rather
	// than not at all — so the fade to the plum ground has already begun by the
	// time the first headline arrives.
	it *pb.Item
	// body is the fetched article, or nil while it is still coming. Separate from
	// `it` because the list stub has the headline and not the text, and the title
	// card is perfectly complete without the second.
	body  *pb.Item
	phase string
	// paused stops the clock in silent mode and the voice in read mode. One flag,
	// because to a reader they are the same button.
	paused bool
	// audio is read-to-me: the narrator paces the slides instead of the clock.
	audio bool
	// speakState is what the voice is doing, so the state line can distinguish
	// "waiting for the server to synthesise this" from "paused" — a wait of five
	// or ten seconds that says nothing looks like a mode that has stopped working.
	speakState string
	// voice is why read-to-me is NOT speaking, or "" when it is. It has to be
	// rendered here rather than left to the reader's notice banner, because the
	// banner is underneath this overlay: a mode that quietly stopped doing the
	// thing its name promises, with the explanation hidden behind itself, is
	// indistinguishable from one that is broken.
	voice string
	// needs is every condition read-to-me depends on, with its current state, and
	// needsOpen is whether the dialog listing them is up. Always computed, even
	// when the dialog is closed — it is four booleans, and the alternative is a
	// second code path that assembles them only when something has already gone
	// wrong, which is the path that would rot.
	needs     []slidePrereq
	needsOpen bool
	// index and total are the running order. One-based when rendered; this is the
	// slice index.
	index int
	total int
	// hosts maps a source id to its favicon host, for the mark in the slug line.
	hosts map[string]string
}

// slideshow renders the whole surface, or nothing at all.
//
// Nothing at all, rather than a hidden element: this holds a parsed article body
// and a full-screen gradient, and leaving both mounted for a mode nobody has
// opened would cost every reader who never uses it. It is the last child of the
// shell, so a late mount appends where it belongs.
func slideshow(tr i18n.Runtime, p slideProps) ui.Node {
	return ui.If(p.open, func() ui.Node {
		hue := map[string]any{}
		if p.it != nil {
			if h := hueVarFor(p.it.GetSourceId()); h != nil {
				hue = h
			}
		}
		// tabindex -1 makes the surface focusable without putting it in the Tab
		// order, the same trick the reading pane uses. Focus has to be able to
		// LEAVE whatever opened the mode — see platform.FocusElement for the bug
		// that comes of it not doing so.
		hue["tabindex"] = "-1"
		return html.Div(html.Props{
			Class: "slides",
			Role:  "region",
			Raw:   hue,
			Data: map[string]string{
				"phase":  p.phase,
				"paused": strconv.FormatBool(p.paused),
				"mode":   slideMode(p.audio),
			},
			Aria: map[string]string{"label": tr.T("slides", "title")},
		},
			// Keyed on the story, so each one's colour arrives with its own fade
			// rather than snapping when the hue variable above changes.
			html.Div(html.Props{Class: "slide-wash", Key: "wash-" + currentID(p.it),
				Aria: map[string]string{"hidden": "true"}}),
			html.Div(html.Props{Class: "slide-vignette",
				Aria: map[string]string{"hidden": "true"}}),
			slideBody(tr, p),
			// The rule's TRACK is outside the slide, so the bottom edge of the
			// picture does not blink at every seam. Its FILL is keyed on the
			// story, and that is load-bearing rather than tidy: the fill carries a
			// 420ms transition, so an element that survived the seam would glide
			// visibly BACKWARDS from full to empty as the next story reset it.
			// Keyed, it is a new element that starts at zero, which is what a
			// playhead reaching the end of one track and starting another does.
			html.Div(html.Props{Class: "slide-rule", Aria: map[string]string{"hidden": "true"}},
				html.I(html.Props{Key: "fill-" + currentID(p.it)})),
			slideHud(tr, p),
			// Above the show and inside it: the reader is in a fullscreen mode,
			// and sending them to a settings screen to answer a question the mode
			// itself raised would mean leaving the thing they were watching.
			slideNeeds(tr, p),
		)
	})
}

// slideMode is the string the stylesheet and the e2e suite both read. Two named
// values rather than a boolean attribute, because "the narrator is driving" and
// "the clock is driving" are different modes rather than one feature turned on.
func slideMode(audio bool) string {
	if audio {
		return "read"
	}
	return "silent"
}

// slideBody is one story: the slug, the headline, and the article under them.
func slideBody(tr i18n.Runtime, p slideProps) ui.Node {
	if p.it == nil {
		return html.Div(html.Props{Class: "slide"})
	}
	it := p.it
	raw := ""
	if p.body != nil {
		raw = p.body.GetContentHtml()
		if raw == "" {
			raw = p.body.GetSummary()
		}
	}
	nodes, empty := parsedBody("slide-"+it.GetId(), raw)

	return html.Div(html.Props{Class: "slide", Key: "slide-" + it.GetId()},
		html.Div(html.Props{Class: "slide-card"},
			html.Div(html.Props{Class: "slide-slug"},
				sourceMark(it.GetSourceId(), p.hosts, "slide-dot"),
				html.Span(html.Props{Class: "slide-source"}, html.Text(it.GetSourceTitle())),
				html.Span(html.Props{}, html.Text(relTime(tr, it.GetPublishedAt()))),
				// The running order. A number that is TRUE — where this story sits
				// in the feed being shown — rather than a decorative marker, which
				// is the only thing that earns a counter a place on a screen this
				// spare. It answers the one question a half-watching reader has:
				// whether the loop is nearly round.
				ui.If(p.total > 0, func() ui.Node {
					return html.Span(html.Props{Class: "slide-order"},
						html.Text(tr.T("slides", "order", i18n.Args{
							"n":     p.index + 1,
							"total": p.total,
						})))
				}),
			),
			html.H1(html.Props{Class: "slide-head"}, html.Text(it.GetTitle())),
			// Only while the card is still up. Once the story has opened, a line
			// saying it is opening is a contradiction on screen.
			ui.If(p.voice == "" && p.body == nil && p.phase == "card", func() ui.Node {
				return html.Div(html.Props{Class: "slide-wait"},
					html.Text(tr.T("slides", "opening")))
			}),
			// Why the voice is not speaking, under the headline where the reader
			// is already looking — not in the HUD, which is invisible until a
			// pointer goes hunting for it.
			//
			// It stays up rather than appearing once. A reader who asked to be
			// read to and is being shown silent slides will wonder again on every
			// story, and a one-time toast they may have missed answers that
			// exactly once.
			//
			// A BUTTON, not a line of text: it says what is wrong, and pressing it
			// opens the thing that fixes it. A message that names a switch in
			// another screen and cannot reach it is a message that has made the
			// reader's problem their own homework.
			ui.If(p.voice != "", func() ui.Node {
				return html.Button(html.Props{
					Class: "slide-wait slide-voice",
					Raw:   map[string]any{"data-action": actSlideNeeds},
				}, html.Text(tr.T("slides", "voice."+p.voice)))
			}),
		),
		// The stage is rendered even when there is nothing to put in it, so the
		// grid keeps its three tracks — the rise from card to header is an
		// interpolation between two track values, and a missing track does not
		// interpolate, it snaps.
		html.Div(html.Props{Class: "slide-stage"},
			html.Div(html.Props{Class: "slide-flow"},
				ui.If(!empty, func() ui.Node {
					return html.Div(html.Props{Class: "slide-body"}, nodes...)
				}),
			),
		),
	)
}

// slideHud is the controls, which fade in when the pointer approaches the
// bottom-right corner and are otherwise not there (design/slideshow.go).
//
// Every one of them has a key as well, and that is the important half: this is a
// mode you may be watching from a sofa, and a control you can only reach by
// finding a pointer is a control that does not exist.
func slideHud(tr i18n.Runtime, p slideProps) ui.Node {
	pauseLabel := tr.T("slides", "pause")
	pauseGlyph := glyphPause
	if p.paused {
		pauseLabel = tr.T("slides", "resume")
		pauseGlyph = glyphListen
	}
	return html.Div(html.Props{Class: "slide-hud"},
		html.Span(html.Props{Class: "slide-state"}, html.Text(slideState(tr, p))),
		slideBtn(actSlidePrev, glyphSlidePrev, tr.T("slides", "previous"), false),
		slideBtn(actSlidePause, pauseGlyph, pauseLabel, p.paused),
		slideBtn(actSlideNext, glyphSlideNext, tr.T("slides", "next"), false),
		// The read-to-me switch is in the HUD as well as in settings, because it
		// is the one setting a reader changes WHILE watching — the moment you
		// decide you would rather be told this than read it is the moment you are
		// already looking at it.
		slideBtn(actSlideListen, glyphListen, tr.T("slides", "readToMe"), p.audio),
		slideBtn(actSlideLeave, glyphRemove, tr.T("slides", "leave"), false),
	)
}

// slideNeeds is the dialog that says what read-to-me is waiting on, and lets the
// reader deal with it without leaving the mode.
//
// # Why a dialog rather than a better sentence
//
// The dependency is not one fact, it is four, and three of them are switches on
// two different settings tabs. A line of copy can name one of them; it cannot
// show which are already on, and it cannot be pressed. Turning a mode on and
// being told — in a sentence, over the top of the thing you wanted — that it
// will not work, with the remedy somewhere else, is the shape of the problem
// this replaces.
//
// # Why the switches are real and immediate
//
// Each toggle here writes the same preference the Listening tab writes, at the
// moment it is pressed. It is not a form with an Apply button: a staged copy of
// four settings is a second source of truth for them, and the first time it
// disagrees with the settings screen nobody will be able to say which is right.
//
// The two that spend money say so on the row. Consent to being read aloud is not
// consent to being read, rewritten and joined up, and a dialog that turns three
// egress switches on behind one confident button would be exactly the kind of
// consent this application does not help itself to.
func slideNeeds(tr i18n.Runtime, p slideProps) ui.Node {
	rows := make([]ui.Node, 0, len(p.needs))
	for _, q := range p.needs {
		rows = append(rows, slideNeedRow(tr, q))
	}
	met := slidePrereqsMet(p.needs)
	return html.Div(html.Props{
		Class: "slide-scrim",
		Data:  map[string]string{"open": strconv.FormatBool(p.needsOpen)},
		Raw:   map[string]any{"data-action": actSlideNeedsClose},
	},
		html.Div(html.Props{
			Class: "slide-needs", Role: "dialog",
			// Stops a click inside the panel counting as a click on the backdrop.
			Raw:  map[string]any{"data-action": "modal-keep"},
			Aria: map[string]string{"modal": "true", "label": tr.T("slides", "needsTitle")},
		},
			html.Div(html.Props{Class: "slide-needs-head"},
				html.Strong(html.Props{}, html.Text(tr.T("slides", "needsTitle"))),
				html.Span(html.Props{Class: "slide-needs-sub"},
					html.Text(tr.T("slides", "needsSub"))),
			),
			html.Div(html.Props{Class: "slide-needs-rows"}, rows...),
			html.Div(html.Props{Class: "slide-needs-foot"},
				// Present but refusing rather than absent, and the label says why:
				// a button that vanishes leaves the reader looking for it, and one
				// that is merely greyed out leaves them guessing which row is the
				// problem.
				html.Button(html.Props{
					Class: "chip slide-needs-go",
					Raw:   map[string]any{"data-action": actSlideNeedsStart},
					Aria:  map[string]string{"disabled": strconv.FormatBool(!met)},
				}, html.Text(tr.T("slides", "needsStart"))),
				html.Button(html.Props{
					Class: "chip chip-mini",
					Raw:   map[string]any{"data-action": actSlideNeedsClose},
				}, html.Text(tr.T("slides", "needsNotNow"))),
			),
		),
	)
}

// slideNeedRow is one requirement: what it is, why, and its state.
//
// The state control is a chip for something the reader owns and a plain word for
// something they do not. That distinction is the row's real job — a screen where
// the server's configuration looks like a switch is a screen that will be
// pressed, repeatedly, by someone who cannot fix what it names.
func slideNeedRow(tr i18n.Runtime, q slidePrereq) ui.Node {
	var control ui.Node
	switch {
	case q.Fixable:
		control = pickChip(actSlideNeedsFix, q.Key, onOff(tr, q.On), q.On)
	case q.On:
		control = html.Span(html.Props{Class: "chip chip-static"},
			html.Text(tr.T("slides", "needsPresent")))
	default:
		control = html.Span(html.Props{Class: "chip chip-static is-missing"},
			html.Text(tr.T("slides", "needsAbsent")))
	}
	return html.Div(html.Props{Class: "slide-need", Key: "need-" + q.Key,
		Data: map[string]string{
			"on": strconv.FormatBool(q.On),
			// Optional requirements are marked so the stylesheet can keep them
			// quieter: they are the difference between "this will not work" and
			// "this is what makes it the thing you wanted".
			"required": strconv.FormatBool(q.Required),
		}},
		html.Div(html.Props{Class: "slide-need-text"},
			html.Span(html.Props{Class: "slide-need-name"},
				html.Text(tr.T("slides", "need."+q.Key))),
			html.Span(html.Props{Class: "slide-need-why"},
				html.Text(tr.T("slides", "why."+q.Key))),
		),
		control,
	)
}

// slideBtn is the HUD's only control shape. Its accessible name is the label,
// and the glyph is hidden, for the reason lead() gives: a screen reader
// announcing the character is worse than announcing the word.
func slideBtn(action, glyph, label string, pressed bool) ui.Node {
	return html.Button(html.Props{
		Class: "slide-btn",
		Title: label,
		Raw:   map[string]any{"data-action": action},
		Aria: map[string]string{
			"label":   label,
			"pressed": strconv.FormatBool(pressed),
		},
	}, html.Span(html.Props{Aria: map[string]string{"hidden": "true"}}, html.Text(glyph)))
}

// slideState is the one line of status, and it exists for a single failure: in
// read mode the server may take five or ten seconds to synthesise a segment, and
// a display that goes quiet for ten seconds with a title card up looks broken.
//
// Ordered by which answer a reader most needs. Paused outranks everything
// because it is the state they caused; loading outranks the steady state because
// it is the one that explains a silence.
func slideState(tr i18n.Runtime, p slideProps) string {
	switch {
	case p.paused:
		return tr.T("slides", "statePaused")
	// Before the two narrating states, because it is the one that CONTRADICTS
	// them: a display reporting "Narrating" in the corner while saying nothing is
	// the reason someone concludes the feature is broken rather than off.
	case p.voice != "":
		return tr.T("slides", "stateSilent")
	case p.audio && p.speakState == "loading":
		return tr.T("slides", "stateSynthesising")
	case p.audio:
		return tr.T("slides", "stateNarrating")
	default:
		return tr.T("slides", "statePlaying")
	}
}
