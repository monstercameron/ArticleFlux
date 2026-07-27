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
const podcastPromptVersion = "v1"

// podcastWords is the length the instructions ask for.
//
// Longer than the digest's 180 because the handover is not free — it is a
// sentence and sometimes two, and taking it out of the same budget would buy the
// continuity by making every story shorter than the digest a reader already
// chose. About seventy-five seconds of speech.
const podcastWords = 210

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
}

// Podcast writes broadcast segments.
type Podcast struct {
	llm      *llm.Client
	settings *store.SettingsRepo
	dir      string
}

// NewPodcast wires the writer. dir may be empty, in which case nothing is cached
// and every listen rewrites — correct for a test, expensive for a server.
func NewPodcast(client *llm.Client, settings *store.SettingsRepo, dir string) *Podcast {
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
var podcastInstructions = `You are the single narrating voice of a continuous news broadcast, writing ONE segment of it.

You will be given the story to cover, and — unless this is the opening segment — the source and headline of the story you have just finished covering.

Write about ` + strconv.Itoa(podcastWords) + ` words of continuous spoken prose, structured as:

1. A HANDOVER of one or two sentences from the previous story into this one. If the two are genuinely related — same subject, same industry, same country, cause and effect, agreement or contradiction — say what the relation IS; that connection is the most valuable sentence in the segment. If they are unrelated, make a plain, unhurried change of subject and do not pretend to a link. Never use a stock phrase like "in other news" or "turning now to" as a substitute for saying what is changing.
2. The story itself: what happened or was found, who by, and why it matters. Lead with the substance, never with "this article says" or "the author argues".

Rules:

- Plain flowing sentences only. NO bullet points, NO numbered lists, NO headings, NO markdown, NO stage directions, NO sound cues, NO speaker labels. Every character you emit is read aloud by a speech synthesiser, so an asterisk or a bracket becomes a noise.
- Name the publication once, naturally, in the sentence where you introduce the story. It is how a listener decides how much weight to give it.
- Keep concrete specifics: names, numbers, dates, the mechanism. These are what survive being heard once.
- Spell out anything that reads badly aloud. Write "about 40 percent", not "~40%".
- If this is the OPENING segment, open the broadcast plainly with the story. Do not greet the listener and do not describe what is coming up.
- Never invent a programme name, a station, a host, or a byline for yourself. You have no name.
- Never say what is coming next. You have not been told, and guessing is a false statement.
- Do not sign off, do not thank the listener, and do not summarise what you just said. The broadcast continues after you; end on the last fact of the story.
- If the text is an error page, a paywall notice, a cookie banner or otherwise not an article, hand over from the previous story, say in one sentence that this one could not be read, and stop.

Output the spoken text and nothing else.`

// Segment returns the spoken text for one slot, from cache when possible.
//
// Returns ErrNothingToSummarise for an item with no usable text, exactly like
// the digest — the caller falls back to reading the article, which is the right
// answer for a two-line link post and a poor one to spend a model call on.
func (p *Podcast) Segment(ctx context.Context, seg Segment) (string, error) {
	body := strings.TrimSpace(seg.Body)
	if body == "" {
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

	if len(body) > maxInputChars {
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
		Instructions: podcastInstructions,
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
	sum := sha256.Sum256([]byte(seg.ItemID + "\x00" + seg.PrevID + "\x00" +
		model + "\x00" + podcastPromptVersion))
	name := hex.EncodeToString(sum[:]) + ".txt"
	// One level of fan-out, matching the digest and audio caches, so a long
	// listener does not end up with one directory holding tens of thousands of
	// entries.
	return filepath.Join(p.dir, name[:2], name)
}
