package textvec

import "testing"

func texts(toks []Token) []string {
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		out = append(out, t.Text)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestScanKeepsWhatTokenizeDrops is the reason this function exists. Every one of
// these is a real lexicon term in internal/classify and every one of them is
// invisible to Tokenize (plan.md §27.3a).
func TestScanKeepsWhatTokenizeDrops(t *testing.T) {
	const s = "AI and 5G on the EV, plus UI, UX, OS and F1"

	got := texts(Scan(s))
	for _, want := range []string{"ai", "5g", "ev", "ui", "ux", "os", "f1"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Scan dropped %q, which is a lexicon term: %v", want, got)
		}
	}

	// And the contrast that motivates it: the filtered path loses all of them.
	filtered := Tokenize(s)
	for _, g := range filtered {
		if len(g) < MinTermLen {
			t.Fatalf("Tokenize returned %q, which is under MinTermLen — this test's premise is stale", g)
		}
	}
}

// TestScanKeepsStopwords: the n-gram builder in internal/classify needs them, or
// "war on drugs" becomes the phrase "war drugs" and "state of the art" becomes
// "state art" — terms nobody wrote.
func TestScanKeepsStopwords(t *testing.T) {
	got := texts(Scan("war on drugs"))
	if !eq(got, []string{"war", "on", "drugs"}) {
		t.Fatalf("Scan returned %v, wanted the stopword kept", got)
	}
}

func TestScanRecordsSentenceBoundaries(t *testing.T) {
	toks := Scan("It runs on Java. Islands are lovely")
	var islands Token
	for _, tk := range toks {
		if tk.Text == "islands" {
			islands = tk
		}
	}
	if islands.Text == "" {
		t.Fatalf("islands was not scanned: %v", texts(toks))
	}
	if !islands.BreakBefore {
		t.Fatalf("the token after a full stop was not marked BreakBefore: %+v", islands)
	}
}

func TestScanRecordsCaseAndDigits(t *testing.T) {
	toks := Scan("Pixel 10 shipped")
	if len(toks) != 3 {
		t.Fatalf("expected 3 tokens, got %v", texts(toks))
	}
	if !toks[0].Capitalised {
		t.Fatalf("Pixel was not marked capitalised: %+v", toks[0])
	}
	if !toks[1].Digits {
		t.Fatalf("10 was not marked as digits: %+v", toks[1])
	}
	if toks[2].Capitalised {
		t.Fatalf("shipped was marked capitalised: %+v", toks[2])
	}
}

// TestScanAgreesWithTokenizeOnTheSplit is the guard on the whole argument for
// sharing a scanner: the two must never disagree about where a word ends, only
// about which words are worth keeping.
func TestScanAgreesWithTokenizeOnTheSplit(t *testing.T) {
	for _, s := range []string{
		"state-of-the-art inference on the Snapdragon X2",
		"don't call it a comeback | The Verge",
		"Ars Technica: NPU throughput, measured",
		"",
		"…",
	} {
		filtered := Tokenize(s)
		var kept []string
		for _, tk := range Scan(s) {
			if len(tk.Text) >= MinTermLen && !stopwords[tk.Text] && !furniture[tk.Text] && !tk.Digits {
				kept = append(kept, tk.Text)
			}
		}
		if !eq(kept, filtered) {
			t.Fatalf("Scan+filter and Tokenize disagreed on %q:\n  scan: %v\n  tok:  %v",
				s, kept, filtered)
		}
	}
}

func TestScanEmpty(t *testing.T) {
	if got := Scan(""); got != nil {
		t.Fatalf("Scan(\"\") returned %v, wanted nil", got)
	}
	if got := Scan("   ---   "); got != nil {
		t.Fatalf("Scan of punctuation returned %v, wanted nil", got)
	}
}
