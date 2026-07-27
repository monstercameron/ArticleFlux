package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// The guards for §20.16.3's boundary. Two questions, and they are different
// questions:
//
//   - Does a theming body carry only what §20.16.3 permits?
//   - Did admitting `prompt` here loosen anything else? That is the one that
//     matters in a year, when there are four allowlists and somebody needs one
//     more field.

func TestAThemeBodyCarriesOnlyWhatIsPermitted(t *testing.T) {
	for _, p := range []ThemePayload{
		{Prompt: "a cold library at 2am", Tone: "dark"},
		{Interests: []string{"NPU inference", "SQLite internals"}, Tone: "light"},
	} {
		body, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		bad, err := AuditThemeEgress(body)
		if err != nil {
			t.Fatal(err)
		}
		if len(bad) > 0 {
			t.Errorf("outbound theming body carries %v", bad)
		}
	}
}

// TestThemeKeysDidNotWidenTheOthers is the point of having three lists.
//
// `prompt` is free text the reader typed. If it were legal in a RANK body, the
// interest layer would have gained a field for arbitrary reader-authored strings
// without anybody deciding that — which is exactly the accidental widening the
// split exists to prevent.
//
// # `prompt` means two different things, which is the argument itself
//
// ClassifyKeys already contains `prompt`, and it is not this one: there it is a
// per-label instruction the reader wrote for a CATEGORY, bounded by
// MaxLabelPromptRunes and only sent under the per-user consent key (§27.4d). Here
// it is a description of a room. Two payloads, one word, different rules, both
// correct — and a single shared allowlist could not express that at all. It would
// have one entry for `prompt` and both callers would inherit whichever set of
// reasons was written down first.
func TestThemeKeysDidNotWidenTheOthers(t *testing.T) {
	for _, key := range []string{"prompt", "interests", "tone"} {
		if EgressKeys[key] {
			t.Errorf("§18.8's interest allowlist now admits %q", key)
		}
	}
	for _, key := range []string{"interests", "tone"} {
		if ClassifyKeys[key] {
			t.Errorf("§27.4e's classification allowlist now admits %q", key)
		}
	}
	// And the other direction: a theming body must not be a way to send an
	// article, a profile, or a reading history.
	for _, key := range []string{
		"body", "article", "candidates", "profile", "topics", "terms",
		"sources", "summary", "title", "id", "labels",
	} {
		if ThemeKeys[key] {
			t.Errorf("the theming allowlist admits %q, which is content", key)
		}
	}
}

// TestThemeAuditCatchesAnAddedField. The types are the enforcement; this is the
// check that the enforcement is still the types. A hand-built body — which is the
// only way a field could appear — is caught.
func TestThemeAuditCatchesAnAddedField(t *testing.T) {
	body := []byte(`{"prompt":"x","tone":"dark","user_id":"u_1","read_items":42}`)
	bad, err := AuditThemeEgress(body)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, k := range bad {
		found[k] = true
	}
	for _, want := range []string{"user_id", "read_items"} {
		if !found[want] {
			t.Errorf("audit missed %q", want)
		}
	}
}

func TestThemeTrimAppliesTheCapsAndSaysSo(t *testing.T) {
	long := strings.Repeat("mood ", 200)
	p, cut := ThemePayload{Prompt: long, Tone: "dark"}.Trim()
	if !cut {
		t.Error("a 1,000-rune prompt was trimmed silently")
	}
	if n := len([]rune(p.Prompt)); n > MaxThemePromptRunes {
		t.Errorf("prompt is %d runes, over the %d cap", n, MaxThemePromptRunes)
	}

	many := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	p, cut = ThemePayload{Interests: many, Tone: "dark"}.Trim()
	if !cut {
		t.Error("eight interests were cut to five silently")
	}
	if len(p.Interests) != MaxThemeInterests {
		t.Errorf("kept %d interests, want %d", len(p.Interests), MaxThemeInterests)
	}

	// A label longer than a label. Not dropped — truncated, because the first
	// forty runes of "NPU inference and the memory wall on mobile silicon" still
	// names the interest.
	p, cut = ThemePayload{Interests: []string{strings.Repeat("x", 80)}, Tone: "dark"}.Trim()
	if !cut || len([]rune(p.Interests[0])) != MaxThemeLabelRunes {
		t.Errorf("label came back %d runes, cut=%v", len([]rune(p.Interests[0])), cut)
	}

	// Nothing to do is not a cut. A caller that logged "capped" on every ordinary
	// request would train its reader to ignore the word.
	if _, cut := (ThemePayload{Prompt: "warm and quiet", Tone: "dark"}).Trim(); cut {
		t.Error("an ordinary prompt reported as capped")
	}
}

// TestTrimDropsEmptyInterests: a topic with no label is not an interest, and
// sending "" spends a slot to tell the model nothing.
func TestTrimDropsEmptyInterests(t *testing.T) {
	p, _ := ThemePayload{Interests: []string{"", "  ", "birds"}, Tone: "light"}.Trim()
	if len(p.Interests) != 1 || p.Interests[0] != "birds" {
		t.Errorf("interests came back %q", p.Interests)
	}
}
