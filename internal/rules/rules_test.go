package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func sample() Item {
	return Item{
		Title:       "Rust 2.0 and the Borrow Checker",
		Author:      "Ada Lovelace",
		Content:     "<p>A long piece about ownership, lifetimes and the borrow checker.</p>",
		URL:         "https://blog.example.com/rust-2-0",
		SourceTitle: "Example Blog",
		FolderName:  "Programming",
		Tags:        []string{"rust", "systems"},
		WordCount:   1200,
		PublishedAt: now.Add(-30 * time.Hour),
		Lang:        "en",
	}
}

// Every field crossed with every operator. The ticket's acceptance bar.
func TestEveryFieldAndOperator(t *testing.T) {
	cases := []struct {
		name string
		cond Condition
		want bool
	}{
		// --- text operators, on title -------------------------------------
		{"contains hit", Condition{FieldTitle, OpContains, "borrow"}, true},
		{"contains miss", Condition{FieldTitle, OpContains, "python"}, false},
		{"contains is case-insensitive", Condition{FieldTitle, OpContains, "BORROW"}, true},
		{"not_contains hit", Condition{FieldTitle, OpNotContains, "python"}, true},
		{"not_contains miss", Condition{FieldTitle, OpNotContains, "rust"}, false},
		{"equals hit", Condition{FieldTitle, OpEquals, "Rust 2.0 and the Borrow Checker"}, true},
		{"equals is case-insensitive", Condition{FieldTitle, OpEquals, "rust 2.0 and the borrow checker"}, true},
		{"equals miss", Condition{FieldTitle, OpEquals, "Rust"}, false},
		{"starts_with hit", Condition{FieldTitle, OpStartsWith, "Rust"}, true},
		{"starts_with miss", Condition{FieldTitle, OpStartsWith, "Borrow"}, false},
		{"regex hit", Condition{FieldTitle, OpRegex, `rust\s+\d\.\d`}, true},
		{"regex miss", Condition{FieldTitle, OpRegex, `^borrow`}, false},
		{"regex is case-insensitive by default", Condition{FieldTitle, OpRegex, `RUST`}, true},
		// An invalid pattern matches nothing rather than everything: the failure
		// of a typo must not be "the whole feed is muted".
		{"invalid regex matches nothing", Condition{FieldTitle, OpRegex, `([unclosed`}, false},

		// --- the other text fields ----------------------------------------
		{"author", Condition{FieldAuthor, OpContains, "lovelace"}, true},
		{"content", Condition{FieldContent, OpContains, "lifetimes"}, true},
		{"url", Condition{FieldURL, OpStartsWith, "https://blog.example.com"}, true},
		{"source", Condition{FieldSource, OpEquals, "Example Blog"}, true},
		{"folder", Condition{FieldFolder, OpContains, "programming"}, true},
		{"lang", Condition{FieldLang, OpEquals, "en"}, true},
		{"lang miss", Condition{FieldLang, OpEquals, "fr"}, false},

		// --- tags: a list, so the semantics differ -------------------------
		{"tag contains hit", Condition{FieldTag, OpContains, "rust"}, true},
		{"tag equals hit", Condition{FieldTag, OpEquals, "systems"}, true},
		{"tag equals miss", Condition{FieldTag, OpEquals, "rus"}, false},
		// "no tag is python", not "some tag is not python" — the second is true
		// of almost every tagged item and would make the operator useless.
		{"tag not_contains, none match", Condition{FieldTag, OpNotContains, "python"}, true},
		{"tag not_contains, one matches", Condition{FieldTag, OpNotContains, "rust"}, false},

		// --- numbers ------------------------------------------------------
		{"word_count gt", Condition{FieldWordCount, OpGT, "1000"}, true},
		{"word_count gt miss", Condition{FieldWordCount, OpGT, "2000"}, false},
		{"word_count lt", Condition{FieldWordCount, OpLT, "2000"}, true},
		{"word_count equals", Condition{FieldWordCount, OpEquals, "1200"}, true},
		{"word_count with junk value", Condition{FieldWordCount, OpGT, "many"}, false},

		// --- age, in hours ------------------------------------------------
		{"age gt 24h", Condition{FieldAge, OpGT, "24"}, true},
		{"age gt 48h", Condition{FieldAge, OpGT, "48"}, false},
		{"age lt 48h", Condition{FieldAge, OpLT, "48"}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.cond.holds(sample(), now)
			if got != c.want {
				t.Errorf("%s %s %q = %v, want %v", c.cond.Field, c.cond.Op, c.cond.Value, got, c.want)
			}
		})
	}
}

// A zero published time must not read as an age of two thousand years, or every
// "older than" rule ever written matches every item whose date failed to parse.
func TestZeroPublishedTimeHasNoAge(t *testing.T) {
	it := sample()
	it.PublishedAt = time.Time{}
	for _, op := range []Op{OpGT, OpLT, OpEquals} {
		c := Condition{FieldAge, op, "24"}
		if c.holds(it, now) {
			t.Errorf("age %s 24 matched an item with no published date", op)
		}
	}
}

func TestAllVersusAny(t *testing.T) {
	hit := Condition{FieldTitle, OpContains, "rust"}
	miss := Condition{FieldTitle, OpContains, "python"}

	cases := []struct {
		name  string
		match Match
		want  bool
	}{
		{"all, both hit", Match{Conditions: []Condition{hit, {FieldLang, OpEquals, "en"}}}, true},
		{"all, one misses", Match{Conditions: []Condition{hit, miss}}, false},
		{"any, one hits", Match{Any: true, Conditions: []Condition{miss, hit}}, true},
		{"any, none hit", Match{Any: true, Conditions: []Condition{miss, {FieldLang, OpEquals, "fr"}}}, false},
		{"all, single", Match{Conditions: []Condition{hit}}, true},
		{"any, single", Match{Any: true, Conditions: []Condition{hit}}, true},
		// The decision worth arguing with: an empty condition set matches
		// NOTHING. Empty-AND is conventionally true, and true here means a
		// half-finished rule or a bad import silently mutes the entire feed.
		{"empty all matches nothing", Match{}, false},
		{"empty any matches nothing", Match{Any: true}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.match.matches(sample(), now); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func rule(id string, pos int, cond Condition, actions ...Action) Rule {
	return Rule{
		ID: id, Name: id, Enabled: true, Position: pos,
		Match:   Match{Conditions: []Condition{cond}},
		Actions: actions,
	}
}

func kinds(as []Action) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, string(a.Kind))
	}
	return out
}

func TestOrderingAndStopProcessing(t *testing.T) {
	hit := Condition{FieldTitle, OpContains, "rust"}

	t.Run("rules run in position order", func(t *testing.T) {
		res := Evaluate(sample(), []Rule{
			rule("second", 2, hit, Action{Kind: ActionStar}),
			rule("first", 1, hit, Action{Kind: ActionMarkRead}),
		}, now)
		if len(res.Hits) != 2 || res.Hits[0].RuleID != "first" {
			t.Errorf("hits ran in the wrong order: %+v", res.Hits)
		}
	})

	t.Run("equal positions fall back to id, so order is stable", func(t *testing.T) {
		first := Evaluate(sample(), []Rule{
			rule("bbb", 0, hit, Action{Kind: ActionStar}),
			rule("aaa", 0, hit, Action{Kind: ActionMarkRead}),
		}, now)
		second := Evaluate(sample(), []Rule{
			rule("aaa", 0, hit, Action{Kind: ActionMarkRead}),
			rule("bbb", 0, hit, Action{Kind: ActionStar}),
		}, now)
		if first.Hits[0].RuleID != second.Hits[0].RuleID {
			t.Error("evaluation order depends on slice order, so a restore can change behaviour")
		}
	})

	t.Run("stop_processing as a rule flag", func(t *testing.T) {
		r1 := rule("first", 1, hit, Action{Kind: ActionMarkRead})
		r1.StopProcessing = true
		res := Evaluate(sample(), []Rule{r1, rule("second", 2, hit, Action{Kind: ActionStar})}, now)
		if res.Stopped != "first" {
			t.Errorf("Stopped = %q, want first", res.Stopped)
		}
		if len(res.Hits) != 1 {
			t.Errorf("%d rules ran after a stop", len(res.Hits))
		}
	})

	t.Run("stop_processing as an action", func(t *testing.T) {
		res := Evaluate(sample(), []Rule{
			rule("first", 1, hit, Action{Kind: ActionMarkRead}, Action{Kind: ActionStopProcessing}),
			rule("second", 2, hit, Action{Kind: ActionStar}),
		}, now)
		if res.Stopped != "first" || len(res.Hits) != 1 {
			t.Errorf("the action form did not stop evaluation: %+v", res)
		}
	})

	t.Run("a disabled rule does not run", func(t *testing.T) {
		r := rule("off", 1, hit, Action{Kind: ActionMute})
		r.Enabled = false
		res := Evaluate(sample(), []Rule{r}, now)
		if len(res.Hits) != 0 || res.Muted() {
			t.Error("a disabled rule fired")
		}
	})

	t.Run("a non-matching rule does not stop the ones after it", func(t *testing.T) {
		r1 := rule("nomatch", 1, Condition{FieldTitle, OpContains, "python"}, Action{Kind: ActionMute})
		r1.StopProcessing = true
		res := Evaluate(sample(), []Rule{r1, rule("second", 2, hit, Action{Kind: ActionStar})}, now)
		if res.Stopped != "" || len(res.Hits) != 1 {
			t.Errorf("a rule that did not match still stopped evaluation: %+v", res)
		}
	})
}

// Precedence: the earlier rule wins. If a later rule could overwrite an earlier
// one's action, moving a rule up the list would stop changing its precedence —
// which is the only thing the ordering is for.
func TestEarlierRulesWin(t *testing.T) {
	hit := Condition{FieldTitle, OpContains, "rust"}
	res := Evaluate(sample(), []Rule{
		rule("first", 1, hit, Action{Kind: ActionSetHomeWeight, Value: "2.0"}),
		rule("second", 2, hit, Action{Kind: ActionSetHomeWeight, Value: "0.1"}),
	}, now)

	var weights []string
	for _, a := range res.Actions {
		if a.Kind == ActionSetHomeWeight {
			weights = append(weights, a.Value)
		}
	}
	if len(weights) != 1 {
		t.Fatalf("expected one home weight, got %v", weights)
	}
	if weights[0] != "2.0" {
		t.Errorf("home weight = %s; the later rule overwrote the earlier one", weights[0])
	}
}

// Distinct values must survive deduplication, or two rules adding two different
// tags produce one tag.
func TestValuedActionsDedupeOnTheValue(t *testing.T) {
	hit := Condition{FieldTitle, OpContains, "rust"}
	res := Evaluate(sample(), []Rule{
		rule("a", 1, hit, Action{Kind: ActionTag, Value: "language"}),
		rule("b", 2, hit, Action{Kind: ActionTag, Value: "deep-dive"}),
		rule("c", 3, hit, Action{Kind: ActionTag, Value: "language"}),
	}, now)

	var tags []string
	for _, a := range res.Actions {
		if a.Kind == ActionTag {
			tags = append(tags, a.Value)
		}
	}
	if len(tags) != 2 {
		t.Errorf("tags = %v, want the two distinct ones", tags)
	}
}

// Each action carries the rule that produced it, which is what rule_hits stores
// and what makes "undo everything rule 7 did" possible.
func TestActionsCarryTheirRuleID(t *testing.T) {
	res := Evaluate(sample(), []Rule{
		rule("r1", 1, Condition{FieldTitle, OpContains, "rust"}, Action{Kind: ActionMute}),
	}, now)
	if len(res.Actions) != 1 || res.Actions[0].RuleID != "r1" {
		t.Errorf("actions do not carry their rule: %+v", res.Actions)
	}
}

func TestMuteIsReported(t *testing.T) {
	hit := Condition{FieldTitle, OpContains, "rust"}
	if !Evaluate(sample(), []Rule{rule("m", 1, hit, Action{Kind: ActionMute})}, now).Muted() {
		t.Error("a mute action did not report as muted")
	}
	if Evaluate(sample(), []Rule{rule("s", 1, hit, Action{Kind: ActionStar})}, now).Muted() {
		t.Error("a star action reported as muted")
	}
}

// Evaluation is pure: the same inputs give the same outputs, and nothing about
// the item is modified. This is what lets the preview (§13.4) be the same code
// as the apply.
func TestEvaluationIsPure(t *testing.T) {
	item := sample()
	set := []Rule{
		rule("a", 1, Condition{FieldTitle, OpContains, "rust"}, Action{Kind: ActionTag, Value: "x"}),
		rule("b", 2, Condition{FieldWordCount, OpGT, "100"}, Action{Kind: ActionStar}),
	}
	first := Evaluate(item, set, now)
	second := Evaluate(item, set, now)

	if strings.Join(kinds(first.Actions), ",") != strings.Join(kinds(second.Actions), ",") {
		t.Error("two evaluations of the same input disagree")
	}
	if item.Title != sample().Title || len(item.Tags) != len(sample().Tags) {
		t.Error("Evaluate modified the item it was given")
	}
}

func TestValidate(t *testing.T) {
	ok := Rule{
		Name:    "good",
		Match:   Match{Conditions: []Condition{{FieldTitle, OpContains, "rust"}}},
		Actions: []Action{{Kind: ActionMute}},
	}
	if err := Validate(ok); err != nil {
		t.Errorf("a valid rule was rejected: %v", err)
	}

	bad := []struct {
		name string
		rule Rule
		want string
	}{
		{"no name", Rule{Match: ok.Match, Actions: ok.Actions}, "needs a name"},
		{"no conditions", Rule{Name: "x", Actions: ok.Actions}, "never match"},
		{"no actions", Rule{Name: "x", Match: ok.Match}, "do nothing"},
		{"unknown field", Rule{Name: "x", Actions: ok.Actions,
			Match: Match{Conditions: []Condition{{"colour", OpContains, "red"}}}}, "unknown field"},
		{"unknown op", Rule{Name: "x", Actions: ok.Actions,
			Match: Match{Conditions: []Condition{{FieldTitle, "sounds_like", "rust"}}}}, "unknown operator"},
		{"bad regex", Rule{Name: "x", Actions: ok.Actions,
			Match: Match{Conditions: []Condition{{FieldTitle, OpRegex, "([unclosed"}}}}, "error parsing regexp"},
		{"non-numeric word count", Rule{Name: "x", Actions: ok.Actions,
			Match: Match{Conditions: []Condition{{FieldWordCount, OpGT, "lots"}}}}, "needs a number"},
		{"unknown action", Rule{Name: "x", Match: ok.Match,
			Actions: []Action{{Kind: "self_destruct"}}}, "unknown action"},
		{"tag with no value", Rule{Name: "x", Match: ok.Match,
			Actions: []Action{{Kind: ActionTag}}}, "needs a value"},
		{"home weight that is not a number", Rule{Name: "x", Match: ok.Match,
			Actions: []Action{{Kind: ActionSetHomeWeight, Value: "high"}}}, "needs a number"},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.rule)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

// An invalid regex is refused at authoring time and inert at evaluation time.
// Both halves matter: the author must be told, and a rule that somehow got saved
// must not take the feed down.
func TestInvalidRegexIsRefusedButNeverFatal(t *testing.T) {
	r := Rule{
		ID: "r", Name: "bad", Enabled: true,
		Match:   Match{Conditions: []Condition{{FieldTitle, OpRegex, "([unclosed"}}},
		Actions: []Action{{Kind: ActionMute}},
	}
	if err := Validate(r); err == nil {
		t.Error("Validate accepted an uncompilable pattern")
	}
	res := Evaluate(sample(), []Rule{r}, now)
	if res.Muted() {
		t.Error("an uncompilable pattern matched, which would mute the whole feed")
	}
}

func BenchmarkEvaluate(b *testing.B) {
	item := sample()
	set := []Rule{
		rule("a", 1, Condition{FieldTitle, OpContains, "rust"}, Action{Kind: ActionTag, Value: "x"}),
		rule("b", 2, Condition{FieldContent, OpRegex, `borrow\s+checker`}, Action{Kind: ActionStar}),
		rule("c", 3, Condition{FieldWordCount, OpGT, "500"}, Action{Kind: ActionSetHomeWeight, Value: "1.5"}),
		rule("d", 4, Condition{FieldTag, OpContains, "systems"}, Action{Kind: ActionMarkRead}),
	}
	b.ReportAllocs()
	// b.Loop rather than `for i := 0; i < b.N; i++`, and not only for style.
	// The compiler is allowed to eliminate a call whose result is discarded, and
	// `_ = Evaluate(...)` is exactly that shape — a benchmark that measures
	// nothing still reports a number. b.Loop keeps the call alive and owns the
	// timer, so setup before it is excluded without a ResetTimer to forget.
	for b.Loop() {
		Evaluate(item, set, now)
	}
}

// TestEvaluateDoesNotMutateTheCallersRuleSet.
//
// Evaluate skips its defensive copy when the rule set already arrives in
// evaluation order, which in this application is always — store.RulesFor selects
// `ORDER BY position ASC, id ASC`. That makes the copy pure waste on the hot
// path (once per item, per subscriber, per poll) and it makes the caller's slice
// reachable from inside Evaluate for the first time.
//
// Nothing in there writes to it today. This is the test that says so out loud,
// because the failure mode is silent and shared: fanout hands the SAME slice to
// every item of a poll, so a rule mutated while evaluating item 1 would change
// how items 2 through 20 are evaluated, and the effect would depend on
// arrival order.
func TestEvaluateDoesNotMutateTheCallersRuleSet(t *testing.T) {
	hit := Condition{FieldTitle, OpContains, "rust"}
	sorted := []Rule{
		rule("aaa", 1, hit, Action{Kind: ActionMarkRead}, Action{Kind: ActionTag, Value: "x"}),
		rule("bbb", 2, hit, Action{Kind: ActionStar}),
	}
	before := fmt.Sprintf("%+v", sorted)

	// Twice, because a mutation that only shows on the second pass — an action
	// stamped with a RuleID on the first — is exactly the shape this guards.
	_ = Evaluate(sample(), sorted, now)
	res := Evaluate(sample(), sorted, now)

	if after := fmt.Sprintf("%+v", sorted); after != before {
		t.Errorf("Evaluate modified the rule set it was given:\n before %s\n after  %s", before, after)
	}
	// And the result is still the one the sorting path produces.
	if len(res.Hits) != 2 || res.Hits[0].RuleID != "aaa" {
		t.Errorf("hits = %+v, want aaa then bbb", res.Hits)
	}
	// The same set, shuffled, must evaluate identically — that is the branch the
	// fast path skips, and it has to keep agreeing with the one that does not.
	shuffled := []Rule{sorted[1], sorted[0]}
	other := Evaluate(sample(), shuffled, now)
	if len(other.Hits) != len(res.Hits) || other.Hits[0].RuleID != res.Hits[0].RuleID {
		t.Errorf("sorted and unsorted input disagree:\n sorted   %+v\n unsorted %+v", res.Hits, other.Hits)
	}
}
