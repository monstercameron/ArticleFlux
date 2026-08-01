package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

func routesApp(t *testing.T, devMode bool) (*App, *httptest.Server) {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath: filepath.Join(t.TempDir(), "routes.db"), DevMode: devMode, PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return a, srv
}

// Liveness never touches the database — a probe that fails on a slow query
// gets the process killed and restarted into the same slow query.
func TestHealthzAnswersOK(t *testing.T) {
	_, srv := routesApp(t, false)
	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if strings.TrimSpace(string(body)) != "ok" {
		t.Errorf("body = %q", body)
	}
}

// Readiness DOES touch the database, and answers "ready" on a healthy
// instance — the only difference from liveness, and why they are separate
// endpoints rather than one convenient probe.
func TestReadyzAnswersReadyOnAHealthyInstance(t *testing.T) {
	_, srv := routesApp(t, false)
	res, err := http.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if strings.TrimSpace(string(body)) != "ready" {
		t.Errorf("body = %q", body)
	}
}

// The dev-only debug endpoints do not exist at all outside DevMode — a
// production instance must not carry an unauthenticated "wipe my read state"
// or "write me an article" surface.
func TestDebugEndpointsAreAbsentOutsideDevMode(t *testing.T) {
	_, srv := routesApp(t, false)
	for _, path := range []string{"/debug/reset-state", "/debug/ingest-one"} {
		res, err := http.Post(srv.URL+path, "text/plain", nil)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s outside DevMode: status %d, want 404 — the route must not exist at all", path, res.StatusCode)
		}
	}
}

// /debug/reset-state only accepts POST, requires a local account, and clears
// it — the e2e suite's way of getting a clean slate between tests without a
// fresh database per test.
func TestDebugResetStateRequiresPOSTAndAnAccount(t *testing.T) {
	_, srv := routesApp(t, true)

	if res, err := http.Get(srv.URL + "/debug/reset-state"); err != nil {
		t.Fatal(err)
	} else {
		res.Body.Close()
		if res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET: status %d, want 405", res.StatusCode)
		}
	}

	// No account yet: devScope fails.
	res, err := http.Post(srv.URL+"/debug/reset-state", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("no account yet: status %d, want 412", res.StatusCode)
	}
}

// /debug/ingest-one writes one article through the real onIngested path so a
// test can prove something arriving on the server reaches an open screen.
func TestDebugIngestOneRequiresASubscription(t *testing.T) {
	a, srv := routesApp(t, true)
	if _, err := a.EnsureDevUser(context.Background(), "cam", "articleflux-routes-test"); err != nil {
		t.Fatalf("dev user: %v", err)
	}

	// An account exists but has not subscribed to anything yet.
	res, err := http.Post(srv.URL+"/debug/ingest-one", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("no subscriptions: status %d, want 412", res.StatusCode)
	}
}

func TestDebugIngestOneWritesAnArticle(t *testing.T) {
	a, srv := routesApp(t, true)
	sc, err := a.EnsureDevUser(context.Background(), "cam", "articleflux-routes-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	if _, _, err := a.Repo().Subscribe(context.Background(), sc, store.NewSubscription{
		NaturalKey: "feed:routes", FeedURL: "https://routes.example/feed", Title: "Routes fixture",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	res, err := http.Post(srv.URL+"/debug/ingest-one", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}

	items, _, err := a.Repo().ListItems(context.Background(), sc, store.ListQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Errorf("got %d items after /debug/ingest-one, want 1", len(items))
	}
}
