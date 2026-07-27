// Command corpus refreshes the committed feed fixtures in
// internal/feed/testdata/corpus (TODO 4.2, D1).
//
// The corpus exists because 151 live subscriptions are a worse regression test
// than 25 committed files. Live feeds prove the parser works on whatever those
// publishers happen to be serving this morning; they cannot prove it still works
// on a format nobody here is subscribed to, and they cannot fail in CI, which is
// the only place a regression gets caught before it ships.
//
// So: fetch verbatim, commit the bytes, never touch them again. A fixture that
// gets "cleaned up" has stopped being evidence — the broken ones are the point.
// The rule the plan sets is that every future parser bug adds a fixture BEFORE it
// gets a fix, and this command is how that fixture gets added:
//
//	go run ./internal/tools/corpus -add https://example.com/feed.xml -slug example-rss20
//
// Refetching everything (rarely wanted, and never as a way to make a test pass):
//
//	go run ./internal/tools/corpus -refresh
package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/feed"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
)

const (
	corpusDir  = "internal/feed/testdata/corpus"
	sourcesTxt = "sources.tsv"
	maxBody    = 8 << 20 // fixtures are committed; a 9 MB one is not a fixture
)

// entry is one line of sources.tsv: slug, url, note.
type entry struct {
	Slug string
	URL  string
	Note string
}

func main() {
	var (
		add     = flag.String("add", "", "feed URL to add to the corpus")
		slug    = flag.String("slug", "", "file name stem for -add")
		note    = flag.String("note", "", "what this fixture is evidence of")
		refresh = flag.Bool("refresh", false, "refetch every fixture (destroys evidence; be sure)")
	)
	flag.Parse()

	root, err := findRoot()
	if err != nil {
		fail(err)
	}
	dir := filepath.Join(root, corpusDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}

	entries, err := readSources(dir)
	if err != nil {
		fail(err)
	}

	switch {
	case *add != "":
		if *slug == "" {
			fail(fmt.Errorf("-add needs -slug"))
		}
		for _, e := range entries {
			if e.Slug == *slug {
				fail(fmt.Errorf("slug %q already in the corpus", *slug))
			}
		}
		e := entry{Slug: *slug, URL: *add, Note: *note}
		if err := fetchOne(dir, e); err != nil {
			fail(err)
		}
		entries = append(entries, e)
		if err := writeSources(dir, entries); err != nil {
			fail(err)
		}
		fmt.Printf("added %s\n", *slug)

	case *refresh:
		for _, e := range entries {
			if e.URL == "-" {
				// Hand-written fixtures. Atom 0.3 and RSS 0.91 have no live
				// publishers left to fetch from, and the malformed cases are
				// composites of values seen in the wild — refetching would erase
				// them.
				fmt.Printf("  -- %-28s hand-written, not fetched\n", e.Slug)
				continue
			}
			if err := fetchOne(dir, e); err != nil {
				fmt.Printf("  !! %-28s %v\n", e.Slug, err)
				continue
			}
			fmt.Printf("  ok %-28s %s\n", e.Slug, e.URL)
		}

	default:
		// Default is a report, not a fetch: the common reason to run this is to
		// find out what the corpus covers, and defaulting to network traffic
		// against 25 publishers because someone typed the command name is rude.
		for _, e := range entries {
			missing := ""
			if _, err := os.Stat(filepath.Join(dir, e.Slug+".raw")); err != nil {
				missing = "  (MISSING)"
			}
			fmt.Printf("%-28s %s%s\n", e.Slug, e.URL, missing)
		}
		fmt.Printf("\n%d fixtures. -add to grow it, -refresh to refetch.\n", len(entries))
	}
}

// fetchOne saves the response body byte-for-byte. No decoding, no
// pretty-printing, no normalising line endings: a fixture is a recording of what
// a real server sent, and every transformation applied on the way in is a bug
// class the corpus can no longer catch.
func fetchOne(dir string, e entry) error {
	client := netguard.Client(netguard.Options{
		Timeout:     45 * time.Second,
		DialTimeout: 15 * time.Second,
		UserAgent:   feed.UserAgent,
	})
	req, err := http.NewRequest(http.MethodGet, e.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept",
		"application/atom+xml, application/rss+xml, application/feed+json, application/xml;q=0.9, */*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxBody {
		return fmt.Errorf("body over %d bytes", maxBody)
	}

	if err := os.WriteFile(filepath.Join(dir, e.Slug+".raw"), body, 0o644); err != nil {
		return err
	}
	// The Content-Type is part of the fixture. charsetdec's whole job is
	// reconciling it against the XML declaration, and a fixture without it cannot
	// exercise the disagreement case.
	meta := "url: " + e.URL + "\ncontent-type: " + resp.Header.Get("Content-Type") + "\n"
	return os.WriteFile(filepath.Join(dir, e.Slug+".meta"), []byte(meta), 0o644)
}

func readSources(dir string) ([]entry, error) {
	b, err := os.ReadFile(filepath.Join(dir, sourcesTxt))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		e := entry{Slug: f[0], URL: f[1]}
		if len(f) > 2 {
			e.Note = f[2]
		}
		out = append(out, e)
	}
	return out, nil
}

func writeSources(dir string, entries []entry) error {
	sort.Slice(entries, func(i, j int) bool { return entries[i].Slug < entries[j].Slug })
	var b strings.Builder
	b.WriteString("# slug\turl\tnote — TODO 4.2. Append-only in spirit: a fixture is evidence.\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\t%s\t%s\n", e.Slug, e.URL, e.Note)
	}
	return os.WriteFile(filepath.Join(dir, sourcesTxt), []byte(b.String()), 0o644)
}

// findRoot walks up for go.mod so the command works from anywhere in the tree.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("corpus: no go.mod above %s", dir)
		}
		dir = parent
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "corpus:", err)
	os.Exit(1)
}
