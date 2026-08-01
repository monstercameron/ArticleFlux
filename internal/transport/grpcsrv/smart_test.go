package grpcsrv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The config/owner-gating half of the Smart+ surface. theme_test.go covers
// ComposeTheme/SuggestTheme/taste over the same server type; this file is
// requireOwner and everything it guards.

// smartConfigServer builds a SmartServer over a real database, seeding a
// tenant/user at sc's identity when one is given, and wiring scopeOf exactly
// as given — including nil, which is requireOwner's "nobody signed in" case.
func smartConfigServer(t *testing.T, encKey []byte, sc store.Scope,
	scopeOf func(context.Context) (store.Scope, error)) (*SmartServer, *store.DB) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "smart.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if sc.TenantID != "" && sc.UserID != "" {
		if err := store.NewReaderRepo(db).CreateTenantAndUser(context.Background(), store.NewTenant{
			TenantID: sc.TenantID, Name: "Test", UserID: sc.UserID,
			Username: "owner", Hash: "x", Role: sc.Role,
			Now: time.Now().UTC().Format(time.RFC3339Nano),
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	settings := store.NewSettingsRepo(db, encKey)
	client := llm.New(func(context.Context) string { return "" })
	return NewSmartServer(settings, client, smart.NewTranslator(client, settings),
		scopeOf, slog.New(slog.NewTextHandler(io.Discard, nil))), db
}

func ownerScope(role string) store.Scope {
	return store.Scope{TenantID: "t1", UserID: "u1", Role: role}
}

func fixedScope(sc store.Scope) func(context.Context) (store.Scope, error) {
	return func(context.Context) (store.Scope, error) { return sc, nil }
}

// detailKey pulls the apierr catalog key off a status, the way a wasm client
// would to look up the localized string. Distinct from crosstenant_test.go's
// shapeOf, which compares two errors rather than reading one.
func detailKey(t *testing.T, err error) string {
	t.Helper()
	st := status.Convert(err)
	for _, d := range st.Details() {
		if ed, ok := d.(*pb.ErrorDetail); ok {
			return ed.GetKey()
		}
	}
	return ""
}

// --- requireOwner -------------------------------------------------------------

func TestRequireOwnerRefusesWithNoScopeOfWired(t *testing.T) {
	s, _ := smartConfigServer(t, nil, store.Scope{}, nil)
	_, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if codeOf(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.notSignedIn" {
		t.Errorf("key = %q, want srv.notSignedIn", key)
	}
}

func TestRequireOwnerRefusesWhenScopeOfErrors(t *testing.T) {
	scopeOf := func(context.Context) (store.Scope, error) { return store.Scope{}, errors.New("no session") }
	s, _ := smartConfigServer(t, nil, store.Scope{}, scopeOf)
	_, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if codeOf(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", codeOf(err))
	}
}

func TestRequireOwnerRefusesAnInvalidScope(t *testing.T) {
	// A scope with no tenant/user at all: store.Scope.Valid() is false, and
	// that has to be caught before the role is even looked at.
	s, _ := smartConfigServer(t, nil, store.Scope{}, fixedScope(store.Scope{Role: "admin"}))
	_, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if codeOf(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", codeOf(err))
	}
}

// TestRequireOwnerRefusesMemberAndViewer is the security-critical assertion
// this whole file exists to guard: per smart.go's own doc comment, this gate
// is "the only thing between a member account and the instance's API key."
func TestRequireOwnerRefusesMemberAndViewer(t *testing.T) {
	for _, role := range []string{"member", "viewer", "", "bogus-role"} {
		sc := ownerScope(role)
		s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
		_, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
		if codeOf(err) != codes.PermissionDenied {
			t.Errorf("role %q: code = %v, want PermissionDenied", role, codeOf(err))
		}
		if key := detailKey(t, err); key != "srv.adminOnly" {
			t.Errorf("role %q: key = %q, want srv.adminOnly", role, key)
		}
	}
}

func TestRequireOwnerAllowsAdminAndSuperadmin(t *testing.T) {
	for _, role := range []string{"admin", "superadmin"} {
		sc := ownerScope(role)
		s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
		if _, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{}); err != nil {
			t.Errorf("role %q was refused: %v", role, err)
		}
	}
}

// --- GetSmartConfig / SetSmartConfig -------------------------------------------

func TestGetSmartConfigDeniedForNonOwner(t *testing.T) {
	sc := ownerScope("member")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	if _, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{}); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestSetSmartConfigDeniedForNonOwner(t *testing.T) {
	sc := ownerScope("viewer")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	_, err := s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{Model: "gpt-5"})
	if codeOf(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestGetSmartConfigFreshInstance(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	res, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if err != nil {
		t.Fatalf("owner was refused: %v", err)
	}
	if res.GetCanStoreSecrets() {
		t.Error("no encryption key was wired; CanStoreSecrets should be false")
	}
	if res.GetConfigured() {
		t.Error("a fresh instance with no key reported Configured=true")
	}
	if res.GetDefaultModel() != llm.DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", res.GetDefaultModel(), llm.DefaultModel)
	}
	if res.GetKeyHint() != "" {
		t.Errorf("KeyHint = %q on an unconfigured instance", res.GetKeyHint())
	}
}

func TestSetSmartConfigRejectsAKeyWithTheWrongShape(t *testing.T) {
	sc := ownerScope("admin")
	encKey, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := smartConfigServer(t, encKey, sc, fixedScope(sc))
	_, err = s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{OpenaiApiKey: "not-a-key"})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.badApiKeyShape" {
		t.Errorf("key = %q, want srv.badApiKeyShape", key)
	}
}

func TestSetSmartConfigStoresAndClearsAKeyAndModel(t *testing.T) {
	sc := ownerScope("superadmin")
	encKey, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := smartConfigServer(t, encKey, sc, fixedScope(sc))
	ctx := context.Background()

	setRes, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{
		OpenaiApiKey: "sk-abcd1234wxyz", Model: "gpt-5",
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !setRes.GetConfig().GetConfigured() {
		t.Error("the response's own config did not reflect the key that was just stored")
	}

	got, err := s.GetSmartConfig(ctx, &pb.GetSmartConfigRequest{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.GetConfigured() {
		t.Error("Configured=false after a shaped key was stored")
	}
	if got.GetKeyHint() != "wxyz" {
		t.Errorf("KeyHint = %q, want the last 4 chars \"wxyz\"", got.GetKeyHint())
	}
	if got.GetModel() != "gpt-5" {
		t.Errorf("Model = %q, want gpt-5", got.GetModel())
	}

	if _, err := s.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{ClearApiKey: true}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	cleared, err := s.GetSmartConfig(ctx, &pb.GetSmartConfigRequest{})
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if cleared.GetConfigured() {
		t.Error("ClearApiKey did not clear Configured")
	}
	if cleared.GetKeyHint() != "" {
		t.Errorf("KeyHint = %q after clearing", cleared.GetKeyHint())
	}
	// The model survives a key clear — they are independent settings.
	if cleared.GetModel() != "gpt-5" {
		t.Errorf("Model = %q after clearing the key, want it to survive at gpt-5", cleared.GetModel())
	}
}

// --- config: the FromEnvironment branch ----------------------------------------

func TestConfigHonoursOpenAIAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-fromenv999")
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	res, err := s.GetSmartConfig(context.Background(), &pb.GetSmartConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.GetFromEnvironment() {
		t.Error("FromEnvironment=false with OPENAI_API_KEY set and nothing stored")
	}
	if !res.GetConfigured() {
		t.Error("Configured=false with OPENAI_API_KEY set")
	}
	if res.GetKeyHint() != "v999" {
		t.Errorf("KeyHint = %q, want the last 4 chars of the env key", res.GetKeyHint())
	}
}

// --- ListLanguages / TranslateUI -----------------------------------------------

func TestListLanguagesDeniedForNonOwner(t *testing.T) {
	sc := ownerScope("member")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	if _, err := s.ListLanguages(context.Background(), &pb.ListLanguagesRequest{}); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestListLanguagesAsOwner(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	res, err := s.ListLanguages(context.Background(), &pb.ListLanguagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetLanguages()) != len(smart.Languages) {
		t.Errorf("got %d languages, want %d", len(res.GetLanguages()), len(smart.Languages))
	}
	if res.GetSourceLocale() != i18n.DefaultLocale {
		t.Errorf("SourceLocale = %q, want %q", res.GetSourceLocale(), i18n.DefaultLocale)
	}
}

func TestTranslateUIDeniedForNonOwner(t *testing.T) {
	sc := ownerScope("viewer")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	if _, err := s.TranslateUI(context.Background(), &pb.TranslateUIRequest{Locale: "fr"}); codeOf(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

func TestTranslateUIRejectsAnUnsupportedLocale(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	_, err := s.TranslateUI(context.Background(), &pb.TranslateUIRequest{Locale: "xx-nope"})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.unsupportedLanguage" {
		t.Errorf("key = %q, want srv.unsupportedLanguage", key)
	}
}

func TestTranslateUIRejectsTheSourceLocale(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	_, err := s.TranslateUI(context.Background(), &pb.TranslateUIRequest{Locale: i18n.DefaultLocale})
	if codeOf(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.alreadySourceLanguage" {
		t.Errorf("key = %q, want srv.alreadySourceLanguage", key)
	}
}

func TestTranslateUIWithNoKeyConfigured(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	_, err := s.TranslateUI(context.Background(), &pb.TranslateUIRequest{Locale: "fr"})
	if codeOf(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.smartNoKey" {
		t.Errorf("key = %q, want srv.smartNoKey", key)
	}
}

// --- secretErr ------------------------------------------------------------------

// TestSecretErrKeyLengthWhenNoEncryptionKeyIsWired. A settings repo opened
// with a nil encryption key (smartConfigServer's default) cannot store a
// secret at all — SetSystemSecret refuses before touching the database.
func TestSecretErrKeyLengthWhenNoEncryptionKeyIsWired(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	_, err := s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{OpenaiApiKey: "sk-abc12345"})
	if codeOf(err) != codes.FailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.cannotStoreSecret" {
		t.Errorf("key = %q, want srv.cannotStoreSecret", key)
	}
}

// TestSecretErrGenericFailureIsInternal forces SetSystemSecret past the
// ErrKeyLength guard (a real 32-byte key IS wired) and into a storage write
// that fails for an unrelated reason — the database pool is closed out from
// under it. That is the only way to reach secretErr's non-ErrKeyLength branch
// without faking a SQL failure mid-transaction.
func TestSecretErrGenericFailureIsInternal(t *testing.T) {
	sc := ownerScope("admin")
	encKey, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	s, db := smartConfigServer(t, encKey, sc, fixedScope(sc))
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err = s.SetSmartConfig(context.Background(), &pb.SetSmartConfigRequest{OpenaiApiKey: "sk-abc12345"})
	if codeOf(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.saveFailed" {
		t.Errorf("key = %q, want srv.saveFailed", key)
	}
}
