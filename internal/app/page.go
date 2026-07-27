package app

import (
	"context"
	"encoding/base64"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/pageproxy"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// pageTTL is how long a minted page capability stays valid.
//
// Shorter than an asset's twelve hours. An image URL has to outlive an article
// left open over lunch; a page capability is spent within seconds of being
// handed out, and anything longer is a link that works after it has left the
// reading pane it was minted for.
const pageTTL = 2 * time.Hour

// servePage serves a publisher's page through the proxy (§10.1b, tier 2).
//
//	GET /p?u=<base64url(url)>&e=<unix expiry>&s=<signature>
//
// Same capability model as /asset, and for the same reason — an <iframe> cannot
// send an Authorization header either. What is different is what comes back: an
// image is inert and a page is not, so this handler carries the isolation that
// §10.1b calls Rule 1.
//
// # How the origin is isolated without a second hostname yet
//
// D20 is still open, and until it is answered proxied HTML would share an origin
// with the app that holds the session. The response therefore carries
// `Content-Security-Policy: sandbox`, which is not a weaker version of the
// hostname split — it puts the document in an **opaque origin**. It cannot read
// this application's localStorage or cookies, cannot script (there is no script
// left after sanitizing, and `script-src 'none'` means one reintroduced by a
// bug still cannot run), and cannot submit a form anywhere.
//
// That is a real control rather than a placeholder, and the separate hostname is
// still worth having: it moves the guarantee from a header we must keep correct
// forever to a boundary the browser enforces without being asked. Set
// ProxyOrigin and this handler serves from there instead, with no other change.
func (a *App) servePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if a.pages == nil {
		http.Error(w, "the page proxy is not configured on this server", http.StatusNotImplemented)
		return
	}

	q := r.URL.Query()
	raw, err := base64.RawURLEncoding.DecodeString(q.Get("u"))
	if err != nil || len(raw) == 0 {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	exp, err := strconv.ParseInt(q.Get("e"), 10, 64)
	if err != nil {
		http.Error(w, "bad expiry", http.StatusBadRequest)
		return
	}
	if !secret.VerifySignature(a.assetKey, pageMessage(string(raw), exp), q.Get("s")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if time.Now().Unix() > exp {
		a.pageError(w, http.StatusGone, "This link expired",
			"Close the page and open it again from the article.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	// Links are rewritten too, so clicking through inside the proxy stays inside
	// it. Without this the first click leaves for the origin — which, on the
	// network this feature exists for, is the click that fails.
	page, err := a.pages.Get(ctx, string(raw), a.AssetURL, a.PageURL)
	if err != nil {
		a.log.Debug("page proxy fetch failed", "err", err)
		switch {
		case errors.Is(err, pageproxy.ErrNotHTML):
			a.pageError(w, http.StatusUnsupportedMediaType, "That isn't a web page",
				"The link points at a file rather than a page. Open the original to download it.")
		case errors.Is(err, pageproxy.ErrTooLarge):
			a.pageError(w, http.StatusRequestEntityTooLarge, "That page is too big to proxy",
				"Try Reader instead — it keeps the text and drops the weight.")
		default:
			a.pageError(w, http.StatusBadGateway, "That page didn't load",
				"The site may be refusing automated requests. Try Reader instead.")
		}
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", pageCSP)
	// Private and brief. This is one reader's view of a page, and §10.1b's
	// whole argument is that it must not be shared or persisted casually.
	h.Set("Cache-Control", "private, max-age=0, must-revalidate")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(a.pageShell(page)))
}

// pageCSP is the response policy for proxied HTML.
//
// `sandbox` first and unqualified except for popups: it is what forces the
// opaque origin. `allow-popups` is the one relaxation, and it buys the thing the
// reader most wants — a link that opens in a new tab still works. The popup
// inherits the sandbox, so it cannot escape into our origin either.
//
// Everything else is an allowlist of what a *document* legitimately needs after
// its scripts are gone: our own images and styles, and nothing from anywhere
// else. `form-action 'none'` is belt to sanitize's braces — forms are already
// dropped, and a login box on our origin posting to a stranger is the one
// mistake here with a real victim.
const pageCSP = "sandbox allow-popups; " +
	"default-src 'none'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"font-src 'self' data:; " +
	"media-src 'self'; " +
	"script-src 'none'; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'self'"

// PageURL mints a capability for one page URL.
func (a *App) PageURL(rawURL string) string {
	if a.pages == nil || rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, a.pagePrefix()) {
		return ""
	}
	exp := time.Now().Add(pageTTL).Unix()
	return a.pagePrefix() +
		"?u=" + base64.RawURLEncoding.EncodeToString([]byte(rawURL)) +
		"&e=" + strconv.FormatInt(exp, 10) +
		"&s=" + url.QueryEscape(secret.Sign(a.assetKey, pageMessage(rawURL, exp)))
}

func (a *App) pagePrefix() string {
	if a.cfg.ProxyOrigin != "" {
		return strings.TrimRight(a.cfg.ProxyOrigin, "/") + "/p"
	}
	return "/p"
}

// pageMessage is what gets signed. The "page" prefix is not decoration: without
// it an asset capability and a page capability over the same URL would have the
// same signature, and an image URL would become a licence to proxy a document.
func pageMessage(rawURL string, exp int64) string {
	return "page\n" + rawURL + "\n" + strconv.FormatInt(exp, 10)
}

// pageShell wraps the proxied document in the provenance strip.
//
// The strip is the one piece of our own interface that appears inside someone
// else's page, and it earns that by answering the question the whole feature
// raises: you are not looking at the site, you are looking at our copy of it,
// fetched from somewhere else, with parts removed. A viewer who forgets that
// will eventually mistake a stale snapshot for a live page, or wonder why a
// menu does not open.
//
// It is styled inline and in one block because this document is served under a
// CSP that permits our stylesheet and nothing else, and because the page below
// it is a stranger's CSS that will fight anything it can reach. Hence the
// `af-` prefix, `all: initial` on the host element, and the fixed position: a
// publisher's `* { position: static !important }` is a real thing.
func (a *App) pageShell(p pageproxy.Page) string {
	host := p.FinalURL
	if u, err := url.Parse(p.FinalURL); err == nil && u.Host != "" {
		host = u.Host
	}
	title := p.Title
	if title == "" {
		title = host
	}

	var b strings.Builder
	b.WriteString("<!doctype html><html><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	b.WriteString("<title>" + html.EscapeString(title) + "</title>")
	b.WriteString("<style>" + pageStripCSS + "</style>")
	b.WriteString("</head><body>")

	b.WriteString(`<aside class="af-strip" role="note">`)
	b.WriteString(`<span class="af-route">`)
	b.WriteString(`<span class="af-from">` + html.EscapeString(host) + `</span>`)
	b.WriteString(`<span class="af-arrow" aria-hidden="true">→</span>`)
	b.WriteString(`<span class="af-via">your server</span>`)
	b.WriteString(`<span class="af-arrow" aria-hidden="true">→</span>`)
	b.WriteString(`<span class="af-you">you</span>`)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="af-note">Scripts removed. Some things won't work.</span>`)
	b.WriteString(`<a class="af-out" href="` + html.EscapeString(p.FinalURL) +
		`" target="_blank" rel="noopener noreferrer">Open the real site</a>`)
	b.WriteString(`</aside>`)

	b.WriteString(`<div class="af-page">`)
	b.WriteString(p.HTML)
	b.WriteString(`</div>`)
	b.WriteString("</body></html>")
	return b.String()
}

// pageStripCSS styles the provenance strip and nothing else.
//
// `all: initial` on the strip is load-bearing. Everything below it is a
// stranger's stylesheet, and a page that sets `aside { display:none }` — which
// plenty do — would otherwise erase the one piece of interface that explains
// what the reader is looking at.
const pageStripCSS = `
.af-strip{all:initial;position:fixed;inset:0 0 auto 0;z-index:2147483647;
 display:flex;gap:14px;align-items:baseline;flex-wrap:wrap;
 padding:9px 16px;box-sizing:border-box;
 font:500 12px/1.4 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;
 letter-spacing:.02em;color:#e8e4dc;background:#14161c;
 border-bottom:1px solid #2a2e39}
.af-strip .af-route{display:inline-flex;gap:7px;align-items:baseline}
.af-strip .af-from{color:#f5f2ec;font-weight:600}
.af-strip .af-arrow{color:#5c6377}
.af-strip .af-via{color:#9aa3b8}
.af-strip .af-you{color:#9aa3b8}
.af-strip .af-note{color:#767e91;font-weight:400}
.af-strip .af-out{margin-left:auto;color:#e8e4dc;text-decoration:none;
 border:1px solid #3a4050;border-radius:999px;padding:3px 11px;white-space:nowrap}
.af-strip .af-out:hover{border-color:#5c6377;background:#1b1e27}
.af-strip .af-out:focus-visible{outline:2px solid #b8c4ff;outline-offset:2px}
.af-page{padding-top:42px}
@media (max-width:520px){
 .af-strip .af-note{display:none}
 .af-page{padding-top:62px}
}
`

// pageError renders a failure as a page rather than as a wall of plain text.
//
// This lands inside an iframe in the reading pane, where `http.Error`'s bare
// monospace line looks like the application broke. Every message here names what
// happened and what to do instead — an error in an embedded frame with no way
// out is the worst version of this feature.
func (a *App) pageError(w http.ResponseWriter, code int, headline, remedy string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>` +
		html.EscapeString(headline) + `</title><style>
body{margin:0;display:grid;place-items:center;min-height:100vh;background:#14161c;
 color:#e8e4dc;font:400 14px/1.6 ui-sans-serif,system-ui,sans-serif;text-align:center}
div{max-width:38ch;padding:24px}
h1{margin:0 0 6px;font-size:15px;font-weight:600}
p{margin:0;color:#9aa3b8}
</style><div><h1>` + html.EscapeString(headline) + `</h1><p>` +
		html.EscapeString(remedy) + `</p></div>`))
}
