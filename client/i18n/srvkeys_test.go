package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The server↔catalog contract, checked from the catalog side.
//
// internal/transport/grpcsrv attaches an ErrorDetail carrying a key, and
// client/view resolves it here. Nothing in the Go type system connects those
// two: the key crosses the wire as a string. So a refusal added on the server
// with a key nobody registered would compile, ship, and show the reader
// "srv.somethingNew" — which is the exact failure the whole catalog exists to
// prevent, arriving through the one door the other tests do not watch.
//
// This walks the server source rather than importing it, for the same reason
// keycoverage does with client/view: source is the thing under test, and a
// parse needs no build tags.

const grpcsrvDir = "../../internal/transport/grpcsrv"

func TestEveryServerErrorKeyExists(t *testing.T) {
	registered := registeredKeys()
	keys := serverErrorKeys(t)

	if len(keys) == 0 {
		t.Fatal("no errKey calls found in internal/transport/grpcsrv — either the " +
			"helper was renamed, or this test has stopped checking anything")
	}
	for key, where := range keys {
		if !strings.HasPrefix(key, "srv.") {
			t.Errorf("%s uses the key %q — server error keys live in the srv "+
				"namespace so one file holds every message the server can send a person",
				where, key)
			continue
		}
		if !registered[key] {
			t.Errorf("%s sends the error key %q, which client/i18n/en_srv.go does "+
				"not register — the reader would see the identifier", where, key)
		}
	}
}

// TestServerErrorKeysMatchTheirEnglishFallback keeps the two copies honest.
//
// errKey sends BOTH a key and an English message: the key for the wasm client,
// the message for the consumers with no catalog (the Google Reader sync API,
// curl). If they drift, two readers of the same instance get two different
// explanations of the same refusal — and the drift is invisible, because each
// half is correct on its own.
func TestServerErrorKeysMatchTheirEnglishFallback(t *testing.T) {
	english := map[string]string{}
	for _, e := range Export(DefaultLocale) {
		english[e.Key] = e.Text
	}
	for key, fallback := range serverErrorFallbacks(t) {
		want, ok := english[key]
		if !ok || fallback == "" {
			continue // covered by the test above
		}
		if want != fallback {
			t.Errorf("%s: the English fallback on the wire and the catalog disagree\n"+
				"  wire:    %q\n  catalog: %q", key, fallback, want)
		}
	}
}

func serverErrorKeys(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for key, v := range walkErrKey(t) {
		out[key] = v[1]
	}
	return out
}

func serverErrorFallbacks(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for key, v := range walkErrKey(t) {
		if v[0] != "" {
			out[key] = v[0]
		}
	}
	return out
}

// walkErrKey yields (key, englishFallback, position) for every errKey call.
// The fallback is empty when it is not a plain literal — a Sprintf, say — which
// the caller treats as "nothing to compare".
func walkErrKey(t *testing.T) map[string][2]string {
	t.Helper()
	// keyed by key, value {message, position}
	found := map[string][2]string{}

	entries, err := os.ReadDir(grpcsrvDir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", grpcsrvDir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(grpcsrvDir, e.Name()), nil, 0)
		if perr != nil {
			t.Fatalf("cannot parse %s: %v", e.Name(), perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "errKey" || len(call.Args) < 3 {
				return true
			}
			key, ok := literal(call.Args[1])
			if !ok {
				return true
			}
			msg, _ := concatLiteral(call.Args[2])
			found[key] = [2]string{msg, fset.Position(call.Args[1].Pos()).String()}
			return true
		})
	}
	return found
}

// concatLiteral flattens `"a" + "b"`, which is how a long fallback is wrapped.
func concatLiteral(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		return literal(v)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, okL := concatLiteral(v.X)
		r, okR := concatLiteral(v.Y)
		if !okL || !okR {
			return "", false
		}
		return l + r, true
	}
	return "", false
}
