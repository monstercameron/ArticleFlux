package llm

import (
	"encoding/json"
	"strings"
)

// The classification payload — §18.8's allowlist, amended narrowly and named
// (plan.md §27.4e, TODO 10.12).
//
// # What changes, and the argument for it
//
// §18.8 forbids full article text. This carries up to 2,000 words of it, so the
// exception is written down here rather than worked around, with the reasoning
// stated so that it can be disagreed with:
//
//	**§18.8 protects the READER.** Its subject is the reading history — what you
//	opened, what you dwelt on, what you starred — a per-item log that identifies
//	a person. An article's body is the PUBLISHER's text, fetched from a public
//	URL, and the fact disclosed by sending it is "some instance subscribes to
//	this feed": aggregate, and already implied by the fetch itself.
//
// The classifier asks a question **about the article**. §18.8b's re-rank asks a
// question **about the reader**, and its allowlist is unchanged.
//
// # What is still forbidden, now explicitly
//
// No user id, tenant id, folder name, or feed URL. No read/starred/dwell state.
// No notes, ever. No engagement events, no timestamps, no per-item history. The
// **source title** is permitted — it is the byline a model needs to tell a press
// release from reporting — and the **feed URL is not**, because a URL is a stable
// identifier that lets a provider recognise the same instance across requests,
// which is §18.8's own reasoning and is unchanged.
//
// # The user's label vocabulary is reader data
//
// A category someone invented and the prompt they wrote for it describe that
// person, not the article, so `Labels` keeps §18.8's treatment: it may only be
// populated under the per-user consent key, never in the instance-wide read that
// serves every subscriber (§27.4d). `ClassifyPayload.Shared()` is the constructor
// that cannot carry it.

// Caps, enforced at the boundary rather than at the call site (§27.4c).
const (
	// MaxClassifyBodyWords is how much article text may leave. Beyond ~2,000
	// words is comment furniture and related-links blocks, which is vocabulary
	// from every category at once — so this is a quality bound as much as a cost
	// one.
	MaxClassifyBodyWords = 2000

	// MaxLabelPromptRunes bounds one per-label instruction.
	MaxLabelPromptRunes = 240

	// MaxLabelsBlockRunes bounds all of them together.
	//
	// The failure this prevents is silent and expensive: a reader tunes ten
	// prompts, the eleventh pushes the request past the model's attention, and the
	// earlier ones quietly stop working. Nothing errors and nothing logs, so the
	// only symptom is that tuning stopped helping.
	MaxLabelsBlockRunes = 4000
)

// ArticleText is what may leave about the article itself.
//
// Note the fields that are ABSENT and would each be natural to add: url, author,
// published_at, feed_url, word_count, item_id. The type is the enforcement
// (§18.8), so a field that does not exist cannot be sent by a caller who was
// sure it would help.
type ArticleText struct {
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
	// Source is the feed's TITLE — "Ars Technica" — never its URL.
	Source string `json:"source,omitempty"`
	Body   string `json:"body,omitempty"`
}

// LabelHint is one category or tag the model may choose from.
type LabelHint struct {
	Slug   string `json:"slug"`
	Name   string `json:"name,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

// LabelSet is the vocabulary offered for this request.
type LabelSet struct {
	Categories []LabelHint `json:"categories,omitempty"`
	Tags       []LabelHint `json:"tags,omitempty"`
}

// ClassifyWant is how many of each to return.
type ClassifyWant struct {
	Category  int `json:"category"`
	Secondary int `json:"secondary"`
	Tags      int `json:"tags"`
}

// ClassifyPayload is one classification request body.
type ClassifyPayload struct {
	Article ArticleText  `json:"article"`
	Labels  LabelSet     `json:"labels,omitempty"`
	Want    ClassifyWant `json:"want"`
}

// Shared builds the instance-wide payload: the article and the DEFAULT taxonomy
// only.
//
// The label set it accepts is the shipped one, and there is deliberately no
// parameter through which a per-user vocabulary could reach it. A single global
// read serves every subscriber on the instance, so one reader's private taxonomy
// appearing in it would shape every other reader's labels and would leave the
// machine on behalf of a job they did not consent to — a privacy defect, not a
// quality one (§27.4d).
func Shared(article ArticleText, builtins []LabelHint, want ClassifyWant) ClassifyPayload {
	return ClassifyPayload{
		Article: article,
		Labels:  LabelSet{Categories: builtins},
		Want:    want,
	}
}

// TrimReport records what the caps removed.
//
// Returned rather than logged inside, because "no silent caps" is a rule this
// codebase applies to itself: a request that quietly dropped eleven of a reader's
// forty prompts looks exactly like one where the tuning simply did not work. The
// caller logs it by name.
type TrimReport struct {
	// BodyWordsDropped is how much article text was cut.
	BodyWordsDropped int
	// PromptsTruncated names the labels whose own prompt was too long.
	PromptsTruncated []string
	// LabelsDropped names labels removed to fit MaxLabelsBlockRunes, lowest
	// priority first — which here means last in the slice, since the caller
	// orders them.
	LabelsDropped []string
}

// Empty reports whether anything was cut.
func (r TrimReport) Empty() bool {
	return r.BodyWordsDropped == 0 && len(r.PromptsTruncated) == 0 && len(r.LabelsDropped) == 0
}

// Trim applies every cap and reports what it removed.
//
// At the boundary rather than at each call site, so a caller that assembled forty
// prompts is corrected once instead of four times, and so the limit is enforced
// where it is documented — the same argument RankPayload.Trim makes.
func (p ClassifyPayload) Trim() (ClassifyPayload, TrimReport) {
	var rep TrimReport

	if words := countWords(p.Article.Body); words > MaxClassifyBodyWords {
		p.Article.Body = firstNWords(p.Article.Body, MaxClassifyBodyWords)
		rep.BodyWordsDropped = words - MaxClassifyBodyWords
	}

	p.Labels.Categories = trimPrompts(p.Labels.Categories, &rep)
	p.Labels.Tags = trimPrompts(p.Labels.Tags, &rep)

	// The block cap drops whole labels from the END, because the caller orders
	// them by priority and a half-described label is worse than an absent one —
	// the model would weigh it against fully-described neighbours and it would
	// lose for the wrong reason.
	for labelsRunes(p.Labels) > MaxLabelsBlockRunes {
		switch {
		case len(p.Labels.Tags) > 0:
			last := len(p.Labels.Tags) - 1
			rep.LabelsDropped = append(rep.LabelsDropped, p.Labels.Tags[last].Slug)
			p.Labels.Tags = p.Labels.Tags[:last]
		case len(p.Labels.Categories) > 0:
			last := len(p.Labels.Categories) - 1
			rep.LabelsDropped = append(rep.LabelsDropped, p.Labels.Categories[last].Slug)
			p.Labels.Categories = p.Labels.Categories[:last]
		default:
			// Nothing left to drop and still over: the remaining content is not
			// labels. Stop rather than loop.
			return p, rep
		}
	}
	return p, rep
}

func trimPrompts(hints []LabelHint, rep *TrimReport) []LabelHint {
	for i := range hints {
		if n := len([]rune(hints[i].Prompt)); n > MaxLabelPromptRunes {
			hints[i].Prompt = string([]rune(hints[i].Prompt)[:MaxLabelPromptRunes])
			rep.PromptsTruncated = append(rep.PromptsTruncated, hints[i].Slug)
		}
	}
	return hints
}

func labelsRunes(ls LabelSet) int {
	n := 0
	for _, h := range ls.Categories {
		n += len([]rune(h.Slug)) + len([]rune(h.Name)) + len([]rune(h.Prompt))
	}
	for _, h := range ls.Tags {
		n += len([]rune(h.Slug)) + len([]rune(h.Name)) + len([]rune(h.Prompt))
	}
	return n
}

func countWords(s string) int {
	return len(strings.Fields(s))
}

func firstNWords(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) <= n {
		return s
	}
	return strings.Join(fields[:n], " ")
}

// ClassifyKeys is the allowlist for a classification body.
//
// # A SECOND list rather than additions to EgressKeys
//
// The obvious move is to add `article`, `body` and the rest to `EgressKeys`, and
// it is wrong for the reason that list exists at all. `AuditEgress` is one global
// check, so widening it to admit `body` would make a body legal in a **rank**
// payload too — and the whole argument for types-as-enforcement (§18.8) is that
// the boundary should be impossible to widen by accident from somewhere else.
// One shared list means every future exception loosens every existing caller.
//
// So the interest layer keeps its own strict list, unchanged, and this is the
// named exception with a named scope. `TestEgressKeysWereNotWidened` asserts that
// `body` never appears in `EgressKeys`, which is the guard that makes the split
// real rather than a convention.
var ClassifyKeys = map[string]bool{
	"article":   true,
	"title":     true,
	"summary":   true,
	"source":    true,
	"body":      true,
	"labels":    true,
	"categories": true,
	"tags":      true,
	"slug":      true,
	"name":      true,
	"prompt":    true,
	"want":      true,
	"category":  true,
	"secondary": true,
}

// ForbiddenKeys are the ones no payload may ever carry, on any list.
//
// Enumerated rather than left as "anything not on the allowlist", because those
// are different claims and only this one survives a future exception: an
// allowlist is a statement about today's payloads, and this is a statement about
// the boundary. A test asserts that no allowlist in this package contains any of
// them, so a later amendment cannot quietly admit one.
var ForbiddenKeys = map[string]bool{
	"url": true, "feed_url": true, "link": true, "permalink": true,
	"user_id": true, "tenant_id": true, "user": true, "tenant": true, "username": true,
	"item_id": true, "id_hash": true,
	"note": true, "notes": true, "annotation": true,
	"read_at": true, "starred_at": true, "dwell": true, "dwell_ms": true,
	"engagement": true, "engagements": true, "history": true,
	"published_at": true, "timestamp": true, "fetched_at": true,
	"folder": true, "folder_name": true, "author": true,
}

// AuditClassify reports any key in a classification body that §27.4e does not
// permit.
func AuditClassify(body []byte) ([]string, error) {
	return auditAgainst(body, ClassifyKeys)
}

// auditAgainst is the shared walk. AuditEgress and AuditClassify differ only in
// their list, and having one walk means a fix to the traversal cannot apply to
// one boundary and not the other.
func auditAgainst(body []byte, allowed map[string]bool) ([]string, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, errNotJSON(err)
	}

	seen := map[string]bool{}
	var bad []string
	var walk func(any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			for k, child := range n {
				if !allowed[k] && !seen[k] {
					seen[k] = true
					bad = append(bad, k)
				}
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(v)
	return bad, nil
}
