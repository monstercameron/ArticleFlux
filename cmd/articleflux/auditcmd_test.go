package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/audit"
	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// `articleflux audit` is the ONLY reader of the audit trail — §7.9's admin
// screen does not exist yet — and it shipped with no test at all. That matters
// more here than it would for another subcommand: the thing this command is for
// is the moment an operator asks "did somebody get into my account", and a
// filter that quietly drops rows gives a reassuring answer to that question
// while being wrong. Every test below is about a row NOT disappearing.

// --- capture ------------------------------------------------------------------

// captureStdout runs fn with os.Stdout replaced by a pipe and returns what was
// written. auditCmd prints rather than returning a value, so there is no way to
// assert on its output without this.
//
// A goroutine drains the pipe: a table wider than the pipe buffer would
// otherwise block the writer forever and hang the test rather than fail it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	os.Stdout = prev
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// --- a database with a trail in it --------------------------------------------

// auditFixture builds an instance with two accounts and a hand-written trail,
// and returns the path plus the two user ids.
//
// The rows are written through store.Audit with explicit `At` stamps, because
// every filter under test is a filter over time or action and a fixture whose
// rows all landed in the same millisecond cannot exercise either.
func auditFixture(t *testing.T) (dbPath, camID, deletedID string) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "audit.db")
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)

	hash, err := secret.HashPassword("a-rather-long-passphrase-42", secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	tenantID := idgen.New()
	camID = idgen.New()
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: tenantID, Name: "Test", UserID: camID, Username: "cam",
		Hash: hash, Role: "superadmin", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	// An id that resolves to no account, standing in for the tombstoned user
	// §7.9 requires the trail to keep naming.
	deletedID = idgen.New()

	now := time.Now().UTC()
	at := func(d time.Duration) string {
		return now.Add(-d).Format(time.RFC3339Nano)
	}
	for _, e := range []store.AuditEntry{
		// Newest first is the order the reader returns; written oldest first
		// here so the ordering assertion is not tautological.
		{At: at(72 * time.Hour), Action: string(audit.ActionLogin), ActorUserID: camID, TenantID: tenantID},
		{At: at(48 * time.Hour), Action: string(audit.ActionPasswordReset), ActorUserID: camID,
			ActingAsUser: deletedID, TenantID: tenantID,
			Detail: `{"reason":"break-glass","by":"operator"}`},
		// No tenant and no actor: the lockout case, recorded before the account
		// is resolved. This is the row AuditTrail cannot return and the whole
		// reason this command reads the instance view.
		{At: at(1 * time.Hour), Action: string(audit.ActionLockout),
			Detail: `{"username":"cam","attempts":"5"}`},
		{At: at(30 * time.Minute), Action: string(audit.ActionLogout), ActorUserID: camID, TenantID: tenantID},
	} {
		if err := repo.Audit(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	return dbPath, camID, deletedID
}

func runAudit(t *testing.T, dbPath string, args ...string) string {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var err error
	out := captureStdout(t, func() {
		err = auditCmd(log, append([]string{"-db", dbPath}, args...))
	})
	if err != nil {
		t.Fatalf("auditCmd %v: %v", args, err)
	}
	return out
}

// --- the command ---------------------------------------------------------------

// The default view: every row, newest first, with ids resolved to names.
func TestAuditPrintsTheWholeInstanceTrailNewestFirst(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	out := runAudit(t, dbPath)

	for _, want := range []string{"auth.logout", "auth.lockout", "account.password.reset", "auth.login"} {
		if !strings.Contains(out, want) {
			t.Errorf("the trail is missing %s:\n%s", want, out)
		}
	}
	if got, want := indexOfLine(out, "auth.logout"), indexOfLine(out, "auth.login"); got > want {
		t.Errorf("the trail is not newest-first; logout (30m ago) printed after login (72h ago):\n%s", out)
	}
	if !strings.Contains(out, "cam") {
		t.Errorf("actor ids were not resolved to usernames:\n%s", out)
	}
}

// The lockout row is the one this command exists for. It carries no tenant, so
// the tenant-scoped reader drops it, and an operator asking "was I attacked"
// would be shown a trail with every lockout silently missing.
func TestAuditShowsTheTenantlessLockoutRowsTheScopedReaderCannot(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	if out := runAudit(t, dbPath); !strings.Contains(out, "auth.lockout") {
		t.Errorf("the lockout is not in the trail — this command reads the INSTANCE view "+
			"precisely so tenantless rows appear:\n%s", out)
	}
}

// -alerts keeps everything except routine sign-in and sign-out. Getting this
// backwards would hide exactly the rows worth reading.
func TestAuditAlertsDropsRoutineSignInAndKeepsEverythingElse(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	out := runAudit(t, dbPath, "-alerts")

	for _, want := range []string{"auth.lockout", "account.password.reset"} {
		if !strings.Contains(out, want) {
			t.Errorf("-alerts dropped %s, which audit.Severity classifies as an alert:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"auth.login", "auth.logout"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("-alerts kept %s, which is Notice:\n%s", unwanted, out)
		}
	}
}

func TestAuditActionFiltersToTheNamedActionsOnly(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	out := runAudit(t, dbPath, "-action", "auth.login,auth.lockout")

	if !strings.Contains(out, "auth.login") || !strings.Contains(out, "auth.lockout") {
		t.Errorf("-action dropped one of the actions it was given:\n%s", out)
	}
	if strings.Contains(out, "account.password.reset") {
		t.Errorf("-action kept an action it was not given:\n%s", out)
	}
	// Whitespace around a name is what a shell quote produces and must not
	// silently match nothing.
	if out := runAudit(t, dbPath, "-action", " auth.login , "); !strings.Contains(out, "auth.login") {
		t.Errorf("-action did not trim whitespace around a name:\n%s", out)
	}
}

func TestAuditSinceExcludesOlderEntries(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	out := runAudit(t, dbPath, "-since", "2h")

	if !strings.Contains(out, "auth.lockout") || !strings.Contains(out, "auth.logout") {
		t.Errorf("-since 2h dropped an entry from the last two hours:\n%s", out)
	}
	if strings.Contains(out, "auth.login") {
		t.Errorf("-since 2h kept the login from 72h ago:\n%s", out)
	}
}

// The over-fetch. The store's LIMIT is applied before the Go-side filters, so
// asking for one row and filtering to alerts would return a login, drop it, and
// print nothing — "no alerts" for an instance that has two.
func TestAuditOverFetchesSoAFilterStillFillsTheRequestedCount(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	out := runAudit(t, dbPath, "-alerts", "-n", "1")

	// The newest alert is the lockout; the login and logout that sit above and
	// below it in time must not have consumed the budget.
	if !strings.Contains(out, "auth.lockout") {
		t.Errorf("-alerts -n 1 printed no alert; the LIMIT was spent on rows the filter drops:\n%s", out)
	}
	if strings.Contains(out, "account.password.reset") {
		t.Errorf("-n 1 printed more than one entry:\n%s", out)
	}
}

// An empty result says so on stderr and still exits zero. An empty table and a
// broken query look identical, which is the failure this sentence exists for.
func TestAuditSaysSoWhenNothingMatchesRatherThanPrintingAnEmptyTable(t *testing.T) {
	dbPath, _, _ := auditFixture(t)
	out := runAudit(t, dbPath, "-action", "no.such.action")
	if strings.TrimSpace(out) != "" {
		t.Errorf("an empty result printed a table header on stdout:\n%q", out)
	}
}

// -json is the piping path, so every field has to survive — including the ones
// the table abbreviates or drops.
func TestAuditJSONEmitsOneCompleteEntryPerLine(t *testing.T) {
	dbPath, camID, deletedID := auditFixture(t)
	out := runAudit(t, dbPath, "-json")

	sc := bufio.NewScanner(strings.NewReader(out))
	var got []store.AuditEntry
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e store.AuditEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("-json emitted a line that is not JSON: %q (%v)", sc.Text(), err)
		}
		got = append(got, e)
	}
	if len(got) != 4 {
		t.Fatalf("-json emitted %d entries, want the 4 in the fixture", len(got))
	}

	var reset store.AuditEntry
	for _, e := range got {
		if e.Action == string(audit.ActionPasswordReset) {
			reset = e
		}
	}
	if reset.ActorUserID != camID || reset.ActingAsUser != deletedID {
		t.Errorf("-json lost the actor/subject ids: %+v", reset)
	}
	// The FULL stamp, not the table's trimmed one — the point of -json is that
	// nothing was abbreviated on the way out.
	if _, err := time.Parse(time.RFC3339Nano, reset.At); err != nil {
		t.Errorf("-json emitted a trimmed timestamp %q; the table trims, this must not", reset.At)
	}
	if !strings.Contains(reset.Detail, "break-glass") {
		t.Errorf("-json lost the detail blob: %q", reset.Detail)
	}
}

// --- resolveActorNames ---------------------------------------------------------

// Both id columns are resolved, and an id that no longer names an account is
// left out of the map rather than being an error — §7.9 expects the trail to
// keep naming deleted accounts.
func TestResolveActorNamesResolvesBothColumnsAndSkipsTheDeleted(t *testing.T) {
	dbPath, camID, deletedID := auditFixture(t)
	ctx := context.Background()

	db, err := store.Open(store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := store.NewReaderRepo(db)

	names := resolveActorNames(ctx, repo, []store.AuditEntry{
		{ActorUserID: camID, ActingAsUser: deletedID},
		{}, // no ids at all: must not become a lookup for the empty string
	})
	if names[camID] != "cam" {
		t.Errorf("the actor id resolved to %q, want cam", names[camID])
	}
	if _, ok := names[deletedID]; ok {
		t.Error("an id that names no account was given a name")
	}
	if _, ok := names[""]; ok {
		t.Error("the empty id was looked up; a row with no actor is not a row about a user with no name")
	}
}

// --- nameOr --------------------------------------------------------------------

func TestNameOrMarksADeletedAccountRatherThanPrintingBareHex(t *testing.T) {
	names := map[string]string{"u1": "cam"}

	if got := nameOr(names, "", "—"); got != "—" {
		t.Errorf("an absent id printed %q, want the placeholder", got)
	}
	if got := nameOr(names, "u1", "—"); got != "cam" {
		t.Errorf("a known id printed %q, want cam", got)
	}
	// The suffix is the whole point: an unannotated id in the ACTOR column reads
	// as a username, and "an account that no longer exists did this" is exactly
	// the information §7.9 wants kept.
	if got := nameOr(names, "u2", "—"); got != "u2 (deleted)" {
		t.Errorf("an unresolved id printed %q, want it marked as deleted", got)
	}
}

// --- shortTime -----------------------------------------------------------------

func TestShortTimeTrimsAValidStampAndPassesThroughAnythingElse(t *testing.T) {
	at := "2026-08-08T14:03:09.123456789Z"
	got := shortTime(at)
	if got == at {
		t.Errorf("shortTime did not trim %q", at)
	}
	want := time.Date(2026, 8, 8, 14, 3, 9, 0, time.UTC).Local().Format("2006-01-02 15:04:05")
	if got != want {
		t.Errorf("shortTime = %q, want %q (the stamp is UTC, the column is local)", got, want)
	}

	// A corrupt stamp is PRINTED, not blanked or replaced with a zero time — a
	// row somebody tampered with is the one worth seeing.
	for _, bad := range []string{"", "not-a-time", "0"} {
		if got := shortTime(bad); got != bad {
			t.Errorf("shortTime(%q) = %q; an unparseable stamp must survive to the screen", bad, got)
		}
	}
}

// --- flattenDetail --------------------------------------------------------------

// Sorted output is what makes two runs over the same rows diffable, and map
// iteration order is random — so this asserts the exact string rather than the
// presence of each pair.
func TestFlattenDetailIsSortedAndThereforeStable(t *testing.T) {
	raw := `{"username":"cam","attempts":"5","window":"1h"}`
	const want = "attempts=5 username=cam window=1h"
	for i := 0; i < 20; i++ {
		if got := flattenDetail(raw); got != want {
			t.Fatalf("flattenDetail = %q, want %q (run %d — the order moved between runs)", got, want, i)
		}
	}
}

func TestFlattenDetailPassesThroughWhatItCannotDecode(t *testing.T) {
	// Blank and whitespace-only produce an empty column, not the literal text.
	for _, empty := range []string{"", "   ", "\n"} {
		if got := flattenDetail(empty); got != "" {
			t.Errorf("flattenDetail(%q) = %q, want an empty column", empty, got)
		}
	}
	// Anything that is not a flat string map is shown RAW. A detail blob written
	// by a future call site with a nested value must still reach the operator;
	// dropping it would hide the only context on that row.
	for _, raw := range []string{
		`{"count":5}`,          // a number, not a string
		`{"nested":{"a":"b"}}`, // an object
		`not json at all`,
		`[1,2,3]`,
	} {
		if got := flattenDetail(raw); got != raw {
			t.Errorf("flattenDetail(%q) = %q; an undecodable detail must survive verbatim", raw, got)
		}
	}
}

// --- indexOfLine ----------------------------------------------------------------

// indexOfLine reports which output line first contains sub, or -1.
func indexOfLine(out, sub string) int {
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return i
		}
	}
	return -1
}
