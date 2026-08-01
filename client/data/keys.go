package data

import (
	"strings"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The cache key vocabulary (TODO 8.9).
//
// # Why this is a file and not three lines next to the cache
//
// A cache key is not a label — it is the address that invalidation is aimed at.
// Written inline, keys drift: one site stores under `"items:"+scope` and another
// drops `"item:"+scope`, both read correctly, and the only symptom is a list
// that does not refresh after a write. Nothing errors, nothing logs, and the
// reader learns to reload the page.
//
// These lived in `cache_wasm.go`, which meant they were unexported, behind
// `//go:build js`, and therefore untestable natively and unreachable from the
// view. The cache is what needs a build tag; the naming scheme is arithmetic on
// strings, and it belongs where it can be read and tested.
//
// # The prefix hierarchy is the design
//
//	feeds                       the rail, with its unread counts
//	items:<scope>[:filters]     one list
//	item:<id>                   one article's body
//	tags                        the tag list
//
// Colon-separated with a fixed field order, so `DropPrefix("items:")` reaches
// every list and nothing else. An order that varied — a map, a struct printed
// with %v — would produce two keys for one list, and a cache with two keys for
// one answer never hits.

// Cache key prefixes. Exported so invalidation can name a family rather than
// enumerate it.
const (
	// KeyPrefixItems addresses every list.
	KeyPrefixItems = "items:"
	// KeyPrefixItem addresses every article body.
	KeyPrefixItem = "item:"
)

// KeyFeeds is the rail.
const KeyFeeds = "feeds"

// KeyTags is the tag list.
const KeyTags = "tags"

// KeyItem addresses one article's body, which list responses deliberately omit
// so that a fifty-item page is not megabytes of unread text.
func KeyItem(id string) string { return KeyPrefixItem + id }

// KeyItems addresses one list.
//
// Returns "" for a request that must not be cached, and the CALLER checks —
// see below for what makes a request uncacheable. Returning a key that then
// nobody stores under would be worse than returning nothing: it reads as
// cacheable at every call site.
//
// Every field that changes which items come back is in the key, and
// `TestEveryListFilterIsInTheKey` enforces that rather than asking for it:
// adding a filter to `ListItemsRequest` without adding it here produces a cache
// that serves one filter's answer to another's question, which looks like the
// application showing the wrong articles. It has done exactly that before,
// through a different route (TODO H10), and a comment would not have stopped
// it.
func KeyItems(req *pb.ListItemsRequest) string {
	// A cursor page is not cacheable.
	//
	// The key would have to include the cursor, and a cursor is only meaningful
	// against the exact list that issued it — so a cached page 2 is an answer to
	// a question that can no longer be asked. Caching the FIRST page is what
	// makes "go back to what I was reading" work offline; caching the rest would
	// make an outage produce a list with holes in it.
	if req.GetCursor() != "" {
		return ""
	}

	var b strings.Builder
	b.WriteString(KeyPrefixItems)
	b.WriteString(req.GetScope().String())
	if req.GetUnreadOnly() {
		b.WriteString(":unread")
	}
	if id := req.GetSourceId(); id != "" {
		b.WriteString(":src=")
		b.WriteString(id)
	}
	for _, id := range req.GetSourceIds() {
		b.WriteString(":s=")
		b.WriteString(id)
	}
	// The classification filters. Both narrow the SAME scope value that an
	// unfiltered list uses, so without them here every topic and the
	// uncategorised pile share one cache entry with "all articles" — and the
	// first one opened answers for the rest. That is the failure this whole
	// key exists to prevent, and it is invisible: the heading is right and the
	// articles under it are somebody else's.
	if slug := req.GetCategorySlug(); slug != "" {
		b.WriteString(":cat=")
		b.WriteString(slug)
	}
	// A distinct marker rather than ":cat=" with an empty value, so it cannot
	// collide with a label whose slug is somehow empty.
	if req.GetUncategorised() {
		b.WriteString(":nocat")
	}
	return b.String()
}
