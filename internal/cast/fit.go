package cast

import (
	"strconv"
	"time"
)

// Fitting: how a show lands on a clock.
//
// # There is exactly one honest lever
//
// Spoken length is words divided by speaking rate. The rate belongs to the
// listener and the audio belongs to a synthesiser, so the only thing an editor
// can change is HOW MANY WORDS each beat gets — which is why every target in
// this package resolves to a word budget and never to a cut, a fade or a
// speed-up. Truncating audio to fit a clock is what bad automation does and it
// is audible in the first second.
//
// # Bounds are not advisory
//
// A lead cannot be written in forty words and a namecheck cannot carry two
// hundred. Every beat therefore has a floor and a ceiling derived from its role,
// and the fitter will MISS A TARGET rather than cross one — then say so. A
// format that cannot be met is a format problem, and a fitter that silently
// produced a 40-word lead would hide it behind a number that looked right.
//
// # The estimate is a model of a model
//
// Words per minute is an average of a synthesiser reading prose that has not
// been written yet. It is close, it is never exact, and the sheet marks every
// estimate with a tilde for that reason. Trace.Calibrate closes the loop: play a
// show, measure what the audio actually ran to, and the next plan is fitted
// against a number that came from this voice rather than from a textbook.

// roleBounds is how far a beat's budget may move from its role's own length.
//
// Asymmetric on purpose. There is more room to shorten than to lengthen: prose
// written to 60% of a standard telling is a tighter version of the same story,
// while prose written to 200% is padding, and padding is the specific failure
// that made the script revision cut its own word count from 210 to 140.
// Bounds are computed from the beat's BASE budget — what it was allocated
// before any fitting — rather than from its current one. Two passes run over
// most beats (the block's, then the show's), and bounds read off an
// already-fitted budget would let the second pass cut 55% of the first pass's
// 55%: a lead trimmed twice into a namecheck, with every individual step inside
// its own bounds.
func roleBounds(p Profile, b Beat) (min, max int) {
	base := b.Base
	if base <= 0 {
		base = b.Words
	}
	if base <= 0 {
		base = RoleWords(p, b.Role)
	}
	switch b.Kind {
	case BeatStory:
		min = pctOf(base, 0.55)
		max = pctOf(base, 1.45)
		if floor := p.Editorial.WordsMention; min < floor {
			min = floor
		}
	case BeatOpening, BeatTease, BeatRecap:
		// A greeting can be brisk or expansive, but below about twenty words it
		// stops being a greeting and becomes an announcement.
		min = maxInt(20, pctOf(base, 0.5))
		max = pctOf(base, 1.8)
	case BeatSignOff:
		// Forty words is the difference between "it finished" and "it broke";
		// much more is a programme that will not let you go.
		min = 15
		max = 90
	default:
		min, max = base, base
	}
	if max < min {
		max = min
	}
	return min, max
}

func pctOf(v int, f float64) int { return int(float64(v)*f + 0.5) }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// fitBlocks fits each block that asked for a length.
//
// Per block rather than per show first, because a format's blocks are its
// promises: "three minutes of world news" has to be three minutes even if the
// show as a whole is flexible, and fitting globally would rob a short block to
// pay a long one.
func fitBlocks(beats []Beat, f Format, p Profile, rate float64) []Note {
	var notes []Note
	for bi, blk := range f.Blocks {
		target := blk.wants()
		if target <= 0 {
			continue
		}
		var idx []int
		for i := range beats {
			if beats[i].BlockIdx == bi && !beats[i].Fixed {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			continue
		}
		// The block's own choreography comes out of its target: a block of six
		// stories with a three-second seam owes fifteen seconds to music, and a
		// fitter that ignored that would overrun by exactly that much.
		overhead := blockOverhead(beats, idx, p)
		want := target - overhead
		if want < 0 {
			want = 0
		}
		got := fitTo(beats, idx, want, p, rate)
		if off := absDur(got - want); want > 0 && float64(off) > float64(want)*f.Tolerance {
			notes = append(notes, Note{
				Path:  "format." + blk.Name,
				Given: fmtDur(target), Used: fmtDur(got + overhead),
				Why: "the block cannot reach its target inside its stories' word bounds — " +
					reason(got, want) + " (add stories, or widen the roles it takes)",
			})
		}
	}
	return notes
}

// fitShow fits the whole programme to a show-level target, using only the beats
// in blocks that did not name their own.
//
// A block with a target has made a promise; the show-level fit is what is left
// over. When every block named a target there is nothing to move, and the
// difference is reported rather than forced — a show whose blocks all insist on
// their lengths has already decided its length.
func fitShow(beats []Beat, f Format, p Profile, rate float64) []Note {
	target := f.Target.D()
	if target <= 0 {
		return nil
	}
	var free []int
	for i := range beats {
		if beats[i].Fixed {
			continue
		}
		if bi := beats[i].BlockIdx; bi < len(f.Blocks) && f.Blocks[bi].wants() > 0 {
			continue
		}
		free = append(free, i)
	}

	total := func() time.Duration {
		var t time.Duration
		for i := range beats {
			if beats[i].Fixed {
				t += beats[i].fixedLen
				continue
			}
			t += estimate(beats[i].Words, p.Editorial.WordsPerMinute, rate)
		}
		return t + choreoOverhead(beats, p)
	}

	now := total()
	if absDur(now-target) <= time.Duration(float64(target)*f.Tolerance) {
		return nil
	}

	var notes []Note
	// Two attempts, and the order encodes an editorial rule: a block's target
	// is a promise, and the show's length is a bigger one. So the flexible
	// beats move first, and only if they cannot close the gap are the promised
	// blocks relaxed — with a note, because a block that did not run to the
	// length it was given is something the person who wrote the format needs
	// told.
	attempts := [][]int{free}
	if len(free) == 0 {
		attempts = nil
	}
	all := make([]int, 0, len(beats))
	for i := range beats {
		if !beats[i].Fixed {
			all = append(all, i)
		}
	}
	attempts = append(attempts, all)

	for n, idx := range attempts {
		if len(idx) == 0 {
			continue
		}
		var carry time.Duration
		for _, i := range idx {
			carry += estimate(beats[i].Words, p.Editorial.WordsPerMinute, rate)
		}
		want := carry + (target - now)
		if want < 0 {
			want = 0
		}
		got := fitTo(beats, idx, want, p, rate)
		after := now - carry + got
		if absDur(after-target) <= time.Duration(float64(target)*f.Tolerance) {
			if n > 0 && len(free) > 0 {
				notes = append(notes, Note{
					Path: "format.target", Given: fmtDur(target), Used: fmtDur(after),
					Why: "the blocks that named their own lengths were relaxed to land the show on its target",
				})
			}
			return notes
		}
		now = after
		if n == len(attempts)-1 {
			notes = append(notes, Note{
				Path: "format.target", Given: fmtDur(target), Used: fmtDur(after),
				Why: "the show cannot reach its target inside its beats' word bounds — " +
					reason(got, want) + " (add or drop stories, or change the format's shape)",
			})
		}
	}
	return notes
}

// fitTo drives the beats at idx toward want, and returns what it achieved.
//
// Proportional, clamped, and iterated: each pass scales every unclamped beat by
// the ratio still needed, then re-clamps whatever crossed a bound and shares the
// remaining deficit among the rest. Converges in a handful of passes for any
// realistic block, and the bounded ones simply stop moving.
//
// Proportional rather than equal, because a lead and a namecheck are not two
// interchangeable slots: taking ten seconds equally out of both would leave the
// namecheck at nothing and the lead barely touched, when what an editor means by
// "trim the block" is that everything gets a little tighter.
func fitTo(beats []Beat, idx []int, want time.Duration, p Profile, rate float64) time.Duration {
	if len(idx) == 0 {
		return 0
	}
	wpm := p.Editorial.WordsPerMinute
	targetWords := wordsFor(want, wpm, rate)
	if targetWords <= 0 {
		return currentLen(beats, idx, p, rate)
	}

	type slot struct {
		i        int
		min, max int
		locked   bool
	}
	slots := make([]slot, 0, len(idx))
	for _, i := range idx {
		lo, hi := roleBounds(p, beats[i])
		slots = append(slots, slot{i: i, min: lo, max: hi})
	}

	for pass := 0; pass < 8; pass++ {
		cur := 0
		lockedWords := 0
		freeWords := 0
		for _, s := range slots {
			cur += beats[s.i].Words
			if s.locked {
				lockedWords += beats[s.i].Words
			} else {
				freeWords += beats[s.i].Words
			}
		}
		if cur == targetWords || freeWords == 0 {
			break
		}
		need := targetWords - lockedWords
		if need < 0 {
			need = 0
		}
		scale := float64(need) / float64(freeWords)
		moved := false
		for k := range slots {
			s := &slots[k]
			if s.locked {
				continue
			}
			w := int(float64(beats[s.i].Words)*scale + 0.5)
			if w < s.min {
				w, s.locked = s.min, true
			}
			if w > s.max {
				w, s.locked = s.max, true
			}
			if w != beats[s.i].Words {
				beats[s.i].Words = w
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	return currentLen(beats, idx, p, rate)
}

func currentLen(beats []Beat, idx []int, p Profile, rate float64) time.Duration {
	var t time.Duration
	for _, i := range idx {
		t += estimate(beats[i].Words, p.Editorial.WordsPerMinute, rate)
	}
	return t
}

// blockOverhead is the music inside one block: the seams between its own
// consecutive stories, plus any pad in front of it.
func blockOverhead(beats []Beat, idx []int, p Profile) time.Duration {
	var t time.Duration
	stories := 0
	for _, i := range idx {
		t += beats[i].PadBefore
		if beats[i].Kind == BeatStory {
			stories++
		}
	}
	if stories > 1 {
		t += time.Duration(stories-1) * p.Choreo.SeamHold.D()
	}
	return t
}

func reason(got, want time.Duration) string {
	if got < want {
		return "it came up " + fmtDur(want-got) + " short"
	}
	return "it ran " + fmtDur(got-want) + " long"
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// ---------------------------------------------------------------------------
// Pass 3: anchors
// ---------------------------------------------------------------------------

// anchor lands blocks that must start at a given offset.
//
// Running EARLY is fixable: hold music until the clock catches up, which is what
// every broadcast in history has done, and what Block.Pad asks for. Running LATE
// is not, and the plan says so rather than pretending — a running order that
// silently moves its own anchors has stopped being a schedule.
func anchor(beats []Beat, f Format, p Profile) []Note {
	var notes []Note
	for bi, blk := range f.Blocks {
		if blk.At <= 0 {
			continue
		}
		first := -1
		for i := range beats {
			if beats[i].BlockIdx == bi {
				first = i
				break
			}
		}
		if first < 0 {
			continue
		}
		at := startOf(beats, first, p)
		want := blk.At.D()
		switch {
		case at < want && blk.Pad:
			beats[first].PadBefore += want - at
		case at < want:
			notes = append(notes, Note{
				Path: "format." + blk.Name, Given: fmtDur(want), Used: fmtDur(at),
				Why: "the show reaches this block early and the block does not allow padding, so it starts early",
			})
		case at > want:
			notes = append(notes, Note{
				Path: "format." + blk.Name, Given: fmtDur(want), Used: fmtDur(at),
				Why: "everything before this block already runs past its anchor by " + fmtDur(at-want) +
					" — nothing can start earlier than the material in front of it",
			})
		}
	}
	return notes
}

// startOf is when the beat at i begins, counting everything before it.
func startOf(beats []Beat, i int, p Profile) time.Duration {
	var t time.Duration
	for k := 0; k < i; k++ {
		if beats[k].Fixed {
			t += beats[k].fixedLen
		} else {
			t += estimate(beats[k].Words, p.Editorial.WordsPerMinute, 1)
		}
	}
	return t + choreoOverhead(beats[:i], p)
}

// ---------------------------------------------------------------------------
// Calibration
// ---------------------------------------------------------------------------

// Calibration is what a voice actually reads at, measured rather than assumed.
//
// This is the other half of "accurate timings", and the half no amount of
// planning can supply: words per minute is a property of a synthesiser, a voice,
// a language and the prose it was given, and the only way to know it is to play
// a show and time it. Feed the result back into Profile.Editorial.WordsPerMinute
// and every subsequent estimate is calibrated to this instance's own narrator.
type Calibration struct {
	// WordsPerMinute is the observed rate, already corrected back out of the
	// listener's playback multiplier — so it describes the voice rather than
	// the person listening to it.
	WordsPerMinute float64
	// Samples is how many beats it was measured over. One beat is an anecdote;
	// the caller decides how many it trusts.
	Samples int
	// Spread is the largest single-beat deviation from the mean, as a
	// fraction. A small spread means the estimate can be tightened; a large one
	// means the synthesiser's pace depends on the prose and a tighter target
	// will simply miss more often.
	Spread float64
}

// Calibrate measures what a programme actually ran to.
//
// Only beats that started AND ended are counted, and breaks are skipped — a
// break has no words, so including it would drag the measured rate toward zero
// for a beat nobody wrote.
func (t Trace) Calibrate(p Program) (Calibration, bool) {
	words := map[string]int{}
	for _, b := range p.Beats {
		if b.Kind.Spoken() && b.Words > 0 {
			words[b.ID] = b.Words
		}
	}
	started := map[string]time.Duration{}
	var rates []float64
	for _, e := range t.Entries {
		if e.Event == nil {
			continue
		}
		switch e.Event.Kind {
		case EvPlaying:
			if _, seen := started[e.Event.BeatID]; !seen {
				started[e.Event.BeatID] = e.At
			}
		case EvEnded:
			st, ok := started[e.Event.BeatID]
			w := words[e.Event.BeatID]
			if !ok || w == 0 {
				continue
			}
			dur := e.At - st
			if dur <= 0 {
				continue
			}
			// Back out the listener's rate: a 1.5x listener heard the same
			// prose in two thirds of the time, so the words-per-minute they
			// observed is half again what the voice actually read at. Dividing
			// is what turns an observation about a session into a fact about a
			// narrator, which is the only kind worth storing.
			rates = append(rates, float64(w)/dur.Minutes()/p.Rate)
		}
	}
	if len(rates) == 0 {
		return Calibration{}, false
	}
	var sum float64
	for _, r := range rates {
		sum += r
	}
	mean := sum / float64(len(rates))
	var spread float64
	for _, r := range rates {
		if d := absF(r-mean) / mean; d > spread {
			spread = d
		}
	}
	return Calibration{WordsPerMinute: mean, Samples: len(rates), Spread: spread}, true
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// String is what a tuning session reads.
func (c Calibration) String() string {
	return "measured " + strconv.FormatFloat(c.WordsPerMinute, 'f', 1, 64) + " wpm over " +
		strconv.Itoa(c.Samples) + " beats, spread ±" +
		strconv.FormatFloat(c.Spread*100, 'f', 0, 64) + "%"
}
