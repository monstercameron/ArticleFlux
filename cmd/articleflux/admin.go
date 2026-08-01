// Administration subcommands: the things an operator does to an instance from a
// shell, as opposed to the things a reader does to it from a browser.
//
// These exist because the reader is now behind a login, and a login needs
// something to log in as. Before this file the only way to get an account was
// DevMode's EnsureDevUser, which is exactly the mechanism that made a
// reverse-proxied instance an open reader — so the replacement had to arrive in
// the same change.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/monstercameron/ArticleFlux/internal/app"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// roles are the values the users.role column is documented to hold.
//
// Validated here rather than accepted as free text: capabilities are resolved
// from the role, and an account created with role "admins" would fail closed on
// every check with no clue as to why.
var roles = map[string]bool{
	"superadmin": true, "admin": true, "member": true, "viewer": true,
}

// minPasswordLen is the shortest password init/adduser/passwd will accept.
//
// Twelve, and no composition rules. Length is the only property that reliably
// costs an attacker anything; "must contain a symbol" reliably costs the user a
// password they cannot remember and writes down somewhere worse.
//
// It is now `pwpolicy.MinLength` rather than its own 12, because the check that
// enforces it moved there (6.1) and a help string promising a floor the check
// does not apply is worse than no promise. `resolvePassword` runs the whole
// policy — length, the bundled known-password list, the username, and the
// keyboard runs — and the durable lockout that used to be missing is now in
// grpcsrv/auth.go, so this is one of three controls rather than one of two.
const minPasswordLen = pwpolicy.MinLength

// The local development account: created by `serve -dev`, used by `seed` and
// `export`, and prefilled on the login screen when the page origin is loopback
// (client/view/login.go).
//
// **devPassword must satisfy minPasswordLen**, and that is not a stylistic
// preference — it is a constraint the two halves of this program have to agree
// on. The previous value was "articleflux", eleven characters, which meant
// .env.example documented a dev password that `init` refused with "password must
// be at least 12 characters". Following the documentation produced an error, and
// then a server that would not start because no account had been created.
//
// Defined here rather than as a literal at each of the four call sites for the
// same reason: the value has to be one value.
const (
	devUsername = "cam"
	devPassword = "articleflux-dev"
)

// initInstance creates the first tenant and its superadmin.
//
// Refuses to run twice. `init` on a populated instance is nearly always someone
// running the setup steps again on a box that is already live, and silently
// adding a second superadmin to it is a worse outcome than an error.
func initInstance(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", "", "username for the first account (required)")
	tenant := fs.String("tenant", "Local", "display name for the tenant")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*user) == "" {
		return errors.New("init: -user is required")
	}
	password, err := resolvePassword(*user)
	if err != nil {
		return err
	}

	ctx := context.Background()
	a, err := openAdmin(ctx, log, *dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	n, err := a.Repo().CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("init: this instance already has %d account(s); "+
			"use `articleflux adduser` to add another, or `articleflux passwd` to reset one", n)
	}

	hash, err := secret.HashPassword(password, secret.Active())
	if err != nil {
		return err
	}
	if err := a.Repo().CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: idgen.New(),
		Name:     *tenant,
		UserID:   idgen.New(),
		Username: *user,
		Hash:     hash,
		Role:     "superadmin",
		Now:      time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return err
	}
	log.Info("initialised", "tenant", *tenant, "user", *user, "role", "superadmin")
	fmt.Fprintf(os.Stderr, "\nCreated superadmin %q. Start the server and log in.\n", *user)
	return nil
}

// addUser creates an account inside the existing tenant.
func addUser(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("adduser", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", "", "username (required)")
	role := fs.String("role", "member", "superadmin | admin | member | viewer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*user) == "" {
		return errors.New("adduser: -user is required")
	}
	if !roles[*role] {
		return fmt.Errorf("adduser: unknown role %q (superadmin, admin, member, viewer)", *role)
	}
	password, err := resolvePassword(*user)
	if err != nil {
		return err
	}

	ctx := context.Background()
	a, err := openAdmin(ctx, log, *dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	tenantID, err := a.Repo().FirstTenantID(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return errors.New("adduser: no tenant exists — run `articleflux init` first")
	}
	if err != nil {
		return err
	}

	hash, err := secret.HashPassword(password, secret.Active())
	if err != nil {
		return err
	}
	if err := a.Repo().AddUser(ctx, store.NewTenant{
		TenantID: tenantID,
		UserID:   idgen.New(),
		Username: *user,
		Hash:     hash,
		Role:     *role,
		Now:      time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		// The unique index is per (tenant, lower(username)), so this is the
		// duplicate case and worth naming rather than echoing SQLite.
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("adduser: %q already exists in this tenant", *user)
		}
		return err
	}
	log.Info("account created", "user", *user, "role", *role)
	return nil
}

// passwd is the break-glass reset (TODO 7.10).
//
// It revokes every session for the account, which is the whole point: a password
// is reset because someone has lost control of it, and leaving the sessions it
// minted alive resets nothing.
func passwd(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", "", "username (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*user) == "" {
		return errors.New("passwd: -user is required")
	}
	password, err := resolvePassword(*user)
	if err != nil {
		return err
	}

	ctx := context.Background()
	a, err := openAdmin(ctx, log, *dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	u, err := a.Repo().UserForLogin(ctx, *user)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("passwd: no account named %q", *user)
	}
	if err != nil {
		return err
	}

	hash, err := secret.HashPassword(password, secret.Active())
	if err != nil {
		return err
	}
	// One transaction (§7.3a SEC3): the new hash, every session and every
	// refresh family for this account commit together or not at all — the
	// same primitive the sudo-gated ChangePassword RPC uses, so the break-glass
	// path cannot drift into leaving a stale credential half-revoked.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	sessions, families, err := a.Repo().ChangePasswordAndRevoke(ctx, u.UserID, hash, "", "", now)
	if err != nil {
		return err
	}
	log.Info("password reset", "user", u.Username, "sessions_revoked", sessions,
		"families_revoked", families)
	return nil
}

// migrate applies pending migrations and exits.
//
// serve migrates on open too, so this is not required for a normal start. It
// exists for the deploy sequence, where the point is to apply the schema change
// and see it succeed BEFORE the new binary starts taking traffic — a migration
// that fails during serve fails a start, and a migration that fails here fails a
// deploy step, which is a much better place to find out.
func migrate(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	dbPath := commonFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: *dbPath})
	if err != nil {
		return err
	}
	defer db.Close()

	n, err := db.Migrate(ctx)
	if err != nil {
		return err
	}
	v, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	log.Info("migrated", "applied", n, "schema_version", v, "db", *dbPath)
	return nil
}

// backup writes a verified point-in-time copy (TODO 3.4).
func backup(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dbPath := commonFlags(fs)
	out := fs.String("out", "",
		"destination file, or a directory to write a timestamped backup into (required)")
	keep := fs.Int("keep", 0,
		"when -out is a directory, keep this many newest backups and delete the rest; 0 keeps all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*out) == "" {
		return errors.New("backup: -out is required")
	}

	ctx := context.Background()
	db, err := store.Open(store.Options{Path: *dbPath})
	if err != nil {
		return err
	}
	defer db.Close()

	dst := *out
	intoDir := false
	// A trailing separator means "directory" even when it does not exist yet,
	// which is the first-run case and the one a cron line hits.
	if fi, err := os.Stat(*out); (err == nil && fi.IsDir()) ||
		strings.HasSuffix(*out, string(os.PathSeparator)) || strings.HasSuffix(*out, "/") {
		intoDir = true
		dst = filepath.Join(*out, store.BackupName("articleflux-", time.Now()))
	}

	start := time.Now()
	n, err := db.Backup(ctx, dst)
	if err != nil {
		return err
	}
	log.Info("backed up", "file", dst, "bytes", n,
		"seconds", int(time.Since(start).Seconds()))

	// The database is not the whole instance. secrets.key seals the Smart+ key
	// and every mailbox password, lives beside the database rather than in it,
	// and a restore without it produces a server that refuses to start — see
	// app.CopyKeyFiles for the whole account of it.
	keys, err := app.CopyKeyFiles(filepath.Dir(*dbPath), filepath.Dir(dst))
	if err != nil {
		return err
	}
	if len(keys.Copied) > 0 {
		log.Info("kept the key material beside it", "files", strings.Join(keys.Copied, ", "),
			"dir", filepath.Dir(dst))
	}
	for _, w := range keys.Warnings {
		log.Warn("a key file was not kept; the URLs it signs will not survive a restore", "err", w)
	}
	// Said out loud rather than inferred from silence. An instance holding its
	// key in ARTICLEFLUX_SECRET_KEY has nothing here to copy and is correct;
	// one that has simply lost the file is not, and the two look identical
	// from the backup directory.
	if slices.Contains(keys.Missing, app.SecretKeyFile) {
		log.Warn("no secrets.key beside the database, so none was backed up. "+
			"If this instance sets ARTICLEFLUX_SECRET_KEY, back that value up "+
			"separately — a restore without it cannot read any stored credential",
			"looked_in", filepath.Dir(*dbPath))
	}

	if intoDir && *keep > 0 {
		removed, err := store.PruneBackups(filepath.Dir(dst), "articleflux-", *keep)
		if err != nil {
			// The backup itself succeeded. Failing the command now would make a
			// cron job report failure for a night that was actually backed up.
			log.Warn("pruning old backups", "err", err)
			return nil
		}
		for _, p := range removed {
			log.Info("pruned", "file", p)
		}
	}
	return nil
}

// openAdmin opens the app for a CLI action.
//
// DevMode is false and stays false: these commands never serve a request, so the
// only thing it could do here is set a precedent.
func openAdmin(ctx context.Context, log *slog.Logger, dbPath string) (*app.App, error) {
	return app.Open(ctx, app.Config{DBPath: dbPath, Log: log, Version: version})
}

// resolvePassword takes the password from the environment or a terminal
// prompt, in that order, and enforces §7.1's policy.
//
// §7.3a SEC5: there used to be a third source, a `-password` flag, checked
// before both of these. It is gone on purpose. A password on a command line
// sits in shell history and in `ps` output for the life of the process, and
// the flag was the thing every usage example showed first — teaching the
// exact habit this removal exists to stop. ARTICLEFLUX_PASSWORD is the
// documented non-interactive path (provisioning scripts, systemd units); a
// human gets a hidden, confirmed prompt.
func resolvePassword(username string) (string, error) {
	pw := os.Getenv("ARTICLEFLUX_PASSWORD")
	if pw == "" {
		var err error
		pw, err = promptPassword("Password: ")
		if err != nil {
			return "", err
		}
		confirm, err := promptPassword("Confirm: ")
		if err != nil {
			return "", err
		}
		if pw != confirm {
			return "", errors.New("passwords do not match")
		}
	}
	// The full §7.1 policy, not just a length floor. Length alone accepts
	// "password1234", which is the first thing an attacker tries and which no
	// lockout curve helps with: the lockout buys four guesses an hour, and the
	// first guess is enough. The two controls only work as a pair.
	//
	// The USERNAME goes in, because "cameron2026" is the single most guessable
	// password for an account called cameron and no generic list can contain it.
	if err := pwpolicy.Check(pw, username); err != nil {
		return "", fmt.Errorf("password rejected: %w", err)
	}
	return pw, nil
}

// promptPassword reads a password from the terminal without echoing it.
//
// term.ReadPassword rather than bufio over stdin, because a password echoed to
// the screen is a password on someone's shoulder-surfed terminal and in their
// scrollback. When stdin is not a terminal — a pipe, a provisioning script,
// systemd — there is no echo to suppress and no prompt worth printing, so it
// falls back to a plain line read. That fallback is what makes
// `echo hunter2xxxxx | articleflux init -user cam` work.
func promptPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	// The prompt goes to stderr so that stdout stays clean for anything being
	// piped, and so it is visible even when stdout is redirected to a log.
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
