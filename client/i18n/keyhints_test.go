package i18n

import (
	"regexp"
	"strings"
	"testing"
)

// keyMention finds the English sentences that tell a reader to press a key:
// "Press u", "j and k move", "Enter open · Esc close". A verb is not a key —
// "Press again", "Enter your username" — so the patterns name keys, not words.
var keyMention = regexp.MustCompile(
	`\bPress [b-zA-Z?/](?:[ ,.]|$)|(?:^|\s)[a-z] and [a-z] (?:move|step)|\bEnter open\b|\bEsc close\b|\bCtrl[ +-]\w|\bPress (?:Esc|Enter|Tab|Space|Ctrl)\b`)

// TestKeyHintsHaveTouchVariants holds the rule behind view.keyHint: a sentence
// that tells the reader to press a key is an instruction a phone cannot follow,
// so each has a sibling "<key>Touch" that does not name one. A new hint written
// without its sibling fails here, which is the point — the reader who meets it
// is on the one screen that has nothing to press.
func TestKeyHintsHaveTouchVariants(t *testing.T) {
	vals := allValues()
	found := 0
	for key, val := range vals {
		// The landing page describes the product to someone deciding whether to
		// install it; its captions name keys the way a screenshot does and
		// instruct nobody.
		if strings.HasSuffix(key, "Touch") || strings.HasPrefix(key, "home.") || !keyMention.MatchString(val) {
			continue
		}
		found++
		touch, ok := vals[key+"Touch"]
		if !ok {
			t.Errorf("%s: %q tells the reader to press a key but has no %sTouch for a screen without a keyboard; "+
				"read it through view.keyHint and add the sibling", key, val, key)
			continue
		}
		if keyMention.MatchString(touch) {
			t.Errorf("%sTouch: %q still names a key", key, touch)
		}
	}
	// A pattern that matches nothing passes vacuously.
	if found < 6 {
		t.Errorf("only %d key hints recognised; the pattern has probably stopped matching the catalog", found)
	}
	for key := range vals {
		if base := strings.TrimSuffix(key, "Touch"); base != key {
			if _, ok := vals[base]; !ok {
				t.Errorf("%s has no %s to be the variant of", key, base)
			}
		}
	}
}

// Has is what stops keyHint asking for a sibling that was never written: true
// for a key in the catalog, false for one that is not, and specifically false
// for the hint that showed its own name on a phone.
func TestHas(t *testing.T) {
	for _, tc := range []struct {
		ns, key string
		want    bool
	}{
		{"list", "emptySearchHint", true},
		{"list", "emptySearchHintTouch", false},
		{"list", "emptyUnreadHintTouch", true},
		{"list", "noSuchKeyAtAll", false},
	} {
		if got := Has(tc.ns, tc.key); got != tc.want {
			t.Errorf("Has(%q, %q) = %v, want %v", tc.ns, tc.key, got, tc.want)
		}
	}
}
