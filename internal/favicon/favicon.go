// Package favicon finds and fetches site icons.
//
// A feed reader without icons is a wall of text. The icon is how you recognise a
// source before reading its name — the same job the per-source hue does, done
// better for sites you already know, and complementary for sites you don't.
//
// Everything here goes through netguard, because an icon URL is derived from a
// user-supplied feed URL and is therefore user-supplied too.
package favicon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

// TTL is how long a cached icon stays fresh.
//
// Thirty days. Icons change on the timescale of rebrands; the cost of being a
// month stale is a slightly old logo, against a cost of one request per site per
// check, forever, for the alternative.
const TTL = 30 * 24 * time.Hour

// MaxBytes bounds one icon. 256 KiB is generous for an icon and small enough
// that a site serving a megabyte PNG cannot bloat the database.
const MaxBytes = 256 << 10

// MaxFailures is when we stop asking. A site that has failed five times will not
// start working because we asked a sixth.
const MaxFailures = 5

// Icon is a fetched icon.
type Icon struct {
	Bytes       []byte
	ContentType string
	ETag        string
}

// ErrNoIcon means the site has no usable icon. It is a normal outcome, and it is
// recorded so it is not retried on every page load.
var ErrNoIcon = errors.New("favicon: none found")

// Fetcher retrieves icons.
type Fetcher struct{ client *http.Client }

// New returns a Fetcher. allowPrivate mirrors the feed fetcher's policy.
func New(allowPrivate bool) *Fetcher {
	return &Fetcher{client: netguard.Client(netguard.Options{
		Timeout:      10 * time.Second,
		DialTimeout:  5 * time.Second,
		UserAgent:    "ArticleFlux/0.1 (favicon)",
		AllowPrivate: allowPrivate,
		Purpose:      "favicon",
	})}
}

// utf8BOM is the three-byte UTF-8 byte-order mark a saved XML/SVG document may
// carry. It has to be stripped before sniffing for the same reason a real XML
// parser strips it: otherwise a BOM-prefixed <svg> reads as three bytes of
// noise followed by markup, and a prefix check never fires.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// looksLikeSVG reports whether the ACTUAL BYTES are — or resolve to — an SVG
// document, independent of whatever Content-Type a server claims for them.
//
// This is the load-bearing check. The remote site is the party this package
// defends against, and a Content-Type header is that party's own claim about
// itself: a hostile server can label a script-bearing SVG "image/png" and a
// header-only check would never see it. The bytes are not something it gets
// to relabel.
//
// A real SVG may legally begin with a BOM, leading whitespace, an XML
// declaration, one or more comments, and a DOCTYPE before the <svg> element
// itself — every one of which a naive bytes.HasPrefix(b, []byte("<svg"))
// would miss, and every one of which a hostile server has every incentive to
// use to slip past a naive check. This walks past all of them before making
// the call.
func looksLikeSVG(b []byte) bool {
	b = bytes.TrimPrefix(b, utf8BOM)
	for i := 0; i < 32; i++ { // bounded: no input can make this loop forever
		trimmed := bytes.TrimLeft(b, " \t\r\n\f")
		switch {
		case len(trimmed) >= 2 && trimmed[0] == '<' && trimmed[1] == '?': // <?xml ... ?>
			idx := bytes.Index(trimmed, []byte("?>"))
			if idx < 0 {
				return false
			}
			b = trimmed[idx+2:]
			continue
		case bytes.HasPrefix(trimmed, []byte("<!--")): // <!-- ... -->
			idx := bytes.Index(trimmed, []byte("-->"))
			if idx < 0 {
				return false
			}
			b = trimmed[idx+3:]
			continue
		case len(trimmed) >= 2 && trimmed[0] == '<' && trimmed[1] == '!': // <!DOCTYPE ...>
			idx := bytes.IndexByte(trimmed, '>')
			if idx < 0 {
				return false
			}
			b = trimmed[idx+1:]
			continue
		default:
			b = trimmed
		}
		break
	}
	b = bytes.TrimLeft(b, " \t\r\n\f")
	if len(b) < 4 || !bytes.EqualFold(b[:4], []byte("<svg")) {
		return false
	}
	if len(b) == 4 {
		return true // the whole body is exactly "<svg" — degenerate, still svg-shaped
	}
	switch b[4] {
	case ' ', '\t', '\r', '\n', '\f', '>', '/':
		return true
	}
	return false // e.g. "<svgfoo>", a different element entirely
}

// sniffAllowedImage reports a favicon's true type from its bytes, and whether
// that type is one of the handful this package will store. Nothing here reads
// a header — that is the point.
//
// This mirrors the small set of raster formats a favicon realistically comes
// in. SVG is handled separately by looksLikeSVG and is never in this list: it
// is a document format that can carry script, and this content is served back
// from our own origin (internal/app/favicons.go) — a stored SVG favicon would
// be a stored-XSS vector wearing an icon's clothes, however it got labelled.
func sniffAllowedImage(b []byte) (string, bool) {
	switch {
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png", true
	case len(b) >= 6 && bytes.Equal(b[:3], []byte("GIF")) &&
		(bytes.Equal(b[3:6], []byte("87a")) || bytes.Equal(b[3:6], []byte("89a"))):
		return "image/gif", true
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg", true
	case len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
	case len(b) >= 4 && b[0] == 0 && b[1] == 0 && b[2] == 1 && b[3] == 0:
		// The ICO/CUR family header: reserved(2 bytes) = 0, type(2 bytes) = 1
		// for an icon (2 would be a cursor, which a favicon never legitimately
		// is).
		return "image/x-icon", true
	default:
		return "", false
	}
}

// Fetch finds the best icon for a site.
//
// Order: the page's declared <link rel="icon"> first, then /favicon.ico. The
// declaration is authoritative and usually higher resolution; the well-known
// path is the fallback for sites that declare nothing.
func (f *Fetcher) Fetch(ctx context.Context, siteURL string) (*Icon, error) {
	base, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || base.Host == "" {
		return nil, ErrNoIcon
	}
	if base.Scheme == "" {
		base.Scheme = "https"
	}

	candidates := f.declared(ctx, base)
	candidates = append(candidates, base.Scheme+"://"+base.Host+"/favicon.ico")

	for _, c := range candidates {
		if icon, err := f.get(ctx, c); err == nil {
			return icon, nil
		}
	}
	return nil, ErrNoIcon
}

// declared parses the site's HTML for <link rel="icon"> hrefs.
func (f *Fetcher) declared(ctx context.Context, base *url.URL) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "text/html")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	// Only the head is needed, and a homepage can be megabytes.
	doc, err := html.Parse(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return nil
	}

	var out []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			var rel, href string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "href":
					href = a.Val
				}
			}
			if href != "" && (strings.Contains(rel, "icon") || rel == "apple-touch-icon") {
				if u, err := base.Parse(href); err == nil {
					out = append(out, u.String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out
}

func (f *Fetcher) get(ctx context.Context, raw string) (*Icon, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrNoIcon
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > MaxBytes {
		return nil, ErrNoIcon
	}

	// The decision is made from the bytes, never from the Content-Type header.
	// The header is the remote site's own claim about itself, and the remote
	// site is exactly who this check exists to defend against: a hostile
	// server can serve a script-bearing SVG labelled "image/png" and a
	// header-only check would wave it through. See looksLikeSVG and
	// sniffAllowedImage.
	if looksLikeSVG(body) {
		return nil, ErrNoIcon
	}
	ct, ok := sniffAllowedImage(body)
	if !ok {
		return nil, ErrNoIcon
	}
	return &Icon{Bytes: body, ContentType: ct, ETag: resp.Header.Get("ETag")}, nil
}

// Host extracts the icon cache key from a URL.
func Host(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
