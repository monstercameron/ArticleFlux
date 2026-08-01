package store

import (
	"errors"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

func TestUITranslationKeyLowercasesAndTrimsTheLocale(t *testing.T) {
	if got, want := UITranslationKey(" FR "), SystemKey("smart.ui_translation.fr"); got != want {
		t.Errorf("UITranslationKey = %q, want %q", got, want)
	}
}

func TestCanStoreSecretsRequiresA32ByteKey(t *testing.T) {
	if (&SettingsRepo{}).CanStoreSecrets() {
		t.Error("a zero-value repo (nil key) reports it can store secrets")
	}
	db := openTest(t)
	if NewSettingsRepo(db, nil).CanStoreSecrets() {
		t.Error("a nil key reports CanStoreSecrets = true")
	}
	if NewSettingsRepo(db, []byte("too-short")).CanStoreSecrets() {
		t.Error("a short key reports CanStoreSecrets = true")
	}
	key, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if !NewSettingsRepo(db, key).CanStoreSecrets() {
		t.Error("a proper 32-byte key reports CanStoreSecrets = false")
	}
}

func TestSystemValueRoundTripsAndReportsUnset(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()

	if _, err := repo.SystemValue(ctx, KeySmartModel); !errors.Is(err, ErrNoSetting) {
		t.Errorf("unset key = %v, want ErrNoSetting", err)
	}
	// by="" is the supported "no attributed user" case; updated_by references
	// users(id), so a non-empty by must name a real account.
	if err := repo.SetSystemValue(ctx, KeySmartModel, "gpt-5", ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.SystemValue(ctx, KeySmartModel)
	if err != nil {
		t.Fatal(err)
	}
	if got != "gpt-5" {
		t.Errorf("SystemValue = %q, want gpt-5", got)
	}
}

// TestModelTierFallback is TODO 11.13's own acceptance line: an instance
// that sets only smart.model behaves exactly as it does today (every tier
// resolves to it), and a call asking for a tier that IS set gets that tier's
// own model instead.
func TestModelTierFallback(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()

	// Nothing set at all: ModelForTier reports unset exactly like
	// SystemValue(KeySmartModel) would, for any tier.
	if _, err := repo.ModelForTier(ctx, ModelTierSmall); !errors.Is(err, ErrNoSetting) {
		t.Errorf("no settings at all: ModelForTier = %v, want ErrNoSetting", err)
	}

	// Only the compatibility default set: every tier falls back to it.
	if err := repo.SetSystemValue(ctx, KeySmartModel, "default-model", ""); err != nil {
		t.Fatal(err)
	}
	for _, tier := range []ModelTier{ModelTierSmall, ModelTierMid, ModelTierLarge, ModelTier("")} {
		got, err := repo.ModelForTier(ctx, tier)
		if err != nil {
			t.Fatalf("tier %q: %v", tier, err)
		}
		if got != "default-model" {
			t.Errorf("tier %q with only smart.model set: got %q, want default-model", tier, got)
		}
	}

	// The small tier gets its own model; mid and large still fall back.
	if err := repo.SetSystemValue(ctx, KeySmartModelSmall, "small-model", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.ModelForTier(ctx, ModelTierSmall); err != nil || got != "small-model" {
		t.Errorf("ModelForTier(small) = %q, %v, want small-model, nil", got, err)
	}
	if got, err := repo.ModelForTier(ctx, ModelTierMid); err != nil || got != "default-model" {
		t.Errorf("ModelForTier(mid), still unset: got %q, %v, want default-model, nil", got, err)
	}
}

// An empty value is a deliberate "off", distinguishable from a key that was
// never written at all.
func TestSetSystemValueEmptyStringIsDistinctFromUnset(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()
	if err := repo.SetSystemValue(ctx, KeyRetentionItemDays, "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.SystemValue(ctx, KeyRetentionItemDays)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("SystemValue = %q, want empty string", got)
	}
}

func TestSetSystemValueOverwritesRatherThanDuplicating(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()
	if err := repo.SetSystemValue(ctx, KeySmartModel, "a", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSystemValue(ctx, KeySmartModel, "b", ""); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM settings WHERE scope='system' AND key=?`, string(KeySmartModel)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows for one key after two writes, want 1", n)
	}
	got, _ := repo.SystemValue(ctx, KeySmartModel)
	if got != "b" {
		t.Errorf("SystemValue = %q, want the latest write (b)", got)
	}
}

func TestSystemSecretRefusesWithoutAKey(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()
	if err := repo.SetSystemSecret(ctx, KeyOpenAIAPIKey, "sk-abc", "u1"); !errors.Is(err, secret.ErrKeyLength) {
		t.Errorf("SetSystemSecret without a key = %v, want ErrKeyLength", err)
	}
	if _, err := repo.SystemSecret(ctx, KeyOpenAIAPIKey); !errors.Is(err, secret.ErrKeyLength) {
		t.Errorf("SystemSecret without a key = %v, want ErrKeyLength", err)
	}
}

func TestSystemSecretEncryptsAtRestAndDecryptsOnRead(t *testing.T) {
	db := openTest(t)
	key, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSettingsRepo(db, key)
	ctx := t.Context()

	if err := repo.SetSystemSecret(ctx, KeyOpenAIAPIKey, "sk-super-secret", ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.SystemSecret(ctx, KeyOpenAIAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sk-super-secret" {
		t.Errorf("SystemSecret = %q, want the plaintext back", got)
	}

	// The stored row must not contain the plaintext — a database dump should
	// not be a pile of usable keys.
	raw, err := repo.SystemValue(ctx, KeyOpenAIAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "sk-super-secret" {
		t.Fatal("the secret was stored in plaintext")
	}
}

// SetSystemSecret with an empty value CLEARS it — the settings screen's "remove
// the key" affordance.
func TestSetSystemSecretEmptyValueClears(t *testing.T) {
	db := openTest(t)
	key, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	repo := NewSettingsRepo(db, key)
	ctx := t.Context()
	if err := repo.SetSystemSecret(ctx, KeyOpenAIAPIKey, "sk-abc", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSystemSecret(ctx, KeyOpenAIAPIKey, "  ", ""); err != nil {
		t.Fatal(err)
	}
	got, err := repo.SystemSecret(ctx, KeyOpenAIAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("SystemSecret after clearing = %q, want empty", got)
	}
}

func TestDeleteSystemValueMakesSystemValueReportUnsetAgain(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()
	if err := repo.SetSystemValue(ctx, KeySmartModel, "x", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteSystemValue(ctx, KeySmartModel); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SystemValue(ctx, KeySmartModel); !errors.Is(err, ErrNoSetting) {
		t.Errorf("SystemValue after delete = %v, want ErrNoSetting", err)
	}
}

// Distinct keys must not collide, and the translation-cache prefix is where a
// naive key scheme would (two locales sharing one row).
func TestDistinctSystemKeysDoNotCollide(t *testing.T) {
	db := openTest(t)
	repo := NewSettingsRepo(db, nil)
	ctx := t.Context()
	if err := repo.SetSystemValue(ctx, UITranslationKey("fr"), "bonjour-catalog", ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetSystemValue(ctx, UITranslationKey("es"), "hola-catalog", ""); err != nil {
		t.Fatal(err)
	}
	fr, err := repo.SystemValue(ctx, UITranslationKey("fr"))
	if err != nil {
		t.Fatal(err)
	}
	es, err := repo.SystemValue(ctx, UITranslationKey("es"))
	if err != nil {
		t.Fatal(err)
	}
	if fr != "bonjour-catalog" || es != "hola-catalog" {
		t.Errorf("fr=%q es=%q, want distinct catalogs", fr, es)
	}
}
