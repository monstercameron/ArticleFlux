// Command guards enforces the structural decisions that a plausible-looking
// change could otherwise violate silently (TODO 1.8, plan.md §22.14).
//
// Why this exists at Tier 1, before there is anything to guard: under
// waterfall-then-implement, the decisions are made once and then implemented over
// weeks. The failure mode is not someone arguing against A26 — it is someone (me,
// most likely) adding a stylesheet at 2am because it was the fast way past a
// problem, and nobody noticing for a month. A guard is how a decision survives
// contact with implementation.
//
// The guards:
//
//  1. No SQL outside internal/store       — one place understands the schema
//  2. No syscall/js outside client/platform — A26; keeps the client portable and testable natively
//  3. No .css files anywhere              — A26; CSS is authored in Go
//  4. Every repository method takes Scope — tenant isolation is structural, not remembered (T1)
//  5. No hardcoded UI copy in client/view — the i18n catalog stays complete (8.4a, §22.16)
//  6. No global SchemaFlux provider      — one tenant's key must not become everyone's (G1, P1.6)
//  7. No unsanitised render path in client/ — article HTML is stored RAW, so the
//     sanitizer on the read side is the only thing between a publisher and the DOM
//
// A guard with nothing to check yet reports "n/a" rather than "pass". Passing
// vacuously is how a guard quietly stops being a guard.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type finding struct{ file, detail string }

type guard struct {
	name     string
	findings []finding
	// checked counts what the guard actually inspected, so "0 findings" can be
	// distinguished from "0 inspected".
	checked int
	note    string
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}

	guards := []*guard{
		guardNoSQLOutsideStore(root),
		guardNoSyscallJS(root),
		guardNoCSSFiles(root),
		guardRepoScope(root),
		guardNoInlineCopy(root),
		guardNoGlobalLLMProvider(root),
		guardNoUnsanitisedRender(root),
	}

	failed := false
	for _, g := range guards {
		switch {
		case len(g.findings) > 0:
			failed = true
			fmt.Printf("FAIL  %s  (%d)\n", g.name, len(g.findings))
			for _, f := range g.findings {
				fmt.Printf("        %s: %s\n", f.file, f.detail)
			}
		case g.checked == 0:
			fmt.Printf("n/a   %s  — %s\n", g.name, g.note)
		default:
			fmt.Printf("ok    %s  (%d checked)\n", g.name, g.checked)
		}
	}
	if failed {
		os.Exit(1)
	}
}

// --- 1. no SQL outside internal/store ----------------------------------------

// reSQL matches SQL in a string literal. Anchored to statement openers rather
// than to bare keywords, so ordinary prose containing "update" or "select" in a
// message string does not trip it.
var reSQL = regexp.MustCompile(`(?is)\b(SELECT\s+.+\s+FROM|INSERT\s+INTO|UPDATE\s+\w+\s+SET|DELETE\s+FROM|CREATE\s+(TABLE|VIRTUAL\s+TABLE|INDEX|TRIGGER)|PRAGMA\s+\w+)\b`)

func guardNoSQLOutsideStore(root string) *guard {
	g := &guard{
		name: "no SQL outside internal/store",
		note: "no Go files inspected",
	}
	walkGo(root, func(path string, f *ast.File, fset *token.FileSet) {
		rel := relSlash(root, path)
		if strings.HasPrefix(rel, "internal/store/") ||
			strings.HasPrefix(rel, "internal/tools/guards/") ||
			strings.HasPrefix(rel, "migrations/") {
			return
		}
		// Tests may hold SQL; production code may not. That is the line this
		// guard is actually drawing — "a second place that understands the
		// schema" is a liability when it SHIPS, and two cases here cannot be
		// written any other way:
		//
		//   - internal/fanout deliberately corrupts a subscription row to prove
		//     the defence against a corrupt one fires. It cannot go through the
		//     store API, because the store API is what prevents that state.
		//   - internal/reader counts rows in a table the repository has no
		//     reader for, and adding one would be production surface with a
		//     single test caller.
		//
		// The cost is real and worth naming: a test that quietly reimplements a
		// production query is no longer caught here. It is still caught by the
		// query being wrong.
		if strings.HasSuffix(rel, "_test.go") {
			return
		}
		g.checked++
		// Only string literals are inspected — a comment quoting a query is
		// documentation, not a second place that understands the schema.
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			val, err := strconv.Unquote(lit.Value)
			if err != nil {
				val = lit.Value
			}
			if m := reSQL.FindString(val); m != "" {
				g.findings = append(g.findings, finding{
					file:   fmt.Sprintf("%s:%d", rel, fset.Position(lit.Pos()).Line),
					detail: "SQL in a string literal: " + condense(m),
				})
			}
			return true
		})
	})
	return g
}

// --- 2. no syscall/js outside client/platform ---------------------------------

func guardNoSyscallJS(root string) *guard {
	g := &guard{
		name: "no syscall/js outside client/platform",
		note: "no Go files inspected",
	}
	walkGo(root, func(path string, f *ast.File, fset *token.FileSet) {
		rel := relSlash(root, path)
		if strings.HasPrefix(rel, "client/platform/") ||
			strings.HasPrefix(rel, "internal/tools/guards/") {
			return
		}
		g.checked++
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "syscall/js" {
				g.findings = append(g.findings, finding{
					file:   fmt.Sprintf("%s:%d", rel, fset.Position(imp.Pos()).Line),
					detail: `imports "syscall/js" — it belongs behind client/platform (A26)`,
				})
			}
		}
	})
	return g
}

// --- 3. no .css files ---------------------------------------------------------

func guardNoCSSFiles(root string) *guard {
	g := &guard{
		name: "no .css files (A26)",
		note: "no files inspected",
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if skipFile(root, path) {
			return nil
		}
		g.checked++
		if strings.EqualFold(filepath.Ext(path), ".css") {
			g.findings = append(g.findings, finding{
				file:   relSlash(root, path),
				detail: "CSS is authored in Go through the css package (A26)",
			})
		}
		return nil
	})
	return g
}

// --- 4. repository methods take Scope -----------------------------------------

// unscopedByDesign are repository methods that legitimately take no Scope.
//
// Each one operates on a GLOBAL table (A14: `sources` and `items` are shared
// across tenants and polled once), so there is no tenant to scope to. That is
// the design, not an oversight — but the exemption is a list rather than a
// convention, so adding to it is a visible decision rather than a method that
// quietly forgot its Scope.
var unscopedByDesign = map[string]string{
	"IngestItems": "writes global items (A14); no tenant owns them",
	// item_analysis (§27.2, TODO 10.5). Global for the same reason `items` is:
	// one analysis per ITEM, not per subscriber, or classifying an article a
	// hundred people follow costs a hundred times what the article did. The
	// table carries no tenant_id and no user_id, so there is nothing to scope
	// to. Reasons mirrored from internal/store/leak_test.go so the two lists
	// cannot say different things about the same method.
	"RecentItemIDs":    "the newest global items (A14), for a probe that pairs them with ItemsByID; no per-user slice exists to scope to",
	"UpsertAnalysis":   "writes global analysis rows; per-user labeling is fanout's job, same split as IngestItems",
	"AnalysisByIDs":    "reads global analysis rows; nothing per-user is returned, same as ItemsByID",
	"StaleAnalysis":    "the backfill's queue over global items and their (possibly absent) analysis rows",
	"ClearAnalysis":    "the repair tool for a fully derived global table (§27.2c); no per-user slice exists to scope to",
	"PendingSmartPlus": "the Smart+ retry queue over global analysis rows, keyed by llm_at alone",
	"RecordFetch":      "updates global source health (A14)",
	"DueSources":       "the scheduler polls for every tenant at once (A14)",
	// A scrape rule belongs to the SOURCE, which is global: it is the site's
	// selectors, not anybody's preference, and the poller that reads it has no
	// user. The WRITE path (PutScrapeRule) does take a Scope and checks the
	// subscription, which is where the isolation actually belongs.
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
	"PollerLag":            "instance-wide polling health over global sources (A14)",
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

	"Close":         "not a query",
	"Path":          "not a query",
	"Tx":            "the caller's fn carries the scope",
	"Migrate":       "schema, not data",
	"SchemaVersion": "schema, not data",
	// Identity bootstrap: these create or produce a Scope, so there is none to
	// take. FirstUserScope is additionally gated on Config.DevMode.
	"CreateFirstUser":     "claims an instance that has no tenant and no user yet; it is the call that CREATES the first scope, so requiring one is circular (§7.11)",
	"CreateTenantAndUser": "creates the tenant and its first user; no scope exists yet",
	"ScopeForSession":     "produces a Scope from a session token",
	"FirstUserScope":      "produces the dev Scope; gated on DevMode + loopback",
	// The login path. Each of these runs before a Scope can exist, or is the
	// thing that ends one. Note what is NOT here: Identity takes a Scope, because
	// "who am I" is a question about an authenticated caller and answering it
	// unscoped would let any session read any user's row.
	"UserForLogin":            "produces the identity a Scope is built from; there is none yet",
	"AuditTrailInstance":      "the INSTANCE view of the audit log, for the operator CLI. Some security events — a login lockout above all — are recorded before the account is resolved and may name a username that does not exist, so those rows carry no tenant and the tenant-scoped AuditTrail cannot return them. The audience is somebody with the database file in their hands",
	"UserForRecovery":         "the same, for a credential that NAMES its user instead of being presented by one: a reset token carries a user id and nothing else, so the account has to be resolved before a Scope can exist. Returns no password hash — nothing on that path verifies one — and refuses a deactivated account, exactly as UserForLogin does",
	"CreateSession":           "creates the credential a Scope is resolved from",
	"RevokeSession":           "the token hash IS the authorisation to revoke it",
	"SweepItems":              "retention over GLOBAL items (A14) — an item belongs to no tenant, so a window is instance-wide by construction; it deletes nothing anybody starred, annotated, tagged, archived, shared or corrected",
	"RecordSweep":             "writes the retention ledger, which accounts for rows that belong to no tenant",
	"RecentSweeps":            "reads that ledger: counts only, never titles, so it cannot become a record of anybody's reading",
	"ShareBySlug":             "the slug IS the credential; a public feed's reader has no identity to scope by",
	"ShareSources":            "reads the scope behind a slug the caller already presented; the owner comes from the share row",
	"SessionAuthenticatedAt":  "reads one session's sudo stamp by token hash; the hash IS the session, exactly as for RevokeSession",
	"StampAuthenticated":      "records a re-authentication against the token hash the caller just proved it holds",
	"RevokeOtherSessions":     "the token hash IS the session being kept; the user id comes from the scope it resolved to",
	"RevokeSessionAndFamily":  "the token hash IS the authorisation to revoke it, exactly as for RevokeSession",
	"FamilyForSession":        "resolves a family from a session token hash the caller has already proved it holds, exactly as for SessionAuthenticatedAt",
	"ChangePasswordAndRevoke": "the CLI break-glass reset has no session (like SetPasswordHash); the RPC caller's authorisation is the fresh sudo window `requireSudo` already checked, and the user id it acts on is the scope that produced",
	"PurgeExpiredSessions":    "maintenance over every tenant's dead rows",
	"PurgeIdempotency":        "maintenance over every tenant's expired keys, like PurgeExpiredSessions",
	"Audit":                   "the actor may be a tombstoned user and the tenant may be one being deleted (§7.9); the whole value of an audit log is surviving what it describes. AuditTrail, which READS it, does take a Scope.",
	"CountUsers":              "asked at boot, before any Scope can exist",
	"RevokeAllSessions":       "the CLI break-glass reset has no session of its own",
	"SetPasswordHash":         "the CLI break-glass reset has no session",
	"AddUser":                 "the operator creating an account has no session",
	// System settings (§6.3, scope='system'). Instance configuration, not user
	// data: the OpenAI API key, the Smart+ model, the translated UI catalogs.
	// Global rows in the same sense sources and items are global under A14.
	// Named distinctly rather than Get/Set so exempting them here cannot
	// accidentally exempt a Get on some future tenant-scoped repository.
	// Authorisation is at the transport, where the caller is checked for being
	// an owner.
	"SystemValue":    "reads a scope='system' row; instance config, no tenant owns it",
	"SetSystemValue": "writes a scope='system' row; instance config, no tenant owns it",
	// Two SystemValue reads and a fallback between them, so it is exempt for
	// exactly SystemValue's reason and no new one: which model a tier uses is a
	// property of the instance and its OpenAI account, not of whoever happens to
	// be reading when the call is made.
	"ModelForTier":      "resolves a tier's model from scope='system' rows; instance config, no tenant owns it",
	"SystemSecret":      "reads the instance's encrypted credential",
	"SetSystemSecret":   "writes the instance's encrypted credential",
	"DeleteSystemValue": "removes a scope='system' row",
	"CanStoreSecrets":   "reports whether an encryption key exists; not a query",
	"FirstTenantID":     "how the CLI finds the one tenant of a single-instance install",
	// Favicons are a property of the public web, cached once for everyone (A14).
	"GetFavicon":  "global icon cache; no tenant owns a favicon",
	"PutFavicon":  "global icon cache",
	"SourceHosts": "global sources, for the icon warmer",

	// Fan-out (6.7). These read GLOBAL rows on behalf of every tenant at once,
	// which is what fan-out is: one polled source, many subscribers across many
	// tenants. A Scope here would be a Scope the caller had to invent.
	//
	// FanoutItems is the one worth pausing on. It is genuinely scoped — it just
	// carries the scope as a `Subscriber`, which holds the tenant and user and
	// additionally the folder, tags and source name its rules need. Splitting
	// that into (Scope, Subscriber) would mean two structs that must agree about
	// who the user is, and the failure of that disagreement is silent
	// cross-account writes. One struct cannot disagree with itself.
	"SubscribersOf": "lists every tenant's subscribers of one global source (A14)",
	"ItemsByID":     "reads global items; nothing per-user is returned (A14)",
	"FanoutItems":   "scoped by the Subscriber it takes, which carries tenant and user",

	// The job queue (6.4). Jobs are infrastructure, not user data: the queue is
	// drained by the server on behalf of everyone, and a worker that could only
	// claim its own tenant's work would need a tenant before it had a job to
	// tell it which. `jobs.tenant_id` records who the work is FOR so a handler
	// can build a Scope; it is not an access-control boundary at this layer,
	// and the handlers are where scoping resumes.
	"Enqueue":       "queue infrastructure; jobs.tenant_id records who the work is for",
	"Claim":         "a worker drains the queue for every tenant",
	"Complete":      "keyed by job id, which the claiming worker already holds",
	"Fail":          "same",
	"GetJob":        "same",
	"ReclaimStale":  "maintenance over every tenant's abandoned jobs",
	"QueueDepth":    "instance-wide queue health for the status screen",
	"PurgeFinished": "maintenance over completed jobs of every tenant",

	// The denormalised unread count (5.4a). Instance-wide repair: it writes each
	// state row only the values of the item that row already names, and returns
	// a count rather than any row.
	"ReconcileUnread": "an instance-wide repair of denormalised columns; it reads nothing back to a caller and writes each row only the values of the item that row already names",

	// Mailboxes (5.7). The IMAP poller has a mailbox id from DueMailboxes and no
	// session, exactly like DueSources/RecordFetch. Note what is NOT here:
	// MailboxSecret, the only method that decrypts a credential, takes a Scope.
	"DueMailboxes":      "the IMAP poller's queue, across every tenant, like DueSources",
	"RecordMailboxPoll": "poll bookkeeping keyed by the mailbox id the worker just claimed; returns nothing",
	"CountMailboxes":    "an instance-wide count for §7.7's boot check; returns a number and nothing else, so there is no tenant data to leak",

	// Authentication, the persistent half (6.1). Every one of these runs BEFORE
	// identity exists, or on behalf of someone who cannot log in — which is the
	// same category as ScopeForSession and UserForLogin, and the reason those
	// are exempt too. Note what is NOT here: ReplaceRecoveryCodes and
	// RecoveryCodesRemaining are things a logged-in user does to their own
	// account, and both take a Scope.
	"RecordLoginAttempt":  "the login ledger, written before identity is established and most valuable for accounts that do not exist",
	"FailureCounts":       "reads that ledger to decide a lockout, keyed by the username and address being attempted",
	"LastFailureAt":       "same ledger, same key",
	"PurgeLoginAttempts":  "housekeeping over the ledger by age alone, like PurgeExpiredSessions",
	"PurgeAuditLog":       "housekeeping over the audit log by age alone. Unscoped for the same reason AuditTrailInstance is: instance-level rows carry no tenant, and a scoped purge would leave exactly those behind forever.",
	"ConsumeRecoveryCode": "a recovery code is presented by somebody who CANNOT log in; requiring a Scope would defeat its only purpose. The code is the credential and it is bound to the user id passed alongside it.",
	"CreateResetToken":    "minted for an account by an admin or the CLI; the authorisation is checked at the service, and the token names the user it resets",
	"ConsumeResetToken":   "the presented token is the authorisation, exactly like RotateRefresh",
	"PurgeResetTokens":    "housekeeping over spent and expired tokens by age alone",
}

// guardRepoScope checks that repository methods take a Scope.
//
// It matches on the receiver type name ending in "Repo" rather than on a
// directory, because repositories live beside the rest of the SQL in
// internal/store — the two guards enforce different things (where SQL may live,
// and that data access is scoped) and should not be coupled to the same layout.
func guardRepoScope(root string) *guard {
	g := &guard{
		name: "repository methods take Scope",
		note: "no *Repo types found yet (Tier 5)",
	}
	walkGo(root, func(path string, f *ast.File, fset *token.FileSet) {
		rel := relSlash(root, path)
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel, "internal/tools/guards/") {
			return
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			if len(fn.Recv.List) == 0 || !strings.HasSuffix(typeName(fn.Recv.List[0].Type), "Repo") {
				continue
			}
			if _, exempt := unscopedByDesign[fn.Name.Name]; exempt {
				continue
			}
			g.checked++
			if !takesScope(fn) {
				g.findings = append(g.findings, finding{
					file: fmt.Sprintf("%s:%d", rel, fset.Position(fn.Pos()).Line),
					detail: fn.Name.Name + " has no Scope parameter — tenant isolation must be " +
						"structural, not remembered (T1). If it operates on a global table, " +
						"add it to unscopedByDesign with the reason.",
				})
			}
		}
	})
	return g
}

func takesScope(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, p := range fn.Type.Params.List {
		if strings.HasSuffix(typeName(p.Type), "Scope") {
			return true
		}
	}
	return false
}

func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return typeName(t.X)
	}
	return ""
}

// --- shared -------------------------------------------------------------------

func walkGo(root string, fn func(path string, f *ast.File, fset *token.FileSet)) {
	fset := token.NewFileSet()
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		// Generated protobuf is not hand-written code and is not ours to shape.
		if strings.HasSuffix(path, ".pb.go") {
			return nil
		}
		if skipFile(root, path) {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		fn(path, f, fset)
		return nil
	})
}

// tracked is the set of files git knows about, or nil when that could not be
// determined.
//
// # Why the guards only look at tracked files
//
// A guard exists to protect what is IN the repository. An untracked file is
// somebody's scratch — a probe script written against whatever bug was in front
// of them, a half-finished experiment, a database dump — and letting one of
// those turn CI red means the guard stops being a signal and becomes a thing
// people learn to skip past. That happened: a throwaway `tq3/main.go` that
// queried the live database sat in the repo root and failed the SQL guard, on a
// tree where nothing wrong had been committed.
//
// Nothing is lost by this. In CI the tree comes from `actions/checkout`, so
// every file is tracked and every guard applies to all of it. Locally it means
// your scratch directory is your business until you `git add` it — at which
// point it is source, and the guards have an opinion again.
//
// A nil set (no git, not a repository, git not on PATH) means scan EVERYTHING.
// A guard that silently checks nothing because a subprocess failed is worse than
// one that occasionally complains about a scratch file.
var tracked = func() map[string]bool {
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			set[path.Clean(p)] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}()

// skipFile reports whether a path is outside the guards' remit.
func skipFile(root, p string) bool {
	if tracked == nil {
		return false
	}
	return !tracked[relSlash(root, p)]
}

func skipDir(name string) bool {
	switch name {
	case ".git", "bin", "node_modules", "vendor", "design", ".github",
		// Test output, not source. Playwright traces embed the CSS of every page
		// they captured — including third-party pages — so scanning them makes
		// the A26 guard fire on somebody else's stylesheet.
		"test-results", "playwright-report", "shots", ".tmp", "web":
		// design/ holds hand-written HTML/CSS mockups that are specifications,
		// not source — nobody ports them, and guarding them would be guarding
		// the spec instead of the build. web/ holds build output.
		return true
	}
	return false
}

func relSlash(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel)
}

func condense(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

// --- 6. no global SchemaFlux provider -----------------------------------------

// forbiddenGlobalProvider is every way SchemaFlux can be told to remember a
// provider — or a client holding one — in package-level state.
//
// **This is the migration's one blocker that can leak a credential across
// tenants** (G1 in docs/AI_SCHEMAFLOW_MIGRATION.md, and P1.6 is this guard).
// SchemaFlux's fluent builders resolve their provider from a package-level
// default: `Init` sets it, the `WithProvider*` methods set it, and every call
// made anywhere in the process then uses whatever was registered last.
//
// ArticleFlux is multi-tenant and its OpenAI key is a per-instance encrypted
// setting, resolved from the tenant scope on `ctx` at the moment of the call.
// Registering one instance's provider globally would therefore hand that
// instance's key to every other instance's request — silently, with no error
// and nothing in a log to notice, because from the library's side it is
// working exactly as documented.
//
// The shape that avoids it is the one internal/llm uses: build the provider per
// call, chain SchemaFlux's middleware around it with `mw.Chain`, and never
// register anything. That path never touches any of the names below, which is
// what makes this greppable rule sufficient rather than merely indicative.
//
// Tests are NOT exempt. There is no legitimate use in this repository, and a
// test that set the global would set it for every other test in the same
// binary — which is the failure this exists to prevent, arriving through the
// one door nobody audits.
var forbiddenGlobalProvider = map[string]string{
	"Init":                 "registers a package-level provider for the whole process",
	"SetDefaultClient":     "registers a package-level client for the whole process",
	"GetDefaultClient":     "reads the package-level client, whose provider belongs to whoever registered last",
	"InitWithEnv":          "registers a package-level provider from the environment",
	"SetDefaultProvider":   "registers a package-level provider for the whole process",
	"WithProvider":         "sets the client's provider from the package-level registry",
	"WithProviderConfig":   "builds and registers a provider from static config, not from the tenant's key",
	"WithProviderInstance": "registers a provider on the package-level client",
	"WithMockProvider":     "registers a mock on the package-level client",
	// P1.1b / OTEL-15. Not a credential leak — a telemetry one, and it is the
	// same shape of mistake: SchemaFlux's InitTracing calls
	// otel.SetTracerProvider and otel.SetTextMapPropagator, which would replace
	// the providers internal/telemetry built, along with their resource
	// attributes and their exporters. ArticleFlux's OTel stack would keep
	// reporting and nothing would arrive where it was configured to go.
	//
	// It is opt-in upstream, so the rule is simply never to call it — which is
	// exactly the kind of rule that survives as a comment for about a month.
	"InitTracing": "replaces ArticleFlux's global tracer provider and propagator (OTEL-15)",
}

func guardNoGlobalLLMProvider(root string) *guard {
	g := &guard{
		name: "no global SchemaFlux provider",
		note: "no Go files inspected",
	}
	walkGo(root, func(path string, f *ast.File, fset *token.FileSet) {
		rel := relSlash(root, path)
		// This file names every forbidden identifier, in the map above.
		if strings.HasPrefix(rel, "internal/tools/guards/") {
			return
		}
		// The one exemption, and the distinction it rests on.
		//
		// `internal/llm/ops.go` registers a provider globally, which is what
		// every other line of this rule forbids. It is safe there, and only
		// there, because the provider it registers holds NO TENANT STATE: it
		// resolves the API key by calling KeyFunc(ctx) at the moment of the
		// call, so the same object is correct for every instance and "which
		// tenant's provider is the global one" stops being a question that has
		// a wrong answer.
		//
		// That is the whole rule, stated properly: a tenant-AGNOSTIC provider
		// may be registered once; a tenant-specific one may never be. A
		// filename is a crude way to express it, and it is the honest one —
		// the alternative is a check that tries to infer from the call site
		// whether a provider closes over a key, which it cannot.
		//
		// If a second file ever needs this, that is the moment to ask whether
		// the provider it registers is really tenant-agnostic, rather than to
		// add a second entry here.
		if rel == "internal/llm/ops.go" {
			return
		}
		// The second exemption, and it is one FILE rather than one kind of file.
		//
		// Tests are not exempt as a class — see the guard's own test for why:
		// a test that sets the global sets it for every other test in the same
		// binary, which is this rule's hazard arriving through the door nobody
		// audits. What `internal/smart/fake_llm_test.go` does is narrower: it
		// owns the install seam for that package, and every install it performs
		// captures the previous client and restores it in `t.Cleanup`, exactly
		// as `schemafluxtest.Install` does. The contamination window is one
		// test, and `t.Setenv` inside it makes `t.Parallel` a compile-time
		// impossibility, so the window cannot overlap another test.
		//
		// A second test file wanting this is the moment to ask why it is not
		// calling the seam that already exists, rather than to add an entry.
		if rel == "internal/smart/fake_llm_test.go" {
			return
		}
		// Only files that can actually reach SchemaFlux are inspected, so the
		// count reported is the number of files where this rule could have been
		// broken rather than the number of Go files in the repository.
		if !importsSchemaFlux(f) {
			return
		}
		g.checked++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			why, bad := forbiddenGlobalProvider[sel.Sel.Name]
			if !bad {
				return true
			}
			g.findings = append(g.findings, finding{
				file:   fmt.Sprintf("%s:%d", rel, fset.Position(sel.Pos()).Line),
				detail: sel.Sel.Name + " " + why,
			})
			return true
		})
	})
	return g
}

// importsSchemaFlux reports whether a file can reach the library at all.
//
// Matched on the import path rather than on a package name, because the root
// package is imported under an explicit `schemaflux` alias in this repository
// and an unaliased import elsewhere would still be the same package.
func importsSchemaFlux(f *ast.File) bool {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if p == "github.com/monstercameron/schemaflux" ||
			strings.HasPrefix(p, "github.com/monstercameron/schemaflux/") {
			return true
		}
	}
	return false
}

// --- 7. no unsanitised render path in the client -----------------------------

// forbiddenRenderSink names the ways client code can put a string into the DOM
// as MARKUP without it passing a sanitizer, and why each one matters.
var forbiddenRenderSink = map[string]string{
	"RawHTMLUnsafe": "parses markup WITHOUT sanitizing it. Use html.RawHTML, which " +
		"is the same call with sanitize.Sanitize in front",
}

// guardNoUnsanitisedRender keeps publisher markup from reaching the DOM unfiltered.
//
// # Why this is a structural decision rather than a code-review note
//
// Article HTML is stored RAW. internal/feed does not sanitize on ingest — it says
// so, and it is right to: sanitizing on write bakes today's policy into the
// database and cannot be revised without a migration over every row. The rule
// that makes that safe is stated in the same comment: "anything that IS rendered
// goes through the sanitizer."
//
// Which means the sanitizer on the READ side is not one defence among several.
// It is the only thing between a feed publisher and script running in the
// reader's page. Everything upstream — the store, the RPC layer, toPBItem — hands
// the bytes along untouched by design, and correctly so.
//
// Today the whole client goes through one door: parsedBody -> html.RawHTML ->
// sanitize.Sanitize, shared by the reading pane and the slideshow. The hazard is
// how easy it is to open a second one. `html.RawHTMLUnsafe` exists in the
// dependency, differs by one word, and its own doc comment is the only thing
// saying not to use it — which is exactly the 2am change this command's package
// comment describes.
//
// Scoped to client/ because that is where rendering happens; the server emits
// markup in one place (/pub) and sanitizes it explicitly there.
func guardNoUnsanitisedRender(root string) *guard {
	g := &guard{
		name: "no unsanitised render path in the client",
		note: "no client Go files inspected",
	}
	walkGo(root, func(path string, f *ast.File, fset *token.FileSet) {
		rel := relSlash(root, path)
		// This file names the forbidden identifier, in the map above.
		if strings.HasPrefix(rel, "internal/tools/guards/") {
			return
		}
		if !strings.HasPrefix(rel, "client/") {
			return
		}
		g.checked++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// The direct DOM sink, whatever it is called on: js.Value.Set with
			// an innerHTML-shaped property assigns markup with nothing in the
			// way. GWC's runtime asserts it has no such sink on its render path,
			// and the application must not add one beside it.
			if sel.Sel.Name == "Set" && len(call.Args) > 0 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					switch strings.Trim(lit.Value, `"`) {
					case "innerHTML", "outerHTML":
						g.findings = append(g.findings, finding{
							file: fmt.Sprintf("%s:%d", rel, fset.Position(sel.Pos()).Line),
							detail: "assigns " + strings.Trim(lit.Value, `"`) +
								" directly, which puts a string into the DOM as markup " +
								"with no sanitizer in front",
						})
					}
				}
				return true
			}
			why, bad := forbiddenRenderSink[sel.Sel.Name]
			if !bad {
				return true
			}
			g.findings = append(g.findings, finding{
				file:   fmt.Sprintf("%s:%d", rel, fset.Position(sel.Pos()).Line),
				detail: sel.Sel.Name + " " + why,
			})
			return true
		})
	})
	return g
}
