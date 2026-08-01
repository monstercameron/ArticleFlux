package discover

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jsonapi.go has no coverage from any other test file, so this is starting
// from zero rather than filling a gap in existing tests.

// --- apiCandidates (pure) ----------------------------------------------------

func TestAPICandidatesCoversTheConventionalRewrites(t *testing.T) {
	got := apiCandidates("https://example.com/comics/hajime-no-ippo")
	want := []string{
		"https://example.com/api/comics/hajime-no-ippo",
		"https://example.com/comics/hajime-no-ippo.json",
		"https://example.com/api/v1/comics/hajime-no-ippo",
		"https://example.com/api/comics",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// A one-level-deep path has no section index to add — the fourth rewrite only
// makes sense when there is a parent segment to fall back to.
func TestAPICandidatesForATopLevelPathSkipsTheSectionIndex(t *testing.T) {
	got := apiCandidates("https://example.com/comics")
	for _, u := range got {
		if u == "https://example.com/api" {
			t.Errorf("a bare /api candidate should not be offered for a top-level path: %v", got)
		}
	}
	if len(got) != 3 {
		t.Errorf("%d candidates for a top-level path, want 3: %v", len(got), got)
	}
}

func TestAPICandidatesPreservesQueryAndDeduplicates(t *testing.T) {
	got := apiCandidates("https://example.com/x?ref=abc")
	for _, u := range got {
		if !strings.HasSuffix(u, "?ref=abc") {
			t.Errorf("candidate %q dropped the query string", u)
		}
	}
	seen := map[string]bool{}
	for _, u := range got {
		if seen[u] {
			t.Errorf("duplicate candidate: %q", u)
		}
		seen[u] = true
	}
}

// A bare origin with no path at all must still probe the root-level
// rewrites, not skip them for lack of a path segment.
func TestAPICandidatesOnABareOriginUsesRootPath(t *testing.T) {
	got := apiCandidates("https://example.com")
	want := []string{
		"https://example.com/api/",
		"https://example.com/.json",
		"https://example.com/api/v1/",
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestAPICandidatesOnAnUnparseableURLReturnsNothing(t *testing.T) {
	if got := apiCandidates("://not a url"); got != nil {
		t.Errorf("apiCandidates(garbage) = %v, want nil", got)
	}
	if got := apiCandidates("/just/a/path"); got != nil {
		t.Errorf("apiCandidates(no host) = %v, want nil", got)
	}
}

// --- largestObjectArray (pure) -----------------------------------------------

func TestLargestObjectArrayFindsTheBiggestListOfEntries(t *testing.T) {
	doc := map[string]any{
		"comic": map[string]any{
			"genres": []any{"a", "b", "c"}, // strings, not objects — must not win
			"chapters": []any{
				map[string]any{"title": "one"},
				map[string]any{"title": "two"},
				map[string]any{"title": "three"},
			},
		},
	}
	path, n := largestObjectArray(doc, nil)
	if n != 3 {
		t.Fatalf("found %d, want 3", n)
	}
	if strings.Join(path, ".") != "comic.chapters" {
		t.Errorf("path = %v, want comic.chapters", path)
	}
}

// An array of scalars contains zero objects and must not be mistaken for the
// list of entries just because it is long.
func TestLargestObjectArrayIgnoresArraysOfScalars(t *testing.T) {
	doc := map[string]any{
		"tags": []any{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"},
	}
	_, n := largestObjectArray(doc, nil)
	if n != 0 {
		t.Errorf("found %d objects in an array of strings, want 0", n)
	}
}

func TestLargestObjectArrayPicksTheLongerOfTwoCandidates(t *testing.T) {
	doc := map[string]any{
		"related": []any{map[string]any{"id": 1}},
		"posts": []any{
			map[string]any{"id": 1}, map[string]any{"id": 2}, map[string]any{"id": 3},
			map[string]any{"id": 4}, map[string]any{"id": 5},
		},
	}
	path, n := largestObjectArray(doc, nil)
	if n != 5 || strings.Join(path, ".") != "posts" {
		t.Errorf("path=%v n=%d, want posts/5", path, n)
	}
}

// The array of arrays fallback: the top-level array itself has no direct
// object elements, but one of its own elements is an array of objects — the
// data most app shells actually wrap their entry list one level deep.
func TestLargestObjectArrayLooksInsideNestedArrays(t *testing.T) {
	doc := []any{
		"not an object",
		[]any{
			map[string]any{"id": 1}, map[string]any{"id": 2}, map[string]any{"id": 3},
		},
	}
	_, n := largestObjectArray(doc, nil)
	if n != 3 {
		t.Errorf("found %d, want 3 — the nested array of objects was not found", n)
	}
}

// Depth is bounded so a pathological or self-referential document cannot turn
// the probe into a hang.
func TestLargestObjectArrayIsDepthBounded(t *testing.T) {
	var deep any = []any{map[string]any{"id": 1}, map[string]any{"id": 2}}
	for i := 0; i < 20; i++ {
		deep = map[string]any{"wrap": deep}
	}
	// Must return quickly and without panicking; the exact answer at absurd
	// depth is not the point, termination is.
	_, _ = largestObjectArray(deep, nil)
}

// --- looksJSON (pure) ---------------------------------------------------------

func TestLooksJSON(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"a":1}`, true},
		{`[1,2,3]`, true},
		{"   \n\t {\"a\":1}", true},
		{"<html>not json</html>", false},
		{"", false},
		{"   ", false},
		{"plain text response", false},
	} {
		if got := looksJSON([]byte(tc.body)); got != tc.want {
			t.Errorf("looksJSON(%q) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

// --- JSONEndpoint (network, via httptest) -------------------------------------

func TestJSONEndpointFindsTheArrayBehindAnAppShell(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/comics/x", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"comic":{"chapters":[{"title":"a"},{"title":"b"},{"title":"c"}]}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	got := local().JSONEndpoint(context.Background(), srv.URL+"/comics/x")
	if got == nil {
		t.Fatal("JSONEndpoint found nothing")
	}
	if got.Found != 3 {
		t.Errorf("Found = %d, want 3", got.Found)
	}
	if got.ItemsPath != "comic.chapters" {
		t.Errorf("ItemsPath = %q", got.ItemsPath)
	}
	if len(got.Body) == 0 {
		t.Error("the response body was not retained for the caller")
	}
}

// A single-object array (the hero record, not the list) must not be accepted —
// the same "two is the floor" rule the HTML side applies.
func TestJSONEndpointRejectsASingletonArray(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/thing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":1}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if got := local().JSONEndpoint(context.Background(), srv.URL+"/thing"); got != nil {
		t.Errorf("a singleton array was accepted: %+v", got)
	}
}

// A candidate that answers with HTML (a login page, a 404 page styled as a
// normal response) must be skipped rather than misread as data.
func TestJSONEndpointSkipsNonJSONResponses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>not json</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if got := local().JSONEndpoint(context.Background(), srv.URL+"/thing"); got != nil {
		t.Errorf("an HTML response was accepted as JSON: %+v", got)
	}
}

// A candidate that claims a JSON content type but sends bytes that do not
// parse (a truncated response, a misconfigured endpoint) must be skipped
// like any other bad candidate, not treated as a parse failure worth reporting.
func TestJSONEndpointSkipsUnparseableJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"comic":{"chapters":[`)) // truncated
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if got := local().JSONEndpoint(context.Background(), srv.URL+"/thing"); got != nil {
		t.Errorf("malformed JSON was accepted: %+v", got)
	}
}

// Nothing answering usefully at any candidate is a normal outcome, not an
// error — most client-rendered sites are simply unfollowable this way.
func TestJSONEndpointReturnsNilWhenNothingMatches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if got := local().JSONEndpoint(context.Background(), srv.URL+"/thing"); got != nil {
		t.Errorf("got %+v, want nil when every candidate 404s", got)
	}
}

// --- JSON (the poll fetch, network via httptest) ------------------------------

func TestJSONFetchesAKnownAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chapters":[1,2,3]}`))
	}))
	defer srv.Close()

	body, err := local().JSON(context.Background(), srv.URL+"/api/x")
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	if !strings.Contains(string(body), "chapters") {
		t.Errorf("body = %s", body)
	}
}

// A `text/plain` response that is nonetheless valid JSON must be accepted —
// real APIs do this, and refusing on the header alone would refuse a working
// source.
func TestJSONAcceptsPlainTextContentTypeWhenBytesAreJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`[1,2,3]`))
	}))
	defer srv.Close()

	if _, err := local().JSON(context.Background(), srv.URL+"/x"); err != nil {
		t.Fatalf("a text/plain JSON body was refused: %v", err)
	}
}

func TestJSONRefusesNonJSONResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>nope</html>`))
	}))
	defer srv.Close()

	if _, err := local().JSON(context.Background(), srv.URL+"/x"); err == nil {
		t.Fatal("an HTML response was accepted as a data endpoint")
	}
}

// A fetch that fails outright (connection refused) must return that error,
// not silently succeed with an empty body.
func TestJSONPropagatesAFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close()

	if _, err := local().JSON(context.Background(), "http://"+addr+"/x"); err == nil {
		t.Error("JSON on a closed listener returned no error")
	}
}

// A paginated dump larger than maxJSONBytes must be refused — the poll is
// meant to re-fetch one known address on a schedule, not buffer an
// unbounded response every hour.
func TestJSONRefusesAnOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		chunk := make([]byte, 1<<20)
		for i := 0; i < 5; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()

	if _, err := local().JSON(context.Background(), srv.URL+"/x"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("JSON on a 5MB body: err = %v, want ErrTooLarge", err)
	}
}
