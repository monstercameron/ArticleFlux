package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"unicode"
)

// Guard 5: no hardcoded user-facing copy in client/view.
//
// This is the ratchet behind TODO 8.4a. The catalog is only worth having if it
// stays complete, and a catalog decays the same way every time: someone adds a
// button at 2am with the label inline because that is the fast way past a
// problem, and nobody notices for a month. By then re-extracting costs what the
// original extraction cost, which is how §22.16's "it always gets deferred
// forever" happens to a project that already did the work once.
//
// # What it flags
//
// Only string literals in argument or field positions that PUT TEXT ON SCREEN:
//
//	html.Text("…")                    element children
//	Props{Title: "…", Placeholder: …} tooltips and field hints
//	Aria: {"label": "…"}              accessible names
//	Raw:  {"title": "…", …}           the same, spelled the long way
//
// plus any literal passed to one of the view's own label-taking helpers
// (fsGroup, setRow, actionButton…), because those are where a label most often
// gets written inline.
//
// # What it deliberately does NOT flag
//
// Class names, data-attribute values, action ids, CSS, keyboard key names,
// glyphs, and anything that is not prose. The test for prose here is crude on
// purpose — a letter and a space, or a letter and a sentence mark — because a
// clever heuristic that flags `"feed-row"` trains people to add exemptions, and
// an exemption list is where a guard goes to die.
//
// # Why a guard and not a test
//
// It runs in `make lint` beside the other four structural guards, which is
// where someone looks when they want to know whether a change is allowed. The
// two catalog tests in client/i18n are the other half — they check that keys
// resolve; this checks that keys are used at all.

// copyProps are Props fields whose value is rendered to a person.
//
// Value is NOT here: it is the contents of an input, which is data. Alt is,
// because alternative text is prose by definition.
var copyProps = map[string]bool{
	"Title":       true,
	"Placeholder": true,
	"Alt":         true,
}

// copyAttrs are Aria/Raw map keys whose value is rendered to a person, either
// visually or by a screen reader.
var copyAttrs = map[string]bool{
	"label":                true,
	"aria-label":           true,
	"title":                true,
	"placeholder":          true,
	"alt":                  true,
	"aria-description":     true,
	"aria-roledescription": true,
}

// labelTakers are the view's own helpers whose named arguments are copy. The
// int is the number of leading arguments to skip — an action id, a glyph, a
// class — before the ones that are text.
//
// A helper with a variable shape is not listed. This is a list of the places
// copy demonstrably went inline during the port, not an attempt at coverage.
// The counts include the leading `tr i18n.Runtime` every helper that
// translates now carries, so they are one higher than the visible label
// position. That parameter is why the offsets are worth stating rather than
// guessing: a helper that gains or loses it silently shifts every entry here.
var labelTakers = map[string]int{
	"actionButton":   2,  // action, class, LABEL      (no tr: takes finished text)
	"itemChip":       1,  // action, LABEL, …          (no tr)
	"glyphChip":      2,  // action, glyph, LABEL, …   (no tr)
	"glyphItemChip":  2,  // action, glyph, LABEL, …   (no tr)
	"staticChip":     0,  // LABEL                     (no tr)
	"fsGroup":        1,  // glyph, TITLE, NOTE        (no tr)
	"fsRow":          0,  // LABEL, HINT, …            (no tr)
	"fsFact":         0,  // LABEL, VALUE              (no tr)
	"setRow":         0,  // LABEL, HINT, …            (no tr)
	"setFact":        0,  // LABEL, VALUE              (no tr)
	"afField":        0,  // LABEL, HINT, …            (no tr)
	"pickChip":       2,  // action, value, LABEL, …   (no tr)
	"specialRow":     2,  // tr, glyph, LABEL, …
	"railBand":       1,  // glyph, LABEL              (no tr)
	"railBandToggle": 2,  // tr, glyph, LABEL, …
	"emptyState":     -1, // tr plus two catalog KEYS, never text — see below
}

func guardNoInlineCopy(root string) *guard {
	g := &guard{
		name: "no hardcoded UI copy in client/view",
		note: "client/view has no Go files yet (Tier 8)",
	}
	walkGo(root, func(path string, f *ast.File, fset *token.FileSet) {
		rel := relSlash(root, path)
		if !strings.HasPrefix(rel, "client/view/") || strings.HasSuffix(rel, "_test.go") {
			return
		}
		g.checked++

		report := func(pos token.Pos, val, where string) {
			g.findings = append(g.findings, finding{
				file: fmt.Sprintf("%s:%d", rel, fset.Position(pos).Line),
				detail: fmt.Sprintf("%s carries the literal %q — route it through "+
					"i18n.T with a key in client/i18n/en_*.go (TODO 8.4a, plan.md §22.16)",
					where, val),
			})
		}

		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				inspectCall(node, report)
			case *ast.CompositeLit:
				inspectComposite(node, report)
			}
			return true
		})
	})
	return g
}

// inspectCall covers html.Text and the view's own label-taking helpers.
func inspectCall(call *ast.CallExpr, report func(token.Pos, string, string)) {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		// html.Text("…") — element children, the commonest case by far.
		pkg, ok := fn.X.(*ast.Ident)
		if !ok || pkg.Name != "html" || fn.Sel.Name != "Text" || len(call.Args) == 0 {
			return
		}
		if s, ok := prose(call.Args[0]); ok {
			report(call.Args[0].Pos(), s, "html.Text")
		}
	case *ast.Ident:
		skip, listed := labelTakers[fn.Name]
		if !listed || skip < 0 {
			return
		}
		for i := skip; i < len(call.Args); i++ {
			if s, ok := prose(call.Args[i]); ok {
				report(call.Args[i].Pos(), s, fn.Name+" argument "+strconv.Itoa(i+1))
			}
		}
	}
}

// inspectComposite covers html.Props fields and the Aria/Raw/Data maps.
func inspectComposite(lit *ast.CompositeLit, report func(token.Pos, string, string)) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch key := kv.Key.(type) {
		case *ast.Ident:
			// Props{Title: "…"}
			if copyProps[key.Name] {
				if s, ok := prose(kv.Value); ok {
					report(kv.Value.Pos(), s, "Props."+key.Name)
				}
			}
		case *ast.BasicLit:
			// Aria/Raw map entries: {"label": "…"}
			if key.Kind != token.STRING {
				continue
			}
			name, err := strconv.Unquote(key.Value)
			if err != nil || !copyAttrs[name] {
				continue
			}
			if s, ok := prose(kv.Value); ok {
				report(kv.Value.Pos(), s, "the "+name+" attribute")
			}
		}
	}
}

// prose reports whether an expression is a string literal that looks like text
// a person reads, and returns it.
//
// Also unwraps `"a" + x + "b"` — a concatenation is the shape inline copy takes
// once it has an interpolated value in it, and it is the exact shape the
// catalog's {placeholder} syntax exists to replace.
func prose(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		if !looksLikeCopy(s) {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		if s, ok := prose(v.X); ok {
			return s, true
		}
		return prose(v.Y)
	}
	return "", false
}

// looksLikeCopy is the prose test.
//
// Two or more words, or one word ending in sentence punctuation. That admits
// "Sign in" and "Loading…" and rejects "feed-row", "modal-keep", "sk-w-90",
// "true", "Enter", and every glyph — which are the four kinds of literal that
// legitimately live in this package.
//
// And a command line, which is the fifth. The front door prints a terminal
// transcript (client/view/home.go's homeBuilt), and this guard demanded that
// `articleflux init -db … -user cam` be routed through the catalog — where a
// translation would produce a command that does not run. The short lines
// ("build", "seed", "dev") passed only because they are one word each, so the
// rule was already inconsistent about the same kind of literal.
//
// A single capitalised word like "Feeds" slips through this net. That is a
// known and accepted gap: tightening it to catch one-word labels would also
// catch every action id and CSS class in the file, and a guard that cries wolf
// is a guard someone disables. The two catalog tests in client/i18n cover the
// other direction.
func looksLikeCopy(s string) bool {
	t := strings.TrimSpace(s)
	if len(t) < 3 {
		return false
	}
	// A CSS-ish or attribute-ish token: no spaces and full of the punctuation
	// identifiers use.
	if !strings.ContainsRune(t, ' ') {
		if strings.ContainsAny(t, "-_:/#.=[]{}") {
			return false
		}
		// One word, and copy only if it ends like a sentence.
		return strings.ContainsAny(t[len(t)-3:], ".!?…")
	}
	// Multi-word, and a command line first: see above.
	if hasFlagWord(t) {
		return false
	}
	// Require at least two runs of letters, so "1 2 3" and "▲ ▼" are not copy.
	words := 0
	inWord := false
	for _, r := range t {
		switch {
		case unicode.IsLetter(r):
			if !inWord {
				words++
				inWord = true
			}
		default:
			inWord = false
		}
	}
	return words >= 2
}

// hasFlagWord reports whether any word is a command-line flag: a leading `-` or
// `--` followed by a letter.
//
// One signal, not a shell parser, and chosen because it is the one thing UI copy
// essentially never contains while every non-trivial command line does. A path
// is deliberately NOT a second signal: "Stored under /var/lib" is prose that
// happens to name a directory, and it should be translated.
//
// The near misses are why this reads a word at a time rather than searching for
// " -": an em dash is not a hyphen, so "Sign in — it's free" is untouched,
// "well-known" does not begin with one, "-5 items" has a digit after it, and a
// bare "-" bullet has nothing after it at all.
func hasFlagWord(s string) bool {
	for _, w := range strings.Fields(s) {
		if !strings.HasPrefix(w, "-") {
			continue
		}
		rest := strings.TrimLeft(w, "-")
		if rest == "" {
			continue
		}
		if unicode.IsLetter([]rune(rest)[0]) {
			return true
		}
	}
	return false
}
