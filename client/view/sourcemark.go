//go:build js && wasm

package view

// A source's identity on screen — its hue and its favicon — shared by the rail,
// the list rows and the article eyebrow, which is why it is a file of its own
// rather than part of any one of them.

import (
	"strings"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// hueFor is the view layer's name for design.HueFor, so the palette and the
// rows agree without every call site importing the design package.
func hueFor(sourceID string) string { return design.HueFor(sourceID) }

// faviconSrc is where the client asks for a site's icon, or "" when there is
// nowhere to ask.
//
// Two fixes in one small function, both visible on the published demo:
//
//  1. **Base-relative, not absolute.** "/favicon?host=…" is correct on exactly
//     one deployment shape. On a GitHub project page it leaves the site and asks
//     github.io for a service that is not there; behind a reverse proxy that
//     mounts the app under /reader/ it asks the wrong root.
//  2. **Nothing to ask, so do not ask.** The demo's "server" is compiled into the
//     same module and answers RPCs, not HTTP — there is no favicon endpoint, so
//     every icon was nine 404s per load in a stranger's console. The hue dot
//     already carries identity; the icon is the enhancement, and an enhancement
//     that cannot work should be absent rather than broken.
func faviconSrc(host string) string {
	if host == "" || !HasFaviconService {
		return ""
	}
	return platform.BasePath() + "favicon?host=" + host
}

// HasFaviconService is false in a build with no HTTP server behind it.
//
// A package var rather than a build tag: the demo and the reader are one module
// compiled one way, and the difference between them is which root is mounted
// (see DemoRoot). A tag would mean two builds of the client and a second thing
// CI has to compile.
var HasFaviconService = true

// iconHost prefers the site over the feed: feeds are frequently served from a
// syndication host (feedburner, feedpress) whose icon is not the publisher's.
func iconHost(f *pb.Feed) string {
	for _, u := range []string{f.GetSiteUrl(), f.GetFeedUrl()} {
		if h := hostOf(u); h != "" {
			return h
		}
	}
	return ""
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "://")
	if i < 0 {
		return ""
	}
	h := raw[i+3:]
	if j := strings.IndexAny(h, "/?#"); j >= 0 {
		h = h[:j]
	}
	if k := strings.IndexByte(h, '@'); k >= 0 {
		h = h[k+1:]
	}
	// A bracketed IPv6 literal, e.g. "[::1]:8080", carries ':' inside the
	// brackets — IndexByte would find one of THOSE first and truncate to "[".
	// The brackets are kept in the return value: the caller (iconHost) uses
	// this as a favicon lookup host, and "[::1]" is what belongs in that
	// lookup's host position, the same way it belongs in a URL's authority —
	// bare "::1" there is ambiguous with a port separator.
	if strings.HasPrefix(h, "[") {
		if end := strings.IndexByte(h, ']'); end >= 0 {
			return strings.ToLower(h[:end+1])
		}
	}
	if k := strings.IndexByte(h, ':'); k >= 0 {
		h = h[:k]
	}
	return strings.ToLower(h)
}
