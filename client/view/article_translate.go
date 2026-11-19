//go:build js && wasm

package view

// Translating one article into the reader's language (plan.md §10.5b): the
// per-article state, the control in the article's action row, the line that
// says what the reader is looking at, and the one transformation the
// translated HTML needs before it can go through the ordinary body path.

import (
	"strings"

	"github.com/monstercameron/GoWebComponents/v7/html"
	"github.com/monstercameron/GoWebComponents/v7/ui"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// actTranslate is the article's Translate / Show original control. It names
// the article in data-for-item, like every other per-article control.
const actTranslate = "translate"

// The states one article's translation can be in. Absent from the map is
// "never asked", which renders as the plain Translate control.
const (
	trBusy   = "busy"
	trDone   = "done"
	trFailed = "failed"
	// trSame is the server saying the article is already in the reader's
	// language. Not a failure, and not something to offer again.
	trSame = "same"
)

// articleTranslation is one article's translation, as plain data — it rides on
// articleProps, whose memo bailout a func or an interface would defeat.
type articleTranslation struct {
	state string
	// original is the toggle: true shows the article as published even though
	// a translation is on hand. Switching back costs nothing, because the
	// translation is kept.
	original bool
	// locale is the language this is IN. A translation into French says nothing
	// about the article once the interface is German, so it is only shown while
	// this matches the language in force.
	locale string
	// source is what the server detected the original as, "" when it could not
	// tell.
	source string
	title  string
	html   string
	// partial is an article longer than one translation covers: the tail stays
	// in the original language, and the reader is told so rather than left to
	// find the language change halfway down.
	partial bool
	// err is the failure in the reader's language (serverText).
	err string
}

// consentOf reads the instance's article-translation consent off a Smart+
// config, in trConsent's terms: 1 when readers may translate, -1 when they
// may not. A consent with no key behind it is -1: every fresh translation
// would be refused for want of one, and a control that can only fail is
// worse than none.
func consentOf(cfg *pb.GetSmartConfigResponse) int {
	if cfg.GetTranslateArticlesEnabled() && cfg.GetConfigured() {
		return 1
	}
	return -1
}

// consentFromLanguages is consentOf for a reader who cannot read the config:
// ListLanguages carries the same answer — consent AND a key, already combined
// by the server — on a response any signed-in reader may have. So a member
// learns it the way an owner does, before the first article, instead of by
// pressing Translate into a refusal.
func consentFromLanguages(res *pb.ListLanguagesResponse) int {
	if res.GetTranslateArticlesEnabled() {
		return 1
	}
	return -1
}

// cachedTranslation files a cache-only lookup's hit (data.CachedArticleTranslation)
// as a translation on hand, showing the ORIGINAL: the reader did not ask for
// it, so it is offered — the Translate control appears, and pressing it is
// free, because the translation is already here — rather than swapped in
// under them. shown is the body as GetItem served it, for alignSubresources.
func cachedTranslation(res *pb.TranslateArticleResponse, locale, shown string) articleTranslation {
	return articleTranslation{
		state: trDone, original: true, locale: locale,
		source: res.GetSourceLocale(), title: res.GetTitle(), partial: res.GetPartial(),
		html: alignSubresources(res.GetContentHtml(), res.GetOriginalContentHtml(), shown),
	}
}

// translationRefused reports a refusal that is about the INSTANCE rather than
// this article: translation is switched off, or the server predates it.
// Either way no other article will fare differently, so the control stops
// being offered.
func translationRefused(err error) bool {
	return serverKey(err) == "srv.articleTranslateOff" || status.Code(err) == codes.Unimplemented
}

// translationFor returns an article's translation when it is about the
// language in force, and the zero value otherwise.
func translationFor(p articleProps, id string) articleTranslation {
	t, ok := p.translations[id]
	if !ok || t.locale != p.locale {
		return articleTranslation{}
	}
	return t
}

// showingTranslation reports whether an article is being shown translated.
func showingTranslation(t articleTranslation) bool {
	return t.state == trDone && !t.original && strings.TrimSpace(t.html) != ""
}

// translateChip is the control, or nil where there is nothing to offer.
//
// Offered when the instance can translate articles (p.translateOK) or when a
// translation is already on hand — a cached one is readable whatever the
// owner's consent says now. Not offered once the server has said the article
// is already in this language: the button would be a promise with nothing
// behind it.
//
// The label is what pressing it WILL do, with no aria-pressed, for
// kitChipProps' reason: "Translate, toggle button, pressed" over an
// article in French is a switch nobody can read the state of. data-state
// carries the state for the stylesheet and the specs.
func translateChip(tr i18n.Runtime, id string, p articleProps) ui.Node {
	t := translationFor(p, id)
	if t.state == trSame || (!p.translateOK && t.state != trDone) {
		return nil
	}
	state, label := "idle", tr.T("article", "translate")
	props := html.Props{Class: "chip article-translate"}
	switch {
	case t.state == trBusy:
		state, label = trBusy, tr.T("article", "translating")
		// Disabled while in flight: a second press is a second bill for the
		// same answer.
		props.Disabled = true
		props.Aria = map[string]string{"busy": "true"}
	case showingTranslation(t):
		state, label = "translated", tr.T("article", "showOriginal")
	case t.state == trDone:
		state = "original"
	case t.state == trFailed:
		state = trFailed
	}
	props.DataAttrs = []html.DataAttribute{
		{Name: "action", Value: actTranslate}, {Name: "for-item", Value: id}, {Name: "state", Value: state},
	}
	return html.Button(props, lead(glyphTranslate), html.Text(label))
}

// translateNote says what the reader is looking at, under the action row.
//
// One live region per article, present only once a translation has been asked
// for — so an untranslated stream carries nothing extra — and kept for the
// rest of the session, because a region that is created at the moment it gains
// content is one some screen readers never announce.
func translateNote(tr i18n.Runtime, id string, p articleProps) ui.Node {
	if _, asked := p.translations[id]; !asked {
		return nil
	}
	t := translationFor(p, id)
	var kids []ui.Node
	bad := false
	switch {
	case t.state == trFailed:
		bad = true
		kids = append(kids, html.Text(tr.T("article", "translateFailed", i18n.Args{"err": t.err})))
	case t.state == trSame:
		kids = append(kids, html.Text(tr.T("article", "translateSame")))
	case showingTranslation(t):
		kids = append(kids, html.Span(html.Props{}, html.Text(translatedFrom(tr, t.source))))
		if t.partial {
			kids = append(kids, html.Span(html.Props{Class: "article-tr-partial"},
				html.Text(tr.T("article", "translatedPartial"))))
		}
	}
	return html.Div(html.Props{
		Class: "article-trnote",
		Key:   "trnote-" + id,
		Role:  "status",
		Aria:  map[string]string{"live": "polite"},
		Data:  map[string]string{"bad": boolAttr(bad), "for-article": id},
	}, kids...)
}

// translatedFrom names the language the article was translated FROM, in the
// language the reader is reading in where the catalog can say it: the English
// name in English, the endonym otherwise (the picker's own rule — the reader
// can read their own language, and an endonym is at least the word the
// publisher would use).
func translatedFrom(tr i18n.Runtime, source string) string {
	name := ""
	switch {
	case source == "":
	case strings.EqualFold(source, i18n.DefaultLocale) || strings.HasPrefix(strings.ToLower(source), "en-"):
		name = tr.T("smart", "langEnglish")
	default:
		if l, ok := i18n.LanguageByCode(source); ok {
			name = l.Native
			if tr.Locale() == i18n.DefaultLocale {
				name = l.English
			}
		} else {
			name = source
		}
	}
	if name == "" {
		return tr.T("article", "translatedUnknown")
	}
	return tr.T("article", "translatedFrom", i18n.Args{"language": name})
}

// bodyKey is the parsed-body cache's key for one rendering of one article.
//
// The original keeps the bare item id, which is what every other caller of
// the cache has always used. A translation gets the id AND its locale, after a
// separator no ULID can contain: keyed by id alone, the cache — which only
// re-parses when the length changes — would hand a translated article the
// original's nodes whenever the two happened to be the same length, and a
// French and a German translation of one article would overwrite each other.
func bodyKey(id, locale string) string {
	if locale == "" {
		return id
	}
	return id + "\x00tr:" + locale
}

// fetches reports the attributes that make the browser FETCH something, per
// element — the same table internal/rewrite repoints at the local proxy
// (§10.1a), which is what this has to agree with.
func fetches(tag, attr string) bool {
	switch attr {
	case "src":
		return tag == "img" || tag == "source" || tag == "video" || tag == "audio" ||
			tag == "track" || tag == "input"
	case "srcset":
		return tag == "img" || tag == "source"
	case "poster":
		return tag == "video"
	}
	return false
}

// attrSpan is one fetchable attribute in a document: where the whole
// `name="value"` sits, so it can be dropped, and where the value sits, so it
// can be replaced.
type attrSpan struct {
	name, val                string // the attribute, and its value unescaped
	start, end, vStart, vEnd int
}

// scanSubresources finds the fetchable attributes in a document, in order.
//
// Hand-rolled, and small on purpose, because everything here is paid for on
// the wasm ratchet (R4): a tokenizer would be the general answer to a
// question with six attributes in it. The input is the server's SANITISED
// markup — quoted attributes, no scripts, no CDATA — and the output goes
// through the same sanitiser as every other body before it reaches the DOM, so
// a tag this misreads costs a picture, never safety.
func scanSubresources(doc string) []attrSpan {
	var out []attrSpan
	i, tag := 0, ""
	for i < len(doc) {
		c := doc[i]
		switch {
		case c == '<':
			n := i + 1
			for n < len(doc) && isNameByte(doc[n]) {
				n++
			}
			tag = strings.ToLower(doc[i+1 : n])
			i = n
		case c == '>':
			tag = ""
			i++
		case tag != "" && isNameByte(c):
			start := i
			for i < len(doc) && isNameByte(doc[i]) {
				i++
			}
			if i >= len(doc) || doc[i] != '=' {
				continue // a bare attribute: nothing to fetch
			}
			name := strings.ToLower(doc[start:i])
			i++
			sp := attrSpan{name: name, start: start, vStart: i}
			if i < len(doc) && (doc[i] == '"' || doc[i] == '\'') {
				end := strings.IndexByte(doc[i+1:], doc[i])
				if end < 0 {
					return out
				}
				sp.vStart, sp.vEnd, i = i+1, i+1+end, i+2+end
			} else {
				for i < len(doc) && doc[i] != ' ' && doc[i] != '>' {
					i++
				}
				sp.vEnd = i
			}
			sp.end = i
			if fetches(tag, name) {
				sp.val = strings.ReplaceAll(strings.ReplaceAll(doc[sp.vStart:sp.vEnd], "&quot;", `"`), "&amp;", "&")
				out = append(out, sp)
			}
		default:
			i++
		}
	}
	return out
}

func isNameByte(c byte) bool {
	return c == '-' || c == '_' || c == ':' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// alignSubresources makes a translation's images load exactly the way the
// original's do.
//
// The server can now apply GetItem's rewrite to both bodies itself
// (SmartServer.WithAssetProxy). Until every server it talks to is wired that
// way this stays, and it is harmless where one is: a proxied translation
// paired with its proxied original maps each URL to the one GetItem served.
//
// Without that wiring the translation comes back with the article's ORIGINAL
// markup — the publisher's own image URLs — because the server rewrites
// subresources onto its local proxy only on the GetItem path (§10.1a, and per
// reader: the `proxy.images` preference). Rendered as it arrives, a translated article
// would fetch every picture straight from the publisher: a request the reader
// switched off, and pictures that do not load on exactly the networks the
// proxy exists for.
//
// The response carries the sanitised original it was translated from, and the
// reader already holds the same article as GetItem served it — proxied or not,
// as this reader's preference says. Paired in document order, the two say what
// each publisher URL became, and the translation, which keeps the original's
// structure, is rewritten the same way.
//
// When the pairing cannot be trusted (the two disagree about which elements
// there are), a reference the shown original does not carry is DROPPED rather
// than kept: a missing picture is the visible, recoverable failure; a silent
// fetch from the publisher is neither. There is no shortcut for "nothing was
// rewritten" for the same reason: the filter is what keeps a translation from
// carrying a reference its original never had.
func alignSubresources(translated, sentOriginal, shownOriginal string) string {
	sent, shown := scanSubresources(sentOriginal), scanSubresources(shownOriginal)
	aligned := len(sent) == len(shown)
	for i := 0; aligned && i < len(sent); i++ {
		aligned = sent[i].name == shown[i].name
	}
	var b strings.Builder
	at := 0
	for _, t := range scanSubresources(translated) {
		b.WriteString(translated[at:t.start])
		at = t.end
		v := ""
		for i, s := range shown {
			if s.val == t.val || (aligned && sent[i].val == t.val) {
				v = s.val
				break
			}
		}
		if v == "" {
			continue // dropped: see above
		}
		b.WriteString(t.name + `="` + strings.ReplaceAll(strings.ReplaceAll(v, "&", "&amp;"), `"`, "&quot;") + `"`)
	}
	b.WriteString(translated[at:])
	return b.String()
}
