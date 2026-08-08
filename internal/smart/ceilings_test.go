package smart

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every typed operation in this package states its output ceiling.
//
// # Why this is a source grep rather than a behavioural test
//
// The thing being guarded is an OMISSION, and an omission has no behaviour to
// observe without a paid provider. A call site that forgets `Configure(cap…)`
// runs perfectly against the fake client in `fake_llm_test.go` — the fake never
// consults `MaxOutputTokens` — and against a real one it returns
// `llm.ErrTruncated` only for inputs long enough to reach the ceiling. So the
// failure is invisible in CI by construction: the `*_llm_test.go` files are
// env-gated behind a real key and do not run.
//
// That is exactly how it happened. The SchemaFlux migration dropped ten
// hand-chosen ceilings in one commit, every test stayed green, and the ceilings
// became dead constants nothing read. A grep is a blunt instrument and it is
// the only one that can see this.
//
// # What it accepts
//
// One `Configure(cap…)` within the operation's fluent chain, where the chain is
// the lines from `schemaflux.<Op>(` to the terminating `Run(`. Anything else —
// a bare tier, a ceiling set two functions away — fails and says which line.
func TestEveryTypedOperationStatesItsCeiling(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// The starts of the fluent chains this package uses. Adding an operation
	// means adding it here, which is the point: a new call site should have to
	// think about its ceiling once.
	starts := []string{
		"schemaflux.Extracting[",
		"schemaflux.Generating[",
		"schemaflux.Choosing[",
		"schemaflux.Summarizing(",
	}

	found := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if !startsAChain(line, starts) {
				continue
			}
			found++
			capped := false
			// The chain ends at Run. A generous window rather than an exact
			// one: these chains carry long comments, and a limit tight enough
			// to be precise would be a limit somebody has to keep raising.
			for j := i; j < len(lines) && j < i+40; j++ {
				if strings.Contains(lines[j], "Configure(cap") {
					capped = true
				}
				if strings.Contains(lines[j], ".Run(") {
					break
				}
			}
			if !capped {
				t.Errorf("%s:%d: this operation names no output ceiling, so it inherits "+
					"SchemaFlux's tier default (2000 for Fast, 4000 for Smart). A ceiling "+
					"below what the feature emits is llm.ErrTruncated, and nothing in the "+
					"unpaid suite can see it. Add Configure(cap…) — see ceilings.go.\n\t%s",
					name, i+1, strings.TrimSpace(lines[i]))
			}
		}
	}
	// A grep that matches nothing passes silently, which would make this test
	// worse than absent — it would read as coverage. The count is asserted
	// loosely: the exact number changes as features land, zero never should.
	if found < 10 {
		t.Errorf("found only %d typed operations; the matcher has stopped matching", found)
	}
}

func startsAChain(line string, starts []string) bool {
	for _, s := range starts {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// The two ceilings that carry named constants elsewhere must still be the
// numbers their prose argues for. A tidy-up that "simplified" either to a tier
// default would reintroduce the gap while looking like a cleanup.
func TestTheRestoredCeilingsAreTheHandChosenNumbers(t *testing.T) {
	if digestMaxTokens != 4000 {
		t.Errorf("digestMaxTokens = %d, want 4000 — see its own comment on why", digestMaxTokens)
	}
	if podcastMaxTokens != 4200 {
		t.Errorf("podcastMaxTokens = %d, want 4200", podcastMaxTokens)
	}
	if translateMaxTokens != 8000 {
		t.Errorf("translateMaxTokens = %d, want 8000 — a batch is sixty strings", translateMaxTokens)
	}
	if analyzeMaxTokensSF != analyzeMaxTokens {
		t.Errorf("the scrape ceiling drifted from analyzeMaxTokens (%d vs %d)",
			analyzeMaxTokensSF, analyzeMaxTokens)
	}
	// Nothing is below SchemaFlux's own Fast default, which is the floor the
	// max(hand-chosen, tier) rule guarantees.
	const fastDefault = 2000
	for name, n := range map[string]int{
		"categorize": categorizeMaxTokens, "invent": inventMaxTokens,
		"rerank": rerankMaxTokens, "entity": entityMaxTokens,
		"topicLabel": topicLabelMaxTokens, "relevance": relevanceMaxTokens,
		"palette": paletteMaxTokens, "digest": digestMaxTokens,
		"translate": translateMaxTokens, "analyze": analyzeMaxTokensSF,
	} {
		if n < fastDefault {
			t.Errorf("%s ceiling is %d, below the %d a Fast operation gets for free — "+
				"lowering a ceiling saves nothing and buys ErrTruncated", name, n, fastDefault)
		}
	}
}
