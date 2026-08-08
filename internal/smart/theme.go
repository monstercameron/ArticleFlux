package smart

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"

	schemaflux "github.com/monstercameron/schemaflux"
)

// The Smart+ half of the theming engine (§20.16.3).
//
// Two calls, and they are the same call with different instructions:
//
//   - **Compose** takes the reader's own words and returns a palette.
//   - **Attune** takes up to five topic labels — what this person actually reads —
//     and returns the palette of a room built for those subjects.
//
// # Why this imports client/design
//
// For the reason internal/smart/translate.go imports client/i18n: the thing being
// produced lives there. `design` is deliberately pure — no syscall/js, no DOM, no
// GWC runtime (see tokens.go) — precisely so the server can hold the same palette
// type the browser paints with, and `design.NewGenerated` is the one place a
// model-authored palette becomes a Theme.
//
// The alternative is for the server to hand back twelve loose strings and let the
// client assemble them, which means the AA repair happens in the browser, which
// means an unreadable palette exists on the wire and in the database and is only
// fixed at the last moment by whichever client happens to be up to date. Doing it
// here means what is STORED has already passed the floor.
//
// # Failure is silent and total, like every other Smart+ path
//
// Every method returns an error on any doubt, and every caller treats an error as
// "no theme this time". A missing key, a timeout, a model that answered in prose,
// a palette that cannot be repaired: all of them leave the reader with the theme
// they already had, which is a complete product. Attune in particular has a
// deterministic free-tier answer (design.Attune over design.AttuneHue), so an
// error here costs richness and nothing else.

// Palettes is the theme generator.
type Palettes struct {
	// The seam from llmclient.go rather than a concrete *llm.Client, so the
	// composition path is testable without a key and without a bill. It matters
	// more here than for most features in this package: what a theming request
	// SENDS is the whole of §20.16.3's privacy claim, and a claim nothing asserts
	// is a comment.
	llm      llmClient
	settings *store.SettingsRepo
}

// NewPalettes wires the generator.
func NewPalettes(c llmClient, s *store.SettingsRepo) *Palettes {
	return &Palettes{llm: c, settings: s}
}

// ErrNoPrompt means the reader pressed the button with an empty box. Named so the
// transport can say so rather than spending a request to be told nothing.
var ErrNoPrompt = errors.New("smart: no prompt")

// themeTimeout bounds one call.
//
// Thirty seconds, and unlike the interest layer's ninety this one is about
// LATENCY: a reader is watching a button spin, and a palette that arrives after a
// minute arrives after they have given up and pressed it again. The work is small
// — one object of thirteen values — so a call that has not answered in thirty
// seconds is not a call that is nearly done.
const themeTimeout = 30 * time.Second

// composeInstructions is the system prompt for a palette from a phrase.
//
// It spends most of its length on what each token IS, because that is the part a
// model cannot guess and the part that goes wrong invisibly. A model that thinks
// `--cc` is a decorative highlight returns a pale colour, the app darkens it to
// keep the "new" tag legible, and the reader gets a theme that is subtly not the
// one that was described — with no error anywhere.
//
// It also states that the floor is ENFORCED. That is deliberate: a model told
// "please keep it accessible" optimises the request it can see, and a model told
// "colours that fail will be moved for you, so leave yourself room" optimises for
// not being moved. The second produces palettes that need no repair, which is the
// outcome that preserves what was asked for.
const composeInstructions = `You are designing a colour theme for a long-form feed reader.

Return one palette as hex colours. The application will check every pair against
WCAG AA (4.5:1) and will darken or lighten anything that fails, so leave yourself
room — a colour that gets moved is no longer the colour you chose.

The tokens, and what each one actually is:
- ground: the page. Everything else is judged against it.
- raised: a hovered row and a card. A step off the ground, not a different colour.
- sunk: the row the reader is sitting on. A step further than raised.
- line: borders. Must be visible against the ground.
- hair: row separators. Softer than line, but still divides two rows.
- cream: primary text. This is the workhorse; make it comfortably legible.
- soft: secondary text.
- dim: tertiary text — datelines and counts, set at 11.5px. The one most often
  too faint. Give it real contrast.
- read: the article's prose, a hair off cream.
- accent: the marker saying "you are here". It is also used as a FILL with the
  ground colour as text ON TOP of it, so the ground must be legible against the
  accent. On a dark theme the accent is light; on a light theme it is DARK.
- pos and neg: approval and refusal — liked, saved, disliked, destructive. They
  must read as good and bad at a glance and stay legible on all three surfaces.
- wash: how much of a source's own hue tints the article background, as a whole
  percent. 6 for a stark theme, 12 for a neutral one, up to about 24 for a theme
  whose ground already has colour of its own to dilute it. Never more than 40.

Also return:
- label: two or three words naming the theme, in title case. No punctuation.
- blurb: one short sentence saying when someone would want it.

Match the tone you are given. A dark theme has a dark ground and light text; a
light theme has a pale ground and dark text. Do not return the other one, however
well it fits the description.

Make it a designed palette rather than a hue rotation: the greys should share a
temperature, and the accent should belong to the same family.`

// attuneInstructions is the system prompt for a palette from what someone reads.
//
// The framing matters more here than in Compose, and it is aimed at one failure:
// asked "what colour is SQLite internals", a model reaches for literal
// associations — a database is blue, cooking is orange — and produces a palette
// that is a joke about the subject rather than a room to read it in. So the
// question is asked as a room, and the model is told the reader will never be
// shown the reasoning.
const attuneInstructions = `You are choosing the colours of a room for someone to read in.

You will be given a few subjects this person actually reads about, and a tone. Give
back the palette of a room that suits spending hours reading those subjects.

Do NOT illustrate the subjects. "Databases are blue" and "cooking is orange" are
the wrong kind of answer — this person will never be told why these colours were
chosen, and a palette that is a joke about a topic is one they have to live inside.
Think about the temperature and the quietness the subjects call for, not their
iconography.

The application will check every pair against WCAG AA (4.5:1) and will darken or
lighten anything that fails, so leave yourself room.

The tokens, and what each one actually is:
- ground: the page. raised: a hovered row. sunk: the row being read.
- line: borders, visible. hair: row separators, softer.
- cream: primary text. soft: secondary. dim: 11.5px datelines — give it real
  contrast. read: article prose, a hair off cream.
- accent: the "you are here" marker, and a FILL carrying the ground colour as
  text. Light on a dark theme, DARK on a light one.
- pos and neg: approval and refusal, legible on all three surfaces.
- wash: percent of a source's hue behind the article, 6 to 24, never over 40.

Also return a label of two or three words and a one-sentence blurb. The label
should describe the ROOM, not the subjects — it appears in a theme picker.

Match the tone you are given, exactly.`

var hexProp = map[string]any{
	"type":    "string",
	"pattern": "^#[0-9A-Fa-f]{6}$",
}

// paletteReply is the wire shape of an answer.
type paletteReply struct {
	Label  string `json:"label"`
	Blurb  string `json:"blurb"`
	Ground string `json:"ground"`
	Raised string `json:"raised"`
	Sunk   string `json:"sunk"`
	Line   string `json:"line"`
	Hair   string `json:"hair"`
	Cream  string `json:"cream"`
	Soft   string `json:"soft"`
	Dim    string `json:"dim"`
	Read   string `json:"read"`
	Accent string `json:"accent"`
	Pos    string `json:"pos"`
	Neg    string `json:"neg"`
	Wash   int    `json:"wash"`
}

// Compose turns a phrase into a palette.
//
// Returns the theme, and the repairs the readability floor had to make. The
// repairs are not an error and not a warning — they are what the Appearance screen
// says out loud, because a palette that came back slightly different from the one
// described is a thing the reader should be told rather than left to suspect.
func (p *Palettes) Compose(ctx context.Context, prompt string, tone design.Tone) (
	design.Theme, []design.Repair, error) {

	if strings.TrimSpace(prompt) == "" {
		return design.Theme{}, nil, ErrNoPrompt
	}
	payload, _ := llm.ThemePayload{Prompt: prompt, Tone: string(tone)}.Trim()
	return p.ask(ctx, composeInstructions, payload, "palette")
}

// Attune turns what someone reads into a palette.
//
// The labels are the reader's topics, largest first, and the caller has already
// decided which ones count. Nothing else goes: not the terms behind them, not how
// many articles are in each, not which sources they came from — see
// internal/llm/theme.go for why the labels alone are the whole question.
func (p *Palettes) Attune(ctx context.Context, interests []string, tone design.Tone) (
	design.Theme, []design.Repair, error) {

	payload, _ := llm.ThemePayload{Interests: interests, Tone: string(tone)}.Trim()
	if len(payload.Interests) == 0 {
		// Not an error the reader ever sees: the caller has a deterministic answer
		// for a reader whose topics have not formed yet, and this says to use it.
		return design.Theme{}, nil, fmt.Errorf("smart: no interests to attune to")
	}
	return p.ask(ctx, attuneInstructions, payload, "attuned_palette")
}

// ask is the shared call. One place the request is built, so the two entry points
// cannot differ in what they send — which is the same argument internal/smart's
// Interest gives for being the only assembler of a RankPayload.
func (p *Palettes) ask(ctx context.Context, instructions string, payload llm.ThemePayload,
	schemaName string) (design.Theme, []design.Repair, error) {

	if !p.llm.Configured(ctx) {
		return design.Theme{}, nil, llm.ErrNotConfigured
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return design.Theme{}, nil, err
	}

	call, cancel := context.WithTimeout(p.llm.OpsContext(ctx), themeTimeout)
	defer cancel()

	// Rebuilt on `Generating` (plan P3.7). The schema comes from `paletteReply`
	// derived from paletteReply rather than hand-written beside it — thirteen colour fields
	// that had to agree across two declarations and nothing checked they did.
	//
	// **The AA repair stays server-side**, below: `design.NewGenerated` is what
	// enforces the readability floor and reports what it had to move. A palette
	// the model swears is legible is a claim; the contrast ratio is a
	// measurement, and this repo does the measuring.
	//
	// `Smart()` rather than `Fast()`, which is this operation's version of the
	// Effort medium the hand-built request set: "which of these greys share a
	// temperature" is deliberation, and it is the product being bought. A cheap
	// answer here is twelve colours that individually satisfy the brief and
	// together look like a spreadsheet.
	reply, err := schemaflux.Generating[paletteReply](string(body)).
		Steer(instructions).
		// The ceiling, stated rather than inherited from the tier (ceilings.go).
		Configure(capGenerate(paletteMaxTokens)).
		Model(p.llm.OpsModel(ctx)).
		Smart().
		Strict().
		Run(call)
	if err != nil {
		return design.Theme{}, nil, err
	}

	theme, reps, err := design.NewGenerated(reply.Label, reply.Blurb, design.GeneratedTokens{
		Ground: reply.Ground, Raised: reply.Raised, Sunk: reply.Sunk,
		Line: reply.Line, Hair: reply.Hair,
		Cream: reply.Cream, Soft: reply.Soft, Dim: reply.Dim, Read: reply.Read,
		Accent: reply.Accent, Pos: reply.Pos, Neg: reply.Neg,
		Wash: reply.Wash,
	})
	if err != nil {
		return design.Theme{}, nil, fmt.Errorf("smart: %w", err)
	}
	// The tone is checked AFTER the palette is built, because it is derived from
	// the ground rather than taken from the reply (design.ToneOf) — a model that
	// says "dark" and hands back paper has answered the wrong question, and the
	// palette itself is the only honest evidence of which.
	//
	// Refused rather than accepted, and this is the one quality failure worth
	// erroring on: the caller asked for a tone because that is the tone the answer
	// has to be able to blend with, and design.Blend across the boundary does
	// nothing at all (see its tone guard). A wrong-tone palette would be a theme
	// that applies once and then never drifts, which is unexplainable.
	if want := design.Tone(payload.Tone); want != "" && theme.Tone != want {
		return design.Theme{}, nil, fmt.Errorf(
			"smart: asked for a %s palette and got a %s one", want, theme.Tone)
	}
	return theme, reps, nil
}

// model resolves the configured model, or the built-in default.
//
// The same shape as Interest.model, and deliberately: the model is an instance
// setting, and a feature that quietly used a different one is the kind of
// divergence that gets diagnosed as a billing surprise.
func (p *Palettes) model(ctx context.Context) (string, error) {
	if p.settings == nil {
		return "", nil
	}
	m, err := p.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(m), nil
}
