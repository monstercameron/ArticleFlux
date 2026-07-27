package derive

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/signals"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// fakePlus is a deterministic Enhancer. No network, no key, no cost.
//
// The whole point of derive.Enhancer being an interface is that the interest layer's
// behaviour WITH Smart+ is testable without a provider — including the failure paths,
// which are the ones that matter most here and which a live provider cannot be relied on
// to produce on demand.
type fakePlus struct {
	// rerank returns these indexes. A nil slice with no error means "no usable ids".
	rerank []int
	// rerankErr, entityErr and labelErr simulate a model that is down, unconfigured or
	// rate-limited — all of which must leave the free-tier answer intact.
	rerankErr error
	entities  []NamedEntity
	entityErr error
	label     string
	labelErr  error

	rerankCalls, entityCalls, labelCalls int
	// sawProfile records what was handed over, so a test can assert the egress shape.
	sawProfile ProfileHint
	sawTitles  []string
}

func (f *fakePlus) RerankCandidates(_ context.Context, cands []Candidate, prof ProfileHint, _ int) ([]int, error) {
	f.rerankCalls++
	f.sawProfile = prof
	if f.rerankErr != nil {
		return nil, f.rerankErr
	}
	return f.rerank, nil
}

func (f *fakePlus) ExtractEntities(_ context.Context, titles []string) ([]NamedEntity, error) {
	f.entityCalls++
	f.sawTitles = titles
	if f.entityErr != nil {
		return nil, f.entityErr
	}
	return f.entities, nil
}

func (f *fakePlus) LabelTopic(_ context.Context, _ []string, fallback string) (string, error) {
	f.labelCalls++
	if f.labelErr != nil {
		return "", f.labelErr
	}
	if f.label == "" {
		return fallback, nil
	}
	return f.label, nil
}

// withSmartPlus wires the enhancer AND records the reader's consent.
//
// Both, because they are deliberately separate: WithSmartPlus is capability and
// SmartPlusPrefKey is permission. Every one of these tests failed the moment the
// preference gate landed — wired but not opted in, so no call was made — which is the gate
// working. Doing it in one helper keeps the tests honest without letting anyone forget that
// an instance which merely CAN call a paid API is not one that may.
func (f *fixture) withSmartPlus(t *testing.T, plus Enhancer) {
	t.Helper()
	f.svc.WithSmartPlus(plus)
	if err := f.repo.SetPrefs(f.ctx, f.scope, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
}

// A wired enhancer with no opt-in must make ZERO calls.
//
// This is the test that matters most in this file, because what it protects is not
// correctness — it is the reader's money. The first version of Smart+ attached the enhancer
// in internal/app and called it on every derivation, which fires after every poll and every
// batch of engagements: an instance with a key in its environment was buying model calls on
// a schedule nobody agreed to.
//
// Capability and consent are separate on purpose. WithSmartPlus says the instance CAN;
// SmartPlusPrefKey says this reader said yes.
func TestSmartPlusMakesNoCallsWithoutConsent(t *testing.T) {
	f := setup(t)
	f.engage(t)

	// Wired, deliberately WITHOUT the preference — note this is svc.WithSmartPlus and not
	// the withSmartPlus helper, which grants consent as well.
	plus := &fakePlus{rerank: []int{3, 2}, entities: []NamedEntity{{Name: "npm", Label: "npm"}}}
	f.svc.WithSmartPlus(plus)

	res, err := f.svc.RunReporting(f.ctx, f.scope, now)
	if err != nil {
		t.Fatalf("RunReporting: %v", err)
	}
	if res.SmartPlus {
		t.Error("the derivation reported Smart+ active without an opt-in")
	}
	if plus.rerankCalls != 0 || plus.entityCalls != 0 || plus.labelCalls != 0 {
		t.Errorf("a paid API was called without consent: rerank=%d entities=%d labels=%d",
			plus.rerankCalls, plus.entityCalls, plus.labelCalls)
	}
	// And nothing is labelled as paid-for, because nothing was.
	for i, r := range mustRanked(t, f) {
		if r.Tier != store.TierSmart {
			t.Errorf("row %d is tiered %q with Smart+ off, want %q", i, r.Tier, store.TierSmart)
		}
	}

	// Turning it on takes effect on the NEXT derivation, with no restart. A reader who
	// switches it off because of the bill must be believed immediately, and the same
	// mechanism has to work in both directions.
	if err := f.repo.SetPrefs(f.ctx, f.scope, store.Prefs{SmartPlusPrefKey: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	res, err = f.svc.RunReporting(f.ctx, f.scope, now)
	if err != nil {
		t.Fatalf("RunReporting after opt-in: %v", err)
	}
	if !res.SmartPlus {
		t.Fatal("the derivation did not report Smart+ active after an opt-in")
	}
	if plus.rerankCalls == 0 {
		t.Error("opting in did not reach the enhancer")
	}

	// And off again, in the same process, without a restart.
	if err := f.repo.SetPrefs(f.ctx, f.scope, store.Prefs{SmartPlusPrefKey: "false"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	before := plus.rerankCalls
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting after opt-out: %v", err)
	}
	if plus.rerankCalls != before {
		t.Errorf("opting out did not stop the calls: %d then %d", before, plus.rerankCalls)
	}
}

func mustRanked(t *testing.T, f *fixture) []store.RankedItem {
	t.Helper()
	got, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	return got
}

// The property everything else here depends on: Smart+ failing must not degrade the page.
//
// A missing key, a timeout, a rate limit and a malformed reply are ORDINARY on a
// self-hosted instance, and the reader must get free Smart's complete ranking in every one
// of those cases. This is the test that would catch a refactor turning a declined call into
// a failed derivation.
func TestSmartPlusFailureLeavesTheFreeRankingIntact(t *testing.T) {
	for name, plus := range map[string]*fakePlus{
		"every call errors": {
			rerankErr: errors.New("no API key"),
			entityErr: errors.New("no API key"),
			labelErr:  errors.New("no API key"),
		},
		"reranker returns nothing usable":   {rerank: nil},
		"reranker invents out-of-range ids": {rerank: []int{999, -3, 100000}},
		"reranker repeats one id":           {rerank: []int{0, 0, 0, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			// One fixture, derived twice. The first version of this test used two
			// fixtures and compared item ids across them, which cannot ever match: each
			// fixture has its own database and its own generated ids. Comparing the same
			// account before and after is the only comparison that means anything.
			f := setup(t)
			f.engage(t)
			if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
				t.Fatalf("free run: %v", err)
			}
			free, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
			if err != nil {
				t.Fatalf("HomeRanking: %v", err)
			}
			if len(free) == 0 {
				t.Fatal("the free tier produced no ranking, so this test proves nothing")
			}

			f.withSmartPlus(t, plus)
			if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
				t.Fatalf("run with Smart+: %v", err)
			}
			got, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
			if err != nil {
				t.Fatalf("HomeRanking: %v", err)
			}
			if len(got) != len(free) {
				t.Fatalf("ranking length changed: %d vs %d", len(got), len(free))
			}
			for i := range got {
				if got[i].ItemID != free[i].ItemID {
					t.Fatalf("row %d changed (%s vs %s) — a declined Smart+ call reordered the page",
						i, got[i].ItemID, free[i].ItemID)
				}
				if got[i].Tier != store.TierSmart {
					t.Errorf("row %d is tiered %q after a declined call, want %q",
						i, got[i].Tier, store.TierSmart)
				}
			}
		})
	}
}

// A successful re-rank promotes what the model chose, and marks only those rows.
func TestSmartPlusPromotesItsPicks(t *testing.T) {
	f := setup(t)
	f.engage(t)

	// Promote the fourth and third candidates, in that order.
	//
	// Both must MOVE. An earlier version used {2, 1}, which puts the old index 1 back at
	// position 1 — so that row was correctly left marked `smart`, and the test was wrong
	// rather than the code. See TestSmartPlusAgreementIsNotMarkedPaid for why that
	// distinction is deliberate.
	plus := &fakePlus{rerank: []int{3, 2}}
	f.withSmartPlus(t, plus)
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}
	if plus.rerankCalls != 1 {
		t.Fatalf("rerank was called %d times, want exactly 1 per derivation", plus.rerankCalls)
	}

	got, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("need at least 3 ranked items, got %d", len(got))
	}
	// The two promoted rows lead, and they are marked as the paid tier because the model
	// changed where they sat.
	for i := 0; i < 2; i++ {
		if got[i].Tier != store.TierSmartPlus {
			t.Errorf("row %d is tiered %q, want %q — a promoted row must say what promoted it",
				i, got[i].Tier, store.TierSmartPlus)
		}
	}
	// Everything below keeps the free tier's marking: Smart+ had no opinion about it.
	for i := 2; i < len(got); i++ {
		if got[i].Tier != store.TierSmart {
			t.Errorf("row %d is tiered %q, want %q", i, got[i].Tier, store.TierSmart)
		}
	}
	// Ranks are still dense and ordered — the reorder must not leave a gap, because
	// RankedItems pages on `rank > ?`.
	for i, r := range got {
		if r.Rank != i+1 {
			t.Fatalf("row %d has rank %d, want %d", i, r.Rank, i+1)
		}
	}
}

// A model that agrees with free Smart has not influenced anything, so nothing is marked
// paid.
//
// Without this, any instance with a key would relabel its whole homepage as Smart+ and the
// tier would stop meaning anything — which matters because the reader is being shown that
// mark as evidence of what they are paying for.
func TestSmartPlusAgreementIsNotMarkedPaid(t *testing.T) {
	f := setup(t)
	f.engage(t)

	// The identity permutation: exactly the free-tier order.
	f.withSmartPlus(t, &fakePlus{rerank: []int{0, 1, 2, 3}})
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}
	got, err := f.repo.HomeRanking(f.ctx, f.scope, 200)
	if err != nil {
		t.Fatalf("HomeRanking: %v", err)
	}
	for i, r := range got {
		if r.Tier != store.TierSmart {
			t.Errorf("row %d is tiered %q after the model agreed with free Smart, want %q",
				i, r.Tier, store.TierSmart)
		}
	}
}

// Only titles and a derived profile are handed over — never bodies, URLs or a log.
//
// derive cannot enforce §18.8 (the typed payloads in internal/llm do that), but it can be
// held to handing over nothing more than the boundary permits. This is the test that
// notices a future edit adding the article body "just for the re-rank".
func TestSmartPlusReceivesOnlyWhatTheBoundaryPermits(t *testing.T) {
	f := setup(t)
	f.engage(t)

	plus := &fakePlus{rerank: []int{0}}
	f.withSmartPlus(t, plus)
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	// Source titles, and they must be titles rather than URLs: a feed URL is a stable
	// identifier that would let a provider recognise this instance across requests.
	if len(plus.sawProfile.Sources) == 0 {
		t.Error("no source titles were offered; the profile is empty")
	}
	for _, src := range plus.sawProfile.Sources {
		if strings.Contains(src, "://") || strings.Contains(src, ".example") {
			t.Errorf("a source went out as a URL rather than a title: %q", src)
		}
	}
	// Entity extraction sees headlines only. A body would arrive as a much longer string
	// containing markup or the summary text.
	for _, title := range plus.sawTitles {
		if strings.Contains(title, "<") {
			t.Errorf("markup reached the entity extractor: %q", title)
		}
	}
}

// Smart+ names things the capitalised-phrase heuristic structurally cannot see.
//
// A lowercase brand produces no phrase at all, so this is the gap the paid tier exists to
// close for entities — and the mention count still comes from the reader's own engagement,
// not from the model's enthusiasm.
func TestSmartPlusFindsLowercaseBrands(t *testing.T) {
	f := setup(t)
	ids := f.ingestEntityItems(t, map[string]string{
		"n1": "npm audit gets a new resolver",
		"n2": "npm workspaces explained",
	})
	f.record(t, signals.Completed, ids, now.Add(-time.Hour))

	// The heuristic cannot produce "npm": it is one lowercase token.
	f.withSmartPlus(t, &fakePlus{entities: []NamedEntity{{Name: "npm", Label: "npm"}}})
	if _, err := f.svc.RunReporting(f.ctx, f.scope, now); err != nil {
		t.Fatalf("RunReporting: %v", err)
	}

	var found *store.Entity
	for _, e := range mustEntities(t, f) {
		if e.Name == "npm" {
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatal("Smart+ named a lowercase brand and it was not recorded")
	}
	if found.Kind != store.EntityLLM {
		t.Errorf("kind = %q, want %q so removing the key visibly narrows the list",
			found.Kind, store.EntityLLM)
	}
	// Two engaged headlines mention it, and that count comes from the log rather than from
	// the model — a model listing forty brands must not be able to promote any of them.
	if found.Mentions != 2 {
		t.Errorf("mentions = %d, want 2 (counted from engagement, not from the model)",
			found.Mentions)
	}
}
