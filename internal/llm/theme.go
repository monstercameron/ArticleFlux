package llm

import "strings"

// The theming payload — §20.16.3's egress boundary, named and scoped.
//
// # Two calls, and what each one sends
//
//   - **Ask for a theme.** The reader types "a cold library at 2am" and gets a
//     palette. What leaves is that sentence and the tone it should be built for.
//   - **Attune.** The interface slowly takes on the colour of what the reader
//     reads, and the model is asked what room those subjects belong in. What
//     leaves is up to five TOPIC LABELS and the tone.
//
// # The prompt is a new kind of thing, so the argument is written down
//
// §18.8's subject is the reading HISTORY: what you opened, what you dwelt on,
// what you starred. A per-item log identifies a person, and the allowlist exists
// so that no amount of "it would rank better with a bit more context" can widen
// it.
//
// A theme prompt is none of that. It is a sentence the reader typed INTO a text
// box that says it is sent to a language model, in order to get an answer back
// from that model — the disclosure is the request. There is no version of this
// feature where the prompt stays on the machine, so the honest treatment is to
// bound it, name it, and audit it, which is what this file does. It is capped at
// MaxThemePromptRunes because a theme request is a sentence.
//
// # Topic labels were already permitted, and nothing else is
//
// §18.8 permits "topic labels, their top ~30 terms, and the top ~10 source titles
// by affinity — bare strings, aggregated, never a log". Attune sends the labels
// and NOT the terms: a label is two to four words naming an interest, and the
// terms are the vocabulary the clustering found, which is a much sharper
// fingerprint of a specific person's reading for no gain at all here. The model is
// being asked what colour a subject feels like, and "NPU inference" is the whole
// of the question.
//
// Absent and deliberately so: item titles, summaries, source titles, member
// counts, trends, how many topics there are in total, and anything that would let
// a provider tell one instance from another across two requests.

// Caps, enforced at the boundary rather than at the call site — the same argument
// RankPayload.Trim and ClassifyPayload.Trim make.
const (
	// MaxThemePromptRunes bounds one theme request.
	//
	// 240 runes: a couple of sentences. It is a quality bound as much as a cost
	// one — a model handed four paragraphs about someone's childhood bedroom
	// returns a palette that answers the paragraph it liked, and the reader has
	// no way to tell which.
	MaxThemePromptRunes = 240

	// MaxThemeInterests is how many topic labels describe a reader's taste here.
	//
	// Five. Not because more would cost much, but because the answer degrades:
	// asked to find one room for twelve subjects a model returns beige, which is
	// the average of everything and the colour of nothing. The caller sends the
	// largest few, and the drift changes when they do.
	MaxThemeInterests = 5

	// MaxThemeLabelRunes bounds one label. A topic label is two to four words
	// (see smart.MaxTopicLabelRunes, which is the same 40 for the same reason).
	MaxThemeLabelRunes = 40
)

// ThemePayload is one theming request body.
//
// Tone is "dark" or "light" and it is not a suggestion: a palette generated for
// the wrong tone is not a near miss, it is cream text on cream. The client sends
// the tone of the theme currently in force, because that is the theme the answer
// has to be able to blend with (see design.Blend's tone guard).
type ThemePayload struct {
	// Prompt is the reader's own words. Empty on the attune path.
	Prompt string `json:"prompt,omitempty"`
	// Interests is up to five topic labels. Empty on the prompt path.
	Interests []string `json:"interests,omitempty"`
	Tone      string   `json:"tone"`
}

// Trim applies every cap and reports whether anything was cut.
//
// Reported rather than silently applied, because "no silent caps" is a rule this
// codebase holds itself to: a prompt quietly truncated at 240 runes produces a
// theme that answers half a sentence, and a reader who cannot see that the
// sentence was cut has no way to understand the answer.
func (p ThemePayload) Trim() (ThemePayload, bool) {
	cut := false
	if r := []rune(p.Prompt); len(r) > MaxThemePromptRunes {
		p.Prompt = string(r[:MaxThemePromptRunes])
		cut = true
	}
	p.Prompt = strings.Join(strings.Fields(p.Prompt), " ")

	out := make([]string, 0, MaxThemeInterests)
	for _, l := range p.Interests {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" {
			continue
		}
		if r := []rune(l); len(r) > MaxThemeLabelRunes {
			l = string(r[:MaxThemeLabelRunes])
			cut = true
		}
		if len(out) >= MaxThemeInterests {
			cut = true
			break
		}
		out = append(out, l)
	}
	p.Interests = out
	return p, cut
}

// ThemeKeys is the allowlist for a theming body.
//
// A THIRD list rather than additions to either existing one, for the reason
// ClassifyKeys gives at length: `AuditEgress` is one global check, so admitting
// `prompt` there would make a free-text field legal in a rank payload — and the
// entire argument for types-as-enforcement is that the boundary cannot be widened
// by accident from somewhere else. One shared list means every future exception
// loosens every existing caller.
//
// TestThemeKeysDidNotWidenTheOthers asserts that `prompt` never appears in
// EgressKeys or ClassifyKeys, which is what makes the split real rather than a
// convention.
var ThemeKeys = map[string]bool{
	"prompt":    true,
	"interests": true,
	"tone":      true,
}

// AuditThemeEgress reports any JSON key in a theming body that §20.16.3 does not
// permit.
func AuditThemeEgress(body []byte) ([]string, error) {
	return auditAgainst(body, ThemeKeys)
}
