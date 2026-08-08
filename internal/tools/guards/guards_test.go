package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run each guard against a FIXTURE TREE holding a known violation
// and a known-clean near-miss, which is the only thing that answers the question
// a guard has to answer about itself: does it still detect?
//
// A guard is unlike ordinary code in that its failure mode is silence. If
// `guardNoSQLOutsideStore` stopped matching tomorrow — a regexp edited, a walk
// that skips a directory it did not used to — every run would print `ok`, CI
// would stay green, and the only evidence would be the absence of an error that
// nobody was expecting anyway. Running the guards over THIS repository (which is
// clean, by construction) cannot tell a working detector from a broken one; only
// showing them a violation can.
//
// # Why every test here neutralises `tracked`
//
// `tracked` is built once, at package init, from `git ls-files` in the process's
// working directory — which for a test binary is this package's own directory,
// inside this repository. So it holds ArticleFlux's paths. A fixture under
// t.TempDir() is in none of them, `skipFile` therefore returns true for every
// fixture file, and a test that did not do this would find nothing, report `0
// findings`, and pass while asserting nothing whatsoever. That is the exact
// failure this file exists to prevent, so it would be a fitting way to fail.

// fixture writes a tree and points the guards at it. Restoring `tracked`
// afterwards keeps these tests from leaking into the ones that rely on the real
// value.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	saved := tracked
	tracked = nil
	t.Cleanup(func() { tracked = saved })

	root := t.TempDir()
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// findingsOn returns the fixture-relative files a guard complained about, so an
// assertion can name the file it expected rather than count anonymous hits.
func findingsOn(g *guard) []string {
	out := make([]string, 0, len(g.findings))
	for _, f := range g.findings {
		out = append(out, f.file)
	}
	return out
}

func flagged(g *guard, prefix string) bool {
	for _, f := range findingsOn(g) {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

// --- 1. no SQL outside internal/store ----------------------------------------

func TestSQLGuardCatchesEveryStatementShape(t *testing.T) {
	// One file per statement opener the regexp claims to cover. A guard that
	// catches SELECT but has quietly stopped catching DELETE is worse than one
	// that catches nothing, because it still prints `ok`.
	files := map[string]string{
		"internal/a/sel.go":     "package a\n\nvar q = `SELECT id FROM items`\n",
		"internal/a/ins.go":     "package a\n\nvar q = `INSERT INTO items (id) VALUES (?)`\n",
		"internal/a/upd.go":     "package a\n\nvar q = `UPDATE items SET title = ?`\n",
		"internal/a/del.go":     "package a\n\nvar q = `DELETE FROM items WHERE id = ?`\n",
		"internal/a/create.go":  "package a\n\nvar q = `CREATE TABLE items (id TEXT)`\n",
		"internal/a/virtual.go": "package a\n\nvar q = `CREATE VIRTUAL TABLE fts USING fts5(title)`\n",
		"internal/a/pragma.go":  "package a\n\nvar q = `PRAGMA journal_mode = WAL`\n",
	}
	g := guardNoSQLOutsideStore(fixture(t, files))
	if len(g.findings) != len(files) {
		t.Errorf("found %d of %d statement shapes: %v", len(g.findings), len(files), findingsOn(g))
	}
}

func TestSQLGuardIgnoresProseThatMerelyContainsKeywords(t *testing.T) {
	// The regexp is anchored to statement openers precisely so an error message
	// or a log line is not a finding. A guard nobody trusts is a guard nobody
	// reads, and false positives are how that happens.
	g := guardNoSQLOutsideStore(fixture(t, map[string]string{
		"internal/a/msg.go": `package a

var (
	m1 = "deleted from your library"
	m2 = "could not update settings"
	m3 = "insert your API key here"
	m4 = "create a new folder"
	m5 = "this article was removed from the feed"
)
`,
	}))
	if len(g.findings) != 0 {
		t.Errorf("prose was mistaken for SQL: %v", g.findings)
	}
	if g.checked == 0 {
		t.Error("the guard inspected nothing, so it proved nothing")
	}
}

// The known edge of the anchoring, pinned rather than left to be rediscovered.
//
// The guard's comment says prose containing "update" or "select" does not trip
// it, and for four of the five statement openers that holds — DELETE, INSERT,
// UPDATE and CREATE all require a following token that English does not supply.
// SELECT is different: `SELECT\s+.+\s+FROM` spans anything at all between the
// two words, so an English sentence that happens to use both, in that order,
// matches. "select a feed to update from the list" is a plausible enough string
// for a UI to hold.
//
// Left as it is on purpose. The alternative is narrowing `.+`, and every
// narrowing risks the direction that actually matters — a real query that stops
// being caught. A guard that occasionally objects to a sentence costs somebody
// one rephrasing; a guard that misses a second place understanding the schema
// costs what this guard exists to prevent. This test exists so the trade is a
// decision on the record rather than a surprise, and so that anyone who does
// narrow the pattern finds out here.
func TestSQLGuardAlsoMatchesEnglishBetweenSelectAndFrom(t *testing.T) {
	g := guardNoSQLOutsideStore(fixture(t, map[string]string{
		"internal/a/msg.go": "package a\n\nvar m = \"select a feed to update from the list\"\n",
	}))
	if len(g.findings) != 1 {
		t.Errorf("the SELECT...FROM span no longer behaves as documented here: %v", findingsOn(g))
	}
}

func TestSQLGuardExemptsStoreMigrationsAndItsOwnSource(t *testing.T) {
	g := guardNoSQLOutsideStore(fixture(t, map[string]string{
		"internal/store/q.go":              "package store\n\nvar q = `SELECT id FROM items`\n",
		"migrations/001.go":                "package migrations\n\nvar q = `CREATE TABLE items (id TEXT)`\n",
		"internal/tools/guards/pattern.go": "package main\n\nvar q = `SELECT id FROM items`\n",
	}))
	if len(g.findings) != 0 {
		t.Errorf("an exempt location was flagged: %v", findingsOn(g))
	}
}

// Tests may hold SQL; production code may not. That line is deliberate and
// documented on the guard, so it is worth pinning — if it ever inverts, a real
// violation starts passing.
func TestSQLGuardExemptsTestFiles(t *testing.T) {
	root := fixture(t, map[string]string{
		"internal/a/corrupt_test.go": "package a\n\nvar q = `UPDATE subscriptions SET tenant_id = 'x'`\n",
		"internal/a/prod.go":         "package a\n\nvar q = `UPDATE subscriptions SET tenant_id = 'x'`\n",
	})
	g := guardNoSQLOutsideStore(root)
	if !flagged(g, "internal/a/prod.go") {
		t.Error("production SQL outside the store was not flagged")
	}
	if flagged(g, "internal/a/corrupt_test.go") {
		t.Error("a test file was flagged; tests may hold SQL by design")
	}
}

// The finding has to identify the line, or a failure is a scavenger hunt across
// a file that might be two thousand lines long.
func TestSQLGuardReportsFileAndLine(t *testing.T) {
	g := guardNoSQLOutsideStore(fixture(t, map[string]string{
		"internal/a/q.go": "package a\n\n// padding\n// padding\nvar q = `SELECT id FROM items`\n",
	}))
	if len(g.findings) != 1 {
		t.Fatalf("expected one finding, got %v", findingsOn(g))
	}
	if got := g.findings[0].file; got != "internal/a/q.go:5" {
		t.Errorf("finding names %q, want internal/a/q.go:5", got)
	}
	// The quoted fragment is the REGEXP'S MATCH, which ends at FROM — the table
	// name is past the last group and is not part of it. Enough to recognise the
	// query in a file, which is all the finding needs to do.
	if !strings.Contains(g.findings[0].detail, "SELECT id FROM") {
		t.Errorf("the detail does not quote the offending SQL: %q", g.findings[0].detail)
	}
}

// --- 2. no syscall/js outside client/platform --------------------------------

func TestSyscallJSGuardCatchesTheImport(t *testing.T) {
	root := fixture(t, map[string]string{
		"client/view/page.go":    "package view\n\nimport \"syscall/js\"\n\nvar _ = js.Global\n",
		"client/platform/dom.go": "package platform\n\nimport \"syscall/js\"\n\nvar _ = js.Global\n",
		"internal/app/app.go":    "package app\n\nimport \"strings\"\n\nvar _ = strings.TrimSpace\n",
	})
	g := guardNoSyscallJS(root)
	if !flagged(g, "client/view/page.go") {
		t.Error("syscall/js outside client/platform was not flagged (A26)")
	}
	if flagged(g, "client/platform/dom.go") {
		t.Error("client/platform is where syscall/js belongs, and it was flagged")
	}
	if len(g.findings) != 1 {
		t.Errorf("expected exactly one finding, got %v", findingsOn(g))
	}
}

// An import is matched by path, not by the name it is bound to. A renamed or
// blank import is the same dependency and the same portability problem.
func TestSyscallJSGuardSeesRenamedAndBlankImports(t *testing.T) {
	g := guardNoSyscallJS(fixture(t, map[string]string{
		"client/view/a.go": "package view\n\nimport alias \"syscall/js\"\n\nvar _ = alias.Global\n",
		"client/view/b.go": "package view\n\nimport _ \"syscall/js\"\n",
	}))
	if len(g.findings) != 2 {
		t.Errorf("expected both spellings flagged, got %v", findingsOn(g))
	}
}

func TestSyscallJSGuardIgnoresPackagesWhoseNameMerelyStartsWithJS(t *testing.T) {
	g := guardNoSyscallJS(fixture(t, map[string]string{
		"client/view/a.go": "package view\n\nimport \"encoding/json\"\n\nvar _ = json.Marshal\n",
		"client/view/b.go": "package view\n\nimport \"github.com/x/jsonsel\"\n\nvar _ = jsonsel.Q\n",
	}))
	if len(g.findings) != 0 {
		t.Errorf("a package that is not syscall/js was flagged: %v", findingsOn(g))
	}
}

// --- 3. no .css files ---------------------------------------------------------

func TestCSSGuardCatchesStylesheetsAnywhere(t *testing.T) {
	g := guardNoCSSFiles(fixture(t, map[string]string{
		"internal/ui/app.css":    "body{margin:0}",
		"client/view/deep/x.CSS": "body{margin:0}",
		"internal/ui/css.go":     "package ui\n",
	}))
	if len(g.findings) != 2 {
		t.Errorf("expected both stylesheets flagged (the match is case-insensitive), got %v", findingsOn(g))
	}
	if flagged(g, "internal/ui/css.go") {
		t.Error("a Go file whose name contains css was flagged")
	}
}

// design/ and web/ are excluded on purpose — mockups are specification and web/
// is build output. Guarding either means guarding something nobody ports.
func TestCSSGuardSkipsMockupsAndBuildOutput(t *testing.T) {
	g := guardNoCSSFiles(fixture(t, map[string]string{
		"design/mock.css":          "body{margin:0}",
		"web/app.css":              "body{margin:0}",
		"node_modules/pkg/x.css":   "body{margin:0}",
		"test-results/trace/y.css": "body{margin:0}",
		"bin/web/z.css":            "body{margin:0}",
	}))
	if len(g.findings) != 0 {
		t.Errorf("an excluded directory was scanned: %v", findingsOn(g))
	}
}

// --- 4. repository methods take Scope ----------------------------------------

func TestRepoScopeGuardCatchesAnUnscopedMethod(t *testing.T) {
	root := fixture(t, map[string]string{
		"internal/store/repo.go": `package store

type ItemRepo struct{}

type Scope struct{}

// Scoped, and therefore fine.
func (r *ItemRepo) ListItems(s Scope, n int) error { return nil }

// Not scoped, and not exempt: this is the defect the guard exists for.
func (r *ItemRepo) ListEverything(n int) error { return nil }

// Unexported methods are not the repository's surface.
func (r *ItemRepo) helper(n int) error { return nil }
`,
	})
	g := guardRepoScope(root)
	if !flagged(g, "internal/store/repo.go") {
		t.Fatalf("an unscoped exported *Repo method was not flagged: %v", findingsOn(g))
	}
	if len(g.findings) != 1 {
		t.Errorf("expected exactly one finding; a scoped or unexported method was flagged too: %v", findingsOn(g))
	}
	if !strings.Contains(g.findings[0].detail, "ListEverything") {
		t.Errorf("the finding names the wrong method: %q", g.findings[0].detail)
	}
	// The message must say what to do about it, because the right answer is
	// sometimes "add it to unscopedByDesign with a reason" rather than "add a
	// parameter", and a guard that does not say so invites the wrong fix.
	if !strings.Contains(g.findings[0].detail, "unscopedByDesign") {
		t.Errorf("the finding does not mention the exemption list: %q", g.findings[0].detail)
	}
}

func TestRepoScopeGuardHonoursTheExemptionList(t *testing.T) {
	// IngestItems writes global items (A14) and legitimately takes no Scope.
	g := guardRepoScope(fixture(t, map[string]string{
		"internal/store/repo.go": `package store

type ItemRepo struct{}

func (r *ItemRepo) IngestItems(n int) error { return nil }
`,
	}))
	if len(g.findings) != 0 {
		t.Errorf("a method exempt by design was flagged: %v", findingsOn(g))
	}
}

func TestRepoScopeGuardOnlyLooksAtRepoTypes(t *testing.T) {
	g := guardRepoScope(fixture(t, map[string]string{
		"internal/store/other.go": `package store

type Cache struct{}

func (c *Cache) Get(k string) error { return nil }
`,
	}))
	if len(g.findings) != 0 {
		t.Errorf("a non-repository type was flagged: %v", findingsOn(g))
	}
	if g.checked != 0 {
		t.Errorf("checked %d methods on a tree with no *Repo type", g.checked)
	}
}

// A Scope from another package is still a Scope. Repositories are called with
// store.Scope from outside, so matching only the bare identifier would flag
// every correctly-written method in the codebase.
func TestRepoScopeGuardAcceptsQualifiedAndPointerScopes(t *testing.T) {
	g := guardRepoScope(fixture(t, map[string]string{
		"internal/x/repo.go": `package x

type FeedRepo struct{}

func (r *FeedRepo) A(s store.Scope) error  { return nil }
func (r *FeedRepo) B(s *store.Scope) error { return nil }
func (r *FeedRepo) C(ctx int, s Scope) error { return nil }
`,
	}))
	if len(g.findings) != 0 {
		t.Errorf("a correctly scoped method was flagged: %v", findingsOn(g))
	}
}

// --- 5. no hardcoded UI copy in client/view ----------------------------------

func TestInlineCopyGuardCatchesCopyInItsUsualHidingPlaces(t *testing.T) {
	g := guardNoInlineCopy(fixture(t, map[string]string{
		"client/view/page.go": `package view

func build() {
	html.Text("Save your changes")
	_ = html.Props{Title: "Open the reader"}
	_ = map[string]string{"aria-label": "Close this dialog"}
}
`,
	}))
	if len(g.findings) < 3 {
		t.Errorf("inline copy went unflagged; found only %v", findingsOn(g))
	}
	for _, f := range g.findings {
		if !strings.Contains(f.detail, "i18n.T") {
			t.Errorf("a finding does not say what to do instead: %q", f.detail)
		}
	}
}

func TestInlineCopyGuardOnlyPolicesClientView(t *testing.T) {
	// The catalog rule is about the view layer. Copy in a server-side template
	// or a test is not what §22.16 is about, and flagging it would make the
	// guard something people route around.
	g := guardNoInlineCopy(fixture(t, map[string]string{
		"internal/app/page.go":     "package app\n\nfunc f() { html.Text(\"Save your changes\") }\n",
		"client/view/page_test.go": "package view\n\nfunc f() { html.Text(\"Save your changes\") }\n",
	}))
	if len(g.findings) != 0 {
		t.Errorf("copy outside client/view was flagged: %v", findingsOn(g))
	}
}

// --- the shared machinery ----------------------------------------------------

func TestWalkGoSkipsGeneratedProtobufAndVendoredTrees(t *testing.T) {
	root := fixture(t, map[string]string{
		"internal/pb/x.pb.go":   "package pb\n\nvar q = `SELECT id FROM items`\n",
		"vendor/dep/x.go":       "package dep\n\nvar q = `SELECT id FROM items`\n",
		"node_modules/dep/x.go": "package dep\n\nvar q = `SELECT id FROM items`\n",
		"bin/x.go":              "package main\n\nvar q = `SELECT id FROM items`\n",
		".git/hooks/x.go":       "package main\n\nvar q = `SELECT id FROM items`\n",
		"internal/real/x.go":    "package real\n\nvar q = `SELECT id FROM items`\n",
	})
	g := guardNoSQLOutsideStore(root)
	if len(g.findings) != 1 || !flagged(g, "internal/real/x.go") {
		t.Errorf("generated or vendored code was scanned, or real code was not: %v", findingsOn(g))
	}
}

func TestWalkGoSurvivesAFileItCannotParse(t *testing.T) {
	// A syntax error is somebody mid-edit. The guard must skip that file and go
	// on, not abandon the walk and report `ok` for the rest of the tree.
	root := fixture(t, map[string]string{
		"internal/a/broken.go": "package a\n\nfunc ( { this is not Go\n",
		"internal/b/real.go":   "package b\n\nvar q = `SELECT id FROM items`\n",
	})
	g := guardNoSQLOutsideStore(root)
	if !flagged(g, "internal/b/real.go") {
		t.Errorf("an unparseable file stopped the walk: %v", findingsOn(g))
	}
}

func TestGuardsOnAnEmptyTreeReportNotApplicableRatherThanPass(t *testing.T) {
	// The distinction main() prints, and the reason it prints it: a guard that
	// inspected nothing has not passed, and saying `ok` would be the beginning
	// of a guard that quietly stopped guarding.
	root := fixture(t, nil)
	for _, g := range []*guard{
		guardNoSQLOutsideStore(root),
		guardNoSyscallJS(root),
		guardNoCSSFiles(root),
		guardRepoScope(root),
		guardNoInlineCopy(root),
	} {
		if g.checked != 0 {
			t.Errorf("%s claims to have checked %d things in an empty tree", g.name, g.checked)
		}
		if len(g.findings) != 0 {
			t.Errorf("%s found something in an empty tree: %v", g.name, findingsOn(g))
		}
		if g.note == "" {
			t.Errorf("%s has no note to print when it is n/a", g.name)
		}
	}
}

func TestSkipDirCoversTheDirectoriesTheCommentClaims(t *testing.T) {
	for _, name := range []string{
		".git", "bin", "node_modules", "vendor", "design", ".github",
		"test-results", "playwright-report", "shots", ".tmp", "web",
	} {
		if !skipDir(name) {
			t.Errorf("%s is documented as skipped but is not", name)
		}
	}
	for _, name := range []string{"internal", "client", "cmd", "migrations", "deploy"} {
		if skipDir(name) {
			t.Errorf("%s holds source and must be scanned", name)
		}
	}
}

func TestTypeNameUnwrapsPointersAndQualifiers(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"Scope", "Scope"},
		{"*Scope", "Scope"},
		{"store.Scope", "Scope"},
		{"*store.Scope", "Scope"},
		{"[]Scope", ""}, // not a shape the guard claims to understand
	} {
		e, err := parser.ParseExpr(tc.src)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		if got := typeName(e); got != tc.want {
			t.Errorf("typeName(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestTakesScopeReadsTheParameterList(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want bool
	}{
		{"func (r *ItemRepo) M(s Scope) error { return nil }", true},
		{"func (r *ItemRepo) M(ctx int, s store.Scope) error { return nil }", true},
		{"func (r *ItemRepo) M(n int) error { return nil }", false},
		{"func (r *ItemRepo) M() error { return nil }", false},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", "package p\n\n"+tc.src, 0)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.src, err)
		}
		fn := f.Decls[0].(*ast.FuncDecl)
		if got := takesScope(fn); got != tc.want {
			t.Errorf("takesScope(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}
}

func TestCondenseCollapsesWhitespaceAndTruncates(t *testing.T) {
	if got := condense("SELECT   id\n\tFROM items"); got != "SELECT id FROM items" {
		t.Errorf("condense collapsed to %q", got)
	}
	long := condense(strings.Repeat("x", 200))
	if len(long) != 60 || !strings.HasSuffix(long, "...") {
		t.Errorf("condense produced %d chars (%q); a finding must stay one line", len(long), long)
	}
	// Exactly at the limit must not be truncated — an off-by-one here turns a
	// complete query into an elided one for no reason.
	if got := condense(strings.Repeat("x", 60)); got != strings.Repeat("x", 60) {
		t.Errorf("a 60-char string was truncated: %q", got)
	}
}

func TestRelSlashIsAlwaysForwardSlashed(t *testing.T) {
	// Findings are compared against documented paths like "internal/store/",
	// so a backslash on Windows would make every prefix test in every guard
	// silently stop matching.
	root := filepath.Join("C:", "repo")
	got := relSlash(root, filepath.Join(root, "internal", "store", "q.go"))
	if got != "internal/store/q.go" {
		t.Errorf("relSlash = %q, want internal/store/q.go", got)
	}
	if strings.Contains(got, `\`) {
		t.Errorf("relSlash leaked a backslash: %q", got)
	}
}

// skipFile is what makes an untracked scratch file somebody's own business. Its
// nil case matters more: no git, no repository, git not on PATH — scan
// everything, because a guard that silently checks nothing because a subprocess
// failed is worse than one that complains about a scratch file.
func TestSkipFileScansEverythingWhenGitIsUnavailable(t *testing.T) {
	saved := tracked
	tracked = nil
	t.Cleanup(func() { tracked = saved })

	if skipFile("/repo", "/repo/anything.go") {
		t.Error("with no git information the guards must scan everything")
	}
}

func TestSkipFileHonoursTheTrackedSet(t *testing.T) {
	saved := tracked
	tracked = map[string]bool{"internal/a/kept.go": true}
	t.Cleanup(func() { tracked = saved })

	root := filepath.Join("C:", "repo")
	if skipFile(root, filepath.Join(root, "internal", "a", "kept.go")) {
		t.Error("a tracked file was skipped")
	}
	if !skipFile(root, filepath.Join(root, "scratch", "probe.go")) {
		t.Error("an untracked scratch file was scanned; that is what turned CI red for a throwaway probe")
	}
}

// --- the i18n guard's internals ----------------------------------------------
//
// looksLikeCopy is the judgement the whole guard rests on, and it is the part
// that decides between a false positive (a guard someone disables) and a miss (a
// string that ships untranslated). Every rule its comment claims is pinned here.

func TestInlineCopyGuardReadsTheViewsOwnLabelHelpers(t *testing.T) {
	// The int in labelTakers is how many leading arguments to skip before the
	// ones that are text. An off-by-one there stops the guard seeing the label
	// and starts it objecting to the action id, so both directions matter.
	g := guardNoInlineCopy(fixture(t, map[string]string{
		"client/view/x.go": `package view

func build() {
	actionButton("open-reader", "btn-primary", "Open the reader")
	staticChip("Saved for later")
	fsFact("Last polled", "Two minutes ago")
}
`,
	}))
	if len(g.findings) < 4 {
		t.Errorf("labels passed to the view's own helpers were not flagged: %v", findingsOn(g))
	}
	for _, f := range g.findings {
		// The skipped leading arguments are ids and classes, never copy.
		if strings.Contains(f.detail, "open-reader") || strings.Contains(f.detail, "btn-primary") {
			t.Errorf("an action id or class was mistaken for copy: %q", f.detail)
		}
	}
}

// emptyState is listed with -1: it takes catalog KEYS, never text. A guard that
// treated those as copy would demand the keys themselves be translated.
func TestInlineCopyGuardLeavesKeyTakingHelpersAlone(t *testing.T) {
	g := guardNoInlineCopy(fixture(t, map[string]string{
		"client/view/x.go": "package view\n\nfunc build() { emptyState(tr, \"empty.title\", \"empty.body\") }\n",
	}))
	if len(g.findings) != 0 {
		t.Errorf("a helper that takes catalog keys was flagged: %v", findingsOn(g))
	}
}

// Concatenation is the shape inline copy takes once it has a value in it, and
// it is exactly what the catalog's {placeholder} syntax exists to replace.
func TestProseUnwrapsConcatenation(t *testing.T) {
	for _, src := range []string{
		`"Showing " + n + " articles"`,
		`n + " articles remaining"`,
	} {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		if _, ok := prose(e); !ok {
			t.Errorf("copy hidden in a concatenation was missed: %s", src)
		}
	}
}

func TestProseIgnoresNonStringExpressions(t *testing.T) {
	for _, src := range []string{`42`, `someVar`, `a - b`, `'x'`, `f()`} {
		e, err := parser.ParseExpr(src)
		if err != nil {
			t.Fatalf("parse %q: %v", src, err)
		}
		if got, ok := prose(e); ok {
			t.Errorf("prose(%s) returned %q; only string literals are copy", src, got)
		}
	}
}

func TestLooksLikeCopyAcceptsRealCopy(t *testing.T) {
	for _, s := range []string{
		"Sign in", "Loading…", "Save your changes", "Open the reader",
		"Stored under /var/lib", "Sign in — it's free", "-5 items left",
	} {
		if !looksLikeCopy(s) {
			t.Errorf("%q is copy and would ship untranslated", s)
		}
	}
}

func TestLooksLikeCopyRejectsTheLiteralsThatLegitimatelyLiveInTheView(t *testing.T) {
	for _, s := range []string{
		"feed-row", "modal-keep", "sk-w-90", "true", "Enter", "▲", "▲ ▼", "1 2 3",
		"", "  ", "px", "aria-label", "data-action", "#fff", "1.5rem",
	} {
		if looksLikeCopy(s) {
			t.Errorf("%q would be flagged; it is an id, a class, or a glyph", s)
		}
	}
}

// The front door prints a terminal transcript, and a translated command does not
// run. The flag word is the one signal UI copy essentially never carries.
func TestLooksLikeCopyRejectsCommandLines(t *testing.T) {
	for _, s := range []string{
		"articleflux init -db ./articleflux.db -user cam",
		"make dev --verbose",
		"go test ./...  -run TestX",
	} {
		if looksLikeCopy(s) {
			t.Errorf("%q is a command line; translating it produces a command that does not run", s)
		}
	}
}

func TestHasFlagWordIgnoresTheNearMisses(t *testing.T) {
	// Each of these is documented on hasFlagWord as a reason it reads a word at
	// a time rather than searching for " -".
	for _, s := range []string{
		"Sign in — it's free", // em dash, not a hyphen
		"a well-known feed",   // does not begin a word
		"-5 items",            // a digit follows
		"- a bullet",          // nothing follows
	} {
		if hasFlagWord(s) {
			t.Errorf("%q was read as a command line", s)
		}
	}
	for _, s := range []string{"run -v", "run --verbose", "x -db path"} {
		if !hasFlagWord(s) {
			t.Errorf("%q is a command line and was not recognised", s)
		}
	}
}

// The accepted gap, on the record. A single capitalised word slips through, and
// tightening the net would catch every action id in the file.
func TestLooksLikeCopyMissesSingleWordLabelsByDesign(t *testing.T) {
	if looksLikeCopy("Feeds") {
		t.Error("the single-word gap closed; if that was deliberate, this test should go, " +
			"and client/i18n's catalog tests are the other half of the story")
	}
}

func TestRelSlashFallsBackToThePathWhenItCannotRelativise(t *testing.T) {
	// filepath.Rel fails between a relative and an absolute path. The fallback
	// keeps a finding readable instead of empty.
	got := relSlash("relative/root", filepath.Join("C:", "elsewhere", "x.go"))
	if got == "" {
		t.Error("relSlash returned nothing; a finding would name no file at all")
	}
}

// --- 6. no global SchemaFlux provider -----------------------------------------

// This guard protects the one thing in the migration that can leak a
// credential BETWEEN TENANTS, so its own failure mode matters more than most:
// a rule that silently stopped matching would leave the hazard in place and the
// lint output still saying `ok`.

func TestGlobalProviderGuardCatchesEveryRegistrationCall(t *testing.T) {
	// Every name in forbiddenGlobalProvider, each in its own file, so a rule
	// that matched only the first is visible as seven passes and one failure
	// rather than as one pass.
	files := map[string]string{}
	for name := range forbiddenGlobalProvider {
		files["internal/app/"+strings.ToLower(name)+".go"] = `package app

import schemaflux "github.com/monstercameron/schemaflux"

func wire(c *schemaflux.Client) { c.` + name + `() }
`
	}
	root := fixture(t, files)
	g := guardNoGlobalLLMProvider(root)

	for name := range forbiddenGlobalProvider {
		want := "internal/app/" + strings.ToLower(name) + ".go"
		if !flagged(g, want) {
			t.Errorf("%s was not caught", name)
		}
	}
}

func TestGlobalProviderGuardIgnoresFilesThatCannotReachTheLibrary(t *testing.T) {
	// The rule is greppable only because the call has to come from a file that
	// imports SchemaFlux. A method called `Init` on somebody else's type is an
	// ordinary name and must not be flagged — otherwise the guard becomes noise
	// and the next person exempts it.
	root := fixture(t, map[string]string{
		"internal/other/thing.go": `package other

type engine struct{}

func (e engine) Init() {}

func start() { engine{}.Init() }
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if len(g.findings) != 0 {
		t.Errorf("flagged a file that cannot reach SchemaFlux: %v", findingsOn(g))
	}
}

func TestGlobalProviderGuardAllowsThePerCallShape(t *testing.T) {
	// The shape internal/llm actually uses, and the whole point of the rule:
	// build a provider per call, chain middleware around it, register nothing.
	// If this were flagged, the guard would be forbidding the fix.
	root := fixture(t, map[string]string{
		"internal/llm/llm.go": `package llm

import (
	schemaflux "github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/mw"
)

func do(base schemaflux.Provider, chain []mw.Middleware) {
	_ = mw.Chain(base, chain...)
}
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if len(g.findings) != 0 {
		t.Errorf("the per-call shape was flagged: %v", findingsOn(g))
	}
	if g.checked == 0 {
		t.Error("the file was not inspected at all, so the pass is vacuous")
	}
}

func TestGlobalProviderGuardExemptsTheSmartTestSeamByName(t *testing.T) {
	// The one test file allowed to register, and the reason it is named rather
	// than matched by suffix: it captures the previous client and restores it in
	// t.Cleanup, so the window in which a global is set is one test long. Every
	// other test file goes through it — see the test above, which proves an
	// arbitrary _test.go is still flagged.
	root := fixture(t, map[string]string{
		"internal/smart/fake_llm_test.go": `package smart

import "github.com/monstercameron/schemaflux"

func install(p schemaflux.Provider) {
	schemaflux.SetDefaultClient(schemaflux.NewClient("k").WithProviderInstance(p))
}
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if len(g.findings) != 0 {
		t.Errorf("the audited test seam was flagged: %v", findingsOn(g))
	}
}

// The exemption is a whole path, not a basename: a `fake_llm_test.go` in some
// other package has none of the restore discipline that earned the one in
// internal/smart its exemption.
func TestGlobalProviderGuardExemptionIsPathSpecific(t *testing.T) {
	root := fixture(t, map[string]string{
		"internal/app/fake_llm_test.go": `package app

import "github.com/monstercameron/schemaflux"

func install(p schemaflux.Provider) {
	schemaflux.SetDefaultClient(schemaflux.NewClient("k").WithProviderInstance(p))
}
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if !flagged(g, "internal/app/fake_llm_test.go") {
		t.Errorf("the same basename in another package was exempted: %v", findingsOn(g))
	}
}

func TestGlobalProviderGuardSeesASubpackageImport(t *testing.T) {
	// `mw`, `pricing` and `telemetry` are separate import paths under the same
	// module. A file importing only one of those can still name the client, so
	// matching on the root path alone would miss it.
	root := fixture(t, map[string]string{
		"internal/app/wire.go": `package app

import "github.com/monstercameron/schemaflux/mw"

type client struct{}

func (c client) WithProviderInstance() {}

func wire(_ mw.Middleware, c client) { c.WithProviderInstance() }
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if !flagged(g, "internal/app/wire.go") {
		t.Errorf("a subpackage import did not bring the file into scope: %v", findingsOn(g))
	}
}

func TestGlobalProviderGuardSeesARenamedImport(t *testing.T) {
	// This repository imports the root under an explicit alias. Matching on the
	// package NAME rather than the path would miss every one of its files.
	root := fixture(t, map[string]string{
		"internal/app/aliased.go": `package app

import sf "github.com/monstercameron/schemaflux"

func wire() { sf.Init("k") }
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if !flagged(g, "internal/app/aliased.go") {
		t.Errorf("a renamed import was missed: %v", findingsOn(g))
	}
}

func TestGlobalProviderGuardDoesNotExemptTests(t *testing.T) {
	// Deliberate, and the opposite of the SQL guard's rule. A test that sets the
	// global sets it for every other test in the same binary — which is the
	// exact cross-contamination this guard exists to prevent, arriving through
	// the one door nobody audits.
	root := fixture(t, map[string]string{
		"internal/app/wire_test.go": `package app

import schemaflux "github.com/monstercameron/schemaflux"

func wire() { schemaflux.Init("k") }
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if !flagged(g, "internal/app/wire_test.go") {
		t.Errorf("a test was exempted: %v", findingsOn(g))
	}
}

func TestGlobalProviderGuardExemptsItsOwnSource(t *testing.T) {
	// main.go names every forbidden identifier in a map literal. A guard that
	// flagged itself would be permanently red.
	root := fixture(t, map[string]string{
		"internal/tools/guards/main.go": `package main

import schemaflux "github.com/monstercameron/schemaflux"

func wire() { schemaflux.Init("k") }
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if len(g.findings) != 0 {
		t.Errorf("the guard flagged its own source: %v", findingsOn(g))
	}
}

func TestGlobalProviderGuardReportsFileAndLine(t *testing.T) {
	// A finding somebody cannot navigate to is a finding they will route around.
	root := fixture(t, map[string]string{
		"internal/app/wire.go": `package app

import schemaflux "github.com/monstercameron/schemaflux"

func wire() {
	schemaflux.Init("k")
}
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if len(g.findings) != 1 {
		t.Fatalf("findings = %v, want exactly one", findingsOn(g))
	}
	if got := g.findings[0].file; got != "internal/app/wire.go:6" {
		t.Errorf("file = %q, want internal/app/wire.go:6", got)
	}
	if !strings.Contains(g.findings[0].detail, "package-level") {
		t.Errorf("detail = %q, want it to say why", g.findings[0].detail)
	}
}

func TestGlobalProviderGuardCountsWhatItInspected(t *testing.T) {
	// "0 findings" and "0 inspected" are different answers and the runner prints
	// them differently. A guard reporting a vacuous pass is the failure mode
	// this whole file exists to catch.
	root := fixture(t, map[string]string{
		"internal/a/a.go": `package a

import schemaflux "github.com/monstercameron/schemaflux"

var _ schemaflux.Provider
`,
		"internal/b/b.go": `package b

func nothing() {}
`,
	})
	g := guardNoGlobalLLMProvider(root)
	if g.checked != 1 {
		t.Errorf("checked = %d, want 1 — only the file that can reach the library", g.checked)
	}
}
