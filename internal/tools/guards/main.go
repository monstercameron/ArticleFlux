// Command guards enforces the four structural decisions that a plausible-looking
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
	"IngestItems":   "writes global items (A14); no tenant owns them",
	"RecordFetch":   "updates global source health (A14)",
	"DueSources":    "the scheduler polls for every tenant at once (A14)",
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
	// Favicons are a property of the public web, cached once for everyone (A14).
	"GetFavicon":  "global icon cache; no tenant owns a favicon",
	"PutFavicon":  "global icon cache",
	"SourceHosts": "global sources, for the icon warmer",
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
