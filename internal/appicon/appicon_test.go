package appicon

import (
	"encoding/json"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/client/design"
)

// The PWA is four files that have to agree with each other, and nothing in the
// build sees more than one of them at a time (§20.24).
//
//	web/manifest.webmanifest   names the icons, the colours and the start URL
//	web/icons/*.png            the icons, generated from the design tokens
//	web/index.html             links the manifest and the iOS icon
//	web/sw.js                  precaches all of it so an install boots offline
//
// Every disagreement between them fails the same way: silently. A manifest naming
// an icon that is not there produces an install prompt that does not appear, with
// nothing in the console of the page itself; a shell that stopped linking the
// manifest produces the same; a worker that stopped precaching it produces an
// installed app that is blank on a plane. None of those is visible in a
// screenshot of a working application, which is exactly the class of defect
// client/design/bootpalette_test.go exists for, one directory over.

// webFile reads something out of web/. This test lives in internal/appicon.
func webFile(t *testing.T, name ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", "..", "web"}, name...)...)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(b)
}

// manifest is only the parts this test has an opinion about. A struct rather than
// a map, so a field that changes shape fails to unmarshal here rather than
// silently reading as absent.
type manifest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ShortName       string `json:"short_name"`
	Description     string `json:"description"`
	StartURL        string `json:"start_url"`
	Scope           string `json:"scope"`
	Display         string `json:"display"`
	BackgroundColor string `json:"background_color"`
	ThemeColor      string `json:"theme_color"`
	Icons           []struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	} `json:"icons"`
	Shortcuts []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"shortcuts"`
	ShareTarget *struct {
		Action string            `json:"action"`
		Method string            `json:"method"`
		Params map[string]string `json:"params"`
	} `json:"share_target"`
}

func readManifest(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal([]byte(webFile(t, "manifest.webmanifest")), &m); err != nil {
		t.Fatalf("web/manifest.webmanifest is not valid JSON: %v", err)
	}
	return m
}

// --- the icons ---------------------------------------------------------------

// TestCommittedIconsAreWhatTheRendererDraws is the drift guard the generator
// exists for.
//
// Regenerating is `go run ./internal/tools/appicon`. This failing means the
// design tokens moved and the icons did not — the same failure D22 produced in
// the splash, where the mockup, the tokens and web/index.html each held a colour
// and one of them was wrong for a while.
func TestCommittedIconsAreWhatTheRendererDraws(t *testing.T) {
	for _, ic := range Render() {
		path := filepath.Join("..", "..", "web", "icons", ic.Name)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s is missing — run: go run ./internal/tools/appicon", ic.Name)
			continue
		}
		if string(got) != string(ic.PNG) {
			t.Errorf("%s is %d bytes on disk and %d rendered — the icons no longer "+
				"match the design tokens. Run: go run ./internal/tools/appicon",
				ic.Name, len(got), len(ic.PNG))
		}
	}
}

// TestTheIconIsTheAppsOwnMark: two colours, and they are the tokens rather than
// two hexes somebody liked.
//
// It samples rather than trusting the code that drew it: the centre must be the
// accent (the mark), and a point just inside the edge at mid-height must be the
// ground. Getting either backwards produces an icon that is a plum diamond on
// amber, which is a thing nobody would notice in a 48px launcher tile.
func TestTheIconIsTheAppsOwnMark(t *testing.T) {
	for _, ic := range Render() {
		img := decode(t, ic)
		b := img.Bounds()
		mid := image.Pt(b.Dx()/2, b.Dy()/2)

		if got := at(img, mid.X, mid.Y); !sameColour(got, design.Accent) {
			t.Errorf("%s: the centre is %s, want the accent %s",
				ic.Name, hexOf(got), design.Accent)
		}
		// Four pixels in from the left edge, at mid-height: outside the kite
		// (whose half-width is 6/32 of the canvas) and inside the tile.
		if got := at(img, 4, mid.Y); !sameColour(got, design.Ground) {
			t.Errorf("%s: the ground is %s, want %s", ic.Name, hexOf(got), design.Ground)
		}
	}
}

// TestMaskableIconsAreFullBleedAndOpaque.
//
// A maskable icon is cropped by the launcher to whatever shape it likes. A
// rounded source cropped to a circle shows four slivers of transparency at the
// corners — the single most common way a maskable icon is wrong — and iOS
// composites a transparent apple-touch-icon onto BLACK, which turns those slivers
// into black wedges on the home screen.
func TestMaskableIconsAreFullBleedAndOpaque(t *testing.T) {
	for _, ic := range Render() {
		img := decode(t, ic)
		b := img.Bounds()
		corners := []image.Point{
			{X: 0, Y: 0}, {X: b.Dx() - 1, Y: 0},
			{X: 0, Y: b.Dy() - 1}, {X: b.Dx() - 1, Y: b.Dy() - 1},
		}
		for _, c := range corners {
			_, _, _, a := img.At(c.X, c.Y).RGBA()
			opaque := a == 0xffff
			if ic.Maskable && !opaque {
				t.Errorf("%s: corner (%d,%d) is transparent; a launcher mask would "+
					"show through it", ic.Name, c.X, c.Y)
			}
			if !ic.Maskable && opaque {
				t.Errorf("%s: corner (%d,%d) is opaque, so the tile is not rounded",
					ic.Name, c.X, c.Y)
			}
		}
	}
}

// TestTheMarkFitsTheMaskableSafeZone.
//
// Android guarantees only the inner 80% — a circle of radius 0.4 — survives every
// launcher's mask. Nothing here is scaled to fit it, because the mark's furthest
// point is 9/32 = 0.281 from the centre and always was; this is what says so, so
// that a future change to the geometry cannot quietly clip the icon on one
// vendor's launcher and no other.
func TestTheMarkFitsTheMaskableSafeZone(t *testing.T) {
	if markHalfH >= safeRadius {
		t.Errorf("the mark reaches %.3f of the canvas and the maskable safe zone is "+
			"%.2f — a round launcher would cut the top and bottom off",
			markHalfH, safeRadius)
	}
	if markHalfW >= safeRadius {
		t.Errorf("the mark is %.3f wide and the safe zone is %.2f", markHalfW, safeRadius)
	}
}

// --- the manifest ------------------------------------------------------------

// TestManifestNamesEveryIconThatIsRendered, in both directions. An icon rendered
// and not named is a file nobody fetches; one named and not rendered is an
// install prompt that never appears.
func TestManifestNamesEveryIconThatIsRendered(t *testing.T) {
	m := readManifest(t)

	named := map[string]bool{}
	for _, ic := range m.Icons {
		named[strings.TrimPrefix(ic.Src, "icons/")] = true
	}
	// apple-touch is linked from the shell rather than from the manifest — iOS
	// reads no manifest at all — so it is checked there instead.
	for _, ic := range Render() {
		if strings.HasPrefix(ic.Name, "apple-touch") {
			continue
		}
		if !named[ic.Name] {
			t.Errorf("%s is rendered but the manifest does not name it", ic.Name)
		}
	}

	rendered := map[string]bool{}
	for _, ic := range Render() {
		rendered[ic.Name] = true
	}
	for _, ic := range m.Icons {
		name := strings.TrimPrefix(ic.Src, "icons/")
		if !rendered[name] {
			t.Errorf("the manifest names %q, which nothing renders", ic.Src)
		}
		if ic.Type != "image/png" {
			t.Errorf("%s is declared %q", ic.Src, ic.Type)
		}
	}

	// The two sizes that decide installability, and the maskable one that decides
	// whether Android crops the art or the whole tile.
	var has192, has512, hasMaskable bool
	for _, ic := range m.Icons {
		switch {
		case ic.Purpose == "maskable":
			hasMaskable = true
		case ic.Sizes == "192x192":
			has192 = true
		case ic.Sizes == "512x512":
			has512 = true
		}
	}
	if !has192 || !has512 {
		t.Error("a browser will not offer to install without a 192 and a 512 " +
			"purpose-any icon")
	}
	if !hasMaskable {
		t.Error("no maskable icon: Android will crop the rounded tile, corners and all")
	}
}

// TestManifestWearsTheHousePalette pins the two colours the OS paints with
// against the tokens they stand in for — the same argument, and the same
// duplication cost, as web/index.html's splash.
//
// These are what somebody sees BEFORE the app runs: background_color is the
// splash Android draws while the module loads, and theme_color is the window
// chrome. Wrong, they are a different product for the second and a half that
// matters most.
func TestManifestWearsTheHousePalette(t *testing.T) {
	m := readManifest(t)
	if !strings.EqualFold(m.BackgroundColor, design.Ground) {
		t.Errorf("background_color is %s, and design.Ground is %s — Android would "+
			"paint its launch screen in a colour this application does not use",
			m.BackgroundColor, design.Ground)
	}
	if !strings.EqualFold(m.ThemeColor, design.Ground) {
		t.Errorf("theme_color is %s, want %s", m.ThemeColor, design.Ground)
	}
	// The shell's own <meta name=theme-color> is the same value, and the running
	// app rewrites it per theme. The static one is the house answer for the frame
	// before any of that exists.
	page := webFile(t, "index.html")
	if !strings.Contains(page, `<meta name="theme-color" content="`+design.Ground+`">`) {
		t.Errorf("web/index.html's theme-color meta is not %s", design.Ground)
	}
}

// TestManifestPathsAreRelative is the one property that lets a single manifest
// describe both deployments.
//
// The server serves the app at the root; the published demo lives at
// /<repo>/ on GitHub Pages. An absolute "/" start_url is correct on the first and
// puts the second's install at the wrong origin path — where it opens somebody
// else's page, or a 404, from a launcher icon.
func TestManifestPathsAreRelative(t *testing.T) {
	m := readManifest(t)
	for _, f := range []struct{ name, val string }{
		{"id", m.ID}, {"start_url", m.StartURL}, {"scope", m.Scope},
	} {
		if strings.HasPrefix(f.val, "/") || strings.Contains(f.val, "://") {
			t.Errorf("%s is %q; it has to be relative or the demo installs at the "+
				"wrong path", f.name, f.val)
		}
	}
	for _, ic := range m.Icons {
		if strings.HasPrefix(ic.Src, "/") {
			t.Errorf("icon src %q is absolute", ic.Src)
		}
	}
	for _, s := range m.Shortcuts {
		if strings.HasPrefix(s.URL, "/") {
			t.Errorf("shortcut %q points at %q, which is absolute", s.Name, s.URL)
		}
	}
	if m.ShareTarget != nil && strings.HasPrefix(m.ShareTarget.Action, "/") {
		t.Errorf("share_target action %q is absolute", m.ShareTarget.Action)
	}
}

// TestManifestIsInstallable checks the fields a browser actually gates on.
func TestManifestIsInstallable(t *testing.T) {
	m := readManifest(t)
	if m.Name == "" || m.ShortName == "" {
		t.Error("name and short_name are both required for an install prompt")
	}
	// Twelve characters is roughly what a launcher shows before it truncates.
	if len(m.ShortName) > 12 {
		t.Errorf("short_name %q is %d characters and will be truncated under the icon",
			m.ShortName, len(m.ShortName))
	}
	switch m.Display {
	case "standalone", "fullscreen", "minimal-ui":
	default:
		t.Errorf("display is %q; a browser will not install a %q app", m.Display, m.Display)
	}
	if m.Description == "" {
		t.Error("no description: the install dialog and every app listing show one")
	}
}

// TestEveryShortcutAndShareParamIsHandled.
//
// A shortcut the client ignores is worse than an absent one: it opens the app,
// nothing happens, and the reader concludes the shortcut is broken rather than
// unimplemented. client/view/launch.go is the other half of this contract, and
// its constants are what these strings have to match.
func TestEveryShortcutAndShareParamIsHandled(t *testing.T) {
	m := readManifest(t)
	// Read as source, because client/view is js && wasm and cannot be linked into
	// a native test binary — the same limitation client/i18n's key coverage test
	// works around the same way.
	src, err := os.ReadFile(filepath.Join("..", "..", "client", "view", "launch.go"))
	if err != nil {
		t.Fatalf("cannot read client/view/launch.go: %v", err)
	}
	code := string(src)

	for _, s := range m.Shortcuts {
		q := s.URL
		if i := strings.IndexByte(q, '?'); i >= 0 {
			q = q[i+1:]
		} else {
			t.Errorf("shortcut %q has no query, so it opens the app and does nothing", s.Name)
			continue
		}
		key, val, _ := strings.Cut(q, "=")
		if !strings.Contains(code, `"`+key+`"`) {
			t.Errorf("shortcut %q sends ?%s=, which client/view/launch.go does not read",
				s.Name, key)
		}
		// The value too, for `view`: an unknown one falls through to the ordinary
		// resume, which is a shortcut that silently does nothing.
		if key == "view" && !strings.Contains(code, `"`+val+`"`) {
			t.Errorf("shortcut %q asks for view=%s, which launch.go does not resolve",
				s.Name, val)
		}
	}

	if m.ShareTarget == nil {
		return
	}
	if !strings.EqualFold(m.ShareTarget.Method, "GET") {
		t.Errorf("share_target method is %q; POST needs a Service Worker handler "+
			"that does not exist", m.ShareTarget.Method)
	}
	for field, param := range m.ShareTarget.Params {
		if !strings.Contains(code, `"`+param+`"`) {
			t.Errorf("share_target maps %s to ?%s=, which launch.go does not read",
				field, param)
		}
	}
}

// --- the shell and the worker -------------------------------------------------

// TestTheShellLinksTheManifestAndTheIOSIcon.
//
// iOS reads no manifest, so its icon is a <link> and nothing else supplies it. A
// missing one is not a broken image — it is a screenshot of the page, scaled
// down, as the home screen icon.
func TestTheShellLinksTheManifestAndTheIOSIcon(t *testing.T) {
	page := webFile(t, "index.html")
	for _, want := range []string{
		`rel="manifest"`,
		`href="manifest.webmanifest"`,
		`rel="apple-touch-icon"`,
		`href="icons/apple-touch-180.png"`,
		`name="apple-mobile-web-app-capable"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("web/index.html no longer carries %s", want)
		}
	}
	// Relative, like every other href on that page: an absolute one 404s wherever
	// the app is not at the root.
	if strings.Contains(page, `href="/manifest.webmanifest"`) {
		t.Error("the manifest is linked absolutely; the demo would not find it")
	}
}

// TestTheWorkerPrecachesTheInstall.
//
// An installed app whose manifest and icons are not in the shell cache is one
// that looks broken the first time it opens without a network — and the manifest
// is what a browser re-reads to decide the installation is still valid.
func TestTheWorkerPrecachesTheInstall(t *testing.T) {
	sw := webFile(t, "sw.js")
	m := readManifest(t)

	if !strings.Contains(sw, "./manifest.webmanifest") {
		t.Error("web/sw.js does not precache the manifest")
	}
	for _, ic := range m.Icons {
		if !strings.Contains(sw, "./"+ic.Src) {
			t.Errorf("web/sw.js does not precache %s", ic.Src)
		}
	}
}

// --- helpers -----------------------------------------------------------------

func decode(t *testing.T, ic Icon) image.Image {
	t.Helper()
	img, _, err := image.Decode(strings.NewReader(string(ic.PNG)))
	if err != nil {
		t.Fatalf("%s did not decode: %v", ic.Name, err)
	}
	return img
}

func at(img image.Image, x, y int) color.NRGBA {
	r, g, b, a := img.At(x, y).RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: uint8(a >> 8)}
}

// sameColour allows one 8-bit level, which is what the supersampled edge can cost
// a pixel that is almost but not quite entirely inside a shape.
func sameColour(got color.NRGBA, hex string) bool {
	want := mustParse(hex)
	return math.Abs(float64(got.R)-float64(want.R)) <= 1 &&
		math.Abs(float64(got.G)-float64(want.G)) <= 1 &&
		math.Abs(float64(got.B)-float64(want.B)) <= 1
}

func hexOf(c color.NRGBA) string {
	const digits = "0123456789ABCDEF"
	out := []byte("#000000")
	for i, v := range []uint8{c.R, c.G, c.B} {
		out[1+i*2] = digits[v>>4]
		out[2+i*2] = digits[v&0x0f]
	}
	return string(out)
}
