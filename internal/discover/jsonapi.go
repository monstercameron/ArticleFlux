package discover

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// Finding the data behind a page that renders itself (plan.md §11.2b).
//
// When a page's HTML is an application shell, the entries are not missing — they
// are one request away, and that request is almost always a plain GET returning
// plain JSON to the same origin. This finds it by trying the conventional
// rewrites of the page's own path, and it validates what comes back the same way
// the feed rungs do: parsed, and rejected unless it actually contains a list.
//
// **The probe is the cheap half of the answer.** It costs a handful of GETs
// against one origin, executes nothing, and either finds an array of entries or
// does not. Compare a headless browser, which would execute the site's
// JavaScript on this machine — outside netguard, since Chromium's network stack
// does not consult Go's transport — to arrive at the same array.

// APIEndpoint is a JSON response that looks like a list of entries.
type APIEndpoint struct {
	URL string
	// Body is the response, kept so the caller does not fetch it twice: the
	// analyser needs it to describe the shape, and the first poll needs it to
	// produce items.
	Body []byte
	// ItemsPath is where the array was found, dotted — "comic.chapters". A
	// starting point for the model rather than the answer: it is the biggest
	// array of objects in the document, which is usually but not always the one
	// a reader wants.
	ItemsPath string
	// Found is how long that array is.
	Found int
}

// maxJSONBytes bounds one API response. Large by page standards because a
// chapter list with 170 entries is legitimately a few hundred KB, and small
// enough that a paginated dump of everything a site has is refused.
const maxJSONBytes = 4 << 20

// JSONEndpoint looks for the data behind a client-rendered page.
//
// The candidates are the rewrites that cover the frameworks people actually
// deploy: an /api prefix (Laravel, Rails, Express), a .json suffix (Next.js data
// routes, some static generators), and /api/v1. Six requests at most, all to the
// origin the reader typed, none of them executing anything.
//
// Returns nil when nothing convincing answers. That is a normal outcome and not
// an error: most client-rendered sites are, from here, simply unfollowable.
func (f *Fetcher) JSONEndpoint(ctx context.Context, pageURL string) *APIEndpoint {
	for _, candidate := range apiCandidates(pageURL) {
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		body, final, ctype, err := f.get(ctx, candidate,
			"application/json, text/javascript;q=0.9, */*;q=0.5")
		cancel()
		if err != nil || len(body) == 0 || len(body) > maxJSONBytes {
			continue
		}
		// The content type is checked but not trusted alone: what settles it is
		// whether the bytes parse and contain a list.
		if !strings.Contains(strings.ToLower(ctype), "json") {
			continue
		}
		var root any
		if err := json.Unmarshal(body, &root); err != nil {
			continue
		}
		path, n := largestObjectArray(root, nil)
		// Two, for the reason the HTML side uses two: one entry is a singleton
		// that proves nothing about a list.
		if n < 2 {
			continue
		}
		return &APIEndpoint{URL: final, Body: body, ItemsPath: strings.Join(path, "."), Found: n}
	}
	return nil
}

// apiCandidates rewrites a page URL into the addresses its data usually lives at.
func apiCandidates(pageURL string) []string {
	u, err := url.Parse(pageURL)
	if err != nil || u.Host == "" {
		return nil
	}
	root := &url.URL{Scheme: u.Scheme, Host: u.Host}
	path := strings.TrimSuffix(u.Path, "/")
	if path == "" {
		path = "/"
	}
	q := ""
	if u.RawQuery != "" {
		q = "?" + u.RawQuery
	}

	var out []string
	add := func(s string) {
		for _, have := range out {
			if have == s {
				return
			}
		}
		out = append(out, s)
	}
	add(root.String() + "/api" + path + q)
	add(root.String() + path + ".json" + q)
	add(root.String() + "/api/v1" + path + q)
	// The section index, for a page that is one level deep: /comics/x → /api/comics.
	if i := strings.LastIndexByte(path, '/'); i > 0 {
		add(root.String() + "/api" + path[:i] + q)
	}
	return out
}

// largestObjectArray finds the longest array of objects in a document, and where
// it is.
//
// "Longest" is a heuristic and it is only a starting point — the model is shown
// the shape and picks — but it is the right heuristic: an API response wrapping
// a list of entries also carries arrays of tags, genres and related items, and
// the one the reader means is essentially always the longest.
//
// Depth-limited: a deeply nested document is a document whose interesting array
// is near the top, and walking forever on a pathological one is how a probe
// becomes a hang.
func largestObjectArray(node any, path []string) ([]string, int) {
	if len(path) > 6 {
		return nil, 0
	}
	bestPath, best := []string(nil), 0
	switch v := node.(type) {
	case []any:
		objects := 0
		for _, e := range v {
			if _, ok := e.(map[string]any); ok {
				objects++
			}
		}
		if objects > best {
			bestPath, best = append([]string(nil), path...), objects
		}
		// An array of arrays is rare and cheap to look through.
		for i, e := range v {
			if i > 3 {
				break
			}
			if p, n := largestObjectArray(e, path); n > best {
				bestPath, best = p, n
			}
		}
	case map[string]any:
		for k, e := range v {
			if p, n := largestObjectArray(e, append(path, k)); n > best {
				bestPath, best = p, n
			}
		}
	}
	return bestPath, best
}

// JSON fetches one API response for a rule that already knows its address.
//
// Separate from JSONEndpoint, which is the search. This is the poll: the address
// was decided when the reader subscribed, and re-probing every hour would be
// four wasted requests against somebody's server to rediscover what is already
// stored.
func (f *Fetcher) JSON(ctx context.Context, dataURL string) ([]byte, error) {
	body, _, ctype, err := f.get(ctx, dataURL,
		"application/json, text/javascript;q=0.9, */*;q=0.5")
	if err != nil {
		return nil, err
	}
	if len(body) > maxJSONBytes {
		return nil, ErrTooLarge
	}
	// The type is checked but the bytes decide, exactly as in the probe: an API
	// that answers with `text/plain` and valid JSON is common, and refusing it
	// on a header would refuse a working source.
	if !strings.Contains(strings.ToLower(ctype), "json") && !looksJSON(body) {
		return nil, ErrNotJSON
	}
	return body, nil
}

// looksJSON is the cheapest possible check: the first non-space byte.
func looksJSON(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}
