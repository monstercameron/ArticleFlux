package app

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/idgen"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// scopeFromContext resolves the caller into a store.Scope.
//
// Fail-closed: an unrecognised or absent token yields ErrNoScope, and every
// repository method refuses to run without a valid scope. The alternative — a
// zero Scope reaching a query — silently matches no rows, which a user reads as
// "my account is empty" rather than "authentication is broken".
func (a *App) scopeFromContext(ctx context.Context) (store.Scope, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return a.devScope(ctx)
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return a.devScope(ctx)
	}
	tok := vals[0]
	if len(tok) > 7 && (tok[:7] == "Bearer " || tok[:7] == "bearer ") {
		tok = tok[7:]
	}
	return a.scopeForToken(ctx, tok)
}

// scopeForToken looks a session up by token hash.
//
// The stored value is a hash, so a database dump does not hand out live
// sessions. It is SHA-256 rather than Argon2id deliberately: the token is 256
// bits of CSPRNG output with no keyspace to search, and this runs on every
// request (see internal/secret).
func (a *App) scopeForToken(ctx context.Context, token string) (store.Scope, error) {
	if token == "" {
		return store.Scope{}, store.ErrNoScope
	}
	sc, err := a.repo.ScopeForSession(ctx, secret.HashToken(token))
	if err != nil {
		return store.Scope{}, store.ErrNoScope
	}
	return sc, nil
}

// devScope returns the single local user when the server is running without
// authentication.
//
// This exists so the dev server is usable before the login UI lands (M2). It is
// gated on DevMode, which cmd/articleflux only sets for a loopback bind — an
// internet-facing instance with no auth would be an open reader.
func (a *App) devScope(ctx context.Context) (store.Scope, error) {
	if !a.cfg.DevMode {
		return store.Scope{}, store.ErrNoScope
	}
	sc, err := a.repo.FirstUserScope(ctx)
	if err != nil {
		return store.Scope{}, store.ErrNoScope
	}
	return sc, nil
}

// EnsureDevUser creates the local tenant and user if the database is empty, and
// returns the scope to use.
func (a *App) EnsureDevUser(ctx context.Context, username, password string) (store.Scope, error) {
	if sc, err := a.repo.FirstUserScope(ctx); err == nil {
		return sc, nil
	}
	hash, err := secret.HashPassword(password, secret.Active())
	if err != nil {
		return store.Scope{}, err
	}
	tenantID, userID := idgen.New(), idgen.New()
	if err := a.repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: tenantID,
		Name:     "Local",
		UserID:   userID,
		Username: username,
		Hash:     hash,
		Role:     "superadmin",
		Now:      time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		return store.Scope{}, err
	}
	a.log.Info("created local account", "username", username)
	return store.Scope{TenantID: tenantID, UserID: userID, Role: "superadmin"}, nil
}

// ErrNoUser is returned when no account exists yet.
var ErrNoUser = errors.New("app: no user")
