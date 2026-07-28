//go:build js && wasm

package view

import (
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/data"
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

// slideHudLinger is how long the transport stays after the last movement.
//
// Long enough to cross the screen and press something without it going away
// under the pointer, short enough that a display left alone is clean again
// before anyone looks up at it. Four seconds is what every video player has
// converged on, and this is close enough to that expectation to need no
// learning.
//
// It does not run while paused: a paused display keeps its controls, because the
// reader needs the way back and the show is not going to hide anything from
// them in the meantime.
const slideHudLinger = 4 * time.Second

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

// The prerequisites, in the order the Podcast settings tab lists them. The order is
// the order
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
func speechFrom(src string, ask speechAsk) string {
	if src == "" || !ask.podcast {
		return src
	}
	if ask.prevID == "" && ask.intro == askIntroDone {
		// The first story of a SPLIT broadcast. Nothing about the opening is sent
		// — not the clock, not the count, not the run-through — because the
		// opening has already been recorded and none of it would be used. All
		// this request has to say is "do not greet anybody": the server decides
		// where the top of the show is by looking for a predecessor, and this
		// request does not have one.
		return src + "&i=0"
	}
	if ask.prevID != "" {
		// Mid-broadcast: hand over from what was just played, and nothing else.
		// The opening parameters are deliberately NOT sent here — the server
		// decides where the top of the show is by whether there is a predecessor,
		// and sending them anyway would only widen the surface it has to ignore.
		return src + "&p=" + ask.prevID
	}
	// The top of the broadcast. Both of these are hints for the greeting: the
	// listener's clock, so "good morning" is true where they are rather than
	// where the server is, and how much is queued, which is the difference
	// between "here's what's happening" and "eleven stories this morning".
	out := src
	if ask.now != "" {
		out += "&now=" + url.QueryEscape(ask.now)
	}
	if ask.stories > 0 {
		out += "&n=" + strconv.Itoa(ask.stories)
	}
	if len(ask.lineup) > 0 {
		out += "&q=" + strings.Join(ask.lineup, ",")
	}
	switch ask.intro {
	case askIntroOnly:
		out += "&i=1"
	case askIntroDone:
		// Sent even though everything else about this request says "top of the
		// broadcast", because that is exactly what makes it necessary: the
		// server decides where the opening goes by looking for a predecessor,
		// and the first story of a split broadcast does not have one.
		out += "&i=0"
	}
	return out
}

// --- the running order ---------------------------------------------------------
//
// The slideshow has always been a view over the LOADED LIST: it steps by index,
// it advances by "the next item in the list", and its slug counts against the
// page. That is exactly right for a display that plays a feed, and exactly wrong
// for one that plays a PROGRAMME — an editorial rundown (§29) is a chosen
// sub-queue in a chosen order, and \"1, 10, 4\" is not a thing list arithmetic can
// express.
//
// So the mode walks a QUEUE of ids. An empty queue means "walk the list", which
// is the behaviour the mode has always had, unchanged and untouched by any of
// this; a non-empty one is a running order and is walked exactly as given.
//
// These are pure and take ids rather than items on purpose. A running order may
// name a story the list pane has never loaded — a rundown can reach page three
// — so the queue cannot be a slice of `*pb.Item` without deciding, here, what to
// do about an item nobody has fetched. That decision belongs to the caller, which
// has a client to fetch with.

// queueIDs resolves what the show should walk: the explicit order if there is
// one, otherwise the loaded list in its own order.
func queueIDs(order []string, list []*pb.Item) []string {
	if len(order) > 0 {
		return order
	}
	out := make([]string, 0, len(list))
	for _, it := range list {
		out = append(out, it.GetId())
	}
	return out
}

// queueIndex is where an id sits in the queue, or -1.
func queueIndex(q []string, id string) int {
	if id == "" {
		return -1
	}
	for i, v := range q {
		if v == id {
			return i
		}
	}
	return -1
}

// queueStep moves through the queue, and `loop` is the difference between a feed
// and a programme.
//
// Walking a feed, the show LOOPS — §19 argues that at length, and the argument
// is that a display you leave running must never turn itself into a dark screen
// with no explanation. A rundown is not that: it has an end because somebody
// chose one, and going round again would replay stories the listener has just
// heard. So the caller passes `loop` and the two modes keep their own answer.
//
// Returns "" when there is nowhere to go.
func queueStep(q []string, id string, delta int, loop bool) string {
	if len(q) == 0 {
		return ""
	}
	i := queueIndex(q, id)
	if i < 0 {
		// The queue changed underneath the display — a feed switch, a refresh
		// that dropped what was showing, a rundown replaced mid-show. Starting
		// again at the top is better than stopping, which is what §19's own
		// recovery already does.
		return q[0]
	}
	next := i + delta
	switch {
	case next >= len(q):
		if !loop {
			return ""
		}
		next = 0
	case next < 0:
		if !loop {
			return ""
		}
		next = len(q) - 1
	}
	return q[next]
}

// queueNext is the story after this one, or "" at the end. It never wraps, in
// either mode: this is what the NARRATOR follows, and a broadcast that silently
// starts again from the top is a second reading of what was just played.
//
// It also does NOT inherit queueStep's restart-at-the-top recovery, and the
// difference is the whole reason this is a separate function. Stepping is
// DELIBERATE — somebody pressed a key and something has to happen, so landing at
// the top beats doing nothing. Advancing is AUTOMATIC, and a story that has left
// the queue (a refresh dropped it, the feed was switched) must end the session
// rather than start it again from the beginning. This is the property
// `itemAfter` documented before the queue replaced it, and it was nearly lost
// with it.
func queueNext(q []string, id string) string {
	if queueIndex(q, id) < 0 {
		return ""
	}
	return queueStep(q, id, 1, false)
}

// queueLineup is the headline run-through the broadcast opens with: this story
// and the few after it, in the order they will actually be told.
//
// `title` resolves an id, and returns "" for a story the caller cannot see yet.
// Those are skipped rather than waited for — the server drops untitled entries
// anyway, so sending one spends a lookup to be told so, and a run-through is a
// nicety that must never be the reason a broadcast does not start.
func queueLineup(q []string, fromID string, max int, title func(string) string) []string {
	if fromID == "" || max <= 0 {
		return nil
	}
	out := make([]string, 0, max)
	found := false
	for _, id := range q {
		if !found && id != fromID {
			continue
		}
		found = true
		if strings.TrimSpace(title(id)) == "" {
			continue
		}
		out = append(out, id)
		if len(out) >= max {
			break
		}
	}
	if len(out) < 2 {
		// One headline is not a run-through, it is the story about to be told.
		return nil
	}
	return out
}

// slideMaxLineup mirrors smart.MaxLineup. Duplicated across the wasm boundary
// for the reason the vibe names are — the server clamps anyway, so the worst a
// drift costs is a couple of ids resolved and discarded.
const slideMaxLineup = 5

// speechAsk is everything the client adds to a minted listening ticket.
//
// A struct rather than four parameters because three of them are optional and
// two are strings — the positional version is the one where somebody eventually
// passes the item id where the timestamp goes and nothing complains.
type speechAsk struct {
	// prevID is the story just played. Empty means this is the top of the show.
	prevID string
	// podcast gates ALL of it: with broadcast mode off none of these parameters
	// mean anything, and appending them would change the URL, which is the
	// browser's audio cache key — re-downloading every segment already heard.
	podcast bool
	// now is the listener's local time, RFC3339 with offset, or "" when unknown.
	now string
	// stories is how many are queued including this one, or 0 when unknown.
	stories int
	// lineup is the first few story IDs, for the headline run-through. Empty is
	// fine and common — a greeting straight into the first story is still a
	// broadcast.
	lineup []string
	// intro says where in the SPLIT opening this request sits. See the askIntro
	// constants.
	intro int
}

// Where a request sits in the split opening.
//
// The opening is its own recording when there is music to time against — the
// programme's theme has to swell and clear before the first story, and the only
// moment a client can see coming is the end of a file. Without music there is
// nothing to time, so the greeting rides on the first segment as it always did
// and one fewer request is paid for.
const (
	// askIntroWith is the unsplit form: greeting and first story in one.
	askIntroWith = iota
	// askIntroOnly asks for the greeting alone.
	askIntroOnly
	// askIntroDone says the greeting has already been recorded — do not send
	// another one. Without this the listener is greeted twice.
	askIntroDone
)

// localStamp composes what platform.LocalNow reports into RFC3339.
//
// Pure, so the one piece of arithmetic here — that a browser's offset is minutes
// and a Go zone is seconds — is testable without a browser. Empty for a clock
// that could not be read, which the caller omits rather than sending: a zero
// timestamp would have the server greeting a listener in 1970.
func localStamp(unixMillis int64, offsetMinutes int) string {
	if unixMillis <= 0 {
		return ""
	}
	return time.UnixMilli(unixMillis).
		In(time.FixedZone("", offsetMinutes*60)).
		Format(time.RFC3339)
}

// The narrator's manner, mirroring smart.Vibe* in internal/smart/podcast.go.
//
// DUPLICATED, and it has to be: internal/smart pulls in the LLM client, the
// store and cgo-linked sqlite, none of which can be compiled to wasm. The server
// is the authority — smart.VibeFor resolves anything it does not recognise to
// the default — so a drift here is a picker whose selection silently does
// nothing, which is why the names are pinned by a test on that side.
const (
	vibeCalm  = "calm"
	vibeBrisk = "brisk"
	vibeDry   = "dry"
	vibeWarm  = "warm"
)

// slideVibeChoices are what the settings screen offers, in the order it offers
// them: the default first, then loudest to quietest.
var slideVibeChoices = []string{vibeCalm, vibeBrisk, vibeWarm, vibeDry}

// vibePrefFrom reads the stored manner, defaulting to calm.
//
// An unrecognised stored value is REPLACED here rather than passed on, unlike
// the dwell preference which is kept. The difference is what the value does: a
// dwell of "17" is a perfectly good number of seconds nobody offered, while a
// vibe of "17" is nothing at all, and showing no chip as selected would leave
// the reader unable to tell what they are listening to.
func vibePrefFrom(prefs map[string]string) string {
	v := strings.ToLower(strings.TrimSpace(prefs[podcastVibePref]))
	for _, c := range slideVibeChoices {
		if v == c {
			return v
		}
	}
	return vibeCalm
}

// podcastVibePref is the stored key, matching internal/app/speech.go.
const podcastVibePref = "tts.podcastVibe"

// How fast the narrator reads, as a playback multiplier.
//
// Client-side and free: the rate is applied to the <audio> element, not asked of
// OpenAI, so changing it costs nothing and applies to audio already synthesised
// and cached. Browsers pitch-correct, so a faster narrator sounds like a person
// reading faster rather than like a cartoon.
const (
	speechRatePref    = "tts.rate"
	speechRateDefault = "1.1"
)

// speechRateChoices are what the settings screen offers.
//
// 1.1 is the DEFAULT rather than 1.0, which is a real opinion: synthesised
// speech is read at a measured, evenly-paced clip that a person listening to
// news does not need, and a little faster is where most people stop noticing the
// pace and start noticing the content. It was 1.2 first and came back down after
// listening to a whole broadcast at it — a tenth is a lot over twenty minutes,
// and the point where "brisk" becomes "hurried" is earlier than it sounds in a
// single paragraph. Slower is offered because "most people" is not everybody,
// and because a second language makes even this tiring.
var speechRateChoices = []string{"0.9", "1", "1.1", "1.3", "1.5"}

// speechRateFrom reads the stored rate, defaulting to 1.1.
//
// An unrecognised value is replaced rather than kept, unlike the dwell pace: a
// dwell of "17" is a perfectly good number of seconds nobody offered, while a
// rate of "17" is seventeen times speed, which is not audio.
func speechRateFrom(prefs map[string]string) string {
	v := strings.TrimSpace(prefs[speechRatePref])
	for _, c := range speechRateChoices {
		if v == c {
			return v
		}
	}
	return speechRateDefault
}

// speechRateValue turns the stored string into the multiplier the player wants.
func speechRateValue(pref string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(pref), 64)
	if err != nil || f < 0.5 || f > 3 {
		f, _ = strconv.ParseFloat(speechRateDefault, 64)
	}
	return f
}

// The opening interlude: what happens between the greeting ending and the first
// story starting. See the choreography in reader.go.
//
// # The handover is triggered by the VOICE, not by a clock
//
// The first version ran it on timers: swell, hold five seconds, fade the theme
// out, bring the bed in, then ask for the first story. That is out of phase by
// construction, because the one duration it does not know is the only one that
// matters — writing and synthesising a segment takes anywhere from a second to
// half a minute. So the theme ended, the bed came up, and the broadcast sat on
// quiet music with nobody talking over it.
//
// Now the theme simply KEEPS PLAYING until the narrator is audible, and the
// crossfade happens on the player's own `playing` event. Nothing waits on a
// guess.
//
// introLead is the one delay left, and it is a delay on the VOICE rather than on
// the music: the player reports the segment ready, the crossfade starts, and the
// narrator is held back two seconds so the news begins into a bed that is
// already there. A crossfade that starts when the voice does is a crossfade you
// hear happening underneath the voice, which is the difference between a
// programme and two things overlapping.
//
// introWait is the backstop. If the voice never arrives at all, the theme must
// not loop under a silent screen forever — so at some point the show crosses to
// the bed anyway and gets on with looking like a broadcast. Long, because the
// legitimate wait is long, and the player's own error state reports a real
// failure immediately.
//
// introHold is the other half, and it is a MINIMUM rather than a schedule: the
// theme plays alone for at least this long after the greeting, however quickly
// the segment turns up. Without it a cached broadcast swells and crossfades in
// the same tenth of a second, which sounds like the music being cut off rather
// than like the end of a phrase. The story is still asked for immediately —
// this delays the handover, never the request.
//
// seamHold is the same idea between two stories: the music comes up, holds, and
// the next segment starts into it. Three seconds, which is short enough not to
// be a pause and long enough to be a beat — without it one story ends and the
// next begins in the same half second, which is what makes a queue sound like a
// queue rather than a programme. Measured from the END of the last story, so a
// segment that took ten seconds to synthesise adds nothing on top.
const (
	introHold = 4 * time.Second
	introLead = 2 * time.Second
	introWait = 45 * time.Second
	seamHold  = 3 * time.Second
)

// podcastBedPref is the opening sting and the low pad underneath the broadcast.
//
// Client-only: the sound is synthesised in the browser (see
// platform/sound_wasm.go), so unlike every other tts.* key the server has no
// opinion about it and never reads it. It is stored server-side anyway, because
// it is a preference about how this reader likes the news and not about this
// browser.
const podcastBedPref = "tts.podcastBed"

// The bed's two reserved values. Everything else stored under podcastBedPref is
// a track id the server named.
//
// bedAuto rather than a track id as the default because the client has no
// business knowing which files a deployment ships: the id it stored last week
// may not exist today, and "whatever this server leads with" survives that where
// a baked-in slug turns into silence nobody can explain.
const (
	bedOff  = "off"
	bedAuto = "auto"
)

// bedTrackFrom reads the stored bed, MIGRATING the switch it used to be.
//
// This preference shipped as a boolean — sound on or off, one synthesised pad —
// and became a track picker when the music moved to real files (§19). So "true"
// and "false" are values in the wild that must keep meaning what they meant, or
// a reader who turned the pad off gets it back the day they update.
func bedTrackFrom(prefs map[string]string) string {
	switch v := strings.TrimSpace(prefs[podcastBedPref]); v {
	case "false":
		return bedOff
	case "", "true":
		return bedAuto
	default:
		return v
	}
}

// The two things a track can be. The server decides which is which (see
// internal/transport/grpcsrv/audio.go); these are the names it uses on the wire.
//
// An unrecognised role counts as a bed, which is the quieter of the two
// mistakes: a piece that was meant to open the show playing quietly underneath
// it is odd, and a piece meant to sit underneath playing at opening volume over
// the narrator is unlistenable.
const (
	roleBed   = "bed"
	roleSting = "sting"
)

// tracksFor is the ids of every track with a role, in the order the server gave
// them.
func tracksFor(list []data.AudioTrack, role string) []string {
	out := make([]string, 0, len(list))
	for _, t := range list {
		if t.Role == role || (role == roleBed && t.Role != roleSting) {
			out = append(out, t.ID)
		}
	}
	return out
}

// stingPick chooses which opening plays, from the seed of the moment.
//
// Random rather than fixed, because the opening is the one piece a regular
// listener hears every single session and the second-most-boring thing it could
// be is the same every time. (The most boring is nothing.) Seeded by the caller
// from the clock rather than drawn from a package generator, so the choice is a
// pure function of an input a test can supply.
func stingPick(list []data.AudioTrack, seed int64) string {
	ids := tracksFor(list, roleSting)
	if len(ids) == 0 {
		return ""
	}
	if seed < 0 {
		seed = -seed
	}
	return ids[seed%int64(len(ids))]
}

// bedTrackID resolves the stored choice against what the server actually has.
//
// Returns "" for silence — which covers three different situations that all
// sound the same and should: the reader chose off, this deployment ships no
// audio at all, and the reader's chosen track is no longer there.
//
// A stored id that has gone missing falls back to the first track rather than to
// silence. The reader asked for music; the specific piece is the part they are
// least attached to.
func bedTrackID(pref string, have []string) string {
	if pref == bedOff || len(have) == 0 {
		return ""
	}
	if pref != bedAuto {
		for _, id := range have {
			if id == pref {
				return id
			}
		}
	}
	return have[0]
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
	// The narrator's manner, from the Listening tab. Carries its value in
	// data-value like every other segmented control here.
	actVibe = "podcast-vibe"
	// The broadcast's own sound: the opening sting and the pad under it.
	actBed = "podcast-bed"
	// How fast the narrator reads, from the Listening tab.
	actRate = "speech-rate"
	// The prerequisites dialog: opening it from the line on the slide, flipping
	// one of the switches it lists, starting once they are met, and leaving.
	actSlideNeeds      = "slide-needs"
	actSlideNeedsFix   = "slide-needs-fix"
	actSlideNeedsStart = "slide-needs-start"
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
	// needs is every condition read-to-me depends on, with its current state.
	// Kept on the slide's props because the VOICE LINE reads it — which
	// requirement is missing decides what that line says, and whether it is worth
	// interrupting a reader for. The list itself is shown in Settings → Podcast;
	// a fullscreen mode is not where four preference switches belong.
	needs []slidePrereq
	// hud is whether the transport is revealed. It fades after a few still
	// seconds and comes back on any movement — see slideHudLinger.
	hud bool
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
				// The transport, and with it the pointer: a display with no
				// controls showing should have no cursor sitting on it either.
				"hud": strconv.FormatBool(p.hud || p.paused),
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
			// **The broadcast is being written.** This is the one status that must
			// be on the SLIDE rather than in the transport, and putting it in the
			// transport was a real mistake: the transport fades after four
			// seconds, writing and synthesising the first segment takes longer
			// than that, and the result was a title card sitting in silence with
			// nothing anywhere on screen saying anything was happening. It read as
			// stalled because there was no evidence it was not.
			//
			// It outranks "opening the story" below, because when both are true
			// the voice is the thing being waited for and the longer of the two.
			ui.If(p.voice == "" && p.audio && p.speakState == "loading", func() ui.Node {
				return html.Div(html.Props{Class: "slide-wait slide-working"},
					html.Text(tr.T("slides", "stateSynthesising")))
			}),
			// Only while the card is still up. Once the story has opened, a line
			// saying it is opening is a contradiction on screen.
			ui.If(p.voice == "" && !(p.audio && p.speakState == "loading") &&
				p.body == nil && p.phase == "card", func() ui.Node {
				return html.Div(html.Props{Class: "slide-wait slide-working"},
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
