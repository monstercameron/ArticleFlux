//go:build js && wasm

package view

// On-the-fly translation, as the reader meets it: the article's Translate
// control through every state it has, the body it swaps in, the cache rule
// that keeps the two bodies apart, the images inside it, the language picker's
// coverage, and the consent switch only an owner is shown.

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// translatedFixture is the article pane with translation offered, in French.
func translatedFixture(t articleTranslation, ok bool) articleProps {
	p := fixtureArticle(3)
	p.locale = "fr"
	p.translateOK = ok
	if t.state != "" {
		t.locale = "fr"
		p.translations = map[string]articleTranslation{"a1": t}
	}
	return p
}

func renderArticle(t *testing.T, p articleProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node { return articlePane(tr, p) })
}

func TestTranslateControlThroughItsStates(t *testing.T) {
	const fr = "<p>Le texte traduit, assez long pour être un article.</p>"
	cases := []struct {
		name      string
		tr        articleTranslation
		ok        bool
		state     string // the control's data-state, "" for no control
		label     string
		bodyLang  bool   // the body carries lang="fr"
		note      string // a phrase the line under the actions must carry
		titleText string
	}{
		{name: "not offered", ok: false},
		{name: "offered, never asked", ok: true, state: "idle", label: "Translate"},
		{name: "in flight", ok: true, tr: articleTranslation{state: trBusy},
			state: trBusy, label: "Translating…"},
		{name: "translated", ok: true,
			tr:    articleTranslation{state: trDone, source: "de", title: "Le titre", html: fr},
			state: "translated", label: "Show original", bodyLang: true,
			note: "Translated from German.", titleText: "Le titre"},
		{name: "partial", ok: true,
			tr:    articleTranslation{state: trDone, source: "de", title: "Le titre", html: fr, partial: true},
			state: "translated", label: "Show original", bodyLang: true,
			note: "The end of this article is in the original language."},
		{name: "showing the original", ok: true,
			tr:    articleTranslation{state: trDone, original: true, source: "de", html: fr},
			state: "original", label: "Translate", titleText: "The long article"},
		{name: "failed", ok: true,
			tr:    articleTranslation{state: trFailed, err: "the model is unavailable"},
			state: trFailed, label: "Translate",
			note: "translate this article: the model is unavailable"},
		{name: "already in this language", ok: true, tr: articleTranslation{state: trSame},
			note: "This article is already in your language."},
		// A translation already on hand stays reachable after the owner turns
		// the feature off: reading it costs nothing.
		{name: "consent withdrawn, translation kept", ok: false,
			tr:    articleTranslation{state: trDone, source: "de", html: fr},
			state: "translated", label: "Show original", bodyLang: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderArticle(t, translatedFixture(c.tr, c.ok))
			if c.state == "" {
				if strings.Contains(out, `data-action="translate" data-for-item="a1"`) {
					t.Fatalf("a Translate control was offered:\n%s", out)
				}
			} else {
				btn := buttonBlock(t, out, `data-action="translate" data-for-item="a1"`)
				if !strings.Contains(btn, `data-state="`+c.state+`"`) || !strings.Contains(btn, c.label) {
					t.Errorf("control = %s, want state %q label %q", btn, c.state, c.label)
				}
				if strings.Contains(btn, "aria-pressed") {
					t.Errorf("the control is a toggle button whose state nobody can read: %s", btn)
				}
				if c.state == trBusy && !strings.Contains(btn, "disabled") {
					t.Errorf("a second press is possible while the first is in flight: %s", btn)
				}
			}
			body := elementTag(t, out, `class="article-body"`)
			if got := strings.Contains(body, `lang="fr"`); got != c.bodyLang {
				t.Errorf("body tag %s: lang=fr is %v, want %v", body, got, c.bodyLang)
			}
			if c.bodyLang && !strings.Contains(body, `dir="ltr"`) {
				t.Errorf("translated body has no dir: %s", body)
			}
			if c.bodyLang != strings.Contains(out, "Le texte traduit") {
				t.Errorf("the translated text is shown=%v, want %v", !c.bodyLang, c.bodyLang)
			}
			if c.note != "" && !strings.Contains(out, c.note) {
				t.Errorf("missing %q", c.note)
			}
			if c.titleText != "" && !strings.Contains(out, c.titleText) {
				t.Errorf("headline %q not shown", c.titleText)
			}
		})
	}
}

// Arabic is laid out right to left inside an English interface: the body says
// so itself rather than inheriting the document's direction.
func TestTranslatedBodyCarriesItsOwnDirection(t *testing.T) {
	p := fixtureArticle(3)
	p.locale = "ar"
	p.translateOK = true
	p.translations = map[string]articleTranslation{"a1": {state: trDone, locale: "ar",
		html: "<p>نص مترجم</p>", title: "عنوان"}}
	out := renderArticle(t, p)
	body := elementTag(t, out, `class="article-body"`)
	if !strings.Contains(body, `dir="rtl"`) || !strings.Contains(body, `lang="ar"`) {
		t.Errorf("body = %s, want lang=ar dir=rtl", body)
	}
	if h := elementTag(t, out, "<h1"); !strings.Contains(h, `dir="rtl"`) {
		t.Errorf("headline = %s, want dir=rtl", h)
	}
}

// A translation into another language than the one in force is not shown: it
// is about an interface the reader has left.
func TestTranslationForAnotherLanguageIsNotShown(t *testing.T) {
	p := translatedFixture(articleTranslation{state: trDone, html: "<p>Le texte traduit</p>"}, true)
	p.locale = "de"
	out := renderArticle(t, p)
	if strings.Contains(out, "Le texte traduit") {
		t.Error("a French translation was shown in a German interface")
	}
	if btn := buttonBlock(t, out, `data-action="translate" data-for-item="a1"`); !strings.Contains(btn, `data-state="idle"`) {
		t.Errorf("control = %s, want a fresh Translate", btn)
	}
}

// The body cache re-parses only when the length changes, so a translation that
// shared the original's key would be shown the original's nodes whenever the
// two were the same length. They must never share one.
func TestTranslatedAndOriginalBodiesNeverShareACacheEntry(t *testing.T) {
	if bodyKey("a1", "") != "a1" {
		t.Errorf("the original's key moved: %q", bodyKey("a1", ""))
	}
	keys := map[string]bool{bodyKey("a1", ""): true}
	for _, loc := range []string{"fr", "de", "ar"} {
		k := bodyKey("a1", loc)
		if keys[k] {
			t.Fatalf("%q collides", k)
		}
		keys[k] = true
	}
	// Same length on purpose: the case a length-keyed cache gets wrong.
	orig, trans := "<p>original text</p>", "<p>traduit texte</p>"
	if len(orig) != len(trans) {
		t.Fatal("fixture drift: the two bodies must be the same length")
	}
	bodyCache = map[string]cachedBody{}
	parsedBody(bodyKey("a1", ""), orig)
	out := renderView(t, func(tr i18n.Runtime) ui.Node {
		return articleBodyIn(tr, bodyKey("a1", "fr"), trans, "fr")
	})
	if !strings.Contains(out, "traduit") || strings.Contains(out, "original text") {
		t.Errorf("the translated body rendered the original's cache entry: %s", out)
	}
}

// Images in a translation load exactly as the original's do: through the
// proxy when the original's went through it, never straight from the
// publisher when they did not.
func TestTranslatedImagesFollowTheOriginals(t *testing.T) {
	sent := `<p>Hi</p><img src="https://pub.example/a.png"><img src="https://pub.example/b.png" srcset="https://pub.example/b2.png 2x">`
	shown := `<p>Hi</p><img src="/asset/1"><img src="/asset/2" srcset="/asset/3 2x">`
	translated := `<p>Salut</p><img src="https://pub.example/a.png"><img src="https://pub.example/b.png" srcset="https://pub.example/b2.png 2x">`

	got := alignSubresources(translated, sent, shown)
	for _, want := range []string{`src="/asset/1"`, `src="/asset/2"`, `srcset="/asset/3 2x"`, "Salut"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "pub.example") {
		t.Errorf("a publisher URL survived: %s", got)
	}

	// The reader opted out of the proxy: the original shows the publisher's
	// URLs, and so does the translation.
	if got := alignSubresources(translated, sent, sent); !strings.Contains(got, `src="https://pub.example/a.png"`) {
		t.Errorf("an unproxied reader lost their images: %s", got)
	}

	// The two originals disagree about what is there: unmatched references are
	// dropped rather than fetched from the publisher.
	got = alignSubresources(translated, sent, `<p>Hi</p><img src="/asset/1">`)
	if strings.Contains(got, "pub.example") {
		t.Errorf("an unaligned translation fetched from the publisher: %s", got)
	}
}

func languagesFixture() []*pb.SmartLanguage {
	return []*pb.SmartLanguage{
		{Code: "de", NativeName: "Deutsch", Translated: 1000, Total: 1000, Cached: true},
		{Code: "fr", NativeName: "Français", Translated: 400, Total: 1000},
		{Code: "ja", NativeName: "日本語", Translated: 0, Total: 1000},
	}
}

func renderLanguages(t *testing.T, p smartProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return ui.Fragment(smartLanguageSection(tr, p)...)
	})
}

// A reader can tell a complete language from a partial one before choosing,
// and a language nobody can translate for them is not a switch that silently
// does nothing.
func TestLanguagePickerShowsCoverage(t *testing.T) {
	member := smartProps{languages: languagesFixture(), locale: "en"}
	out := renderLanguages(t, member)
	if b := buttonBlock(t, out, `data-value="de"`); !strings.Contains(b, "ready") {
		t.Errorf("a complete language: %s", b)
	}
	if b := buttonBlock(t, out, `data-value="fr"`); !strings.Contains(b, "40% translated") || strings.Contains(b, "disabled") {
		t.Errorf("a partial language: %s", b)
	}
	if b := buttonBlock(t, out, `data-value="ja"`); !strings.Contains(b, "not translated yet") || !strings.Contains(b, "disabled") {
		t.Errorf("an empty language, for a member: %s", b)
	}

	owner := member
	owner.owner = true
	owner.cfg = &pb.GetSmartConfigResponse{Configured: true}
	out = renderLanguages(t, owner)
	if b := buttonBlock(t, out, `data-value="ja"`); !strings.Contains(b, "will translate") || strings.Contains(b, "disabled") {
		t.Errorf("an empty language, for an owner with a key: %s", b)
	}
}

// The status under the picker is honest about the language in force: how much
// is translated, and whether the rest is coming or needs an administrator.
func TestLanguageStatusSaysWhatHappensToTheRest(t *testing.T) {
	base := smartProps{languages: languagesFixture(), locale: "fr"}

	filling := base
	filling.owner = true
	filling.fill = langFill{locale: "fr", done: 412, total: 1000, running: true, owner: true}
	out := renderLanguages(t, filling)
	if !strings.Contains(out, "412 of 1,000 translated.") || !strings.Contains(out, "in the background") {
		t.Errorf("an owner's fill in progress:\n%s", out)
	}
	if b := buttonBlock(t, out, `data-value="fr"`); !strings.Contains(b, "41% translated") {
		t.Errorf("the chip did not follow the live count: %s", b)
	}

	member := base
	member.fill = langFill{locale: "fr", done: 412, total: 1000, idle: "not_owner"}
	if out := renderLanguages(t, member); !strings.Contains(out, "Only an administrator can translate the rest") {
		t.Errorf("a member was not told who can finish it:\n%s", out)
	}

	done := base
	done.fill = langFill{locale: "fr", done: 1000, total: 1000}
	if out := renderLanguages(t, done); strings.Contains(out, `data-role="lang-status"`) {
		t.Errorf("a complete language still reports a count:\n%s", out)
	}
}

// The article-translation consent is the instance's, so only an owner sees it.
func TestArticleTranslateConsentIsOwnerOnly(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{Configured: true, TranslateArticlesEnabled: true}
	render := func(owner bool) string {
		return renderView(t, func(tr i18n.Runtime) ui.Node {
			return ui.Fragment(smartFeatureSection(tr, smartProps{cfg: cfg, owner: owner})...)
		})
	}
	out := render(true)
	b := buttonBlock(t, out, `data-action="`+actSmartArticleTranslate+`"`)
	if !strings.Contains(b, `aria-pressed="true"`) {
		t.Errorf("owner's switch = %s, want it showing On", b)
	}
	if !strings.Contains(out, "never its links") {
		t.Error("the hint does not say what leaves the machine")
	}
	if strings.Contains(render(false), actSmartArticleTranslate) {
		t.Error("a member was shown the instance's consent switch")
	}
}

func TestOwnerRoles(t *testing.T) {
	for role, want := range map[string]bool{"superadmin": true, "admin": true, "owner": true, "member": false, "": false} {
		if isOwnerRole(role) != want {
			t.Errorf("isOwnerRole(%q) = %v", role, !want)
		}
	}
	if percentOf(999, 1000) != 99 || percentOf(1, 1000) != 1 || percentOf(0, 1000) != 0 {
		t.Error("percentOf rounds a nearly-complete or barely-started language to a claim it cannot make")
	}
}
