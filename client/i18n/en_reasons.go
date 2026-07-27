package i18n

// The ranking's reason labels, keyed by the SCORING TERM rather than by the server's
// prose (§18.9, §18.4).
//
// # Why this namespace exists at all
//
// internal/rank writes a sentence per factor — "from a feed you read closely", "points
// at engadget.com, which you keep opening" — in Go, on the server, in English. Two
// things are wrong with putting those on screen directly.
//
// They cannot be translated. Every other string in this client comes from a catalogue;
// these would arrive over the wire already worded, and the hardcoded-copy lint cannot
// see them because the literal is not in client/view. Keying on the term puts them back
// under the same discipline as everything else.
//
// And they do not fit. Each one is a clause from a longer sentence, so in a list row
// they truncate to "from a feed you rea…", which tells the reader nothing. These labels
// are written for the width that actually exists.
//
// # The register: a noun phrase, not a sentence
//
// Each label names the FACTOR from the reader's side — "your feed", "3 feeds", "seen
// before" — because it sits in a metadata line beside a source and an age, and a
// sentence fragment among data points reads as broken text. The full prose is still one
// hover away, which is where an explanation belongs once the label has told you which
// judgement to ask about.
//
// Positive and negative factors are deliberately not marked with symbols. A chip reading
// "− volume" invites the reader to think the item was punished, when the honest statement
// is that a busy feed's items are normalised against its own volume. The words carry the
// direction where it matters and stay neutral where it does not.
//
// Keys here MUST match internal/rank's term strings exactly. An unrecognised term falls
// back to the server's prose, which is what makes a newer server safe against an older
// client: the row still says something true, just longer.
func init() {
	text(DefaultLocale, "reason", map[string]string{
		// Positive factors.
		//
		// "your topics" rather than the topic's name: the label is one of several on a
		// crowded line and a topic name can be long. The hover carries the specific one.
		"topic": "your topics",
		"feed":  "your feed",
		// The domain, not the feed. §18.6's point is that an aggregator item almost
		// never points at the aggregator, so "a site you open" is a different and
		// sometimes better signal than the feed it arrived through.
		"domain": "a site you open",
		"fresh":  "fresh",
		// The one factor that is about other people rather than about the reader, and the
		// most legible of them all: several feeds carrying one story is corroboration a
		// reader can check for themselves.
		"corroboration": "widely carried",
		"manual":        "your rule",
		"external":      "discussed",
		"deliberate":    "you engage with these",

		// Factors that pushed the item DOWN. Stated as observations, never as verdicts:
		// the reader may disagree with the inference, and a label that asserts a
		// conclusion gives them nothing to disagree with.
		//
		// "busy feed", not "too many posts": §18.4's volume term exists so a weekly
		// essayist is not drowned by a 50-a-day firehose, which is normalisation rather
		// than punishment.
		"volume": "busy feed",
		// "already have this" rather than "duplicate", because from the reader's side
		// that is what it is — the same story, from another source, already on the page.
		"duplicate": "already have this",
		"negative":  "not an interest",
		// The most ambiguous signal in the taxonomy, and the label reflects that. It says
		// what was observed — this has been on screen before — and not what it means. A
		// reader saving something for the weekend is not uninterested, and being told
		// they are is the fastest way to lose their trust in the whole page.
		"skipped": "seen before",
	})
}
