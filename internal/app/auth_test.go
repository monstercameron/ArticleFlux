package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

func authApp(t *testing.T, devMode bool) *App {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath: filepath.Join(t.TempDir(), "auth.db"), DevMode: devMode, Log: testLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// scopeFromContext fails closed: a context carrying no gRPC metadata at all —
// as opposed to metadata with an empty authorization header — falls through to
// devScope, which itself refuses outside DevMode.
func TestScopeFromContextWithNoMetadataFailsClosedOutsideDevMode(t *testing.T) {
	a := authApp(t, false)
	if _, err := a.scopeFromContext(context.Background()); err != store.ErrNoScope {
		t.Errorf("err = %v, want ErrNoScope", err)
	}
}

// An empty token is refused before ever reaching the repository — the
// alternative is a query that matches nothing and looks identical to "the
// database is down".
func TestScopeForTokenRejectsAnEmptyToken(t *testing.T) {
	a := authApp(t, false)
	if _, err := a.scopeForToken(context.Background(), ""); err != store.ErrNoScope {
		t.Errorf("err = %v, want ErrNoScope", err)
	}
}

// devScope is gated on DevMode alone: without it, an internet-facing instance
// with no session would otherwise be an open reader.
func TestDevScopeRefusesOutsideDevMode(t *testing.T) {
	a := authApp(t, false)
	if _, err := a.devScope(context.Background()); err != store.ErrNoScope {
		t.Errorf("err = %v, want ErrNoScope", err)
	}
}

// EnsureDevUser is idempotent: called again once an account exists, it
// returns that account's scope rather than trying (and failing) to create a
// second one.
func TestEnsureDevUserIsIdempotent(t *testing.T) {
	a := authApp(t, true)
	first, err := a.EnsureDevUser(context.Background(), "cam", "articleflux-auth-test")
	if err != nil {
		t.Fatalf("first EnsureDevUser: %v", err)
	}
	second, err := a.EnsureDevUser(context.Background(), "someone-else", "a-different-password")
	if err != nil {
		t.Fatalf("second EnsureDevUser: %v", err)
	}
	if second != first {
		t.Errorf("second call = %+v, want the same scope as the first %+v — a repeat call must not "+
			"attempt to create a second local account", second, first)
	}
}
