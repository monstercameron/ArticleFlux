package host

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
)

// LaunchPath is where the window starts: it trades the per-launch token for a
// cookie and redirects to the reader.
const LaunchPath = "/__desktop/launch"

// CookieName carries the launch token. Its own name, because cookies are
// scoped by host and not by port — the dev server on 127.0.0.1:9000 shares the
// jar in an ordinary browser — and a name nothing else sets cannot collide.
const CookieName = "articleflux_desktop"

// NewToken mints a launch token: 256 bits, URL-safe.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Gate is what stands between the loopback socket and a server that asks no
// one for a password.
//
// # Why the desktop needs more than the dev server has
//
// The shell runs internal/app in DevMode — one local account, no login —
// because a person on their own laptop should not have to sign in to their own
// feed reader. DevMode was built for a developer's loopback port, and it
// assumes that whoever can reach the socket is the person at the keyboard. On a
// laptop that the user browses the web from, three other parties can reach it:
//
//   - Any web page in any browser on this machine, by guessing the port. It
//     cannot READ responses (no CORS), but it can send form POSTs and load URLs.
//   - A page using DNS rebinding, which CAN read responses: its own hostname,
//     re-pointed at 127.0.0.1, makes the request same-origin to the browser.
//   - Another Windows account on the same machine — loopback is shared across
//     sessions.
//
// # What it checks, in order
//
//  1. Host must be exactly 127.0.0.1:<port>. That is the whole DNS-rebinding
//     defence: a rebound request carries the attacker's hostname.
//  2. /debug/ is not served. DevMode mounts the e2e suite's reset and ingest
//     endpoints there, unauthenticated by design; the desktop has no use for
//     them, and "wipe my read state" should not be one POST away.
//  3. The launch token. The window opens LaunchPath?t=<token>, which sets an
//     HttpOnly, SameSite=Strict cookie; every other request must carry it. This
//     is what makes the socket useless to the other two parties above — a
//     cross-site request does not carry a Strict cookie, and another account
//     never saw the token.
//  4. A WebSocket upgrade, or any request that can change state, must come
//     from this origin if it names one. The cookie already implies it; this is
//     the check the gRPC tunnel would make itself with app.Config.
//     AllowedOrigins, which cannot be set here because the port is chosen after
//     app.Open (see Start).
//
// The token is new on every launch and is never written anywhere.
type Gate struct {
	Origin string
	Host   string
	Token  string
	Next   http.Handler
}

func (g *Gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Host != g.Host {
		// 421 is "this server is not the one for that name", which is exactly
		// the rebinding case, and a status nothing in the client treats as
		// retryable.
		http.Error(w, "wrong host", http.StatusMisdirectedRequest)
		return
	}
	if r.URL.Path == "/debug" || strings.HasPrefix(r.URL.Path, "/debug/") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == LaunchPath {
		g.launch(w, r)
		return
	}
	if !g.hasCookie(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("This address belongs to the ArticleFlux desktop app. Open the app instead.\n"))
		return
	}
	if (isUpgrade(r) || !safeMethod(r.Method)) && !g.sameOrigin(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	g.Next.ServeHTTP(w, r)
}

func (g *Gate) launch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if !equal(r.URL.Query().Get("t"), g.Token) {
		http.Error(w, "this launch link has expired; open the app again", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    g.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// No Expires: a session cookie dies with the WebView process, and the
		// token it carries is dead by then anyway.
	})
	// 303 so the token leaves the address bar and the history entry; Back
	// from the reader does not land on a launch link that no longer works.
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (g *Gate) hasCookie(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	return err == nil && equal(c.Value, g.Token)
}

// sameOrigin accepts a request with no Origin header — same-origin GETs and
// the Service Worker's fetches send none — and otherwise demands an exact match.
func (g *Gate) sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	return o == "" || o == g.Origin
}

func equal(a, b string) bool {
	return a != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func isUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}
