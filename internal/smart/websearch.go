package smart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// maxWebSearchDomainRunes bounds one returned domain — a defensive cap on
// untrusted model output, not a real-world domain length limit.
const maxWebSearchDomainRunes = 253 // DNS's own ceiling; anything past it is not a domain

// cleanWebSearchDomain normalises a candidate domain, or drops it. The model's
// output is untrusted (WebSearchPayload's own doc): this is a syntactic
// sanity check only — "is this shaped like a bare domain" — never a claim
// that the site exists or has a feed. That claim is discover.Validator's,
// made by an actual fetch, same as every other rung.
func cleanWebSearchDomain(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	if i := strings.IndexAny(s, "/?# "); i >= 0 {
		s = s[:i]
	}
	if s == "" || len([]rune(s)) > maxWebSearchDomainRunes || !strings.Contains(s, ".") {
		return ""
	}
	return strings.ToLower(s)
}

// WebSearchFinder is rung 5's discovery half (Cam, 2026-08-01; plan.md §18.7):
// given the reader's topic terms, ask the model to search the web for real
// sites covering them. It implements recommendjob.WebSearchFinder
// structurally, for the same reason RelevanceChecker implements
// recommendjob.RelevanceChecker structurally — the egress boundary lives
// here, the caller's interface should not have to reach back in.
type WebSearchFinder struct {
	llm      llmClient
	settings *store.SettingsRepo
}

// NewWebSearchFinder wires the finder. c is the llmClient seam (llmclient.go)
// — production passes a *llm.Client, tests pass a fake. s may be nil, in
// which case model() falls back to the provider default.
func NewWebSearchFinder(c llmClient, s *store.SettingsRepo) *WebSearchFinder {
	return &WebSearchFinder{llm: c, settings: s}
}

// model resolves the configured model, or the provider default — see
// RelevanceChecker.model's identical doc for why this reads store.KeySmartModel
// rather than hardcoding the provider default (Cam, 2026-08-01).
func (f *WebSearchFinder) model(ctx context.Context) string {
	if f == nil || f.settings == nil {
		return ""
	}
	m, err := f.settings.SystemValue(ctx, store.KeySmartModel)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(m)
}

// Find asks the model to search the web for sites covering topic, and
// returns cleaned, deduplicated candidate domains mapped to the model's own
// one-line reason for each — recommendjob.WebSearchFinder's doc explains why
// a map rather than a domain list plus a side-channel. Every rule
// llm.WebSearchPayload documents applies: topic is terms only, and the
// domains returned are UNTRUSTED — the caller is responsible for validating
// each one exactly like any other candidate (recommendjob does this via the
// same discover.Validator every other rung uses).
//
// A configuration or provider failure returns (nil, err) rather than a
// partial list — recommendjob.Run treats an error as "search did not run"
// and simply supplies no rung-5 candidates this pass, the same fail-open-to-
// nothing behaviour RelevanceChecker.Check's caller already relies on.
//
// positive/negative are taste-calibration titles (Cam, 2026-08-01) — see
// RelevanceChecker.Check's identical parameter doc. Either or both may be
// nil; the search still runs on topic alone, same as before this parameter
// existed.
func (f *WebSearchFinder) Find(
	ctx context.Context, topic string, positive, negative []string,
) (map[string]string, error) {
	if f == nil || f.llm == nil || !f.llm.Configured(ctx) {
		return nil, fmt.Errorf("smart: no API key")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, fmt.Errorf("smart: no topic terms to search with")
	}

	payload := llm.WebSearchPayload{
		Topic: topic, PositiveExamples: positive, NegativeExamples: negative,
	}
	payload, _ = payload.Trim()

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	call, cancel := context.WithTimeout(ctx, interestTimeout)
	defer cancel()

	out, err := f.llm.Do(call, llm.Request{
		Model:        f.model(ctx),
		Instructions: llm.WebSearchInstructions,
		Input:        string(body),
		SchemaName:   "discover_web_search",
		Schema:       llm.WebSearchSchema,
		Tools:        []string{llm.WebSearchTool},
		// A search-and-synthesize task, not a two-post read — the tool calls
		// themselves cost output-token budget on top of the final answer (see
		// llm.Request.MaxOutputTokens's own doc on this), so this gets more
		// headroom than RelevanceChecker's 600.
		MaxOutputTokens: 2000,
		Effort:          "low",
	})
	if err != nil {
		return nil, err
	}

	var reply llm.WebSearchReply
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		return nil, fmt.Errorf("smart: web search reply was not the schema: %w", err)
	}

	found := make(map[string]string, len(reply.Candidates))
	for _, c := range reply.Candidates {
		d := cleanWebSearchDomain(c.Domain)
		if d == "" {
			continue
		}
		if _, dup := found[d]; dup {
			continue
		}
		found[d] = strings.TrimSpace(c.Reason)
	}
	return found, nil
}
