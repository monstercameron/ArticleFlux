package themewire

import (
	"testing"

	"github.com/monstercameron/ArticleFlux/client/design"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// TestTheMappingIsLossless is the reason this package exists.
//
// Tokens and Theme are hand-written eighteen-field lists, which is deliberate: a
// loop over design.Theme.Vars() would match named proto fields to list positions,
// and that keeps compiling after a token is inserted in the middle — it just starts
// sending --hair as --line. The cost of writing them out is that a field can be
// FORGOTTEN, and the symptom of a forgotten colour is a CSS declaration silently
// dropped and an element inheriting. This is the check that pays that cost.
//
// It walks Vars(), so a token added to the engine is covered without this test
// being edited: the new one will round-trip as "" and fail here.
func TestTheMappingIsLossless(t *testing.T) {
	cases := append([]design.Theme{}, design.Themes...)
	// A generated theme too, because its identity fields are the ones that differ:
	// the reserved name, a model-authored label, and a derived shadow.
	gen, _, err := design.NewGenerated("Thunderhead", "Slate and rain.", design.GeneratedTokens{
		Ground: design.Ink.Ground, Raised: design.Ink.Raised, Sunk: design.Ink.Sunk,
		Line: design.Ink.Line, Hair: design.Ink.Hair,
		Cream: design.Ink.Cream, Soft: design.Ink.Soft, Dim: design.Ink.Dim,
		Read: design.Ink.Read, Accent: design.Ink.Accent,
		Pos: design.Ink.Pos, Neg: design.Ink.Neg, Wash: 13,
	})
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, gen)

	for _, th := range cases {
		got, err := Theme(Tokens(th))
		if err != nil {
			t.Fatalf("%s did not survive the wire: %v", th.Name, err)
		}
		if got.Name != th.Name || got.Label != th.Label ||
			got.Blurb != th.Blurb || got.Tone != th.Tone {
			t.Errorf("%s: identity crossed as %q/%q/%q/%q",
				th.Name, got.Name, got.Label, got.Blurb, got.Tone)
		}
		for i, kv := range got.Vars() {
			if want := th.Vars()[i][1]; kv[1] != want {
				t.Errorf("%s: --%s crossed as %q, was %q", th.Name, kv[0], kv[1], want)
			}
		}
	}
}

// TestANilMessageIsNotABlackTheme.
//
// A zero design.Theme has empty colours, and an empty colour paints nothing — so
// returning one for an absent palette would make "the server sent no theme"
// indistinguishable from "the server sent a theme of nothing", and the second one
// reaches documentElement.
func TestANilMessageIsNotABlackTheme(t *testing.T) {
	if _, err := Theme(nil); err == nil {
		t.Error("a nil ThemeTokens was accepted as a palette")
	}
}

// TestTheWireIsValidated. Crossing a process boundary is exactly where a palette
// stops being trustworthy, in both directions.
func TestTheWireIsValidated(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*pb.ThemeTokens)
	}{
		{"a colour that is not one", func(b *pb.ThemeTokens) { b.Ground = "plum" }},
		{"an injected shadow", func(b *pb.ThemeTokens) { b.Shadow = "0 0 0 url(http://x/y)" }},
		{"a wash that is not a percentage", func(b *pb.ThemeTokens) { b.Wash = "lots" }},
	} {
		msg := Tokens(design.Fanciful)
		tc.mut(msg)
		if _, err := Theme(msg); err == nil {
			t.Errorf("%s crossed the wire unchallenged", tc.name)
		}
	}

	// And the readability floor, not merely the parser: a palette that would paint
	// illegible datelines is repaired on arrival rather than applied.
	bad := design.Fanciful
	bad.Dim = "#2A2233"
	got, err := Theme(Tokens(bad))
	if err != nil {
		t.Fatal(err)
	}
	if r := design.ContrastRatio(got.Dim, got.Ground); r < design.AAFloor {
		t.Errorf("an illegible --mute crossed the wire and stayed illegible (%.2f:1)", r)
	}
}

// TestRepairsRoundTrip: the screen reports what the floor changed, so the reason
// ids have to survive the trip that carries them.
func TestRepairsRoundTrip(t *testing.T) {
	in := []design.Repair{
		{Token: "mute", From: "#111111", To: "#222222", Why: design.ReasonUnreadable},
		{Token: "wash", From: "90%", To: "40%", Why: design.ReasonWashClamped},
	}
	out := FromRepairs(Repairs(in))
	if len(out) != len(in) {
		t.Fatalf("%d repairs crossed, sent %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("repair %d crossed as %+v, was %+v", i, out[i], in[i])
		}
	}
	// Nil in, nil out. An empty slice on the wire and an absent one mean the same
	// thing here — nothing was repaired — and the client checks length either way.
	if Repairs(nil) != nil || FromRepairs(nil) != nil {
		t.Error("an empty repair list allocated")
	}
}
