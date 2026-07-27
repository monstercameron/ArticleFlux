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

		// --- the status line, in the order it outranks itself.
		"statePaused": "Paused",
		// The one that earns its place: Smart+ synthesis of a segment takes
		// seconds, and a display that goes quiet with a title card up looks
		// broken. This is the word that says it is working.
		"stateSynthesising": "Writing the segment",
		"stateNarrating":    "Narrating",
		"statePlaying":      "Playing",
	})
}
