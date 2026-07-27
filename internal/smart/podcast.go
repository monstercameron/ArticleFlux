// Podcast turns a QUEUE of articles into a broadcast.
//
// This is the third and last thing listening needs (§10.7, §19). The Smart+
// voice made an article audible; the digest made a long one bearable; neither
// made a SESSION of them listenable. Play six digests back to back and what you
// hear is six essays with a hard cut between each — no handover, no sense of an
// order, and no signal at the seam that one thing ended and another began. Every
// podcast and every news bulletin ever made solves that the same way, with one
// or two sentences of connective tissue, and it is the difference between a
// playlist and a programme.
//
// So a segment written here is not a summary of an article. It is **that
// article's slot in a running broadcast**: it knows what was just said, hands
// off from it in a clause, and then tells the story. Nothing else changes — the
// same voice reads it, the same cache pays for it once.
//
// # What it costs and how often
//
// Once per ORDERED PAIR of articles, ever. That is the one real difference from
// the digest's cache: the same story following a different story is a different
// segment, because the handover names what came before. A reader who plays the
// same feed twice in the same order pays nothing the second time; a reader who
// shuffles pays again for the seams. That is the honest shape of the cost, and
// it is bounded by the fact that a feed is normally played in feed order.
//
// # What it deliberately does not do
//
// It does not invent a show, a host, a name, a station, a jingle, or a
// "welcome back". Those are the first things a model reaches for when told the
// word "podcast", they are all fabrications about a product that does not exist,
// and a listener who hears "you're listening to the Morning Wire" from their own
// RSS reader has been lied to by their own software.

package smart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// podcastPromptVersion is part of the cache key, for the reason promptVersion is:
// an edit to the instructions that nothing can invalidate would apply only to
// pairs nobody has listened to yet, and the two halves of the library would
// differ permanently and invisibly.
//
// v2: the narrator got a persona, permission to say what a story MEANS rather
// than only what it said, and an opening that greets the listener and dates the
// broadcast. v1's segments were accurate and inert — a competent transcription
// of a headline and a body, which is exactly what a listener does not need,
// because they can already read.
// v3: shorter, sharper, and spoken rather than read. v2's segments were correct
// and slightly wet — they inherited the publisher's setup paragraph, restated
// the headline, and read the opening headlines out as a list, which a listener
// hears as a list of nouns going past. This one opens on the fact, cuts the
// scene-setting, and gives the run-through momentum.
const podcastPromptVersion = "v3"

// The vibes: how the narrator sounds.
//
// Four, and each is a MANNER rather than a character. That distinction is the
// whole design of this feature and it is worth stating plainly: a manner is a
// way of saying true things, and a character is a person who does not exist.
// "Calm" changes sentence length and what gets emphasised; "this is Sarah with
// your morning update" would be a fabricated human being introduced by software
// the listener owns. The prompt gives the narrator a personality and explicitly
// withholds an identity.
const (
	VibeCalm  = "calm"
	VibeBrisk = "brisk"
	VibeDry   = "dry"
	VibeWarm  = "warm"
)

// DefaultVibe is what an unconfigured reader gets.
//
// Calm, because this is news being read to someone who did not ask to be sold
// anything, and because it is the one that ages best over an hour — energy is
// enjoyable for two stories and exhausting for twenty.
const DefaultVibe = VibeCalm

// vibes are the persona paragraphs, keyed by the stored value.
//
// Each says how to SOUND and — more usefully — what to do differently with the
// same facts, because a persona expressed only as adjectives ("be warm!")
// produces a model that says warm things about the weather rather than one that
// writes shorter sentences.
var vibes = map[string]string{
	VibeCalm: `Your manner is calm and measured: the tone of someone who has read everything and is not in a hurry to prove it.
Sentences are unhurried and complete. You explain rather than announce. Where a story is being over-sold elsewhere, you are the voice that says plainly how big it actually is.`,

	VibeBrisk: `Your manner is brisk and energetic: this is the morning bulletin and the listener has somewhere to be.
Sentences are short. You lead with the fact, not the wind-up. You move between stories quickly and never linger on a detail that will not matter by lunchtime — but you do not clip words or sound rushed, because a listener who has to concentrate to keep up has stopped listening.`,

	VibeDry: `Your manner is dry and understated: an eyebrow rather than a joke.
You state remarkable things flatly and let them be remarkable on their own. Where a claim deserves scepticism you convey it by what you choose to put next to it, not by editorialising loudly. Never sneer, and never be funny at the expense of being clear — the wit is in the framing, and it is occasional.`,

	VibeWarm: `Your manner is warm and conversational: a well-read friend telling you what happened, not a bulletin being read at you.
You use plain everyday words, address the listener directly when it helps ("you might remember"), and are willing to say when something is genuinely good news or genuinely worrying. You are never chummy, never use filler, and never pad.`,
}

// VibeFor resolves a stored preference, falling back to the default.
//
// An unknown value lands on the default rather than being passed through to the
// prompt. That matters more than it looks: the vibe is interpolated into the
// instructions, so an unvalidated string from a preference row would be a way to
// write arbitrary text into the system prompt of a model spending the reader's
// money.
func VibeFor(pref string) string {
	pref = strings.ToLower(strings.TrimSpace(pref))
	if _, ok := vibes[pref]; ok {
		return pref
	}
	return DefaultVibe
}

// Opening is the top of a broadcast: the greeting, the date, and how much there
// is to get through.
//
// Present only on the FIRST segment of a session, and nil on every other, which
// is what makes the opening a property of the broadcast rather than of the
// article that happens to come first.
//
// PartOfDay and Date are computed from the LISTENER'S clock, not the server's
// (see internal/app/speech.go): a self-hosted reader on a VPS three timezones
// away would otherwise be wished good morning at ten at night, which is the
// single most obviously wrong thing this feature could say.
type Opening struct {
	// PartOfDay is "morning", "afternoon" or "evening".
	PartOfDay string
	// Date is already formatted for a person: "Monday, 27 July 2026". The model
	// phrases it; this decides which day it is.
	Date string
	// Stories is how many are queued behind this one, or 0 when unknown. It is
	// the difference between "here's what's happening" and "eleven stories this
	// morning, starting with", and the second is a better thing to hear because
	// it tells the listener whether to settle in.
	Stories int
	// Lineup is the first few stories, for the run-through a bulletin opens with
	// — "the headlines" in the literal sense. Empty is fine and common: a
	// greeting followed straight by the first story is still a broadcast.
	//
	// Bounded at MaxLineup. The point of a run-through is that a listener can
	// hold it in their head; a bulletin that recites eleven headlines before
	// covering any of them has told you nothing you can keep.
	Lineup []Headline
}

// Headline is one story in the opening run-through: who ran it, and what it
// said.
//
// Source AND title, because a run-through of bare headlines is a list of claims
// with no way to weigh them — and naming the publication is most of how anyone
// decides how much attention to give a story they are hearing in one clause.
type Headline struct {
	Source string
	Title  string
}

// MaxLineup is how many headlines the opening may run through.
//
// Five, and the ceiling is about MEMORY rather than tokens: a listener can hold
// about that many one-clause items before the earlier ones start falling out,
// and a bulletin that recites eleven headlines before covering any of them has
// told you nothing you can keep. It is also the point past which the greeting
// stops being an opening and becomes the programme.
const MaxLineup = 5

// podcastWords is the length the instructions ask for.
//
// Cut from 210 to 140 in v3, and the cut IS the feature. Length was buying
// padding rather than substance: given room, a model fills it with the same
// things a publisher fills a page with — the scene-setting opener, the history
// nobody asked for, the restatement of the headline, the closing paragraph that
// says what the piece just said. None of that survives being heard once, and all
// of it costs the listener attention they were spending on the facts.
//
// About fifty seconds at a natural pace, and closer to forty at the 1.2x the
// player now defaults to. Short enough that the writing has to choose.
const podcastWords = 140

// podcastMaxTokens covers the model's thinking as well as its answer, like
// digestMaxTokens. Truncation is worse here than there: a segment that stops
// mid-sentence stops the BROADCAST mid-sentence, and the next thing the listener
// hears is a cheerful handover from a story that never finished.
const podcastMaxTokens = 4200

// Segment is one slot in the broadcast: the story to tell, and the one just
// told.
//
// The previous story is carried as its SOURCE AND HEADLINE rather than its text.
// That is the whole trick of making this cheap: a handover needs to know what
// was just talked about, not to have read it — so the input stays one article
// long however many stories deep the session gets, and a two-hour broadcast
// costs exactly what a two-hour queue of digests costs.
type Segment struct {
	ItemID string
	Source string
	Title  string
	Body   string

	// PrevID, PrevSource and PrevTitle describe the story that just finished.
	// All empty means this is the top of the broadcast, which is a genuinely
	// different piece of writing and not merely one missing a sentence.
	PrevID     string
	PrevSource string
	PrevTitle  string

	// Vibe is how the narrator sounds. Empty resolves to DefaultVibe.
	Vibe string
	// Open is the top-of-broadcast greeting, on the first segment only.
	Open *Opening
	// OpenOnly writes the greeting and the headline run-through AND NOTHING ELSE
	// — no handover, no story.
	//
	// It exists for a reason that is entirely about sound rather than about text:
	// the broadcast opens over music, and the music has to swell and clear before
	// the first story starts. That transition can only be timed if the opening is
	// its own recording with its own end — inside the first segment it is a
	// moment the client cannot see coming.
	//
	// Body is still given (it is the story the run-through ends on) but is not
	// covered. Requires Open.
	OpenOnly bool
}

// Podcast writes broadcast segments.
type Podcast struct {
	llm      llmClient
	settings *store.SettingsRepo
	dir      string
}

// NewPodcast wires the writer. dir may be empty, in which case nothing is cached
// and every listen rewrites — correct for a test, expensive for a server.
//
// client is the llmClient seam (see llmclient.go): production keeps passing a
// *llm.Client, tests pass a fake.
func NewPodcast(client llmClient, settings *store.SettingsRepo, dir string) *Podcast {
	return &Podcast{llm: client, settings: settings, dir: dir}
}

// Configured reports whether segments can be written at all.
func (p *Podcast) Configured(ctx context.Context) bool {
	return p != nil && p.llm != nil && p.llm.Configured(ctx)
}

// podcastInstructions is the whole feature.
//
// Every negative clause below is there because a model asked for "a podcast
// segment" produces it unprompted, and every one of them is audible:
//
//   - a show name and a host name, both invented;
//   - "welcome back", to a listener who has not been anywhere;
//   - "coming up next", followed by a story the model has never seen — this one
//     is the most damaging, because it is a confident statement about the future
//     that is wrong roughly always;
//   - a sign-off at the end of every segment, so a forty-minute session ends
//     forty times;
//   - stage directions and sound cues, which the synthesiser reads aloud.
//
// The handover is specified tightly for the same reason the digest's rules are:
// the failure mode is not that the model refuses, it is that it writes a
// paragraph of throat-clearing before each story and the broadcast becomes half
// transitions. One or two sentences, and they must carry MEANING — a real
// relation between the two stories where there is one, and a plain change of
// subject where there is not. "Turning now to something completely different" is
// the sound of a transition that had nothing to say.
// podcastInstructionsFor builds the instructions for one vibe.
//
// A function rather than a constant because the persona is interpolated, and the
// persona is the half of this that was missing: v1 produced accurate, inert
// segments — a competent transcription of a headline and a body — which is
// exactly what a listener does not need, since they can already read.
//
// The vibe is looked up rather than pasted, so an unknown preference cannot
// write arbitrary text into a system prompt. See VibeFor.
// podcastIntroWords is the budget for an opening on its own.
//
// Short, and shorter than it first looks: about twenty words of greeting and
// three or four headline beats. An opening is the part a listener has heard
// before — they are waiting for the news, not for the preamble — and the one
// failure mode that makes people turn a broadcast off in the first minute is an
// introduction that will not end.
const podcastIntroWords = 90

// podcastIntroInstructions writes the top of the broadcast and stops.
//
// Its own prompt rather than a flag inside the segment one, because almost every
// rule in that prompt is about covering a story and none of them apply here. A
// prompt that says "do all this, except not the parts about the article" is a
// prompt a model half-follows.
func podcastIntroInstructions(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing THE OPENING ONLY.

` + vibes[VibeFor(vibe)] + `

You will be given the part of the day, the date, how many stories are queued, and the headlines you are about to run through. You will NOT cover any of them. Something else follows you.

Write about ` + strconv.Itoa(podcastIntroWords) + ` words of continuous spoken prose:

1. GREET the listener for the part of the day and say the date — for example "Good morning. It's Monday the twenty-seventh of July" or "Good evening — eleven stories tonight." Vary the wording; do not use the same construction every time. Two sentences at most.

2. RUN THROUGH what is coming. This is the part most worth getting right, because a listener hears a plain list of titles as a string of nouns going past and retains none of it.

   So do not read a list. Give each one a beat of COLOUR: the thing about it that makes it worth staying for, in your own words, with a verb in it. "The government has finally backed down on X — after a year of saying it wouldn't. There's a study out that looks damning until you read the sample size. And the thing everyone said was vapourware has actually shipped." Three or four like that, building, ending on the one the broadcast starts with.

   You may lean on them lightly — a raised eyebrow at a claim, a note that something is overdue or surprising. That judgement is what makes a run-through worth hearing rather than worth skipping. Do not number them, do not name the publication for each one, and do not explain any of them: you are saying why they matter, not what happened.

   Never read a title verbatim if it was written for the eye. A headline with a colon in it, a bracketed aside, a site name stuck on the end or a number in front of it is not a sentence anybody says out loud — say the thing it is about instead.

3. END on a handoff into the first story — a few words, not a sentence of explanation. "That one first." "We start there." Then stop.

NEVER:

- Never cover a story. Not one sentence of one. You are the trailer, not the programme.
- Never invent a programme name, a station, a host, or a name for yourself. You have a manner, not an identity. Do not say "I'm" anyone.
- Never invent a fact, a number or an attribution. Everything you say about a headline must be supportable from the headline itself.
- Never sign off or thank the listener.

Plain flowing sentences only. NO bullet points, NO numbered lists, NO headings, NO markdown, NO stage directions, NO sound cues, NO speaker labels. Every character you emit is read aloud by a speech synthesiser, so an asterisk or a bracket becomes a noise.

Output the spoken text and nothing else.`
}

func podcastInstructionsFor(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing ONE segment of it.

` + vibes[VibeFor(vibe)] + `

You will be given the story to cover, and — unless this is the opening segment — the source and headline of the story you have just finished covering. If an OPENING is given, this is the top of the broadcast.

Write about ` + strconv.Itoa(podcastWords) + ` words of continuous spoken prose (plus the opening, if there is one), in this order:

1. THE OPENING, only if one was given. Greet the listener for the part of the day and say the date — for example "Good morning. It's Monday the twenty-seventh of July" or "Good evening — eleven stories tonight." Vary the wording; do not use the same construction every time.

   Then, if HEADLINES were given, run through what is coming — and this is the part most worth getting right, because a listener hears a plain list of titles as a string of nouns going past and retains none of it.

   So do not read a list. Give each one a beat of COLOUR: the thing about it that makes it worth staying for, in your own words, with a verb in it. "The government has finally backed down on X — after a year of saying it wouldn't. There's a study out that looks damning until you read the sample size. And the thing everyone said was vapourware has actually shipped." Three or four like that, building, ending on the one you are about to cover.

   You may lean on them lightly — a raised eyebrow at a claim, a note that something is overdue or surprising. That judgement is what makes a run-through worth hearing rather than worth skipping. Do not number them, do not name the publication for each one, and do not explain any of them yet: you are saying why they matter, not what happened.

   Never read a title verbatim if it was written for the eye. A headline with a colon in it, a bracketed aside, a site name stuck on the end or a number in front of it is not a sentence anybody says out loud — say the thing it is about instead.
2. THE HANDOVER, only if a previous story was given: one or two sentences carrying the listener from it into this one. If the two are genuinely related — same subject, same industry, same country, cause and effect, agreement or contradiction — say what the relation IS. That connection is the most valuable sentence in the segment. If they are unrelated, make a plain, unhurried change of subject and do not pretend to a link. Never use a stock phrase like "in other news" or "turning now to" as a substitute for saying what is changing.
3. THE STORY, told for a listener rather than transcribed for a reader.

WHAT "TOLD FOR A LISTENER" MEANS. This is the whole job, so it is spelled out:

- **Open on the fact.** Not on context, not on the field it belongs to, not on why the topic is interesting. The first sentence is the thing that happened.
- **Cut the fat the publisher put there.** Articles are padded for a page: the scene-setting opener, the paragraph of history nobody asked for, the restatement of the headline, the "why this matters" section that mostly matters to advertisers, the closing summary of what was just said. None of it survives being spoken. If a sentence would still be true of a different story, it is padding — leave it out.
- **Say what it MEANS, not only what it says.** A listener can already read; what they cannot do is skim, re-read a line, or check a chart. Give them the finding, then why it matters, then how much weight to put on it.
- **You may editorialise about SIGNIFICANCE.** You may say a result is surprising, a claim is thin, a number is smaller than the headline suggests, a company has said this before, or that this mostly matters if you use the thing in question. That judgement is why anyone would listen to a person instead of a feed reader.
- **You may NOT invent.** No facts, numbers, quotes, dates, names or attributions that are not in the text you were given. If you could not point at the sentence that supports it, do not say it. Never attribute your own judgement to the publication.
- **Write for the mouth, not the page.** Contractions. Short sentences. One idea in each — a sentence a listener has to hold in their head to the end is a sentence they have lost. Vary the length so it has a rhythm; three of the same length in a row is a metronome.
- **Be specific rather than clever.** A concrete detail is worth three adjectives, and wordplay that needs a second to land is wordplay a listener has already missed. Where a turn of phrase is genuinely better than the plain version, use it — sparingly, and never at the cost of being understood first time.
- **Round numbers and give them scale**: "about a third", "roughly nine thousand — a small town's worth". Never read a table, a list of figures, or a version number.
- **Skip what does not survive being heard once**: percentages to two decimal places, URLs, code, long proper nouns said more than once, the names of everyone quoted.
- **Signpost when you change direction**: "the catch is", "what's new here is", "worth saying".

ALWAYS:

- Plain flowing sentences only. NO bullet points, NO numbered lists, NO headings, NO markdown, NO stage directions, NO sound cues, NO speaker labels. Every character you emit is read aloud by a speech synthesiser, so an asterisk or a bracket becomes a noise.
- Name the publication once, naturally, where you introduce the story. It is how a listener decides how much weight to give it.
- Spell out anything that reads badly aloud. Write "about 40 percent", not "~40%".

NEVER:

- Never invent a programme name, a station, a host, or a name for yourself. You have a manner, not an identity. Do not say "I'm" anyone.
- Never say what is coming next by name. You have not been told, and guessing is a false statement. Saying how MANY stories are left is fine when you were told the number.
- Never sign off, thank the listener, or summarise what you just said. The broadcast continues after you; end on the last thing worth knowing.
- If the text is an error page, a paywall notice, a cookie banner or otherwise not an article, hand over from the previous story, say in one sentence that this one could not be read, and stop.

Output the spoken text and nothing else.`
}

// podcastInstructionsOf picks the prompt for what this segment IS.
//
// Two prompts rather than one with a flag in it, because almost every rule in
// the segment prompt is about covering a story and none of them apply to an
// opening. "Do all of this, except the parts about the article" is an
// instruction a model half-follows, and the half it keeps is the story.
func podcastInstructionsOf(seg Segment) string {
	if seg.OpenOnly {
		return podcastIntroInstructions(seg.Vibe)
	}
	return podcastInstructionsFor(seg.Vibe)
}

// Segment returns the spoken text for one slot, from cache when possible.
//
// Returns ErrNothingToSummarise for an item with no usable text, exactly like
// the digest — the caller falls back to reading the article, which is the right
// answer for a two-line link post and a poor one to spend a model call on.
func (p *Podcast) Segment(ctx context.Context, seg Segment) (string, error) {
	body := strings.TrimSpace(seg.Body)
	// An opening has nothing to summarise by design — it covers no story — so the
	// emptiness check that protects a two-line link post would reject every
	// intro. What an intro needs instead is something to introduce.
	if seg.OpenOnly {
		if seg.Open == nil || len(seg.Open.Lineup) == 0 {
			return "", ErrNothingToSummarise
		}
		body = ""
	} else if body == "" {
		return "", ErrNothingToSummarise
	}
	// Cache before key, like Digest.Speakable and for the same reason: this text
	// is already paid for and on disk, and withholding it because the key was
	// rotated would punish a reader for a configuration change.
	model := p.model(ctx)
	path := p.cachePath(seg, model)
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	if !p.Configured(ctx) {
		return "", llm.ErrNotConfigured
	}

	if len(body) > maxInputChars && !seg.OpenOnly {
		// On a word boundary, so the model's last piece of evidence is not half
		// a word.
		if cut := strings.LastIndexByte(body[:maxInputChars], ' '); cut > maxInputChars/2 {
			body = body[:cut]
		} else {
			body = body[:maxInputChars]
		}
	}

	out, err := p.llm.Do(ctx, llm.Request{
		Model:        model,
		Instructions: podcastInstructionsOf(seg),
		Input:        podcastInput(seg, body),
		// Bounded because the budget covers reasoning too, and a truncated
		// segment ends mid-sentence — far more obvious spoken than read, and
		// worse still when the next segment hands over from it.
		MaxOutputTokens: podcastMaxTokens,
		// Low, like the digest. Finding the relation between two headlines is a
		// small act of reading rather than a reasoning problem, and deliberation
		// here buys tokens rather than a better link.
		Effort: "low",
	})
	if err != nil {
		return "", err
	}
	out = cleanForSpeech(out)
	if out == "" {
		return "", ErrNothingToSummarise
	}

	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		// Temp-then-rename, like the digest and the audio cache: a reader who
		// reloads mid-write must not find half a segment.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), 0o644); err == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return out, nil
}

// podcastInput assembles what the model is shown.
//
// Labelled fields rather than prose, and the previous story FIRST, because that
// is the order the segment is written in — the handover is the opening, so the
// thing being handed over from should be the first thing read. When there is no
// previous story the label is absent rather than empty: a model shown
// "Previously: " with nothing after it will write a handover from nothing.
//
// Split out and exported to the package's tests as a pure function, because the
// shape of this string is the contract with the instructions above and it is the
// part that silently rots — an instruction referring to a label that is no
// longer emitted still reads perfectly.
func podcastInput(seg Segment, body string) string {
	var in strings.Builder
	// The opening first, because it is the first thing said. It is given as
	// FACTS — a part of the day, a date, a count — rather than as a sentence to
	// read out, so the wording varies between broadcasts instead of the same
	// greeting arriving every morning like a recording.
	if o := seg.Open; o != nil {
		in.WriteString("OPENING — this is the top of the broadcast.\n")
		if s := strings.TrimSpace(o.PartOfDay); s != "" {
			in.WriteString("  Part of day: ")
			in.WriteString(s)
			in.WriteByte('\n')
		}
		if s := strings.TrimSpace(o.Date); s != "" {
			in.WriteString("  Date: ")
			in.WriteString(s)
			in.WriteByte('\n')
		}
		if o.Stories > 0 {
			in.WriteString("  Stories queued, including this one: ")
			in.WriteString(strconv.Itoa(o.Stories))
			in.WriteByte('\n')
		}
		// The run-through. Numbered in the INPUT so the order is unambiguous, and
		// explicitly not numbered in the output — the instructions say so, because
		// a model given a numbered list will read "one, two, three" aloud.
		if len(o.Lineup) > 0 {
			// The trailing clause differs by mode because it is a claim about
			// what follows in this input, and in intro mode nothing does.
			where := "the first is the story covered below"
			if seg.OpenOnly {
				where = "the first is the story the broadcast starts with"
			}
			in.WriteString("  HEADLINES to run through, in this order (" +
				where + "):\n")
			for i, h := range o.Lineup {
				in.WriteString("    ")
				in.WriteString(strconv.Itoa(i + 1))
				in.WriteString(". ")
				if s := strings.TrimSpace(h.Source); s != "" {
					in.WriteString(s)
					in.WriteString(" — ")
				}
				in.WriteString(strings.TrimSpace(h.Title))
				in.WriteByte('\n')
			}
		}
		in.WriteByte('\n')
	}
	// An opening on its own stops here. Nothing below this line is anything but
	// a story to cover or a story to hand over from, and both are exactly what
	// this mode must not produce — a model shown an article and told not to
	// discuss it will discuss it.
	if seg.OpenOnly {
		return in.String()
	}
	prevSource := strings.TrimSpace(seg.PrevSource)
	prevTitle := strings.TrimSpace(seg.PrevTitle)
	if prevTitle != "" || prevSource != "" {
		in.WriteString("The story you have just finished covering:\n")
		if prevSource != "" {
			in.WriteString("  Publication: ")
			in.WriteString(prevSource)
			in.WriteByte('\n')
		}
		if prevTitle != "" {
			in.WriteString("  Headline: ")
			in.WriteString(prevTitle)
			in.WriteByte('\n')
		}
		in.WriteByte('\n')
	} else {
		// Said explicitly rather than left to be inferred from an absence. A
		// model given only one story will write a handover from an imagined one
		// about as often as not, and "we were just discussing" a story that never
		// aired is the single most damaging thing this feature can produce.
		in.WriteString("This is the OPENING segment of the broadcast. " +
			"There is no previous story.\n\n")
	}
	in.WriteString("The story to cover now:\n")
	if s := strings.TrimSpace(seg.Source); s != "" {
		in.WriteString("  Publication: ")
		in.WriteString(s)
		in.WriteByte('\n')
	}
	if t := strings.TrimSpace(seg.Title); t != "" {
		in.WriteString("  Headline: ")
		in.WriteString(t)
		in.WriteByte('\n')
	}
	in.WriteByte('\n')
	in.WriteString(body)
	return in.String()
}

// model is the instance's Smart+ model, or the built-in default.
func (p *Podcast) model(ctx context.Context) string {
	if p.settings == nil {
		return llm.DefaultModel
	}
	m, err := p.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil || strings.TrimSpace(m) == "" {
		return llm.DefaultModel
	}
	return strings.TrimSpace(m)
}

// cachePath hashes the PAIR, the model and the prompt version together.
//
// The pair, and that is the one thing to get right here: caching on the item
// alone would serve the segment written for a different predecessor, so the
// broadcast would hand over from a story that was not just told. That failure is
// both convincing and completely wrong, which is the worst combination — it does
// not sound like a bug, it sounds like the narrator misremembering.
func (p *Podcast) cachePath(seg Segment, model string) string {
	if p.dir == "" || seg.ItemID == "" {
		return ""
	}
	// The vibe and the opening are part of the key because they are part of the
	// TEXT. A reader who switches from calm to brisk has asked for a different
	// recording of the same story, and serving the old one would look exactly
	// like the setting not working.
	//
	// The opening's date is in here too, which gives the behaviour a listener
	// expects without any extra machinery: the same broadcast restarted an hour
	// later opens identically and costs nothing, and tomorrow's opens fresh
	// because it is a different day.
	open := ""
	if o := seg.Open; o != nil {
		open = o.PartOfDay + "|" + o.Date + "|" + strconv.Itoa(o.Stories)
		// The run-through is part of the opening's text, so it is part of its
		// identity. Two broadcasts of the same first story with different stories
		// behind it open differently, and serving one for the other would read
		// out headlines that are not coming.
		for _, h := range o.Lineup {
			open += "|" + h.Source + "\x01" + h.Title
		}
	}
	// The opening alone and the opening plus a story are two different scripts
	// written from the same inputs, so the mode has to be in the key. Without it
	// the intro would be served as the first segment or the other way round — a
	// broadcast that either never gets to the news or never introduces itself,
	// depending on which was asked for first.
	mode := "seg"
	if seg.OpenOnly {
		mode = "intro"
	}
	sum := sha256.Sum256([]byte(seg.ItemID + "\x00" + seg.PrevID + "\x00" +
		model + "\x00" + podcastPromptVersion + "\x00" + VibeFor(seg.Vibe) +
		"\x00" + open + "\x00" + mode))
	name := hex.EncodeToString(sum[:]) + ".txt"
	// One level of fan-out, matching the digest and audio caches, so a long
	// listener does not end up with one directory holding tens of thousands of
	// entries.
	return filepath.Join(p.dir, name[:2], name)
}
