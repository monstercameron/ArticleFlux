package host

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// --- data directory -----------------------------------------------------------

func TestTheDataFolderIsUnderLocalAppData(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	got, err := ResolveDataDir("")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(base, "ArticleFlux"); got != want {
		t.Errorf("data dir = %q, want %q", got, want)
	}
	p := PathsIn(got)
	if p.DB != filepath.Join(base, "ArticleFlux", "articleflux.db") {
		t.Errorf("db = %q", p.DB)
	}
	// Everything the shell names lives inside the one folder, so deleting it is
	// a complete reset.
	for _, f := range []string{p.DB, p.Lock, p.WebView} {
		if filepath.Dir(f) != got {
			t.Errorf("%q is outside the data folder %q", f, got)
		}
	}
}

func TestAnExplicitDataFolderWinsAndIsMadeAbsolute(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	got, err := ResolveDataDir("  rel\\reading  ")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Join("rel", "reading")) {
		t.Errorf("data dir = %q, want an absolute path ending rel\\reading", got)
	}
}

// --- instance lock ------------------------------------------------------------

func TestASecondCopyCannotTakeTheSameDataFolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: err = %v, want ErrLocked", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	// Released means released: the next launch gets in.
	again, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	_ = again.Release()
	_ = again.Release() // twice is fine
}

func TestDifferentDataFoldersDoNotContend(t *testing.T) {
	a, err := AcquireLock(filepath.Join(t.TempDir(), "desktop.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release()
	b, err := AcquireLock(filepath.Join(t.TempDir(), "desktop.lock"))
	if err != nil {
		t.Fatalf("a different folder was refused: %v", err)
	}
	_ = b.Release()
}

// --- port ---------------------------------------------------------------------

func TestListenTakesTheRememberedPortWhenItIsFree(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	want := Port(probe)
	_ = probe.Close()

	ln, err := Listen(want)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if Port(ln) != want {
		t.Errorf("port = %d, want the remembered %d", Port(ln), want)
	}
	if host, _, _ := net.SplitHostPort(ln.Addr().String()); host != "127.0.0.1" {
		t.Errorf("bound to %q; the no-login server must be loopback-only", host)
	}
}

func TestListenFallsBackWhenTheRememberedPortIsTaken(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ln, err := Listen(Port(held))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if Port(ln) == Port(held) || Port(ln) == 0 {
		t.Errorf("port = %d, want a fresh one", Port(ln))
	}
}

func TestListenNeverTakesTheDevServersPort(t *testing.T) {
	ln, err := Listen(devServerPort)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if Port(ln) == devServerPort {
		t.Fatal("took 9000, which is the dev server's")
	}
}

func TestParsePortRejectsNonsense(t *testing.T) {
	for in, want := range map[string]int{"": 0, "abc": 0, "-1": 0, "70000": 0, "51234": 51234} {
		if got := ParsePort(in); got != want {
			t.Errorf("ParsePort(%q) = %d, want %d", in, got, want)
		}
	}
}

// --- gate ---------------------------------------------------------------------

func newGate(t *testing.T) (*Gate, *int) {
	t.Helper()
	hits := 0
	return &Gate{
		Origin: OriginFor(51234), Host: HostFor(51234), Token: "tok-123",
		Next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }),
	}, &hits
}

func serve(g *Gate, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func authed(method, target string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:51234"+target, nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "tok-123"})
	return r
}

func TestTheGateAdmitsTheWindow(t *testing.T) {
	g, hits := newGate(t)
	if w := serve(g, authed("GET", "/")); w.Code != 200 || *hits != 1 {
		t.Fatalf("GET / = %d (hits %d), want it passed through", w.Code, *hits)
	}
	r := authed("POST", "/speech")
	r.Header.Set("Origin", "http://127.0.0.1:51234")
	if w := serve(g, r); w.Code != 200 {
		t.Fatalf("same-origin POST = %d", w.Code)
	}
}

func TestTheLaunchLinkSetsAStrictCookieAndLeavesTheAddressBar(t *testing.T) {
	g, _ := newGate(t)
	w := serve(g, httptest.NewRequest("GET", "http://127.0.0.1:51234"+LaunchPath+"?t=tok-123", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("launch = %d to %q, want 303 to /", w.Code, w.Header().Get("Location"))
	}
	c := w.Result().Cookies()
	if len(c) != 1 || c[0].Name != CookieName || c[0].Value != "tok-123" ||
		!c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie = %+v, want HttpOnly SameSite=Strict carrying the token", c)
	}
	if w := serve(g, httptest.NewRequest("GET", "http://127.0.0.1:51234"+LaunchPath+"?t=wrong", nil)); w.Code != 403 {
		t.Errorf("a wrong token = %d, want 403", w.Code)
	}
	if w := serve(g, httptest.NewRequest("GET", "http://127.0.0.1:51234"+LaunchPath, nil)); w.Code != 403 {
		t.Errorf("no token = %d, want 403", w.Code)
	}
}

func TestTheGateRefusesEveryoneElse(t *testing.T) {
	g, hits := newGate(t)
	cases := map[string]*http.Request{}

	cases["no cookie"] = httptest.NewRequest("GET", "http://127.0.0.1:51234/", nil)

	r := httptest.NewRequest("GET", "http://127.0.0.1:51234/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "tok-12"})
	cases["wrong cookie"] = r

	r = authed("GET", "/")
	r.Host = "evil.example:51234" // DNS rebinding: right socket, attacker's name
	cases["rebound host"] = r

	r = authed("GET", "/")
	r.Host = "localhost:51234"
	cases["localhost spelling"] = r

	r = authed("POST", "/debug/reset-state")
	r.Header.Set("Origin", "http://127.0.0.1:51234")
	cases["debug endpoint"] = r

	r = authed("POST", "/speech")
	r.Header.Set("Origin", "https://evil.example")
	cases["cross-origin post"] = r

	r = authed("GET", "/grpc")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "http://127.0.0.1:9000")
	cases["tunnel from the dev server's origin"] = r

	for name, r := range cases {
		if w := serve(g, r); w.Code < 400 {
			t.Errorf("%s: %d, want refused", name, w.Code)
		}
	}
	if *hits != 0 {
		t.Errorf("%d refused requests reached the server", *hits)
	}
}

// --- page messages --------------------------------------------------------------

func TestOnlyTheReadersOwnPageMayAskForAnything(t *testing.T) {
	o := OriginFor(51234)
	if m := ParseMessage(o, "http://127.0.0.1:51234/feed/x", "articleflux:title Hello · ArticleFlux"); m.Kind != MsgTitle || m.Value != "Hello · ArticleFlux" {
		t.Errorf("own page title = %+v", m)
	}
	for _, sender := range []string{"", "https://evil.example/", "http://127.0.0.1:9000/", "http://localhost:51234/"} {
		if m := ParseMessage(o, sender, "articleflux:open https://example.com/"); m.Kind != MsgUnknown {
			t.Errorf("message from %q accepted: %+v", sender, m)
		}
	}
}

func TestOnlyWebAddressesOnAnotherOriginOpenInTheBrowser(t *testing.T) {
	o := OriginFor(51234)
	good := []string{"https://example.com/a?b=c#d", "http://news.ycombinator.com/item?id=1"}
	bad := []string{
		"file:///C:/Windows/System32/calc.exe", "ms-settings:privacy", "javascript:alert(1)",
		"http://127.0.0.1:51234/settings", // ours: the browser has no cookie for it
		"https://user:pw@example.com/", "//example.com", "", strings.Repeat("a", maxURL+1),
	}
	for _, u := range good {
		if got, ok := ExternalURL(o, u); !ok || got != u {
			t.Errorf("ExternalURL(%q) = %q, %v; want accepted", u, got, ok)
		}
	}
	for _, u := range bad {
		if _, ok := ExternalURL(o, u); ok {
			t.Errorf("ExternalURL(%q) accepted", u)
		}
	}
}

func TestTitlesAreCleanedForTheTitleBar(t *testing.T) {
	if got := CleanTitle("  a\n\tb\u2028c  "); got != "a b c" {
		t.Errorf("CleanTitle = %q", got)
	}
	if got := CleanTitle(""); got != "ArticleFlux" {
		t.Errorf("empty title = %q, want the app name", got)
	}
	if got := []rune(CleanTitle(strings.Repeat("é", 1000))); len(got) != maxTitle {
		t.Errorf("long title is %d runes, want %d", len(got), maxTitle)
	}
}

func TestTheShimIsBoundToOneOrigin(t *testing.T) {
	s := Shim(OriginFor(51234))
	if !strings.Contains(s, `const ORIGIN = "http://127.0.0.1:51234";`) {
		t.Error("the origin was not baked into the shim")
	}
	if strings.Contains(s, "__ORIGIN__") {
		t.Error("placeholder left in the shim")
	}
	if got := CmdAddFeed.Script(); got != `window.__articlefluxDesktop&&window.__articlefluxDesktop.run("add");` {
		t.Errorf("command script = %q", got)
	}
}

// --- window bounds ----------------------------------------------------------------

func TestWindowBoundsRoundTripAndRefuseNonsense(t *testing.T) {
	b := Bounds{X: 10, Y: 20, Width: 1400, Height: 900, Maximised: true}
	if got, ok := ParseBounds(b.String()); !ok || got != b {
		t.Errorf("round trip = %+v, %v", got, ok)
	}
	for _, s := range []string{"", "{", `{"w":3,"h":3}`, `{"x":0,"y":0,"w":99999,"h":900}`, `{"x":-999999,"y":0,"w":1000,"h":900}`} {
		if _, ok := ParseBounds(s); ok {
			t.Errorf("ParseBounds(%q) accepted", s)
		}
	}
}

func TestAWindowOnAnUnpluggedMonitorIsNotRestoredThere(t *testing.T) {
	laptop := []Rect{{X: 0, Y: 0, Width: 1920, Height: 1040}}
	if !(Bounds{X: 100, Y: 100, Width: 1280, Height: 800}).Visible(laptop) {
		t.Error("an on-screen window was judged invisible")
	}
	// It was on the monitor to the right, which is gone.
	if (Bounds{X: 2200, Y: 100, Width: 1280, Height: 800}).Visible(laptop) {
		t.Error("a window wholly off-screen was judged visible")
	}
	// Title bar above the top of the screen: cannot be grabbed.
	if (Bounds{X: 100, Y: -400, Width: 1280, Height: 800}).Visible(laptop) {
		t.Error("a window whose title bar is off the top was judged visible")
	}
	if (Bounds{Width: 1280, Height: 800}).Visible(nil) {
		t.Error("no screens means nothing is visible")
	}
}
