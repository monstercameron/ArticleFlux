package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/themewire"
	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// The guards for §20.16.3's transport half.
//
// Three things are being asserted, and none of them is "the model returns a nice
// palette" — that is not testable and is not the risk. The risks are that the
// drift is derived from the wrong thing, that the wrong people can spend on it,
// and that a palette crosses the wire without being checked.

// themingFor builds a SmartServer with theming wired over a real database.
func themingFor(t *testing.T, sc store.Scope) (*SmartServer, *store.ReaderRepo) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "theme.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	// A real tenant and user, because topics and preferences are both foreign-keyed
	// to one. A Scope naming rows that do not exist is not a lighter fixture, it is
	// a different test — every write fails on the constraint.
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: sc.TenantID, Name: "Test", UserID: sc.UserID,
		Username: "reader", Hash: "x", Role: sc.Role,
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := NewSmartServer(
		store.NewSettingsRepo(db, nil),
		llm.New(func(context.Context) string { return "" }),
		nil,
		func(context.Context) (store.Scope, error) { return sc, nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	).WithTheming(nil, repo)
	return s, repo
}

func scopeForTheme() store.Scope {
	return store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
}

// --- taste ------------------------------------------------------------------------

// TestTasteLeadsWithRisingAndDropsDormant is the one that decides whether the
// feature is about the reader or about their archive.
//
// `Topics` returns largest first, which is right for a Trends screen and wrong
// here: a drifting interface answers "what are you moving toward", and §18.3's
// whole premise is that interests expire. A theme still tuned to a topic somebody
// stopped reading in March is the app being confidently out of date about its
// owner.
func TestTasteLeadsWithRisingAndDropsDormant(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	// The labels are chosen so that the order `Topics` returns them in — member
	// count descending, then label ascending — puts the RISING one last. Without
	// that, a test where the rising topic already sorted first would pass whether
	// or not the trend was consulted at all.
	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Alpine hiking", TopTerms: []string{"alps", "trail"}, Trend: topics.TrendSteady},
		{Label: "Kubernetes", TopTerms: []string{"k8s", "pods"}, Trend: topics.TrendDormant},
		{Label: "Zeppelins", TopTerms: []string{"airship", "helium"}, Trend: topics.TrendRising},
	}); err != nil {
		t.Fatalf("topics: %v", err)
	}

	labels, why := s.taste(ctx, sc)
	if why != "Zeppelins" {
		t.Errorf("the drift is following %q; a rising topic should lead", why)
	}
	if got := strings.Join(labels, " | "); got != "Zeppelins | Alpine hiking" {
		t.Errorf("taste is %q — a dormant topic is over, not faint, and must be dropped", got)
	}
}

// TestSuppressedTopicsAreNotATaste. Marking a topic "not an interest" is a reader
// telling the app it is wrong about them (§18.2). A feature that then painted their
// interface in that topic's colours would be the app arguing.
func TestSuppressedTopicsAreNotATaste(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "Crypto", TopTerms: []string{"crypto", "token"}, Trend: topics.TrendRising},
		{Label: "Typography", TopTerms: []string{"kerning", "opsz"}, Trend: topics.TrendSteady},
	}); err != nil {
		t.Fatalf("topics: %v", err)
	}
	rows, err := repo.Topics(ctx, sc)
	if err != nil {
		t.Fatal(err)
	}
	var cryptoID string
	for _, r := range rows {
		if r.Label == "Crypto" {
			cryptoID = r.ID
		}
	}
	if cryptoID == "" {
		t.Fatal("the topic under test was not stored")
	}
	if err := repo.SuppressTopic(ctx, sc, cryptoID, true); err != nil {
		t.Fatalf("suppress: %v", err)
	}

	labels, why := s.taste(ctx, sc)
	for _, l := range labels {
		if l == "Crypto" {
			t.Error("a suppressed topic is still steering the theme")
		}
	}
	if why != "Typography" {
		t.Errorf("the drift is following %q", why)
	}
}

// TestTasteIsCappedAndTheSignatureIsStable.
//
// The signature is what stops a feature that repaints daily from being a REQUEST
// that happens daily: the client compares it and only asks again when the taste
// behind it moved. A fingerprint that changed on its own would restart the drift
// for nothing, over and over.
func TestTasteIsCappedAndTheSignatureIsStable(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	var ts []topics.Topic
	for _, name := range []string{"one", "two", "three", "four", "five", "six", "seven"} {
		ts = append(ts, topics.Topic{
			Label: name, TopTerms: []string{name, name + "x"},
			Trend: topics.TrendSteady,
		})
	}
	if err := repo.ReplaceTopics(ctx, sc, ts); err != nil {
		t.Fatalf("topics: %v", err)
	}

	labels, _ := s.taste(ctx, sc)
	if len(labels) != MaxTasteLabels {
		t.Errorf("seven topics produced %d labels, want %d", len(labels), MaxTasteLabels)
	}

	first := tasteSignature(labels)
	again, _ := s.taste(ctx, sc)
	if tasteSignature(again) != first {
		t.Error("two identical reads produced two signatures; the drift would restart itself")
	}
	// Order is part of it on purpose: the first label picks the hue on the
	// deterministic path, so the same members in a different order are a different
	// room.
	swapped := append([]string{labels[1], labels[0]}, labels[2:]...)
	if tasteSignature(swapped) == first {
		t.Error("reordering the interests did not change the signature")
	}
}

// TestNoTopicsIsNotAnError. Cold start (§18.4): the honest answer is that there is
// nothing to attune to yet, and the client keeps the target it has.
func TestNoTopicsIsNotAnError(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)

	res, err := s.SuggestTheme(context.Background(), &pb.SuggestThemeRequest{
		Base: themewire.Tokens(design.Fanciful),
	})
	if err != nil {
		t.Fatalf("a reader with no topics got an error: %v", err)
	}
	if res.GetTarget() != nil || res.GetWhy() != "" || res.GetSignature() != "" {
		t.Error("a target was invented for a reader whose topics have not formed")
	}
}

// --- the deterministic rung -------------------------------------------------------

// TestSuggestWorksWithNoKeyAtAll is the ladder §11 and §18.7 both describe:
// deterministic first, model last. An instance with no credential must still
// drift, because the free answer is a complete answer.
func TestSuggestWorksWithNoKeyAtAll(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	if err := repo.ReplaceTopics(ctx, sc, []topics.Topic{
		{Label: "NPU inference", TopTerms: []string{"npu", "qnn"}, Trend: topics.TrendRising},
	}); err != nil {
		t.Fatalf("topics: %v", err)
	}

	res, err := s.SuggestTheme(ctx, &pb.SuggestThemeRequest{Base: themewire.Tokens(design.Ink)})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if res.GetSmart() {
		t.Error("an instance with no key reported a model-written palette")
	}
	if res.GetWhy() != "NPU inference" {
		t.Errorf("why is %q; the screen has nothing to explain the drift with", res.GetWhy())
	}
	target, err := themewire.Theme(res.GetTarget())
	if err != nil {
		t.Fatalf("the target did not survive validation: %v", err)
	}
	if target.Ground == design.Ink.Ground {
		t.Error("the deterministic target is the base unchanged, so nothing ever drifts")
	}
	if target.Tone != design.Ink.Tone {
		t.Errorf("the target's tone is %q, the base's is %q — design.Blend would "+
			"refuse to move at all", target.Tone, design.Ink.Tone)
	}
}

// TestSmartDriftNeedsConsent. Storing a key is not consent to use it, and
// consenting to one egress is not consenting to the next (see AttunePrefKey).
func TestSmartDriftNeedsConsent(t *testing.T) {
	sc := scopeForTheme()
	s, repo := themingFor(t, sc)
	ctx := context.Background()

	if s.attuneSmart(ctx, sc) {
		t.Error("a reader who has never opened the screen is opted in")
	}
	if err := repo.SetPrefs(ctx, sc, store.Prefs{AttunePrefKey: "true"}); err != nil {
		t.Fatalf("prefs: %v", err)
	}
	if !s.attuneSmart(ctx, sc) {
		t.Error("an explicit yes was not read back")
	}
	if err := repo.SetPrefs(ctx, sc, store.Prefs{AttunePrefKey: "false"}); err != nil {
		t.Fatalf("prefs: %v", err)
	}
	if s.attuneSmart(ctx, sc) {
		t.Error("an explicit no was ignored")
	}
}

// --- validation and refusals ------------------------------------------------------

// TestAnEmptyPromptIsRefusedBeforeItCostsARateLimitSlot.
//
// The slot matters: a reader iterating on a prompt has twenty an hour, and
// spending one on a button pressed with an empty box is spending one on nothing.
func TestAnEmptyPromptIsRefusedBeforeItCostsARateLimitSlot(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)
	// A generator has to exist, or the empty-prompt branch is never reached.
	s.palettes = nonCallingPalettes()

	for i := 0; i < 30; i++ {
		_, err := s.ComposeTheme(context.Background(), &pb.ComposeThemeRequest{
			Prompt: "   ", Tone: "dark",
		})
		if code := status.Code(err); code != codes.InvalidArgument {
			t.Fatalf("attempt %d: code %v, want InvalidArgument — an empty prompt is "+
				"consuming the reader's budget", i, code)
		}
	}
}

// TestComposeIsRateLimitedPerUser. This is the only thing between a member account
// and the instance's OpenAI bill on this surface, because — unlike the rest of
// SmartService — these two methods are deliberately not gated on a role.
func TestComposeIsRateLimitedPerUser(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)
	s.palettes = nonCallingPalettes()

	// The generator has no key, so every attempt fails at the provider — which is
	// after the limiter, which is what this is counting.
	throttled := false
	for i := 0; i < 60; i++ {
		_, err := s.ComposeTheme(context.Background(), &pb.ComposeThemeRequest{
			Prompt: "a cold library at 2am", Tone: "dark",
		})
		if status.Code(err) == codes.ResourceExhausted {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Error("sixty compositions in a row were all admitted; nothing bounds the spend")
	}
}

// TestABaseThatWouldNotBeStoredIsNotAccepted.
//
// The server cannot trust a client for the same reason the client cannot trust a
// preference (design.DecodeTheme): the value may have been written by an older
// build or by a hand editing the database. `--shadow` is the case that matters —
// it is the one token whose value is not a colour, and it ends up on
// documentElement.style.
func TestABaseThatWouldNotBeStoredIsNotAccepted(t *testing.T) {
	sc := scopeForTheme()
	s, _ := themingFor(t, sc)

	for _, tc := range []struct {
		name string
		mut  func(*pb.ThemeTokens)
	}{
		{"a missing base", nil},
		{"a colour that is not one", func(b *pb.ThemeTokens) { b.Ground = "plum" }},
		{"an injected shadow", func(b *pb.ThemeTokens) { b.Shadow = "0 0 0 url(http://x/y)" }},
		{"a css function", func(b *pb.ThemeTokens) { b.Accent = "var(--x)" }},
	} {
		req := &pb.SuggestThemeRequest{}
		if tc.mut != nil {
			req.Base = themewire.Tokens(design.Fanciful)
			tc.mut(req.Base)
		}
		_, err := s.SuggestTheme(context.Background(), req)
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s was accepted as a base (code %v)", tc.name, status.Code(err))
		}
	}
}

// TestAnAbsentToneIsDarkRatherThanTheModelsChoice. A light palette handed to a
// dark base cannot be drifted toward at all — design.Blend refuses to cross the
// tone boundary — so an unset tone has to resolve to something, and the house
// theme is dark.
func TestAnAbsentToneIsDarkRatherThanTheModelsChoice(t *testing.T) {
	for _, in := range []string{"", "  ", "nonesuch", "DARK"} {
		if got := toneOf(in); got != design.ToneDark {
			t.Errorf("toneOf(%q) = %q", in, got)
		}
	}
	for _, in := range []string{"light", "LIGHT", " Light "} {
		if got := toneOf(in); got != design.ToneLight {
			t.Errorf("toneOf(%q) = %q", in, got)
		}
	}
}

// --- helpers ----------------------------------------------------------------------

// nonCallingPalettes is a generator over a client with no key.
//
// Every call fails at llm.Configured, which is cheaper than a fake HTTP server and
// is the SAME failure an unconfigured instance has — so the tests that use it are
// exercising the real unconfigured path rather than a mock of it. It exists only so
// that `s.palettes != nil`, which is what the empty-prompt and rate-limit branches
// need in order to be reached at all.
func nonCallingPalettes() *smart.Palettes {
	return smart.NewPalettes(llm.New(func(context.Context) string { return "" }), nil)
}
