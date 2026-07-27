package design

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The splash screen is the one place in this repository where CSS is not
// authored in Go, and it cannot be anything else: it has to paint on the first
// frame, before the wasm module that emits the stylesheet has been fetched.
//
// The price of that exemption is duplication — web/index.html hardcodes the
// house ground, the cream, the mute, the accent and the seven source hues,
// because there is no Go running yet to hand them over. Duplication with no
// check is drift, and this particular drift is invisible in the worst possible
// way: the splash and the application would simply be slightly different
// products for the 200ms they overlap, and nobody would ever catch it in a
// screenshot of either one alone.
//
// So: the exemption stands, and this test is what it costs.

func bootPage(t *testing.T) string {
	t.Helper()
	// ../../web/index.html — this test lives in client/design.
	path := filepath.Join("..", "..", "web", "index.html")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the splash page at %s: %v", path, err)
	}
	return string(b)
}

// TestSplashUsesTheHousePalette pins every colour the splash paints against the
// token it is standing in for.
func TestSplashUsesTheHousePalette(t *testing.T) {
	page := bootPage(t)
	for _, want := range []struct{ name, hex string }{
		{"Ground", Ground},
		{"Cream", Cream},
		{"Dim", Dim},
		{"Accent", Accent},
		// The verdict colour the failed state uses, so a splash that could not
		// start is the same red as everything else that went wrong.
		{"Fanciful.Neg", Fanciful.Neg},
	} {
		if !strings.Contains(page, want.hex) {
			t.Errorf("web/index.html no longer contains design.%s (%s) — the splash "+
				"and the application have drifted apart", want.name, want.hex)
		}
	}
}

// TestSplashProgressRuleIsTheSourcePalette checks the one piece of ornament on
// the splash that carries information.
//
// The progress fill is a gradient through the seven hand-picked source hues, in
// order, because the idea this whole reader is built on is that every source
// owns a hue — so the bar is made of the thing it is loading. A gradient in some
// other colours would be decoration, and decoration that carries no information
// is what got two earlier design directions rejected.
func TestSplashProgressRuleIsTheSourcePalette(t *testing.T) {
	page := bootPage(t)
	for i, hue := range sourceHues {
		if !strings.Contains(page, hue) {
			t.Errorf("the splash progress rule is missing source hue %d (%s)", i, hue)
		}
	}
	// In order, and in one declaration: seven hues scattered through the file
	// would satisfy the loop above while the bar was a flat colour.
	joined := strings.Join(sourceHues, ",")
	if !strings.Contains(page, joined) {
		t.Errorf("the splash progress rule does not run the source hues in order.\n"+
			"expected the gradient to contain: %s", joined)
	}
}

// TestSplashRespectsReducedMotion is a floor, not a style check.
//
// The application's own motion switch is a server-side preference, and the
// transport is what the splash is waiting for — so at this moment the machine's
// setting is the ONLY signal available, and it is the only chance the splash
// gets to honour it.
func TestSplashRespectsReducedMotion(t *testing.T) {
	page := bootPage(t)
	if !strings.Contains(page, "prefers-reduced-motion") {
		t.Error("the splash animates and never asks whether motion is wanted")
	}
}

// TestSplashKeepsItsBootHandshake pins the two names the splash shares with Go.
//
// Both are stringly-typed handshakes across a boundary the compiler cannot see:
// client/view/theme.go writes the theme mirror under this key so the splash can
// wear the reader's chosen theme instead of flashing plum at them, and the
// module path is what the boot shim fetches. Renaming either on one side is a
// silent regression on the other.
func TestSplashKeepsItsBootHandshake(t *testing.T) {
	page := bootPage(t)
	// `id="app"` rather than "#app": the selector lives in client/app/main.go's
	// ui.Render call, and what this page owes it is the element it selects.
	for _, want := range []string{"af.boot", "/app.wasm", `id="app"`} {
		if !strings.Contains(page, want) {
			t.Errorf("web/index.html no longer references %q", want)
		}
	}
}
