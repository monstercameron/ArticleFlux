package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// ---------------------------------------------------------------------------
// Language
// ---------------------------------------------------------------------------

// LangEnglish is the only language the shipped lexicon covers.
const LangEnglish = "en"

// minLangWords is how much text the detector needs before it will answer.
//
// Below this a headline is as likely to be a proper noun as a sentence — "Nintendo
// Switch 2 Pro" contains no function words in any language — and a detector that
// answers anyway will say "not English" for a large fraction of perfectly ordinary
// tech headlines, which then never get categorised.
const minLangWords = 12

// englishRatio is the share of tokens that must be English function words.
//
// Six percent. Ordinary English prose runs 25-40% function words; a Romance or
// Germanic language shares a handful of short forms with English by coincidence
// and lands in the low single digits. The gap is wide, so the exact cut matters
// little — what matters is that a bar exists and that it is low enough not to
// reject terse, jargon-heavy English, which is most of this corpus.
const englishRatio = 0.06

// functionWords is a deliberately small, high-frequency English closed class.
//
// Hand-written here rather than borrowed from `textvec.stopwords`, and the
// difference is the point: that list is tuned to REMOVE noise from a topic
// vocabulary and includes feed furniture like "read" and "more". This one is
// evidence that a text IS English, so it holds only closed-class words no other
// major language uses in the same forms.
var functionWords = map[string]bool{
	"the": true, "and": true, "that": true, "have": true, "for": true,
	"not": true, "with": true, "you": true, "this": true, "but": true,
	"his": true, "from": true, "they": true, "she": true, "will": true,
	"one": true, "all": true, "would": true, "there": true, "their": true,
	"what": true, "out": true, "about": true, "who": true, "get": true,
	"which": true, "when": true, "make": true, "can": true, "like": true,
	"time": true, "just": true, "him": true, "know": true, "take": true,
	"into": true, "your": true, "some": true, "could": true, "them": true,
	"than": true, "then": true, "now": true, "only": true, "its": true,
	"over": true, "also": true, "back": true, "after": true, "how": true,
	"our": true, "well": true, "way": true, "even": true, "want": true,
	"because": true, "these": true, "give": true, "most": true, "should": true,
}

// langAnalyzer decides whether an item is English.
//
// # Why a heuristic and not a library
//
// The only decision that depends on this is whether to run an English lexicon,
// and the cost of the two errors is wildly asymmetric. Calling English text
// "unknown" costs one uncategorised article. Calling German text English costs a
// German article filed under a category chosen by whichever English words it
// happened to share — which is a confidently wrong chip, the thing R23 is about.
//
// So this answers "en" or "", never "de" or "fr". A proper detector would let it
// name the language, and naming the language is not a feature anything here has
// asked for. §27.13 records the multi-language lexicon as explicitly out of scope;
// refusing is the correct behaviour for now and this is the smallest thing that
// refuses correctly.
type langAnalyzer struct{}

func (langAnalyzer) Name() string { return "lang" }
func (langAnalyzer) Version() int { return 1 }

func (langAnalyzer) Analyze(_ context.Context, b *Batch) error {
	for i := range b.Items {
		it := b.Items[i]
		// Title and summary rather than the body: they are always present, they
		// are prose, and the body may be a code listing or a table, which has no
		// function words in any language.
		text := it.Title + ". " + it.Summary
		if len(text) < 24 {
			text += ". " + firstWords(it.Body, 60)
		}

		toks := textvec.Scan(text)
		if len(toks) < minLangWords {
			// Not enough evidence. Empty rather than a guess, and the category
			// analyzer treats empty as "go ahead" — see its comment for why that
			// asymmetry is deliberate.
			continue
		}
		hits := 0
		for _, t := range toks {
			if functionWords[t.Text] {
				hits++
			}
		}
		if float64(hits)/float64(len(toks)) >= englishRatio {
			b.Out[i].Lang = LangEnglish
		} else {
			b.Out[i].Lang = "und"
		}
	}
	return nil
}

func firstWords(s string, n int) string {
	if s == "" {
		return ""
	}
	count := 0
	for i, r := range s {
		if r == ' ' || r == '\n' || r == '\t' {
			count++
			if count >= n {
				return s[:i]
			}
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// Categories
// ---------------------------------------------------------------------------

// categoryAnalyzer scores the default taxonomy.
type categoryAnalyzer struct {
	lexicon  *classify.Lexicon
	strategy classify.Strategy
}

func (categoryAnalyzer) Name() string { return "category" }
func (categoryAnalyzer) Version() int { return 1 }

func (a categoryAnalyzer) Analyze(_ context.Context, b *Batch) error {
	if a.lexicon == nil {
		return fmt.Errorf("no lexicon")
	}
	for i := range b.Items {
		// "und" is a positive statement that this is not English, and the shipped
		// lexicon is English-only (§27.13). "" is the detector declining to
		// answer, and those go through — a twelve-word headline is the single
		// most common shape in a feed and refusing to categorise it would gut the
		// feature to avoid an error the lexicon's own guards already handle.
		if b.Out[i].Lang != "" && b.Out[i].Lang != LangEnglish {
			continue
		}
		it := b.Items[i]
		r := a.lexicon.Score(classify.Item{
			Title:       it.Title,
			URL:         it.URL,
			Summary:     it.Summary,
			SourceTitle: it.SourceTitle,
			Body:        it.Body,
		}, a.strategy)

		if len(r.Scores) > 0 {
			scores := make(map[string]float64, len(r.Scores))
			for _, s := range r.Scores {
				scores[s.Slug] = s.Value
			}
			b.Out[i].CategoryScores = scores
		}
		b.Out[i].Primary = r.Primary
		b.Out[i].Secondary = r.Secondary
		b.Out[i].Ambiguous = r.Ambiguous
	}
	return nil
}

// ---------------------------------------------------------------------------
// Genre
// ---------------------------------------------------------------------------

// Genres are §27.1a's article forms.
const (
	GenreNews         = "news"
	GenreAnalysis     = "analysis"
	GenreOpinion      = "opinion"
	GenreTutorial     = "tutorial"
	GenreRelease      = "release"
	GenreReview       = "review"
	GenreInterview    = "interview"
	GenreRoundup      = "roundup"
	GenreResearch     = "research"
	GenreAnnouncement = "announcement"
)

// genreMarkers are ordered: the FIRST match wins, most specific first.
//
// Order rather than scoring, because these are near-disjoint in practice and a
// scored version would need weights nobody has evidence for. The one real overlap
// — a review IS an opinion — is resolved by putting review first, which is what a
// reader would call it.
var genreMarkers = []struct {
	genre   string
	phrases []string
}{
	{GenreRelease, []string{"release notes", "patch notes", "changelog", "now available",
		"is out", "ships with", "general availability", "release candidate", "stable release"}},
	{GenreTutorial, []string{"how to", "a guide to", "getting started", "step by step",
		"tutorial", "walkthrough", "build your own", "from scratch"}},
	{GenreInterview, []string{"an interview with", "interview:", "in conversation with",
		"we spoke to", "q&a with", "talks about"}},
	{GenreReview, []string{"review:", "reviewed", "hands on", "first impressions",
		"long term review", "we tested", "put to the test"}},
	{GenreRoundup, []string{"this week in", "weekly roundup", "roundup", "link roundup",
		"best of", "what we read", "issue #", "newsletter"}},
	{GenreResearch, []string{"arxiv", "preprint", "peer reviewed", "a new study",
		"study finds", "researchers found", "published in nature", "the paper"}},
	{GenreOpinion, []string{"opinion", "why i", "why we", "the case for", "the case against",
		"is wrong", "we need to talk about", "an essay", "rant"}},
	{GenreAnalysis, []string{"what it means", "explained", "deep dive", "a closer look",
		"analysis", "why it matters", "the real reason", "breaking down"}},
	{GenreAnnouncement, []string{"we are excited", "announcing", "introducing",
		"joins", "acquires", "welcome to"}},
}

// genreAnalyzer labels an article's form from its headline.
//
// # A weak default, and it says so
//
// Headline phrasing is the only free signal for form, and it is genuinely weak:
// plenty of tutorials never say "how to" and plenty of news headlines contain
// "explained". This is here because the column has to be populated from day one
// for the history to exist when a feature wants it (§27.1a), and because a weak
// deterministic answer is a better floor than an empty one — Smart+ overwrites it
// when it runs, and the row records which answered.
//
// It reads the TITLE and the URL slug, never the body. The signal is entirely in
// how the piece announced itself.
type genreAnalyzer struct{}

func (genreAnalyzer) Name() string { return "genre" }
func (genreAnalyzer) Version() int { return 1 }

func (genreAnalyzer) Analyze(_ context.Context, b *Batch) error {
	for i := range b.Items {
		subject := strings.ToLower(b.Items[i].Title + " " + slugWords(b.Items[i].URL))
		b.Out[i].Genre = GenreNews
		for _, m := range genreMarkers {
			if containsAny(subject, m.phrases) {
				b.Out[i].Genre = m.genre
				break
			}
		}
	}
	return nil
}

func containsAny(s string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// slugWords turns a URL path into spaced words so "how-to-build" matches the
// phrase "how to".
func slugWords(u string) string {
	if u == "" {
		return ""
	}
	repl := strings.NewReplacer("/", " ", "-", " ", "_", " ", ".", " ")
	return repl.Replace(u)
}

// ---------------------------------------------------------------------------
// Key phrases
// ---------------------------------------------------------------------------

// MaxKeyphrases bounds what is stored per item.
//
// Twelve. These are shown to a reader in the "why" line and are matched against a
// user's custom label definitions (§27.4d), and both uses degrade past a dozen —
// a list long enough to contain everything describes nothing.
const MaxKeyphrases = 12

type keyphraseAnalyzer struct{}

func (keyphraseAnalyzer) Name() string { return "keyphrase" }
func (keyphraseAnalyzer) Version() int { return 1 }

// Analyze takes the heaviest terms of the item's own text.
//
// Term frequency, not TF-IDF, for the reason given on vectorAnalyzer: there is no
// collection here to compute a document frequency against. That makes these the
// item's most REPEATED meaningful terms rather than its most DISTINCTIVE ones,
// which is a weaker claim and an honest one — and it is still the right input for
// matching a user's label definition, which is asking "does this article talk
// about X" rather than "is X unusual".
func (keyphraseAnalyzer) Analyze(_ context.Context, b *Batch) error {
	for i := range b.Items {
		it := b.Items[i]
		text := it.Title + ". " + it.Summary + ". " + it.Body
		v := textvec.TermFreq(text)
		if len(v) == 0 {
			continue
		}
		phrases := v.TopN(MaxKeyphrases)
		if len(phrases) > 0 {
			b.Out[i].Keyphrases = phrases
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

// MaxEntities bounds what one item contributes.
const MaxEntities = 10

// entityAnalyzer names the brands, products and organisations in an item.
//
// # This is the free tier of something that used to be paid
//
// `internal/derive` extracts entities only on the Smart+ path and only over the
// titles of items a reader ENGAGED with, which is a small and biased sample —
// `entity_affinity` (0019) has never seen an article nobody opened. Running it
// here gives that table every item, once, with no key configured, which is what
// makes §27.3e's "you have seen Ollama 14 times, make it a tag?" possible at all.
//
// `textvec.Phrases` is the extractor and its limitations are its own: capitalised
// pairs only, so a lowercase name like `npm` or `ffmpeg` produces nothing, and it
// returns nothing at all for a Title Case headline where capitalisation carries no
// signal. Both are documented there and both degrade rather than misfire, which is
// the right direction for a signal that accumulates.
type entityAnalyzer struct{}

func (entityAnalyzer) Name() string { return "entity" }
func (entityAnalyzer) Version() int { return 1 }

func (entityAnalyzer) Analyze(_ context.Context, b *Batch) error {
	for i := range b.Items {
		it := b.Items[i]
		// The title and the summary. Not the body: a capitalised pair in running
		// prose is far more often a sentence opening than a name, and the measured
		// cost of that on a real corpus was entries like "Continue Reading" and
		// "Depth Review" presented to a reader as brands they follow.
		// Title and summary are passed SEPARATELY, not concatenated.
		//
		// `textvec.Phrases` refuses to answer for text written in Title Case,
		// because capitalisation there is a house style rather than a statement
		// about particular words. That check is a ratio over the whole string, so
		// concatenating a Title Case headline with a sentence-case summary
		// averages the two and defeats it in one direction while a short
		// name-dense headline glued to a long summary can trip it in the other.
		// Each field has its own capitalisation convention and is judged on it.
		seen := make(map[string]bool, MaxEntities)
		var out []Entity
		for _, field := range [2]string{it.Title, it.Summary} {
			for _, p := range textvec.Phrases(field) {
				key := strings.ToLower(strings.Join(strings.Fields(p), " "))
				if key == "" || seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, Entity{Name: key, Label: titleWords(key)})
				if len(out) == MaxEntities {
					break
				}
			}
			if len(out) == MaxEntities {
				break
			}
		}
		b.Out[i].Entities = out
	}
	return nil
}

// titleWords is the display form for an entity whose original casing the
// tokenizer discarded.
//
// It is a guess and it is a lossy one — "npm" becomes "Npm" — which is why the
// Smart+ extractor is asked for the display form directly (`derive.NamedEntity`
// carries both). Here there is nothing better available, and showing the
// lowercase key would look like a bug rather than like a limitation.
func titleWords(s string) string {
	parts := strings.Fields(s)
	for i, p := range parts {
		r := []rune(p)
		parts[i] = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	return strings.Join(parts, " ")
}

// ---------------------------------------------------------------------------
// Vector
// ---------------------------------------------------------------------------

// MinVectorWeight prunes the tail before storage.
//
// The vector is the largest column on the row and its tail is terms that appeared
// once in a four-thousand-word article. `textvec.Vector.Prune` exists for exactly
// this and the interest layer already applies the same idea through `MaxTerms`.
const MinVectorWeight = 0.01

// vectorAnalyzer stores the item's term-frequency vector.
//
// # TF, not TF-IDF — a correction to §27.7 as first written
//
// The spec said this column would hold "the TF-IDF vector derive stops
// recomputing". It cannot, and the reason is not an implementation detail:
//
//	**TF is a property of the document. IDF is a property of the collection.**
//
// There is no collection here. This runs once per item, globally, before anyone
// has read it — while `internal/derive` computes IDF over one reader's engaged
// items in a ninety-day window, which is a different corpus per user and per day.
// Freezing an IDF at ingest would mean every reader scoring against document
// frequencies taken from whatever else happened to be in that poll's batch, and
// the numbers would drift as the batch composition changed rather than as
// anyone's interests did.
//
// So this stores TF and `derive` applies its own IDF at derivation time. That
// keeps derive's semantics **exactly** as they are today while still removing the
// expensive half from the hot path: tokenising and counting a four-thousand-word
// article is the cost, and looking up a document frequency per term is not.
//
// The saving is real and it is the payoff A41 was written for — a derivation fires
// after every poll and after every batch of engagements, and each one currently
// re-tokenises every engaged item from raw text.
type vectorAnalyzer struct{}

func (vectorAnalyzer) Name() string { return "vector" }
func (vectorAnalyzer) Version() int { return 1 }

func (vectorAnalyzer) Analyze(_ context.Context, b *Batch) error {
	for i := range b.Items {
		it := b.Items[i]
		v := textvec.TermFreq(it.Title + ". " + it.Summary + ". " + it.Body)
		if len(v) == 0 {
			continue
		}
		v.Prune(MinVectorWeight)
		if len(v) == 0 {
			continue
		}
		b.Out[i].Vector = v
	}
	return nil
}

// ---------------------------------------------------------------------------
// Lexicon hash
// ---------------------------------------------------------------------------

// LexiconHash fingerprints a compiled lexicon.
//
// Every input that can change a score is in it: slug, per-label floor, and every
// term's text, weight, guards and regex flag — positive and negative alike. A
// display name is deliberately NOT, because renaming "Film & TV" must not
// re-analyse a database.
//
// Sorted before hashing, so the hash describes the lexicon rather than the order
// somebody wrote the files in. Without that, moving a category up in
// `Categories()` would invalidate every row in the database and nobody would know
// why.
func LexiconHash(lx *classify.Lexicon) string {
	if lx == nil {
		return ""
	}
	labels := lx.Labels()
	lines := make([]string, 0, len(labels))
	for _, l := range labels {
		var b strings.Builder
		fmt.Fprintf(&b, "%s|%g|", l.Slug, l.MinScore)
		writeTerms(&b, "+", l.Terms)
		writeTerms(&b, "-", l.Exclude)
		lines = append(lines, b.String())
	}
	sort.Strings(lines)

	h := sha256.New()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	// Twelve hex characters. This is a change DETECTOR, not a credential: it is
	// compared for equality against itself, so collision resistance beyond
	// "different lexicons differ" buys nothing and a 64-character string is
	// repeated on every row and in every log line.
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func writeTerms(b *strings.Builder, sign string, terms []classify.Term) {
	out := make([]string, 0, len(terms))
	for _, t := range terms {
		w := t.Weight
		if w == 0 {
			w = 1
		}
		guards := append([]string(nil), t.Requires...)
		sort.Strings(guards)
		out = append(out, fmt.Sprintf("%s%s:%g:%v:%s", sign, t.Text, w, t.Regex,
			strings.Join(guards, ",")))
	}
	sort.Strings(out)
	b.WriteString(strings.Join(out, ";"))
	b.WriteString("|")
}
