package i18n

// The tag glyph picker's fifty marks and seven group headings
// (client/view/tagsettings.go, internal/tagglyph).
//
// These are the accessible name AND the tooltip on every cell of a grid of
// fifty unlabelled symbols. Without them the picker is unusable with a screen
// reader and unreadable for anyone who cannot tell ◆ from ◈ at 13px — which is
// exactly the population the names exist for, so leaving them in English is
// not a cosmetic gap.
//
// Keyed by the CHARACTER, not by the English name. The character is the stable
// identity — it is the whole point of a glyph and it is what gets stored on the
// tag — whereas a key derived from "Diamond outline" moves the moment somebody
// edits the wording. internal/tagglyph keeps its own Name field as the
// fallback for a glyph added without a catalog entry.
func init() {
	text(DefaultLocale, "glyph", map[string]string{
		// Shapes
		"◆": "Diamond",
		"◇": "Diamond outline",
		"●": "Dot",
		"○": "Ring",
		"■": "Square",
		"□": "Square outline",
		"▲": "Triangle up",
		"▼": "Triangle down",
		"◈": "Inset diamond",
		"◉": "Bullseye",
		"◐": "Half circle",

		// Stars
		"★": "Star",
		"☆": "Star outline",
		"✦": "Spark",
		"✧": "Spark outline",
		"✱": "Asterisk",
		"❉": "Burst",

		// Nature
		"☀": "Sun",
		"☁": "Cloud",
		"☂": "Umbrella",
		"☾": "Moon",
		"❄": "Snowflake",
		"✿": "Flower",
		"☘": "Shamrock",
		"❦": "Leaf",

		// Craft
		"⚒": "Hammer",
		"⚖": "Scales",
		"⚗": "Alembic",
		"⚛": "Atom",
		"⚜": "Fleur-de-lis",
		"⌂": "House",
		"⌘": "Command",
		"✈": "Aeroplane",

		// Writing
		"✉": "Envelope",
		"✒": "Nib",
		"✐": "Pencil",
		"☰": "Lines",
		"❝": "Quote",
		"✂": "Scissors",
		"⌨": "Keyboard",

		// Marks
		"⚑": "Flag",
		"⚐": "Flag outline",
		"☞": "Pointer",
		"✔": "Tick",
		"✖": "Cross",
		"☑": "Checkbox",

		// Odds
		"♠": "Spade",
		"♣": "Club",
		"♪": "Music note",
		"♫": "Music notes",
	})

	// The group headings, keyed by the group constant's own value. Rendered
	// uppercased by the picker; a locale where that transform is wrong (Turkish
	// dotted i) can supply the cased form here instead.
	text(DefaultLocale, "glyphGroup", map[string]string{
		"Shapes":      "Shapes",
		"Stars":       "Stars",
		"Nature":      "Nature",
		"Craft":       "Craft",
		"Writing":     "Writing",
		"Marks":       "Marks",
		"Odds & ends": "Odds & ends",
	})
}
