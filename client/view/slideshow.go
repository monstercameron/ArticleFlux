//go:build js && wasm

package view

import (
	"math"
	"strconv"
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

// slideAuto is the stored value that means "work it out from the story".
const slideAuto = "auto"

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
// A dwell shorter than the card and the tail together cannot scroll at all, and
// says so by answering 0 rather than by dividing by a negative.
func slideScan(elapsed, dwell time.Duration) float64 {
	span := dwell - slideCardHold - slideExit - slideSettle
	if span <= 0 {
		return 0
	}
	return clamp01(float64(elapsed-slideCardHold) / float64(span))
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

// slideScanSeconds is how long the scroll has in silent mode. Named rather than
// inlined because slideShift and slideScan have to agree about it, and two
// copies of `dwell - card - exit - settle` is how they would stop agreeing.
func slideScanSeconds(dwell time.Duration) float64 {
	span := dwell - slideCardHold - slideExit - slideSettle
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

// --- the surface ---------------------------------------------------------------

// The slideshow's own data-action ids. Declared as constants because each is
// written in three places — the button, the click dispatcher and the keyboard
// map — and a typo in any one of them is a control that silently does nothing.
const (
	actSlideLeave  = "slide-leave"
	actSlidePause  = "slide-pause"
	actSlideNext   = "slide-next"
	actSlidePrev   = "slide-prev"
	actSlideListen = "slide-listen"
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
			// The rule is INSIDE the surface rather than inside the slide, because
			// it is the one element that must not be replaced between stories: it
			// is the playhead, and remounting it would make it jump back to zero
			// and re-glide from there at every seam.
			html.Div(html.Props{Class: "slide-rule", Aria: map[string]string{"hidden": "true"}},
				html.I(html.Props{})),
			slideHud(tr, p),
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
			ui.If(p.body == nil && p.phase == "card", func() ui.Node {
				return html.Div(html.Props{Class: "slide-wait"},
					html.Text(tr.T("slides", "opening")))
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
	case p.audio && p.speakState == "loading":
		return tr.T("slides", "stateSynthesising")
	case p.audio:
		return tr.T("slides", "stateNarrating")
	default:
		return tr.T("slides", "statePlaying")
	}
}
