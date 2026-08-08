package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/app"
	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Rotating the instance key leaves a row in the trail.
//
// # Why this is the command that most needed one
//
// The four other privileged shell operations already record: `init` writes
// instance.claimed, `adduser` writes account.created, `passwd` writes
// account.password.reset, `reset` writes auth.reset.issued. `rotate-key` — which
// re-encrypts every stored credential and replaces secrets.key — wrote nothing.
//
// audit.go's own argument for the administration group applies to it more than
// to any of them: the act is legitimate, it is invisible from inside the app
// (no screen changes), and it is indistinguishable from an intruder with
// filesystem access re-sealing the database under a key of their own.
//
// And it has a shape the others do not. People run this because they SUSPECT AN
// EXPOSURE, so the moment somebody reads `articleflux audit` in earnest is
// exactly the moment they need to know whether the key was rotated and when.
// Before this the only evidence was a timestamped `.old` file in a directory.
func TestRotateKeyLeavesAnAuditRow(t *testing.T) {
	dbPath := tempDB(t)
	if err := migrate(cliLogger(), []string{"-db", dbPath}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// rotate-key reads the key from beside the database, exactly as the server
	// does.
	key, err := secret.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(filepath.Dir(dbPath), app.SecretKeyFile)
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := rotateKeyCmd(cliLogger(), []string{"-db", dbPath, "-yes"}); err != nil {
		t.Fatalf("rotate-key: %v", err)
	}

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entries, err := store.NewReaderRepo(db).AuditTrailInstance(context.Background(), 50)
	if err != nil {
		t.Fatalf("reading the trail: %v", err)
	}
	var found bool
	for _, e := range entries {
		if e.Action == string(audit.ActionKeyRotated) {
			found = true
			// The counts and the recovery path are the whole point of the row:
			// "1 setting and 2 mailbox passwords" is what an operator checks
			// against what they expected, and the `.old` copy is the way back.
			var detail map[string]string
			if err := json.Unmarshal([]byte(e.Detail), &detail); err != nil {
				t.Fatalf("detail_json does not parse: %v (%q)", err, e.Detail)
			}
			for _, k := range []string{"settings", "mailbox_passwords", "old_key_kept_at"} {
				if _, ok := detail[k]; !ok {
					t.Errorf("the rotation row carries no %q; that is what makes it "+
						"readable in an incident report", k)
				}
			}
			// Never the key material. audit.Event.Detail says so: this table
			// outlives the sessions it describes and gets quoted. Checked against
			// the raw JSON rather than field by field, so a future field cannot
			// smuggle it in.
			if strings.Contains(e.Detail, string(key)) {
				t.Error("the rotation row contains the old key material")
			}
		}
	}
	if !found {
		t.Errorf("rotate-key wrote no %s row. The instance key was replaced and "+
			"the trail an incident report reads from does not mention it",
			audit.ActionKeyRotated)
	}

	// And the rotation itself still happened — a test that only checked the row
	// would pass over a command that logged and did nothing.
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("the key file is gone: %v", err)
	}
	after, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) == string(key) {
		t.Error("the key on disk is unchanged, so nothing was rotated")
	}
}
