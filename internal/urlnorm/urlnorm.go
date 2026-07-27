// Package urlnorm canonicalises URLs.
//
// One canonicaliser, two outputs, because the two questions are genuinely
// different and using one answer for both is a data-loss bug:
//
//   - **Norm** is *bookmark identity*. "Is this the page I already saved?" It is
//     conservative: it strips only what is provably noise, because collapsing two
//     different pages into one silently destroys a bookmark.
//   - **DupeKey** is *cross-source duplicate detection*. "Did three feeds carry
//     the same article?" It is aggressive, and a false merge costs one item in
//     one megafeed rather than someone's saved page.
//
// Norm ⊆ DupeKey in aggressiveness, always.
package urlnorm

import (
	"net/url"
	"sort"
	"strings"
)

// trackingParams are query parameters that never identify a document — they
// identify how you arrived at it. Stripping them is safe for both outputs.
var trackingParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true,
	"utm_term": true, "utm_content": true, "utm_name": true, "utm_id": true,
	"fbclid": true, "gclid": true, "dclid": true, "gbraid": true, "wbraid": true,
	"msclkid": true, "twclid": true, "igshid": true, "yclid": true,
	"mc_cid": true, "mc_eid": true, "_hsenc": true, "_hsmi": true,
	"ref": true, "referrer": true, "source": true,
	"at_medium": true, "at_campaign": true, "ito": true, "cmpid": true,
	"sh": true, "share": true, "s": true,
	"spm": true, "scm": true, "vero_id": true, "vero_conv": true,
	"__s": true, "oly_enc_id": true, "oly_anon_id": true,
	"campaign_id": true, "ck_subscriber_id": true,
}

// defaultPorts are dropped because :443 on https is the same host as no port.
var defaultPorts = map[string]string{"http": "80", "https": "443"}

// Norm canonicalises for bookmark identity. Unique per user (§6.2), so two saves
// of the same article from different places collapse to one row.
//
// What it does: lowercase scheme and host, drop the default port, drop the
// fragment, strip tracking parameters, sort the remaining query, and remove a
// trailing slash from a non-root path.
//
// What it deliberately does NOT do:
//
//   - strip "www." — www.example.com and example.com are usually the same site,
//     but not always, and a bookmark is the user's data.
//   - upgrade http to https — some hosts genuinely differ, and silently changing
//     someone's saved scheme is a lie about what they saved.
//   - drop unknown query parameters — ?id=42 and ?p=7 usually identify the page.
//
// An unparseable input is returned trimmed rather than as an error: a bookmark
// with an odd URL is still a bookmark.
func Norm(raw string) string {
	u, ok := parse(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	stripQuery(u, nil)
	trimTrailingSlash(u)
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// ItemKey canonicalises a feed item's link for identity WITHIN its source — the
// second rung of the guid fallback ladder, used when a feed omits <guid>.
//
// It is Norm plus the fragment, and the fragment is the entire reason it exists
// separately. A linkblog that publishes a day's entries as anchors on one page —
// scripting.com/1999/07/08.html#stockMarket, #nodesc, #ents — is the oldest
// shape in this format, and Norm collapses all three onto one key. The corpus
// caught it: three items, one guid, two of them permanently unstorable.
//
// The asymmetry with Norm is deliberate rather than an oversight in one of them.
// Norm serves bookmarks, where a saved page#section really is the saved page.
// Identity serves storage, where merging two items is unrecoverable. When those
// two purposes disagree the answer is two functions, not a compromise that is
// slightly wrong for both.
func ItemKey(raw string) string {
	u, ok := parse(raw)
	if !ok {
		return strings.TrimSpace(raw)
	}
	stripQuery(u, nil)
	frag, rawFrag := u.Fragment, u.RawFragment
	trimTrailingSlash(u)
	u.Fragment, u.RawFragment = frag, rawFrag
	return u.String()
}

// dupeOnly are parameters stripped for duplicate detection but kept for bookmark
// identity — pagination and view state, which change the rendering of a document
// without changing which document it is.
var dupeOnly = map[string]bool{
	"page": true, "p": true, "amp": true, "output": true, "format": true,
	"print": true, "single_page": true, "view": true, "sp": true, "seq": true,
}

// DupeKey produces a cross-source duplicate key (§6.2, §15.3).
//
// On top of Norm it also lowercases the whole path, drops "www.", collapses
// http/https to a single scheme, strips AMP path suffixes, and removes the
// pagination parameters above — the transforms that are right for "three feeds
// carried the same article" and wrong for "this is the page I saved".
//
// The scheme is dropped entirely rather than normalised to https, because the
// key's only job is equality.
func DupeKey(raw string) string {
	u, ok := parse(raw)
	if !ok {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	stripQuery(u, dupeOnly)
	// The fragment goes, unlike in ItemKey. Across sources, #section-2, #top and
	// #:~:text=... are three ways of pointing at one article, and collapsing them
	// is the entire job. Within a source the opposite is true — see ItemKey — and
	// the reason those can differ safely is that dedup must never merge two items
	// from the SAME source: those already have distinct guids, and a suppression
	// pass that ignored that would eat a linkblog's whole day.
	u.Fragment = ""
	u.RawFragment = ""
	u.Host = strings.TrimPrefix(u.Host, "www.")
	u.Path = strings.ToLower(u.Path)

	// AMP variants are the same article at a different address.
	for _, suffix := range []string{"/amp", "/amp/", ".amp", "/amp.html"} {
		if strings.HasSuffix(u.Path, suffix) {
			u.Path = strings.TrimSuffix(u.Path, suffix)
			break
		}
	}
	trimTrailingSlash(u)

	key := u.Host + u.Path
	if q := u.RawQuery; q != "" {
		key += "?" + q
	}
	return key
}

// Host returns the registrable-ish host: lowercased, no port, no "www.". This is
// what §18.7's outbound-link mining aggregates on, and what a domain allow/deny
// rule matches.
func Host(raw string) string {
	u, ok := parse(raw)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(u.Hostname(), "www.")
}

func parse(raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, false
	}
	// Only web URLs normalise. mailto:, javascript:, data: and friends have no
	// host semantics and must not be reshaped into something that looks like one.
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, false
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if port := u.Port(); port != "" && defaultPorts[u.Scheme] == port {
		u.Host = u.Hostname()
	}
	return u, true
}

// stripQuery removes tracking parameters (plus any extra set), then sorts what
// remains so ?b=2&a=1 and ?a=1&b=2 produce one key.
//
// A parameter with an empty value is kept: ?print and ?print= are meaningfully
// different on enough sites that dropping them would merge distinct pages.
func stripQuery(u *url.URL, extra map[string]bool) {
	if u.RawQuery == "" {
		return
	}
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if trackingParams[lk] || (extra != nil && extra[lk]) {
			q.Del(k)
		}
	}
	if len(q) == 0 {
		u.RawQuery = ""
		return
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		vals := q[k]
		sort.Strings(vals)
		for _, v := range vals {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	u.RawQuery = b.String()
}

// trimTrailingSlash drops a trailing slash from a non-root path. The root is left
// alone: "example.com/" and "example.com" already normalise to the same thing via
// the empty path, and stripping it would produce a hostless-looking key.
func trimTrailingSlash(u *url.URL) {
	if len(u.Path) > 1 && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimRight(u.Path, "/")
		if u.Path == "" {
			u.Path = "/"
		}
	}
	if u.Path == "/" {
		u.Path = ""
	}
	u.RawPath = ""
}
