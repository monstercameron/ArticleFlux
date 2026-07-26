package idgen

import (
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewIsSortableByTime(t *testing.T) {
	base := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	defer func() { Now = func() time.Time { return time.Now().UTC() } }()

	var ids []string
	for i := 0; i < 50; i++ {
		Now = func() time.Time { return base.Add(time.Duration(i) * time.Millisecond) }
		ids = append(ids, New())
	}
	if !sort.StringsAreSorted(ids) {
		t.Error("ids minted in time order must sort in time order — §6.5 pages on (sort_key, id)")
	}
}

// The tiebreak that keyset pagination depends on: ids minted inside one
// millisecond still have a defined order.
func TestNewIsMonotonicWithinAMillisecond(t *testing.T) {
	fixed := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	defer func() { Now = func() time.Time { return time.Now().UTC() } }()
	Now = func() time.Time { return fixed }

	var ids []string
	for i := 0; i < 1000; i++ {
		ids = append(ids, New())
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("ids within one millisecond must still be ordered")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %s inside one millisecond", id)
		}
		seen[id] = true
	}
}

func TestTimeOfRoundTrips(t *testing.T) {
	want := time.Date(2026, 7, 26, 12, 34, 56, 789000000, time.UTC)
	defer func() { Now = func() time.Time { return time.Now().UTC() } }()
	Now = func() time.Time { return want }

	got, err := TimeOf(New())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Errorf("TimeOf = %v, want %v", got, want)
	}
}

func TestTimeOfRejectsSlugs(t *testing.T) {
	// A slug is not a timestamped id and must not decode as one — otherwise a
	// retention sweep could read a share slug as a date.
	if _, err := TimeOf("not-an-id!!"); err == nil {
		t.Error("garbage should not decode")
	}
}

func TestSlugsAreUnguessable(t *testing.T) {
	const n = 5000
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		s := Slug()
		if len(s) != 26 {
			t.Fatalf("slug length %d, want 26 (128 bits)", len(s))
		}
		if seen[s] {
			t.Fatal("slug collision — this should be arithmetically unreachable")
		}
		seen[s] = true
	}
	// A slug must carry no timestamp: a public share URL that leaks when it was
	// created is a slug with structure, and structure is what makes ids guessable.
	a, b := Slug(), Slug()
	if commonPrefix(a, b) > 6 {
		t.Errorf("slugs share a %d-char prefix (%s / %s) — they should have no structure",
			commonPrefix(a, b), a, b)
	}
}

func TestTokenIs256Bits(t *testing.T) {
	if got := len(Token()); got != 52 {
		t.Errorf("token length %d, want 52 (256 bits base32)", got)
	}
}

func TestConcurrentMintingIsSafe(t *testing.T) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				id := New()
				mu.Lock()
				if seen[id] {
					t.Errorf("duplicate id %s under concurrency", id)
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

func TestAlphabetIsUnambiguous(t *testing.T) {
	// These ids get typed off a phone screen and read aloud. I, L, O and U are
	// excluded so 1/I/l and 0/O never have to be disambiguated.
	for _, id := range []string{New(), Slug(), Token()} {
		if strings.ContainsAny(id, "ILOU") {
			t.Errorf("%s contains an ambiguous character", id)
		}
	}
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
