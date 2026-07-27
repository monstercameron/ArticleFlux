package main

import "testing"

// parseLine has to survive the format `go test` actually emits, which is not one
// format: -benchmem adds two columns and b.SetBytes adds another, and this suite
// has benchmarks with every combination of the two. A positional reader passes
// its author's hand-written example and then misreads half the real file.
func TestParseLineReadsEveryColumnLayout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		line    string
		want    sample
		wantNam string
		ok      bool
	}{
		{
			name:    "bare",
			line:    "BenchmarkPhrases-18   \t  277622\t      4477 ns/op",
			want:    sample{ns: 4477},
			wantNam: "BenchmarkPhrases",
			ok:      true,
		},
		{
			name:    "benchmem",
			line:    "BenchmarkPhrases-18   \t  277622\t      4477 ns/op\t    2608 B/op\t      60 allocs/op",
			want:    sample{ns: 4477, bytes: 2608, allocs: 60},
			wantNam: "BenchmarkPhrases",
			ok:      true,
		},
		{
			// SetBytes injects MB/s between ns/op and B/op. This is the layout
			// that breaks a reader counting fields.
			name:    "benchmem with SetBytes",
			line:    "BenchmarkParseCorpus-18   \t  10\t 282674490 ns/op\t  29.57 MB/s\t162267668 B/op\t  969613 allocs/op",
			want:    sample{ns: 282674490, bytes: 162267668, allocs: 969613},
			wantNam: "BenchmarkParseCorpus",
			ok:      true,
		},
		{
			// A subtest name can itself contain a hyphen followed by digits, so
			// stripping the -P suffix has to check it really is the suffix.
			name:    "subtest with digits after a hyphen",
			line:    "BenchmarkHTML/paras=8/Feed-18   \t  50\t    173752 ns/op\t   81976 B/op\t    1206 allocs/op",
			want:    sample{ns: 173752, bytes: 81976, allocs: 1206},
			wantNam: "BenchmarkHTML/paras=8/Feed",
			ok:      true,
		},
		{name: "ok line", line: "ok  \tgithub.com/x/y\t0.123s"},
		{name: "pass line", line: "PASS"},
		{name: "goos line", line: "goos: windows"},
		{name: "truncated", line: "BenchmarkX-18\t100"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, got, ok := parseLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if name != tc.wantNam {
				t.Errorf("name = %q, want %q", name, tc.wantNam)
			}
			if got != tc.want {
				t.Errorf("sample = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// The diagnostic that says whether a wide spread is the machine or the
// benchmark. It was wrong in the first version — comparing B/op exactly called
// every benchmark unstable, because B/op carries a byte or two of rounding while
// allocs/op does not — which inverted the only judgement this tool makes.
func TestAllocNoteDistinguishesTheBoxFromTheBenchmark(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples []sample
		quiet   bool // true when allocation held and the box is to blame
	}{
		{
			name:    "identical",
			samples: []sample{{bytes: 65225, allocs: 1369}, {bytes: 65225, allocs: 1369}},
			quiet:   true,
		},
		{
			// The real numbers from six runs of one unchanged query.
			name: "B/op rounding only",
			samples: []sample{
				{bytes: 65225, allocs: 1369}, {bytes: 65226, allocs: 1369},
				{bytes: 65229, allocs: 1369}, {bytes: 65225, allocs: 1369},
			},
			quiet: true,
		},
		{
			name:    "allocs/op moved",
			samples: []sample{{bytes: 65225, allocs: 1369}, {bytes: 65225, allocs: 1554}},
			quiet:   false,
		},
		{
			// search_common_term's outlier: 70k against 178k is not rounding.
			name:    "B/op moved far beyond rounding",
			samples: []sample{{bytes: 70370, allocs: 1555}, {bytes: 178910, allocs: 1555}},
			quiet:   false,
		},
		{
			// Zero-allocation benchmarks must not divide by zero on the way to
			// saying nothing is wrong.
			name:    "zero allocation",
			samples: []sample{{bytes: 0, allocs: 0}, {bytes: 0, allocs: 0}},
			quiet:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := allocNote(tc.samples) == ""; got != tc.quiet {
				t.Errorf("allocation called steady = %v, want %v (note %q)",
					got, tc.quiet, allocNote(tc.samples))
			}
		})
	}
}
