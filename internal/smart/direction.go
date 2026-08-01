package smart

import (
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
)

// The format's direction, on its way to the model (plan §29.7).
//
// internal/fluxcast resolves what a format says about a beat — who is
// listening, what may be assumed, how deep to go, what to avoid — down its
// cascade into one fluxcast.Direction. This file is where that becomes words in
// a prompt. Without it the whole scripting half of a format file is plumbing
// that stops one layer short of the only thing that could act on it.
//
// It is rendered into the INPUT rather than into the instructions, and the
// distinction is the cache: instructions are one string per vibe, shared by
// every beat and versioned by PromptVersion, while this varies per beat and
// belongs with the rest of the per-beat commission.

// writeDirection renders what the format says about this beat.
//
// Plain labelled prose rather than JSON, because what is being conveyed is
// editorial intent — "assume technical fluency", "open on the fact, not the
// setup" — and a structure would be turned back into sentences by the model
// anyway, at the cost of the author's own phrasing, which is the only part of
// this carrying judgement.
//
// Nothing is written when the direction is empty, so an instance with no format
// sends the prompt it has always sent, byte for byte.
func writeDirection(in *strings.Builder, d fluxcast.Direction) {
	if d.Empty() {
		return
	}
	// It outranks the model's defaults and never the NEVER list, and it says so
	// in that order. A format may add a constraint and may never remove one —
	// enforced upstream by Direction.Merge's lists-append rule, and stated here
	// too because this is where a reader of the prompt would look for it.
	in.WriteString("HOW THIS PROGRAMME IS WRITTEN — the editor's direction. " +
		"It outranks your usual judgement about register and depth. " +
		"It does NOT override any of the NEVER rules you were given: those still hold.\n")

	if d.Audience != "" {
		in.WriteString("  Who is listening: " + d.Audience + "\n")
	}
	for _, s := range d.Standing {
		in.WriteString("  Assume: " + s + "\n")
	}
	if d.Depth != "" {
		in.WriteString("  Depth: " + depthLine(d.Depth) + "\n")
	}
	if d.Energy != 0 {
		in.WriteString("  Energy: " + energyLine(d.Energy) + "\n")
	}
	if d.Colour != "" {
		in.WriteString("  Colour: " + colourLine(d.Colour) + "\n")
	}
	switch strings.ToLower(strings.TrimSpace(d.Address)) {
	case "second-person":
		in.WriteString("  Speak to the listener directly; \"you\" is allowed.\n")
	case "none":
		in.WriteString("  Do not address the listener directly; no \"you\".\n")
	}
	if d.Locale != "" {
		in.WriteString("  Write for this locale, including its spelling and its units: " + d.Locale + "\n")
	}
	for _, s := range d.Avoid {
		in.WriteString("  Avoid: " + s + "\n")
	}
	for _, s := range d.Always {
		in.WriteString("  Always: " + s + "\n")
	}
	for _, s := range d.Never {
		in.WriteString("  Never: " + s + "\n")
	}
	if d.Note != "" {
		// Last, because it is the most specific thing anybody said about THIS
		// beat, and a model reading in order treats the last instruction as the
		// operative one.
		in.WriteString("  For this segment in particular: " + d.Note + "\n")
	}
	in.WriteString("\n")
}

// The three ladders, spelled out rather than passed through as a bare word.
//
// "depth: analytical" means nothing to a model on its own; what it needs is the
// sentence saying what to include. Expanding here rather than in the format
// means an author writes one word and the prompt carries the paragraph — and
// means changing what "analytical" asks for is a change in one place instead of
// in every format file anybody has written.

func depthLine(d string) string {
	switch strings.ToLower(strings.TrimSpace(d)) {
	case "headline":
		return "what happened, in one clause. Nothing else."
	case "brief":
		return "what happened, and who reported it. No explanation."
	case "explanatory":
		return "what happened, why it matters, and the one mechanism a listener needs in order to follow it."
	case "analytical":
		return "what happened, why it matters, what it implies, and what to watch next. " +
			"You may draw a conclusion the article does not, provided it follows from what the article actually says."
	default:
		return "what happened, and why it matters."
	}
}

func energyLine(e int) string {
	switch {
	case e >= 2:
		return "considerably more urgent than your usual register. This is the top of the hour."
	case e == 1:
		return "a little more urgent than your usual register."
	case e == -1:
		return "a little flatter than your usual register."
	default:
		return "flat. Say it plainly and stop."
	}
}

func colourLine(c string) string {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case "none":
		return "none. No figures of speech, no editorialising, no raised eyebrows."
	case "full":
		return "full. A turn of phrase, a dry aside, or a note that something is overdue " +
			"are all welcome where the story earns them."
	default:
		return "light. One turn of phrase where it earns its place; not more."
	}
}
