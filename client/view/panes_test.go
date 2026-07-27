//go:build js && wasm

package view

import (
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/platform"
)

// --- connLabel ---------------------------------------------------------------

func TestConnLabel(t *testing.T) {
	tr := mustRuntime(t)
	cases := []struct {
		name string
		s    data.ConnState
		want string
	}{
		{"live", data.Live, "connected"},
		{"connecting", data.Connecting, "connecting"},
		{"offline", data.Offline, "no network"},
		{"blocked", data.Blocked, "action needed"},
		// Down, and anything else this type can hold, share the `default` arm.
		{"down", data.Down, "no server"},
		{"unrecognised value falls to the default arm", data.ConnState("bogus"), "no server"},
		{"empty value falls to the default arm", data.ConnState(""), "no server"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := connLabel(tr, c.s); got != c.want {
				t.Errorf("connLabel(%q) = %q, want %q", c.s, got, c.want)
			}
		})
	}
}

// --- readingTime ---------------------------------------------------------------

func TestReadingTime(t *testing.T) {
	tr := mustRuntime(t)
	cases := []struct {
		name  string
		words int32
		want  string
	}{
		{"zero words never reads 0 min", 0, "1 min read"},
		{"one word", 1, "1 min read"},
		// 230 wpm is the documented rate; integer division truncates, so 229
		// words (just under one minute's worth) and 230 (exactly one minute's
		// worth) both round to the floor of 1, not 0.
		{"one word under the 230wpm boundary", 229, "1 min read"},
		{"exactly the 230wpm boundary", 230, "1 min read"},
		{"one word over the boundary truncates back to 1", 231, "1 min read"},
		{"459 words is still 1 (truncation, not rounding)", 459, "1 min read"},
		{"460 words is exactly 2 minutes", 460, "2 min read"},
		{"large word count uses the plural form", 23000, "100 min read"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := readingTime(tr, c.words); got != c.want {
				t.Errorf("readingTime(%d) = %q, want %q", c.words, got, c.want)
			}
		})
	}
}

// --- ageOf -----------------------------------------------------------------

func TestAgeOf(t *testing.T) {
	now := time.Now()
	fmtT := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339Nano) }

	cases := []struct {
		name        string
		publishedAt string
		read        bool
		want        string
	}{
		{"read wins over a fresh timestamp", fmtT(-time.Minute), true, "read"},
		{"read wins even over an unparsable timestamp", "not-a-date", true, "read"},
		{"read wins even over the empty string", "", true, "read"},
		{"empty timestamp, unread", "", false, "unread"},
		{"garbage timestamp, unread", "not-a-date", false, "unread"},
		{"just published is new", fmtT(-time.Minute), false, "new"},
		{"just under the 24h fresh boundary is new", fmtT(-(23*time.Hour + 59*time.Minute)), false, "new"},
		{"just over the 24h fresh boundary is unread, not new", fmtT(-(24*time.Hour + time.Minute)), false, "unread"},
		{"just under the 30 day stale boundary is unread", fmtT(-(29 * 24 * time.Hour)), false, "unread"},
		{"just over the 30 day stale boundary is stale", fmtT(-(30*24*time.Hour + time.Hour)), false, "stale"},
		{"RFC3339 (no fractional seconds) also parses", "2020-01-01T00:00:00Z", false, "stale"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ageOf(c.publishedAt, c.read); got != c.want {
				t.Errorf("ageOf(%q, %v) = %q, want %q", c.publishedAt, c.read, got, c.want)
			}
		})
	}
}

// --- firstWords --------------------------------------------------------------

func TestFirstWords(t *testing.T) {
	cases := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"empty string", "", 10, ""},
		{"shorter than max, unchanged", "hello", 10, "hello"},
		{"exactly max, unchanged, no ellipsis", "0123456789", 10, "0123456789"},
		{
			"newlines and repeated whitespace collapse to single spaces",
			"the argument\n\nabout   compilers",
			100,
			"the argument about compilers",
		},
		{
			// The documented invariant: cut on a WORD boundary, not mid-word.
			// "the argument about compil…" would be a typo with an ellipsis.
			"cuts on the last space before max, never mid-word",
			"the argument about compilers",
			26,
			"the argument about…",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := firstWords(c.s, c.max)
			if got != c.want {
				t.Errorf("firstWords(%q, %d) = %q, want %q", c.s, c.max, got, c.want)
			}
		})
	}

	t.Run("a single word longer than max has no boundary to cut on", func(t *testing.T) {
		// firstWords documents cutting on a word boundary; with no space
		// anywhere in the cut region there is none to cut on, so this
		// necessarily falls back to a hard byte cut. Documented here as the
		// known shape of that fallback rather than asserted as "correct".
		s := strings.Repeat("a", 50)
		got := firstWords(s, 10)
		want := strings.Repeat("a", 10) + "…"
		if got != want {
			t.Errorf("firstWords(50 a's, 10) = %q, want %q", got, want)
		}
	})

	// Hostile/unicode: a run of multi-byte runes with no ASCII space anywhere
	// near the cut point. firstWords slices by BYTE offset (`s[:max]`) before
	// it ever looks for a space, so when no space is found within range the
	// byte cut stands as the final answer. For a multi-byte rune straddling
	// that offset, the result is not valid UTF-8 — real product bug, not a
	// test bug: the function's own doc comment promises a boundary cut, and
	// this violates it in a stronger way (an invalid string, not just an
	// unlovely one).
	t.Run("BUG: unicode input with no space in range can yield invalid UTF-8", func(t *testing.T) {
		s := strings.Repeat("日", 20) // 3 bytes/rune, no spaces anywhere
		got := firstWords(s, 10)     // 10 is not a multiple of 3: splits a rune
		if !utf8.ValidString(got) {
			t.Errorf("firstWords(%q, 10) = %q (%x), which is not valid UTF-8 — "+
				"panes.go's firstWords slices s[:max] by byte offset before it "+
				"looks for a word boundary, so a multi-byte rune straddling that "+
				"offset with no space nearby gets sliced in half", s, got, got)
		}
	})

	t.Run("unicode input WITH a space in range cuts cleanly", func(t *testing.T) {
		// The counter-case: when a space genuinely is found within s[:max],
		// the trim point is always an ASCII byte, which can never sit inside
		// a valid UTF-8 rune's continuation bytes — so this path is safe.
		// max=12 deliberately includes the space after "日本語" (byte offset
		// 9) inside the initial s[:max] window; a smaller max would put the
		// space outside that window and hit the same bug as the case above.
		s := "日本語 " + strings.Repeat("z", 40)
		got := firstWords(s, 12)
		if !utf8.ValidString(got) {
			t.Errorf("firstWords(%q, 12) = %q, not valid UTF-8", s, got)
		}
		if got != "日本語…" {
			t.Errorf("firstWords(%q, 12) = %q, want %q", s, got, "日本語…")
		}
	})
}

// --- catOpen / toggleCat -------------------------------------------------------

func TestCatOpen(t *testing.T) {
	cases := []struct {
		name     string
		openCats string
		id       string
		want     bool
	}{
		{"empty set, nothing is open", "", "a", false},
		{"present in a single-entry set", "a", "a", true},
		{"present among several", "a,b,c", "b", true},
		{"absent among several", "a,b,c", "z", false},
		{"is a prefix of an entry but not equal to it — must not match", "cat-1", "cat", false},
		{"case-sensitive: differing case does not match", "Cat1", "cat1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := catOpen(c.openCats, c.id); got != c.want {
				t.Errorf("catOpen(%q, %q) = %v, want %v", c.openCats, c.id, got, c.want)
			}
		})
	}
}

func TestToggleCat(t *testing.T) {
	cases := []struct {
		name     string
		openCats string
		id       string
		want     string
	}{
		{"adding to empty set", "", "a", "a"},
		{"adding a second entry", "a", "b", "a,b"},
		{"removing the only entry empties the set", "a", "a", ""},
		{"removing the first of several", "a,b,c", "a", "b,c"},
		{"removing the middle of several", "a,b,c", "b", "a,c"},
		{"removing the last of several", "a,b,c", "c", "a,b"},
		{"adding an id already present is a no-op removal+re-add is NOT what happens: it removes it", "a,b", "a", "b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toggleCat(c.openCats, c.id); got != c.want {
				t.Errorf("toggleCat(%q, %q) = %q, want %q", c.openCats, c.id, got, c.want)
			}
		})
	}

	t.Run("round trip: toggling twice returns to empty", func(t *testing.T) {
		once := toggleCat("", "cat1")
		twice := toggleCat(once, "cat1")
		if twice != "" {
			t.Errorf("toggling %q twice = %q, want empty string", "cat1", twice)
		}
	})

	t.Run("round trip through catOpen", func(t *testing.T) {
		s := toggleCat("", "cat1")
		if !catOpen(s, "cat1") {
			t.Fatalf("catOpen(%q, \"cat1\") = false after toggling it open", s)
		}
		s = toggleCat(s, "cat1")
		if catOpen(s, "cat1") {
			t.Fatalf("catOpen(%q, \"cat1\") = true after toggling it closed", s)
		}
	})
}

// --- hostOf / iconHost ---------------------------------------------------------

func TestHostOf(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty string", "", ""},
		{"no scheme at all", "example.com/feed", ""},
		{"ordinary https URL", "https://example.com/feed.xml", "example.com"},
		{"scheme and host are lowercased", "HTTPS://Example.COM/Feed", "example.com"},
		{"port is stripped", "https://example.com:8443/feed", "example.com"},
		{"userinfo is stripped", "https://user:pass@example.com/feed", "example.com"},
		{"userinfo AND port are both stripped", "https://user:pass@example.com:8443/feed", "example.com"},
		{"query string does not leak into the host", "https://example.com?x=1", "example.com"},
		{"fragment does not leak into the host", "https://example.com#frag", "example.com"},
		{"leading/trailing whitespace is trimmed first", "  https://example.com/feed  ", "example.com"},
		{"bare scheme with nothing after it", "https://", ""},
		{"unicode host is lowercased, not mangled", "https://Ünïcödé.example/x", "ünïcödé.example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostOf(c.raw); got != c.want {
				t.Errorf("hostOf(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}

	// Hostile input found while covering this function: a bracketed IPv6
	// literal contains a ':' before the port separator that hostOf is
	// looking for, and hostOf finds THAT one first (IndexByte returns the
	// first colon, not the one that actually separates host from port).
	t.Run("BUG: bracketed IPv6 host is truncated at the first colon inside the brackets", func(t *testing.T) {
		raw := "https://[::1]:8080/feed"
		want := "[::1]"
		if got := hostOf(raw); got != want {
			t.Errorf("hostOf(%q) = %q, want %q — hostOf cuts at the FIRST ':' "+
				"in the remainder, which for a bracketed IPv6 literal is inside "+
				"the brackets, not the host:port separator", raw, got, want)
		}
	})
}

func TestIconHost(t *testing.T) {
	cases := []struct {
		name string
		f    *pb.Feed
		want string
	}{
		{
			"site URL wins when both are set — feeds are often served off a syndication host",
			&pb.Feed{SiteUrl: "https://publisher.example/", FeedUrl: "https://feedburner.example/rss"},
			"publisher.example",
		},
		{
			"falls back to the feed URL when the site URL does not resolve",
			&pb.Feed{SiteUrl: "not-a-url", FeedUrl: "https://feedburner.example/rss"},
			"feedburner.example",
		},
		{
			"falls back to the feed URL when the site URL is empty",
			&pb.Feed{SiteUrl: "", FeedUrl: "https://feedburner.example/rss"},
			"feedburner.example",
		},
		{
			"neither resolves",
			&pb.Feed{SiteUrl: "not-a-url", FeedUrl: "also-not-a-url"},
			"",
		},
		{
			"both empty",
			&pb.Feed{},
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := iconHost(c.f); got != c.want {
				t.Errorf("iconHost(%+v) = %q, want %q", c.f, got, c.want)
			}
		})
	}
}

// --- faviconSrc ----------------------------------------------------------------

func TestFaviconSrc(t *testing.T) {
	saved := HasFaviconService
	t.Cleanup(func() { HasFaviconService = saved })

	t.Run("empty host is always empty, service enabled or not", func(t *testing.T) {
		HasFaviconService = true
		if got := faviconSrc(""); got != "" {
			t.Errorf("faviconSrc(\"\") = %q, want empty", got)
		}
	})

	t.Run("service disabled produces no src even with a host", func(t *testing.T) {
		HasFaviconService = false
		if got := faviconSrc("example.com"); got != "" {
			t.Errorf("faviconSrc with HasFaviconService=false = %q, want empty", got)
		}
	})

	t.Run("service enabled builds a path relative to BasePath, not a hardcoded absolute one", func(t *testing.T) {
		HasFaviconService = true
		got := faviconSrc("example.com")
		want := platform.BasePath() + "favicon?host=example.com"
		if got != want {
			t.Errorf("faviconSrc(\"example.com\") = %q, want %q", got, want)
		}
		if strings.HasPrefix(got, "//") {
			t.Errorf("faviconSrc(\"example.com\") = %q, looks scheme-relative rather than base-relative", got)
		}
	})
}

// --- feedsByFolder / folderSources / tagSources / hasUnfiled --------------------

func TestFeedsByFolder(t *testing.T) {
	feeds := []*pb.Feed{
		{SourceId: "s1", FolderId: ""},
		{SourceId: "s2", FolderId: "cat-a"},
		{SourceId: "s3", FolderId: ""},
		{SourceId: "s4", FolderId: "cat-b"},
	}
	got := feedsByFolder(feeds)

	if len(got) != 3 {
		t.Fatalf("feedsByFolder produced %d groups, want 3 (unfiled, cat-a, cat-b): %+v", len(got), got)
	}
	unfiled := got[unfiledID]
	if len(unfiled) != 2 || unfiled[0].GetSourceId() != "s1" || unfiled[1].GetSourceId() != "s3" {
		t.Errorf("unfiled group = %v, want [s1 s3] in input order", sourceIDs(unfiled))
	}
	if a := got["cat-a"]; len(a) != 1 || a[0].GetSourceId() != "s2" {
		t.Errorf("cat-a group = %v, want [s2]", sourceIDs(a))
	}
	if b := got["cat-b"]; len(b) != 1 || b[0].GetSourceId() != "s4" {
		t.Errorf("cat-b group = %v, want [s4]", sourceIDs(b))
	}
}

func TestFeedsByFolderEmptyInput(t *testing.T) {
	got := feedsByFolder(nil)
	if len(got) != 0 {
		t.Errorf("feedsByFolder(nil) = %v, want an empty map", got)
	}
}

func sourceIDs(feeds []*pb.Feed) []string {
	out := make([]string, len(feeds))
	for i, f := range feeds {
		out[i] = f.GetSourceId()
	}
	return out
}

func TestFolderSources(t *testing.T) {
	feeds := []*pb.Feed{
		{SourceId: "s1", FolderId: "cat-a"},
		{SourceId: "s2", FolderId: ""},
		{SourceId: "s3", FolderId: "cat-a"},
	}
	t.Run("matching folder", func(t *testing.T) {
		got := folderSources(feeds, "cat-a")
		want := []string{"s1", "s3"}
		if !equalStrings(got, want) {
			t.Errorf("folderSources(feeds, \"cat-a\") = %v, want %v", got, want)
		}
	})
	t.Run("unfiled sentinel matches empty FolderId", func(t *testing.T) {
		got := folderSources(feeds, unfiledID)
		want := []string{"s2"}
		if !equalStrings(got, want) {
			t.Errorf("folderSources(feeds, unfiledID) = %v, want %v", got, want)
		}
	})
	t.Run("a folder with nothing filed returns the __none__ sentinel, not an empty (unfiltered) slice", func(t *testing.T) {
		got := folderSources(feeds, "cat-nonexistent")
		want := []string{"__none__"}
		if !equalStrings(got, want) {
			t.Errorf("folderSources(feeds, \"cat-nonexistent\") = %v, want %v", got, want)
		}
	})
	t.Run("empty feed list also returns the sentinel", func(t *testing.T) {
		got := folderSources(nil, "cat-a")
		want := []string{"__none__"}
		if !equalStrings(got, want) {
			t.Errorf("folderSources(nil, \"cat-a\") = %v, want %v", got, want)
		}
	})
}

func TestTagSources(t *testing.T) {
	bySource := map[string][]string{
		"s1": {"tag-a", "tag-b"},
		"s2": {"tag-b"},
		"s3": {},
	}
	t.Run("a tag with feeds returns them", func(t *testing.T) {
		got := tagSources(nil, bySource, "tag-b")
		want := []string{"s1", "s2"}
		if !equalStringSets(got, want) {
			t.Errorf("tagSources(..., \"tag-b\") = %v, want set %v", got, want)
		}
	})
	t.Run("a tag with no feeds returns the __none__ sentinel, not nil", func(t *testing.T) {
		got := tagSources(nil, bySource, "tag-nonexistent")
		want := []string{"__none__"}
		if !equalStrings(got, want) {
			t.Errorf("tagSources(..., \"tag-nonexistent\") = %v, want %v", got, want)
		}
	})
	t.Run("an entirely empty association map returns the sentinel", func(t *testing.T) {
		got := tagSources(nil, map[string][]string{}, "tag-a")
		want := []string{"__none__"}
		if !equalStrings(got, want) {
			t.Errorf("tagSources({}, \"tag-a\") = %v, want %v", got, want)
		}
	})
}

func equalStrings(a, b []string) bool {
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

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}

func TestHasUnfiled(t *testing.T) {
	cases := []struct {
		name  string
		feeds []*pb.Feed
		want  bool
	}{
		{"nil slice", nil, false},
		{"empty slice", []*pb.Feed{}, false},
		{"all filed", []*pb.Feed{{FolderId: "a"}, {FolderId: "b"}}, false},
		{"one unfiled among several", []*pb.Feed{{FolderId: "a"}, {FolderId: ""}}, true},
		{"all unfiled", []*pb.Feed{{FolderId: ""}, {FolderId: ""}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasUnfiled(c.feeds); got != c.want {
				t.Errorf("hasUnfiled(%v) = %v, want %v", c.feeds, got, c.want)
			}
		})
	}
}

// --- cursorY ---------------------------------------------------------------

func TestCursorY(t *testing.T) {
	cases := []struct {
		name  string
		index int
		h     float64
		want  string
	}{
		{"no selection parks at the top", -1, ItemRowHeight, "0px"},
		{"a more negative index still parks at the top", -100, ItemRowHeight, "0px"},
		{"first row", 0, ItemRowHeight, "0px"},
		{"fifth row (5 * 96px)", 5, ItemRowHeight, "480px"},
		{"a big index", 1000, ItemRowHeight, "96000px"},
		// The reason the height is a parameter at all: My Feed's rows are taller,
		// and a cursor computed against the standard row lands between two of them.
		{"My Feed's taller row", 5, MyFeedRowHeight, "660px"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cursorY(c.index, c.h); got != c.want {
				t.Errorf("cursorY(%d, %v) = %q, want %q", c.index, c.h, got, c.want)
			}
		})
	}
}

// --- sessionOf / liveSrcFor / modeName / modeWidth / boolAttr ------------------

func TestSessionOf(t *testing.T) {
	cases := []struct {
		name      string
		streamURL string
		want      string
	}{
		{"empty URL", "", ""},
		{"no query string at all", "https://x/stream", ""},
		{"s is the only param", "https://x/stream?s=abc123", "abc123"},
		{"s among several params", "https://x/stream?w=1600&s=abc123&h=1000", "abc123"},
		{"no s param present", "https://x/stream?w=1600&h=1000", ""},
		{"empty query string", "https://x/stream?", ""},
		{"s with an empty value", "https://x/stream?s=", ""},
		// Only the FIRST '=' should split the k/v pair; a value that itself
		// contains '=' (base64url-ish tokens sometimes do, though normally
		// escaped) should not truncate at the second one.
		{"value containing an internal '=' is kept whole", "https://x/stream?s=abc=def", "abc=def"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sessionOf(c.streamURL); got != c.want {
				t.Errorf("sessionOf(%q) = %q, want %q", c.streamURL, got, c.want)
			}
		})
	}
}

func TestLiveSrcFor(t *testing.T) {
	cases := []struct {
		name string
		src  string
		wide bool
		want string
	}{
		{"empty source stays empty regardless of width", "", true, ""},
		{"empty source stays empty (narrow)", "", false, ""},
		{"narrow sizes to 1100x760", "https://x/stream?s=1", false, "https://x/stream?s=1&w=1100&h=760"},
		{"wide sizes to 1600x1000", "https://x/stream?s=1", true, "https://x/stream?s=1&w=1600&h=1000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := liveSrcFor(c.src, c.wide); got != c.want {
				t.Errorf("liveSrcFor(%q, %v) = %q, want %q", c.src, c.wide, got, c.want)
			}
		})
	}
}

func TestModeNameAndWidth(t *testing.T) {
	if got := modeName(true); got != "live" {
		t.Errorf("modeName(true) = %q, want %q", got, "live")
	}
	if got := modeName(false); got != "doc" {
		t.Errorf("modeName(false) = %q, want %q", got, "doc")
	}
	if got := modeWidth(true); got != "wide" {
		t.Errorf("modeWidth(true) = %q, want %q", got, "wide")
	}
	if got := modeWidth(false); got != "col" {
		t.Errorf("modeWidth(false) = %q, want %q", got, "col")
	}
}

func TestBoolAttr(t *testing.T) {
	if got := boolAttr(true); got != "true" {
		t.Errorf("boolAttr(true) = %q, want %q", got, "true")
	}
	if got := boolAttr(false); got != "false" {
		t.Errorf("boolAttr(false) = %q, want %q", got, "false")
	}
}

// --- parsedBody + bodyCacheMax eviction -----------------------------------------

// resetBodyCache isolates a test from whatever earlier tests (or, in
// principle, earlier real use) left in the package-level cache.
func resetBodyCache(t *testing.T) {
	t.Helper()
	saved := bodyCache
	bodyCache = map[string]cachedBody{}
	t.Cleanup(func() { bodyCache = saved })
}

func TestParsedBody(t *testing.T) {
	resetBodyCache(t)

	t.Run("real content is not empty", func(t *testing.T) {
		nodes, empty := parsedBody("art-1", "<p>Hello, world</p>")
		if empty {
			t.Errorf("parsedBody with real content reported empty=true")
		}
		if nodes == nil {
			t.Errorf("parsedBody with real content returned nil nodes")
		}
	})

	t.Run("whitespace-only content is empty", func(t *testing.T) {
		nodes, empty := parsedBody("art-2", "   \n\t  ")
		if !empty {
			t.Errorf("parsedBody(whitespace) reported empty=false")
		}
		if nodes != nil {
			t.Errorf("parsedBody(whitespace) returned non-nil nodes: %v", nodes)
		}
	})

	t.Run("markup that sanitizes away to nothing is empty", func(t *testing.T) {
		// A comment or a disallowed tag with no visible text.
		_, empty := parsedBody("art-3", "<!-- just a comment -->")
		if !empty {
			t.Errorf("parsedBody(comment-only markup) reported empty=false")
		}
	})

	t.Run("empty string id and raw do not panic and report empty", func(t *testing.T) {
		_, empty := parsedBody("", "")
		if !empty {
			t.Errorf("parsedBody(\"\", \"\") reported empty=false")
		}
	})
}

// TestParsedBodyCachesByIDAndLength pins the documented cache key: a second
// call with the SAME id and the SAME byte length is a cache HIT even when the
// underlying content changed — the doc comment on bodyCache says this is a
// deliberate simplification ("length is a sufficient and free signal"), not
// an oversight, so this proves the mechanism rather than reporting a bug.
func TestParsedBodyCachesByIDAndLength(t *testing.T) {
	resetBodyCache(t)

	first := "<p>Hi</p>" // 9 bytes, sanitizes to real content -> empty=false
	_, empty1 := parsedBody("art-cache", first)
	if empty1 {
		t.Fatalf("priming call reported empty=true, test needs it to be false")
	}

	// Same length (9 bytes), now whitespace-only — freshly parsed this would
	// report empty=true. If the cache is doing what it documents, the second
	// call returns the FIRST result instead.
	second := strings.Repeat(" ", len(first))
	if len(second) != len(first) {
		t.Fatalf("test setup bug: lengths differ (%d vs %d)", len(second), len(first))
	}
	_, empty2 := parsedBody("art-cache", second)
	if empty2 {
		t.Errorf("parsedBody returned the FRESH (empty) result for a same-length " +
			"cache hit; want the cached (non-empty) result from the first call — " +
			"either the cache key changed or this call missed the cache")
	}

	// A DIFFERENT length for the same id must NOT hit the cache.
	third := "" // 0 bytes: definitely a different length
	_, empty3 := parsedBody("art-cache", third)
	if !empty3 {
		t.Errorf("parsedBody with a new length for an existing id returned the " +
			"stale cached result instead of re-parsing")
	}
}

func TestParsedBodyCacheEvictsWhollyAtMax(t *testing.T) {
	resetBodyCache(t)

	// Fill the cache to exactly its cap directly, rather than by calling
	// parsedBody bodyCacheMax times — this test is about the eviction policy,
	// not about re-proving parsing works.
	for i := 0; i < bodyCacheMax; i++ {
		bodyCache[strconv.Itoa(i)] = cachedBody{rawLen: 1}
	}
	if len(bodyCache) != bodyCacheMax {
		t.Fatalf("test setup: bodyCache has %d entries, want exactly %d", len(bodyCache), bodyCacheMax)
	}

	// One more call, for a brand new id: since the cache is already at (not
	// past) the cap, this must clear the WHOLE map rather than evict a single
	// entry — that is the documented policy ("dropping the whole map on
	// overflow ... needs no bookkeeping to be correct").
	parsedBody("brand-new", "x")

	if len(bodyCache) != 1 {
		t.Errorf("after crossing bodyCacheMax, bodyCache has %d entries, want "+
			"exactly 1 (only the entry just added) — the whole map should have "+
			"been cleared first", len(bodyCache))
	}
	if _, stillThere := bodyCache["0"]; stillThere {
		t.Errorf("entry \"0\" survived the cache clear; the whole map should have been dropped")
	}
	if _, present := bodyCache["brand-new"]; !present {
		t.Errorf("the entry that triggered the clear was not itself inserted afterwards")
	}
}
