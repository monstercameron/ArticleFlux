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
	})}
}

// allowedTypes are the image types worth storing.
//
// SVG is excluded deliberately. It is a document format that can carry script,
// and this content is served back from our own origin — an SVG favicon would be
// a stored-XSS vector wearing an icon's clothes.
var allowedTypes = map[string]bool{
	"image/png": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
	"image/jpeg": true, "image/gif": true, "image/webp": true,
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

	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !allowedTypes[ct] {
		return nil, ErrNoIcon
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > MaxBytes {
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
