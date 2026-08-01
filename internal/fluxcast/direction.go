package fluxcast

import (
	"strconv"
	"strings"
)

// Direction is everything a format says about HOW a beat should be written,
// resolved down the cascade to one struct (plan §29.7.1).
//
// # It only matters if it reaches the model
//
// Every field here is prompt input. A format that carried an audience and a
// house style and never sent them would be a settings screen with nothing
// behind it — the most expensive kind of feature, because it looks like it
// works. So this type exists to be handed to a Writer, and internal/smart
// renders it into the commission it sends.
//
// # It is EGRESS, and it is declared
//
// Standing, Avoid, Never, Always and Note are prose the instance's owner wrote,
// and they cross to the provider with every beat, alongside the article text
// that already does. That puts them under §18.8's boundary, and under this
// application's blunter standard: a feature that sends your articles somewhere
// and cannot produce the list is a feature that has not finished. The list is
// this struct, and Summary prints it.
//
// # What is NOT here
//
// Nothing derived. Part of day, date, story count, position and the predecessor
// are facts about THIS show that the engine supplies per beat, and a format may
// not set them — see Brief, which carries them separately. Keeping the two
// apart is what stops a format claiming it is Tuesday.
type Direction struct {
	// Audience is who is listening, in the author's words: "a working software
	// engineer, listening while doing something else".
	Audience string
	// Standing is what is true of every show — the things a narrator may
	// assume. "assumes technical fluency; never explain what an API is".
	Standing []string
	// Avoid is the house's list of things not to do. Softer than Never: this is
	// taste, and the writer's own NEVER rules are the ones about honesty.
	Avoid []string

	// Never and Always are constraints, and they are ADDITIVE ONLY. A format
	// may add one and may never remove one — which is what keeps a format file
	// from becoming a jailbreak for the instance's own narrator. The writer's
	// own list is not addressable from here at all; these are appended to it.
	Never  []string
	Always []string

	// Note is the free-text direction for this beat: "open on the fact, not the
	// setup". The most powerful field here and the one most likely to be
	// misused, which is why it is per-beat and resolved rather than global.
	Note string

	// Energy is -2..+2, a modifier that composes with any manner rather than
	// replacing it. The lead block turns it up; the sign-off turns it down.
	Energy int
	// Colour is how much licence the narrator has: "none", "light", "full".
	Colour string
	// Address is whether the narrator says "you".
	Address string
	// Locale is the spoken language and region, BCP-47.
	Locale string
	// Depth is what the beat should INCLUDE — headline, brief, standard,
	// explanatory, analytical. Not the same axis as length: a forty-word beat
	// can be deep or shallow.
	Depth string
}

// Empty reports a direction that would add nothing to a prompt. Checked by the
// writer so an instance with no format sends exactly what it sent before —
// adopting formats has to be inaudible until somebody writes one.
func (d Direction) Empty() bool {
	return d.Audience == "" && len(d.Standing) == 0 && len(d.Avoid) == 0 &&
		len(d.Never) == 0 && len(d.Always) == 0 && d.Note == "" &&
		d.Energy == 0 && d.Colour == "" && d.Address == "" &&
		d.Locale == "" && d.Depth == ""
}

// Merge lays `over` on top of the receiver under the cascade's one rule:
// **scalars override, lists append, energy sums** (plan §29.7.1).
//
// One rule everywhere, so a reader of a format never has to ask which of two
// mechanisms is in play. Lists appending is what makes Never additive-only.
func (d Direction) Merge(over Direction) Direction {
	out := d
	if over.Audience != "" {
		out.Audience = over.Audience
	}
	if over.Colour != "" {
		out.Colour = over.Colour
	}
	if over.Address != "" {
		out.Address = over.Address
	}
	if over.Locale != "" {
		out.Locale = over.Locale
	}
	if over.Depth != "" {
		out.Depth = over.Depth
	}
	if over.Note != "" {
		// The most specific note wins rather than accumulating. Two directions
		// for one beat is an argument, and a model handed both follows the
		// easier one.
		out.Note = over.Note
	}
	out.Energy = clampEnergy(d.Energy + over.Energy)
	out.Standing = appendNew(out.Standing, over.Standing)
	out.Avoid = appendNew(out.Avoid, over.Avoid)
	out.Never = appendNew(out.Never, over.Never)
	out.Always = appendNew(out.Always, over.Always)
	return out
}

// clampEnergy keeps the sum inside the range a manner can absorb. Past ±2 the
// instruction stops modifying a voice and starts replacing it.
func clampEnergy(v int) int {
	if v > 2 {
		return 2
	}
	if v < -2 {
		return -2
	}
	return v
}

// appendNew appends without duplicating, because the cascade visits a rule once
// per scope and the same constraint written at two levels is one constraint —
// repeated in a prompt, it reads as emphasis the author did not intend.
func appendNew(base, add []string) []string {
	if len(add) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base)+len(add))
	for _, s := range base {
		seen[strings.ToLower(strings.TrimSpace(s))] = true
	}
	for _, s := range add {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		base = append(base, strings.TrimSpace(s))
	}
	return base
}

// Summary is the direction as a person reads it — for the cue sheet, and for
// the egress list a reader is owed.
func (d Direction) Summary() string {
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	line := func(label, v string) {
		if v != "" {
			b.WriteString(label + ": " + v + "\n")
		}
	}
	list := func(label string, v []string) {
		for _, s := range v {
			b.WriteString(label + ": " + s + "\n")
		}
	}
	line("audience", d.Audience)
	line("note", d.Note)
	if d.Energy != 0 {
		line("energy", strconv.Itoa(d.Energy))
	}
	line("colour", d.Colour)
	line("address", d.Address)
	line("locale", d.Locale)
	line("depth", d.Depth)
	list("standing", d.Standing)
	list("avoid", d.Avoid)
	list("never", d.Never)
	list("always", d.Always)
	return b.String()
}

// key is the direction's contribution to a script key, in a fixed order so that
// two identical directions built by different paths hash the same.
func (d Direction) key() string {
	if d.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString(d.Audience)
	b.WriteByte(0)
	b.WriteString(d.Note)
	b.WriteByte(0)
	b.WriteString(strconv.Itoa(d.Energy))
	b.WriteByte(0)
	b.WriteString(d.Colour)
	b.WriteByte(0)
	b.WriteString(d.Address)
	b.WriteByte(0)
	b.WriteString(d.Locale)
	b.WriteByte(0)
	b.WriteString(d.Depth)
	for _, l := range [][]string{d.Standing, d.Avoid, d.Never, d.Always} {
		b.WriteByte(0)
		for _, s := range l {
			b.WriteString(s)
			b.WriteByte(1)
		}
	}
	return b.String()
}
