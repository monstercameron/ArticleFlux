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
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/monstercameron/ArticleFlux/internal/app"
	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/authn"
	"github.com/monstercameron/ArticleFlux/internal/diskspace"
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
	tenantID, userID := idgen.New(), idgen.New()
	if err := a.Repo().CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: tenantID,
		Name:     *tenant,
		UserID:   userID,
		Username: *user,
		Hash:     hash,
		Role:     "superadmin",
		Now:      time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return err
	}
	log.Info("initialised", "tenant", *tenant, "user", *user, "role", "superadmin")
	// The trail's first row when an instance is claimed from the shell rather
	// than through the setup screen. Both routes create a superadmin out of
	// nothing, so both have to leave the same evidence.
	audit.New(a.Repo(), log).Record(ctx, audit.Event{
		Action: audit.ActionInstanceClaim, Subject: userID, Tenant: tenantID,
		Detail: map[string]string{
			"username": *user, "role": "superadmin", "tenant": *tenant, "via": "cli",
		},
	})
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
	userID := idgen.New()
	if err := a.Repo().AddUser(ctx, store.NewTenant{
		TenantID: tenantID,
		UserID:   userID,
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
	// An operator creating an account from the shell is invisible from inside the
	// app. That is the case §7.9 exists for: legitimate, routine, and
	// indistinguishable from somebody with filesystem access helping themselves.
	audit.New(a.Repo(), log).Record(ctx, audit.Event{
		Action: audit.ActionAccountCreated, Subject: userID, Tenant: tenantID,
		Detail: map[string]string{"username": *user, "role": *role, "via": "cli"},
	})
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
	// No Actor: whoever ran this had a shell, not a session, so there is no user
	// id to name. Subject is the account it happened TO, which is the question
	// somebody reading this row six months later is asking.
	audit.New(a.Repo(), log).Record(ctx, audit.Event{
		Action: audit.ActionPasswordReset, Subject: u.UserID, Tenant: u.TenantID,
		Detail: map[string]string{
			"username": u.Username, "via": "cli",
			"sessions_revoked": strconv.FormatInt(sessions, 10),
			"families_revoked": strconv.FormatInt(families, 10),
		},
	})
	return nil
}

// reset mints a single-use reset token for an account and prints it (§7.2).
//
// # Why this exists next to passwd, which already resets a password
//
// `passwd` sets the new password ON THE BOX. That is the right tool when the
// person locked out is the person with the shell — and the wrong one for every
// other case, because the operator ends up choosing a password and then reading
// it down a phone line to the reader, who now shares their credential with
// whoever else was listening and usually never changes it.
//
// A reset token inverts that: the admin proves nothing about the password, the
// reader chooses one nobody else has seen, and the thing that travels is a
// value that stops working the moment it is used. It is also the only recovery
// path for a reader who lost their sheet of codes, which — given people lose
// them — is most of them.
//
// # The lifetime is doing security work the channel cannot
//
// D14 rules out SMTP, so this token travels by whatever the admin already has:
// chat, SMS, a phone call. Those have no security properties at all. One hour
// (authn.ResetTokenLifetime) is the compensation, and it is why the token is
// printed rather than mailed — the operator can see exactly what they are
// handing over and when it dies. Minting a second token kills the first, so
// "issue a new one" replaces the attack surface rather than doubling it.
func reset(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	dbPath := commonFlags(fs)
	user := fs.String("user", "", "username (required)")
	origin := fs.String("origin", "", "public origin, to print a full link (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*user) == "" {
		return errors.New("reset: -user is required")
	}

	ctx := context.Background()
	a, err := openAdmin(ctx, log, *dbPath)
	if err != nil {
		return err
	}
	defer a.Close()

	u, err := a.Repo().UserForLogin(ctx, *user)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("reset: no account named %q", *user)
	}
	if err != nil {
		return err
	}

	token, err := authn.GenerateToken()
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(authn.ResetTokenLifetime)
	// Only the hash is stored, exactly as for a session token. A database dump
	// must not hand out live resets, and the plaintext exists here and in
	// whatever the operator pastes it into — nowhere else, ever.
	if err := a.Repo().CreateResetToken(ctx, store.NewResetToken{
		UserID:    u.UserID,
		TokenHash: secret.HashToken(token),
		Origin:    "cli",
		ExpiresAt: expires,
	}); err != nil {
		return err
	}

	// Printed to stdout rather than logged. A log line goes to the journal, gets
	// shipped, and outlives the token by however long the retention is — this is
	// a credential, and it belongs on the operator's terminal and nowhere a
	// pipeline will pick it up.
	fmt.Printf("\nReset token for %s (valid for %s, single use):\n\n  %s\n",
		u.Username, authn.ResetTokenLifetime, token)
	// The link, and the client route that answers it.
	//
	// `/reset?token=…` is recognised by client/view/root.go (resetTokenFrom),
	// which mounts the credential screen in recovery mode with the token already
	// filled in and then strips it out of the address bar. That was NOT true for
	// a while: this line printed a URL to a route that did not exist, so the link
	// landed on the plain sign-in card and the only way through was to paste the
	// token by hand out of the URL bar — the exact step the link exists to
	// remove, asked of somebody already locked out.
	//
	// The two halves are one grammar in two files. If this format string
	// changes, `resetPath` and `resetTokenParam` change with it.
	if o := strings.TrimRight(strings.TrimSpace(*origin), "/"); o != "" {
		fmt.Printf("\n  %s/reset?token=%s\n", o, token)
	}
	fmt.Printf("\nIt expires at %s. Minting another one invalidates this.\n\n",
		expires.Format(time.RFC3339))

	// The log records THAT a reset was issued and for whom — which is audit
	// evidence somebody will want — and never the token itself.
	log.Info("reset token issued", "user", u.Username, "expires_at", expires.Format(time.RFC3339))
	// The ISSUE is recorded here and the REDEEM is recorded by the RPC, so the
	// trail shows both halves and the gap between them. A token minted and never
	// redeemed is worth seeing; so is one redeemed from an address nobody
	// expected.
	audit.New(a.Repo(), log).Record(ctx, audit.Event{
		Action: audit.ActionResetIssued, Subject: u.UserID, Tenant: u.TenantID,
		Detail: map[string]string{
			"username": u.Username, "via": "cli",
			"expires_at": expires.Format(time.RFC3339),
		},
	})
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

// vacuumCmd compacts the live database, and can convert it to incremental
// auto-vacuum on the way.
//
// # Why this is a command somebody runs rather than a timer
//
// SQLite never shrinks a file on DELETE — freed pages go on a free list and are
// reused. That was invisible until retention started removing rows at scale
// (items on the operator's window; `login_attempts` and `audit_log` on windows
// that default to deleting), and it is hidden further by `articleflux backup`,
// which VACUUMs the COPY: the backup is compact and the live file is not.
//
// VACUUM builds a complete second copy before replacing the original, so it
// needs free space roughly equal to the database's size and holds a write lock
// throughout. Both are worst on the instance that needs it most. So this
// reports the numbers first and refuses when there is not obviously room,
// rather than being a thing that happens to somebody at 03:30.
func vacuumCmd(log *slog.Logger, args []string) error {
	fs := flag.NewFlagSet("vacuum", flag.ExitOnError)
	dbPath := commonFlags(fs)
	convert := fs.Bool("incremental", false,
		"also switch this database to incremental auto-vacuum, so later deletions "+
			"can be reclaimed without a full rewrite (only possible during a VACUUM)")
	dryRun := fs.Bool("n", false, "report what would be reclaimed and do nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx := context.Background()
	db, err := store.Open(store.Options{Path: *dbPath})
	if err != nil {
		return err
	}
	defer db.Close()

	mode, err := db.AutoVacuum(ctx)
	if err != nil {
		return err
	}
	free, pageSize, err := db.FreePages(ctx)
	if err != nil {
		return err
	}
	size := int64(0)
	if fi, serr := os.Stat(*dbPath); serr == nil {
		size = fi.Size()
	}
	reclaimable := free * pageSize
	log.Info("database",
		"file", *dbPath,
		"size_mb", size>>20,
		"auto_vacuum", mode,
		"free_pages", free,
		"reclaimable_mb", reclaimable>>20)

	if mode == "none" && !*convert {
		// Said rather than left to be discovered. Without the conversion this
		// compaction is a one-off: the next year of retention sweeps puts the
		// pages straight back on a free list nothing can return.
		log.Warn("this database has auto_vacuum=none, so nothing reclaims pages between " +
			"full rewrites. Pass -incremental to convert it during this VACUUM — it is the " +
			"only moment the mode can be changed")
	}

	if *dryRun {
		log.Info("dry run; nothing was changed")
		return nil
	}

	// The precheck. VACUUM needs room for a second copy, and finding that out
	// halfway through is finding it out on a disk that is already the problem.
	if free, ferr := diskspace.Free(filepath.Dir(*dbPath)); ferr == nil && size > 0 {
		if int64(free) < size+(size/10) {
			return fmt.Errorf("vacuum: this needs about %d MB free (a full copy of the database, "+
				"plus a margin) and the volume has %d MB. Free some space first — "+
				"`articleflux backup -out` elsewhere and prune, or clear the *-cache directories",
				(size+size/10)>>20, free>>20)
		}
	}

	start := time.Now()
	if err := db.Vacuum(ctx, *convert); err != nil {
		return err
	}
	after := int64(0)
	if fi, serr := os.Stat(*dbPath); serr == nil {
		after = fi.Size()
	}
	newMode, _ := db.AutoVacuum(ctx)
	log.Info("vacuumed",
		"before_mb", size>>20, "after_mb", after>>20,
		"reclaimed_mb", (size-after)>>20,
		"auto_vacuum", newMode,
		"seconds", int(time.Since(start).Seconds()))
	return nil
}
