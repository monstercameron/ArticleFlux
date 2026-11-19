package smart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Article translation on demand (plan.md §10.5b).
//
// A reader whose language is not the article's asks for it in theirs. What this
// file guarantees, in the order they would otherwise go wrong:
//
//  1. **Nothing leaves without consent.** A key, AND the owner's
//     `smart.translate_articles`, which defaults off — the §27.4e shape. A
//     cached translation needs neither: serving it sends nothing and costs
//     nothing.
//  2. **What leaves is text.** The article is sanitised, parsed, and its TEXT
//     NODES are sent as numbered segments (llm.TranslatePayload). No tag, no
//     attribute, no href or src — so no link target or tracking parameter can
//     leave, and nothing the model writes is ever parsed as markup: each answer
//     is put back into the text node it came from, which the HTML renderer
//     escapes. The structure — paragraphs, headings, lists, links, images — is
//     the original's by construction, and the result is sanitised again anyway.
//  3. **A reader sees only what they could already see.** The item is read
//     through the caller's Scope (store.GetItem), and the cache is read and
//     written through it too; the cache row itself records nothing about who
//     asked (migration 0036).
//  4. **Each article is paid for once per language per version.** Cached per
//     (item, language, hash of the sanitised source): a publisher's correction
//     changes the hash and the next request re-translates; nothing else does.

// KeyTranslateArticles is the owner's consent for sending article text to be
// translated. Absent or anything but "true" is off.
const KeyTranslateArticles store.SystemKey = "smart.translate_articles"

var (
	// ErrTranslateOff means the owner has not consented to article translation.
	ErrTranslateOff = errors.New("smart: article translation is not enabled on this instance")
	// ErrSameLanguage means the article is already in the language asked for.
	ErrSameLanguage = errors.New("smart: the article is already in that language")
	// ErrNothingToTranslate means the article has no text to translate.
	ErrNothingToTranslate = errors.New("smart: the article has no text to translate")
)

// articleBatchRunes bounds one request's segments. Sized against the output
// ceiling (articleMaxTokens) with room for a language that runs long and for
// the JSON around each segment.
const (
	articleBatchRunes    = 6000
	articleBatchSegments = 80
	articleMaxTokens     = 8000
)

// ArticleTranslator translates articles for readers.
type ArticleTranslator struct {
	llm      llmClient
	settings *store.SettingsRepo
	repo     *store.ReaderRepo

	// inflight makes a double-click one request, not two bills.
	mu       sync.Mutex
	inflight map[string]*sync.Mutex

	// eng is the translation engine; nil means opsEngine over llm.
	eng translationEngine
}

// NewArticleTranslator wires article translation.
func NewArticleTranslator(c llmClient, s *store.SettingsRepo, repo *store.ReaderRepo) *ArticleTranslator {
	return &ArticleTranslator{llm: c, settings: s, repo: repo, inflight: map[string]*sync.Mutex{}}
}

// Enabled reports the owner's consent. It says nothing about the key.
func (a *ArticleTranslator) Enabled(ctx context.Context) bool {
	if a == nil || a.settings == nil {
		return false
	}
	v, err := a.settings.SystemValue(ctx, KeyTranslateArticles)
	return err == nil && strings.TrimSpace(v) == "true"
}

// ArticleTranslation is one article in the reader's language, with its
// original.
type ArticleTranslation struct {
	ItemID, Locale, SourceLocale string
	Title, BodyHTML              string
	OriginalTitle, OriginalHTML  string
	Cached, Partial              bool
}

// TargetLocale resolves a locale the reader may ask for an article in: the
// source catalog's language or any offered UI language.
func TargetLocale(code string) (Language, bool) {
	code = strings.TrimSpace(code)
	if strings.EqualFold(code, i18n.DefaultLocale) {
		return Language{Code: i18n.DefaultLocale, English: "English", Native: "English"}, true
	}
	return LanguageByCode(code)
}

// Cached returns the item's cached translation into locale when one exists for
// the article as it stands, and ok=false otherwise. It never calls the model,
// so the transport can serve it before any rate limit: re-reading something
// already paid for is not the spend the limit is for.
func (a *ArticleTranslator) Cached(ctx context.Context, sc store.Scope, itemID, locale string) (
	ArticleTranslation, bool, error) {
	lang, ok := TargetLocale(locale)
	if !ok {
		return ArticleTranslation{}, false, ErrUnsupportedLanguage
	}
	out, hash, err := a.original(ctx, sc, itemID, lang)
	if err != nil {
		return ArticleTranslation{}, false, err
	}
	hit, err := a.repo.ItemTranslation(ctx, sc, out.ItemID, lang.Code)
	if err != nil || hit.SourceHash != hash {
		return out, false, nil
	}
	out.Title, out.BodyHTML, out.SourceLocale, out.Cached = hit.Title, hit.BodyHTML, hit.SourceLang, true
	return out, true, nil
}

// original reads the item through the caller's scope — the whole of the access
// check: an item the reader cannot see is ErrNotFound here, before anything
// else — and returns its sanitised original and that original's hash.
func (a *ArticleTranslator) original(ctx context.Context, sc store.Scope, itemID string, lang Language) (
	ArticleTranslation, string, error) {
	it, err := a.repo.GetItem(ctx, sc, itemID)
	if err != nil {
		return ArticleTranslation{}, "", err
	}
	origHTML := sanitize.HTML(it.ContentHTML, sanitize.Feed)
	origTitle := strings.TrimSpace(it.Title)
	return ArticleTranslation{ItemID: it.ID, Locale: lang.Code,
		OriginalTitle: origTitle, OriginalHTML: origHTML}, articleHash(origTitle, origHTML), nil
}

// Translate returns the item in locale, from cache when it can.
func (a *ArticleTranslator) Translate(ctx context.Context, sc store.Scope, itemID, locale string,
	force bool) (ArticleTranslation, error) {

	lang, ok := TargetLocale(locale)
	if !ok {
		return ArticleTranslation{}, ErrUnsupportedLanguage
	}
	out, hash, err := a.original(ctx, sc, itemID, lang)
	if err != nil {
		return ArticleTranslation{}, err
	}
	origTitle, origHTML := out.OriginalTitle, out.OriginalHTML
	id := out.ItemID

	key := id + "\x00" + lang.Code
	lock := a.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	if !force {
		if hit, err := a.repo.ItemTranslation(ctx, sc, id, lang.Code); err == nil && hit.SourceHash == hash {
			out.Title, out.BodyHTML, out.SourceLocale, out.Cached = hit.Title, hit.BodyHTML, hit.SourceLang, true
			return out, nil
		}
	}

	declared, detected, _ := a.repo.ItemLanguage(ctx, sc, id)
	source := sourceLanguage(declared, detected)
	if source != "" && primary(source) == primary(lang.Code) {
		return ArticleTranslation{}, ErrSameLanguage
	}
	out.SourceLocale = source

	// The two gates, in the order that produces the useful message.
	if a.llm == nil || !a.llm.Configured(ctx) {
		return ArticleTranslation{}, llm.ErrNotConfigured
	}
	if !a.Enabled(ctx) {
		return ArticleTranslation{}, ErrTranslateOff
	}

	doc, segs, partial, err := segmentArticle(origHTML)
	if err != nil {
		return ArticleTranslation{}, err
	}
	if len(segs) == 0 && origTitle == "" {
		return ArticleTranslation{}, ErrNothingToTranslate
	}

	title, err := a.translateSegments(ctx, lang, origTitle, segs)
	if err != nil {
		return ArticleTranslation{}, err
	}
	var b strings.Builder
	for c := doc.FirstChild; c != nil; c = c.NextSibling {
		if err := html.Render(&b, c); err != nil {
			return ArticleTranslation{}, err
		}
	}
	// Sanitised again. The text was escaped by Render and the structure is the
	// sanitised original's, so this should change nothing — which is exactly
	// what makes it the right last step rather than a redundant one.
	out.BodyHTML = sanitize.HTML(b.String(), sanitize.Feed)
	out.Title = title
	out.Partial = partial

	_ = a.repo.PutItemTranslation(ctx, sc, store.ItemTranslation{
		ItemID: id, Lang: lang.Code, SourceLang: source, SourceHash: hash,
		Title: out.Title, BodyHTML: out.BodyHTML, Model: a.llm.OpsModel(ctx),
	})
	return out, nil
}

func (a *ArticleTranslator) lockFor(key string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	m, ok := a.inflight[key]
	if !ok {
		m = &sync.Mutex{}
		a.inflight[key] = m
	}
	return m
}

// articleHash fingerprints what a translation was made from.
func articleHash(title, body string) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte{0})
	h.Write([]byte(body))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// sourceLanguage picks the cheapest trustworthy answer to "what is this written
// in". The feed's declared language first — the publisher said so — then the
// analyzer's, which can only say "en" or that it is not English ("und"), and
// "und" is not a language to compare against.
func sourceLanguage(declared, detected string) string {
	if d := strings.TrimSpace(declared); d != "" {
		return d
	}
	if strings.EqualFold(strings.TrimSpace(detected), "en") {
		return "en"
	}
	return ""
}

func primary(code string) string {
	p, _, _ := strings.Cut(strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "_", "-")), "-")
	return p
}

// segment is one text node and where its answer goes.
type segment struct {
	node       *html.Node
	lead, tail string // whitespace the model will not preserve
	text       string
}

// skipText are elements whose text is not prose: translating code changes what
// it does, and the others are not rendered at all.
var skipText = map[atom.Atom]bool{
	atom.Pre: true, atom.Code: true, atom.Kbd: true, atom.Samp: true, atom.Var: true,
	atom.Script: true, atom.Style: true, atom.Template: true, atom.Noscript: true,
}

// segmentArticle parses sanitised HTML and returns its translatable text nodes
// in document order, up to llm.MaxTranslateRunes.
func segmentArticle(clean string) (*html.Node, []segment, bool, error) {
	ctx := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	nodes, err := html.ParseFragment(strings.NewReader(clean), ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("smart: parsing the article: %w", err)
	}
	root := &html.Node{Type: html.ElementNode, DataAtom: atom.Div, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var segs []segment
	budget := llm.MaxTranslateRunes
	partial := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && skipText[n.DataAtom] {
			return
		}
		if n.Type == html.TextNode && hasWords(n.Data) {
			trimmed := strings.TrimSpace(n.Data)
			r := utf8.RuneCountInString(trimmed)
			if r > budget {
				partial = true
				return
			}
			budget -= r
			lead := n.Data[:strings.Index(n.Data, trimmed)]
			tail := n.Data[len(lead)+len(trimmed):]
			segs = append(segs, segment{node: n, lead: lead, tail: tail, text: trimmed})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return root, segs, partial, nil
}

// hasWords reports text worth sending: at least one letter. A run of digits,
// punctuation or whitespace is the same in every language.
func hasWords(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// articleReply is one batch's answer, keyed by segment ordinal so a dropped or
// reordered segment is caught rather than shifting every later one.
type articleReply struct {
	Title    *string `json:"title"`
	Segments []struct {
		ID   int     `json:"id"`
		Text *string `json:"text"`
	} `json:"segments"`
}

const articleInstructions = `You are translating a news or blog article for a reader of a feed reader.

You will be given a JSON object: "target" is the language to translate into, "title" (when present) is the article's headline, and "segments" are the article's text in document order, each with an "id". Segments are pieces of the same text — a sentence may be split across several where the original had a link or emphasis — so translate them so that, joined in order, they read naturally.

Rules:
1. Return every segment id you were given, with its translation. Never merge, split, drop, reorder or invent segments.
2. Plain text only. No HTML, no Markdown, no notes or explanations. Your text is shown as text.
3. Keep names, numbers, code, and URLs as they are.
4. If a segment is already in the target language, return it unchanged.
5. Translate the title when one is given; otherwise leave it out.`

// translateSegments sends the segments in batches and writes each answer into
// its node. The title rides with the first batch. A batch that fails fails the
// whole translation — a half-translated article cached as a translation would
// be served as one.
func (a *ArticleTranslator) translateSegments(ctx context.Context, lang Language, title string,
	segs []segment) (string, error) {

	target := lang.Code + " (" + lang.English + ")"
	outTitle := title
	start := 0
	first := true
	for first || start < len(segs) {
		end, runes := start, 0
		for end < len(segs) && end-start < articleBatchSegments &&
			(runes == 0 || runes+utf8.RuneCountInString(segs[end].text) <= articleBatchRunes) {
			runes += utf8.RuneCountInString(segs[end].text)
			end++
		}
		p := llm.TranslatePayload{Target: target}
		if first {
			p.Title = title
		}
		for i := start; i < end; i++ {
			p.Segments = append(p.Segments, llm.TranslateSegment{ID: i - start, Text: segs[i].text})
		}
		eng := translationEngine(opsEngine{llm: a.llm})
		if a.eng != nil {
			eng = a.eng
		}
		gotTitle, answers, err := eng.ArticleBatch(ctx, p)
		if err != nil {
			return "", err
		}
		for id, text := range answers {
			i := start + id
			if id < 0 || i >= end {
				continue
			}
			v := strings.TrimSpace(text)
			// A segment the model emptied, or answered with a runaway, keeps
			// the original words: a sentence left in German is a smaller defect
			// than a sentence missing from the article.
			if v == "" || utf8.RuneCountInString(v) > 4*utf8.RuneCountInString(segs[i].text)+40 {
				continue
			}
			segs[i].node.Data = segs[i].lead + v + segs[i].tail
		}
		if first {
			if v := strings.TrimSpace(gotTitle); v != "" && title != "" &&
				utf8.RuneCountInString(v) <= 4*utf8.RuneCountInString(title)+40 {
				outTitle = v
			}
		}
		first = false
		start = end
	}
	return outTitle, nil
}
