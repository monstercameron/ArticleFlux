package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// Rotating the key that seals every stored credential.
//
// The property that matters most is the NEGATIVE one: a rotation that fails
// halfway leaves a database sealed under two keys with no marker saying which
// row is which, which is unreadable by both and strictly worse than the
// exposure somebody was rotating to answer.

func rotateFixture(t *testing.T) (*DB, *SettingsRepo, []byte) {
	t.Helper()
	db := openTest(t)
	oldKey, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	return db, NewSettingsRepo(db, oldKey), oldKey
}

// The whole point: what was readable under the old key is readable under the
// new one, and nothing else changed.
func TestRotationRe_sealsEverySecret(t *testing.T) {
	db, settings, oldKey := rotateFixture(t)
	ctx := context.Background()

	const apiKey = "sk-the-instances-smart-plus-key"
	if err := settings.SetSystemSecret(ctx, KeyOpenAIAPIKey, apiKey, ""); err != nil {
		t.Fatal(err)
	}
	// A plain setting too, to prove the rotation does not touch what it should
	// not: an unsealed row run through the decrypt path would be destroyed.
	if err := settings.SetSystemValue(ctx, KeySmartModel, "gpt-5-mini", ""); err != nil {
		t.Fatal(err)
	}

	newKey, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	moved, err := db.RotateSecretKey(ctx, oldKey, newKey)
	if err != nil {
		t.Fatalf("RotateSecretKey: %v", err)
	}
	if moved.Settings != 1 {
		t.Errorf("Settings = %d, want 1", moved.Settings)
	}

	// Readable under the NEW key.
	after := NewSettingsRepo(db, newKey)
	got, err := after.SystemSecret(ctx, KeyOpenAIAPIKey)
	if err != nil {
		t.Fatalf("reading with the new key: %v", err)
	}
	if got != apiKey {
		t.Errorf("secret = %q, want %q", got, apiKey)
	}
	// And NOT under the old one, or the rotation did not happen.
	if _, err := settings.SystemSecret(ctx, KeyOpenAIAPIKey); err == nil {
		t.Error("the old key still decrypts the secret; nothing was rotated")
	}
	// The plain setting is untouched.
	if m, err := after.SystemValue(ctx, KeySmartModel); err != nil || m != "gpt-5-mini" {
		t.Errorf("the plain setting became %q/%v; rotation must not touch unsealed rows", m, err)
	}
}

// The wrong old key aborts and writes nothing. Continuing past a value that
// will not decrypt would re-seal the readable rows and orphan the rest.
func TestTheWrongOldKeyChangesNothing(t *testing.T) {
	db, settings, _ := rotateFixture(t)
	ctx := context.Background()

	const apiKey = "sk-original"
	if err := settings.SetSystemSecret(ctx, KeyOpenAIAPIKey, apiKey, ""); err != nil {
		t.Fatal(err)
	}
	wrong, _ := secret.NewKey()
	newKey, _ := secret.NewKey()

	moved, err := db.RotateSecretKey(ctx, wrong, newKey)
	if err == nil {
		t.Fatal("a rotation with the wrong old key reported success")
	}
	if moved != (RotatedSecrets{}) {
		t.Errorf("counts = %+v on a rollback; the caller would report work that did not happen", moved)
	}
	if !strings.Contains(err.Error(), "old key") {
		t.Errorf("err = %v; want it to point at the key rather than at the cipher", err)
	}
	// Still readable under the real old key — the transaction rolled back.
	if got, gerr := settings.SystemSecret(ctx, KeyOpenAIAPIKey); gerr != nil || got != apiKey {
		t.Errorf("after a failed rotation the secret reads %q/%v; the rollback did not hold", got, gerr)
	}
}

// Rotating to the same key is refused rather than treated as a no-op. Somebody
// running this has decided the current key is compromised, and reporting
// success over an unchanged key is the worst possible answer.
func TestRotatingToTheSameKeyIsRefused(t *testing.T) {
	db, _, oldKey := rotateFixture(t)
	if _, err := db.RotateSecretKey(context.Background(), oldKey, oldKey); err == nil {
		t.Error("rotating to the same key reported success")
	}
}

func TestRotationRejectsAKeyOfTheWrongLength(t *testing.T) {
	db, _, oldKey := rotateFixture(t)
	ctx := context.Background()
	if _, err := db.RotateSecretKey(ctx, oldKey, []byte("short")); err == nil {
		t.Error("a five-byte new key was accepted")
	}
	if _, err := db.RotateSecretKey(ctx, []byte("short"), oldKey); err == nil {
		t.Error("a five-byte old key was accepted")
	}
}

// An instance with nothing sealed rotates cleanly. The file is still worth
// replacing — it is the thing every backup has a copy of — so this must not
// error, and it must not claim to have moved anything.
func TestRotatingAnInstanceWithNoSecretsIsClean(t *testing.T) {
	db, _, oldKey := rotateFixture(t)
	newKey, _ := secret.NewKey()

	moved, err := db.RotateSecretKey(context.Background(), oldKey, newKey)
	if err != nil {
		t.Fatalf("RotateSecretKey on an instance with no secrets: %v", err)
	}
	if moved != (RotatedSecrets{}) {
		t.Errorf("counts = %+v, want zeroes", moved)
	}
}

// The count is what an operator checks the rotation against before running it.
func TestCountSealedSecretsCountsWhatRotationWouldMove(t *testing.T) {
	db, settings, _ := rotateFixture(t)
	ctx := context.Background()

	before, err := db.CountSealedSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Settings != 0 {
		t.Errorf("a fresh instance reports %d sealed settings", before.Settings)
	}
	if err := settings.SetSystemSecret(ctx, KeyOpenAIAPIKey, "sk-x", ""); err != nil {
		t.Fatal(err)
	}
	after, err := db.CountSealedSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Settings != 1 {
		t.Errorf("Settings = %d after storing one secret, want 1", after.Settings)
	}
}

// Every key written through SetSystemSecret must be in SecretSystemKeys.
//
// # Why a source grep
//
// There is no column marking a settings row as sealed — `SetSystemSecret`
// encrypts and `SetSystemValue` does not, and which one a key uses is decided
// at its call site. A sealed key missing from the rotation list survives a
// rotation UNROTATED and is unreadable afterwards, which is the exact failure
// this package exists to prevent, reintroduced by omission.
//
// An omission has no behaviour: adding a second sealed setting works perfectly
// until somebody rotates, and then the thing they were protecting is gone.
func TestEverySealedSettingIsListedForRotation(t *testing.T) {
	listed := map[string]bool{}
	for _, k := range SecretSystemKeys {
		listed[string(k)] = true
	}

	// The tree, from internal/store upward. Two levels covers internal/* and
	// cmd/*, which is everything that can reach a SettingsRepo.
	root := filepath.Join("..", "..")
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, line := range strings.Split(string(body), "\n") {
			i := strings.Index(line, "SetSystemSecret(")
			if i < 0 {
				continue
			}
			// The key is the argument after ctx: `SetSystemSecret(ctx,
			// store.KeyX, …)`. Matched on the constant NAME rather than parsed,
			// because a grep that tried to evaluate Go would be a worse thing
			// to own than one that occasionally asks a human to look.
			rest := line[i:]
			for _, k := range knownKeyNames() {
				if strings.Contains(rest, k.ident) && !listed[k.value] {
					offenders = append(offenders,
						path+": "+k.ident+" is sealed here but is not in SecretSystemKeys")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offenders {
		t.Error(o + "\n\tA rotation would leave it under the OLD key, unreadable, " +
			"and nothing would say so until somebody needed it.")
	}
}

// knownKeyNames pairs every SystemKey identifier with its value, so the grep
// above can recognise a call site by the constant it names.
//
// Written out rather than reflected: the constants are untyped strings in a
// const block, which reflection cannot enumerate, and a list that has to be
// extended when a key is added is the same maintenance the rotation list needs
// anyway — one place to forget instead of two.
func knownKeyNames() []struct{ ident, value string } {
	return []struct{ ident, value string }{
		{"KeyOpenAIAPIKey", string(KeyOpenAIAPIKey)},
		{"KeySmartModel", string(KeySmartModel)},
		{"KeySmartBudgetUSD", string(KeySmartBudgetUSD)},
		{"KeySmartSpendTotal", string(KeySmartSpendTotal)},
		{"KeySpeechSpendTotal", string(KeySpeechSpendTotal)},
		{"KeyRetentionItemDays", string(KeyRetentionItemDays)},
		{"KeyRetentionAttemptDays", string(KeyRetentionAttemptDays)},
		{"KeyRetentionAuditDays", string(KeyRetentionAuditDays)},
	}
}
