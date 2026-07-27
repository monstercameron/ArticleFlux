package app

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// loadOrCreateSecretKey opens the key that seals stored credentials — today the
// Smart+ API key, later the mailbox passwords A20 needs the IMAP poller to be
// able to *use* and therefore cannot hash.
//
// Beside the database, like proxy.key, and for a related but not identical
// reason. proxy.key must persist so URLs on an open page survive a restart;
// this must persist because losing it makes every stored secret permanently
// unreadable, and the failure mode is silent — GCM does not return garbage, it
// returns an error, and the settings screen would say "not configured" about a
// key that is sitting right there.
//
// **What 0600 beside the data actually buys.** It protects a leaked DATABASE:
// a backup, a `VACUUM INTO` copy, a .db attached to an email. It does not
// protect against a compromised host, because anything that can read the
// database can read the file next to it. That is the same bet §7.2 already
// makes about filesystem access being proof of ownership, and it is the right
// one for a self-hosted box — the alternative is prompting for a passphrase on
// every restart of a server that is supposed to come back on its own.
//
// ARTICLEFLUX_SECRET_KEY overrides the file, for a deployment that keeps its
// keys somewhere the disk is not — a systemd EnvironmentFile on a tmpfs, or a
// secrets manager. It must be exactly 32 raw bytes, so in practice it is set
// from a file read rather than typed.
func loadOrCreateSecretKey(dir string) ([]byte, error) {
	if env := os.Getenv("ARTICLEFLUX_SECRET_KEY"); env != "" {
		if len(env) != 32 {
			return nil, secret.ErrKeyLength
		}
		return []byte(env), nil
	}

	path := filepath.Join(dir, "secrets.key")
	b, err := os.ReadFile(path)
	if err == nil && len(b) >= 32 {
		// Truncated to 32 rather than passed whole: a file that picked up a
		// trailing newline from an editor would otherwise be a key length error
		// on every boot, and the first 32 bytes are the key either way.
		return b[:32], nil
	}
	if err == nil {
		// Present but too short. NOT overwritten — a short key file is more
		// likely a botched restore than a fresh install, and generating a new
		// one on top of it would make every existing ciphertext unreadable at
		// exactly the moment someone is trying to recover them.
		return nil, secret.ErrKeyLength
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	key, err := secret.NewKey()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}
