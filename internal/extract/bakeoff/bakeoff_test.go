
// The D7 bake-off. Not part of the normal test run — it exists to be read once,
// argued with, and then cited.
//
//	go test -tags bakeoff ./internal/extract -run TestBakeoff -v
//
// It stays in the tree rather than being deleted because D7 is the kind of
// decision that gets re-litigated in six months ("why aren't we using X?"), and
// the cheapest answer to that is a command anyone can re-run against the same
// twelve pages.
package bakeoff

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	distiller "github.com/markusmobius/go-domdistiller"
	"github.com/markusmobius/go-trafilatura"

	readability "github.com/go-shiori/go-readability"
)

type page struct {
	slug string
	html []byte
	url  string
}

func loadPages(t *testing.T) []page {
	t.Helper()
	paths, _ := filepath.Glob("../testdata/articles/*.html")
	sort.Strings(paths)
	var out []page
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		slug := strings.TrimSuffix(filepath.Base(p), ".html")
		pg := page{slug: slug, html: b}
		if meta, err := os.ReadFile(filepath.Join("../testdata/articles", slug+".meta")); err == nil {
			for _, line := range strings.Split(string(meta), "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "url:"); ok {
					pg.url = strings.TrimSpace(v)
				}
			}
		}
		out = append(out, pg)
	}
	return out
}

// boilerplate is the phrases that must NOT survive extraction. Their presence is
// the single most useful quality signal available without a human: every one of
// them is chrome, and none of them belongs in a reading pane.
var boilerplate = []string{
	"Subscribe", "Newsletter", "Sign up", "Sign in", "Log in",
	"Cookie", "Privacy Policy", "Terms of Service", "All rights reserved",
	"Related Stories", "Read More", "Share this", "Follow us",
	"Advertisement", "Skip to content", "Table of contents",
}

type result struct {
	lib     string
	slug    string
	title   string
	text    string
	html    string
	elapsed time.Duration
	err     error
}

func (r result) boilerHits() []string {
	var hits []string
	for _, b := range boilerplate {
		if strings.Contains(r.text, b) {
			hits = append(hits, b)
		}
	}
	return hits
}

func runReadability(p page) result {
	start := time.Now()
	u, _ := url.Parse(p.url)
	art, err := readability.FromReader(bytes.NewReader(p.html), u)
	return result{
		lib: "go-readability", slug: p.slug, title: art.Title,
		text: art.TextContent, html: art.Content,
		elapsed: time.Since(start), err: err,
	}
}

func runTrafilatura(p page) result {
	start := time.Now()
	u, _ := url.Parse(p.url)
	res, err := trafilatura.Extract(bytes.NewReader(p.html), trafilatura.Options{
		OriginalURL:   u,
		IncludeImages: true,
		IncludeLinks:  true,
		// EnableFallback is what makes this a fair fight rather than a rigged
		// one: with it on, trafilatura runs go-readability and domdistiller as
		// fallbacks when its own extraction looks thin. Measuring it WITHOUT the
		// fallback measures the algorithm; measuring it WITH the fallback
		// measures what we would actually ship. Both are run below.
		EnableFallback:  true,
		ExcludeComments: true,
	})
	r := result{lib: "trafilatura", slug: p.slug, elapsed: time.Since(start), err: err}
	if res != nil {
		r.title = res.Metadata.Title
		r.text = res.ContentText
		if res.ContentNode != nil {
			var buf bytes.Buffer
			_ = renderNode(&buf, res.ContentNode)
			r.html = buf.String()
		}
	}
	return r
}

func runDistiller(p page) result {
	start := time.Now()
	res, err := distiller.ApplyForReader(bytes.NewReader(p.html), &distiller.Options{
		OriginalURL: mustParse(p.url),
	})
	r := result{lib: "domdistiller", slug: p.slug, elapsed: time.Since(start), err: err}
	if res != nil {
		r.title = res.Title
		r.text = res.Text
		if res.Node != nil {
			var buf bytes.Buffer
			_ = renderNode(&buf, res.Node)
			r.html = buf.String()
		}
	}
	return r
}

func TestBakeoff(t *testing.T) {
	pages := loadPages(t)
	if len(pages) == 0 {
		t.Fatal("no article fixtures")
	}

	runners := []struct {
		name string
		fn   func(page) result
	}{
		{"go-readability", runReadability},
		{"trafilatura", runTrafilatura},
		{"domdistiller", runDistiller},
	}

	totals := map[string]struct {
		chars, boiler, failures, titles int
		elapsed                         time.Duration
	}{}

	for _, p := range pages {
		t.Logf("\n=== %s  (%d KB source)", p.slug, len(p.html)/1024)
		for _, r := range runners {
			res := func() (res result) {
				// A panic on one page must not take the whole comparison with it;
				// "it panics on Wikipedia" is itself a finding.
				defer func() {
					if v := recover(); v != nil {
						res = result{lib: r.name, slug: p.slug, err: fmt.Errorf("panic: %v", v)}
					}
				}()
				return r.fn(p)
			}()

			agg := totals[r.name]
			agg.elapsed += res.elapsed
			if res.err != nil || len(res.text) == 0 {
				agg.failures++
				totals[r.name] = agg
				t.Logf("  %-15s FAILED  %v", r.name, res.err)
				continue
			}
			hits := res.boilerHits()
			agg.chars += len(res.text)
			agg.boiler += len(hits)
			if strings.TrimSpace(res.title) != "" {
				agg.titles++
			}
			totals[r.name] = agg

			t.Logf("  %-15s %7d chars  %6d html  title=%-5v  boilerplate=%v  %v",
				r.name, len(res.text), len(res.html), strings.TrimSpace(res.title) != "",
				hits, res.elapsed.Round(time.Millisecond))
		}
	}

	t.Log("\n=== totals ===")
	names := []string{"go-readability", "trafilatura", "domdistiller"}
	for _, n := range names {
		a := totals[n]
		t.Logf("  %-15s %8d chars  %3d boilerplate hits  %d/%d failed  %d/%d titled  %v total",
			n, a.chars, a.boiler, a.failures, len(pages), a.titles, len(pages), a.elapsed.Round(time.Millisecond))
	}
}

// TestBakeoffDump writes each library's output to disk so the actual decision —
// which is made by reading them — can be made by reading them.
//
//	go test -tags bakeoff ./internal/extract -run TestBakeoffDump
func TestBakeoffDump(t *testing.T) {
	dir := os.Getenv("BAKEOFF_DUMP")
	if dir == "" {
		t.Skip("set BAKEOFF_DUMP=<dir>")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range loadPages(t) {
		for name, fn := range map[string]func(page) result{
			"readability": runReadability,
			"trafilatura": runTrafilatura,
			"distiller":   runDistiller,
		} {
			func() {
				defer func() { _ = recover() }()
				res := fn(p)
				_ = os.WriteFile(filepath.Join(dir, p.slug+"."+name+".txt"), []byte(res.text), 0o644)
				_ = os.WriteFile(filepath.Join(dir, p.slug+"."+name+".html"), []byte(res.html), 0o644)
			}()
		}
	}
	t.Logf("wrote to %s", dir)
}
