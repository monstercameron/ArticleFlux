package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// System settings: the §6.3 registry's `scope='system'` layer.
//
// This is instance configuration, not user data — the OpenAI API key, the
// Smart+ model, the translation cache. It is deliberately NOT in `user_prefs`:
// prefs are per-user and per-tenant, and an API key that every tenant could set
// for themselves would be an instance with N egress paths and N bills. There is
// one server, it has one key, and the person who runs it sets it.
//
// That is also why these methods take no Scope. They are global rows in the
// same sense `sources` and `items` are global under A14, and the guard's
// unscopedByDesign list records that rather than letting it be a silent
// omission. **Authorisation happens at the transport**, where the caller is
// checked for being an owner — a repository method that reads a system setting
// must not be reachable from an unauthenticated RPC.

// SystemKey names a system setting. A named type rather than bare strings so a
// typo is a compile error and the whole set is enumerable from one place.
type SystemKey string

const (
	// KeyOpenAIAPIKey holds the Smart+ credential, encrypted at rest.
	KeyOpenAIAPIKey SystemKey = "smart.openai_api_key"
	// KeySmartModel is the compatibility default: the model a call uses when
	// it does not ask for a tier, and the fallback for a tier that has no
	// model of its own set (TODO 11.13). It used to be documented as "the
	// model every Smart+ feature uses. One setting, not one per feature" —
	// true when every Smart+ call made one comparable request, which stopped
	// being true the moment FluxCast made two calls with different economics
	// in the same second (a cheap, frequent planner pass; an expensive,
	// per-story writer). An instance that sets only this key still behaves
	// exactly as before: every tier falls back to it.
	KeySmartModel SystemKey = "smart.model"
	// KeySmartModelSmall / KeySmartModelMid / KeySmartModelLarge hold the
	// model id for one price/capability tier apiece (ModelTierSmall /
	// ModelTierMid / ModelTierLarge). Unset means "use KeySmartModel" — see
	// SettingsRepo.ModelForTier, the one place that fallback is implemented.
	KeySmartModelSmall SystemKey = "smart.model.small"
	KeySmartModelMid   SystemKey = "smart.model.mid"
	KeySmartModelLarge SystemKey = "smart.model.large"
	// KeyRetentionItemDays is how many days of articles this instance keeps.
	// "0" — and an absent row — mean forever, which is the stated default
	// (TODO F36). A SystemKey rather than a per-user preference because items
	// are global (A14): a window is a decision about rows that belong to no
	// tenant, and offering it per-user would be offering to delete somebody
	// else's reading.
	KeyRetentionItemDays SystemKey = "retention.items.days"
	// KeySmartBudgetUSD is the ceiling on Smart+ spending for this instance, in
	// US dollars, as a decimal string. Absent — and "0" — mean no ceiling,
	// which is the default and has to be: a cap nobody set must not stop an
	// instance that has been working for months (T4, plan Phase 4 / P4.2).
	//
	// A SystemKey rather than a per-reader preference for the reason
	// KeyOpenAIAPIKey is one: the bill belongs to whoever supplied the
	// credential, and a per-reader cap would let one reader spend another
	// reader's allowance by having none of their own.
	//
	// Read per call rather than at boot, so raising it while a job is hitting
	// it takes effect without a restart — see llm.CapFunc.
	KeySmartBudgetUSD SystemKey = "smart.budget.usd"
	// KeySmartSpendTotal is what this instance has ALREADY spent, as the JSON
	// form of llm.Cost. It is the other half of KeySmartBudgetUSD and exists
	// for one reason: a total kept in memory resets on every start, so a $5 cap
	// was $5 per restart and a crash loop had no cap at all.
	//
	// Written after every priced call and read once at boot. Deleting the row
	// resets the meter, which is the only way to do it and is deliberate — a
	// "reset" button would need a story about who is allowed to press it, and
	// this is an operator action on a self-hosted box.
	//
	// Not a secret: it is a number about this instance's own spending, and
	// encrypting it would mean an instance that lost secrets.key also lost its
	// ceiling.
	KeySmartSpendTotal SystemKey = "smart.spend.total"
	// KeyUITranslationPrefix is joined with a locale to hold that language's
	// translated catalog: "smart.ui_translation.fr".
	KeyUITranslationPrefix SystemKey = "smart.ui_translation."
)

// UITranslationKey names the cached catalog for one locale.
func UITranslationKey(locale string) SystemKey {
	return SystemKey(string(KeyUITranslationPrefix) + strings.ToLower(strings.TrimSpace(locale)))
}

// ErrNoSetting means the key has never been written. Distinct from an empty
// value, which is a deliberate "turn this off".
var ErrNoSetting = errors.New("store: setting not set")

// SettingsRepo reads and writes the system layer of the settings registry.
//
// It holds the encryption key rather than taking one per call, because a caller
// that supplies the key per call is a caller that can supply the wrong one and
// get ErrCiphertext from a row that is perfectly intact.
type SettingsRepo struct {
	db *DB
	// encKey seals the values marked secret. Nil means secrets cannot be
	// stored — SetSecret refuses rather than writing plaintext, which is the
	// only safe direction for a failure here to fall.
	encKey []byte
}

// NewSettingsRepo wires the system-settings surface. encKey must be 32 bytes
// (secret.NewKey) or nil.
func NewSettingsRepo(db *DB, encKey []byte) *SettingsRepo {
	return &SettingsRepo{db: db, encKey: encKey}
}

// CanStoreSecrets reports whether an encryption key was supplied. The settings
// screen asks, so it can say "the server cannot store a key" instead of
// accepting one and losing it.
func (r *SettingsRepo) CanStoreSecrets() bool { return r != nil && len(r.encKey) == 32 }

// SystemValue returns a plain (non-secret) system setting.
//
// Values are stored as JSON because the column says value_json and because a
// setting that is a number today is a struct next quarter. A string round-trips
// as a JSON string.
func (r *SettingsRepo) SystemValue(ctx context.Context, key SystemKey) (string, error) {
	var raw string
	err := r.db.Read.QueryRowContext(ctx,
		`SELECT value_json FROM settings WHERE scope = 'system' AND scope_id IS NULL AND key = ?`,
		string(key)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoSetting
	}
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		// A value written as something other than a JSON string. Returned raw
		// rather than as an error: it is more useful to see it than to be told
		// it is unreadable.
		return raw, nil
	}
	return s, nil
}

// ModelTier is which of the three price/capability tiers a Smart+ call is
// costed against (TODO 11.13, plan §29). A tier names RELATIVE cost, not a
// specific model — the caller says "this is cheap and frequent" or "this is
// the one place deliberation is the product being bought", and the
// instance's settings say what that currently resolves to. No tier's model
// id is written into this repository until G6 answers what Sol/Terra/Luna
// actually are; this type and ModelForTier are the indirection that lets
// that answer be a settings change instead of a call-site rewrite.
type ModelTier string

const (
	ModelTierSmall ModelTier = "small"
	ModelTierMid   ModelTier = "mid"
	ModelTierLarge ModelTier = "large"
)

// tierKey maps a ModelTier to the SystemKey holding its model id. An
// unrecognised tier resolves to KeySmartModel itself, so a caller passing a
// zero-value ModelTier gets the same compatibility default a tier miss falls
// back to, rather than reading a key that was never set for a different
// reason.
func tierKey(tier ModelTier) SystemKey {
	switch tier {
	case ModelTierSmall:
		return KeySmartModelSmall
	case ModelTierMid:
		return KeySmartModelMid
	case ModelTierLarge:
		return KeySmartModelLarge
	default:
		return KeySmartModel
	}
}

// ModelForTier resolves the model id a call at the given tier should use:
// the tier's own setting if one is set, else KeySmartModel (TODO 11.13's
// compatibility default), else ErrNoSetting — mirroring SystemValue's own
// contract exactly, so the existing `if err != nil || strings.TrimSpace(m)
// == "" { return llm.DefaultModel }` pattern at every current call site
// needs no new branch to adopt this.
func (r *SettingsRepo) ModelForTier(ctx context.Context, tier ModelTier) (string, error) {
	if m, err := r.SystemValue(ctx, tierKey(tier)); err == nil && strings.TrimSpace(m) != "" {
		return strings.TrimSpace(m), nil
	}
	return r.SystemValue(ctx, KeySmartModel)
}

// SetSystemValue writes a plain system setting. by is the user id to record, or
// empty.
func (r *SettingsRepo) SetSystemValue(ctx context.Context, key SystemKey, value string, by string) error {
	enc, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return r.write(ctx, key, string(enc), by)
}

// SystemSecret returns a decrypted secret setting.
//
// A row that cannot be decrypted returns the error rather than an empty string.
// Silently treating an undecryptable key as "not configured" would tell the
// operator to paste their key again, which would work — and would quietly
// destroy whatever the old key was protecting.
func (r *SettingsRepo) SystemSecret(ctx context.Context, key SystemKey) (string, error) {
	if !r.CanStoreSecrets() {
		return "", secret.ErrKeyLength
	}
	sealed, err := r.SystemValue(ctx, key)
	if err != nil {
		return "", err
	}
	if sealed == "" {
		return "", nil
	}
	plain, err := secret.Decrypt(r.encKey, sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// SetSystemSecret encrypts and stores a secret setting. An empty value CLEARS it,
// which is how the settings screen offers "remove the key" without a second
// RPC that means almost the same thing.
func (r *SettingsRepo) SetSystemSecret(ctx context.Context, key SystemKey, value string, by string) error {
	if !r.CanStoreSecrets() {
		// Refuse rather than store plaintext. An operator who is told this
		// failed will fix it; one whose key was written in the clear will never
		// find out.
		return secret.ErrKeyLength
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return r.SetSystemValue(ctx, key, "", by)
	}
	sealed, err := secret.Encrypt(r.encKey, []byte(value))
	if err != nil {
		return err
	}
	return r.SetSystemValue(ctx, key, sealed, by)
}

// DeleteSystemValue removes a system setting entirely, so SystemValue reports
// ErrNoSetting again.
func (r *SettingsRepo) DeleteSystemValue(ctx context.Context, key SystemKey) error {
	_, err := r.db.Write.ExecContext(ctx,
		`DELETE FROM settings WHERE scope = 'system' AND scope_id IS NULL AND key = ?`,
		string(key))
	return err
}

func (r *SettingsRepo) write(ctx context.Context, key SystemKey, valueJSON string, by string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var updatedBy any
	if by != "" {
		updatedBy = by
	}
	// The table's uniqueness is enforced by an expression index
	// (`ifnull(scope_id, '')`), not by a plain UNIQUE constraint, so ON
	// CONFLICT cannot name the columns — SQLite will not match a partial or
	// expression index by column list. Delete-then-insert inside the same
	// statement pair is the portable equivalent, and it is what the schema
	// comment in 0013_platform.sql is warning about.
	return r.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM settings WHERE scope = 'system' AND scope_id IS NULL AND key = ?`,
			string(key)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings (scope, scope_id, key, value_json, updated_at, updated_by)
			 VALUES ('system', NULL, ?, ?, ?, ?)`,
			string(key), valueJSON, now, updatedBy)
		return err
	})
}
