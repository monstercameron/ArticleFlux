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

// Both places the server names a catalog key.
//
// apierr was added by TODO 7.3a and is where §20.7's taxonomy now lives, which
// makes it the source of most of the keys that used to be written inline in
// grpcsrv. Scanning only grpcsrv after that move would leave this test passing
// while checking a shrinking fraction of what the server can send — the failure
// this file exists to prevent, arriving in its own blind spot.
var serverKeyDirs = []string{
	"../../internal/transport/grpcsrv",
	"../../internal/apierr",
	"../../internal/skew",
}

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

	// Skips where directories cannot be listed at all. See requireDirScan.
	requireDirScan(t)

	fset := token.NewFileSet()
	for _, dir := range serverKeyDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("cannot read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
			if perr != nil {
				t.Fatalf("cannot parse %s: %v", e.Name(), perr)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				keyArg, msgArg := keyAndMessageArgs(call)
				if keyArg == nil {
					return true
				}
				key, ok := literal(keyArg)
				if !ok {
					return true
				}
				msg := ""
				if msgArg != nil {
					msg, _ = concatLiteral(msgArg)
				}
				found[key] = [2]string{msg, fset.Position(keyArg.Pos()).String()}
				return true
			})
		}
	}
	return found
}

// keyAndMessageArgs recognises the shapes that carry a catalog key.
//
// Listed by name rather than inferred, because "any call whose second argument
// looks like srv.something" would match a comparison as readily as a
// construction, and a test that matches too much is one people disable.
func keyAndMessageArgs(call *ast.CallExpr) (key, msg ast.Expr) {
	name := ""
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		name = fn.Name
	case *ast.SelectorExpr:
		name = fn.Sel.Name
	default:
		return nil, nil
	}

	switch name {
	case "errKey":
		// errKey(code, key, msg, args)
		if len(call.Args) >= 3 {
			return call.Args[1], call.Args[2]
		}
	case "New", "Precondition", "Unimplemented":
		// apierr.New(kind, key, msg) and (key, msg)
		if len(call.Args) == 3 {
			return call.Args[1], call.Args[2]
		}
		if len(call.Args) == 2 {
			return call.Args[0], call.Args[1]
		}
	case "Invalid":
		// apierr.Invalid(field, key, msg)
		if len(call.Args) == 3 {
			return call.Args[1], call.Args[2]
		}
	case "Unavailable":
		// apierr.Unavailable(key, msg, retryAfter)
		if len(call.Args) == 3 {
			return call.Args[0], call.Args[1]
		}
	}
	return nil, nil
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
