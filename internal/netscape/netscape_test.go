package netscape

import (
	"strings"
	"testing"
	"time"
)

// A real export's shape: unclosed <DT> and <p>, uppercase tags, nested folders,
// tags, descriptions, and an ADD_DATE. Written the way a browser writes it
// rather than the way a tidy person would.
const firefoxExport = `<!DOCTYPE NETSCAPE-Bookmark-file-1>
<!-- This is an automatically generated file.
     It will be read and overwritten.
     DO NOT EDIT! -->
<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">
<TITLE>Bookmarks</TITLE>
<H1>Bookmarks Menu</H1>

<DL><p>
    <DT><H3 ADD_DATE="1300000000" LAST_MODIFIED="1400000000">Reading</H3>
    <DL><p>
        <DT><A HREF="https://danluu.com/" ADD_DATE="1300000001" TAGS="engineering,blogs">Dan Luu</A>
        <DD>Long-form engineering writing
        <DT><A HREF="https://simonwillison.net/" ADD_DATE="1300000002">Simon Willison</A>
        <DT><H3>Go</H3>
        <DL><p>
            <DT><A HREF="https://go.dev/blog/" ADD_DATE="1300000003">The Go Blog</A>
        </DL><p>
        <DT><H3>Rust</H3>
        <DL><p>
            <DT><A HREF="https://blog.rust-lang.org/" ADD_DATE="1300000004">Rust Blog</A>
        </DL><p>
    </DL><p>
    <DT><A HREF="https://example.com/top" ADD_DATE="1300000005">Top level</A>
    <DT><A HREF="javascript:alert(1)" ADD_DATE="1300000006">A bookmarklet</A>
    <DT><A HREF="https://example.com/untitled"></A>
</DL><p>
`

func find(bs []Bookmark, url string) (Bookmark, bool) {
	for _, b := range bs {
		if b.URL == url {
			return b, true
		}
	}
	return Bookmark{}, false
}

func TestParseRealExport(t *testing.T) {
	got, err := Parse(strings.NewReader(firefoxExport))
	if err != nil {
		t.Fatal(err)
	}

	// Six anchors, minus the javascript: one, which is code rather than a
	// bookmark and would execute when clicked.
	if len(got) != 6 {
		t.Fatalf("got %d bookmarks: %+v", len(got), got)
	}

	t.Run("nesting comes from document order, not from the tree", func(t *testing.T) {
		b, ok := find(got, "https://go.dev/blog/")
		if !ok {
			t.Fatal("missing")
		}
		want := []string{"Reading", "Go"}
		if strings.Join(b.Folder, "/") != strings.Join(want, "/") {
			t.Errorf("folder = %v, want %v", b.Folder, want)
		}
	})

	// The bug that only shows up with two folders at the same depth: if the path
	// slice is appended in place, the second sibling overwrites the first's name
	// in every bookmark already collected.
	t.Run("sibling folders do not overwrite each other", func(t *testing.T) {
		g, _ := find(got, "https://go.dev/blog/")
		r, _ := find(got, "https://blog.rust-lang.org/")
		if strings.Join(g.Folder, "/") != "Reading/Go" {
			t.Errorf("Go bookmark ended up in %v", g.Folder)
		}
		if strings.Join(r.Folder, "/") != "Reading/Rust" {
			t.Errorf("Rust bookmark ended up in %v", r.Folder)
		}
	})

	t.Run("top level has no folder", func(t *testing.T) {
		b, _ := find(got, "https://example.com/top")
		if len(b.Folder) != 0 {
			t.Errorf("folder = %v, want none", b.Folder)
		}
	})

	t.Run("tags", func(t *testing.T) {
		b, _ := find(got, "https://danluu.com/")
		if strings.Join(b.Tags, ",") != "engineering,blogs" {
			t.Errorf("tags = %v", b.Tags)
		}
	})

	t.Run("description from DD", func(t *testing.T) {
		b, _ := find(got, "https://danluu.com/")
		if b.Description != "Long-form engineering writing" {
			t.Errorf("description = %q", b.Description)
		}
	})

	t.Run("add date", func(t *testing.T) {
		b, _ := find(got, "https://danluu.com/")
		if b.AddedAt.Unix() != 1300000001 {
			t.Errorf("added at %v", b.AddedAt)
		}
	})

	// A javascript: URL in a bookmark file is a bookmarklet. Importing one as a
	// link produces an entry that runs code when clicked.
	t.Run("bookmarklets are refused", func(t *testing.T) {
		if _, ok := find(got, "javascript:alert(1)"); ok {
			t.Error("a javascript: bookmarklet was imported as a bookmark")
		}
	})

	// An untitled bookmark is legal and common. An empty row is worse than a URL.
	t.Run("untitled falls back to the URL", func(t *testing.T) {
		b, ok := find(got, "https://example.com/untitled")
		if !ok {
			t.Fatal("the untitled bookmark was dropped")
		}
		if b.Title != "https://example.com/untitled" {
			t.Errorf("title = %q", b.Title)
		}
	})
}

// Out, in, identical. The same bar 4.5's OPML round-trip has to clear.
func TestRoundTrip(t *testing.T) {
	original := []Bookmark{
		{Title: "Dan Luu", URL: "https://danluu.com/", Folder: []string{"Reading"},
			Tags: []string{"engineering", "blogs"}, AddedAt: time.Unix(1300000001, 0).UTC(),
			Description: "Long-form engineering writing"},
		{Title: "The Go Blog", URL: "https://go.dev/blog/", Folder: []string{"Reading", "Go"},
			AddedAt: time.Unix(1300000003, 0).UTC()},
		{Title: "Rust Blog", URL: "https://blog.rust-lang.org/", Folder: []string{"Reading", "Rust"},
			AddedAt: time.Unix(1300000004, 0).UTC()},
		{Title: "Top level", URL: "https://example.com/top", AddedAt: time.Unix(1300000005, 0).UTC()},
		{Title: "Deep", URL: "https://example.com/deep",
			Folder: []string{"A", "B", "C", "D"}, AddedAt: time.Unix(1300000006, 0).UTC()},
	}

	var out strings.Builder
	if err := Write(&out, original); err != nil {
		t.Fatal(err)
	}
	back, err := Parse(strings.NewReader(out.String()))
	if err != nil {
		t.Fatalf("our own output did not parse: %v\n%s", err, out.String())
	}
	if len(back) != len(original) {
		t.Fatalf("wrote %d, read back %d\n%s", len(original), len(back), out.String())
	}

	for _, want := range original {
		got, ok := find(back, want.URL)
		if !ok {
			t.Errorf("%s did not survive the round trip", want.URL)
			continue
		}
		if got.Title != want.Title {
			t.Errorf("%s: title %q became %q", want.URL, want.Title, got.Title)
		}
		if strings.Join(got.Folder, "/") != strings.Join(want.Folder, "/") {
			t.Errorf("%s: folder %v became %v", want.URL, want.Folder, got.Folder)
		}
		if strings.Join(got.Tags, ",") != strings.Join(want.Tags, ",") {
			t.Errorf("%s: tags %v became %v", want.URL, want.Tags, got.Tags)
		}
		if !got.AddedAt.Equal(want.AddedAt) {
			t.Errorf("%s: added %v became %v", want.URL, want.AddedAt, got.AddedAt)
		}
		if got.Description != want.Description {
			t.Errorf("%s: description %q became %q", want.URL, want.Description, got.Description)
		}
	}
}

// The export must look like what browsers emit, because they parse it by
// convention rather than by specification and a tidied file is one some
// importers get wrong.
func TestExportMatchesTheBrowserShape(t *testing.T) {
	var out strings.Builder
	if err := Write(&out, []Bookmark{{Title: "X", URL: "https://x.tld/"}}); err != nil {
		t.Fatal(err)
	}
	s := out.String()

	for _, want := range []string{
		"<!DOCTYPE NETSCAPE-Bookmark-file-1>",
		"DO NOT EDIT!",
		`<META HTTP-EQUIV="Content-Type" CONTENT="text/html; charset=UTF-8">`,
		"<H1>Bookmarks</H1>",
		"<DL><p>",
		"<DT><A HREF=",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("export is missing %q:\n%s", want, s)
		}
	}
	// Closing these is the tidy thing to do and the wrong thing to do.
	if strings.Contains(s, "</DT>") {
		t.Error("export closes <DT>, which browsers do not")
	}
}

func TestExportEscapes(t *testing.T) {
	var out strings.Builder
	err := Write(&out, []Bookmark{{
		Title:  `Tom & Jerry's "<big>" adventure`,
		URL:    "https://x.tld/?a=1&b=2",
		Folder: []string{`R&D`},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if strings.Contains(s, "<big>") {
		t.Errorf("an unescaped tag reached the output:\n%s", s)
	}
	if !strings.Contains(s, "a=1&amp;b=2") {
		t.Errorf("the URL's ampersand was not escaped:\n%s", s)
	}

	back, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 {
		t.Fatalf("got %d back", len(back))
	}
	if back[0].Title != `Tom & Jerry's "<big>" adventure` {
		t.Errorf("title came back as %q", back[0].Title)
	}
	if back[0].URL != "https://x.tld/?a=1&b=2" {
		t.Errorf("url came back as %q", back[0].URL)
	}
}

func TestEmptyAndJunkInputs(t *testing.T) {
	for _, in := range []string{"", "not html at all", "<html><body></body></html>", "<DL><p></DL><p>"} {
		got, err := Parse(strings.NewReader(in))
		if err != nil {
			t.Errorf("Parse(%q) errored: %v", in, err)
		}
		if len(got) != 0 {
			t.Errorf("Parse(%q) invented %d bookmarks", in, len(got))
		}
	}

	var out strings.Builder
	if err := Write(&out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "<!DOCTYPE NETSCAPE-Bookmark-file-1>") {
		t.Error("an empty export is not a valid file")
	}
}

// --- Chrome JSON -----------------------------------------------------------

const chromeJSON = `{
  "checksum": "abc",
  "roots": {
    "bookmark_bar": {
      "children": [
        {"date_added": "13350000000000000", "name": "Dan Luu", "type": "url", "url": "https://danluu.com/"},
        {"children": [
            {"date_added": "13350000000000001", "name": "The Go Blog", "type": "url", "url": "https://go.dev/blog/"}
         ],
         "name": "Go", "type": "folder"}
      ],
      "name": "Bookmarks bar",
      "type": "folder"
    },
    "other": {
      "children": [
        {"date_added": "13350000000000002", "name": "Unfiled", "type": "url", "url": "https://example.com/unfiled"},
        {"date_added": "13350000000000003", "name": "Bad", "type": "url", "url": "javascript:alert(1)"},
        {"date_added": "0", "name": "No date", "type": "url", "url": "https://example.com/nodate"}
      ],
      "name": "Other bookmarks",
      "type": "folder"
    },
    "sync_transaction_version": "1"
  },
  "version": 1
}`

func TestParseChrome(t *testing.T) {
	got, err := ParseChrome(strings.NewReader(chromeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d bookmarks: %+v", len(got), got)
	}

	t.Run("bookmark bar becomes a folder", func(t *testing.T) {
		b, _ := find(got, "https://danluu.com/")
		if strings.Join(b.Folder, "/") != "Bookmarks bar" {
			t.Errorf("folder = %v", b.Folder)
		}
	})
	t.Run("nested folders", func(t *testing.T) {
		b, _ := find(got, "https://go.dev/blog/")
		if strings.Join(b.Folder, "/") != "Bookmarks bar/Go" {
			t.Errorf("folder = %v", b.Folder)
		}
	})
	// "Other bookmarks" is where Chrome puts everything unfiled. Wrapping the
	// import in a folder nobody chose is worse than putting it at the top.
	t.Run("other bookmarks lands at the top level", func(t *testing.T) {
		b, _ := find(got, "https://example.com/unfiled")
		if len(b.Folder) != 0 {
			t.Errorf("folder = %v, want none", b.Folder)
		}
	})
	t.Run("bookmarklets are refused here too", func(t *testing.T) {
		if _, ok := find(got, "javascript:alert(1)"); ok {
			t.Error("a bookmarklet was imported")
		}
	})
	// Microseconds since 1601. Misreading this produces bookmarks dated 1601
	// that sort to the bottom of every list and look like data loss.
	t.Run("the WebKit epoch", func(t *testing.T) {
		b, _ := find(got, "https://danluu.com/")
		if b.AddedAt.Year() != 2024 {
			t.Errorf("13350000000000000 parsed to %v; expected January 2024", b.AddedAt)
		}
	})
	t.Run("a zero date is no date, not 1601", func(t *testing.T) {
		b, _ := find(got, "https://example.com/nodate")
		if !b.AddedAt.IsZero() {
			t.Errorf("added at %v, want zero", b.AddedAt)
		}
	})
	t.Run("non-node roots are skipped rather than refused", func(t *testing.T) {
		// sync_transaction_version is a string sitting among the roots. Its
		// presence must not fail the whole import.
		if len(got) == 0 {
			t.Error("the file was refused")
		}
	})
}

func TestParseChromeIsDeterministic(t *testing.T) {
	// Roots is a map, so without an explicit sort the order changes per run.
	var first []string
	for i := 0; i < 8; i++ {
		got, err := ParseChrome(strings.NewReader(chromeJSON))
		if err != nil {
			t.Fatal(err)
		}
		var order []string
		for _, b := range got {
			order = append(order, b.URL)
		}
		if i == 0 {
			first = order
			continue
		}
		if strings.Join(order, ",") != strings.Join(first, ",") {
			t.Fatalf("import order varies between runs:\n  %v\n  %v", first, order)
		}
	}
}

func TestParseChromeRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "{}", `{"roots":{}}`, "not json"} {
		if _, err := ParseChrome(strings.NewReader(in)); err == nil {
			t.Errorf("ParseChrome(%q) accepted it", in)
		}
	}
}

// Chrome in, Netscape out — the actual migration path someone takes.
func TestChromeImportExportsCleanly(t *testing.T) {
	imported, err := ParseChrome(strings.NewReader(chromeJSON))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := Write(&out, imported); err != nil {
		t.Fatal(err)
	}
	back, err := Parse(strings.NewReader(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(imported) {
		t.Errorf("imported %d from Chrome, exported and read back %d", len(imported), len(back))
	}
}

func TestParseEpochUnits(t *testing.T) {
	cases := []struct {
		in   string
		year int
	}{
		{"1300000001", 2011},        // seconds
		{"1300000001000", 2011},     // milliseconds
		{"13350000000000000", 2024}, // microseconds since 1601 (WebKit)
		{"1780000000000000", 2026},  // microseconds since 1970 — one order of magnitude away
		{"0", 1},                    // zero time
		{"", 1},                     // absent
		{"not a number", 1},         // junk
		{"-5", 1},                   // negative
	}
	for _, c := range cases {
		got := parseEpoch(c.in)
		if c.year == 1 {
			if !got.IsZero() {
				t.Errorf("parseEpoch(%q) = %v, want zero", c.in, got)
			}
			continue
		}
		if got.Year() != c.year {
			t.Errorf("parseEpoch(%q) = %v, want a date in %d", c.in, got, c.year)
		}
	}
}
