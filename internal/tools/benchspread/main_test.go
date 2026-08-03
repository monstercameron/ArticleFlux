package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// dur is what every number in the report is read through, and the thresholds
// are the whole point of it: a benchmark that says "1200000ns" and one that says
// "1.2ms" carry the same information and only one of them can be scanned down a
// column. An off-by-one order of magnitude here misreports every timing in the
// verdict this tool exists to give.
func TestDurPicksTheUnitAHumanWouldRead(t *testing.T) {
	for _, c := range []struct {
		ns   float64
		want string
	}{
		{0, "0ns"},
		{999, "999ns"},
		{1000, "1.0µs"},
		{1500, "1.5µs"},
		{999_999, "1000.0µs"},
		{1e6, "1.0ms"},
		{1_500_000, "1.5ms"},
		{1e9, "1.00s"},
		{2_500_000_000, "2.50s"},
	} {
		if got := dur(c.ns); got != c.want {
			t.Errorf("dur(%v) = %q, want %q", c.ns, got, c.want)
		}
	}
}

// Each threshold is a boundary, and a `>` where `>=` belongs would push the
// exact value into the unit below and print "1000.0µs" where "1.0ms" belongs.
func TestDurBoundariesAreInclusive(t *testing.T) {
	for _, c := range []struct {
		ns   float64
		unit string
	}{
		{1e3, "µs"},
		{1e6, "ms"},
		{1e9, "s"},
	} {
		got := dur(c.ns)
		if !strings.HasSuffix(got, c.unit) {
			t.Errorf("dur(%v) = %q, want it to reach %s", c.ns, got, c.unit)
		}
	}
}

func TestPluralAgreesWithItsCount(t *testing.T) {
	if got := plural(1); got != "" {
		t.Errorf("plural(1) = %q, want empty", got)
	}
	// Zero takes the plural in English — "0 samples", not "0 sample".
	for _, n := range []int{0, 2, 17} {
		if got := plural(n); got != "s" {
			t.Errorf("plural(%d) = %q, want \"s\"", n, got)
		}
	}
}

// --- the verdict --------------------------------------------------------------
//
// run's whole job is to say whether the numbers beside it can be believed, so a
// wrong verdict is worse than no verdict: the only thing anybody does with this
// output is decide whether to trust a measurement.

func benchFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bench.txt")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write bench file: %v", err)
	}
	return path
}

func runReport(t *testing.T, paths ...string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := run(paths, &buf); err != nil {
		t.Fatalf("run: %v", err)
	}
	return buf.String()
}

// Six samples that agree are the case this tool should stay quiet about.
func TestRunReportsStableTimingsAsClean(t *testing.T) {
	var lines []string
	for range 6 {
		lines = append(lines, "BenchmarkListItems-8   1000   120000 ns/op   65225 B/op   400 allocs/op")
	}
	got := runReport(t, benchFile(t, lines...))

	if strings.Contains(got, "cannot be believed") {
		t.Errorf("stable timings were called unusable:\n%s", got)
	}
	if !strings.Contains(got, "1 benchmark,") {
		t.Errorf("the count is wrong or not singular:\n%s", got)
	}
	if !strings.Contains(got, "0 with unusable timings") {
		t.Errorf("a clean run was not reported as clean:\n%s", got)
	}
}

// A tenfold spread on one unchanged query is the exact observation this tool was
// written for, and it must be named rather than averaged away.
func TestRunFlagsAWideSpread(t *testing.T) {
	got := runReport(t, benchFile(t,
		"BenchmarkListItems-8   1000    120000 ns/op   65225 B/op   400 allocs/op",
		"BenchmarkListItems-8   1000   1200000 ns/op   65225 B/op   400 allocs/op",
	))
	if !strings.Contains(got, "cannot be believed") {
		t.Errorf("a tenfold spread was not flagged:\n%s", got)
	}
	if !strings.Contains(got, "BenchmarkListItems") {
		t.Errorf("the flagged benchmark is not named:\n%s", got)
	}
	if !strings.Contains(got, "1 with unusable timings") {
		t.Errorf("the count of unusable timings is wrong:\n%s", got)
	}
	// And the advice, which is the actionable half.
	if !strings.Contains(got, "idle box") {
		t.Errorf("no advice on what to do about it:\n%s", got)
	}
}

// "We did not measure it twice" and "it was stable" are different claims, and
// collapsing them is how a single-sample run reads as a clean one.
func TestRunReportsSingleSampleBenchmarksSeparately(t *testing.T) {
	got := runReport(t, benchFile(t,
		"BenchmarkOnce-8   1000   120000 ns/op   1000 B/op   10 allocs/op",
	))
	if !strings.Contains(got, "measured only once") {
		t.Errorf("a single-sample benchmark was not called out:\n%s", got)
	}
	if strings.Contains(got, "cannot be believed") {
		t.Errorf("one sample was reported as a spread:\n%s", got)
	}
}

// Allocation moving alongside the timings means the BENCHMARK is unstable, not
// the box — a cache filling or a fixture accumulating rows. Saying so is what
// keeps this from blaming the room for something the benchmark does to itself.
func TestRunDistinguishesAnUnstableBenchmarkFromAnUnstableBox(t *testing.T) {
	got := runReport(t, benchFile(t,
		"BenchmarkGrowing-8   1000    120000 ns/op   1000 B/op   10 allocs/op",
		"BenchmarkGrowing-8   1000   1200000 ns/op   9000 B/op   90 allocs/op",
	))
	if !strings.Contains(got, "the benchmark, not the box") {
		t.Errorf("moving allocation was not attributed to the benchmark:\n%s", got)
	}
}

// Insertion order, so the report reads in the order the benchmarks ran — which
// keeps a package's shapes together instead of scattering them alphabetically.
func TestRunKeepsBenchmarksInTheOrderTheyRan(t *testing.T) {
	got := runReport(t, benchFile(t,
		"BenchmarkZeta-8    1000    100000 ns/op",
		"BenchmarkZeta-8    1000   1000000 ns/op",
		"BenchmarkAlpha-8   1000    100000 ns/op",
		"BenchmarkAlpha-8   1000   1000000 ns/op",
	))
	z, a := strings.Index(got, "BenchmarkZeta"), strings.Index(got, "BenchmarkAlpha")
	if z < 0 || a < 0 {
		t.Fatalf("both benchmarks should be flagged:\n%s", got)
	}
	if z > a {
		t.Errorf("the report was sorted rather than kept in run order:\n%s", got)
	}
}

// Several files are one measurement set — that is how `make perf` accumulates a
// label across packages.
func TestRunCombinesSamplesAcrossFiles(t *testing.T) {
	a := benchFile(t, "BenchmarkX-8   1000    100000 ns/op")
	b := benchFile(t, "BenchmarkX-8   1000   1000000 ns/op")
	got := runReport(t, a, b)
	if !strings.Contains(got, "cannot be believed") {
		t.Errorf("samples from two files were not combined:\n%s", got)
	}
}

// A file that is not benchmark output must say so rather than reporting a clean
// run over nothing — which would read as "all your timings are fine".
func TestRunRefusesOutputThatHasNoBenchmarkLines(t *testing.T) {
	err := run([]string{benchFile(t, "ok  \tgithub.com/x/y\t0.5s", "PASS")}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a file with no benchmark lines reported a clean run")
	}
	if !strings.Contains(err.Error(), "no benchmark lines") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

func TestRunReportsAMissingFile(t *testing.T) {
	if err := run([]string{filepath.Join(t.TempDir(), "nope.txt")}, &bytes.Buffer{}); err == nil {
		t.Error("a missing file reported success")
	}
}
