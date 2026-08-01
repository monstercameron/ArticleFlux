package smart

import (
	"context"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// No key configured is the default on a fresh instance, and the finder must
// refuse rather than send anything — mirrors relevance_test.go's own guard.
func TestWebSearchFinderRefusesWithoutAConfiguredKey(t *testing.T) {
	f := NewWebSearchFinder(&fakeLLM{configured: false}, nil)
	_, err := f.Find(context.Background(), "distributed systems", nil, nil)
	if err == nil {
		t.Fatal("Find succeeded with no API key configured")
	}
}

func TestWebSearchFinderRefusesWithNoTopic(t *testing.T) {
	f := NewWebSearchFinder(&fakeLLM{configured: true}, nil)
	_, err := f.Find(context.Background(), "  ", nil, nil)
	if err == nil {
		t.Fatal("Find succeeded with no topic terms — nothing to search for")
	}
}

// The happy path: candidate domains and reasons are forwarded, cleaned, and
// deduplicated, and — the point of the fake — the actual request is
// inspected to prove the web_search tool was actually requested and the
// egress boundary held (topic only, nothing about the reader).
func TestWebSearchFinderForwardsCandidatesAndRequestsTheTool(t *testing.T) {
	fake := &fakeLLM{configured: true, text: `{"candidates": [
		{"domain": "https://www.Example.com/blog", "reason": "writes about distributed systems"},
		{"domain": "example.com", "reason": "duplicate of the one above once normalised"},
		{"domain": "not a domain", "reason": "should be dropped"},
		{"domain": "another.example", "reason": "a second real one"}
	]}`}
	f := NewWebSearchFinder(fake, nil)

	found, err := f.Find(context.Background(), "distributed systems, database internals", nil, nil)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %v, want exactly 2 domains after cleaning+dedup", found)
	}
	if reason, ok := found["example.com"]; !ok || reason == "" {
		t.Errorf("found[example.com] = %q, ok=%v — want the scheme/www-stripped, deduplicated entry with its reason", reason, ok)
	}
	if _, ok := found["another.example"]; !ok {
		t.Errorf("found = %v, want another.example present", found)
	}

	if fake.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", fake.callCount())
	}
	req := fake.callN(0)

	var sawWebSearch bool
	for _, tool := range req.Tools {
		if tool == "web_search" {
			sawWebSearch = true
		}
	}
	if !sawWebSearch {
		t.Errorf("Request.Tools = %v, want the web_search tool requested", req.Tools)
	}

	bad, err := llm.AuditWebSearch([]byte(req.Input))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("the web-search request body carried keys §18.8 does not permit: %v", bad)
	}
}

// cleanWebSearchDomain's own edge cases, table-driven per this package's
// existing style.
func TestCleanWebSearchDomain(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"bare domain", "example.com", "example.com"},
		{"scheme and path", "https://example.com/blog/post", "example.com"},
		{"www prefix", "http://www.Example.COM", "example.com"},
		{"query string", "example.com?utm=1", "example.com"},
		{"empty", "", ""},
		{"not a domain", "not a domain at all", ""},
		{"whitespace only", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanWebSearchDomain(c.in); got != c.want {
				t.Errorf("cleanWebSearchDomain(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The model picker was being ignored here too (Cam, 2026-08-01) — mirrors
// TestRelevanceCheckerModelReadsTheConfiguredSetting.
func TestWebSearchFinderModelReadsTheConfiguredSetting(t *testing.T) {
	settings := newSettings(t)
	if err := settings.SetSystemValue(context.Background(), store.KeySmartModel, "gpt-5.6-luna", ""); err != nil {
		t.Fatalf("seeding the model setting: %v", err)
	}
	fake := &fakeLLM{configured: true, text: `{"candidates":[]}`}
	f := NewWebSearchFinder(fake, settings)
	if _, err := f.Find(context.Background(), "distributed systems", nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fake.calls))
	}
	if got := fake.calls[0].Model; got != "gpt-5.6-luna" {
		t.Errorf("Request.Model = %q, want the configured model to be forwarded verbatim", got)
	}
}
