// Package tagglyph is the fixed catalogue of marks a tag feed may wear in the
// rail.
//
// It lives in internal/ rather than in the client for the same reason
// internal/signals does: the browser offers the choice and the server enforces
// it, and those two must be reading the same list. A picker built from one list
// and validated against another is a control that can offer something the server
// will refuse — and the refusal arrives as a failed save with nothing on screen
// to explain it.
//
// Three constraints shaped what is in here, and all three are worth stating
// because they rule out the obvious alternative (emoji):
//
//   - TEXT PRESENTATION. The rail's glyphs inherit colour and weight from the
//     row — a selected row's mark goes cream with it, a muted one goes grey.
//     Emoji are their own colour and their own weight, so a rail of them reads
//     as stickers stuck onto the design rather than as part of it, and the one
//     tag wearing an emoji would outshout the 150 feeds above it.
//   - ONE WEIGHT OF MEANING. These are marks, not pictures. A glyph's job is to
//     make a row findable at a glance among fifty others, which needs distinct
//     SHAPES far more than it needs literal depiction.
//   - A CLOSED SET. Fifty is enough to give every tag a distinct mark and small
//     enough to be scanned in one grid. Free text would be a font-availability
//     lottery and, since it renders in the sidebar, an injection surface.
//
// The order is deliberate: the catalogue is grouped, and the picker renders it
// grouped. Fifty ungrouped symbols is a wall that a reader gives up on and
// takes the first thing from; fifty in seven runs of related shapes is a list
// you can navigate to the part you want.
package tagglyph

// Glyph is one entry in the catalogue.
//
// Name is not decoration: it is the accessible label and the tooltip, and it is
// the only thing distinguishing two similar marks for a reader who cannot see
// the difference between ◆ and ◈ at 12px — which, in a sidebar, is most people.
type Glyph struct {
	Char  string
	Name  string
	Group string
}

// The seven groups, in render order.
const (
	GroupShapes  = "Shapes"
	GroupStars   = "Stars"
	GroupNature  = "Nature"
	GroupCraft   = "Craft"
	GroupWriting = "Writing"
	GroupMarks   = "Marks"
	GroupOdds    = "Odds & ends"
)

// List is the catalogue. Fifty entries, and the count is asserted by a test
// rather than trusted to a comment — "a list of 50" is the specification, and a
// list that quietly became 49 during an edit would still compile and still look
// fine.
//
// APPEND-ONLY in spirit. Removing an entry does not corrupt anything (the
// character is stored, not an index — see 0008_tag_style.sql), but it does mean
// a reader who chose it can see it and never pick it again, which is a strange
// thing to do to someone.
var List = []Glyph{
	// Shapes — the workhorses. Filled and outline of the same form are kept
	// adjacent so the pair reads as a pair: it is the cheapest way to give two
	// related tags two related marks.
	{"◆", "Diamond", GroupShapes},
	{"◇", "Diamond outline", GroupShapes},
	{"●", "Dot", GroupShapes},
	{"○", "Ring", GroupShapes},
	{"■", "Square", GroupShapes},
	{"□", "Square outline", GroupShapes},
	{"▲", "Triangle up", GroupShapes},
	{"▼", "Triangle down", GroupShapes},
	{"◈", "Inset diamond", GroupShapes},
	{"◉", "Bullseye", GroupShapes},
	{"◐", "Half circle", GroupShapes},

	// Stars — for the handful of tags that are about worth rather than subject.
	{"★", "Star", GroupStars},
	{"☆", "Star outline", GroupStars},
	{"✦", "Spark", GroupStars},
	{"✧", "Spark outline", GroupStars},
	{"✱", "Asterisk", GroupStars},
	{"❉", "Burst", GroupStars},

	// Nature — subject matter, and the group that carries the most literal
	// meaning. Weather and growing things cover a surprising share of what
	// people actually tag.
	{"☀", "Sun", GroupNature},
	{"☁", "Cloud", GroupNature},
	{"☂", "Umbrella", GroupNature},
	{"☾", "Moon", GroupNature},
	{"❄", "Snowflake", GroupNature},
	{"✿", "Flower", GroupNature},
	{"☘", "Shamrock", GroupNature},
	{"❦", "Leaf", GroupNature},

	// Craft — making, measuring and moving. The trades and the sciences.
	{"⚒", "Hammer", GroupCraft},
	{"⚖", "Scales", GroupCraft},
	{"⚗", "Alembic", GroupCraft},
	{"⚛", "Atom", GroupCraft},
	{"⚜", "Fleur-de-lis", GroupCraft},
	{"⌂", "House", GroupCraft},
	{"⌘", "Command", GroupCraft},
	{"✈", "Aeroplane", GroupCraft},

	// Writing — words, correspondence and the tools of both.
	{"✉", "Envelope", GroupWriting},
	{"✒", "Nib", GroupWriting},
	{"✐", "Pencil", GroupWriting},
	{"☰", "Lines", GroupWriting},
	{"❝", "Quote", GroupWriting},
	{"✂", "Scissors", GroupWriting},
	{"⌨", "Keyboard", GroupWriting},

	// Marks — status rather than subject: flagged, done, dropped, pointed at.
	{"⚑", "Flag", GroupMarks},
	{"⚐", "Flag outline", GroupMarks},
	{"☞", "Pointer", GroupMarks},
	{"✔", "Tick", GroupMarks},
	{"✖", "Cross", GroupMarks},
	{"☑", "Checkbox", GroupMarks},

	// Odds and ends — four shapes with no inherent meaning at all, which is
	// exactly what makes them useful. Not every tag is ABOUT something; some
	// just need to be told apart from the tag above them.
	{"♠", "Spade", GroupOdds},
	{"♣", "Club", GroupOdds},
	{"♪", "Music note", GroupOdds},
	{"♫", "Music notes", GroupOdds},
}

// The grouping, computed once.
//
// Derived from List rather than declared beside it, so adding an entry in a new
// group cannot leave the picker silently dropping it — a second hand-maintained
// list is a second thing to forget.
//
// Computed at init rather than per call because the caller is a RENDER. The
// picker asks for the group order and then for each group's entries, and doing
// that by scanning the catalogue meant walking all fifty entries once per group
// — three hundred and fifty comparisons and eight allocations — every time the
// panel repainted, to rebuild a value that is a compile-time constant in all but
// name.
var (
	groupOrder []string
	byGroup    = map[string][]Glyph{}
)

func init() {
	for _, g := range List {
		if _, seen := byGroup[g.Group]; !seen {
			groupOrder = append(groupOrder, g.Group)
		}
		byGroup[g.Group] = append(byGroup[g.Group], g)
	}
}

// Groups is the render order.
//
// The returned slice is shared, not copied: it is read-only by contract and
// copying it per render would reintroduce half of what the index above removed.
func Groups() []string { return groupOrder }

// In returns the entries of one group, in catalogue order. Shared, as above.
func In(group string) []Glyph { return byGroup[group] }

// index is built once. Validation runs on every tag write, and a linear scan of
// fifty strings per write is cheap but pointless when the set never changes.
var index = func() map[string]Glyph {
	m := make(map[string]Glyph, len(List))
	for _, g := range List {
		m[g.Char] = g
	}
	return m
}()

// Valid reports whether s is a glyph this server will store.
//
// The empty string is valid and means "no glyph — use the section default".
// That is the state every existing tag is already in, so rejecting it would
// make "clear my choice" impossible to express.
func Valid(s string) bool {
	if s == "" {
		return true
	}
	_, ok := index[s]
	return ok
}

// Name returns the accessible name for a glyph, or "" if it is not in the
// catalogue. Used by the picker; a caller rendering a glyph that has since been
// retired gets an empty name rather than a wrong one.
func Name(s string) string {
	return index[s].Name
}
