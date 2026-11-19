//go:build js && wasm

package view

// The Smart+ tab as each reader meets it: an owner, under every combination of
// the two credentials the escalation ladder (A47) reads, and a member, who gets
// a calm account of whose decision this is instead of a refusal.

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v7/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

func renderSmart(t *testing.T, p smartProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node { return ui.Fragment(settingsSmart(tr, p)...) })
}

// rungOn reads one rung's data-on off the rendered tab.
func rungOn(t *testing.T, out, rung string) string {
	t.Helper()
	i := strings.Index(out, `data-rung="`+rung+`"`)
	if i < 0 {
		t.Fatalf("no %s rung:\n%s", rung, out)
	}
	// The span carries data-on beside data-rung; look in its own tag.
	start := strings.LastIndex(out[:i], "<")
	end := i + strings.Index(out[i:], ">")
	tag := out[start:end]
	switch {
	case strings.Contains(tag, `data-on="true"`):
		return "true"
	case strings.Contains(tag, `data-on="false"`):
		return "false"
	}
	t.Fatalf("%s rung has no data-on: %s", rung, tag)
	return ""
}

func TestTheLadderShowsWhichRungsThisInstanceHas(t *testing.T) {
	cases := []struct {
		name       string
		cfg        *pb.GetSmartConfigResponse
		jev, model string
		jevState   string
	}{
		{"no keys", &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevProvider: "jev"}, "false", "false", "no key"},
		{"OpenAI only", &pb.GetSmartConfigResponse{CanStoreSecrets: true, Configured: true, KeyHint: "4f2a",
			JevProvider: "jev"}, "false", "true", "no key"},
		{"JEV only", &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevConfigured: true, JevReady: true,
			JevKeyHint: "WXYZ", JevProvider: "jev"}, "true", "false", "ready"},
		{"both", &pb.GetSmartConfigResponse{CanStoreSecrets: true, Configured: true, JevConfigured: true,
			JevReady: true, JevProvider: "jev-openrouter"}, "true", "true", "ready"},
		// A JEV key on a LiteLLM route with no address: stored, but skipped.
		{"JEV key, route incomplete", &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevConfigured: true,
			JevProvider: "jev-litellm"}, "false", "false", "needs the proxy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := renderSmart(t, smartProps{cfg: c.cfg, owner: true})
			if got := rungOn(t, out, "code"); got != "true" {
				t.Errorf("code rung on = %s; it needs nothing", got)
			}
			if got := rungOn(t, out, "jev"); got != c.jev {
				t.Errorf("jev rung on = %s, want %s", got, c.jev)
			}
			if got := rungOn(t, out, "model"); got != c.model {
				t.Errorf("model rung on = %s, want %s", got, c.model)
			}
			jevAt := strings.Index(out, `data-rung="jev"`)
			if !strings.Contains(out[jevAt:jevAt+200], c.jevState) {
				t.Errorf("jev rung does not say %q:\n%s", c.jevState, out[jevAt:jevAt+200])
			}
			// The two paid rungs share the one ceiling, and the screen says so.
			if !strings.Contains(out, "same spend limit") {
				t.Error("the shared spend ceiling is not stated")
			}
			// It says what JEV is asked, including what it cannot do.
			if !strings.Contains(out, "cannot write text") {
				t.Error("the ladder does not say JEV cannot write")
			}
			assertIDRefsResolve(t, c.name, out)
		})
	}
}

func TestTheJEVSectionMirrorsTheOpenAIKey(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevConfigured: true, JevReady: true,
		JevKeyHint: "WXYZ", JevProvider: "jev"}
	out := renderSmart(t, smartProps{cfg: cfg, owner: true})

	var field string
	for _, tag := range inputTagRe.FindAllString(out, -1) {
		if strings.Contains(tag, `data-role="smart-jev-key"`) {
			field = tag
		}
	}
	if field == "" {
		t.Fatalf("no JEV key field:\n%s", out)
	}
	for _, want := range []string{`type="password"`, `autocomplete="off"`, `spellcheck="false"`,
		`autocapitalize="off"`, `autocorrect="off"`, `data-1p-ignore`, `data-lpignore="true"`,
		`data-bwignore="true"`, `id="` + fieldSmartJEVKey + `"`} {
		if !strings.Contains(field, want) {
			t.Errorf("JEV key field lacks %s: %s", want, field)
		}
	}
	// The state says presence and the last four — never more.
	if !strings.Contains(out, "ending WXYZ") {
		t.Error("the key's last four are not shown")
	}
	if !strings.Contains(out, `data-action="`+actSmartJEVClear+`"`) {
		t.Error("a stored key offers no removal")
	}
	// A fixed route: no address field.
	if strings.Contains(out, `data-role="smart-jev-base"`) {
		t.Error("the address field shows beside a fixed-address route")
	}
}

func TestTheJEVRemovalAsksFirst(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevConfigured: true, JevReady: true, JevProvider: "jev"}
	rest := renderSmart(t, smartProps{cfg: cfg, owner: true})
	if strings.Contains(rest, `data-action="`+actSmartJEVClearDo+`"`) {
		t.Fatal("removing the JEV key is one press away at rest")
	}
	armed := renderSmart(t, smartProps{cfg: cfg, owner: true, armed: armJEVClear})
	at := strings.Index(armed, `data-role="`+armConfirmRole+`"`)
	if at < 0 {
		t.Fatalf("arming showed no confirmation:\n%s", armed)
	}
	group := armed[at:]
	cancel := strings.Index(group, `data-action="`+actArmCancel+`"`)
	do := strings.Index(group, `data-action="`+actSmartJEVClearDo+`"`)
	if cancel < 0 || do < 0 || cancel > do {
		t.Errorf("armed: want Cancel before the removal, got cancel@%d do@%d", cancel, do)
	}
	// Arming the JEV removal is not arming the OpenAI one: one shared state,
	// two keys, so only one can be open.
	if strings.Contains(armed, `data-action="`+actSmartKeyClearDo+`"`) {
		t.Error("arming the JEV removal armed the OpenAI one too")
	}
}

func TestAJEVKeyFromTheEnvironmentIsSaidAndNotRemovable(t *testing.T) {
	cfg := &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevConfigured: true, JevReady: true,
		JevFromEnvironment: true, JevProvider: "jev"}
	out := renderSmart(t, smartProps{cfg: cfg, owner: true})
	if !strings.Contains(out, "SCHEMAFLUX_TYPESAFE_API_KEY") {
		t.Error("the environment source is not named")
	}
	if strings.Contains(out, `data-action="`+actSmartJEVClear+`"`) {
		t.Error("offers to remove a key this screen cannot remove")
	}
}

func TestTheLiteLLMRouteOpensItsAddressField(t *testing.T) {
	// Stored LiteLLM route: the field shows the stored address.
	cfg := &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevConfigured: true, JevReady: true,
		JevProvider: "jev-litellm", JevBaseUrl: "https://proxy.example.com"}
	out := renderSmart(t, smartProps{cfg: cfg, owner: true})
	var field string
	for _, tag := range inputTagRe.FindAllString(out, -1) {
		if strings.Contains(tag, `data-role="smart-jev-base"`) {
			field = tag
		}
	}
	if field == "" || !strings.Contains(field, `value="https://proxy.example.com"`) {
		t.Fatalf("stored LiteLLM route: field %q\n%s", field, out)
	}
	if !strings.Contains(out, `data-action="`+actSmartJEVBaseSave+`"`) {
		t.Error("no way to save the address")
	}

	// A LiteLLM pick not yet saved, on a direct route: the field opens empty,
	// and the picked chip is the pressed one.
	cfg = &pb.GetSmartConfigResponse{CanStoreSecrets: true, JevProvider: "jev"}
	out = renderSmart(t, smartProps{cfg: cfg, owner: true, jevRoute: "jev-litellm"})
	if !strings.Contains(out, `data-role="smart-jev-base"`) {
		t.Fatalf("a pending LiteLLM pick did not open the field:\n%s", out)
	}
	for route, pressed := range map[string]string{"jev": "false", "jev-litellm": "true"} {
		i := strings.Index(out, `data-value="`+route+`"`)
		start := strings.LastIndex(out[:i], "<button")
		tag := out[start : i+strings.Index(out[i:], ">")]
		if !strings.Contains(tag, `aria-pressed="`+pressed+`"`) {
			t.Errorf("%s chip: %s, want pressed=%s", route, tag, pressed)
		}
	}
	assertIDRefsResolve(t, "litellm", out)
}

func TestAMemberGetsACalmStateNotAnError(t *testing.T) {
	langs := languagesFixture()
	for _, on := range []bool{true, false} {
		// The config call was refused, as it is for every member: err is the
		// server's sentence, cfg is nil. Neither must reach the screen as a
		// failure.
		out := renderSmart(t, smartProps{owner: false, languages: langs, translateOn: on,
			err: "only an administrator can change Smart+ settings"})
		if strings.Contains(out, "fs-error") || strings.Contains(out, `role="alert"`) {
			t.Errorf("translateOn=%v: a member is shown an error:\n%s", on, out)
		}
		if !strings.Contains(out, `data-role="smart-member"`) || !strings.Contains(out, "your administrator") {
			t.Errorf("translateOn=%v: no member state:\n%s", on, out)
		}
		want := "Off —"
		if on {
			want = "On —"
		}
		if !strings.Contains(out, want) {
			t.Errorf("translateOn=%v: article translation state %q missing:\n%s", on, want, out)
		}
		// What is already translated here, and nothing about what is not.
		if !strings.Contains(out, "Deutsch") || !strings.Contains(out, "Français") || strings.Contains(out, "日本語") {
			t.Errorf("coverage: %s", out)
		}
		// None of the owner's controls.
		for _, owner := range []string{`data-role="smart-key"`, `data-role="smart-jev-key"`,
			actSmartArticleTranslate, actSmartModel, `data-role="smart-rung"`} {
			if strings.Contains(out, owner) {
				t.Errorf("a member is shown %s", owner)
			}
		}
		assertIDRefsResolve(t, "member", out)
	}

	out := renderSmart(t, smartProps{owner: false})
	if !strings.Contains(out, "None yet") {
		t.Errorf("no languages translated, and nothing says so:\n%s", out)
	}
	// Still loading, nothing yet to show: the skeleton, as for an owner.
	if out := renderSmart(t, smartProps{owner: false, loading: true}); !strings.Contains(out, "sk-line") {
		t.Errorf("a loading member tab is not a skeleton:\n%s", out)
	}
}

// An owner whose config failed for any other reason still sees the failure:
// the calm state is for a refusal that is not theirs to fix.
func TestAnOwnersFailedLoadIsStillAnError(t *testing.T) {
	out := renderSmart(t, smartProps{owner: true, err: "couldn't reach the server"})
	if !strings.Contains(out, "fs-error") || strings.Contains(out, `data-role="smart-member"`) {
		t.Errorf("owner failure:\n%s", out)
	}
}

// The demo serves a config to a reader with no role: it is an instance with no
// keys, and is shown as one rather than as a member's state.
func TestAServedConfigIsShownWhateverTheRole(t *testing.T) {
	out := renderSmart(t, smartProps{owner: false, cfg: &pb.GetSmartConfigResponse{JevProvider: "jev"}})
	if strings.Contains(out, `data-role="smart-member"`) || !strings.Contains(out, `data-role="smart-rung"`) {
		t.Errorf("served config rendered as the member state:\n%s", out)
	}
}

func TestACachedTranslationIsOfferedWhenFreshOnesAreOff(t *testing.T) {
	if consentFromLanguages(&pb.ListLanguagesResponse{TranslateArticlesEnabled: true}) != 1 ||
		consentFromLanguages(&pb.ListLanguagesResponse{}) != -1 || consentFromLanguages(nil) != -1 {
		t.Error("consentFromLanguages misreads the flag")
	}
	res := &pb.TranslateArticleResponse{ItemId: "a1", Locale: "fr", Cached: true, SourceLocale: "de",
		Title: "Bonjour", ContentHtml: "<p>Bonjour le monde, un texte assez long.</p>",
		OriginalContentHtml: "<p>Hallo Welt</p>"}
	got := cachedTranslation(res, "fr", "<p>Hallo Welt</p>")
	if got.state != trDone || !got.original || got.locale != "fr" || got.source != "de" || got.title != "Bonjour" {
		t.Errorf("cachedTranslation: %+v", got)
	}
	// Offered, not swapped in: the article still reads as published, and the
	// Translate control is there although fresh translation is off.
	p := translatedFixture(got, false)
	out := renderArticle(t, p)
	if !strings.Contains(out, `data-action="`+actTranslate+`"`) {
		t.Fatalf("a cached translation is not offered with translation off:\n%s", out)
	}
	if strings.Contains(out, "Bonjour le monde") {
		t.Error("the cached translation was swapped in unasked")
	}
}
