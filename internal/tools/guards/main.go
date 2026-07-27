// Command guards enforces the five structural decisions that a plausible-looking
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
	"RecordFetch": "updates global source health (A14)",
	"DueSources":  "the scheduler polls for every tenant at once (A14)",
	// A scrape rule belongs to the SOURCE, which is global: it is the site's
	// selectors, not anybody's preference, and the poller that reads it has no
	// user. The WRITE path (PutScrapeRule) does take a Scope and checks the
	// subscription, which is where the isolation actually belongs.
	"ScrapeRuleFor":       "a global source's extraction rule; the poller has no tenant (A14)",
	"RecordScrapeOutcome": "rule health on a global source, written by the poller (A14)",
	"KnownGUIDs":          "reads global item guids for one global source (A14)",
	"RecordOutlinks":      "outlinks are a property of a global item (A14); one extraction serves every subscriber",
	// Identity (5.1). Each of these either PRODUCES a Scope or runs before one
	// can exist — the same category as ScopeForSession, and the reason that one
	// is exempt too.
	"SeedSystemRoles":      "seeds the four built-in roles at boot, before any tenant exists",
	"RedeemInvite":         "the code IS the authorisation, and it names the tenant the account joins",
	"ScopeForAPIToken":     "produces a Scope from a token; requiring one would be circular",
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
	"CreateTenantAndUser": "creates the tenant and its first user; no scope exists yet",
	"ScopeForSession":     "produces a Scope from a session token",
	"FirstUserScope":      "produces the dev Scope; gated on DevMode + loopback",
	// The login path. Each of these runs before a Scope can exist, or is the
	// thing that ends one. Note what is NOT here: Identity takes a Scope, because
	// "who am I" is a question about an authenticated caller and answering it
	// unscoped would let any session read any user's row.
	"UserForLogin":         "produces the identity a Scope is built from; there is none yet",
	"CreateSession":        "creates the credential a Scope is resolved from",
	"RevokeSession":        "the token hash IS the authorisation to revoke it",
	"PurgeExpiredSessions": "maintenance over every tenant's dead rows",
	"PurgeIdempotency":     "maintenance over every tenant's expired keys, like PurgeExpiredSessions",
	"Audit":                "the actor may be a tombstoned user and the tenant may be one being deleted (§7.9); the whole value of an audit log is surviving what it describes. AuditTrail, which READS it, does take a Scope.",
	"CountUsers":           "asked at boot, before any Scope can exist",
	"RevokeAllSessions":    "the CLI break-glass reset has no session of its own",
	"SetPasswordHash":      "the CLI break-glass reset has no session",
	"AddUser":              "the operator creating an account has no session",
	// System settings (§6.3, scope='system'). Instance configuration, not user
	// data: the OpenAI API key, the Smart+ model, the translated UI catalogs.
	// Global rows in the same sense sources and items are global under A14.
	// Named distinctly rather than Get/Set so exempting them here cannot
	// accidentally exempt a Get on some future tenant-scoped repository.
	// Authorisation is at the transport, where the caller is checked for being
	// an owner.
	"SystemValue":       "reads a scope='system' row; instance config, no tenant owns it",
	"SetSystemValue":    "writes a scope='system' row; instance config, no tenant owns it",
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
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}
		fn(path, f, fset)
		return nil
	})
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
