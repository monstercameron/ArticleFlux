package pipeline

import (
	"strings"
	"testing"
)

// longEnough is a title that clears MinEscalateWords on its own, so a case that
// is about the policy is not accidentally about the word count.
//
// It was 24 words against a floor of 25 on the first attempt, and every policy
// case failed with `no_text` — which is the gate working. Kept a few words clear
// of the boundary so that raising MinEscalateWords by one does not silently turn
// this file into a test of the word counter.
const longEnough = "A reasonably long headline about something that goes on for " +
	"quite a while indeed and keeps going past the word floor without ever stopping here at all"

func TestShouldEscalate(t *testing.T) {
	cases := []struct {
		name   string
		a      Analysis
		it     Item
		policy EscalatePolicy
		want   bool
		reason Reason
	}{
		{
			name: "confident placement is not escalated",
			a:    Analysis{Lang: LangEnglish, Primary: "software"},
			it:   Item{Title: longEnough}, policy: EscalateAmbiguous,
			want: false, reason: ReasonConfident,
		},
		{
			name: "a thin margin is the tie-break case",
			a:    Analysis{Lang: LangEnglish, Primary: "security", Ambiguous: true},
			it:   Item{Title: longEnough}, policy: EscalateAmbiguous,
			want: true, reason: ReasonAmbiguous,
		},
		{
			name: "unsorted escalates",
			a:    Analysis{Lang: LangEnglish},
			it:   Item{Title: longEnough}, policy: EscalateAmbiguous,
			want: true, reason: ReasonUnsorted,
		},
		{
			// The free tier refused because the shipped lexicon is English-only,
			// which is the model's whole advantage rather than a hard case.
			name: "a non-English item escalates",
			a:    Analysis{Lang: "und"},
			it:   Item{Title: longEnough}, policy: EscalateAmbiguous,
			want: true, reason: ReasonNotEnglish,
		},
		{
			name: "policy off never escalates, whatever the state",
			a:    Analysis{Ambiguous: true},
			it:   Item{Title: longEnough}, policy: EscalateNever,
			want: false, reason: ReasonPolicyOff,
		},
		{
			name: "always escalates a confident item",
			a:    Analysis{Lang: LangEnglish, Primary: "software"},
			it:   Item{Title: longEnough}, policy: EscalateAlways,
			want: true, reason: ReasonAlways,
		},
		{
			name: "a headline with no body is not worth sending",
			a:    Analysis{Lang: LangEnglish},
			it:   Item{Title: "Six words is not enough here"}, policy: EscalateAmbiguous,
			want: false, reason: ReasonNoText,
		},
		{
			// The documented precedence: "always" means always where there is
			// something to read. A policy name must not be able to override the
			// absence of input.
			name: "no_text beats always",
			a:    Analysis{Lang: LangEnglish},
			it:   Item{Title: "Short headline"}, policy: EscalateAlways,
			want: false, reason: ReasonNoText,
		},
		{
			name: "a body is enough on its own",
			a:    Analysis{Lang: LangEnglish},
			it:   Item{Title: "Short", Body: "a body"}, policy: EscalateAmbiguous,
			want: true, reason: ReasonUnsorted,
		},
		{
			name: "a long summary carries a short title",
			a:    Analysis{Lang: LangEnglish},
			it:   Item{Title: "Short", Summary: longEnough}, policy: EscalateAmbiguous,
			want: true, reason: ReasonUnsorted,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ShouldEscalate(c.a, c.it, c.policy)
			if got.Escalate != c.want || got.Reason != c.reason {
				t.Fatalf("got {%v %s}, wanted {%v %s}",
					got.Escalate, got.Reason, c.want, c.reason)
			}
		})
	}
}

func TestGateCountsEveryDecision(t *testing.T) {
	analyses := []Analysis{
		{Lang: LangEnglish, Primary: "software"},                  // confident
		{Lang: LangEnglish, Primary: "security", Ambiguous: true}, // ambiguous
		{Lang: LangEnglish}, // unsorted
		{Lang: "und"},       // not english
	}
	items := make([]Item, len(analyses))
	for i := range items {
		items[i] = Item{Title: longEnough}
	}

	set := Gate(analyses, items, EscalateAmbiguous)
	if len(set.Indexes) != 3 {
		t.Fatalf("escalated %v, wanted 3 of 4", set.Indexes)
	}

	// Every item is counted, escalated or not — a counter that only records the
	// spend cannot answer "why is my spend this high", which is answered by the
	// skips.
	total := 0
	for _, n := range set.Reasons {
		total += n
	}
	if total != len(analyses) {
		t.Fatalf("reasons account for %d of %d items: %v", total, len(analyses), set.Reasons)
	}
	if set.Reasons[ReasonConfident] != 1 {
		t.Fatalf("the confident item was not counted: %v", set.Reasons)
	}
	if s := set.Share(len(analyses)); s != 0.75 {
		t.Fatalf("share %v, wanted 0.75", s)
	}
}

func TestGateNeverSpendsWhenOff(t *testing.T) {
	analyses := make([]Analysis, 50)
	items := make([]Item, 50)
	for i := range items {
		items[i] = Item{Title: longEnough, Body: strings.Repeat("text ", 400)}
	}
	set := Gate(analyses, items, EscalateNever)
	if len(set.Indexes) != 0 {
		t.Fatalf("policy=never escalated %d items", len(set.Indexes))
	}
}

func TestGateHandlesRaggedInput(t *testing.T) {
	// Defensive rather than expected: pairing answers with the wrong articles is
	// the failure mode this whole index-based API exists to avoid, so a length
	// mismatch must truncate rather than panic or misalign.
	set := Gate(make([]Analysis, 3), make([]Item, 1), EscalateAlways)
	if len(set.Indexes) > 1 {
		t.Fatalf("gated %d items against a batch of 1", len(set.Indexes))
	}
}

// TestUnknownPolicyFallsBackToDefault is the one that protects a bill: a typo in
// a settings row, or a value written by a newer build, must not resolve to
// `always`.
func TestUnknownPolicyFallsBackToDefault(t *testing.T) {
	for _, s := range []string{"", "Always", "aggressive", "yes", "1", "ALWAYS"} {
		got := Policy(s)
		if got != DefaultPolicy {
			t.Fatalf("Policy(%q) resolved to %q, wanted the default %q", s, got, DefaultPolicy)
		}
		if got == EscalateAlways {
			t.Fatalf("Policy(%q) resolved to always, which spends on every item", s)
		}
	}
	for _, s := range []string{"never", "ambiguous", "always"} {
		if Policy(s) != EscalatePolicy(s) {
			t.Fatalf("Policy(%q) did not round-trip", s)
		}
	}
}
