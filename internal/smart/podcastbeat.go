package smart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
)

// ErrStaleRevision means a beat was commissioned against instructions this build
// no longer carries.
//
// Its own error rather than a generic failure because the remedy is specific and
// nothing else has it: the programme was planned under an older writer, and
// re-planning it is the fix. Serving the beat anyway would put prose under a
// cache key that claims a revision it was not written to — permanently, and
// invisibly, which is the exact failure the version exists to prevent.
var ErrStaleRevision = errors.New("smart: this beat was planned against a different script revision")

// The bridge between the broadcast engine and the writer.
//
// internal/fluxcast decides WHEN each beat happens, what it is for and how many
// words it may have; this file turns one of its Briefs into the Segment call
// that already exists here, and hands back what was written. Nothing about the
// prose moves: the instructions, the vibes, the handover shapes and the cache
// all stay where they are, because they are this package's job and the engine
// deliberately knows nothing about them.
//
// The one thing that crosses in the other direction is PromptVersion, which the
// caller copies into fluxcast.Profile.Script.Revision so that every beat's cache key
// changes when these instructions do. That direction matters: a copy of "v7"
// inside internal/fluxcast would be the same fact in two files, and the copy that
// drifts is the one nobody rebuilt.

// Write implements fluxcast.Writer.
//
// The mapping is deliberately total — every fluxcast.BeatKind has an answer here,
// including the one that has no answer:
//
//	OPENING   → OpenOnly, with the run-through
//	STORY     → an ordinary segment, handing over from Prev
//	TEASE     → TeaseOnly over the stories it names
//	RECAP     → RecapOnly over the stories it names
//	SIGN_OFF  → CloseOnly
//	BREAK     → an error, because a break has no words and asking for them is a
//	            caller that has lost track of what it is playing
//
// A total mapping rather than a default case: a new beat kind should fail to
// compile into a writer that has not been taught what it is, rather than
// silently coming out as an ordinary story segment.
func (p *Podcast) Write(ctx context.Context, b fluxcast.Brief) (fluxcast.Draft, error) {
	if p == nil {
		return fluxcast.Draft{}, ErrNothingToSummarise
	}
	if b.Revision != "" && b.Revision != PromptVersion {
		// A commission written against instructions this build no longer has.
		// Refused rather than served: the beat's cache key claims a revision,
		// and answering with prose from a different one would put text under a
		// key that does not describe it — permanently, and invisibly.
		return fluxcast.Draft{}, ErrStaleRevision
	}

	seg := Segment{
		ItemID:     b.Subject.ItemID,
		Source:     b.Subject.Source,
		Title:      b.Subject.Title,
		Body:       b.Subject.Body,
		PrevID:     b.Prev.ItemID,
		PrevSource: b.Prev.Source,
		PrevTitle:  b.Prev.Title,
		Vibe:       b.Vibe,
	}

	switch b.Kind {
	case fluxcast.BeatOpening:
		seg.OpenOnly = true
		seg.Open = &Opening{
			PartOfDay: b.PartOfDay,
			Date:      b.Date,
			Stories:   b.Stories,
			Lineup:    headlines(b.Lineup),
		}
	case fluxcast.BeatStory:
		if b.Opening() {
			// The greeting rides on this segment: the format did not split it
			// into a recording of its own.
			seg.Open = &Opening{
				PartOfDay: b.PartOfDay,
				Date:      b.Date,
				Stories:   b.Stories,
				Lineup:    headlines(b.Lineup),
			}
		} else if b.Prev.ItemID == "" {
			// No predecessor and no greeting to write means the programme has
			// already opened in a recording of its own. Said explicitly,
			// because a segment left to infer it greets the listener a second
			// time — the date twice in ninety seconds.
			seg.Opened = true
		}
	case fluxcast.BeatTease:
		seg.TeaseOnly = true
		seg.Names = headlines(b.Lineup)
	case fluxcast.BeatRecap:
		seg.RecapOnly = true
		seg.Names = headlines(b.Lineup)
	case fluxcast.BeatSignOff:
		seg.CloseOnly = true
		seg.Open = &Opening{Stories: b.Stories}
	case fluxcast.BeatBreak:
		return fluxcast.Draft{}, ErrNothingToSummarise
	default:
		return fluxcast.Draft{}, ErrNothingToSummarise
	}

	text, err := p.Segment(ctx, seg)
	if err != nil {
		return fluxcast.Draft{}, err
	}
	return fluxcast.Draft{
		Text:  text,
		Words: fluxcast.CountWords(text),
		Model: p.model(ctx),
	}, nil
}

func headlines(in []fluxcast.Headline) []Headline {
	if len(in) == 0 {
		return nil
	}
	out := make([]Headline, 0, len(in))
	for _, h := range in {
		out = append(out, Headline{Source: h.Source, Title: h.Title})
	}
	if len(out) > MaxLineup {
		// The engine's format may ask for more than the writer will read out.
		// Clamped here rather than argued about: the ceiling is about what a
		// listener can hold in their head, which is this package's judgement.
		out = out[:MaxLineup]
	}
	return out
}

// namesKey identifies the set of stories a tease or a recap names, for the
// cache. Order is part of it: "the vote, then the study" and "the study, then
// the vote" are different scripts, because a run-through builds.
func namesKey(names []Headline) string {
	if len(names) == 0 {
		return "none"
	}
	h := sha256.New()
	for _, n := range names {
		_, _ = h.Write([]byte(n.Source + "\x00" + n.Title + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// writeNames is the input a tease or a recap gets: the headlines, and which way
// they point. Nothing else — no bodies, no story to cover.
func writeNames(in *strings.Builder, seg Segment) {
	if seg.RecapOnly {
		in.WriteString("RECAP — the listener has ALREADY HEARD these stories in this programme.\n")
	} else {
		in.WriteString("COMING UP — the listener has NOT heard these stories yet. They are still to come in this programme.\n")
	}
	if seg.PrevTitle != "" || seg.PrevSource != "" {
		in.WriteString("\nThe story that has just finished:\n")
		if s := strings.TrimSpace(seg.PrevSource); s != "" {
			in.WriteString("  Publication: " + s + "\n")
		}
		if t := strings.TrimSpace(seg.PrevTitle); t != "" {
			in.WriteString("  Headline: " + t + "\n")
		}
	}
	in.WriteString("\nThe stories to name, in this order:\n")
	for i, n := range seg.Names {
		in.WriteString("  " + strconv.Itoa(i+1) + ". ")
		if s := strings.TrimSpace(n.Source); s != "" {
			in.WriteString(s + " — ")
		}
		in.WriteString(strings.TrimSpace(n.Title) + "\n")
	}
}

// podcastTeaseInstructions writes "coming up" and stops.
//
// Its own prompt for the reason the opening and the sign-off have theirs: almost
// every rule about covering a story is wrong here. The failure it exists to
// prevent is narrower and worse than a model wandering into the article, though
// — a tease is one sentence away from being a summary, and a tease that
// summarises has SPENT the listener's interest rather than buying it. Somebody
// who has just been told what happened has no reason to stay for the story.
func podcastTeaseInstructions(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing A LOOK AHEAD and nothing else.

` + vibes[VibeFor(vibe)] + `

You will be given a few headlines that are STILL TO COME later in this programme, and the story that has just finished. You will not cover any of them.

Write about ` + strconv.Itoa(teaseWords) + ` words of continuous spoken prose:

1. TURN from what just finished, in a few words. Not a summary of it — the listener heard it.

2. NAME what is coming, giving each one the thing that makes it worth staying for: a verb, a stake, a tension. "There's a resignation later that nobody saw coming." "And a set of numbers that look impressive until you ask what they're measured against." Two or three, ending on the strongest.

3. STOP. No handoff into a story, because the next thing is not a story — the programme carries on around you.

The whole job is to make somebody stay. That means saying enough to be interesting and less than enough to be satisfying, which is the difference between a look ahead and a summary.

NEVER:

- Never explain, resolve or summarise any of these stories. If a listener could skip the story after hearing you, you have written it wrong.
- Never say when in the programme something is coming, or give it a position — "in a moment", "later", "after this" are fine; "third" and "in ten minutes" are claims about a running order you cannot see.
- Never invent a fact, a number or an attribution. Everything must be supportable from the headline itself.
- ` + noIdentityRule + `
- Never greet the listener or sign off. You are in the middle of a programme.

` + noMarkupRule + `

Output the spoken text and nothing else.`
}

// podcastRecapInstructions writes "what you missed" and stops.
//
// The mirror of a tease and NOT the same prompt with the tense changed. One is
// selling and one is catching somebody up: a recap written in a tease's voice
// tells a listener to look forward to something they have already heard, which
// is the single most disorienting thing this beat can do.
func podcastRecapInstructions(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing A RECAP and nothing else.

` + vibes[VibeFor(vibe)] + `

You will be given the headlines of stories ALREADY COVERED in this programme. Somebody listening now may have joined part-way through and missed them.

Write about ` + strconv.Itoa(recapWords) + ` words of continuous spoken prose:

1. SAY what this is, in a few words — that this is what has been covered so far. Once, plainly.

2. GIVE each story its outcome in one clause: what happened, not what is interesting about it. This is the opposite of a look ahead. A listener who missed the story should be able to carry on without it, and one who heard it should feel reminded rather than sold to.

3. STOP, and hand back into the programme in a few words.

NEVER:

- Never tease. These stories are done; there is nothing to stay for. "Coming up", "still to come" and "you won't want to miss" are all wrong here.
- Never add a fact that was not in the headline you were given, and never invent a number, a name or an attribution.
- Never apologise for repeating yourself, and never explain that you are recapping because somebody may have joined late. Say the thing; the reason is obvious.
- ` + noIdentityRule + `
- Never greet the listener or sign off. You are in the middle of a programme.

` + noMarkupRule + `

Output the spoken text and nothing else.`
}

const (
	// teaseWords and recapWords are what the INSTRUCTIONS ask for. The engine's
	// format also has a word budget for the beat, and the two are different
	// things: the engine's is what a fitted programme has room for and this is
	// what the prose should aim at. Where they disagree the engine's wins,
	// because it is the one holding a clock — see Write, which passes it.
	teaseWords = 70
	recapWords = 90
)
