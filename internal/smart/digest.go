// Digest turns an article into something worth HEARING rather than reading.
//
// This is the second half of listening to a long feed (§10.7). The Smart+ voice
// will happily read a four-thousand-word essay at you, and the result is
// technically the article and practically unlistenable: prose written for the
// eye leans on paragraph breaks, subheadings, pull quotes and the ability to
// skim back a line, none of which survive being spoken.
//
// So this does not "shorten the article". It rewrites it for the ear, and the
// difference shows up in the prompt: no bullets, no headings, no "in this
// article we will", and never a sentence whose structure only parses on a second
// look. What comes back is about twenty-five seconds of speech that keeps the
// argument — shorter than the feed copy it sits beside rather than longer, which
// is what a summary has to be to be worth pressing play on. See targetWords.
//
// # What it costs and how often
//
// Once per article, ever. The digest is cached on disk beside the audio, keyed
// by the item and the prompt version, so a re-listen re-bills nothing — and a
// reader who switches voice re-synthesises the audio but does not re-summarise
// the text. Article text is immutable, so there is no TTL here for the same
// reason there is none on the audio cache: an expiry could only ever be a
// schedule for re-buying an identical paragraph.

package smart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"

	"time"
)

// promptVersion is part of the cache key.
//
// Bumping it re-summarises everything, which is the point: a change to the
// instructions below that nobody can invalidate is a change that only applies to
// articles nobody has listened to yet, and the difference between the two
// halves of the library would be invisible and permanent.
const promptVersion = "v2"

// targetWords is the length the instructions ask for.
//
// It asked for 180 — about a minute — on the reasoning that a shorter digest
// tells you the topic and leaves you to go and read the thing anyway. Per
// product direction that was the wrong end of the trade in practice: set beside
// the feed copy it is summarising, a minute of speech is LONGER than the thing
// somebody pressed play to avoid, and it stopped sounding like a summary at all.
//
// Seventy words is twenty-five seconds or so — recognisably shorter than the
// article, in the same neighbourhood as the blurb already on the card, and still
// enough for a finding and its reason. The rules below carry the rest of the
// weight: what protects the argument at this length is "lead with the finding"
// and "keep the specifics", not the word count.
const targetWords = 70

// maxInputChars bounds what is sent to the model.
//
// Generous, because the whole value here is handling the LONG article — cutting
// the input at the point where summarising starts to matter would be a feature
// that works only when it is not needed. What it stops is a scraped page that
// came back as a hundred kilobytes of navigation.
const maxInputChars = 60 << 10

// maxOutputTokens has to cover the model's THINKING as well as the answer (see
// llm.Request). A seventy-word answer is ~100 tokens; the rest is headroom so a
// model that deliberates does not truncate mid-sentence, which is audible. Left
// where it was when the target came down — the headroom was never about the
// answer's length, and a tighter ceiling would buy nothing but truncation.
const digestMaxTokens = 4000

// digestTimeout bounds one summarise.
//
// Stated here rather than inherited, which is the whole point of the constant.
// It used to be neither: this call passed the caller's context straight through
// and SchemaFlux layered its own thirty-second default underneath, so the real
// budget was a library default nothing in this file mentioned — and one that
// applied to `Summarizing` but not to `Extracting`, so two Smart+ features with
// the same shape had different budgets for no reason anybody chose. SchemaFlux
// DX-008 made a caller's deadline authoritative, which makes this constant the
// answer instead of a suggestion.
//
// Ninety seconds, matching the interest pass. A reader is waiting on this one,
// but they are waiting on speech synthesis after it either way, and the failure
// this guards against is a stalled provider holding the request open — not a
// slow one.
const digestTimeout = 90 * time.Second

// ErrNothingToSummarise means the item had no usable text. Distinct so the
// caller can fall back to reading the article itself rather than reporting a
// failure for an item that simply has no body.
var ErrNothingToSummarise = errors.New("smart: nothing to summarise")

// Digest writes spoken summaries.
type Digest struct {
	llm      llmClient
	settings *store.SettingsRepo
	dir      string
}

// NewDigest wires the summariser. dir may be empty, in which case nothing is
// cached and every listen re-summarises — correct for a test, expensive for a
// server.
//
// client is the llmClient seam, not the concrete type: production callers keep
// passing a *llm.Client (it satisfies the seam implicitly) and tests pass a
// fake that never reaches the network.
func NewDigest(client llmClient, settings *store.SettingsRepo, dir string) *Digest {
	return &Digest{llm: client, settings: settings, dir: dir}
}

// Configured reports whether summarising can happen at all.
func (d *Digest) Configured(ctx context.Context) bool {
	return d != nil && d.llm != nil && d.llm.Configured(ctx)
}

// instructions is the whole difference between a summary and a script.
//
// Every clause here is load-bearing and most of them are negative, because the
// default behaviour of a summarising model is to produce a document: a title, a
// bulleted list of "key takeaways", and a closing sentence that restates the
// opening one. Read aloud, that is unbearable — "bullet, bullet, bullet" with no
// intonation to hang meaning on, and the listener cannot skim past it.
// A var rather than a const so the word count comes from targetWords instead of
// being typed twice. Two copies of the same number, one in the prompt and one in
// the code, is the kind of thing that stays wrong for months because each half
// looks correct on its own.
var digestInstructions = `You SHORTEN an article so it can be listened to. The same article, in fewer words. You are not retelling it, not presenting it and not introducing it.

Write about ` + strconv.Itoa(targetWords) + ` words of continuous spoken prose. Rules:

- Add NOTHING. No framing, no scene-setting, no context the article did not give, no opinion of your own about it, and no conclusion it did not draw. Every sentence you write must be something the article itself says.
- NO INTRODUCTION OF ANY KIND. Do not greet the listener, do not say good morning, do not announce the publication, the headline, the date or what is coming. The first words out are the substance. There is no show around this: nothing came before it and nothing follows it, so anything that hands over, sets a scene or signs off is wrong here.
- Plain flowing sentences only. NO bullet points, NO numbered lists, NO headings, NO markdown of any kind. Every character you emit will be read aloud by a speech synthesiser, so a hyphen or an asterisk becomes a noise.
- Lead with the actual finding, claim or event. Never open with "This article discusses" or "The author argues that" — say the thing itself.
- Keep the argument, not just the topic. A listener should finish knowing what was concluded and why, not merely what the subject was.
- Keep concrete specifics: names, numbers, dates, the actual mechanism. These are what make a summary worth hearing; adjectives are not.
- Spell out anything that reads badly aloud. Write "about 40 percent", not "~40%". Expand an abbreviation the first time unless it is universally spoken as letters.
- Do not address the listener, do not editorialise, and do not close with a summary of your summary. Stop when the argument is delivered.
- If the text is an error page, a paywall notice, a cookie banner or otherwise not an article, say so in one short sentence and stop.

Output the spoken text and nothing else.`

// Speakable returns the digest for one article, from cache when possible.
//
// title and source are passed separately rather than glued onto the body: the
// model gets them as context for what it is summarising, and the CALLER decides
// how the finished segment is introduced. Putting the announcement in here would
// bake one presentation into the cached text.
func (d *Digest) Speakable(ctx context.Context, itemID, source, title, body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ErrNothingToSummarise
	}
	// The cache is consulted BEFORE the key is required, which is the right way
	// round: this text has already been paid for and is sitting on disk, and
	// refusing to hand it over because the key was rotated out would punish the
	// reader for a configuration change that costs nothing to work around.
	model := d.model(ctx)
	path := d.cachePath(itemID, model)
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	if !d.Configured(ctx) {
		return "", llm.ErrNotConfigured
	}

	if len(body) > maxInputChars {
		// On a word boundary, so the model is not handed half a word as its last
		// piece of evidence.
		if cut := strings.LastIndexByte(body[:maxInputChars], ' '); cut > maxInputChars/2 {
			body = body[:cut]
		} else {
			body = body[:maxInputChars]
		}
	}

	var in strings.Builder
	if s := strings.TrimSpace(source); s != "" {
		in.WriteString("Publication: ")
		in.WriteString(s)
		in.WriteByte('\n')
	}
	if t := strings.TrimSpace(title); t != "" {
		in.WriteString("Headline: ")
		in.WriteString(t)
		in.WriteByte('\n')
	}
	in.WriteByte('\n')
	in.WriteString(body)

	// Rebuilt on `Summarizing` (plan P3.4), which is what this operation is:
	// the same article in fewer words, not a retelling.
	//
	// The whole brief stays — every rule in `digestInstructions` is about
	// SPOKEN output (no markdown, no headings, spell out "40 percent") and none
	// of it is something a general-purpose summariser would know. It goes in
	// through Steer, which reaches the provider now that SchemaFlux ST-010 is
	// fixed; before that it would have been silently dropped and every digest
	// would have come back full of bullet points for the synthesiser to read out
	// as noise.
	//
	// The model is applied by the bridge from this instance's setting — see
	// llm.Client.OpsContext — so the `model` resolved above is no longer named
	// here. Fast for the same reason the request said Effort low: condensing an
	// argument already on the page is not a reasoning problem.
	// The context has to be the OPS context — a bare one resolves the provider
	// from a package global, which on a fresh process is a mock whose answers
	// parse into zero values and look like a working deployment.
	//
	// It carries an explicit deadline now. Before SchemaFlux DX-002 the text
	// operations could not take a context on `Run` at all, so this read
	// `.Context(ctx).Run()` while every other feature read `.Run(ctx)`; the two
	// spellings meant the same thing and neither said so.
	call, cancel := context.WithTimeout(d.llm.OpsContext(ctx), digestTimeout)
	defer cancel()

	out, err := schemaflux.Summarizing(in.String()).
		Steer(digestInstructions).
		// The ceiling, restated rather than inherited from `Fast` — see
		// ceilings.go. It was `MaxLength(digestMaxTokens)` for a while, which
		// is a DIFFERENT KNOB wearing a similar name: `MaxLength` sets
		// `TargetLength`, measured in SENTENCES, and the library put "Target
		// length: 4000 sentences" in the prompt while the actual token ceiling
		// quietly fell to Fast's 2000. The only reason that was not visibly
		// broken is that the length invariant is an upper bound and no summary
		// has ever been four thousand sentences.
		Configure(capSummarize(digestMaxTokens)).
		Model(d.llm.OpsModel(ctx)).
		Fast().
		Run(call)
	if err != nil {
		// An answer with no content is not a provider fault, it is nothing to
		// summarise — which is a state three callers key on to fall back to
		// reading the article (app/speech.go, app/refusal.go, app/doctor.go).
		// SchemaFlux refuses an empty completion before this code sees it, so
		// the sentinel has to be restored here or that fallback silently
		// becomes an error banner.
		//
		// Matched on the message because the library exposes no sentinel for
		// it. Fragile, and deliberately narrow: anything else is passed through
		// as the provider failure it is. If this ever stops matching, the test
		// beside it fails rather than the behaviour drifting quietly.
		if strings.Contains(err.Error(), "empty completion content") {
			return "", ErrNothingToSummarise
		}
		return "", err
	}
	out = cleanForSpeech(out)
	if out == "" {
		return "", ErrNothingToSummarise
	}

	if path != "" {
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		// Temp-then-rename, like the audio cache: a reader who reloads mid-write
		// must not find half a digest and conclude the feature is broken.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), 0o644); err == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return out, nil
}

// The two shapes a list marker arrives in. Separate patterns because the second
// is the one that gets missed: "- item" is obviously a bullet, and a lone "-"
// on its own line does not look like one until a voice reads it out as "dash".
var (
	listMarkerRE = regexp.MustCompile(`^(?:[-*•–—]|\d{1,2}[.)])\s+`)
	bareMarkerRE = regexp.MustCompile(`^(?:[-*•–—]|\d{1,2}[.)])$`)
)

// cleanForSpeech strips the markdown the instructions asked the model not to
// emit.
//
// Belt and braces, and not paranoia: a model told six times not to use bullets
// will still occasionally produce one, and the cost of it getting through is
// that a synthesiser reads "asterisk" aloud in the middle of a sentence. Cheap
// to remove here, impossible to remove once it is cached as audio.
func cleanForSpeech(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		// Heading hashes.
		ln = strings.TrimSpace(strings.TrimLeft(ln, "#"))
		// A list marker, bulleted or numbered, with the space that usually
		// follows it — and separately the case of a marker ALONE on its line,
		// which the "marker plus space" form misses and which a synthesiser
		// pronounces: a lone "-" is read aloud as "dash".
		ln = strings.TrimSpace(listMarkerRE.ReplaceAllString(ln, ""))
		if bareMarkerRE.MatchString(ln) {
			ln = ""
		}
		// Emphasis markers anywhere. Removed rather than converted: there is no
		// spoken equivalent of italics, so the honest rendering is the word.
		ln = strings.NewReplacer("**", "", "__", "", "`", "").Replace(ln)
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, ln)
		}
	}
	return strings.Join(out, "\n\n")
}

// model is the instance's Smart+ model, or the built-in default.
// Cached returns a digest only if it is already on disk, and never writes one.
//
// The captions' half of the same bargain Podcast.Cached makes (TODO 11.47): the
// slide shows the words being spoken, and a request that could trigger a model
// call would buy a second summary to put text on a screen. A miss is `false`
// rather than an error, because by the time the audio exists the digest does.
func (d *Digest) Cached(ctx context.Context, itemID string) (string, bool) {
	if d == nil || itemID == "" {
		return "", false
	}
	path := d.cachePath(itemID, d.model(ctx))
	if path == "" {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

func (d *Digest) model(ctx context.Context) string {
	if d.settings == nil {
		return llm.DefaultModel
	}
	m, err := d.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil || strings.TrimSpace(m) == "" {
		return llm.DefaultModel
	}
	return strings.TrimSpace(m)
}

// cachePath hashes the item, the model and the prompt version together.
//
// All three, because all three change the answer: a different model writes a
// different digest, and an edit to the instructions is exactly the case where
// serving the old text would make the change invisible.
func (d *Digest) cachePath(itemID, model string) string {
	if d.dir == "" || itemID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(itemID + "\x00" + model + "\x00" + promptVersion))
	name := hex.EncodeToString(sum[:]) + ".txt"
	// One level of fan-out, matching the audio cache, so a heavy listener does
	// not end up with one directory holding tens of thousands of entries.
	return filepath.Join(d.dir, name[:2], name)
}
