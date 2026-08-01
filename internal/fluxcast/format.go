package fluxcast

import (
	"strconv"
	"strings"
	"time"
)

// A Format is the SHAPE of a show, independent of what is in it.
//
// # Why this is separate from the profile
//
// A profile answers "how does this sound" — how long a lead runs, how deep the
// bed ducks, how long a seam holds. A format answers "what happens, in what
// order, for how long": greeting, headlines, three minutes of world news, a
// tease, six minutes of technology, a recap at exactly ten minutes, sign-off.
// The two are orthogonal, and keeping them so is what makes a format reusable —
// the same half-hour magazine shape can be brisk or ambient without being
// rewritten, and the same tuning can drive a two-minute bulletin.
//
// # Blocks flow, anchors do not
//
// Most blocks simply follow the one before. A block with At set is ANCHORED: it
// must start at that offset into the show, which is what makes "the recap is at
// the half hour" expressible. The compiler pads with music when the show runs
// early and reports a note when it runs late, because it cannot go back in time
// and pretending otherwise is how a running order silently stops meaning
// anything.
//
// # Targets are honoured by writing shorter, not by cutting mid-sentence
//
// A block with a Target gets its stories' word budgets fitted to it, inside
// per-role bounds. That is the only honest lever: audio length is words divided
// by speaking rate, and the two things you can change are how many stories and
// how many words each gets. Truncating audio to fit a clock is what a bad
// automation system does, and it is audible immediately.
type Format struct {
	Name string
	// Target is the whole show. Zero means "as long as the content is", which
	// is what a feed-shaped programme wants; a broadcast slot wants a number.
	Target Dur
	// Tolerance is how far off Target the compiler may land before it says so,
	// as a fraction. Broadcast practice is tight; a podcast does not care.
	Tolerance float64
	Blocks    []Block
}

// BlockKind is what a block is made of.
type BlockKind string

const (
	// BlockGreeting is the top of the show: the greeting, the date, and — when
	// the block asks for it — the headline run-through.
	BlockGreeting BlockKind = "GREETING"
	// BlockStories is the substance: some number of stories from the pool.
	BlockStories BlockKind = "STORIES"
	// BlockTease names what is coming later in the show. It reads from a
	// LATER block, which is why a format is planned whole rather than
	// streamed: a tease that cannot see ahead has nothing to say.
	BlockTease BlockKind = "TEASE"
	// BlockRecap names what has already been covered. The mirror of a tease,
	// and the thing that makes a long programme navigable for somebody who
	// joined it late.
	BlockRecap BlockKind = "RECAP"
	// BlockBreak is music alone, for a fixed length. A break with no length is
	// a break nobody scheduled.
	BlockBreak BlockKind = "BREAK"
	// BlockSignOff ends it.
	BlockSignOff BlockKind = "SIGN_OFF"
)

// A Filter narrows which stories a block may take.
//
// Deliberately small: themes and roles, include and exclude. Anything richer is
// a ranking question and belongs upstream — by the time a running order reaches
// a format, somebody has already decided what matters, and a format that
// re-sorts it is a second opinion with no reasons behind it.
type Filter struct {
	// Themes admits only these themes. Empty admits everything.
	Themes []string
	// NotThemes excludes. Applied after Themes, so a block can say "anything
	// except sport" without listing the other twenty-five.
	NotThemes []string
	// Roles admits only these roles (LEAD, STANDARD…). Empty admits all.
	Roles []string
}

func (f Filter) admits(s Story) bool {
	if len(f.Themes) > 0 && !containsFold(f.Themes, s.Theme) {
		return false
	}
	if len(f.NotThemes) > 0 && containsFold(f.NotThemes, s.Theme) {
		return false
	}
	if len(f.Roles) > 0 && !containsFold(f.Roles, s.Role) {
		return false
	}
	return true
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(v)) {
			return true
		}
	}
	return false
}

// A Block is one section of a show.
type Block struct {
	// Name is what the sheet calls it — "headlines", "world", "the long one".
	// Free text, because it is for a person: the compiler identifies blocks by
	// position, so renaming one cannot break a programme.
	Name string
	Kind BlockKind

	// Take is how many stories this block wants. Zero on a STORIES block means
	// "everything left that this block's filter admits", which is what the
	// last block of a feed-shaped show wants and what every block of a
	// fixed-length show must not be.
	Take   int
	Filter Filter

	// Target is how long this block should run. Zero means "however long its
	// content is" — the block flows rather than fits.
	Target Dur
	// Min and Max are hard bounds the fitter may not cross even to hit a
	// target. A block that cannot reach its target inside them says so rather
	// than silently missing it.
	Min, Max Dur

	// At anchors the block's START to an offset into the show. Zero means it
	// simply follows the block before. This is the field that makes "the recap
	// is at ten minutes, whatever else happened" expressible, and it is also
	// the one that can fail — see Format's own comment.
	At Dur
	// Pad allows the compiler to hold music before an anchored block when the
	// show is running early. Without it, arriving early is reported rather
	// than filled, which is what a format wants when the anchor is a
	// preference rather than a commitment.
	Pad bool

	// Lineup asks a GREETING block to run through the headlines, or a TEASE or
	// RECAP block how many stories to name.
	Lineup int
	// Look is how far a TEASE looks ahead, in blocks. Zero means "the next
	// block". A recap ignores it and looks at everything already played.
	Look int

	// Words overrides the word budget for the non-story beats a block
	// produces — a greeting, a tease, a recap, a sign-off. Zero takes the
	// engine's own sizing, which is derived from how many headlines the beat
	// has to carry rather than typed as a constant.
	Words int

	// Music says what the mix does under this block. Empty inherits: the show
	// opens on the theme and runs on the bed, which is the ordinary answer.
	Music MusicMode
}

// MusicMode is what the mix does under a block.
type MusicMode string

const (
	// MusicInherit is the ordinary answer: whatever the show is already doing.
	MusicInherit MusicMode = ""
	// MusicNone plays this block dry. A serious story under a bed can read as
	// flippant, and a format should be able to say so.
	MusicNone MusicMode = "none"
	// MusicUnder keeps the bed, ducked, which is the default under speech.
	MusicUnder MusicMode = "under"
	// MusicFeature brings the music up: a break, a sting, the end.
	MusicFeature MusicMode = "feature"
)

// Duration is what a block asks for, or zero.
func (b Block) wants() time.Duration { return b.Target.D() }

// ---------------------------------------------------------------------------
// Stock formats
// ---------------------------------------------------------------------------

// Formats are the shapes that ship.
func Formats() []string { return []string{"feed", "bulletin", "magazine", "wire"} }

// FormatByName returns a stock format, or the feed shape for an unknown name —
// the same fallback Preset makes, for the same reason: a broadcast that refuses
// to start because a format was renamed is worse than one that plays the
// ordinary shape.
func FormatByName(name string) Format {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bulletin":
		return BulletinFormat(0)
	case "magazine":
		return MagazineFormat(0)
	case "wire":
		return WireFormat()
	default:
		return FeedFormat()
	}
}

// FeedFormat is the show as it shipped before formats existed: a greeting with
// a run-through, every story there is, and a goodbye.
//
// It is the DEFAULT and it has no Target, which is the honest description of
// what the broadcast has always been — a feed read aloud, as long as the feed.
func FeedFormat() Format {
	return Format{
		Name:      "feed",
		Tolerance: 0.1,
		Blocks: []Block{
			{Name: "opening", Kind: BlockGreeting, Lineup: 3},
			{Name: "stories", Kind: BlockStories},
			{Name: "close", Kind: BlockSignOff},
		},
	}
}

// BulletinFormat is a fixed-length news bulletin: headlines, the lead block,
// the rest, out. total of zero takes twenty minutes, which is the length the
// settings screen offers by default.
//
// Everything in it is fitted, which is the point: a bulletin that runs to
// nineteen or twenty-three minutes depending on the feed is not a bulletin.
func BulletinFormat(total time.Duration) Format {
	if total <= 0 {
		total = 20 * time.Minute
	}
	// ONE block carries the target, and the rest flow. That is not a
	// simplification, it is the only arrangement that works: a greeting, a
	// lead and a sign-off have natural lengths that no target can stretch —
	// a sign-off is forty words and there is no honest way to make it two
	// minutes — so giving them shares of a clock produces three blocks that
	// each report they cannot reach it. The body is the only part with enough
	// stories in it to absorb a target, so the body gets it and the rest are
	// allowed for.
	//
	// The allowances below are what those flowing blocks actually take at the
	// default tuning, and they are deliberately generous by a few seconds: the
	// show-level fit trims the difference, and it can only trim if there is a
	// difference to trim.
	const (
		headlineAllow = 40 * time.Second // greeting, date and three headlines
		leadAllow     = 95 * time.Second // 220 words at 150wpm, plus its seam
		closeAllow    = 20 * time.Second // forty words and the outro's rise
		musicAllow    = 20 * time.Second // the theme's phrase and the crossfade
	)
	body := total - headlineAllow - leadAllow - closeAllow - musicAllow
	if body < total/3 {
		// A show too short to carry an opening at all. The body gets most of
		// it and the fitter reports whatever it cannot manage.
		body = total * 2 / 3
	}
	return Format{
		Name:      "bulletin",
		Target:    Dur(total),
		Tolerance: 0.05,
		Blocks: []Block{
			{Name: "headlines", Kind: BlockGreeting, Lineup: 3},
			{Name: "lead", Kind: BlockStories, Take: 1, Filter: Filter{Roles: []string{"LEAD"}}},
			{Name: "main", Kind: BlockStories, Target: Dur(body)},
			{Name: "close", Kind: BlockSignOff},
		},
	}
}

// MagazineFormat is the long shape: an opening, two substantial halves with a
// tease before the break and a recap after it, and a close.
//
// The recap is ANCHORED at the midpoint, which is the thing this format exists
// to demonstrate — a listener who joins at the half hour gets caught up at a
// time somebody chose, not whenever the first half happened to finish.
func MagazineFormat(total time.Duration) Format {
	if total <= 0 {
		total = 40 * time.Minute
	}
	half := total / 2
	// Only the two halves carry targets, for the reason BulletinFormat gives at
	// length: an opening, a tease, a recap and a sign-off have natural lengths,
	// and a clock cannot stretch forty words of goodbye into two minutes.
	const (
		openAllow  = 40 * time.Second
		teaseAllow = 25 * time.Second
		recapAllow = 30 * time.Second
		closeAllow = 20 * time.Second
		breakLen   = 30 * time.Second
	)
	return Format{
		Name:      "magazine",
		Target:    Dur(total),
		Tolerance: 0.05,
		Blocks: []Block{
			{Name: "opening", Kind: BlockGreeting, Lineup: 3},
			{Name: "first half", Kind: BlockStories,
				Target: Dur(half - openAllow - teaseAllow - breakLen)},
			{Name: "coming up", Kind: BlockTease, Lineup: 2, Look: 1},
			{Name: "break", Kind: BlockBreak, Target: Dur(breakLen), Music: MusicFeature},
			// The anchor is the point of this format: a listener who joins at
			// the half hour is caught up at a time somebody chose, not whenever
			// the first half happened to finish.
			{Name: "what you missed", Kind: BlockRecap, Lineup: 3, At: Dur(half), Pad: true},
			{Name: "second half", Kind: BlockStories,
				Target: Dur(half - recapAllow - closeAllow)},
			{Name: "close", Kind: BlockSignOff},
		},
	}
}

// WireFormat is stories and nothing else: no greeting, no music, no goodbye.
// The shape that proves the engine does not depend on its own choreography.
func WireFormat() Format {
	return Format{
		Name:      "wire",
		Tolerance: 0.2,
		Blocks: []Block{
			{Name: "wire", Kind: BlockStories, Music: MusicNone},
		},
	}
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

// Validate reports what a format cannot do before anybody plans against it.
//
// Like Profile.Validate it corrects rather than refuses, and says what it
// corrected. A format is authored — by a person, or by a settings screen, or
// one day by a model — and the failures are all of the shape "these numbers
// cannot all be true at once", which is worth being told plainly.
func (f Format) Validate() (Format, []Note) {
	var notes []Note
	note := func(path, given, used, why string) {
		notes = append(notes, Note{Path: path, Given: given, Used: used, Why: why})
	}
	if f.Tolerance <= 0 {
		f.Tolerance = 0.1
	}
	if f.Tolerance > 1 {
		f.Tolerance = 1
	}

	var flow time.Duration
	seen := map[BlockKind]int{}
	for i := range f.Blocks {
		b := &f.Blocks[i]
		path := "format." + strconv.Itoa(i)
		if b.Name == "" {
			b.Name = strings.ToLower(string(b.Kind))
		}
		seen[b.Kind]++

		if b.Min > 0 && b.Max > 0 && b.Min > b.Max {
			note(path+".min", b.Min.String(), b.Max.String(),
				"a floor above the ceiling would fix the block at the ceiling")
			b.Min = b.Max
		}
		if b.Target > 0 && b.Max > 0 && b.Target > b.Max {
			note(path+".target", b.Target.String(), b.Max.String(),
				"a target above the block's own ceiling can never be reached")
			b.Target = b.Max
		}
		if b.Target > 0 && b.Min > 0 && b.Target < b.Min {
			note(path+".target", b.Target.String(), b.Min.String(),
				"a target below the block's own floor can never be reached")
			b.Target = b.Min
		}
		// A break with no length is a break nobody scheduled.
		if b.Kind == BlockBreak && b.Target <= 0 {
			note(path+".target", "0", "10s", "a break with no length is silence of an unknown duration")
			b.Target = Dur(10 * time.Second)
		}
		// An anchor before everything that must precede it cannot be met, and
		// the compiler will say so per show — but a format that is impossible
		// on its own terms can say so now.
		if b.At > 0 && b.At.D() < flow {
			note(path+".at", b.At.String(), fmtDur(flow),
				"anchored before the blocks that must come first have finished")
			b.At = Dur(flow)
		}
		if b.At > 0 {
			flow = b.At.D()
		}
		flow += b.wants()
	}

	if seen[BlockGreeting] > 1 {
		note("format", strconv.Itoa(seen[BlockGreeting]), "1", "a programme does not greet you twice")
	}
	if seen[BlockSignOff] > 1 {
		note("format", strconv.Itoa(seen[BlockSignOff]), "1", "a programme does not end twice")
	}
	// A fixed-length show whose blocks all take "everything left" cannot be
	// fitted: the fitter has nothing to divide.
	if f.Target > 0 {
		open := 0
		for _, b := range f.Blocks {
			if b.Kind == BlockStories && b.Take == 0 && b.Target <= 0 {
				open++
			}
		}
		if open > 1 {
			note("format", strconv.Itoa(open), "1",
				"more than one open-ended block in a fixed-length show: the fitter cannot divide a target between blocks that each want everything")
		}
	}
	return f, notes
}

// TotalTarget is the sum of what the blocks ask for, which is what the show
// will run to when no show-level target overrides it.
func (f Format) TotalTarget() time.Duration {
	if f.Target > 0 {
		return f.Target.D()
	}
	var t time.Duration
	for _, b := range f.Blocks {
		t += b.wants()
	}
	return t
}
