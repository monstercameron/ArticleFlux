package grpcsrv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/themewire"
	"github.com/monstercameron/ArticleFlux/internal/topics"

	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// The gaps theme_test.go's own fixtures cannot reach: a caller who never
// resolves to a scope at all, the two branches of the Smart+ attune call that
// need a working (fake) generator rather than an absent one, and themeErr's
// mapping table, which theme_test.go only ever drives through the
// llm.ErrNotConfigured arm.

// silentLog is a logger that discards, used wherever a test needs s.log !=
// nil without asserting on what it says.
func silentLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// bareSmartServer is a server with no scopeOf at all, for requireReader's own
// nil-check branch — themingFor always wires one, so that branch never fires
// through any test in theme_test.go.
func bareSmartServer() *SmartServer {
	return NewSmartServer(nil, llm.New(func(context.Context) string { return "" }), nil, nil, silentLog())
}

func erroringScopeServer(err error) *SmartServer {
	return NewSmartServer(nil, llm.New(func(context.Context) string { return "" }), nil,
		func(context.Context) (store.Scope, error) { return store.Scope{}, err },
		silentLog())
}

// --- requireReader ------------------------------------------------------------

func TestRequireReaderWithNoScopeResolverIsNotSignedIn(t *testing.T) {
	s := bareSmartServer()
	if _, err := s.requireReader(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestRequireReaderWhenScopeOfErrorsIsNotSignedIn(t *testing.T) {
	s := erroringScopeServer(errors.New("session lookup failed"))
	if _, err := s.requireReader(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestRequireReaderWithAnInvalidScopeIsNotSignedIn(t *testing.T) {
	s := NewSmartServer(nil, llm.New(func(context.Context) string { return "" }), nil,
		func(context.Context) (store.Scope, error) { return store.Scope{}, nil }, silentLog())
	if _, err := s.requireReader(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Errorf("an empty scope was accepted as signed in (code %v)", status.Code(err))
	}
}

// --- allowTheme ---------------------------------------------------------------

// TestAllowThemeWithNoLimiterIsUnbounded. A server built without WithTheming
// has no budget at all — the same "an optional capability's absence is not a
// wiring error" shape as WithSpeechMeter's.
func TestAllowThemeWithNoLimiterIsUnbounded(t *testing.T) {
	s := bareSmartServer()
	if err := s.allowTheme(scopeForTheme()); err != nil {
		t.Errorf("allowTheme with no limiter refused: %v", err)
	}
}

// --- ComposeTheme's own gaps, past requireReader --------------------------------

func TestComposeThemePropagatesARequireReaderFailure(t *testing.T) {
	s := erroringScopeServer(errors.New("boom"))
	s.palettes = nonCallingPalettes()
	_, err := s.ComposeTheme(context.Background(), &pb.ComposeThemeRequest{Prompt: "a room"})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestComposeThemeWithNoGeneratorNeedsAKey(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)
	// themingFor's own WithTheming(nil, repo) leaves s.palettes nil — the shape
	// of an instance that has never had an OpenAI key configured.
	_, err := s.ComposeTheme(context.Background(), &pb.ComposeThemeRequest{Prompt: "a room"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition (srv.smartNoKey)", status.Code(err))
	}
}

// TestComposeThemeSucceedsOverAFakeGenerator is the one success path
// reachable without a real OpenAI key: the llmClient seam smart.Palettes was
// built with (internal/smart/llmclient.go) lets a fake stand in, so the
// generator's own JSON reply — not a live model — is what the assembled
// ComposeThemeResponse is checked against.
func TestComposeThemeSucceedsOverAFakeGenerator(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)
	schemafluxtest.Install(t, schemafluxtest.New().Reply(goodPaletteJSON))
	s.palettes = smart.NewPalettes(&fakePaletteClient{configured: true}, nil)

	res, err := s.ComposeTheme(context.Background(), &pb.ComposeThemeRequest{
		Prompt: "a cold library at 2am", Tone: "dark",
	})
	if err != nil {
		t.Fatalf("ComposeTheme: %v", err)
	}
	if res.GetTheme() == nil {
		t.Fatal("a successful composition returned no theme")
	}
	if _, err := themewire.Theme(res.GetTheme()); err != nil {
		t.Errorf("the composed theme did not survive the client's own validation: %v", err)
	}
}

// --- SuggestTheme's own gaps -----------------------------------------------------

func TestSuggestThemePropagatesARequireReaderFailure(t *testing.T) {
	s := erroringScopeServer(errors.New("boom"))
	_, err := s.SuggestTheme(context.Background(), &pb.SuggestThemeRequest{
		Base: themewire.Tokens(design.Fanciful),
	})
	if status.Code(err) != codes.Unauthenticated {
		t.Errorf("code = %v, want Unauthenticated", status.Code(err))
	}
}

// TestSuggestThemeIsRateLimitedPerUser. allowTheme sits BEFORE the taste
// lookup, so this needs no topics at all — the budget is spent by asking, not
// by what the reader reads.
func TestSuggestThemeIsRateLimitedPerUser(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)

	throttled := false
	for i := 0; i < 60; i++ {
		_, err := s.SuggestTheme(context.Background(), &pb.SuggestThemeRequest{
			Base: themewire.Tokens(design.Fanciful),
		})
		if status.Code(err) == codes.ResourceExhausted {
			throttled = true
			break
		}
		if err != nil {
			t.Fatalf("attempt %d: unexpected error %v", i, err)
		}
	}
	if !throttled {
		t.Error("sixty suggestions in a row were all admitted; nothing bounds the spend")
	}
}

// TestSuggestThemeUsesTheSmartPaletteWhenConsentedAndConfigured drives the
// branch theme_test.go's TestSmartDriftNeedsConsent stops short of: consent
// AND a generator both present, so SuggestTheme actually calls Attune rather
// than only deciding whether it is allowed to.
func TestSuggestThemeUsesTheSmartPaletteWhenConsentedAndConfigured(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Zeppelins", TopTerms: []string{"airship", "helium"}, Trend: topics.TrendRising},
	}); err != nil {
		t.Fatalf("topics: %v", err)
	}
	if err := repo.SetPrefs(ctx, sc, store.Prefs{AttunePrefKey: "true"}); err != nil {
		t.Fatalf("prefs: %v", err)
	}
	schemafluxtest.Install(t, schemafluxtest.New().Reply(goodPaletteJSON))
	s.palettes = smart.NewPalettes(&fakePaletteClient{configured: true}, nil)

	res, err := s.SuggestTheme(ctx, &pb.SuggestThemeRequest{Base: themewire.Tokens(design.Ink)})
	if err != nil {
		t.Fatalf("SuggestTheme: %v", err)
	}
	if !res.GetSmart() {
		t.Error("a consenting reader with a configured generator got the deterministic drift")
	}
}

// TestSuggestThemeFallsBackWhenTheSmartCallFails covers the sibling branch:
// consent is given but the generator declines, and the response must still be
// the deterministic target rather than an error — that is the whole point of
// §11's ladder.
func TestSuggestThemeFallsBackWhenTheSmartCallFails(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Zeppelins", TopTerms: []string{"airship", "helium"}, Trend: topics.TrendRising},
	}); err != nil {
		t.Fatalf("topics: %v", err)
	}
	if err := repo.SetPrefs(ctx, sc, store.Prefs{AttunePrefKey: "true"}); err != nil {
		t.Fatalf("prefs: %v", err)
	}
	// Configured false, so Palettes.ask fails at llm.ErrNotConfigured before it
	// ever calls Do — the same "declined" shape a real unconfigured instance
	// would produce.
	s.palettes = smart.NewPalettes(&fakePaletteClient{configured: false}, nil)

	res, err := s.SuggestTheme(ctx, &pb.SuggestThemeRequest{Base: themewire.Tokens(design.Ink)})
	if err != nil {
		t.Fatalf("SuggestTheme: %v", err)
	}
	if res.GetSmart() {
		t.Error("a declined smart call was reported as a smart-derived target")
	}
	if res.GetTarget() == nil {
		t.Error("the deterministic fallback did not run when the smart call declined")
	}
}

// --- attuneSmart ------------------------------------------------------------------

func TestAttuneSmartWithNoReaderRepoIsFalse(t *testing.T) {
	s := bareSmartServer()
	if s.attuneSmart(context.Background(), scopeForTheme()) {
		t.Error("an instance with no reader repository reported consent")
	}
}

func TestAttuneSmartFailsClosedWhenPrefsCannotBeRead(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)
	// An unscoped read errors at the repository (store.ErrNoScope) rather than
	// returning an empty preference set.
	if s.attuneSmart(context.Background(), store.Scope{}) {
		t.Error("an unreadable preference was treated as consent")
	}
}

// --- taste --------------------------------------------------------------------

func TestTasteWithNoReaderRepoIsEmpty(t *testing.T) {
	s := bareSmartServer()
	labels, why := s.taste(context.Background(), scopeForTheme())
	if labels != nil || why != "" {
		t.Errorf("taste() = %v, %q over an instance with no reader repository", labels, why)
	}
}

// --- themeErr -----------------------------------------------------------------

// TestThemeErrMapsEveryGeneratorFailure. Each arm names a different remedy on
// the screen, and a misclassification here is a client telling a reader "try
// again" for a missing key, or the reverse — see the package's rules about
// never silently fixing a security-relevant mapping.
func TestThemeErrMapsEveryGeneratorFailure(t *testing.T) {
	s := &SmartServer{log: silentLog()}

	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"not configured", llm.ErrNotConfigured, codes.FailedPrecondition},
		{"no prompt", smart.ErrNoPrompt, codes.InvalidArgument},
		{"truncated", llm.ErrTruncated, codes.ResourceExhausted},
		{"not a palette", design.ErrNotAPalette, codes.Unavailable},
		{"anything else", errors.New("the provider is on fire"), codes.Unavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := s.themeErr(c.err, "test")
			if status.Code(err) != c.want {
				t.Errorf("themeErr(%v) code = %v, want %v", c.err, status.Code(err), c.want)
			}
		})
	}
}

// TestThemeErrDoesNotPanicWithNoLogger. The default arm is the only one that
// touches s.log, and a server built without WithTheming/a logger must not
// crash the RPC it is trying to fail gracefully.
func TestThemeErrDoesNotPanicWithNoLogger(t *testing.T) {
	s := &SmartServer{}
	if err := s.themeErr(errors.New("unclassified"), "test"); status.Code(err) != codes.Unavailable {
		t.Errorf("code = %v, want Unavailable", status.Code(err))
	}
}

// fakePaletteClient is the llmClient seam (internal/smart/llmclient.go) stood
// up here rather than in internal/smart, so ComposeTheme/SuggestTheme's own
// success and failure branches are reachable without a real OpenAI key.
type fakePaletteClient struct {
	configured bool
	reply      string
	err        error
	// n counts Do calls — used by the categorizer's own pref-gating tests
	// (reader_subscribe_test.go) to prove a gated-off path never asks at all,
	// not merely that its answer goes unused.
	n int
}

func (f *fakePaletteClient) Configured(context.Context) bool { return f.configured }

// OpsContext leaves the context alone, which is what a fake must do: the real
// client's version installs ArticleFlux's provider on it, and SchemaFlux prefers
// a context provider over anything a test registered. See smart's own fake.
func (f *fakePaletteClient) OpsContext(ctx context.Context) context.Context { return ctx }

// OpsModel names the model the operation runs on. A model has to arrive at
// the provider or SchemaFlux refuses the call before making it.
func (f *fakePaletteClient) OpsModel(context.Context) string { return "test-model" }

func (f *fakePaletteClient) Do(context.Context, llm.Request) (string, error) {
	f.n++
	return f.reply, f.err
}

func (f *fakePaletteClient) calls() int { return f.n }

// goodPaletteJSON is client/design's own known-good fixture (palette_test.go,
// TestNewGeneratedDerivesWhatAModelMayNotAuthor) — a dark theme that survives
// design.NewGenerated and design.Sanitize unchanged, so it exercises the wire
// path rather than the readability-repair path.
const goodPaletteJSON = `{
	"label": "Thunderhead", "blurb": "Slate and rain.",
	"ground": "#12151A", "raised": "#191D24", "sunk": "#20252E",
	"line": "#2C323C", "hair": "#232830",
	"cream": "#EEF2F7", "soft": "#B9C2CF", "dim": "#8A94A2", "read": "#DDE3EB",
	"accent": "#8FC2FF", "pos": "#6FDCA8", "neg": "#FF8E76",
	"wash": 14
}`
