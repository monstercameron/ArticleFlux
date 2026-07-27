package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The two directions a catalog can rot, and a test for each.
//
//   - A call site names a key nobody registered → the reader sees "list.foo"
//     where a word should be. TestEveryReferencedKeyExists.
//   - A key is registered and nothing uses it → dead weight a translator pays
//     to translate. TestNoOrphanedKeys.
//
// Both parse client/view as SOURCE rather than importing it, because that
// package is `js && wasm` and cannot be linked into a native test binary. That
// is a limitation worth naming: these see literal keys only. A key assembled at
// runtime — tr.T("theme", t.Name) — is invisible to both, which is exactly why
// themeLabel and friends fall back to the design package's own Label instead of
// trusting the catalog.
//
// Two call shapes are recognised, both of them the framework's:
//
//	tr.T("namespace", "key")            the Runtime form
//	ns := tr.NS("namespace"); ns.T("key")   the bound-Namespace form

// viewDir is where the UI lives, relative to this package.
const viewDir = "../view"

// dynamicPrefixes are the key prefixes built at runtime from a stable id rather
// than written as literals. Everything under them is exempt from the orphan
// check, and each entry names what supplies the suffix so a future reader can
// verify it rather than trust it.
var dynamicPrefixes = map[string]string{
	"theme.":       "design.Theme.Name, via view.themeLabel/themeBlurb",
	"accent.":      "design.Swatch.Name, via view.accentLabel",
	"readingSize.": "design.ReadingSize.Name",
	"glyph.":       "tagglyph.Glyph.Char, via view.glyphName",
	"glyphGroup.":  "the tagglyph group constants, via view.glyphGroupName",
	"month.":       "time.Month, via view.relTime",
	// The server names these in an ErrorDetail; the client resolves them in
	// view.serverText from a key that arrives over the wire, so no literal in
	// client/view mentions any of them. internal/transport/grpcsrv is the other
	// half of this contract.
	"srv.":                "gRPC ErrorDetail keys, via view.serverText",
	"settings.tab.":       "the settingsTab constants",
	"palette.cmd.":        "the palette command ids",
	"feedSettings.poll.":  "the pollChoices values",
	"feedSettings.cache.": "the cacheChoices values",
	"feedSettings.mute.":  "the muteChoices values",
	"list.empty":          "emptyState(titleKey, hintKey) call sites",
	// The slideshow's reason for not speaking, built as "voice."+p.voice from
	// the slideVoice* constants in client/view.
	"slides.voice.": "the slideVoice* constants, via view.slideBody",
	// The prerequisites dialog names each requirement and says why, both keyed
	// off slidePrereq.Key — the prereq* constants in client/view.
	"slides.need.": "the prereq* constants, via view.slideNeedRow",
	"slides.why.":  "the prereq* constants, via view.slideNeedRow",
	// The narrator's manner, built from slideVibeChoices in client/view.
	"settings.vibe.": "the vibe* constants, via view.vibePicker",
	// My Feed's settings tab (§18.2). Three runtime-keyed families, and each
	// suffix arrives from somewhere the catalog cannot see:
	//
	//   factor.  internal/rank's own reason terms, off the wire in
	//            InterestFactor.term — via view.factorLabel
	//   trend.   the topics table's trend column — via view.topicEvidence
	//   level.   the steerLevels table in client/view/myfeedsettings.go, which
	//            is positional against pb.SteerLevel
	"myFeed.factor.": "internal/rank reason terms, via view.factorLabel",
	"myFeed.trend.":  "the topics.trend values, via view.topicEvidence",
	"myFeed.level.":  "the steerLevels table, via view.mfDial",
}

func TestEveryReferencedKeyExists(t *testing.T) {
	registered := registeredKeys()
	for key, where := range referencedKeys(t) {
		if _, ok := registered[key]; !ok {
			t.Errorf("%s references i18n key %q, which no en_*.go registers", where, key)
		}
	}
}

func TestNoOrphanedKeys(t *testing.T) {
	referenced := referencedKeys(t)
	var orphans []string
	for key := range registeredKeys() {
		if _, ok := referenced[key]; ok {
			continue
		}
		if reason := dynamicReason(key); reason != "" {
			continue
		}
		orphans = append(orphans, key)
	}
	sort.Strings(orphans)
	for _, k := range orphans {
		t.Errorf("catalog key %q is registered but nothing in client/view uses it — "+
			"delete it, or add its prefix to dynamicPrefixes if it is built at runtime", k)
	}
}

// TestEveryPlaceholderIsResolvable is the check a translation most needs.
//
// A message whose text contains {name} is interpolated by whatever the call
// site passes. This cannot verify the ARGUMENTS (they are in an unlinkable
// package), but it can verify that the English never carries a placeholder that
// is obviously a typo — an unclosed brace, or a brace pair with a space in it,
// which interpolates to nothing and renders the literal on screen.
func TestEveryPlaceholderIsResolvable(t *testing.T) {
	for _, e := range Export(DefaultLocale) {
		texts := []string{e.Text}
		for _, v := range e.Plural {
			texts = append(texts, v)
		}
		for _, txt := range texts {
			for i := 0; i < len(txt); i++ {
				if txt[i] != '{' {
					continue
				}
				end := strings.IndexByte(txt[i:], '}')
				if end < 0 {
					t.Errorf("%s: unclosed placeholder in %q", e.Key, txt)
					break
				}
				name := txt[i+1 : i+end]
				if name == "" || strings.ContainsAny(name, " \t{") {
					t.Errorf("%s: malformed placeholder {%s} in %q", e.Key, name, txt)
				}
				i += end
			}
		}
	}
}

// TestPluralsHaveOther guards the one form the runtime falls back to. A plural
// message without "other" resolves to Default, and a Default built from a
// missing key is the empty string — a blank label nobody notices in review.
func TestPluralsHaveOther(t *testing.T) {
	for _, e := range Export(DefaultLocale) {
		if len(e.Plural) == 0 {
			continue
		}
		if e.Plural[string(Other)] == "" {
			t.Errorf("%s is pluralised but has no %q form", e.Key, Other)
		}
	}
}

// literal unwraps a string literal argument.
func literal(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

func registeredKeys() map[string]bool {
	out := map[string]bool{}
	for _, e := range Export(DefaultLocale) {
		out[e.Key] = true
	}
	return out
}

func dynamicReason(key string) string {
	for prefix, why := range dynamicPrefixes {
		if strings.HasPrefix(key, prefix) {
			return why
		}
	}
	return ""
}

// referencedKeys collects every literal key passed to i18n.T or i18n.N in
// client/view, mapped to the file:line that used it.
func referencedKeys(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}

	entries, err := os.ReadDir(viewDir)
	if err != nil {
		t.Fatalf("cannot read %s: %v", viewDir, err)
	}
	fset := token.NewFileSet()
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		path := filepath.Join(viewDir, e.Name())
		// ParseFile ignores build tags, which is the point: client/view is
		// js+wasm and would otherwise be invisible to a native test.
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("cannot parse %s: %v", path, perr)
		}
		seen++

		// First, every `x := tr.NS("surface")` in this file, so an ns.T("key")
		// below can be attributed to the right namespace. Package-wide rather
		// than scope-aware: two handles bound to different namespaces under the
		// same variable name in one file would confuse it, and the test says so
		// rather than pretending otherwise.
		namespaces := map[string]string{}
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			name, ok := as.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			call, ok := as.Rhs[0].(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NS" {
				return true
			}
			if ns, ok := literal(call.Args[0]); ok {
				namespaces[name.Name] = ns
			}
			return true
		})

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" {
				return true
			}
			recv, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch recv.Name {
			case "tr":
				// tr.T("namespace", "key") — the Runtime form.
				if len(call.Args) < 2 {
					return true
				}
				ns, ok1 := literal(call.Args[0])
				key, ok2 := literal(call.Args[1])
				if !ok1 || !ok2 {
					// A computed namespace or key. Invisible here by
					// construction — see dynamicPrefixes.
					return true
				}
				out[ns+"."+key] = fset.Position(call.Args[1].Pos()).String()
			default:
				// A Namespace handle: `ns := tr.NS("feedSettings")` then
				// ns.T("key"). The namespace is recovered from the NS() call
				// that produced the variable, collected in the pass below.
				bound, known := namespaces[recv.Name]
				if !known || len(call.Args) < 1 {
					return true
				}
				key, okKey := literal(call.Args[0])
				if !okKey {
					return true
				}
				out[bound+"."+key] = fset.Position(call.Args[0].Pos()).String()
			}
			return true
		})
	}
	// A guard that inspected nothing passes vacuously, which is how a guard
	// quietly stops being one — the same rule internal/tools/guards states.
	if seen == 0 {
		t.Fatalf("no .go files found in %s: this test would pass without checking anything", viewDir)
	}
	// A guard that inspected nothing passes vacuously. The file count above
	// catches an empty directory; this catches the subtler version — a call-shape
	// change that makes the walker stop recognising translations, which would
	// turn both tests green while checking nothing at all.
	if len(out) < 300 {
		t.Fatalf("only %d translation keys found across %d files — the walker has "+
			"probably stopped recognising the call shape", len(out), seen)
	}
	return out
}
