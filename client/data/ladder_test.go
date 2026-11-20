package data

import (
	"testing"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

func TestLadderRungsReadsEachCredentialCombination(t *testing.T) {
	cases := []struct {
		name string
		cfg  *pb.GetSmartConfigResponse
		want Rungs
	}{
		{"not loaded", nil, Rungs{Code: true}},
		{"no keys", &pb.GetSmartConfigResponse{}, Rungs{Code: true}},
		{"OpenAI only", &pb.GetSmartConfigResponse{Configured: true}, Rungs{Code: true, Model: true}},
		{"JEV only", &pb.GetSmartConfigResponse{JevConfigured: true, JevReady: true}, Rungs{Code: true, Decider: true}},
		{"both", &pb.GetSmartConfigResponse{Configured: true, JevConfigured: true, JevReady: true},
			Rungs{Code: true, Decider: true, Model: true}},
		// A key on an incomplete route (LiteLLM with no address) is not a rung:
		// the ladder skips it, so the screen must not claim it.
		{"JEV key, route incomplete", &pb.GetSmartConfigResponse{Configured: true, JevConfigured: true},
			Rungs{Code: true, Model: true}},
	}
	for _, c := range cases {
		if got := LadderRungs(c.cfg); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestJEVChangeTouchesOnlyWhatItSays(t *testing.T) {
	if req := (JEVChange{}).Request(); req.GetJevApiKey() != "" || req.GetClearJevApiKey() ||
		req.JevProvider != nil || req.JevBaseUrl != nil {
		t.Errorf("the zero change sent something: %v", req)
	}
	// Never the OpenAI half, whatever the JEV half carries.
	route, base := JEVRouteLiteLLM, " https://proxy.example.com "
	req := JEVChange{Key: " tsk_abc ", Route: &route, BaseURL: &base}.Request()
	if req.GetOpenaiApiKey() != "" || req.GetClearApiKey() || req.GetModel() != "" ||
		req.ClassifyEnabled != nil || req.TranslateArticlesEnabled != nil || req.BudgetUsd != nil {
		t.Errorf("a JEV save carried OpenAI-side fields: %v", req)
	}
	if req.GetJevApiKey() != "tsk_abc" || req.GetJevProvider() != route || req.GetJevBaseUrl() != "https://proxy.example.com" {
		t.Errorf("fields: %v", req)
	}
	// Clear wins, and the key is not sent beside it.
	req = JEVChange{Key: "tsk_abc", Clear: true}.Request()
	if !req.GetClearJevApiKey() || req.GetJevApiKey() != "" {
		t.Errorf("clear: %v", req)
	}
	// An empty base URL is a removal, and is sent as one.
	empty := ""
	if req = (JEVChange{BaseURL: &empty}).Request(); req.JevBaseUrl == nil || req.GetJevBaseUrl() != "" {
		t.Errorf("remove base: %v", req)
	}
}

func TestOnlyLiteLLMNeedsABaseURL(t *testing.T) {
	for _, r := range JEVRoutes {
		if JEVRouteNeedsBaseURL(r) != (r == JEVRouteLiteLLM) {
			t.Errorf("%s: needs base = %v", r, JEVRouteNeedsBaseURL(r))
		}
	}
}

// The lookup is the one request that cannot spend, so its shape is pinned.
func TestCachedTranslationRequestIsCacheOnly(t *testing.T) {
	req := CachedTranslationRequest("i1", "fr")
	if !req.GetCacheOnly() || req.GetForce() || req.GetItemId() != "i1" || req.GetLocale() != "fr" {
		t.Errorf("request: %v", req)
	}
	for _, c := range []struct {
		res  *pb.TranslateArticleResponse
		want bool
	}{
		{nil, false},
		{&pb.TranslateArticleResponse{NotCached: true}, false},
		{&pb.TranslateArticleResponse{Cached: true}, false}, // no body: nothing to show
		{&pb.TranslateArticleResponse{Cached: true, ContentHtml: "<p>Bonjour</p>"}, true},
	} {
		if got := CachedTranslationHit(c.res); got != c.want {
			t.Errorf("%v: hit = %v, want %v", c.res, got, c.want)
		}
	}
}

func TestCategoryChooser(t *testing.T) {
	for _, c := range []struct {
		it   *pb.Item
		want string
	}{
		{&pb.Item{}, CategoryByTerms},
		{&pb.Item{Category: "security"}, CategoryByTerms},
		{&pb.Item{Category: "business", CategoryByModel: true, CategoryRung: "jev"}, CategoryByDecider},
		{&pb.Item{Category: "science", CategoryByModel: true, CategoryRung: "model"}, CategoryByModel},
		// Stored before 0035 recorded the rung: the model's, as it was then.
		{&pb.Item{Category: "science", CategoryByModel: true}, CategoryByModel},
		// A rung without the bool is not a verdict; the bool is the contract.
		{&pb.Item{Category: "security", CategoryRung: "jev"}, CategoryByTerms},
	} {
		if got := CategoryChooser(c.it); got != c.want {
			t.Errorf("%v: %q, want %q", c.it, got, c.want)
		}
	}
}

func TestModelRequestResetsOnlyWhenBlank(t *testing.T) {
	if req := ModelRequest("  gpt-x "); req.GetModel() != "gpt-x" || req.GetResetModel() {
		t.Errorf("a named model: %v", req)
	}
	for _, blank := range []string{"", "   "} {
		req := ModelRequest(blank)
		if !req.GetResetModel() || req.GetModel() != "" {
			t.Errorf("blank %q: %v", blank, req)
		}
		if req.GetOpenaiApiKey() != "" || req.GetClearApiKey() || req.ClassifyEnabled != nil ||
			req.TranslateArticlesEnabled != nil || req.BudgetUsd != nil {
			t.Errorf("a model save carried other fields: %v", req)
		}
	}
}
