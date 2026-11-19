package grpcsrv

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The JEV credential on the Smart+ config (A47, §18.8b): rung 2's key, route and
// base URL, set the way the OpenAI key is set and reported the way it is
// reported — presence, the last four, the source, and never the key.

func jevServer(t *testing.T, role string) (*SmartServer, *store.DB) {
	t.Helper()
	// A developer's own TypeSafe key in the environment would read as a stored
	// one to every assertion below. TestJEVKeyFallsBackToTheEnvironment sets its
	// own after this; the config reads the environment per call.
	t.Setenv("SCHEMAFLUX_TYPESAFE_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	encKey, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	sc := ownerScope(role)
	return smartConfigServer(t, encKey, sc, fixedScope(sc))
}

func TestJEVKeyRoundTripsAndIsNeverEchoed(t *testing.T) {
	s, _ := jevServer(t, "admin")
	ctx := context.Background()
	const key = "tsk_live_0123456789abcdWXYZ"

	fresh, err := s.GetSmartConfig(ctx, &pb.GetSmartConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.GetJevConfigured() || fresh.GetJevReady() || fresh.GetJevKeyHint() != "" {
		t.Errorf("fresh instance claims a JEV key: %+v", fresh)
	}
	// An unset route is reported as the route in force, not as an empty one.
	if fresh.GetJevProvider() != "jev" {
		t.Errorf("default route = %q, want jev", fresh.GetJevProvider())
	}

	res, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{JevApiKey: key})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	cfg := res.GetConfig()
	if !cfg.GetJevConfigured() || !cfg.GetJevReady() || cfg.GetJevFromEnvironment() {
		t.Errorf("after set: configured=%v ready=%v env=%v", cfg.GetJevConfigured(), cfg.GetJevReady(), cfg.GetJevFromEnvironment())
	}
	if cfg.GetJevKeyHint() != "WXYZ" {
		t.Errorf("hint = %q, want WXYZ", cfg.GetJevKeyHint())
	}
	// Never the key: not in any field of the response, in any form.
	if strings.Contains(cfg.String(), "0123456789abcd") {
		t.Errorf("the response carries the key: %s", cfg.String())
	}
	// The OpenAI key is a different credential and is untouched.
	if cfg.GetConfigured() {
		t.Error("storing a JEV key configured the OpenAI key")
	}

	// An unrelated save leaves it alone.
	if res, err = s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{Model: "gpt-6-luna"}); err != nil ||
		!res.GetConfig().GetJevConfigured() {
		t.Fatalf("an unrelated save changed the JEV key: %v %v", res, err)
	}

	res, err = s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{ClearJevApiKey: true})
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if res.GetConfig().GetJevConfigured() || res.GetConfig().GetJevKeyHint() != "" {
		t.Errorf("after clear: %+v", res.GetConfig())
	}
}

func TestJEVKeyFallsBackToTheEnvironment(t *testing.T) {
	for _, env := range []string{"SCHEMAFLUX_TYPESAFE_API_KEY", "TYPESAFE_API_KEY"} {
		t.Run(env, func(t *testing.T) {
			s, _ := jevServer(t, "admin")
			t.Setenv(env, "env-key-9876")
			cfg, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.GetJevConfigured() || !cfg.GetJevFromEnvironment() || cfg.GetJevKeyHint() != "9876" {
				t.Errorf("env key: %+v", cfg)
			}
		})
	}
}

func TestJEVKeyShapeIsChecked(t *testing.T) {
	s, _ := jevServer(t, "admin")
	for _, bad := range []string{"https://api.typesafe.ai/key", "export TYPESAFE_API_KEY=abc", "two words"} {
		_, err := s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{JevApiKey: bad})
		if codeOf(err) != codes.InvalidArgument || detailKey(t, err) != "srv.badJevKeyShape" {
			t.Errorf("%q: %v (%s)", bad, err, detailKey(t, err))
		}
	}
}

func TestJEVKeyWithNoEncryptionKeyNamesItsOwnVariable(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	_, err := s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{JevApiKey: "tsk_abc12345"})
	if codeOf(err) != codes.FailedPrecondition || detailKey(t, err) != "srv.cannotStoreJevSecret" {
		t.Errorf("err = %v (%s)", err, detailKey(t, err))
	}
}

func TestJEVProviderAndBaseURLValidation(t *testing.T) {
	str := func(v string) *string { return &v }
	cases := []struct {
		name     string
		req      *pb.SetSmartConfigRequest
		wantKey  string // "" means accepted
		provider string
		base     string
	}{
		{"unknown route", &pb.SetSmartConfigRequest{JevProvider: str("openai")}, "srv.jevBadProvider", "", ""},
		{"empty route", &pb.SetSmartConfigRequest{JevProvider: str("")}, "srv.jevBadProvider", "", ""},
		{"openrouter", &pb.SetSmartConfigRequest{JevProvider: str("jev-openrouter")}, "", "jev-openrouter", ""},
		{"litellm without a base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm")}, "srv.jevBaseURLRequired", "", ""},
		{"base beside a fixed route", &pb.SetSmartConfigRequest{JevBaseUrl: str("https://proxy.example.com")}, "srv.jevBaseURLNotUsed", "", ""},
		{"http base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("http://proxy.example.com")}, "srv.jevBadBaseURL", "", ""},
		{"loopback base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https://127.0.0.1:4000")}, "srv.jevBadBaseURL", "", ""},
		{"private base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https://10.0.0.5")}, "srv.jevBadBaseURL", "", ""},
		{"metadata base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https://169.254.169.254/latest")}, "srv.jevBadBaseURL", "", ""},
		{"localhost base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https://localhost:4000")}, "srv.jevBadBaseURL", "", ""},
		{"credentials in base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https://u:p@proxy.example.com")}, "srv.jevBadBaseURL", "", ""},
		{"no host", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https:///v1")}, "srv.jevBadBaseURL", "", ""},
		{"litellm with a base", &pb.SetSmartConfigRequest{JevProvider: str("jev-litellm"), JevBaseUrl: str("https://proxy.example.com/v1")}, "", "jev-litellm", "https://proxy.example.com/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := jevServer(t, "admin")
			res, err := s.SetSmartConfig(context.Background(), c.req)
			if c.wantKey != "" {
				if codeOf(err) != codes.InvalidArgument || detailKey(t, err) != c.wantKey {
					t.Fatalf("err = %v (%s), want %s", err, detailKey(t, err), c.wantKey)
				}
				// Refused means nothing was written.
				cfg, _ := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
				if cfg.GetJevProvider() != "jev" || cfg.GetJevBaseUrl() != "" {
					t.Errorf("a refused save changed the route: %q %q", cfg.GetJevProvider(), cfg.GetJevBaseUrl())
				}
				return
			}
			if err != nil {
				t.Fatalf("refused: %v (%s)", err, detailKey(t, err))
			}
			if got := res.GetConfig(); got.GetJevProvider() != c.provider || got.GetJevBaseUrl() != c.base {
				t.Errorf("stored %q %q, want %q %q", got.GetJevProvider(), got.GetJevBaseUrl(), c.provider, c.base)
			}
		})
	}
}

// A refused JEV field refuses the whole save: the OpenAI key sent beside it
// must not be stored, or a screen showing one error would be hiding a change.
func TestARefusedJEVFieldWritesNothingElse(t *testing.T) {
	s, _ := jevServer(t, "admin")
	bad := "jev-nope"
	_, err := s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{
		OpenaiApiKey: "sk-abcd1234wxyz", Model: "gpt-x", JevProvider: &bad,
	})
	if detailKey(t, err) != "srv.jevBadProvider" {
		t.Fatalf("err = %v", err)
	}
	cfg, _ := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if cfg.GetConfigured() || cfg.GetModel() == "gpt-x" {
		t.Errorf("the refused save stored the rest: configured=%v model=%q", cfg.GetConfigured(), cfg.GetModel())
	}
}

func TestLeavingLiteLLMTakesTheBaseURLWithIt(t *testing.T) {
	s, _ := jevServer(t, "admin")
	ctx := context.Background()
	lite, base := "jev-litellm", "https://proxy.example.com"
	if _, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{JevApiKey: "tsk_abc12345", JevProvider: &lite, JevBaseUrl: &base}); err != nil {
		t.Fatal(err)
	}
	// Clearing the base URL while LiteLLM is the route would leave a route the
	// ladder silently skips.
	empty := ""
	if _, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{JevBaseUrl: &empty}); detailKey(t, err) != "srv.jevBaseURLRequired" {
		t.Errorf("clearing a LiteLLM base: %v", err)
	}
	direct := "jev"
	res, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{JevProvider: &direct})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetConfig().GetJevBaseUrl() != "" || !res.GetConfig().GetJevReady() {
		t.Errorf("after leaving LiteLLM: base=%q ready=%v", res.GetConfig().GetJevBaseUrl(), res.GetConfig().GetJevReady())
	}
}

// An inconsistent stored route — written before these rules, or by hand — is
// reported as not ready, and does not make unrelated saves fail.
func TestAnIncompleteStoredRouteIsNotReadyAndBlocksNothing(t *testing.T) {
	s, _ := jevServer(t, "admin")
	ctx := context.Background()
	if err := s.settings.SetSystemValue(ctx, store.KeyJEVProvider, "jev-litellm", ""); err != nil {
		t.Fatal(err)
	}
	res, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{JevApiKey: "tsk_abc12345", Model: "m"})
	if err != nil {
		t.Fatalf("an unrelated save was refused over the stored route: %v", err)
	}
	if cfg := res.GetConfig(); !cfg.GetJevConfigured() || cfg.GetJevReady() {
		t.Errorf("configured=%v ready=%v, want true false", cfg.GetJevConfigured(), cfg.GetJevReady())
	}
}

func TestJEVConfigIsOwnerOnly(t *testing.T) {
	for _, role := range []string{"member", "viewer"} {
		s, _ := jevServer(t, role)
		route := "jev-openrouter"
		for name, req := range map[string]*pb.SetSmartConfigRequest{
			"key": {JevApiKey: "tsk_abc12345"}, "clear": {ClearJevApiKey: true}, "route": {JevProvider: &route},
		} {
			if _, err := s.SetSmartConfig(context.Background(), req); codeOf(err) != codes.PermissionDenied {
				t.Errorf("%s %s: code = %v, want PermissionDenied", role, name, codeOf(err))
			}
		}
		if _, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{}); codeOf(err) != codes.PermissionDenied {
			t.Errorf("%s read the config: %v", role, err)
		}
	}
}
