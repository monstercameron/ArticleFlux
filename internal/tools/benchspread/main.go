// Command benchspread reads `go test -bench` output and reports whether its
// timings can be believed.
//
// # Why this exists
//
// The development box is fanless and is frequently not the only thing running on
// itself. Under either condition `ns/op` stops describing the code: a full
// capture taken while another `go test` was in flight reported one unchanged
// query between 273ms and 3.3 SECONDS across six samples, and a single-row
// primary-key fetch across a 24-fold range. Nothing in the code moved.
//
// A person reading that file sees six plausible numbers. `-count 6` does not
// help, because contention and thermal drift are not random noise — they push
// one direction for as long as they last, so the samples agree with each other
// and are wrong together. benchstat will happily report a confident, significant
// difference between two such runs.
//
// # The signal it keys on
//
// Allocation is deterministic. B/op and allocs/op depend on what the code did,
// never on how hot the machine is or what else is on it — in that same ruined
// capture they were identical to five significant figures while the timings swung
// twelve-fold. So a benchmark whose ns/op spread is wide while its allocation is
// flat is measuring the room, and that is a mechanical test rather than a
// judgement call.
//
// It does not fail the build. A contended run is still completely valid for the
// allocation half of the answer, and throwing it away would discard the numbers
// that ARE trustworthy along with the ones that are not. It prints what can be
// believed and what cannot.
//
//	go run ./internal/tools/benchspread bin/perf/baseline.txt
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// noisyRatio is the max/min ns/op above which a benchmark's timings are called
// unusable.
//
// Three, and the number is chosen from the two populations rather than from
// taste. A quiet run of this suite holds well inside 2x — the same query
// measured six times lands in a band. A contended one produces 12x and 24x.
// Nothing observed here lands between 3 and 10, so the exact cut does not
// matter much; what matters is that a run has to declare which side it is on.
const noisyRatio = 3.0

// sample is one measured line.
type sample struct {
	ns     float64
	bytes  int64
	allocs int64
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: benchspread <bench-output-file>...")
		os.Exit(2)
	}

	byName := map[string][]sample{}
	// Insertion order, so the report reads in the order the benchmarks ran
	// rather than alphabetically — which keeps a package's shapes together.
	var order []string

	for _, path := range os.Args[1:] {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchspread: %v\n", err)
			os.Exit(1)
		}
		sc := bufio.NewScanner(f)
		// Benchmark lines are short, but a package path plus a subtest name can
		// run long and the default 64KB is plenty either way.
		for sc.Scan() {
			name, s, ok := parseLine(sc.Text())
			if !ok {
				continue
			}
			if _, seen := byName[name]; !seen {
				order = append(order, name)
			}
			byName[name] = append(byName[name], s)
		}
		_ = f.Close()
		if err := sc.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "benchspread: reading %s: %v\n", path, err)
			os.Exit(1)
		}
	}

	if len(order) == 0 {
		fmt.Fprintln(os.Stderr, "benchspread: no benchmark lines found — is this `go test -bench` output?")
		os.Exit(1)
	}

	var noisy, single int
	for _, name := range order {
		ss := byName[name]
		// One sample cannot have a spread. Reported separately rather than
		// silently counted as clean, because "we did not measure it twice" and
		// "it was stable" are different claims.
		if len(ss) < 2 {
			single++
			continue
		}
		lo, hi := ss[0].ns, ss[0].ns
		for _, s := range ss {
			lo = min(lo, s.ns)
			hi = max(hi, s.ns)
		}
		if lo <= 0 || hi/lo < noisyRatio {
			continue
		}
		if noisy == 0 {
			fmt.Println("timings that cannot be believed (ns/op spread over " +
				strconv.FormatFloat(noisyRatio, 'g', -1, 64) + "x):")
		}
		noisy++
		fmt.Printf("  %-52s %5.1fx   %s .. %s%s\n",
			name, hi/lo, dur(lo), dur(hi), allocNote(ss))
	}

	fmt.Printf("\n%d benchmark%s, %d with unusable timings", len(order), plural(len(order)), noisy)
	if single > 0 {
		fmt.Printf(", %d measured only once", single)
	}
	fmt.Println(".")

	if noisy == 0 {
		return
	}
	fmt.Println("\nAllocation figures in this run are still exact — B/op and allocs/op do not")
	fmt.Println("depend on machine load. The timings do. Re-measure on an idle box before")
	fmt.Println("comparing them, or put the before and after in ONE run as interleaved")
	fmt.Println("sub-benchmarks, which is what internal/feed's BenchmarkTextHelpers does.")
}

// allocNote reports whether allocation ALSO moved while the timings did, which
// is what distinguishes an unstable MACHINE from an unstable BENCHMARK.
//
// A benchmark whose allocation varies is doing different work on different
// iterations — a cache filling, a map growing, a fixture accumulating rows — and
// that is a fact about the benchmark rather than about the room. Saying so keeps
// this from blaming the box for something the benchmark is doing to itself.
//
// # Why allocs/op is compared exactly and B/op is not
//
// Because they are not the same kind of number, and the first version of this
// treated them as one. allocs/op is a count divided by iterations and lands on
// an integer; six samples of deterministic code give six identical values.
// B/op is a byte total over the same divisor and carries a byte or two of
// rounding — the store's list query reported 65225, 65226, 65226, 65225, 65229,
// 65225 for six runs of identical work.
//
// Comparing B/op exactly therefore called EVERY benchmark unstable, including
// the ones whose allocation was demonstrably fixed, which inverted the one
// diagnostic this tool exists to give. One percent is well above that rounding
// and far below any real change in what the code allocates.
func allocNote(ss []sample) string {
	for _, s := range ss[1:] {
		if s.allocs != ss[0].allocs {
			return "   (allocation moved too — this one is the benchmark, not the box)"
		}
		if ss[0].bytes > 0 {
			drift := float64(s.bytes-ss[0].bytes) / float64(ss[0].bytes)
			if drift > 0.01 || drift < -0.01 {
				return "   (allocation moved too — this one is the benchmark, not the box)"
			}
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dur(ns float64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2fs", ns/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.1fms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.1fµs", ns/1e3)
	default:
		return fmt.Sprintf("%.0fns", ns)
	}
}

// parseLine pulls the name and the three measured columns out of one benchmark
// line.
//
// The format is `Name-P  iters  X ns/op  [Y MB/s]  [Z B/op]  [N allocs/op]`,
// with the optional columns present or absent depending on -benchmem and
// b.SetBytes. So the units are searched for by name rather than counted by
// position — a positional reader breaks the first time someone adds SetBytes to
// one benchmark and not another, which is exactly the state this suite is in.
func parseLine(line string) (string, sample, bool) {
	if !strings.HasPrefix(line, "Benchmark") {
		return "", sample{}, false
	}
	f := strings.Fields(line)
	if len(f) < 4 {
		return "", sample{}, false
	}
	// Strip the -P suffix `go test` appends, so six samples of one benchmark
	// group even if GOMAXPROCS changed between runs.
	name := f[0]
	if i := strings.LastIndex(name, "-"); i > 0 {
		if _, err := strconv.Atoi(name[i+1:]); err == nil {
			name = name[:i]
		}
	}

	var s sample
	var haveNS bool
	for i := 1; i+1 < len(f); i++ {
		v, err := strconv.ParseFloat(f[i], 64)
		if err != nil {
			continue
		}
		switch f[i+1] {
		case "ns/op":
			s.ns, haveNS = v, true
		case "B/op":
			s.bytes = int64(v)
		case "allocs/op":
			s.allocs = int64(v)
		}
	}
	if !haveNS {
		return "", sample{}, false
	}
	return name, s, true
}
