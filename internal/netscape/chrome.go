package netscape

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Chrome's JSON bookmarks file (TODO 4.6, import only).
//
// Import only, and deliberately so: Chrome reads the Netscape HTML format
// perfectly well, so writing its JSON would be a second export format serving no
// destination that the first does not already reach. Reading it is worth doing
// because the JSON file is what someone finds when they go looking in their
// profile directory, and telling them to export first is a step that loses
// people.
//
// Two things about the format are worth knowing before reading the code. Every
// numeric field is a STRING, including the ids and the timestamps. And the
// timestamps are microseconds since 1601-01-01, the WebKit epoch, which is
// neither unix seconds nor anything else in this codebase.

type chromeFile struct {
	Roots   map[string]json.RawMessage `json:"roots"`
	Version int                        `json:"version"`
}

type chromeNode struct {
	Type      string       `json:"type"` // "url" | "folder"
	Name      string       `json:"name"`
	URL       string       `json:"url"`
	DateAdded string       `json:"date_added"`
	Children  []chromeNode `json:"children"`
}

// ParseChrome reads a Chrome/Chromium/Edge Bookmarks JSON file.
func ParseChrome(r io.Reader) ([]Bookmark, error) {
	var f chromeFile
	if err := json.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("netscape: chrome bookmarks: %w", err)
	}
	if len(f.Roots) == 0 {
		return nil, fmt.Errorf("netscape: no bookmark roots; this does not look like a Chrome Bookmarks file")
	}

	// Roots is a map, so iteration order is random. Sorting makes the import
	// deterministic — without it, two imports of the same file produce the same
	// bookmarks in a different order, and any test comparing output is flaky in
	// a way that takes an afternoon to attribute.
	names := make([]string, 0, len(f.Roots))
	for name := range f.Roots {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []Bookmark
	for _, name := range names {
		var root chromeNode
		if err := json.Unmarshal(f.Roots[name], &root); err != nil {
			// "sync_transaction_version" and friends live alongside the roots and
			// are not nodes. Skipping them beats refusing the file.
			continue
		}
		// The root's OWN name is discarded and replaced by rootName's. Chrome
		// localises it — a German profile says "Lesezeichenleiste" — so keying
		// off the stable map key rather than the display string is what makes
		// the import behave the same on every machine.
		//
		// The children are walked directly rather than passing the root node to
		// collect, because collect would then append the root's name a second
		// time and every bookmark would land in "Bookmarks bar/Bookmarks bar".
		var path []string
		if display := rootName(name); display != "" {
			path = []string{display}
		}
		for _, c := range root.Children {
			collect(c, path, &out)
		}
	}
	return out, nil
}

func rootName(key string) string {
	switch key {
	case "other":
		return "" // unfiled; top level here
	case "bookmark_bar":
		return "Bookmarks bar"
	case "synced":
		return "Mobile bookmarks"
	default:
		return ""
	}
}

func collect(n chromeNode, path []string, out *[]Bookmark) {
	switch n.Type {
	case "url":
		if n.URL == "" || isScriptURL(n.URL) {
			return
		}
		b := Bookmark{
			Title:   strings.TrimSpace(n.Name),
			URL:     strings.TrimSpace(n.URL),
			AddedAt: parseChromeTime(n.DateAdded),
		}
		if len(path) > 0 {
			b.Folder = append([]string{}, path...)
		}
		if b.Title == "" {
			b.Title = b.URL
		}
		*out = append(*out, b)

	case "folder":
		child := path
		if name := strings.TrimSpace(n.Name); name != "" {
			// Copied rather than appended in place, for the reason in walk():
			// two sibling folders would otherwise share a backing array and the
			// second would rewrite the first's name in every bookmark already
			// collected.
			child = append(append([]string{}, path...), name)
		}
		for _, c := range n.Children {
			collect(c, child, out)
		}

	default:
		// A root node arrives with no type set. Recurse rather than dropping it,
		// or the entire bookmarks bar is silently skipped.
		for _, c := range n.Children {
			collect(c, path, out)
		}
	}
}

// The WebKit epoch: 1601-01-01, which is the Windows FILETIME epoch, which
// exists for reasons lost to history. Chrome writes microseconds from it.
const webkitEpochOffset = 11644473600

func fromWebKit(micros int64) time.Time {
	secs := micros/1e6 - webkitEpochOffset
	if secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0).UTC()
}

// parseChromeTime reads Chrome's date_added.
//
// Delegates to parseEpoch rather than assuming WebKit microseconds, because
// Chrome's own HTML export writes the same field in unix seconds — so the two
// importers would otherwise disagree about the same bookmark depending on which
// file the reader happened to hand us. One units table, not two.
func parseChromeTime(s string) time.Time { return parseEpoch(s) }
