package data

import (
	"strings"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The escalation ladder (plan.md A47, §18.8b) as the client sees it: which
// rungs this instance can climb, how a JEV change goes on the wire, and who
// chose an article's category.
//
// Untagged, unlike smart.go, so all of it is tested natively: these are the
// rules a settings screen and a category chip read, and a rule that can only be
// tested in a browser is one that is not tested.

// The three JEV routes, in the order the Smart+ tab offers them. The same
// names as internal/llm.DeciderHosts — written out rather than imported,
// because that package brings SchemaFlux into a wasm bundle that is ratcheted
// (R4). TestJEVRoutesMatchTheLadder holds the two lists together.
const (
	JEVRouteDirect     = "jev"
	JEVRouteOpenRouter = "jev-openrouter"
	JEVRouteLiteLLM    = "jev-litellm"
)

// JEVRoutes is every route, in display order.
var JEVRoutes = []string{JEVRouteDirect, JEVRouteOpenRouter, JEVRouteLiteLLM}

// JEVRouteNeedsBaseURL reports whether a route is the operator's own proxy,
// which has no fixed address and so needs one before it can be used.
func JEVRouteNeedsBaseURL(route string) bool { return route == JEVRouteLiteLLM }

// Rungs is which rungs of the ladder are available on an instance right now.
//
// Code is always true and is here so a screen listing three rungs reads three
// answers from one place rather than hardcoding the first. Decider is JEV with
// a key AND a complete route (the server's jev_ready); Model is the OpenAI
// key's presence. Presence, not proof, on both paid rungs — proving a key works
// costs a call.
type Rungs struct {
	Code, Decider, Model bool
}

// LadderRungs reads the rungs off a Smart+ config. A nil config — not loaded,
// or a member's, who is refused it — has the code rung alone, which is the
// honest floor rather than a guess.
func LadderRungs(cfg *pb.GetSmartConfigResponse) Rungs {
	return Rungs{Code: true, Decider: cfg.GetJevReady(), Model: cfg.GetConfigured()}
}

// JEVChange is one save of the JEV half of the Smart+ config. The zero value
// changes nothing.
type JEVChange struct {
	// Key is a new credential; "" leaves the stored one alone. Clear removes
	// it, and wins over Key — "delete it" is never inferred from an empty field.
	Key   string
	Clear bool
	// Route and BaseURL are nil to leave alone. A BaseURL of "" removes it.
	Route   *string
	BaseURL *string
}

// Request is the change on the wire: the JEV fields and no others, so a JEV
// save cannot touch the OpenAI key, the model or either consent.
func (c JEVChange) Request() *pb.SetSmartConfigRequest {
	req := &pb.SetSmartConfigRequest{}
	if c.Clear {
		req.ClearJevApiKey = true
	} else {
		req.JevApiKey = strings.TrimSpace(c.Key)
	}
	if c.Route != nil {
		v := strings.TrimSpace(*c.Route)
		req.JevProvider = &v
	}
	if c.BaseURL != nil {
		v := strings.TrimSpace(*c.BaseURL)
		req.JevBaseUrl = &v
	}
	return req
}

// CachedTranslationRequest is a cache-only article translation lookup: the one
// TranslateArticle request that cannot reach the provider (see
// TranslateArticleRequest.cache_only). Built here, in one place, so no caller
// can ask "is there one cached?" with a request that would buy one if not.
func CachedTranslationRequest(itemID, locale string) *pb.TranslateArticleRequest {
	return &pb.TranslateArticleRequest{ItemId: itemID, Locale: locale, CacheOnly: true}
}

// CachedTranslationHit reports whether a cache-only answer carries a
// translation. A server that predates cache_only would have translated (or
// refused); one that has it says not_cached on a miss — and either way an
// answer with no body is not one to show.
func CachedTranslationHit(res *pb.TranslateArticleResponse) bool {
	return res != nil && !res.GetNotCached() && strings.TrimSpace(res.GetContentHtml()) != ""
}

// Who placed an article in its category (A47), as the chip says it.
const (
	CategoryByTerms   = ""      // the free-tier lexicon: these terms earned it
	CategoryByDecider = "jev"   // JEV picked it from the shipped labels
	CategoryByModel   = "model" // the model's own read
)

// CategoryChooser is who chose an item's category: CategoryByTerms when the
// lexicon did, CategoryByDecider for JEV, CategoryByModel for the model —
// including a verdict stored before the rung was recorded, which the server
// sends as category_by_model with no rung, and which was the model's then.
func CategoryChooser(it *pb.Item) string {
	if !it.GetCategoryByModel() {
		return CategoryByTerms
	}
	if it.GetCategoryRung() == CategoryByDecider {
		return CategoryByDecider
	}
	return CategoryByModel
}
