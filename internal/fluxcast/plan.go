package fluxcast

import (
	"strconv"
	"strings"
	"time"
)

// Plan turns a pool of stories and a format into a programme.
//
// Pure: no clock, no network, and no randomness that is not seeded. The same
// Input gives the same Program byte for byte, which is what makes a cue sheet
// diffable and a variation replayable.
//
// It runs in four passes, and they are separate because each can fail in a way
// the others cannot explain:
//
//  1. FILL — which stories go in which block, under the block's filter and its
//     appetite. A block can come out short; that is a feed problem.
//  2. FIT — word budgets adjusted so each block, and then the show, lands on
//     its target inside per-role bounds. A block can come out unfittable; that
//     is a format problem, and it is reported as one.
//  3. ANCHOR — blocks that must start at an offset get padded to it, or the
//     plan says why it could not.
//  4. KEY — beat ids and script cache keys, computed last, because everything
//     above can still change the words.
//
// The notes it returns are the whole diagnostic surface: nothing here fails
// silently, and nothing here refuses. A programme that missed its target by
// forty seconds is still a programme, and a caller that cannot be told why is
// the reason the old one was tuned by listening to it.
func Plan(in Input) (Program, []Note) {
	prof, notes := in.Profile.Validate()
	format := in.Format
	if len(format.Blocks) == 0 {
		format = FeedFormat()
	}
	format, fnotes := format.Validate()
	notes = append(notes, fnotes...)

	rate := in.Rate
	if rate <= 0 {
		rate = 1
	}

	prog := Program{
		ID:      in.ID,
		Title:   in.Title,
		Profile: prof,
		Format:  format,
		Variant: in.Variant,
		Rate:    rate,
	}
	if prog.Variant.Vibe == "" {
		prog.Variant.Vibe = prof.Script.Vibe
	}
	if len(in.Stories) == 0 {
		// A programme with nothing in it gets no opening and no goodbye. A
		// greeting followed immediately by a sign-off is worse than silence: it
		// is the application performing a broadcast it does not have.
		return prog, notes
	}

	beats, unused, fillNotes := fill(format, in.Stories, prof, prog.Variant, rate)
	notes = append(notes, fillNotes...)
	prog.Unused = unused

	notes = append(notes, fitBlocks(beats, format, prof, rate)...)
	notes = append(notes, fitShow(beats, format, prof, rate)...)
	notes = append(notes, anchor(beats, format, prof)...)

	// Ids and keys last: everything above could still change a word budget, and
	// a key computed before the fitter would address a script nobody wrote.
	for i := range beats {
		beats[i].Ordinal = i
		beats[i].ID = beatID(in.ID, beats[i])
		beats[i].Est = estimate(beats[i].Words, prof.Editorial.WordsPerMinute, rate)
		if beats[i].Fixed {
			// A break's length is scheduled, not written.
			beats[i].Est = beats[i].fixedLen
		}
	}
	for i := range beats {
		beats[i].Key = beatKey(prog, beats[i])
	}
	prog.Beats = beats
	for _, b := range beats {
		if b.Kind == BeatStory {
			prog.Order = append(prog.Order, b.ItemID)
		}
	}
	return prog, notes
}

// ---------------------------------------------------------------------------
// Pass 1: fill
// ---------------------------------------------------------------------------

// fill walks the format and hands stories to blocks.
//
// Order matters and is the format's, not the pool's: a block takes the highest
// stories its filter admits from what is LEFT, so a lead block declared first
// gets the lead, and a general block declared after it gets the rest. That is
// the behaviour a running order implies and the one a reader would predict.
func fill(f Format, pool []Story, prof Profile, v Variant, rate float64) ([]Beat, []string, []Note) {
	var notes []Note
	taken := make([]bool, len(pool))
	var beats []Beat
	rnd := newSeq(v.Seed)

	// blockStories is filled as we go so a TEASE can look ahead and a RECAP can
	// look back — both need to name stories by id, and both are planned in the
	// same pass as the stories they name.
	blockStories := make([][]int, len(f.Blocks))

	// Look-ahead for teases has to happen after the fill, so the first pass
	// assigns stories and the second writes the interstitials. Teases are
	// therefore planted as placeholders here and resolved below.
	type pending struct{ beatIdx, blockIdx int }
	var teases, recaps []pending

	prevItem := ""
	for bi, blk := range f.Blocks {
		switch blk.Kind {
		case BlockGreeting:
			if !prof.Script.Greeting {
				continue
			}
			w := blk.Words
			if w == 0 {
				w = greetingWords(blk.Lineup)
			}
			beats = append(beats, Beat{
				Kind: BeatOpening, Words: w, Base: w, Handover: -1,
				Block: blk.Name, BlockIdx: bi,
			})

		case BlockStories:
			picked := pick(pool, taken, blk, prof, rate)
			if len(picked) == 0 {
				notes = append(notes, Note{
					Path: "format." + blk.Name, Given: strconv.Itoa(blk.Take), Used: "0",
					Why: "no story in the pool matched this block's filter, so the block is empty",
				})
				continue
			}
			for _, idx := range picked {
				s := pool[idx]
				taken[idx] = true
				blockStories[bi] = append(blockStories[bi], idx)
				words := s.Words
				if words <= 0 {
					words = RoleWords(prof, s.Role)
				}
				h := -1
				if prevItem != "" && prof.Script.HandoverVariety > 0 {
					h = int(rnd.next() % uint64(prof.Script.HandoverVariety))
				}
				beats = append(beats, Beat{
					Kind: BeatStory, ItemID: s.ItemID, PrevItemID: prevItem,
					Role: s.Role, Theme: s.Theme, Words: words, Base: words, Handover: h,
					Block: blk.Name, BlockIdx: bi,
				})
				prevItem = s.ItemID
			}

		case BlockTease:
			w := blk.Words
			if w == 0 {
				w = greetingWords(blk.Lineup) - 20
			}
			beats = append(beats, Beat{
				Kind: BeatTease, Words: w, Base: w, Handover: -1, PrevItemID: prevItem,
				Block: blk.Name, BlockIdx: bi,
			})
			teases = append(teases, pending{len(beats) - 1, bi})

		case BlockRecap:
			w := blk.Words
			if w == 0 {
				w = greetingWords(blk.Lineup) - 10
			}
			beats = append(beats, Beat{
				Kind: BeatRecap, Words: w, Base: w, Handover: -1, PrevItemID: prevItem,
				Block: blk.Name, BlockIdx: bi,
			})
			recaps = append(recaps, pending{len(beats) - 1, bi})

		case BlockBreak:
			beats = append(beats, Beat{
				Kind: BeatBreak, Handover: -1, Fixed: true, fixedLen: blk.Target.D(),
				Block: blk.Name, BlockIdx: bi,
			})

		case BlockSignOff:
			if !prof.Script.SignOff {
				continue
			}
			w := blk.Words
			if w == 0 {
				w = 40
			}
			beats = append(beats, Beat{
				Kind: BeatSignOff, PrevItemID: prevItem, Words: w, Base: w, Handover: -1,
				Block: blk.Name, BlockIdx: bi,
			})
		}
	}

	// The greeting is ABOUT the first story: it introduces it, and the display
	// needs something on it while the greeting plays. Resolved after the fill
	// because until then nobody knows which story is first.
	firstStory := ""
	for _, b := range beats {
		if b.Kind == BeatStory {
			firstStory = b.ItemID
			break
		}
	}
	lastStory := ""
	for i := len(beats) - 1; i >= 0; i-- {
		if beats[i].Kind == BeatStory {
			lastStory = beats[i].ItemID
			break
		}
	}
	for i := range beats {
		switch beats[i].Kind {
		case BeatOpening:
			beats[i].ItemID = firstStory
			if blk := f.Blocks[beats[i].BlockIdx]; blk.Lineup > 0 && prof.Script.Lineup {
				beats[i].Lineup = firstN(beats, BeatStory, blk.Lineup, pool)
			}
		case BeatSignOff:
			beats[i].ItemID = lastStory
		}
	}

	// Teases name what is coming; recaps name what is done. Both are why a
	// programme is planned whole rather than streamed.
	for _, p := range teases {
		blk := f.Blocks[p.blockIdx]
		look := blk.Look
		if look <= 0 {
			look = 1
		}
		target := p.blockIdx + look
		var ids []string
		for bi := target; bi < len(f.Blocks) && len(ids) < maxOr(blk.Lineup, 2); bi++ {
			for _, idx := range blockStories[bi] {
				ids = append(ids, pool[idx].ItemID)
				if len(ids) >= maxOr(blk.Lineup, 2) {
					break
				}
			}
		}
		if len(ids) == 0 {
			notes = append(notes, Note{
				Path: "format." + blk.Name, Given: "tease", Used: "dropped",
				Why: "nothing follows this tease for it to name",
			})
			beats[p.beatIdx].Kind = ""
			continue
		}
		beats[p.beatIdx].Lineup = ids
		beats[p.beatIdx].ItemID = ids[0]
	}
	for _, p := range recaps {
		blk := f.Blocks[p.blockIdx]
		var ids []string
		for bi := 0; bi < p.blockIdx; bi++ {
			for _, idx := range blockStories[bi] {
				ids = append(ids, pool[idx].ItemID)
			}
		}
		if len(ids) == 0 {
			notes = append(notes, Note{
				Path: "format." + blk.Name, Given: "recap", Used: "dropped",
				Why: "nothing has been covered yet for this recap to name",
			})
			beats[p.beatIdx].Kind = ""
			continue
		}
		// The most recent few, because a recap is "what you missed", not an
		// index of the whole programme.
		if n := maxOr(blk.Lineup, 3); len(ids) > n {
			ids = ids[len(ids)-n:]
		}
		beats[p.beatIdx].Lineup = ids
		beats[p.beatIdx].ItemID = ids[len(ids)-1]
	}

	// Drop the interstitials that had nothing to say.
	live := beats[:0]
	for _, b := range beats {
		if b.Kind != "" {
			live = append(live, b)
		}
	}
	beats = live

	var unused []string
	for i, t := range taken {
		if !t {
			unused = append(unused, pool[i].ItemID)
		}
	}
	return beats, unused, notes
}

// pick chooses a block's stories.
//
// Three appetites, and the difference between them is what makes a fixed-length
// show possible at all:
//
//   - Take > 0 — exactly that many, in pool order.
//   - Take == 0 with a Target — as many as fit the target at their natural
//     budgets, admitting the one that crosses it so a block is never short by
//     the width of one story.
//   - Take == 0 with no target — everything the filter admits, which is the
//     feed-shaped answer.
func pick(pool []Story, taken []bool, blk Block, prof Profile, rate float64) []int {
	var out []int
	budget := blk.wants()
	var used time.Duration
	for i, s := range pool {
		if taken[i] || !blk.Filter.admits(s) {
			continue
		}
		if blk.Take > 0 && len(out) >= blk.Take {
			break
		}
		if blk.Take == 0 && budget > 0 {
			w := s.Words
			if w <= 0 {
				w = RoleWords(prof, s.Role)
			}
			est := estimate(w, prof.Editorial.WordsPerMinute, rate)
			if used > 0 && used+est > budget+est/2 {
				// Adding this one overshoots by more than half of itself.
				// Stopping here and letting the fitter stretch what is already
				// in beats a block that is audibly padded.
				break
			}
			used += est + prof.Choreo.SeamHold.D()
		}
		out = append(out, i)
	}
	return out
}

// firstN is the first n story item ids, for a run-through.
func firstN(beats []Beat, kind BeatKind, n int, pool []Story) []string {
	title := map[string]string{}
	for _, s := range pool {
		title[s.ItemID] = s.Title
	}
	var out []string
	for _, b := range beats {
		if b.Kind != kind {
			continue
		}
		if strings.TrimSpace(title[b.ItemID]) == "" {
			// A headline with no text is a lookup spent to be told so, and a
			// run-through is a nicety that must never be why a show is late.
			continue
		}
		out = append(out, b.ItemID)
		if len(out) >= n {
			break
		}
	}
	if len(out) < 2 {
		// One headline is not a run-through, it is the story about to be told.
		return nil
	}
	return out
}

func maxOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// greetingWords sizes a greeting from what it has to carry rather than typing a
// constant: a run-through of three headlines is three times the words of one,
// and a fixed budget would either starve it or pad it.
func greetingWords(lineup int) int {
	w := 35
	if lineup > 0 {
		w += 18 * lineup
	}
	return w
}

// RoleWords is a role's budget under a profile.
//
// An unrecognised role falls back to standard rather than zero: a silent
// zero-word story would vanish from the minute budget without ever being
// dropped from the running order, which is a worse failure than one that costs
// slightly more than its role implies.
func RoleWords(p Profile, role string) int {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case "LEAD":
		return p.Editorial.WordsLead
	case "SUPPORTING":
		return p.Editorial.WordsSupporting
	case "QUICK_HIT":
		return p.Editorial.WordsQuickHit
	case "MENTION":
		return p.Editorial.WordsMention
	default:
		return p.Editorial.WordsStandard
	}
}

// estimate converts a word budget into spoken time at the listener's rate.
func estimate(words int, wpm, rate float64) time.Duration {
	if words <= 0 || wpm <= 0 {
		return 0
	}
	if rate <= 0 {
		rate = 1
	}
	secs := (float64(words) / wpm) * 60 / rate
	return time.Duration(secs * float64(time.Second))
}

// wordsFor is estimate's inverse: how many words fill a duration. The fitter's
// only lever.
func wordsFor(d time.Duration, wpm, rate float64) int {
	if d <= 0 || wpm <= 0 {
		return 0
	}
	if rate <= 0 {
		rate = 1
	}
	return int(d.Seconds() * rate * wpm / 60)
}

// beatID is stable within a programme and unique across it.
func beatID(programID string, b Beat) string {
	return programID + "/" + string(b.Kind) + "/" + strconv.Itoa(b.Ordinal) + "/" + b.ItemID
}

// beatKey identifies the SCRIPT, which is what the text and audio caches are
// keyed on.
//
// Everything that changes the words goes in, and nothing that does not: the
// beat's position does NOT, so a story that follows the same predecessor in two
// different shows is one recording, paid for once. What DOES go in is the
// predecessor, the vibe, the script revision, the word budget, the handover
// shape and — for anything that names other stories — the run-through, the part
// of day and the date, all of which visibly change what is said.
//
// The word budget being in the key is what makes fitted shows honest: a story
// written to 140 words for a feed-shaped show and to 95 for a twenty-minute
// bulletin are two different recordings, and sharing one would mean a fitted
// programme quietly playing unfitted audio.
func beatKey(p Program, b Beat) string {
	br := Brief{
		Kind:      b.Kind,
		Words:     b.Words,
		Vibe:      p.Variant.Vibe,
		Revision:  p.Profile.Script.Revision,
		Handover:  b.Handover,
		Subject:   Subject{ItemID: b.ItemID},
		Prev:      Subject{ItemID: b.PrevItemID},
		PartOfDay: p.Variant.PartOfDay,
		Date:      p.Variant.Date,
	}
	for _, id := range b.Lineup {
		br.Lineup = append(br.Lineup, Headline{ItemID: id})
	}
	return ScriptKey(br)
}

// seq is a seeded, dependency-free sequence.
//
// splitmix64: eight lines, no import, and the same numbers on every platform —
// which matters more here than statistical quality, because the only thing it
// picks is which of a handful of sentence shapes a bridge uses, and the
// property that has to hold is that the same seed picks the same ones on a
// phone, in a test, and on the server that cached the audio.
type seq struct{ state uint64 }

func newSeq(seed uint64) *seq { return &seq{state: seed + 0x9E3779B97F4A7C15} }

func (s *seq) next() uint64 {
	s.state += 0x9E3779B97F4A7C15
	z := s.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}
