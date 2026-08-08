package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// Re-sealing every stored credential under a new key.
//
// # The gap
//
// `secrets.key` seals the Smart+ API key and every mailbox password, and there
// was no way to change it. Every backup keeps a copy of it forever — `-keep`
// prunes the timestamped databases and never the keys, deliberately, because a
// key rotated out from under the copies that need it makes them unrestorable —
// and off-box shipping now sends it further still. So the file accumulates
// copies and can never be retired, which means a SUSPECTED EXPOSURE had no
// remedy short of clearing every stored secret by hand and typing them all in
// again.
//
// A rotation path is what makes that recoverable, and it is also what makes the
// backup story honest: keeping a key forever is defensible when the key can be
// replaced, and is a slow accumulation of unrevokable secrets when it cannot.
//
// # Why this is one transaction and why a partial failure aborts
//
// Every ciphertext has to move together. A database with half its rows under
// the old key and half under the new one cannot be read by either, and there is
// no marker on a row saying which — so a rotation that stopped halfway would be
// strictly worse than the exposure it was answering. Everything is decrypted
// first, and only then written, inside one transaction: if any single value
// fails to decrypt with the old key, nothing is written at all.
//
// That is also why the CALLER replaces the key file only after this returns
// nil. The database is the thing that cannot be half-done; the file is a single
// atomic rename afterwards.

// SecretSystemKeys are the settings rows sealed under the instance key.
//
// A list rather than a column flag, because there is no flag: `SetSystemSecret`
// encrypts and `SetSystemValue` does not, and which a key uses is decided by
// its call site. That is fine for reading — a caller knows which function it
// wants — and not fine for rotation, which has to find every sealed row without
// being able to ask one.
//
// So the set is written down here, and `TestEverySealedSettingIsListedForRotation`
// greps the tree for `SetSystemSecret` call sites and fails if one names a key
// this list does not. A sealed setting missing from here would survive a
// rotation unrotated and be unreadable afterwards, which is the failure this
// whole file exists to prevent, reintroduced by omission.
var SecretSystemKeys = []SystemKey{KeyOpenAIAPIKey}

// RotatedSecrets counts what a rotation moved.
type RotatedSecrets struct {
	// Settings is how many sealed settings rows were re-encrypted.
	Settings int
	// Mailboxes is how many mailbox passwords were re-encrypted.
	Mailboxes int
}

// RotateSecretKey re-encrypts every stored credential from oldKey to newKey.
//
// It does NOT touch the key file. The caller replaces that after this returns
// nil, because the database is the part that cannot be half-applied and the
// file is one rename.
//
// A value that will not decrypt with oldKey aborts the whole rotation. That is
// almost always "you gave me the wrong old key", and continuing past it would
// re-seal the readable rows and orphan the rest.
func (db *DB) RotateSecretKey(ctx context.Context, oldKey, newKey []byte) (RotatedSecrets, error) {
	var out RotatedSecrets
	if len(oldKey) != 32 || len(newKey) != 32 {
		return out, secret.ErrKeyLength
	}
	if string(oldKey) == string(newKey) {
		// Refused rather than treated as a no-op. Somebody running this has
		// decided the current key is compromised, and "done" over a key that
		// did not change is the worst possible thing to tell them.
		return out, errors.New("store: the new key is identical to the old one")
	}

	err := db.Tx(ctx, func(tx *sql.Tx) error {
		// --- settings -------------------------------------------------------
		for _, key := range SecretSystemKeys {
			var sealed string
			err := tx.QueryRowContext(ctx,
				`SELECT value_json FROM settings
				  WHERE scope = 'system' AND scope_id IS NULL AND key = ?`,
				string(key)).Scan(&sealed)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			// Settings values are stored as JSON, so the ciphertext arrives
			// quoted. Unwrapping it here rather than reusing SystemSecret
			// because that method takes the repo's key, and the whole point of
			// this function is that two keys are in play at once.
			plain, err := unsealJSON(oldKey, sealed)
			if err != nil {
				return fmt.Errorf("store: %s will not decrypt with the old key "+
					"(is it the right one?): %w", key, err)
			}
			if plain == "" {
				continue
			}
			resealed, err := secret.Encrypt(newKey, []byte(plain))
			if err != nil {
				return err
			}
			enc, err := jsonString(resealed)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE settings SET value_json = ?
				  WHERE scope = 'system' AND scope_id IS NULL AND key = ?`,
				enc, string(key)); err != nil {
				return err
			}
			out.Settings++
		}

		// --- mailbox passwords ----------------------------------------------
		//
		// Read entirely before writing. Iterating a *sql.Rows while issuing
		// UPDATEs on the same transaction is the classic way to deadlock a
		// single-connection write pool, and the set here is small enough that
		// buffering it costs nothing.
		type box struct {
			id     string
			sealed []byte
		}
		var boxes []box
		rows, err := tx.QueryContext(ctx,
			`SELECT id, secret_enc FROM mailboxes WHERE secret_enc IS NOT NULL`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var b box
			if err := rows.Scan(&b.id, &b.sealed); err != nil {
				rows.Close()
				return err
			}
			boxes = append(boxes, b)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, b := range boxes {
			if len(b.sealed) == 0 {
				continue
			}
			plain, err := secret.Decrypt(oldKey, string(b.sealed))
			if err != nil {
				return fmt.Errorf("store: the password for mailbox %s will not decrypt "+
					"with the old key (is it the right one?): %w", b.id, err)
			}
			resealed, err := secret.Encrypt(newKey, plain)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE mailboxes SET secret_enc = ? WHERE id = ?`,
				[]byte(resealed), b.id); err != nil {
				return err
			}
			out.Mailboxes++
		}
		return nil
	})
	if err != nil {
		// The transaction rolled back, so nothing moved. Reported with the
		// counts zeroed rather than partially filled: a caller that logged
		// "rotated 1 of 3" about a rollback would be describing a state the
		// database is not in.
		return RotatedSecrets{}, err
	}
	return out, nil
}

// CountSealedSecrets reports how many values a rotation would have to move.
//
// For the operator's confirmation prompt. "This will re-encrypt 1 setting and 2
// mailbox passwords" is a sentence somebody can check against what they expect;
// "rotate the key?" is not.
func (db *DB) CountSealedSecrets(ctx context.Context) (RotatedSecrets, error) {
	var out RotatedSecrets
	for _, key := range SecretSystemKeys {
		var n int
		if err := db.Read.QueryRowContext(ctx,
			`SELECT count(*) FROM settings
			  WHERE scope = 'system' AND scope_id IS NULL AND key = ?
			    AND value_json IS NOT NULL AND value_json <> '""'`,
			string(key)).Scan(&n); err != nil {
			return out, err
		}
		out.Settings += n
	}
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM mailboxes WHERE secret_enc IS NOT NULL`).
		Scan(&out.Mailboxes); err != nil {
		return out, err
	}
	return out, nil
}

// unsealJSON unwraps a settings value and decrypts it with the key given.
//
// Its own function rather than SettingsRepo.SystemSecret because that method
// uses the repo's ONE key, and rotation is the one operation with two in play.
// The JSON unwrap matches SystemValue's: a value written as something other
// than a JSON string is used raw, which is what that method does and is the
// forgiving side to be on.
func unsealJSON(key []byte, raw string) (string, error) {
	sealed := raw
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		sealed = s
	}
	if sealed == "" {
		return "", nil
	}
	plain, err := secret.Decrypt(key, sealed)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// jsonString wraps a value the way SetSystemValue does, so a rotated row is
// byte-identical in shape to one written normally.
func jsonString(v string) (string, error) {
	enc, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(enc), nil
}
