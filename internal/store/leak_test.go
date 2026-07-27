package store

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// G2 — the reflective cross-tenant leak harness (TODO 3.7, T1, R1).
//
// The plan calls this the highest-value test in the project, and the reason is
// arithmetic. `TestTenantIsolation` checks six methods by hand. There are 56
// exported repository methods and the number only goes up; hand-written coverage
// of a growing surface is coverage that decays, and it decays silently — the
// existing test keeps passing while the twelve methods added since it was
// written go unexamined.
//
// So this does not test methods. It ENUMERATES them, and fails on any method it
// cannot account for. Adding a repository method with a tenant leak fails this
// test; adding one this harness does not know how to call also fails it, until
// somebody writes down why. That second property is the one that makes it hold
// its value, because the first thing a hurried change does is find the gap.
//
// # What "leak" means here
//
// Tenant B's rows all carry a marker string. The harness calls every method
// under tenant A's Scope while passing tenant B's identifiers as arguments —
// which is the shape of the real attack: a valid session plus an id belonging to
// someone else, obtained from a shared link, a log, or a guess. Any marker
// appearing anywhere in any returned value, at any depth, is a leak.
//
// # Why guard 4 is not enough
//
// `internal/tools/guards` fails the build when a repository method omits its
// Scope parameter. That proves the scope is TAKEN. It says nothing about whether
// it reaches the WHERE clause, and a method that accepts a Scope and ignores it
// is exactly as leaky as one that never asked for it — while looking, in review,
// completely correct.

// leakMarker appears in every string field of every row tenant B owns. It is
// deliberately unlike anything else in the fixture: a substring match on it can
// have no other explanation.
const leakMarker = "TENANTB-LEAK-CANARY"

// unscopedByDesign lists exported methods that legitimately take no Scope.
//
// Every entry is a claim that the method is safe without one, and the claim is
// stated so it can be argued with. A method appearing here that should not is a
// tenant leak with a note attached, so this list is the part of the file to read
// first in review.
//
// The test fails when a method takes no Scope and is not listed, which is what
// stops this from being a list someone forgets to update.
var unscopedByDesign = map[string]string{
	// Bootstrap: these run before any Scope can exist.
	"CreateTenantAndUser": "creates the tenant a Scope would name",
	"AddUser":             "admin-level; the caller is the CLI, and there is no session yet",
	"FirstUserScope":      "produces a Scope; cannot require one",
	"FirstTenantID":       "same, for the tenant",
	"CountUsers":          "boot-time check for 7.9's 'refuse to serve with no superadmin'",

	// Authentication: by definition these run before the caller is identified.
	"ScopeForSession":      "turns a token into a Scope; requiring one would be circular",
	"UserForLogin":         "the login path, before identity is established",
	"ReconcileUnread":      "an instance-wide repair of denormalised columns; it reads nothing back to a caller and writes each row only the values of the item that row already names",
	"CreateSession":        "same",
	"RevokeSession":        "keyed by token hash, which the holder already has",
	"RevokeAllSessions":    "admin and break-glass; keyed by user id",
	"SetPasswordHash":      "break-glass reset (7.10), run from the CLI",
	"PurgeExpiredSessions": "housekeeping over expiry alone; touches no tenant data",
	"PurgeIdempotency":     "maintenance over every tenant's expired keys, like PurgeExpiredSessions",
	"Audit":                "the actor may be a tombstoned user and the tenant may be one being deleted (§7.9); the whole value of an audit log is surviving what it describes. AuditTrail, which READS it, does take a Scope.",

	// Global rows (A14). sources, items and favicons belong to no tenant, which
	// is the entire point of the source/subscription split — a Scope here would
	// be decorative, and worse, would imply an isolation that does not exist.
	"IngestItems": "writes global items; per-user state is fanout's job",
	"RecordFetch": "poll health on a global source row",
	"DueSources":  "the poller's queue over global sources",
	"PollerLag":   "instance-wide polling health over global sources (A14)",
	// Archives (6.12). Of GLOBAL items (A14): one copy serves every subscriber,
	// and two tenants on the same feed must not cause the same article to be
	// fetched and stored twice. Nothing per-user is stored or returned.
	"PutArchive":      "archives global items; one copy serves every subscriber (A14)",
	"GetArchive":      "same",
	"HasArchive":      "same",
	"MarkOriginDead":  "records that a global item's URL no longer resolves",
	"UnarchivedItems": "the §10.6 distress sweep over one global source",
	"EvictArchives":   "instance-wide disk reclamation",
	"ArchiveStats":    "instance-wide archive footprint for §22.6's ladder",

	// Scraped sources (4.7/6.8), added alongside this work. Reasons mirrored
	// from internal/tools/guards so the two lists cannot say different things
	// about the same method.
	"ScrapeRuleFor":       "a global source's extraction rule; the poller has no tenant (A14)",
	"ScopesToDerive":      "DISCOVERS scopes for the interest deriver rather than acting inside one; requiring a Scope would be circular, exactly like ScopeForSession. Background loop only — never reachable from an RPC",
	"RecordScrapeOutcome": "rule health on a global source, written by the poller (A14)",
	"KnownGUIDs":          "reads global item guids for one global source (A14)",
	"RecordOutlinks":      "outlinks are a property of a global item (A14); one extraction serves every subscriber",
	// Identity (5.1). Each of these either PRODUCES a Scope or runs before one
	// can exist — the same category as ScopeForSession, and the reason that one
	// is exempt too.
	"SeedSystemRoles":      "seeds the four built-in roles at boot, before any tenant exists",
	"RedeemInvite":         "the code IS the authorisation, and it names the tenant the account joins",
	"ScopeForAPIToken":     "produces a Scope from a token; requiring one would be circular",
	"ScopeForDevice":       "produces a Scope from a device the caller has just proved they hold the refresh secret for; requiring one would be circular, exactly like ScopeForSession",
	"RotateRefresh":        "the presented refresh token is the authorisation, and reuse revokes the family",
	"RetireUnusableSource": "deactivates a global source nobody subscribes to (A14/A22)",

	"SourceHosts": "global; feeds the favicon fetcher",
	"GetFavicon":  "global cache keyed by host",
	"PutFavicon":  "same",

	// Fan-out (6.7): global reads on behalf of every tenant at once.
	// FanoutItems is scoped, just by a Subscriber rather than a Scope — one
	// struct carrying tenant, user, folder, tags and source name, so there are
	// not two structs that can disagree about who the user is.
	"SubscribersOf": "lists every tenant's subscribers of one global source (A14)",
	"ItemsByID":     "reads global items; nothing per-user is returned",
	"FanoutItems":   "scoped by the Subscriber it takes, which carries tenant and user",

	// The job queue (6.4). Infrastructure rather than user data: a worker drains
	// it for everyone, and jobs.tenant_id records who the work is FOR so the
	// handler can build a Scope. Scoping resumes in the handlers.
	"Enqueue":       "queue infrastructure; jobs.tenant_id records who the work is for",
	"Claim":         "a worker drains the queue for every tenant",
	"Complete":      "keyed by job id, which the claiming worker already holds",
	"Fail":          "same",
	"GetJob":        "same",
	"ReclaimStale":  "maintenance over abandoned jobs",
	"QueueDepth":    "instance-wide queue health",
	"PurgeFinished": "maintenance over completed jobs",

	// Mailboxes (5.7). The poller has a mailbox id from DueMailboxes and no
	// session, exactly like DueSources/RecordFetch — but with a sharper edge,
	// because these rows hold a third party's password. Note what is NOT here:
	// MailboxSecret, which is the only method that decrypts one, takes a Scope
	// and is swept.
	"DueMailboxes":      "the IMAP poller's queue, across every tenant, like DueSources",
	"RecordMailboxPoll": "poll bookkeeping keyed by the mailbox id the worker just claimed; returns nothing",
	"CountMailboxes":    "an instance-wide count for §7.7's boot check; returns a number and nothing else, so there is no tenant data to leak",
	"CanStoreSecrets":   "reports whether an encryption key was configured; touches no rows at all",

	// Authentication, the persistent half (6.1). Every one of these runs BEFORE
	// identity exists, or on behalf of someone who cannot log in — which is the
	// same category as ScopeForSession and UserForLogin, and the reason those
	// are exempt too. Note what is NOT here: ReplaceRecoveryCodes and
	// RecoveryCodesRemaining are things a logged-in user does to their own
	// account, and both take a Scope.
	"RecordLoginAttempt": "the login ledger, written before identity is established and most valuable for accounts that do not exist",
	"FailureCounts":      "reads that ledger to decide a lockout, keyed by the username and address being attempted",
	"LastFailureAt":      "same ledger, same key",
	"PurgeLoginAttempts": "housekeeping over the ledger by age alone, like PurgeExpiredSessions",
	"ConsumeRecoveryCode": "a recovery code is presented by somebody who CANNOT log in; " +
		"requiring a Scope would defeat its only purpose. The code is the credential, " +
		"and it is bound to the user id passed alongside it.",
	"CreateResetToken":  "minted for an account by an admin or the CLI; the authorisation is checked at the service, and the token names the user it resets",
	"ConsumeResetToken": "the presented token is the authorisation, exactly like RotateRefresh",
	"PurgeResetTokens":  "housekeeping over spent and expired tokens by age alone",
}

// twoTenants builds the fixture: tenant A with its own rows, tenant B with rows
// whose every string carries leakMarker, and one source both subscribe to.
//
// The shared source matters. Isolation is easy when nothing overlaps; the real
// question is whether A can see B's *state* on a row they genuinely share, which
// is the case A14's global sources create and the one most likely to be wrong.
func twoTenants(t *testing.T, db *DB) (repo *ReaderRepo, a, b Scope, bIDs map[string]string) {
	t.Helper()
	ctx := context.Background()
	repo = NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, tt := range []NewTenant{
		{TenantID: "ta", Name: "A", UserID: "ua", Username: "alice", Hash: "x", Role: "member", Now: now},
		{TenantID: "tb", Name: leakMarker + "-tenant", UserID: "ub",
			Username: leakMarker + "-bob", Hash: "x", Role: "member", Now: now},
	} {
		if err := repo.CreateTenantAndUser(ctx, tt); err != nil {
			t.Fatal(err)
		}
	}
	a = Scope{TenantID: "ta", UserID: "ua", Role: "member"}
	b = Scope{TenantID: "tb", UserID: "ub", Role: "member"}
	bIDs = map[string]string{}

	// A shared source: both tenants subscribe to the same global feed.
	shared, _, err := repo.Subscribe(ctx, a, NewSubscription{
		NaturalKey: "feed:shared.example/rss",
		FeedURL:    "https://shared.example/rss",
		SiteURL:    "https://shared.example/",
		Title:      "Shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.Subscribe(ctx, b, NewSubscription{
		NaturalKey: "feed:shared.example/rss",
		FeedURL:    "https://shared.example/rss",
		SiteURL:    "https://shared.example/",
		Title:      leakMarker + "-shared",
	}); err != nil {
		t.Fatal(err)
	}

	// And one only B has.
	bOnly, _, err := repo.Subscribe(ctx, b, NewSubscription{
		NaturalKey: "feed:secret.example/rss",
		FeedURL:    "https://secret.example/rss",
		SiteURL:    "https://secret.example/",
		Title:      leakMarker + "-private-feed",
	})
	if err != nil {
		t.Fatal(err)
	}
	bIDs["sourceID"] = bOnly.SourceID
	_ = shared

	// Items on B's private source.
	if _, err := repo.IngestItems(ctx, bOnly.SourceID, []IngestItem{{
		GUID:        "b-item-1",
		URL:         "https://secret.example/1",
		Title:       leakMarker + "-article",
		Summary:     leakMarker + "-summary",
		ContentHTML: "<p>" + leakMarker + "-body</p>",
		PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
	items, _, err := repo.ListItems(ctx, b, ListQuery{Limit: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("B has no items: %v", err)
	}
	bIDs["itemID"] = items[0].ID
	bIDs["id"] = items[0].ID

	// B's per-user work: a note, a tag, a folder, read state.
	if err := repo.SetNote(ctx, b, items[0].ID, leakMarker+"-note"); err != nil {
		t.Fatal(err)
	}
	tag, err := repo.SetFeedTag(ctx, b, bOnly.SourceID, leakMarker+"tag", true)
	if err != nil {
		t.Fatal(err)
	}
	bIDs["tagID"] = tag.ID
	folder, err := repo.CreateFolder(ctx, b, leakMarker+"-folder")
	if err != nil {
		t.Fatal(err)
	}
	bIDs["folderID"] = folder.ID
	if _, err := repo.SetItemState(ctx, b, items[0].ID, StateChange{Read: boolPtr(true)}); err != nil {
		t.Fatal(err)
	}

	return repo, a, b, bIDs
}

func TestNoRepositoryMethodLeaksAcrossTenants(t *testing.T) {
	db := openTest(t)
	repo, a, b, bIDs := twoTenants(t, db)

	// EVERY scoped repository type, not just ReaderRepo.
	//
	// The harness swept one type for as long as there was one, and the day a
	// second appeared it would have gone on reporting a clean sweep of the
	// first. A guard whose coverage is a closed list has to be told when the
	// list grows, and nothing tells it — so the list is here, checked against
	// the package's actual repository types by TestEveryRepoTypeIsSwept below.
	mail := NewMailboxRepo(db, testEncKey)
	seedMailboxLeakFixture(t, mail, b, bIDs)

	sweep(t, repo, a, bIDs)
	sweep(t, mail, a, bIDs)
}

// sweep calls every scoped method on one repository under tenant A's scope with
// tenant B's identifiers, and fails if tenant B's data comes back.
func sweep(t *testing.T, repo any, a Scope, bIDs map[string]string) {
	t.Helper()
	ctx := context.Background()

	rt := reflect.TypeOf(repo)
	if rt.NumMethod() == 0 {
		t.Fatalf("%s has no exported methods — the harness is not looking at the repository", rt)
	}
	minMethods := 30
	if rt != reflect.TypeOf((*ReaderRepo)(nil)) {
		// ReaderRepo is the big one and its floor catches a harness that has
		// stopped seeing methods. A smaller repo needs a floor too, or adding
		// one with every method accidentally unscoped would pass silently.
		minMethods = 3
	}

	var checked, skipped int
	for i := 0; i < rt.NumMethod(); i++ {
		m := rt.Method(i)
		ft := m.Type

		scopeIdx := -1
		for arg := 1; arg < ft.NumIn(); arg++ {
			if ft.In(arg) == reflect.TypeOf(Scope{}) {
				scopeIdx = arg
				break
			}
		}

		if scopeIdx == -1 {
			why, ok := unscopedByDesign[m.Name]
			if !ok {
				t.Errorf("%s takes no Scope and is not in unscopedByDesign. "+
					"Either give it a Scope or write down why it is safe without one.", m.Name)
				continue
			}
			t.Logf("unscoped by design: %s — %s", m.Name, why)
			skipped++
			continue
		}

		t.Run(rt.Elem().Name()+"."+m.Name, func(t *testing.T) {
			args := make([]reflect.Value, ft.NumIn())
			args[0] = reflect.ValueOf(repo)
			for arg := 1; arg < ft.NumIn(); arg++ {
				switch {
				case arg == scopeIdx:
					// Tenant A's scope. This is the attacker's own valid session.
					args[arg] = reflect.ValueOf(a)
				case ft.In(arg) == reflect.TypeOf((*context.Context)(nil)).Elem():
					args[arg] = reflect.ValueOf(ctx)
				default:
					args[arg] = argFor(ft, arg, m.Name, bIDs)
				}
			}

			out := callAndRecover(t, m, args)
			for _, v := range out {
				if found := findMarker(v, 0); found != "" {
					t.Errorf("LEAK: %s returned tenant B data under tenant A's scope: %s",
						m.Name, found)
				}
			}
		})
		checked++
	}

	t.Logf("%s: %d scoped methods exercised, %d unscoped by design", rt, checked, skipped)
	if checked < minMethods {
		t.Errorf("only %d scoped methods were exercised on %s; it has more, "+
			"so the harness has stopped seeing them", checked, rt)
	}
}

// argFor synthesises an argument, preferring one of tenant B's identifiers.
//
// Passing B's ids under A's scope is the whole design. A harness that passed
// zero values would call every method with an empty id, get an empty result, and
// report that nothing leaks — which is true and worthless.
func argFor(ft reflect.Type, i int, method string, bIDs map[string]string) reflect.Value {
	t := ft.In(i)

	if t.Kind() == reflect.String {
		// The parameter names are not available through reflection, so the id is
		// chosen by method rather than by name. Where a method takes several
		// strings the first is the identifier in every current signature; the
		// rest get benign values, because a search query or a folder name is not
		// an access-control decision.
		switch method {
		case "GetItem", "SetItemState", "SetNote", "GetNote", "GetFeedSettings",
			"UpdateFeedSettings", "Unsubscribe", "SetFeedTag", "SourcesForTag",
			"UpdateTag", "DeleteFolder", "RenameFolder", "SetFeedFolder", "MarkAllRead":
			if v, ok := firstID(method, bIDs); ok {
				return reflect.ValueOf(v)
			}
		}
		return reflect.ValueOf("")
	}

	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// A limit of zero returns nothing, which would make every list method
		// vacuously clean.
		return reflect.ValueOf(int64(100)).Convert(t)
	case reflect.Bool:
		return reflect.ValueOf(true)
	case reflect.Slice:
		// A slice of ids: hand it B's item id, so ItemSignals and friends are
		// asked about a row they must not describe.
		if t.Elem().Kind() == reflect.String {
			s := reflect.MakeSlice(t, 1, 1)
			s.Index(0).SetString(bIDs["itemID"])
			return s
		}
		return reflect.MakeSlice(t, 0, 0)
	case reflect.Map:
		return reflect.MakeMap(t)
	case reflect.Ptr:
		return reflect.New(t.Elem())
	}
	return reflect.New(t).Elem()
}

func firstID(method string, bIDs map[string]string) (string, bool) {
	switch method {
	case "GetItem", "SetItemState", "SetNote", "GetNote":
		return bIDs["itemID"], true
	case "SourcesForTag", "UpdateTag":
		return bIDs["tagID"], true
	case "DeleteFolder", "RenameFolder":
		return bIDs["folderID"], true
	default:
		return bIDs["sourceID"], true
	}
}

// callAndRecover invokes the method, turning a panic into a failure rather than
// letting one bad method abort the whole sweep. "Method X panics when handed
// another tenant's id" is itself a finding worth reporting.
func callAndRecover(t *testing.T, m reflect.Method, args []reflect.Value) (out []reflect.Value) {
	t.Helper()
	defer func() {
		if v := recover(); v != nil {
			t.Errorf("%s panicked when passed another tenant's identifier: %v", m.Name, v)
			out = nil
		}
	}()
	return m.Func.Call(args)
}

// findMarker walks a value to any depth looking for the canary.
//
// Depth-limited because a cyclic structure would otherwise hang the test, and a
// test that hangs is a test that gets deleted.
func findMarker(v reflect.Value, depth int) string {
	if depth > 8 || !v.IsValid() {
		return ""
	}
	switch v.Kind() {
	case reflect.String:
		if strings.Contains(v.String(), leakMarker) {
			return v.String()
		}
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return ""
		}
		return findMarker(v.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if s := findMarker(v.Index(i), depth+1); s != "" {
				return s
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if s := findMarker(k, depth+1); s != "" {
				return s
			}
			if s := findMarker(v.MapIndex(k), depth+1); s != "" {
				return s
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			// Unexported fields cannot be read through reflection without
			// unsafe. Every leak-carrying type here is a plain data struct with
			// exported fields, so skipping them loses nothing.
			if !v.Type().Field(i).IsExported() {
				continue
			}
			if s := findMarker(v.Field(i), depth+1); s != "" {
				return s
			}
		}
	}
	return ""
}

// The harness must be able to fail. A leak test that cannot detect a leak is
// worse than no leak test, because it is also a reassurance — so this proves the
// detector by handing it the leak directly.
func TestLeakHarnessDetectsAnActualLeak(t *testing.T) {
	db := openTest(t)
	repo, _, b, _ := twoTenants(t, db)
	ctx := context.Background()

	// Tenant B reading its own rows: the canary must be all over this.
	items, _, err := repo.ListItems(ctx, b, ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if findMarker(reflect.ValueOf(items), 0) == "" {
		t.Fatal("the fixture does not carry the marker into item rows, so the sweep " +
			"above proves nothing — every method would pass by returning nothing")
	}

	feeds, err := repo.ListFeeds(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if findMarker(reflect.ValueOf(feeds), 0) == "" {
		t.Error("the marker does not reach feed rows")
	}
}

// Cross-tenant access returns NotFound, never PermissionDenied (§20.7, 7.3a).
//
// The distinction is not pedantry. PermissionDenied confirms the object exists,
// so an attacker holding a list of guessed ids can separate the real ones from
// the invented ones by error code alone — a tenant leak with good manners.
func TestCrossTenantErrorsDoNotConfirmExistence(t *testing.T) {
	db := openTest(t)
	repo, a, _, bIDs := twoTenants(t, db)
	ctx := context.Background()

	real := bIDs["itemID"]
	const invented = "definitely-not-an-id"

	_, errReal := repo.GetItem(ctx, a, real)
	_, errFake := repo.GetItem(ctx, a, invented)
	if errReal != errFake {
		t.Errorf("a real id of another tenant errors %v while an invented one errors %v; "+
			"the difference is enough to enumerate", errReal, errFake)
	}
	if errReal != ErrNotFound {
		t.Errorf("cross-tenant GetItem = %v, want ErrNotFound", errReal)
	}
}

// testEncKey is a fixed 32-byte key. Fixed rather than random so a ciphertext
// written by one test can be read by another, and so a failure is reproducible.
var testEncKey = []byte("0123456789abcdef0123456789abcdef")

// seedMailboxLeakFixture gives tenant B a mailbox and a mailbox-derived source,
// every string in them carrying the marker.
//
// Newsletters are the most sensitive rows in the database: a mailbox leak is
// somebody's email account, and a mailbox SOURCE leak is their private mail.
// §6.4's per-user keying is the defence, and this is what tests that it holds.
func seedMailboxLeakFixture(t *testing.T, mail *MailboxRepo, b Scope, bIDs map[string]string) {
	t.Helper()
	ctx := context.Background()

	id, err := mail.PutMailbox(ctx, b, Mailbox{
		Host:     leakMarker + ".imap.example",
		Port:     993,
		Username: leakMarker + "@example.com",
		Folder:   leakMarker + "-Newsletters",
	}, leakMarker+"-password")
	if err != nil {
		t.Fatal(err)
	}
	bIDs["mailboxID"] = id

	if _, err := mail.EnsureMailboxSource(ctx, b, id,
		leakMarker+"@sender.example", leakMarker+"-newsletter"); err != nil {
		t.Fatal(err)
	}
}

// TestEveryRepoTypeIsSwept is the guard on the guard.
//
// The leak harness sweeps a hand-written list of repository types, and a list
// nothing checks is a list that goes stale — which is the exact failure mode
// this whole file exists to prevent, one level up. So the list is compared
// against the package's real repository types: anything named `*Repo` with a
// constructor must be swept, or named here with a reason.
//
// Go has no reflection over a package's types at runtime, so this reads the
// source. That is unusual and it is the only thing that actually works: the
// alternative is a list that agrees with itself.
func TestEveryRepoTypeIsSwept(t *testing.T) {
	swept := map[string]bool{
		"ReaderRepo":  true,
		"MailboxRepo": true,
	}
	notSwept := map[string]string{
		// SettingsRepo is instance-wide by design: system settings belong to no
		// tenant. Its tenant- and user-layer reads live on ReaderRepo, which IS
		// swept — see settingslayers.go.
		"SettingsRepo": "system settings are instance-wide; the tenant/user layers are on ReaderRepo",
	}

	found, err := repoTypeNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(found) < 2 {
		t.Fatalf("found %d repository types by scanning the package; the scan is broken", len(found))
	}
	for _, name := range found {
		if swept[name] {
			continue
		}
		if why, ok := notSwept[name]; ok {
			t.Logf("not swept: %s — %s", name, why)
			continue
		}
		t.Errorf("%s is a repository type and the leak harness does not sweep it. "+
			"Add it to sweep() in TestNoRepositoryMethodLeaksAcrossTenants, or "+
			"write down here why it needs no tenant isolation.", name)
	}
}

// repoTypeNames finds `type XxxRepo struct` declarations in this package.
func repoTypeNames() ([]string, error) {
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "type ") || !strings.Contains(line, "struct") {
				continue
			}
			name := strings.Fields(line)[1]
			if strings.HasSuffix(name, "Repo") {
				out = append(out, name)
			}
		}
	}
	return out, nil
}
