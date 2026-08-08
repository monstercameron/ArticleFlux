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
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"
)

// PromptVersion is part of the cache key, for the reason promptVersion is:
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
// v4: added the segment-GROUP writer (TODO 11.6) — new instruction text
// (podcastGroupInstructionsFor) that did not exist in v3 at all, so the same
// argument applies: a v3-cached single segment and a v4 group segment must not
// be mistaken for interchangeable text sharing one version number.
// v5: three changes that are all about the same complaint — the narrator sounded
// like software. (a) The handover gets a rotating SHAPE (handoverShapes), because
// every segment is an independent call with no memory of the last one, so
// identical instructions converged on one construction and a listener heard the
// same bridge sentence all session. (b) The prose brief allows colour and a
// figure of speech, where v4 read as a style guide for a wire service. (c) The
// sign-off exists at all (podcastOutroInstructions), which is new text under this
// version like the group writer was under v4.
// v6: four changes, all from product direction rather than a listening complaint.
// (a) A fourth PartOfDay, "night" — see partOfDay in internal/app/speech.go and
// the new guidance here about not saying "good night" as a greeting. (b) The
// editorialising bullet asks for visibly more energy when a story warrants it,
// not just permission to note significance flatly. (c) handoverShapes is a wider
// pool and handoverShapeFor is now GENUINELY RANDOM rather than a hash of the
// pair — see that function's own comment for the cache trade-off this accepts.
// (d) The run-through's headline count went from "three or four" to "two or
// three", matching MaxLineup's cut from five to three.
// v7: two new beats that did not exist in v6 at all — the TEASE ("coming up")
// and the RECAP ("what you missed"), each with its own instruction text, added
// so internal/fluxcast's formats can schedule them. The same argument the group
// writer made under v4 applies: text written under v6 and text written under v7
// must not be mistaken for interchangeable prose sharing one version number.
//
// Exported because it is the WRITER's identity and internal/fluxcast carries it into
// every beat's cache key (fluxcast.Profile.Script.Revision). That package
// deliberately does not know what the current revision is — a hardcoded copy
// there would be the same fact in two files, and the copy that drifts is the one
// nobody rebuilt.
const PromptVersion = "v7"

// handoverShapes are the STRUCTURES a segment can use to get from the last story
// to this one — not phrases.
//
// The distinction is the entire point. A list of approved sentences would
// produce a narrator with five catchphrases instead of one, which is the same
// failure at a lower frequency. These describe what the sentence has to DO, and
// leave the words to the model, so two segments that draw the same shape still
// read differently because the stories under them differ.
//
// The rotation is what makes it work, and it is why this is a slice rather than
// a paragraph telling the model to "vary the transitions". A model asked to vary
// something inside one call has nothing to vary against: it cannot see the
// segment before it, and it will produce its single most likely bridge every
// time, confidently and identically. Handing each call a DIFFERENT constraint is
// the only version of this that survives contact with a stateless writer.
//
// Ten now rather than six — per product direction that transitions were still
// noticeable across a long session, and doubling the pool halves how often any
// one shape's flavour repeats without changing what any single transition
// sounds like.
var handoverShapes = []string{
	"Name the RELATION between the two stories in plain words — same industry, same country, cause and effect, one contradicting the other, one explaining the other. Say what the connection is rather than gesturing at one.",
	"Make a CLEAN BREAK. No bridge at all: finish the last thought and open the new story on its own first fact. A hard cut is a legitimate transition and it is the one that sounds least like software.",
	"Carry ONE THREAD across — an idea, a word, a number, a company, a question the last story left open — and let it be the first thing the new story answers or complicates.",
	"Use CONTRAST. Set the new story against the last one: bigger, smaller, the opposite conclusion, the same promise from a different direction. Say the contrast, do not merely imply it.",
	"CHANGE PACE instead of changing subject with a phrase. A short sentence that lands the last story, then straight into the new fact. The rhythm does the work the connective would have done.",
	"Step back for HALF A SENTENCE — what the two have in common about the week, the field, or the way these things tend to go — then into the new story. At most one clause of it; this is a breath, not an essay.",
	"Ask a QUESTION the last story leaves you with, then answer it by moving to the new one — 'so what happens to the people who bet the other way' is a real bridge if the new story is the answer.",
	"Use SCALE. Put a number or a size from the last story next to one from this one — bigger, smaller, faster, further along — and let the comparison itself be the transition.",
	"NAME THE MOOD SHIFT. If the two stories are tonally opposite — grim to light, urgent to slow — say that shift plainly in a few words rather than pretending the programme's mood did not just change.",
	"Borrow a WORD OR PHRASE from the end of the last story and open the new one on it, redefined by the new context — the same trick a good essay uses between paragraphs.",
}

// handoverShapeFor picks the shape for one segment, genuinely at random.
//
// v5 chose by hashing the pair, deterministically, so a segment rewritten after
// a cache miss would stay the same segment — the reasoning was that randomness
// here would make the same story after the same story sound different on
// Tuesday, "indistinguishable from the cache being broken". Per product
// direction that traded too much variety for a property few listeners would
// ever notice: what is CACHED is the finished TEXT, keyed on the pair
// (cachePath), not the shape choice itself — a listener who has already heard
// a pair always hears the same recording on replay, because that recording is
// what the cache serves. The only case where this trade-off is visible at all
// is a regeneration after the cache is invalidated (a prompt-version bump),
// where the SAME pair could now pick a different, equally valid shape than it
// did before — which is an acceptable cost for transitions that no longer
// settle into a predictable pattern across a long broadcast.
func handoverShapeFor(prevID, itemID string) string {
	return handoverShapes[rand.IntN(len(handoverShapes))]
}

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
	// PartOfDay is "morning", "afternoon", "evening" or "night".
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

// stories is Stories, nil-safe.
//
// The sign-off is the one caller that reads an Opening it may not have been
// given: a broadcast can be closed whether or not anyone was greeted at the top
// of it — the listener may have joined mid-programme, or the opening may have
// failed — and a close that panicked on that would take the whole request with
// it for the sake of one number in one sentence.
func (o *Opening) stories() int {
	if o == nil {
		return 0
	}
	return o.Stories
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
// Three, cut from five per product direction: a run-through is a trailer, not
// the programme, and the ceiling is about MEMORY as well as pace — a listener
// can hold about that many one-clause items before the earlier ones start
// falling out, and a bulletin that recites eleven headlines before covering
// any of them has told you nothing you can keep. It is also the point past
// which the greeting stops being an opening and becomes the programme. The
// caller (client/view/slideshow.go's queueLineup) also picks the three most
// interesting candidates rather than simply the next three in the queue, so
// the cut in count is paired with a cut in how they are chosen.
const MaxLineup = 3

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
	// Opened says the broadcast has ALREADY introduced itself, in a recording of
	// its own, and this is the first story after it.
	//
	// It exists because leaving it out is not neutral. A segment with no previous
	// story has to be TOLD it has none, or a model invents a handover from a
	// story that never aired — and a model told "this is the opening segment"
	// greets the listener and says the date, whatever the instructions say about
	// only doing that when an opening was given. The listener then hears the date
	// twice in ninety seconds, which is the most obviously wrong thing a split
	// broadcast can do.
	Opened bool
	// CloseOnly writes the SIGN-OFF and nothing else: no handover, no story.
	//
	// The mirror of OpenOnly, and it exists for the same reason the opening is
	// its own recording — a programme that stops dead after its last story has
	// not ended, it has run out. Every other segment is forbidden from signing
	// off (see the NEVER list in podcastInstructionsFor), because the broadcast
	// continues after it; exactly one segment per session is allowed to, and this
	// is how it is told which.
	//
	// PrevSource and PrevTitle are the LAST story covered, so the close can land
	// on what the listener was actually just told rather than on the programme in
	// general. Body is not read: there is no story here to cover.
	CloseOnly bool

	// TeaseOnly writes "coming up" and nothing else: the stories in Names are
	// ones the listener has NOT heard yet.
	//
	// Its own mode for the reason every other mode here has one — almost every
	// rule about covering a story is wrong for it, and a prompt that says "do
	// all of this except the parts about the article" is one a model half
	// follows, keeping the article. The specific failure it guards against is
	// sharper than that, though: a tease is one sentence away from being a
	// summary, and a tease that summarises has spent the listener's interest
	// instead of buying it.
	TeaseOnly bool
	// RecapOnly writes "what you missed": the stories in Names HAVE been heard.
	// The mirror of a tease and not the same prompt with a tense changed — one
	// is selling and one is catching somebody up, and a recap that sells is
	// telling a listener to look forward to something they already heard.
	RecapOnly bool
	// Direction is what the FORMAT says about how to write this beat: who is
	// listening, what may be assumed, what to avoid, and any note for this
	// beat alone (plan §29.7).
	//
	// It is rendered into the input rather than into the instructions, and the
	// distinction is the cache: the instructions are one string per vibe,
	// shared by every beat and versioned by PromptVersion, while this varies
	// per beat and belongs with the rest of the per-beat commission.
	//
	// Empty on an instance with no format, in which case nothing is added and
	// the prompt is byte-identical to what it was before formats existed.
	Direction fluxcast.Direction
	// Names are the stories a tease or a recap names. Ignored by every other
	// mode: the opening's run-through travels in Open.Lineup, because it is
	// part of the greeting rather than a beat of its own.
	Names []Headline
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

// podcastOutroWords is the budget for the sign-off.
//
// Shorter than the opening, and deliberately the shortest thing in the
// programme. An opening has work to do — it has to make someone stay. A close
// has none: the listener has already heard everything, and every extra sentence
// after the last story is a programme reluctant to stop. Forty words is a beat
// of reflection and a goodbye.
const podcastOutroWords = 45

// noMarkupRule is the anti-markup instruction, word for word, in every prompt
// this file writes. Shared rather than retyped four times: the four writers
// are edited independently, the rule is safety-critical (every character
// reaches a speech synthesiser), and four copies are four chances for one
// edit to leave the others behind.
const noMarkupRule = `Plain flowing sentences only. NO bullet points, NO numbered lists, NO headings, NO markdown, NO stage directions, NO sound cues, NO speaker labels. Every character you emit is read aloud by a speech synthesiser, so an asterisk or a bracket becomes a noise.`

// noIdentityRule is the identity rule three of the four writers state
// identically. The outro states its own longer variant — it alone also
// forbids naming the show just finished — so it is left out of this constant
// rather than forced to share it awkwardly.
const noIdentityRule = `Never invent a programme name, a station, a host, or a name for yourself. You have a manner, not an identity. Do not say "I'm" anyone.`

// podcastOutroInstructions writes the end of the broadcast and stops.
//
// Its own prompt rather than a flag on the segment prompt, for the reason the
// opening has its own: almost every rule about covering a story is wrong here,
// and "do all of this except the parts about the article" is an instruction a
// model half-follows — keeping, of course, the article.
//
// The one rule it INVERTS is the segment prompt's "never sign off". That rule is
// right for every other segment and is exactly what has to be lifted for this
// one, which is why the permission is stated so plainly below: a model that has
// been told all session not to say goodbye will not say goodbye by implication.
func podcastOutroInstructions(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing THE SIGN-OFF ONLY.

` + vibes[VibeFor(vibe)] + `

The programme is over. Every story has been told. You will be given the source and headline of the LAST story covered, and how many stories the broadcast ran — nothing else, because there is nothing else left to say.

This is the one moment in the whole broadcast where signing off is not only allowed but is the entire job. Do it.

Write about ` + strconv.Itoa(podcastOutroWords) + ` words of continuous spoken prose:

1. LAND. One sentence that closes the programme rather than the last story — a look back across what the listener has just heard, or a plain acknowledgement that it is done. If the last story genuinely leaves something hanging, you may nod at that in a few words. Do not summarise the stories; the listener heard them.

2. SIGN OFF. A goodbye, short, in your own manner. "That's the lot." "That's everything for now — back when there's more." "Thanks for the company." One line. Vary it; do not use the same farewell every time.

Then stop, mid-air if need be. The last thing a listener should hear is you deciding to stop, not you running out.

NEVER:

- Never cover a story, quote one, or explain one. There is nothing left to cover.
- Never invent a fact, a number, a name or an attribution.
- Never invent a programme name, a station, a host, or a name for yourself. You have a manner, not an identity. Do not say "I'm" anyone, and do not name a show you have just finished presenting.
- Never promise a specific next edition, a time, or a schedule. You do not know when the listener will come back and saying so is a false statement.
- Never tell the listener to subscribe, rate, share, follow or do anything else. This is their own reader reading their own feeds to them; there is nothing to sell and nobody to sell it.

` + noMarkupRule + `

Output the spoken text and nothing else.`
}

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

   If you are told the part of the day is NIGHT, do not say "good night" — that is a farewell, not a greeting, and it tells the listener the programme is ending before it has begun. Acknowledge the hour without signing off instead: "Still up? Here's what's happened while you were." or "It's late, and here's what you missed." works; a plain "Here's what's happening" with no time-of-day phrase at all is also completely fine.

2. RUN THROUGH what is coming. This is the part most worth getting right, because a listener hears a plain list of titles as a string of nouns going past and retains none of it.

   So do not read a list. Give each one a beat of COLOUR: the thing about it that makes it worth staying for, in your own words, with a verb in it. "The government has finally backed down on X — after a year of saying it wouldn't. There's a study out that looks damning until you read the sample size." Two or three like that, building, ending on the one the broadcast starts with.

   You may lean on them lightly — a raised eyebrow at a claim, a note that something is overdue or surprising. That judgement is what makes a run-through worth hearing rather than worth skipping. Do not number them, do not name the publication for each one, and do not explain any of them: you are saying why they matter, not what happened.

   Never read a title verbatim if it was written for the eye. A headline with a colon in it, a bracketed aside, a site name stuck on the end or a number in front of it is not a sentence anybody says out loud — say the thing it is about instead.

3. END on a handoff into the first story — a few words, not a sentence of explanation. "That one first." "We start there." Then stop.

NEVER:

- Never cover a story. Not one sentence of one. You are the trailer, not the programme.
- ` + noIdentityRule + `
- Never invent a fact, a number or an attribution. Everything you say about a headline must be supportable from the headline itself.
- Never sign off or thank the listener.

` + noMarkupRule + `

Output the spoken text and nothing else.`
}

func podcastInstructionsFor(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing ONE segment of it.

` + vibes[VibeFor(vibe)] + `

You will be given the story to cover, and — unless this is the opening segment — the source and headline of the story you have just finished covering. If an OPENING is given, this is the top of the broadcast.

Write about ` + strconv.Itoa(podcastWords) + ` words of continuous spoken prose (plus the opening, if there is one), in this order:

1. THE OPENING, only if one was given. Greet the listener for the part of the day and say the date — for example "Good morning. It's Monday the twenty-seventh of July" or "Good evening — eleven stories tonight." Vary the wording; do not use the same construction every time.

   If you are told the part of the day is NIGHT, do not say "good night" — that is a farewell, not a greeting. Acknowledge the hour without signing off instead: "Still up? Here's what's happened while you were." works; a plain "Here's what's happening" with no time-of-day phrase at all is also completely fine.

   Then, if HEADLINES were given, run through what is coming — and this is the part most worth getting right, because a listener hears a plain list of titles as a string of nouns going past and retains none of it.

   So do not read a list. Give each one a beat of COLOUR: the thing about it that makes it worth staying for, in your own words, with a verb in it. "The government has finally backed down on X — after a year of saying it wouldn't. There's a study out that looks damning until you read the sample size." Two or three like that, building, ending on the one you are about to cover.

   You may lean on them lightly — a raised eyebrow at a claim, a note that something is overdue or surprising. That judgement is what makes a run-through worth hearing rather than worth skipping. Do not number them, do not name the publication for each one, and do not explain any of them yet: you are saying why they matter, not what happened.

   Never read a title verbatim if it was written for the eye. A headline with a colon in it, a bracketed aside, a site name stuck on the end or a number in front of it is not a sentence anybody says out loud — say the thing it is about instead.
2. THE HANDOVER, only if a previous story was given: one or two sentences carrying the listener from it into this one.

   You will be given a SHAPE for this handover. Use it. It is there because you cannot see the segment before this one and will otherwise write the same bridge sentence every time — which a listener hears, within about four stories, as a machine.

   If the shape genuinely does not fit these two stories, do the plainest possible change of subject instead. Never force a connection that is not there: a manufactured link is worse than no link, because the listener checks it and finds nothing. Never use a stock phrase — "in other news", "turning now to", "meanwhile", "speaking of which" — as a substitute for saying what is changing.
3. THE STORY, told for a listener rather than transcribed for a reader.

WHAT "TOLD FOR A LISTENER" MEANS. This is the whole job, so it is spelled out:

- **Open on the fact.** Not on context, not on the field it belongs to, not on why the topic is interesting. The first sentence is the thing that happened.
- **Cut the fat the publisher put there.** Articles are padded for a page: the scene-setting opener, the paragraph of history nobody asked for, the restatement of the headline, the "why this matters" section that mostly matters to advertisers, the closing summary of what was just said. None of it survives being spoken. If a sentence would still be true of a different story, it is padding — leave it out.
- **Say what it MEANS, not only what it says.** A listener can already read; what they cannot do is skim, re-read a line, or check a chart. Give them the finding, then why it matters, then how much weight to put on it.
- **You may editorialise about SIGNIFICANCE, and lean into it.** You may say a result is surprising, a claim is thin, a number is smaller than the headline suggests, a company has said this before, or that this mostly matters if you use the thing in question — and when a story actually warrants it, say so with real energy rather than a flat aside. "This is a big deal" reads as filler; "this is the part that should actually worry you" or "and here's the bit that's genuinely wild" sounds like someone who means it. Match the size of the reaction to the size of the story — not every item earns one, and reaching for excitement on a routine update is how a narrator stops being believed on the story that deserves it. That judgement, applied with real conviction where it is earned, is why anyone would listen to a person instead of a feed reader.
- **You may NOT invent.** No facts, numbers, quotes, dates, names or attributions that are not in the text you were given. If you could not point at the sentence that supports it, do not say it. Never attribute your own judgement to the publication.
- **Write for the mouth, not the page.** Contractions. Short sentences. One idea in each — a sentence a listener has to hold in their head to the end is a sentence they have lost. Vary the length so it has a rhythm; three of the same length in a row is a metronome.
- **Be specific rather than clever — but do not be beige.** A concrete detail is worth three adjectives, and wordplay that needs a second to land is wordplay a listener has already missed. Within that: you are allowed to be good company. One image, one aside, one dry observation per segment is not a lapse in discipline, it is the difference between a person and a text-to-speech engine reading a summary. A listener who could not tell your segment from an automated one has been given no reason to keep listening.
- **Have a voice, not a formula.** Do not open every segment the same way. Do not use the same connective twice in one segment. If you notice yourself reaching for a construction because it is safe, reach past it. The failure this whole brief is written against is not inaccuracy — it is a narrator who is correct, fluent, and completely interchangeable.
- **Round numbers and give them scale**: "about a third", "roughly nine thousand — a small town's worth". Never read a table, a list of figures, or a version number.
- **Skip what does not survive being heard once**: percentages to two decimal places, URLs, code, long proper nouns said more than once, the names of everyone quoted.
- **Signpost when you change direction**: "the catch is", "what's new here is", "worth saying".

ALWAYS:

- ` + noMarkupRule + `
- Name the publication once, naturally, where you introduce the story. It is how a listener decides how much weight to give it.
- Spell out anything that reads badly aloud. Write "about 40 percent", not "~40%".

NEVER:

- ` + noIdentityRule + `
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
	switch {
	case seg.CloseOnly:
		// Before OpenOnly, though the two are never both set: a request that
		// somehow carried both is a client that has lost track of where it is,
		// and ending the programme is the safer of the two things to do wrong.
		return podcastOutroInstructions(seg.Vibe)
	case seg.OpenOnly:
		return podcastIntroInstructions(seg.Vibe)
	case seg.TeaseOnly:
		return podcastTeaseInstructions(seg.Vibe)
	case seg.RecapOnly:
		return podcastRecapInstructions(seg.Vibe)
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
	switch {
	case seg.CloseOnly:
		// A sign-off needs even less: it can close a programme it knows nothing
		// about except that it is over. There is deliberately NO precondition
		// here — no last headline required, no story count — because every one
		// of those would be a reason to end a broadcast in silence, and silence
		// is the exact failure this whole feature exists to remove.
		body = ""
	case seg.OpenOnly:
		if seg.Open == nil || len(seg.Open.Lineup) == 0 {
			return "", ErrNothingToSummarise
		}
		body = ""
	case body == "":
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

	// Rebuilt on `Summarizing` (plan P3.9). A segment is spoken prose, not a
	// structured answer, so there is no schema — the whole brief is in
	// `podcastInstructionsOf(seg)` and it goes in through Steer.
	//
	// **The cache key and the prompt version are unchanged, deliberately.**
	// Segments are cached forever, keyed by prompt version; any change to the
	// prose would re-bill every reader's back catalogue. So the instructions are
	// passed through byte for byte rather than reworded to suit the operation.
	out, err := schemaflux.Summarizing(podcastInput(seg, body)).
		Steer(podcastInstructionsOf(seg)).
		MaxLength(podcastMaxTokens).
		Fast().
		Context(p.llm.OpsContext(ctx)).
		Run()
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

// writeOpening appends the OPENING block — part of day, date, story count and
// the headline run-through — that both a single segment (podcastInput) and a
// segment GROUP (segmentGroupInput) need stated identically.
//
// Factored out rather than duplicated because the greeting is a property of
// the BROADCAST, not of which call happens to be writing the next words: if
// the two call sites drifted, a group's opening and a single segment's
// opening would silently stop sounding like the same programme.
//
// It is given as FACTS — a part of the day, a date, a count — rather than as
// a sentence to read out, so the wording varies between broadcasts instead of
// the same greeting arriving every morning like a recording. `where` is the
// trailing clause on the headline list, because what "the first story" refers
// to differs by caller (an intro-only recording vs. the segment or group it
// leads into).
// writeClosing states the little the sign-off is allowed to know.
//
// Deliberately thin: the last headline, so the close can land on what the
// listener was actually just told, and the story count, because "that's the
// eleven" is a real thing a presenter says and is the only number available
// that is true. No body, no other headlines, no dates — every one of those is
// something a model would be tempted to cover, and there is nothing left to
// cover.
func writeClosing(in *strings.Builder, seg Segment) {
	in.WriteString("SIGN-OFF — the broadcast is over and every story has been told.\n")
	if n := seg.Open.stories(); n > 0 {
		in.WriteString("  Stories in this broadcast: ")
		in.WriteString(strconv.Itoa(n))
		in.WriteByte('\n')
	}
	source := strings.TrimSpace(seg.PrevSource)
	title := strings.TrimSpace(seg.PrevTitle)
	if source != "" || title != "" {
		in.WriteString("  The last story you covered:\n")
		if source != "" {
			in.WriteString("    Publication: ")
			in.WriteString(source)
			in.WriteByte('\n')
		}
		if title != "" {
			in.WriteString("    Headline: ")
			in.WriteString(title)
			in.WriteByte('\n')
		}
	}
	in.WriteString("\nClose the programme and say goodbye. Nothing else.\n")
}

func writeOpening(in *strings.Builder, o *Opening, where string) {
	if o == nil {
		return
	}
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
		in.WriteString("  HEADLINES to run through, in this order (" + where + "):\n")
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
	// The format's direction goes FIRST, before the story and before the
	// handover, for the reason every other "read this before the article"
	// block here goes first: a model shown an article and then told how to
	// treat it has already decided how to treat it.
	writeDirection(&in, seg.Direction)
	// The sign-off takes nothing but what it is closing over, and takes it
	// FIRST: everything below this line is a story to cover or to hand over
	// from, and a model shown an article while being told the programme is over
	// will cover it anyway. Same argument OpenOnly makes a few lines down.
	if seg.CloseOnly {
		writeClosing(&in, seg)
		return in.String()
	}
	// A tease and a recap take the headlines they name and nothing else, and
	// they take them FIRST for the same reason the sign-off does: everything
	// below is a story to cover, and a model shown an article writes about it
	// whatever it was told.
	if seg.TeaseOnly || seg.RecapOnly {
		writeNames(&in, seg)
		return in.String()
	}
	// The opening first, because it is the first thing said. See writeOpening
	// for why it is given as FACTS rather than as a sentence to read out.
	where := "the first is the story covered below"
	if seg.OpenOnly {
		where = "the first is the story the broadcast starts with"
	}
	writeOpening(&in, seg.Open, where)
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
		// The shape for THIS handover. Emitted only where there is something to
		// hand over from, because it is the one instruction in the prompt that is
		// meaningless at the top of a broadcast — and an instruction with nothing
		// to apply to is an instruction a model applies anyway.
		in.WriteString("SHAPE FOR THE HANDOVER — use this one:\n  ")
		in.WriteString(handoverShapeFor(seg.PrevID, seg.ItemID))
		in.WriteString("\n\n")
	} else {
		// Said explicitly rather than left to be inferred from an absence. A
		// model given only one story will write a handover from an imagined one
		// about as often as not, and "we were just discussing" a story that never
		// aired is the single most damaging thing this feature can produce.
		if seg.Opened {
			// And this is the OTHER thing it must not do. The broadcast has
			// already been introduced — in a separate recording, seconds ago,
			// over music — so a second greeting here is the listener being told
			// the date twice.
			in.WriteString("The broadcast has ALREADY OPENED: the listener has " +
				"just heard the greeting, the date and the run-through of what " +
				"is coming, in a recording of its own. This is the first story.\n" +
				"Do NOT greet the listener. Do NOT say the date or the part of " +
				"the day. Do NOT say how many stories there are. Do NOT hand " +
				"over from anything — nothing has been covered yet. Begin on " +
				"the story itself.\n\n")
		} else {
			in.WriteString("This is the OPENING segment of the broadcast. " +
				"There is no previous story.\n\n")
		}
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

// openCacheKey turns an Opening into the fragment cachePath and groupCachePath
// both fold into their hash — shared because the two are the same rule
// (the opening is part of the TEXT, so it must be part of the key) applied at
// two call sites, and a fragment this load-bearing must not be free to drift
// between them.
func openCacheKey(o *Opening) string {
	if o == nil {
		return ""
	}
	var key strings.Builder
	key.WriteString(o.PartOfDay)
	key.WriteByte('|')
	key.WriteString(o.Date)
	key.WriteByte('|')
	key.WriteString(strconv.Itoa(o.Stories))
	// The run-through is part of the opening's text, so it is part of its
	// identity. Two broadcasts of the same first story with different stories
	// behind it open differently, and serving one for the other would read out
	// headlines that are not coming.
	for _, h := range o.Lineup {
		key.WriteByte('|')
		key.WriteString(h.Source)
		key.WriteByte('\x01')
		key.WriteString(h.Title)
	}
	return key.String()
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
	open := openCacheKey(seg.Open)
	// The opening alone and the opening plus a story are two different scripts
	// written from the same inputs, so the mode has to be in the key. Without it
	// the intro would be served as the first segment or the other way round — a
	// broadcast that either never gets to the news or never introduces itself,
	// depending on which was asked for first.
	mode := "seg"
	switch {
	case seg.CloseOnly:
		// FIRST, so a sign-off can never be served as the segment that covers
		// the same story. The close is keyed on the last item covered, which is
		// an id that already has a segment of its own under it.
		mode = "outro"
	case seg.OpenOnly:
		mode = "intro"
	case seg.TeaseOnly:
		// The named stories are in the key, not just the mode: two teases at
		// different points in a programme name different things and are
		// different scripts, and they share an item id whenever the tease is
		// keyed on the story it lands into.
		mode = "tease:" + namesKey(seg.Names)
	case seg.RecapOnly:
		mode = "recap:" + namesKey(seg.Names)
	case seg.Opened:
		// A first story that greets and one that does not are different scripts
		// from the same inputs, so they cannot share an entry — the same rule the
		// opening itself follows, and for the same reason.
		mode = "opened"
	}
	// The format's direction is part of the key because it is part of the TEXT.
	// Editing a house style and being served back every segment written under
	// the old one is indistinguishable from the format never having been read.
	//
	// It is APPENDED ONLY WHEN THERE IS ONE, and that is not a micro-
	// optimisation. An unconditional separator changes the key for every reader
	// who has no format at all — which invalidated the whole library the moment
	// this field was added, so every segment somebody had already paid for got
	// rewritten and re-billed to say the same thing. The prompt already had this
	// rule (an empty direction leaves it byte-identical); the key did not, and
	// nothing tested it. It does now.
	dir := ""
	if !seg.Direction.Empty() {
		dir = "\x00" + seg.Direction.Summary()
	}
	sum := sha256.Sum256([]byte(seg.ItemID + "\x00" + seg.PrevID + "\x00" +
		model + "\x00" + PromptVersion + "\x00" + VibeFor(seg.Vibe) +
		"\x00" + open + "\x00" + mode + dir))
	name := hex.EncodeToString(sum[:]) + ".txt"
	// One level of fan-out, matching the digest and audio caches, so a long
	// listener does not end up with one directory holding tens of thousands of
	// entries.
	return filepath.Join(p.dir, name[:2], name)
}

// ---------------------------------------------------------------------------
// Segment GROUPS — write per segment, synthesise per story (TODO 11.6)
// ---------------------------------------------------------------------------
//
// Segment above writes one story next to the one immediately before it. A
// GROUP widens that to 2-4 stories the rundown decided belong together — a
// cluster running as one beat rather than as separate handovers — written in
// ONE model call so the prose actually flows across them, the way a real
// bulletin's "and while we're on the subject of X" segment does and four
// independent Segment calls sharing a topic never quite manage.
//
// It is returned split back into one block per story because the audio unit
// downstream is sealed at the ITEM, and that is not a preference this call
// gets to have an opinion about: `/speech` is reached with an AES-GCM sealed
// per-item ticket — `speech\n<tenant>\n<user>\n<role>\n<itemID>\n<exp>`, minted
// by `GetItem` — and one item is one audio file is one slide. The slideshow's
// progress bar reads that file's own playhead, and read-state fires on that
// file's `ended` event. A single file covering three stories has no ticket to
// be sealed under, no slide boundary, and no `ended` event to attach
// read-state to for the second or third story in it. So the model writes the
// whole segment — that is what makes it flow — and this method hands the
// caller back one block per story, each of which is cached and synthesised
// exactly as a lone Segment's output is today. Do not collapse this back into
// one returned string; see TODO 11's "constraint that decides the
// architecture" for why that would break four things at once.

// MinSegmentStories and MaxSegmentStories bound one WriteSegment call.
//
// Two, because a "segment" of one story is just Segment and already has a
// cheaper path that does not need a schema round-trip. Four, because the
// transition the model writes into story N has to relate it to a story
// several paragraphs back in the SAME reply, and a model asked to hold more
// than a handful of articles in mind at once starts writing connective tissue
// rather than remembering what it already said — at which point four
// independent Segment calls would have been both cheaper and no worse.
const (
	MinSegmentStories = 2
	MaxSegmentStories = 4
)

// podcastGroupMaxTokens is sized as a multiple of podcastMaxTokens rather than
// guessed fresh: the per-story budget a single segment needs still applies,
// there are just up to MaxSegmentStories of them held in one reply, plus the
// model's own reasoning over all of them at once.
const podcastGroupMaxTokens = podcastMaxTokens * MaxSegmentStories

// SegmentStory is one story's input to a group: the same four fields Segment
// itself carries, pulled into their own type because a group is a slice of
// these rather than one of them.
type SegmentStory struct {
	ItemID string
	Source string
	Title  string
	Body   string
}

// SegmentGroup is one call to WriteSegment.
type SegmentGroup struct {
	// Stories is 2 to 4 entries, in the order they will be told. The order is
	// the caller's decision — the rundown's — and not this package's: the
	// transition out of story 1 into story 2 is written for THIS order and
	// no other, so reordering the slice after the fact would leave a
	// handover pointing at the wrong neighbour.
	Stories []SegmentStory

	// PrevTheme is what the segment immediately before this one was ABOUT —
	// "the row over the transit budget", "the earnings season" — not its
	// text and not its stories. A group is a step up from one story to
	// another, so the handover into it is a step from one THEME to another;
	// the model is never shown the previous segment's stories, so it cannot
	// restate them even by accident. Empty means this group opens the
	// broadcast, exactly as an empty PrevSource/PrevTitle does for Segment.
	PrevTheme string

	// Vibe is how the narrator sounds. Empty resolves to DefaultVibe.
	Vibe string
	// Open is the top-of-broadcast greeting, exactly as on Segment: present
	// only when this group is first in the session, and folded into the
	// FIRST block only — every other block in the group is never told it
	// exists, the same way every segment after the first never is.
	Open *Opening
	// Opened mirrors Segment.Opened: the broadcast has already introduced
	// itself in a recording of its own, and this group's first block must
	// not greet a second time.
	Opened bool
}

// SegmentBlock is one story's slice of a written group: the text to
// synthesise under that story's own item ticket, unchanged from how a lone
// Segment's output is synthesised today.
type SegmentBlock struct {
	ItemID string
	Text   string
}

// podcastGroupSchema forces one block per story rather than a single prose
// blob the caller would then have to guess how to split.
//
// That guess is exactly the thing this call exists to avoid: paragraph breaks
// in free text do not reliably land on story boundaries, and a split
// recovered after the fact would occasionally cut a sentence in half across
// two audio files. The schema makes the split a GUARANTEE from the provider
// instead — the same reasoning `rerankSchema` and `entitySchema` (interest.go)
// are built on.
// podcastGroupReply is the shape a grouped write comes back in; the schema is
// derived from it.
type podcastGroupReply struct {
	Blocks []podcastBlock `json:"blocks"`
}

// podcastBlock is one story's spoken block.
//
// `story` is a one-based ordinal into the STORY list this request carried,
// exactly as a rerank's `id` is an ordinal into its candidates — never anything
// the model invents on its own. The caller re-validates it either way.
type podcastBlock struct {
	Story *int   `json:"story"`
	Text  string `json:"text"`
}

var podcastGroupSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"blocks"},
	"properties": map[string]any{
		"blocks": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"story", "text"},
				"properties": map[string]any{
					// story is a one-based ordinal into the STORY list this request
					// carried, exactly as rerankSchema's `id` is an ordinal into its
					// candidates — never anything the model invents on its own.
					"story": map[string]any{"type": "integer"},
					"text":  map[string]any{"type": "string"},
				},
			},
		},
	},
}

// podcastGroupInstructionsFor builds the instructions for writing a whole
// segment of 2-4 stories in one call, in the given vibe.
//
// A sibling of podcastInstructionsFor rather than a parameterised version of
// it: almost every rule below restates a rule that prompt already states, but
// the SHAPE of the job — one call, several stories, the transition belongs to
// exactly one of them, no restating a fact once it has been said — is
// different enough that a single prompt branching on story count would be a
// prompt a model half-follows, the same argument podcastIntroInstructions
// already makes for staying its own function.
func podcastGroupInstructionsFor(vibe string) string {
	return `You are the narrating voice of a continuous news broadcast, writing ONE SEGMENT that covers SEVERAL related stories together.

` + vibes[VibeFor(vibe)] + `

You will be given 2 to 4 stories, labelled STORY 1 through STORY N in the order you must tell them, and — unless this is the opening segment — either an OPENING to fold in, or the theme of the segment you have just finished, or a note that the broadcast has already opened elsewhere. Return one block of spoken prose per story, in that same order, and nothing else.

THE SHAPE OF THIS SEGMENT

1. THE OPENING, only if one was given — exactly as a single segment would carry it: greet the listener for the part of day, say the date, and if headlines were given, run through them with a beat of colour each rather than reading a list. If the part of day is NIGHT, do not say "good night" — that is a farewell; acknowledge the hour without signing off instead. This belongs ENTIRELY to block 1. Blocks 2 and onward must not repeat any of it — no greeting, no date, no story count, ever, from the second block on.

2. THE TRANSITION INTO THE SEGMENT, at the very top of BLOCK 1 and nowhere else. If a previous segment's theme was given, hand over from it in one or two sentences — say what changes ("from the transit budget to something with no politics in it at all") if the two are actually related, or make a plain unhurried change of subject if they are not. Never a stock phrase like "in other news" or "turning now to" standing in for an actual thought. If there is no previous theme and no opening, block 1 simply starts on its story.

3. EACH STORY, told for a listener exactly as a single segment would tell it — open on the fact, cut the publisher's padding, say what it means as well as what it says, and never invent a fact, number, quote or attribution that is not in the text given for THAT story.

4. THE HANDOVERS BETWEEN STORIES, at the top of every block after the first. One or two sentences carrying the listener from the story just told into the next one: say the real relation if there is one — that is usually the reason these stories were grouped together — or make a plain change of subject if there is not. Never restate a fact from an earlier block to set up the next one; the listener just heard it thirty seconds ago and does not need it again.

WHAT "TOLD FOR A LISTENER" MEANS, for every block:

- Open on the fact, not on context, the field it belongs to, or why the topic is interesting.
- Cut the fat the publisher put there: the scene-setting opener, the restated headline, the closing summary of what was just said. None of it survives being spoken.
- Say what it MEANS, not only what it says. You may editorialise about SIGNIFICANCE, and lean into it when a story warrants it — that a result is surprising, a claim is thin, a number is smaller than the headline suggests, a company has said this before. Say it with real conviction where it is earned rather than a flat aside; match the size of the reaction to the size of the story.
- You may NOT invent. No facts, numbers, quotes, dates, names or attributions that are not in the text you were given for that specific story. If you could not point at the sentence that supports it, do not say it. Never attribute your own judgement to the publication.
- Write for the mouth, not the page: contractions, short sentences, one idea in each, a rhythm that varies rather than three sentences of the same length in a row.
- Round numbers and give them scale: "about a third", not a precise percentage. Skip anything that does not survive being heard once — exact decimals, URLs, code, a long proper noun repeated.
- Name the publication once per story, naturally, where you introduce it.

ALWAYS:

- ` + noMarkupRule + `
- Return exactly one block per STORY given, in the same order, and only that: no block for a story you were not given, no single block merging two stories, no extra commentary block at the end.

NEVER:

- ` + noIdentityRule + `
- Never say what is coming after this segment. You have not been told, and guessing is a false statement.
- Never sign off, thank the listener, or summarise the segment. The broadcast continues after you.
- If a story's own text is an error page, a paywall notice, a cookie banner or otherwise not an article, hand over from whatever precedes it in the group, say in one sentence that this one could not be read, and stop that block there — it does not cost the other blocks in the segment.

Output nothing but the requested blocks.`
}

// segmentGroupInput assembles what the model is shown for one WriteSegment
// call: the same shapes podcastInput uses, laid out for N stories instead of
// one, so the input's contract with the instructions above is exactly as
// explicit and exactly as prone to rotting silently — see podcastInput's own
// comment for why this is a named, tested function rather than inlined.
func segmentGroupInput(g SegmentGroup, bodies []string) string {
	var in strings.Builder
	writeOpening(&in, g.Open, "the first is the first story of this segment")

	prevTheme := strings.TrimSpace(g.PrevTheme)
	switch {
	case prevTheme != "":
		in.WriteString("The segment you have just finished was about: ")
		in.WriteString(prevTheme)
		in.WriteString("\n\n")
	case g.Opened:
		// The same statement Segment makes when Opened is true and there is no
		// previous story: the absence of a previous theme is not neutral, and a
		// model left to infer it invents one about as often as not.
		in.WriteString("The broadcast has ALREADY OPENED: the listener has just heard " +
			"the greeting, the date and the run-through of what is coming, in a " +
			"recording of its own. This is the first segment.\n" +
			"Do NOT greet the listener. Do NOT say the date or the part of the day. " +
			"Do NOT say how many stories there are. Do NOT hand over from anything " +
			"— nothing has been covered yet. Begin block 1 on its own story.\n\n")
	default:
		in.WriteString("This is the OPENING segment of the broadcast. There is no " +
			"previous segment.\n\n")
	}

	for i, s := range g.Stories {
		in.WriteString("STORY ")
		in.WriteString(strconv.Itoa(i + 1))
		in.WriteString(" of ")
		in.WriteString(strconv.Itoa(len(g.Stories)))
		in.WriteString(":\n")
		if src := strings.TrimSpace(s.Source); src != "" {
			in.WriteString("  Publication: ")
			in.WriteString(src)
			in.WriteByte('\n')
		}
		if t := strings.TrimSpace(s.Title); t != "" {
			in.WriteString("  Headline: ")
			in.WriteString(t)
			in.WriteByte('\n')
		}
		in.WriteByte('\n')
		if i < len(bodies) {
			in.WriteString(bodies[i])
		}
		in.WriteString("\n\n")
	}
	return in.String()
}

// WriteSegment writes 2 to 4 related stories as one flowing segment and
// returns them split back into one block per story, in call order.
//
// Cache is consulted PER STORY, before a key is required, for the same reason
// Segment's own cache is: text already paid for and sitting on disk must not
// be withheld because a credential was rotated. A group counts as cached only
// when EVERY block is on disk — a partial hit still pays for the whole call,
// because the blocks were written together and a lone cached block cannot be
// regenerated to match siblings it no longer has.
func (p *Podcast) WriteSegment(ctx context.Context, g SegmentGroup) ([]SegmentBlock, error) {
	if n := len(g.Stories); n < MinSegmentStories || n > MaxSegmentStories {
		return nil, fmt.Errorf("smart: a segment takes %d-%d stories, got %d",
			MinSegmentStories, MaxSegmentStories, n)
	}

	bodies := make([]string, len(g.Stories))
	for i, s := range g.Stories {
		b := strings.TrimSpace(s.Body)
		if b == "" {
			return nil, ErrNothingToSummarise
		}
		if len(b) > maxInputChars {
			// On a word boundary, like Segment.Segment does, so the model's last
			// piece of evidence for this story is not half a word.
			if cut := strings.LastIndexByte(b[:maxInputChars], ' '); cut > maxInputChars/2 {
				b = b[:cut]
			} else {
				b = b[:maxInputChars]
			}
		}
		bodies[i] = b
	}

	model := p.model(ctx)

	paths := make([]string, len(g.Stories))
	blocks := make([]SegmentBlock, len(g.Stories))
	allCached := p.dir != ""
	for i, s := range g.Stories {
		path := p.groupCachePath(g, i, model)
		paths[i] = path
		if path == "" {
			allCached = false
			continue
		}
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			blocks[i] = SegmentBlock{ItemID: s.ItemID, Text: string(b)}
		} else {
			allCached = false
		}
	}
	if allCached {
		return blocks, nil
	}

	if !p.Configured(ctx) {
		return nil, llm.ErrNotConfigured
	}

	// The GROUPED write keeps its schema — one block per story, and the caller
	// splits the reply by story — so it is `Extracting` rather than
	// `Summarizing`. Same cache-key reasoning as Segment above: the instructions
	// are passed through unchanged.
	//
	// Not `Strict()`, for the reason given at interest.go's rerank: rejecting a
	// field the schema does not name is exactly wrong for a contract that
	// tolerates one, and this is the most expensive call in the feature — a
	// whole broadcast's worth of stories written in one request. Failing it
	// because the model volunteered an extra key would re-bill all of them.
	// Every block is re-validated below regardless.
	reply, err := schemaflux.Extracting[podcastGroupReply](segmentGroupInput(g, bodies)).
		Steer(podcastGroupInstructionsFor(g.Vibe)).
		Fast().
		Run(p.llm.OpsContext(ctx))
	if err != nil {
		return nil, err
	}

	// Re-validated field by field, exactly as interest.go's rerank reply is:
	// the caller cannot trust this package and this package cannot trust the
	// provider, and both checks are cheap. `story` is a one-based ordinal into
	// g.Stories and nothing else may address a block.
	byStory := make(map[int]string, len(reply.Blocks))
	for _, b := range reply.Blocks {
		// A pointer because a derived schema makes every field required and
		// reads a zero int as an unanswered one. Story 0 is a real thing a model
		// sends and a real thing this loop must DROP rather than fail the whole
		// group over, so it has to arrive to be dropped.
		if b.Story == nil || *b.Story < 1 || *b.Story > len(g.Stories) {
			continue
		}
		if text := cleanForSpeech(b.Text); text != "" {
			byStory[*b.Story] = text
		}
	}
	if len(byStory) != len(g.Stories) {
		return nil, fmt.Errorf("smart: segment reply covered %d of %d stories",
			len(byStory), len(g.Stories))
	}

	for i, s := range g.Stories {
		text := byStory[i+1]
		blocks[i] = SegmentBlock{ItemID: s.ItemID, Text: text}
		if path := paths[i]; path != "" {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			// Temp-then-rename, like every other cache write in this file: a
			// reader who reloads mid-write must not find half a block.
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(text), 0o644); err == nil {
				_ = os.Rename(tmp, path)
			}
		}
	}
	return blocks, nil
}

// groupCachePath hashes ONE STORY's block together with the whole group it
// was written inside.
//
// The argument cachePath makes about the ordered PAIR applies one level up:
// the SAME story written into a different group — different neighbours, a
// different order, a different theme it transitions from — is a different
// piece of writing, because the handover and the "do not restate" rule mean
// every block's wording depends on what surrounds it. Caching on the story's
// id alone would serve a block that hands over from a story that, in THIS
// playing, is not the one actually next to it — the same failure cachePath's
// own comment describes, one level up the structure.
//
// The explicit "group" tag is what keeps this from ever colliding with
// cachePath's own hash space even by coincidence, the same way cachePath
// itself tags "intro" and "opened" so the three first-segment shapes cannot
// share an entry.
func (p *Podcast) groupCachePath(g SegmentGroup, idx int, model string) string {
	if p.dir == "" || idx < 0 || idx >= len(g.Stories) || g.Stories[idx].ItemID == "" {
		return ""
	}
	var ids strings.Builder
	for _, s := range g.Stories {
		ids.WriteString(s.ItemID)
		ids.WriteByte('\x01')
	}
	open := openCacheKey(g.Open)
	sum := sha256.Sum256([]byte(g.Stories[idx].ItemID + "\x00" + ids.String() + "\x00" +
		strconv.Itoa(idx) + "\x00" + strings.TrimSpace(g.PrevTheme) + "\x00" +
		model + "\x00" + PromptVersion + "\x00" + VibeFor(g.Vibe) + "\x00" +
		open + "\x00group"))
	name := hex.EncodeToString(sum[:]) + ".txt"
	// One level of fan-out, matching every other cache in this file.
	return filepath.Join(p.dir, name[:2], name)
}
