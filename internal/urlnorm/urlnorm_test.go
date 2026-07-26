package urlnorm

import "testing"

// TODO 2.4's bar: 30 real-world pairs collapse, and two genuinely different
// articles don't.
func TestDupeKeyCollapsesRealWorldPairs(t *testing.T) {
	pairs := [][2]string{
		// Tracking parameters, the overwhelming majority of real duplicates.
		{"https://example.com/a", "https://example.com/a?utm_source=feedly"},
		{"https://example.com/a", "https://example.com/a?utm_source=x&utm_medium=rss&utm_campaign=q3"},
		{"https://example.com/a", "https://example.com/a?fbclid=IwAR123"},
		{"https://example.com/a", "https://example.com/a?gclid=abc"},
		{"https://example.com/a", "https://example.com/a?mc_cid=1&mc_eid=2"},
		{"https://example.com/a", "https://example.com/a?ref=hackernews"},
		{"https://example.com/a", "https://example.com/a?igshid=zz"},
		{"https://example.com/a", "https://example.com/a?_hsenc=x&_hsmi=9"},
		{"https://example.com/a", "https://example.com/a?ck_subscriber_id=44"},
		{"https://example.com/a", "https://example.com/a?at_medium=RSS&at_campaign=KARANGA"},
		// Fragments never identify a document.
		{"https://example.com/a", "https://example.com/a#section-2"},
		{"https://example.com/a", "https://example.com/a#:~:text=quoted"},
		// Scheme, host case, www, default port, trailing slash.
		{"https://example.com/a", "http://example.com/a"},
		{"https://example.com/a", "https://www.example.com/a"},
		{"https://example.com/a", "https://EXAMPLE.com/a"},
		{"https://example.com/a", "https://example.com:443/a"},
		{"http://example.com/a", "http://example.com:80/a"},
		{"https://example.com/a", "https://example.com/a/"},
		{"https://example.com/a", "https://example.com/A"},
		// AMP is the same article at a different address.
		{"https://example.com/a", "https://example.com/a/amp"},
		{"https://example.com/a", "https://example.com/a/amp/"},
		{"https://example.com/a", "https://example.com/a.amp"},
		{"https://example.com/a", "https://example.com/a?amp=1"},
		// Pagination and view state render a document differently; it is still
		// the same document for dedup purposes.
		{"https://example.com/a", "https://example.com/a?page=2"},
		{"https://example.com/a", "https://example.com/a?print=1"},
		{"https://example.com/a", "https://example.com/a?output=amp"},
		{"https://example.com/a", "https://example.com/a?single_page=true"},
		// Query order is not information.
		{"https://example.com/a?b=2&c=3", "https://example.com/a?c=3&b=2"},
		// Combinations, which is what actually arrives.
		{"https://example.com/a?id=7", "https://www.example.com/a/?id=7&utm_source=rss#top"},
		{"https://example.com/a", "HTTPS://WWW.EXAMPLE.COM/a/?utm_campaign=z"},
	}
	if len(pairs) < 30 {
		t.Fatalf("only %d pairs — TODO 2.4 asks for 30", len(pairs))
	}
	for _, p := range pairs {
		if a, b := DupeKey(p[0]), DupeKey(p[1]); a != b {
			t.Errorf("should collapse:\n  %s -> %s\n  %s -> %s", p[0], a, p[1], b)
		}
	}
}

// The other half of the bar, and the more important one: a false merge hides an
// article that was never a duplicate.
func TestDupeKeyKeepsDifferentArticlesApart(t *testing.T) {
	pairs := [][2]string{
		{"https://example.com/a", "https://example.com/b"},
		{"https://example.com/a", "https://other.com/a"},
		{"https://example.com/post?id=1", "https://example.com/post?id=2"},
		{"https://example.com/2026/07/26/x", "https://example.com/2026/07/27/x"},
		// A subdomain is a different site often enough to matter.
		{"https://blog.example.com/a", "https://example.com/a"},
		// Only "www." is stripped, not any leading label.
		{"https://www2.example.com/a", "https://example.com/a"},
		// A kept parameter with an empty value differs from one with a value.
		{"https://example.com/a?id=", "https://example.com/a?id=1"},
		// Two different feeds' actual article ids.
		{"https://news.example.com/story/1234", "https://news.example.com/story/12345"},
	}
	for _, p := range pairs {
		if a, b := DupeKey(p[0]), DupeKey(p[1]); a == b {
			t.Errorf("should NOT collapse: %s and %s both -> %s", p[0], p[1], a)
		}
	}
}

// Norm is the conservative one. Collapsing here destroys a bookmark, so it must
// keep everything DupeKey is allowed to throw away.
func TestNormIsConservative(t *testing.T) {
	same := [][2]string{
		{"https://example.com/a", "https://example.com/a?utm_source=x"},
		{"https://example.com/a", "https://example.com/a#frag"},
		{"https://example.com/a", "https://example.com/a/"},
		{"https://example.com/a", "https://EXAMPLE.com:443/a"},
		{"https://example.com/a?b=2&c=3", "https://example.com/a?c=3&b=2"},
	}
	for _, p := range same {
		if a, b := Norm(p[0]), Norm(p[1]); a != b {
			t.Errorf("Norm should collapse %s and %s (%s vs %s)", p[0], p[1], a, b)
		}
	}

	differ := [][2]string{
		// These are the ones DupeKey merges and Norm must not: the user saved a
		// specific address and it is their data.
		{"https://example.com/a", "http://example.com/a"},
		{"https://example.com/a", "https://www.example.com/a"},
		{"https://example.com/a", "https://example.com/A"},
		{"https://example.com/a", "https://example.com/a?page=2"},
		{"https://example.com/a", "https://example.com/a/amp"},
	}
	for _, p := range differ {
		if a, b := Norm(p[0]), Norm(p[1]); a == b {
			t.Errorf("Norm must NOT collapse %s and %s (both -> %s)", p[0], p[1], a)
		}
	}
}

func TestNonWebSchemesAreLeftAlone(t *testing.T) {
	// A mailto: or javascript: URL has no host semantics. Reshaping one into
	// something host-shaped is how a sanitiser bypass starts.
	for _, raw := range []string{
		"mailto:someone@example.com",
		"javascript:alert(1)",
		"data:text/html,<script>x</script>",
		"ftp://example.com/file",
	} {
		if got := Norm(raw); got != raw {
			t.Errorf("Norm(%q) = %q, want it returned untouched", raw, got)
		}
		if Host(raw) != "" {
			t.Errorf("Host(%q) should be empty", raw)
		}
	}
}

func TestHost(t *testing.T) {
	cases := map[string]string{
		"https://www.example.com/a?b=1": "example.com",
		"https://example.com:8443/a":    "example.com",
		"http://BLOG.Example.COM/":      "blog.example.com",
		"not a url":                     "",
		"":                              "",
	}
	for in, want := range cases {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGarbageDoesNotPanic(t *testing.T) {
	for _, raw := range []string{"", "   ", "://", "http://", "%%%", "https://[::1"} {
		_ = Norm(raw)
		_ = DupeKey(raw)
		_ = Host(raw)
	}
}

func TestRootPathNormalises(t *testing.T) {
	if a, b := DupeKey("https://example.com"), DupeKey("https://example.com/"); a != b {
		t.Errorf("bare host and host+slash should match: %q vs %q", a, b)
	}
	if got := DupeKey("https://example.com/"); got != "example.com" {
		t.Errorf("root DupeKey = %q, want %q", got, "example.com")
	}
}
