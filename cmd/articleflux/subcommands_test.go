package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The subcommands, which between them are how an instance is created, migrated,
// backed up and populated — and which had no tests at all. `init` is the first
// command a fresh install runs and `backup` is the one that decides whether a
// bad night is recoverable, so "it returned nil" is not the assertion here; what
// it actually wrote is.

func cliLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// tempDB returns a path in a fresh directory. The directory matters as much as
// the file: backup copies key material into the DESTINATION directory, and
// several commands look beside the database.
func tempDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "articleflux.db")
}

// --- migrate ------------------------------------------------------------------

func TestMigrateCreatesTheSchemaAndIsIdempotent(t *testing.T) {
	db := tempDB(t)

	if err := migrate(cliLogger(), []string{"-db", db}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("migrate created no database: %v", err)
	}

	// Running it twice is what a redeploy does, every time.
	if err := migrate(cliLogger(), []string{"-db", db}); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	d, err := store.Open(store.Options{Path: db})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	v, err := d.SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if v <= 0 {
		t.Errorf("schema version is %d after migrating", v)
	}
}

// --- init ---------------------------------------------------------------------

func TestInitCreatesTheFirstSuperadmin(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)

	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	a, err := openAdmin(context.Background(), cliLogger(), db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close()
	n, err := a.Repo().CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 1 {
		t.Errorf("init produced %d accounts, want 1", n)
	}
}

func TestInitRequiresAUsername(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	err := initInstance(cliLogger(), []string{"-db", tempDB(t)})
	if err == nil {
		t.Fatal("init with no -user succeeded")
	}
	if !strings.Contains(err.Error(), "-user") {
		t.Errorf("the error does not name the missing flag: %v", err)
	}
	// Whitespace is not a username either.
	if err := initInstance(cliLogger(), []string{"-db", tempDB(t), "-user", "   "}); err == nil {
		t.Error("init accepted a whitespace username")
	}
}

// Running init twice must not quietly create a second superadmin, and the error
// has to point at the command that does what the person meant.
func TestInitRefusesToRunOnAnInstanceThatHasAccounts(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("first init: %v", err)
	}

	err := initInstance(cliLogger(), []string{"-db", db, "-user", "second"})
	if err == nil {
		t.Fatal("init ran twice and created a second account")
	}
	if !strings.Contains(err.Error(), "adduser") || !strings.Contains(err.Error(), "passwd") {
		t.Errorf("the error does not point at the commands that do what was meant: %v", err)
	}
}

// The policy applies to the environment path too — it is the documented
// non-interactive route, not a bypass.
func TestInitEnforcesThePasswordPolicy(t *testing.T) {
	withPassword(t, "short")
	if err := initInstance(cliLogger(), []string{"-db", tempDB(t), "-user", "cam"}); err == nil {
		t.Error("init accepted a password that violates the policy")
	}
}

// --- adduser ------------------------------------------------------------------

func TestAddUserPutsTheAccountInTheExistingTenant(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := addUser(cliLogger(), []string{"-db", db, "-user", "reader", "-role", "viewer"}); err != nil {
		t.Fatalf("adduser: %v", err)
	}

	a, err := openAdmin(context.Background(), cliLogger(), db)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer a.Close()
	n, err := a.Repo().CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 2 {
		t.Errorf("there are %d accounts, want 2", n)
	}
}

// Without a tenant there is nothing to add the account TO, and the error has to
// say which command makes one rather than echoing a foreign-key failure.
func TestAddUserBeforeInitSaysToRunInit(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := migrate(cliLogger(), []string{"-db", db}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	err := addUser(cliLogger(), []string{"-db", db, "-user", "reader"})
	if err == nil {
		t.Fatal("adduser succeeded with no tenant")
	}
	if !strings.Contains(err.Error(), "init") {
		t.Errorf("the error does not say to run init: %v", err)
	}
}

func TestAddUserRejectsAnUnknownRole(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	err := addUser(cliLogger(), []string{"-db", db, "-user", "x", "-role", "root"})
	if err == nil {
		t.Fatal("adduser accepted a role that does not exist")
	}
	// The four real roles have to be listed, or the fix is a guess.
	for _, role := range []string{"superadmin", "admin", "member", "viewer"} {
		if !strings.Contains(err.Error(), role) {
			t.Errorf("the error does not list %q: %v", role, err)
		}
	}
}

// The unique index is per (tenant, lower(username)). The duplicate case is worth
// naming rather than echoing SQLite at somebody.
func TestAddUserNamesTheDuplicateRatherThanEchoingSQLite(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	err := addUser(cliLogger(), []string{"-db", db, "-user", "cam"})
	if err == nil {
		t.Fatal("adduser created a second account with the same username")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("the duplicate was reported as %v", err)
	}
	if strings.Contains(err.Error(), "UNIQUE constraint") {
		t.Errorf("SQLite's message was echoed rather than explained: %v", err)
	}
}

func TestAddUserRequiresAUsername(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	if err := addUser(cliLogger(), []string{"-db", tempDB(t)}); err == nil {
		t.Error("adduser with no -user succeeded")
	}
}

// --- backup -------------------------------------------------------------------

func TestBackupWritesAFileToAnExplicitPath(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	out := filepath.Join(t.TempDir(), "snapshot.db")
	if err := backup(cliLogger(), []string{"-db", db, "-out", out}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	fi, err := os.Stat(out)
	if err != nil {
		t.Fatalf("backup wrote nothing: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("the backup is empty")
	}

	// It has to be a database, not a file of the right size. A backup nobody
	// can open is worse than no backup, because it is believed.
	d, err := store.Open(store.Options{Path: out})
	if err != nil {
		t.Fatalf("the backup does not open as a database: %v", err)
	}
	defer d.Close()
	if _, err := d.SchemaVersion(context.Background()); err != nil {
		t.Errorf("the backup has no readable schema: %v", err)
	}
}

func TestBackupRequiresADestination(t *testing.T) {
	err := backup(cliLogger(), []string{"-db", tempDB(t)})
	if err == nil {
		t.Fatal("backup with no -out succeeded")
	}
	if !strings.Contains(err.Error(), "-out") {
		t.Errorf("the error does not name the missing flag: %v", err)
	}
}

// A directory destination gets a timestamped name, which is what a cron line
// relies on — the same -out every night has to produce a new file each time
// rather than overwrite last night's.
func TestBackupIntoADirectoryTimestampsTheFile(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	dir := t.TempDir()
	if err := backup(cliLogger(), []string{"-db", db, "-out", dir}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "articleflux-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) != 1 {
		t.Fatalf("expected one timestamped backup, found %v", entries)
	}
	if backups[0] == filepath.Base(db) {
		t.Errorf("the backup reused the database's name: %q", backups[0])
	}
}

// -keep prunes older backups, which is what stops a nightly cron from filling
// the disk. The older ones are planted rather than taken: BackupName is
// second-granularity and a backup never overwrites, so two real backups in one
// test would collide on the name rather than exercise the pruning.
func TestBackupPrunesOlderBackupsWhenKeepIsSet(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	dir := t.TempDir()
	older := []string{
		"articleflux-20260101T000000Z.db",
		"articleflux-20260102T000000Z.db",
		"articleflux-20260103T000000Z.db",
	}
	for _, name := range older {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o600); err != nil {
			t.Fatalf("plant %s: %v", name, err)
		}
	}

	if err := backup(cliLogger(), []string{"-db", db, "-out", dir, "-keep", "2"}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	var kept []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "articleflux-") {
			kept = append(kept, e.Name())
		}
	}
	if len(kept) != 2 {
		t.Errorf("kept %d backups, want the 2 newest: %v", len(kept), kept)
	}
	// The one just taken is the newest and must be among the survivors.
	for _, name := range kept {
		if name == older[0] {
			t.Errorf("the oldest backup survived a -keep 2 prune: %v", kept)
		}
	}
}

// A pruning failure must not fail the command: the backup itself succeeded, and
// reporting failure would make a cron job cry wolf for a night that was in fact
// backed up.
func TestBackupSucceedsWhenPruningCannotRun(t *testing.T) {
	withPassword(t, testPasswordForCLI)
	db := tempDB(t)
	if err := initInstance(cliLogger(), []string{"-db", db, "-user", "cam"}); err != nil {
		t.Fatalf("init: %v", err)
	}

	// A destination that does not exist yet, named with a trailing separator so
	// it is still treated as a directory — the first-run case a cron line hits.
	dir := filepath.Join(t.TempDir(), "backups") + string(os.PathSeparator)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := backup(cliLogger(), []string{"-db", db, "-out", dir, "-keep", "5"}); err != nil {
		t.Fatalf("backup into a fresh directory: %v", err)
	}
}

// --- loadDotenv ---------------------------------------------------------------

// A malformed .env is a warning, not a fatal: it is a development convenience,
// and refusing to start because a comment was wrong would be a worse failure
// than ignoring the file.
func TestLoadDotenvNeverStopsTheServer(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	// No file at all — the ordinary case for a deployed instance.
	loadDotenv(cliLogger())

	// A file that parses, and one that does not.
	if err := os.WriteFile(dotenvPath, []byte("ARTICLEFLUX_TEST_DOTENV=value\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	loadDotenv(cliLogger())
	if os.Getenv("ARTICLEFLUX_TEST_DOTENV") != "value" {
		t.Error(".env was not applied")
	}
	t.Cleanup(func() { _ = os.Unsetenv("ARTICLEFLUX_TEST_DOTENV") })

	if err := os.WriteFile(dotenvPath, []byte("this is not a key=value line\x00\n"), 0o600); err != nil {
		t.Fatalf("write bad .env: %v", err)
	}
	loadDotenv(cliLogger()) // must not panic and must not exit
}

// --- OPML round trip ----------------------------------------------------------
//
// An importer without an exporter is a roach motel, and an export that loses the
// categories is the same thing with extra steps — it costs whoever spent an
// evening filing 151 feeds that evening. So the assertion is the ROUND TRIP, not
// that each half returned nil.

const sampleOPML = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="1.0">
  <head><title>Subscriptions</title></head>
  <body>
    <outline text="Tech">
      <outline type="rss" text="Example One" xmlUrl="https://one.example/feed.xml"/>
      <outline type="rss" text="Example Two" xmlUrl="https://two.example/feed.xml"/>
    </outline>
    <outline type="rss" text="Loose Feed" xmlUrl="https://three.example/feed.xml"/>
  </body>
</opml>
`

func writeOPML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "feeds.opml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write opml: %v", err)
	}
	return path
}

func TestImportOPMLSubscribesWithoutFetching(t *testing.T) {
	db := tempDB(t)
	// No -fetch: subscribing is separated from fetching on purpose, so this must
	// return promptly and touch no network.
	if err := importOPML(cliLogger(), []string{"-db", db, "-file", writeOPML(t, sampleOPML)}); err != nil {
		t.Fatalf("import: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.opml")
	if err := exportOPML(cliLogger(), []string{"-db", db, "-file", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	for _, feed := range []string{
		"https://one.example/feed.xml",
		"https://two.example/feed.xml",
		"https://three.example/feed.xml",
	} {
		if !strings.Contains(string(data), feed) {
			t.Errorf("the round trip lost %s:\n%s", feed, data)
		}
	}
	// The category is the part that used to be lost.
	if !strings.Contains(string(data), "Tech") {
		t.Errorf("the round trip lost the category:\n%s", data)
	}
}

// Importing the same file twice is what somebody does when they are not sure it
// worked the first time. It must not double every subscription.
func TestImportOPMLIsIdempotent(t *testing.T) {
	db := tempDB(t)
	file := writeOPML(t, sampleOPML)
	for i := 0; i < 2; i++ {
		if err := importOPML(cliLogger(), []string{"-db", db, "-file", file}); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}

	out := filepath.Join(t.TempDir(), "out.opml")
	if err := exportOPML(cliLogger(), []string{"-db", db, "-file", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if n := strings.Count(string(data), "https://one.example/feed.xml"); n != 1 {
		t.Errorf("a second import produced %d copies of the same feed", n)
	}
}

func TestImportOPMLRequiresAFile(t *testing.T) {
	err := importOPML(cliLogger(), []string{"-db", tempDB(t)})
	if err == nil {
		t.Fatal("import with no -file succeeded")
	}
	if !strings.Contains(err.Error(), "-file") {
		t.Errorf("the error does not name the missing flag: %v", err)
	}
}

func TestImportOPMLReportsAMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.opml")
	if err := importOPML(cliLogger(), []string{"-db", tempDB(t), "-file", missing}); err == nil {
		t.Error("importing a file that does not exist reported success")
	}
}

// A file that is not OPML must be refused with the filename in the message —
// "import failed" on its own is what sends somebody looking in the database.
func TestImportOPMLNamesTheFileItCouldNotRead(t *testing.T) {
	file := writeOPML(t, "this is not xml at all")
	err := importOPML(cliLogger(), []string{"-db", tempDB(t), "-file", file})
	if err == nil {
		t.Fatal("importing a non-OPML file reported success")
	}
	if !strings.Contains(err.Error(), filepath.Base(file)) {
		t.Errorf("the error does not name the file: %v", err)
	}
}

// Where the private-address guard actually sits, pinned because it is easy to
// assume otherwise.
//
// -allow-private is passed to app.Open as AllowPrivateFeeds and governs
// FETCHING. Subscribing writes a row and reaches no network, so a loopback URL
// in an OPML file is subscribed either way and is refused later, by netguard,
// when something tries to poll it. That is the right place for it — the check
// belongs where the request is made — but it does mean this flag does not filter
// the import, and a test that asserted it did would be asserting a guarantee
// nothing provides.
func TestImportOPMLSubscribesPrivateURLsAndLeavesTheGuardToFetchTime(t *testing.T) {
	private := `<?xml version="1.0"?><opml version="1.0"><head><title>x</title></head><body>
	  <outline type="rss" text="Local" xmlUrl="http://127.0.0.1:9999/feed.xml"/>
	</body></opml>`
	db := tempDB(t)
	if err := importOPML(cliLogger(), []string{"-db", db, "-file", writeOPML(t, private)}); err != nil {
		t.Fatalf("import: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.opml")
	if err := exportOPML(cliLogger(), []string{"-db", db, "-file", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "127.0.0.1") {
		t.Errorf("the subscription was dropped at import time; if that is now "+
			"deliberate, the fetch-time guard is no longer the only one:\n%s", data)
	}
}

// An outline with no xmlUrl is a folder, a comment, or a malformed line. None of
// them is a feed, and none may become a subscription with an empty URL — which
// the poller would then try, and fail, forever.
func TestImportOPMLSkipsOutlinesThatAreNotFeeds(t *testing.T) {
	odd := `<?xml version="1.0"?><opml version="1.0"><head><title>x</title></head><body>
	  <outline text="Just A Folder"/>
	  <outline type="rss" text="No URL"/>
	  <outline type="rss" text="Real" xmlUrl="https://real.example/feed.xml"/>
	</body></opml>`
	db := tempDB(t)
	if err := importOPML(cliLogger(), []string{"-db", db, "-file", writeOPML(t, odd)}); err != nil {
		t.Fatalf("import: %v", err)
	}

	out := filepath.Join(t.TempDir(), "out.opml")
	if err := exportOPML(cliLogger(), []string{"-db", db, "-file", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), "https://real.example/feed.xml") {
		t.Errorf("the one real feed was not subscribed:\n%s", data)
	}
	if strings.Contains(string(data), `xmlUrl=""`) {
		t.Errorf("an outline with no URL became a subscription:\n%s", data)
	}
}

func TestExportOPMLWritesValidOPMLForAnEmptyAccount(t *testing.T) {
	out := filepath.Join(t.TempDir(), "empty.opml")
	if err := exportOPML(cliLogger(), []string{"-db", tempDB(t), "-file", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	// An account with no feeds still has to produce a file another reader can
	// open, not an empty one.
	if !strings.Contains(string(data), "<opml") || !strings.Contains(string(data), "</opml>") {
		t.Errorf("the export is not an OPML document:\n%s", data)
	}
}

// --- poll ---------------------------------------------------------------------

// poll against an account with no subscriptions must be a clean no-op rather
// than an error — it is what a cron line runs on a fresh instance.
func TestPollOnAnEmptyInstanceIsANoOp(t *testing.T) {
	if err := poll(cliLogger(), []string{"-db", tempDB(t)}); err != nil {
		t.Errorf("poll on an empty instance: %v", err)
	}
}
