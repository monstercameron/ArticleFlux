package app

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// The gate, both ways.
//
// The half that matters is the first: a profiling surface that ships on by
// default is a denial of service anyone can trigger with a GET, and "we never
// meant to enable it" is the story behind most of the ones that are live. The
// second half is only here so that a refactor which quietly stops registering
// the handlers fails loudly instead of leaving an operator holding a flag that
// does nothing.
func TestPprofIsOffUnlessAskedFor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		profiling bool
		want      int
	}{
		// 404 rather than 403: there is no reader-facing question this endpoint
		// answers, so an informative refusal buys nothing and an absent path is
		// the honest description of an instance that never asked for it.
		{"default", false, http.StatusNotFound},
		{"enabled", true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := Open(t.Context(), Config{
				DBPath:    filepath.Join(t.TempDir(), "test.db"),
				Profiling: tc.profiling,
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			t.Cleanup(func() { _ = a.Close() })

			srv := httptest.NewServer(a.Handler())
			t.Cleanup(srv.Close)

			// The index, not /profile — asking for a CPU profile would block this
			// test for thirty seconds, which is the same property that makes the
			// endpoint worth gating.
			res, err := http.Get(srv.URL + "/debug/pprof/")
			if err != nil {
				t.Fatalf("GET /debug/pprof/: %v", err)
			}
			defer func() { _ = res.Body.Close() }()

			if res.StatusCode != tc.want {
				t.Errorf("status = %d, want %d (Profiling=%v)", res.StatusCode, tc.want, tc.profiling)
			}
		})
	}
}

// The named profiles have to be reachable too, and this is not a formality: the
// index and the four dedicated handlers are registered by separate lines, and
// dropping the prefix registration would leave /debug/pprof/heap answering with
// the static file server's 404 while the index above still passed.
func TestPprofServesNamedProfiles(t *testing.T) {
	a, err := Open(t.Context(), Config{
		DBPath:    filepath.Join(t.TempDir(), "test.db"),
		Profiling: true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)

	// heap and goroutine are the two that answer the questions this suite exists
	// to make answerable; cmdline is here because it is a separate registration
	// line from the prefix and so can rot independently.
	for _, path := range []string{"/debug/pprof/heap", "/debug/pprof/goroutine", "/debug/pprof/cmdline"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, res.StatusCode)
		}
	}
}
