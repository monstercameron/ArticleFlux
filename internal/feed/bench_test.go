package feed

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The poll path, measured on the committed corpus.
//
// # Why the corpus and not a synthetic document
//
// Because the corpus is 27 real feeds, and the shape of real feeds is the whole
// cost. A generated RSS document is uniform: every entry the same length, every
// date the same format, no CDATA, no namespaced extensions, no eight-hundred-
// item archive feed. The corpus has a 1,012-episode podcast feed in it, and the
// per-entry work that costs is exactly what a synthetic twenty-item fixture
// would hide.
//
// It is also the input the correctness tests use, so a change that makes this
// faster and TestCorpusParses fail is caught in the same package by the same
// fixtures.
//
// # What it measures, and what it does not
//
// ParseBytes only: the format detection, gofeed's parse, and Normalize. There
// is no network here, deliberately — a poll's wall-clock time is dominated by
// the fetch, and the fetch is not something this codebase can make faster. What
// it CAN make faster is the CPU it spends per entry once the bytes have
// arrived, which for a 150-feed subscription is the part that runs 150 times in
// a row on one background worker.
//
//	go test ./internal/feed -run '^$' -bench . -benchmem

// corpusFixtures loads the corpus once.
//
// Reading 8MB off disk inside the timed region would measure the filesystem's
// page cache, which is neither interesting nor stable.
//
// testing.TB rather than *testing.B because summarize_equiv_test.go's
// comparison needs the same inputs the benchmark uses — a guard that agrees on
// hand-written strings and a benchmark that runs on real feeds would be
// measuring and checking two different things.
func corpusFixtures(b testing.TB) []fixture {
	b.Helper()
	paths, err := filepath.Glob(filepath.Join(corpusDir, "*.raw"))
	if err != nil || len(paths) == 0 {
		b.Skipf("no corpus in %s", corpusDir)
	}
	out := make([]fixture, 0, len(paths))
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if err != nil {
			b.Fatal(err)
		}
		slug := strings.TrimSuffix(filepath.Base(p), ".raw")
		ct := ""
		if meta, err := os.ReadFile(filepath.Join(corpusDir, slug+".meta")); err == nil {
			ct = contentTypeFromMeta(string(meta))
		}
		out = append(out, fixture{slug: slug, body: body, contentType: ct})
	}
	return out
}

// contentTypeFromMeta pulls the Content-Type line out of a .meta sidecar.
//
// Tolerant of a missing header on purpose: ParseBytes sniffs when the content
// type is empty, and a fixture whose capture lost the header should measure the
// sniffing path rather than skip.
func contentTypeFromMeta(meta string) string {
	for line := range strings.Lines(meta) {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "content-type") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

var benchNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// The whole corpus, once per iteration. One number for "what does a poll cycle
// cost", which is the number that moves when anything in Normalize changes.
func BenchmarkParseCorpus(b *testing.B) {
	fixtures := corpusFixtures(b)
	f := NewFetcher()

	var bytes int64
	for _, fx := range fixtures {
		bytes += int64(len(fx.body))
	}

	b.ReportAllocs()
	b.SetBytes(bytes)
	for b.Loop() {
		for _, fx := range fixtures {
			if _, err := f.ParseBytes(fx.body, fx.contentType,
				"https://fixture.example/"+fx.slug, benchNow); err != nil {
				b.Fatalf("%s: %v", fx.slug, err)
			}
		}
	}
}

// And per fixture, because the aggregate above hides which format is expensive.
// A win in the Atom path and a loss in the JSON Feed path can cancel exactly in
// the total and be visible here.
func BenchmarkParseFixture(b *testing.B) {
	f := NewFetcher()
	for _, fx := range corpusFixtures(b) {
		b.Run(fx.slug, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(fx.body)))
			for b.Loop() {
				if _, err := f.ParseBytes(fx.body, fx.contentType,
					"https://fixture.example/"+fx.slug, benchNow); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
