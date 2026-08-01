package i18n

// English copy for the slideshow (client/view/slideshow.go), the mode that puts
// one story on the whole screen and moves on by itself.
//
// Three rules the English follows here, and they are different from the rest of
// the catalog because the reading situation is different — this text is on a
// screen somebody is glancing at from across a room, not reading at a desk:
//
//   - **Nothing apologises and nothing explains itself.** The status line has
//     four possible values and each is one or two words. A sentence on this
//     screen is a sentence nobody is close enough to read.
//   - **The controls are named by what they do to the SHOW**, not by their
//     mechanism: "Next story", not "Advance". A translation should keep the
//     noun — it is what tells a reader that "next" does not mean the next
//     paragraph.
//   - **The status line is in the interface's voice about the DISPLAY**, which
//     is why it is "Narrating" and not "Reading to you": the display is doing
//     the thing, and the second person here would be a claim about a person who
//     may not be in the room.
func init() {
	text(DefaultLocale, "slides", map[string]string{
		// The accessible name of the whole surface. It says what the mode is
		// rather than what is in it, because what is in it changes every minute
		// and is announced by the headline anyway.
		"title": "Slideshow",

		// The running order in the slug line. Two numbers and a word — expanded
		// rather than "3/42", because a slash is read aloud as "slash" and this
		// line is the one a screen reader reaches first.
		"order": "{n} of {total}",

		// Shown under the headline while the story's text is still coming. It
		// says what is happening, not that anything is wrong: a slow fetch here
		// looks like a longer title card, which is fine, and this line is what
		// keeps it from looking like a stall.
		"opening": "Opening the story",

		// --- the transport. Every one of these has a key as well, so the labels
		// are accessible names first and tooltips second.
		"pause":  "Pause",
		"resume": "Resume",
		"next":   "Next story",
		// "Previous story" rather than "Back": there is nothing to go back TO in
		// a display that runs on its own, and "back" would suggest leaving.
		"previous": "Previous story",
		// The name the setting uses too, so the switch on screen and the switch
		// in Settings are recognisably the same thing.
		"readToMe": "Read to me",
		"leave":    "Leave the slideshow",
		// Said once at the top of a script (§19, TODO 11.46). It exists because
		// this mode used to show the ARTICLE while the narrator read a rewritten
		// segment, so a reader who has used it before will notice the article is
		// gone and deserves to be told why rather than left to think something
		// broke.
		"scriptNote": "What the narrator is saying",

		// --- the status line, in the order it outranks itself.
		"statePaused": "Paused",
		// The one that earns its place: Smart+ synthesis of a segment takes
		// seconds, and a display that goes quiet with a title card up looks
		// broken. This is the word that says it is working.
		"stateSynthesising": "Writing the segment",
		"stateNarrating":    "Narrating",
		"statePlaying":      "Playing",
		// Read to me is on and nothing is speaking. It outranks the two above,
		// because a corner reporting "Narrating" over silence is the reason
		// someone concludes the feature is broken rather than switched off.
		"stateSilent": "Not speaking",

		// --- why read to me is not speaking, shown under the headline.
		//
		// Both name the REMEDY, because that is the only reason to say anything:
		// the first is a switch this reader can flip, the second is the server's
		// configuration and nothing they do in this window will change it. Neither
		// apologises, and neither says "error" — the display is working, and one
		// part of it is switched off.
		// All three end in the same place — press this to see what is needed —
		// because the line IS a button. None of them says "error": the display is
		// working, and one part of it is not switched on.
		//
		// `failed` is an OBSERVATION and deliberately not a diagnosis. It replaced
		// "The Smart+ voice isn't available on this server", which was inferred
		// from a timeout on an instance whose key worked perfectly — a confident
		// claim about somebody's deployment, drawn from a stopwatch. If we do not
		// know why, we do not get to say why.
		"voice.off":    "Read to me needs a couple of things switched on — see what",
		"voice.nokey":  "This server has no Smart+ key, so read to me can't speak here. Playing silently",
		"voice.failed": "The voice didn't start, so this is playing silently — see what read to me needs",
		// Not a fault, and the only line in this group that is not. It shares the
		// slot because it answers the same question — why is nobody talking —
		// and a reader who has just heard a sign-off should see the show agree
		// with them rather than an error about a voice that worked.
		"voice.ended": "That's the end of the broadcast",

		// --- what read-to-me needs
		//
		// These moved to Settings → FluxCast when the dialog they used to fill was
		// taken out of the slideshow; the WORDING stays here because it describes
		// the slideshow's own dependency, and a requirement with two descriptions
		// in two packages is a requirement whose descriptions drift.
		//
		// The title says what the reader ASKED FOR rather than what is wrong,
		// because they usually arrive having just pressed something.
		"needsTitle": "Read to me needs a few things on",
		// The two states of something the reader does not control. "Ready" rather
		// than "on", because a key is not a switch they flipped.
		"needsPresent": "ready",
		"needsAbsent":  "not on this server",
		"needsStart":   "Start reading to me",

		// One line per requirement, and one line saying WHY. The why is the part
		// that stops this reading as a checklist somebody has to satisfy: each one
		// names what it buys, or what breaks without it.
		"need.smartVoice":  "Smart+ voice",
		"why.smartVoice":   "The browser's own voice reads the page, so it can't speak a written segment or say where it has got to. Sends article text to OpenAI.",
		"need.podcast":     "Join the stories up",
		"why.podcast":      "Optional. Rewrites each article to hand over from the one before it, so it sounds like a broadcast rather than a queue.",
		"need.keepPlaying": "Keep playing",
		"why.keepPlaying":  "The queue moving on is what moves the picture on. Without it the display stops after one story.",
		"need.serverKey":   "An OpenAI key on the server",
		"why.serverKey":    "Set by whoever runs this server, not from here.",
	})
}
