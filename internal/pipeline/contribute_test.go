package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fake is a minimal well-formed contributor, for the registry's own tests.
type fake struct {
	name     string
	priority int
	tokens   int
	instr    string
	schema   map[string]any
	consume  func(*Analysis, json.RawMessage) error
}

func (f fake) Name() string           { return f.name }
func (f fake) Priority() int          { return f.priority }
func (f fake) EstTokens() int         { return f.tokens }
func (f fake) Instructions() string   { return f.instr }
func (f fake) Schema() map[string]any { return f.schema }
func (f fake) Consume(_ context.Context, out *Analysis, raw json.RawMessage) error {
	if f.consume != nil {
		return f.consume(out, raw)
	}
	return nil
}

func okSchema(prop string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{prop},
		"properties":           map[string]any{prop: map[string]any{"type": "string"}},
	}
}

// withProp builds a strict top-level object carrying one property, so a case can
// be about that property's schema and nothing else.
func withProp(name string, prop map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{name},
		"properties":           map[string]any{name: prop},
	}
}

func newFake(name string, priority int) fake {
	return fake{
		name: name, priority: priority, tokens: 50,
		instr: "do the " + name + " thing", schema: okSchema("v"),
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func TestRegistryRejectsWhatWouldFailOnTheWire(t *testing.T) {
	loose := okSchema("v")
	loose["additionalProperties"] = true
	partial := map[string]any{
		"type": "object", "additionalProperties": false,
		"required":   []string{"a"},
		"properties": map[string]any{"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "string"}},
	}

	cases := []struct {
		name string
		c    Contributor
		want string
	}{
		{"hyphenated name", fake{name: "key-phrases", priority: 1, tokens: 1, instr: "x", schema: okSchema("v")}, "must match"},
		{"capitalised name", fake{name: "Classify", priority: 1, tokens: 1, instr: "x", schema: okSchema("v")}, "must match"},
		{"no budget", fake{name: "a", priority: 1, tokens: 0, instr: "x", schema: okSchema("v")}, "no token budget"},
		{"no instructions", fake{name: "a", priority: 1, tokens: 1, instr: "  ", schema: okSchema("v")}, "no instructions"},
		{"empty schema", fake{name: "a", priority: 1, tokens: 1, instr: "x", schema: map[string]any{}}, "empty schema"},
		{"not an object", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: map[string]any{"type": "array", "additionalProperties": false}}, "type object"},
		{"additionalProperties true", fake{name: "a", priority: 1, tokens: 1, instr: "x", schema: loose}, "additionalProperties:false"},
		{"partial required", fake{name: "a", priority: 1, tokens: 1, instr: "x", schema: partial}, "required"},

		// The strict-mode keyword sweep. Each of these is well-formed JSON Schema
		// that the provider answers with a 400, which no test using a fake
		// provider can ever see — the fake answers whatever it is asked. Two of
		// the four SHIPPED contributors carried one of these until this check
		// existed.
		{"numeric bound", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: withProp("v", map[string]any{"type": "number", "minimum": 0})}, "minimum"},
		{"array bound", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: withProp("v", map[string]any{
				"type": "array", "maxItems": 8,
				"items": map[string]any{"type": "string"},
			})}, "maxItems"},
		{"string pattern", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: withProp("v", map[string]any{"type": "string", "pattern": "^x"})}, "pattern"},
		{"maxLength", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: withProp("v", map[string]any{"type": "string", "maxLength": 10})}, "maxLength"},

		// Nested objects carry the same obligations as the top level, and a
		// validator that only checked the outer one is the version that passes
		// here and 400s on the wire.
		{"nested object without additionalProperties", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: withProp("v", map[string]any{
				"type":       "object",
				"required":   []string{"inner"},
				"properties": map[string]any{"inner": map[string]any{"type": "string"}},
			})}, "additionalProperties:false"},
		{"array with no items", fake{name: "a", priority: 1, tokens: 1, instr: "x",
			schema: withProp("v", map[string]any{"type": "array"})}, "no items schema"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewRegistry(c.c)
			if err == nil {
				t.Fatalf("registered %s without complaint", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error was %q, wanted it to mention %q", err, c.want)
			}
		})
	}
}

// TestDuplicateNamesAreRefused: two contributors sharing a name means one
// silently overwrites the other's slice, and the loser's feature just stops
// working with no error anywhere.
func TestDuplicateNamesAreRefused(t *testing.T) {
	_, err := NewRegistry(newFake("genre", 1), newFake("genre", 2))
	if err == nil || !strings.Contains(err.Error(), "two contributors named") {
		t.Fatalf("duplicate names were accepted: %v", err)
	}
}

func TestRegistryOrdersByPriorityThenName(t *testing.T) {
	r, err := NewRegistry(newFake("bravo", 10), newFake("alpha", 10), newFake("top", 99))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"top", "alpha", "bravo"}
	got := r.Names()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order was %v, wanted %v", got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// Build
// ---------------------------------------------------------------------------

func TestBuildAssemblesTheUnion(t *testing.T) {
	r := MustRegistry(newFake("alpha", 50), newFake("bravo", 40))
	req := r.Build([]string{"alpha", "bravo"}, 0)

	if len(req.Included) != 2 {
		t.Fatalf("included %v", req.Included)
	}
	props, _ := req.Schema["properties"].(map[string]any)
	if len(props) != 2 || props["alpha"] == nil || props["bravo"] == nil {
		t.Fatalf("schema properties were %v", props)
	}
	// Strict mode: every property must be required.
	req2, _ := req.Schema["required"].([]string)
	if len(req2) != len(props) {
		t.Fatalf("required lists %d of %d properties", len(req2), len(props))
	}
	if !strings.Contains(req.Instructions, "alpha") || !strings.Contains(req.Instructions, "bravo") {
		t.Fatalf("instructions did not include both fragments: %q", req.Instructions)
	}
	if req.MaxOutputTokens != 100+ReasoningHeadroom {
		t.Fatalf("budget was %d, wanted %d", req.MaxOutputTokens, 100+ReasoningHeadroom)
	}
}

// TestBuildOmitsWhatWasNotConsentedTo is §27.2b rule 5: a contributor whose
// feature is off is not in the union AT ALL, rather than present and ignored. The
// union that goes out must be the union that was consented to, or an egress audit
// over the assembled body is checking the wrong thing.
func TestBuildOmitsWhatWasNotConsentedTo(t *testing.T) {
	r := MustRegistry(newFake("alpha", 50), newFake("bravo", 40))
	req := r.Build([]string{"alpha"}, 0)

	if len(req.Included) != 1 || req.Included[0] != "alpha" {
		t.Fatalf("included %v", req.Included)
	}
	props, _ := req.Schema["properties"].(map[string]any)
	if _, present := props["bravo"]; present {
		t.Fatalf("a contributor that was not enabled appeared in the schema")
	}
	if strings.Contains(req.Instructions, "bravo") {
		t.Fatalf("a contributor that was not enabled appeared in the instructions")
	}
}

func TestBuildWithNothingEnabled(t *testing.T) {
	r := MustRegistry(newFake("alpha", 50))
	req := r.Build(nil, 0)
	if len(req.Included) != 0 || req.MaxOutputTokens != 0 {
		t.Fatalf("an empty union asked for %d tokens: %+v", req.MaxOutputTokens, req)
	}
}

// TestDroppedContributorsAreNamed is "no silent caps". A narrowed read is a
// feature that appears to work and quietly stopped, so every omission has to come
// back with a name and a reason.
func TestDroppedContributorsAreNamed(t *testing.T) {
	var cs []Contributor
	for i := range MaxContributors + 3 {
		cs = append(cs, newFake(fmt.Sprintf("c%d", i), 100-i))
	}
	r := MustRegistry(cs...)

	enabled := make([]string, 0, len(cs))
	for _, c := range cs {
		enabled = append(enabled, c.Name())
	}
	req := r.Build(enabled, 0)

	if len(req.Included) != MaxContributors {
		t.Fatalf("included %d, cap is %d", len(req.Included), MaxContributors)
	}
	if len(req.Dropped) != 3 {
		t.Fatalf("dropped %d without naming them all: %+v", len(req.Dropped), req.Dropped)
	}
	for _, d := range req.Dropped {
		if d.Name == "" || d.Reason == "" {
			t.Fatalf("a drop was recorded without a name or a reason: %+v", d)
		}
	}
	// Lowest priority goes first.
	for _, d := range req.Dropped {
		if d.Name == "c0" {
			t.Fatalf("the highest-priority contributor was dropped: %+v", req.Dropped)
		}
	}
}

func TestBudgetDropsLowestPriorityFirst(t *testing.T) {
	r := MustRegistry(newFake("keep", 90), newFake("drop", 10))
	// Room for one contributor plus the headroom, and not two.
	req := r.Build([]string{"keep", "drop"}, ReasoningHeadroom+60)

	if len(req.Included) != 1 || req.Included[0] != "keep" {
		t.Fatalf("included %v, wanted just keep", req.Included)
	}
	if len(req.Dropped) != 1 || req.Dropped[0].Name != "drop" {
		t.Fatalf("dropped %+v, wanted drop", req.Dropped)
	}
	if !strings.Contains(req.Dropped[0].Reason, "budget") {
		t.Fatalf("the reason did not mention the budget: %q", req.Dropped[0].Reason)
	}
}

// TestUnknownEnabledNameIsReported: a name nobody answers to is a wiring
// mistake, and silence would make it look like the feature is simply off.
func TestUnknownEnabledNameIsReported(t *testing.T) {
	r := MustRegistry(newFake("alpha", 50))
	req := r.Build([]string{"alpha", "typo"}, 0)
	found := false
	for _, d := range req.Dropped {
		if d.Name == "typo" && strings.Contains(d.Reason, "registered") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an unregistered name was silently ignored: %+v", req.Dropped)
	}
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// TestUnionIsolation is §27.2b rule 2 and the reason the union is defensible at
// all: one contributor returning garbage must not cost the others their answers.
// If it did, the union would be strictly worse than separate requests.
func TestUnionIsolation(t *testing.T) {
	good := newFake("good", 50)
	good.consume = func(out *Analysis, raw json.RawMessage) error {
		out.Abstract = "it worked"
		return nil
	}
	bad := newFake("bad", 40)
	bad.consume = func(out *Analysis, raw json.RawMessage) error {
		return fmt.Errorf("this slice was nonsense")
	}
	r := MustRegistry(good, bad)

	var out Analysis
	failures, fatal := r.Dispatch(context.Background(), &out,
		[]byte(`{"good":{"v":"x"},"bad":{"v":"y"}}`), []string{"good", "bad"})

	if fatal != nil {
		t.Fatalf("one bad slice was treated as a whole-read failure: %v", fatal)
	}
	if out.Abstract != "it worked" {
		t.Fatalf("the good contributor lost its answer to the bad one's failure")
	}
	if len(failures) != 1 || failures[0].Name != "bad" {
		t.Fatalf("failures were %v, wanted just bad", failures)
	}
}

func TestDispatchReportsAMissingSlice(t *testing.T) {
	r := MustRegistry(newFake("alpha", 50), newFake("bravo", 40))
	var out Analysis
	failures, fatal := r.Dispatch(context.Background(), &out,
		[]byte(`{"alpha":{"v":"x"}}`), []string{"alpha", "bravo"})

	if fatal != nil {
		t.Fatalf("a missing slice was fatal: %v", fatal)
	}
	if len(failures) != 1 || failures[0].Name != "bravo" {
		t.Fatalf("failures were %v, wanted bravo missing", failures)
	}
}

// TestDispatchIsFatalOnlyWhenThereIsNothingToSplit.
func TestDispatchIsFatalOnNonJSON(t *testing.T) {
	r := MustRegistry(newFake("alpha", 50))
	var out Analysis
	_, fatal := r.Dispatch(context.Background(), &out, []byte("not json at all"), []string{"alpha"})
	if fatal == nil {
		t.Fatalf("a non-JSON reply was not fatal")
	}
}

// ---------------------------------------------------------------------------
// The shipped four
// ---------------------------------------------------------------------------

func TestDefaultRegistryIsValidAndOrdered(t *testing.T) {
	want := []string{"classify", "genre", "keyphrases", "abstract"}
	got := DefaultRegistry.Names()
	if len(got) != len(want) {
		t.Fatalf("registry has %v, wanted %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order is %v, wanted %v — the drop order is the priority order, "+
				"and abstract must be the first thing sacrificed", got, want)
		}
	}
}

func TestClassifyConsume(t *testing.T) {
	c := classifyContributor{}

	t.Run("applies an answer and clears ambiguity", func(t *testing.T) {
		out := Analysis{Primary: "software", Ambiguous: true}
		err := c.Consume(context.Background(), &out, []byte(
			`{"primary":"security","secondary":["software","security"],"tags":["cve","Cve"],
			  "confidence":0.82,"unsure":false}`))
		if err != nil {
			t.Fatal(err)
		}
		if out.Primary != "security" {
			t.Fatalf("primary is %q", out.Primary)
		}
		// The primary must not repeat as a secondary, and duplicates collapse.
		if len(out.Secondary) != 1 || out.Secondary[0] != "software" {
			t.Fatalf("secondary is %v", out.Secondary)
		}
		if len(out.ModelTags) != 1 || out.ModelTags[0] != "cve" {
			t.Fatalf("model tags are %v, wanted case-folded and deduped", out.ModelTags)
		}
		if out.Ambiguous {
			t.Fatalf("ambiguity survived a model answer, so the retry sweep would pay twice")
		}
	})

	t.Run("unsure keeps the free tier's answer", func(t *testing.T) {
		out := Analysis{Primary: "software", Ambiguous: true}
		err := c.Consume(context.Background(), &out,
			[]byte(`{"primary":"gaming","secondary":[],"tags":[],"confidence":0.2,"unsure":true}`))
		if err != nil {
			t.Fatal(err)
		}
		if out.Primary != "software" {
			t.Fatalf("an unsure answer overwrote the free tier: %q", out.Primary)
		}
		if !out.ModelUnsure {
			t.Fatalf("the refusal was not recorded, so it is indistinguishable from never asking")
		}
	})

	t.Run("a confident answer with no primary is refused", func(t *testing.T) {
		out := Analysis{Primary: "software"}
		if err := c.Consume(context.Background(), &out,
			[]byte(`{"primary":"","secondary":[],"tags":[],"confidence":0.9,"unsure":false}`)); err == nil {
			t.Fatalf("an empty primary was accepted")
		}
		if out.Primary != "software" {
			t.Fatalf("a refused answer still modified the analysis: %q", out.Primary)
		}
	})
}

func TestGenreConsumeRefusesAnUnknownKind(t *testing.T) {
	c := genreContributor{}
	out := Analysis{Genre: GenreNews}

	if err := c.Consume(context.Background(), &out, []byte(`{"kind":"listicle"}`)); err == nil {
		t.Fatalf("an off-enum genre was accepted")
	}
	if out.Genre != GenreNews {
		t.Fatalf("a refused genre still overwrote the heuristic: %q", out.Genre)
	}
	if err := c.Consume(context.Background(), &out, []byte(`{"kind":"Analysis"}`)); err != nil {
		t.Fatalf("a correctly-cased genre was refused: %v", err)
	}
	if out.Genre != GenreAnalysis {
		t.Fatalf("genre is %q", out.Genre)
	}
}

func TestKeyphraseConsumeCapsAndDedupes(t *testing.T) {
	c := keyphraseContributor{}
	out := Analysis{Keyphrases: []string{"old", "terms"}}
	err := c.Consume(context.Background(), &out,
		[]byte(`{"phrases":["write-ahead log","Write-Ahead Log","  checkpoint  starvation ",""]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Keyphrases) != 2 {
		t.Fatalf("phrases are %v, wanted 2 after dedupe", out.Keyphrases)
	}
	if out.Keyphrases[1] != "checkpoint starvation" {
		t.Fatalf("whitespace was not normalised: %q", out.Keyphrases[1])
	}

	if err := c.Consume(context.Background(), &out, []byte(`{"phrases":[]}`)); err == nil {
		t.Fatalf("an empty phrase list was accepted")
	}
}

func TestAbstractConsumeRefusesRatherThanTruncates(t *testing.T) {
	c := abstractContributor{}
	out := Analysis{}

	long := strings.Repeat("word ", MaxAbstractRunes)
	if err := c.Consume(context.Background(), &out, []byte(
		`{"text":"`+long+`"}`)); err == nil {
		t.Fatalf("an over-long abstract was accepted")
	}
	if out.Abstract != "" {
		t.Fatalf("a refused abstract was stored anyway: %q", out.Abstract)
	}

	if err := c.Consume(context.Background(), &out,
		[]byte(`{"text":"  Postgres 19   makes logical replication work.  "}`)); err != nil {
		t.Fatal(err)
	}
	if out.Abstract != "Postgres 19 makes logical replication work." {
		t.Fatalf("whitespace was not normalised: %q", out.Abstract)
	}
}

// TestEveryShippedContributorRefusesGarbage. Each Consume must reject a slice it
// cannot use rather than storing a zero value — a blank genre or an empty
// abstract written into the row is indistinguishable from a feature that never
// ran, and the backfill would never revisit it.
func TestEveryShippedContributorRefusesGarbage(t *testing.T) {
	for _, c := range DefaultContributors() {
		t.Run(c.Name(), func(t *testing.T) {
			var out Analysis
			if err := c.Consume(context.Background(), &out, []byte(`"a bare string"`)); err == nil {
				t.Errorf("%s accepted a bare string as its slice", c.Name())
			}
			if err := c.Consume(context.Background(), &out, []byte(`{}`)); err == nil {
				t.Errorf("%s accepted an empty object as its slice", c.Name())
			}
		})
	}
}

// TestFullRoundTrip exercises the whole shape: build a union, answer it, and
// check every contributor's field landed.
func TestFullRoundTrip(t *testing.T) {
	req := DefaultRegistry.Build(DefaultRegistry.Names(), 0)
	if len(req.Included) != 4 || len(req.Dropped) != 0 {
		t.Fatalf("build produced %v / dropped %v", req.Included, req.Dropped)
	}

	reply := []byte(`{
	  "classify":{"primary":"software","secondary":["security"],"tags":["sqlite"],
	              "confidence":0.9,"unsure":false},
	  "genre":{"kind":"analysis"},
	  "keyphrases":{"phrases":["write-ahead log","checkpoint starvation"]},
	  "abstract":{"text":"SQLite's checkpoint can starve under sustained writes."}
	}`)

	out := Analysis{ItemID: "x", Primary: "gaming", Ambiguous: true}
	failures, fatal := DefaultRegistry.Dispatch(context.Background(), &out, reply, req.Included)
	if fatal != nil || len(failures) != 0 {
		t.Fatalf("round trip failed: %v %v", fatal, failures)
	}

	if out.Primary != "software" || len(out.Secondary) != 1 || out.Ambiguous {
		t.Fatalf("classify did not apply: %+v", out)
	}
	if out.Genre != GenreAnalysis {
		t.Fatalf("genre is %q", out.Genre)
	}
	if len(out.Keyphrases) != 2 {
		t.Fatalf("keyphrases are %v", out.Keyphrases)
	}
	if out.Abstract == "" {
		t.Fatalf("no abstract")
	}
	if out.ItemID != "x" {
		t.Fatalf("the item id was lost")
	}
}
