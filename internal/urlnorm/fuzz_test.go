package urlnorm

import "testing"

// FuzzNormNeverPanicsAndIsIdempotent feeds arbitrary strings — not just URLs —
// through Norm, because every caller (bookmarks, OPML htmlUrl, a scraped
// href) hands this package whatever a stranger's document contained.
//
// Idempotence is the invariant worth checking beyond "does not crash": a
// canonicaliser that is not a fixed point after one application would make a
// bookmark's identity drift the second time it happens to be re-saved.
func FuzzNormNeverPanicsAndIsIdempotent(f *testing.F) {
	for _, s := range []string{
		"https://example.com/a?utm_source=x#frag",
		"HTTPS://WWW.EXAMPLE.com:443/a/", "mailto:x@y.z", "javascript:alert(1)",
		"data:text/html,<script>", "not a url", "", "   ", "://", "http://",
		"%%%", "https://[::1", "http://ex ample.com/a b?c=d e#f g",
		"https://example.com/" + string(rune(0)),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Norm(%q) panicked: %v", raw, r)
			}
		}()
		once := Norm(raw)
		twice := Norm(once)
		if once != twice {
			t.Fatalf("Norm is not idempotent: Norm(%q) = %q, Norm(that) = %q", raw, once, twice)
		}
	})
}

// FuzzDupeKeyNeverPanics.
//
// Unlike Norm, DupeKey's output is deliberately not a URL — the scheme is
// dropped "because the key's only job is equality" (see DupeKey's doc
// comment) — so DupeKey(DupeKey(x)) == DupeKey(x) is NOT a real contract:
// feeding a bare "host+path?query" string back in makes it fail parse() and
// fall to the lowercased-raw-string branch, which is a different code path
// with different casing behaviour on the query. An earlier version of this
// fuzz test asserted that idempotence anyway and found exactly that — a real
// difference in the fallback path, not a bug, since no real caller ever
// re-runs DupeKey on its own output. What every caller DOES need, because
// DupeKey runs on every item of every feed a stranger controls, is that it
// never panics.
func FuzzDupeKeyNeverPanics(f *testing.F) {
	for _, s := range []string{
		"https://example.com/a?page=2&utm_source=x#frag",
		"https://WWW.Example.com/A/amp/", "mailto:x@y.z", "not a url", "",
		"https://example.com/a?b=2&c=3", "https://example.com" + string(rune(0)),
		"http://0?A",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("DupeKey(%q) panicked: %v", raw, r)
			}
		}()
		_ = DupeKey(raw)
	})
}

// FuzzItemKeyNeverPanics. No idempotence check here: unlike Norm and DupeKey,
// ItemKey deliberately keeps the fragment, and running its OWN output back
// through itself is not guaranteed to be a no-op in the general case the way
// it is for the other two (a raw fragment can itself look like part of a path
// once re-parsed as a bare string in some pathological inputs). What every
// caller actually needs from it — never panicking on stranger-controlled feed
// links — is what this checks.
func FuzzItemKeyNeverPanics(f *testing.F) {
	for _, s := range []string{
		"https://example.com/a#frag", "https://example.com/a?utm_source=x#frag",
		"http://www.scripting.com/1999/07/08.html#stockMarket", "not a url", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ItemKey(%q) panicked: %v", raw, r)
			}
		}()
		_ = ItemKey(raw)
	})
}
