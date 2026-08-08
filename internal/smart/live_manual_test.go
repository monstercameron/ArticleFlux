package smart

import (
	"context"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/pipeline"
)

// The three calls that stay hand-built, run against the real model.
//
// `llm.Manual` names them and says why each cannot be a typed operation: A10
// sends the Responses API's hosted `tools`, which `schemaflux.CompletionRequest`
// has no field for; A14 composes its schema at RUNTIME from the reader's own
// contributor set, so there is no Go type to derive one from; and `Client.Models`
// carries no prompt at all.
//
// # Why they need live cover as much as the rebuilt ones
//
// They were not rewritten, so the instinct is that they cannot have regressed.
// That instinct is wrong twice over. They still travel through `Client.Do` into
// SchemaFlux's middleware chain — the retry policy, the breaker, the spend
// ceiling and the provider interface are all the library's now — and the
// provider underneath them was renamed to "articleflux" during the migration.
// A hand-built request is only safe from the model-resolution trap that killed
// translation because `Do` sets `req.Model` itself before the chain sees it,
// and "because I read the code" is exactly the evidence that was wrong last
// time.
//
//	AF_LIVE=1 OPENAI_API_KEY=$(...) go test ./internal/smart/ -run Live -v

// --- A14: classification, with the runtime-composed schema --------------------

// The highest-risk of the three, because its schema is assembled per call.
//
// `registry.Build` composes `req.Schema` from whichever contributors are
// enabled, so the shape sent is data rather than a type — nothing checks it at
// compile time, and a malformed one is refused by the API rather than by Go.
// This is the only place that shape meets a real strict-schema endpoint.
func TestLiveClassifyEnrich(t *testing.T) {
	client, settings := liveFixture(t)
	if err := settings.SetSystemValue(context.Background(), KeyClassifyEnabled, "true", ""); err != nil {
		t.Fatalf("seeding consent: %v", err)
	}

	c := NewClassifier(client, settings, smallLexicon(t)).WithContributors("classify")
	out := &pipeline.Analysis{Primary: "unsorted"}
	item := pipeline.Item{
		Title:   "Attackers exploit a VPN appliance zero-day",
		Summary: "A pre-authentication flaw is being used in the wild; a patch shipped Tuesday.",
		Body: "A pre-authentication remote code execution flaw in a widely deployed VPN " +
			"appliance is under active exploitation. The vendor shipped a patch on Tuesday " +
			"and published indicators of compromise.",
	}

	if err := c.Enrich(context.Background(), item, out); err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	t.Logf("primary=%q secondary=%v keyphrases=%v", out.Primary, out.Secondary, out.Keyphrases)

	// The lexicon offers exactly "security" and "software". The contract is
	// membership: a slug outside it is dropped by dropUnknownSlugs, so a reply
	// that named only invented slugs leaves Primary exactly as it arrived — the
	// free tier's answer, which is the silent failure worth catching.
	if out.Primary == "unsorted" {
		t.Errorf("Primary is still the free tier's answer; the model's reply "+
			"contributed nothing (secondary=%v keyphrases=%v)", out.Secondary, out.Keyphrases)
	}
	if out.Primary != "security" {
		t.Errorf("Primary = %q, want security for an actively-exploited VPN zero-day", out.Primary)
	}
}

// --- A10: web-search discovery, with the hosted tool --------------------------

// The one feature a typed operation cannot express at all.
//
// It asks the model to use the Responses API's server-side web_search tool, so
// what comes back depends on the live web as well as the model. That makes it
// the flakiest test here by nature, and the assertions are correspondingly
// loose: what must hold is that the hosted tool still runs and still yields
// fetchable feed candidates, not which sites it names today.
func TestLiveWebSearchFind(t *testing.T) {
	client, settings := liveFixture(t)
	f := NewWebSearchFinder(client, settings)

	got, err := f.Find(context.Background(), "PostgreSQL and database internals",
		[]string{"Databases", "Systems programming"},
		[]string{"Celebrity news"},
	)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	for url, why := range got {
		t.Logf("%s — %s", url, why)
	}

	if len(got) == 0 {
		t.Fatal("the hosted web-search tool returned nothing; either the tool is no " +
			"longer being invoked or the request shape stopped being accepted")
	}
	// DOMAINS, not URLs — that is what Find documents and what
	// recommendjob feeds to discover.Validator. This assertion originally
	// demanded a scheme and failed a perfectly correct answer; the contract is
	// a bare, cleaned hostname.
	for domain := range got {
		if strings.Contains(domain, "://") || strings.Contains(domain, "/") {
			t.Errorf("candidate %q is not a bare domain; Find documents cleaned domains", domain)
		}
		if !strings.Contains(domain, ".") || strings.ContainsAny(domain, " 	") {
			t.Errorf("candidate %q is not a usable hostname", domain)
		}
	}
}

// --- Client.Models ------------------------------------------------------------

// The cheapest call in the application and the only one with no prompt.
//
// It is here because the Smart+ tab's model picker is built from it: an empty
// or filtered-to-nothing list is a settings page offering the reader no choice,
// which fails quietly and looks like a UI bug rather than an API one.
func TestLiveModels(t *testing.T) {
	client, _ := liveFixture(t)

	got, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	t.Logf("%d models offered; first few: %v", len(got), got[:min(5, len(got))])

	if len(got) == 0 {
		t.Fatal("no models came back; the Smart+ tab's picker would be empty")
	}
	// The picker filters to chat-capable models — the embedding, audio and image
	// families are excluded by name (see llm.go). If that filter ever matches
	// everything, the list is empty; if it matches nothing, the list is full of
	// models a completion call cannot use.
	for _, m := range got {
		for _, bad := range []string{"text-embedding", "whisper", "tts-", "dall-e", "gpt-image"} {
			if strings.Contains(m, bad) {
				t.Errorf("%q is not a completion model and should have been filtered out", m)
			}
		}
	}
}
