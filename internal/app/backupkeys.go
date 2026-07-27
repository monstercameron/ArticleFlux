package app

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// KeyFiles are the files a restored database needs beside it to be a working
// instance rather than a working schema.
//
// secrets.key is first because it is the only one whose loss is fatal; see
// CopyKeyFiles.
var KeyFiles = []string{"secrets.key", "proxy.key", "speech.key"}

// SecretKeyFile seals the stored credentials. Losing it is unrecoverable.
const SecretKeyFile = "secrets.key"

// KeyBackup is what a backup managed to keep besides the database.
//
// The three fields are separated because they are three different things to
// say to an operator, and folding them into one error is how "there was
// nothing to copy" starts reading like "the copy failed".
type KeyBackup struct {
	// Copied are the key files written next to the database backup.
	Copied []string
	// Missing are key files that do not exist beside the database. Normal:
	// proxy.key and speech.key are only minted once their feature is used, and
	// secrets.key is absent on an instance using ARTICLEFLUX_SECRET_KEY.
	Missing []string
	// Warnings are copy failures that do not make the backup unrestorable.
	Warnings []error
}

// CopyKeyFiles puts the instance's key material beside a database backup, and
// is the difference between a backup and a belief.
//
// # What was wrong
//
// `articleflux backup` copied the database and nothing else, and the database
// is not the whole instance. `secrets.key` seals the Smart+ API key and every
// mailbox password, it lives beside the database rather than in it, and it is
// never regenerated in place — losing it makes those values permanently
// unreadable. Restoring yesterday's backup onto a fresh box therefore produced
// an instance that REFUSES TO START: loadOrCreateSecretKey mints a new key,
// preflightCredentials finds a stored secret that will not decrypt, and serve
// exits on it. The error even says "restore secrets.key" — about a file the
// backup had never been asked to keep.
//
// The rehearsal in deploy/README.md could not catch it. It copies the .db,
// runs `migrate` against it and calls that a restore, which passes on a backup
// that cannot boot, because migrate never opens a sealed setting. A drill that
// only exercises the part that works is how this survived being written down.
//
// # Why beside, rather than inside
//
// Sealing the key into the backup file would undo the reason the key is a
// separate file at all: 0600-beside-the-data protects a LEAKED DATABASE — a
// copy, a file attached to an email — and a .db with the key baked in protects
// nothing. Kept as a sibling, the leaked-file property survives intact, and the
// destination is the deployment's `/var/backups/articleflux`, which the install
// instructions already chmod 700 to the service user. That is the same trust
// boundary the live key has, not a weaker one.
//
// # What is fatal
//
// Only secrets.key. A backup that could not keep it is unrestorable, and a
// cron job reporting success over an unrestorable file is worse than one
// reporting failure. proxy.key and speech.key sign URLs that a restart
// invalidates anyway, so they come back as Warnings.
func CopyKeyFiles(dbDir, destDir string) (KeyBackup, error) {
	var out KeyBackup
	if filepath.Clean(dbDir) == filepath.Clean(destDir) {
		// Backing up into the data directory. The keys are already there, and
		// copying a file onto itself is the one way to lose it.
		return out, nil
	}

	for _, name := range KeyFiles {
		err := copyKeyFile(filepath.Join(dbDir, name), filepath.Join(destDir, name))
		switch {
		case err == nil:
			out.Copied = append(out.Copied, name)
		case errors.Is(err, errKeyUnchanged):
			// Already identical at the destination. Not copied, not a problem.
		case errors.Is(err, os.ErrNotExist):
			out.Missing = append(out.Missing, name)
		case name == SecretKeyFile:
			return out, fmt.Errorf("backing up %s, without which this backup "+
				"cannot be restored: %w", name, err)
		default:
			out.Warnings = append(out.Warnings, fmt.Errorf("%s: %w", name, err))
		}
	}
	return out, nil
}

// errKeyUnchanged reports that the destination already holds these bytes.
var errKeyUnchanged = errors.New("unchanged")

func copyKeyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	// Rewritten only when it differs, so a nightly job does not rewrite the
	// same 32 bytes forever — and so a key that was ROTATED does get through,
	// which "write it once if absent" would silently miss.
	if old, oldErr := os.ReadFile(dst); oldErr == nil && bytes.Equal(old, b) {
		return errKeyUnchanged
	}
	return os.WriteFile(dst, b, 0o600)
}
