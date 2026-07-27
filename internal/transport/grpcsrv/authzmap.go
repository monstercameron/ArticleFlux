package grpcsrv

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/authz"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Policy is the per-method capability map §7.5 asked for, and the interceptor
// that enforces it.
//
// # Why this file exists
//
// `internal/authz` was written, tested, and then imported by nothing. Thirty-five
// handlers resolved a Scope; exactly one — SmartServer — checked a role. So the
// enforcement model was "each handler remembers", which is the model the authz
// package's own doc comment names as the failure worth avoiding.
//
// It was not theoretical. `ListLogs` resolved a scope and stopped there, with a
// comment noting that log lines carry feed URLs and host names. The ring also
// holds `login failed username=… client=<ip>`, so any member of a multi-user
// instance could read other people's usernames and addresses — account
// enumeration through the diagnostics screen, walking straight around the tenant
// isolation the store layer enforces structurally.
//
// One forgotten check is a bug. No place to forget it is a design.
//
// # The map is keyed by FULL method name
//
// `/articleflux.v1.ReaderService/GetItem`, not `GetItem`. Two services may
// legitimately hold a method of the same name, and a short-name map would let a
// public method on one service quietly authorise a privileged method on another
// — a collision that would look like a typo and behave like a grant.

// DefaultPolicy returns the map for every RPC this server registers.
//
// EVERY method is listed. An unlisted one is denied at runtime (authz.Map.Check)
// and refuses to start the server (Unmapped, called from app.Preflight), so this
// list going stale is a loud failure rather than a silent grant.
func DefaultPolicy() *authz.Map {
	const (
		reader = "/articleflux.v1.ReaderService/"
		auth   = "/articleflux.v1.AuthService/"
		system = "/articleflux.v1.SystemService/"
		smart  = "/articleflux.v1.SmartService/"
		events = "/articleflux.v1.EventService/"
	)

	m := authz.NewMap()

	// --- unauthenticated ------------------------------------------------------
	//
	// Five, and each is here because the credential either does not exist yet or
	// IS the request:
	//
	//   - Login and Reauthenticate carry the password; they are the authentication.
	//   - RefreshSession carries the refresh token, which is its own authorisation
	//     (and reuse revokes the family — see RotateRefresh).
	//   - Logout revokes the session named by the token presented with it. Holding
	//     the token is the right to end it, and a logout that can fail closed
	//     strands a user with a session they cannot revoke.
	//   - WhoAmI and GetVersion are what the client calls BEFORE it knows whether
	//     it is signed in — version skew is checked ahead of the login screen, and
	//     WhoAmI's handler resolves a scope itself and answers Unauthenticated
	//     when there is none.
	//   - CheckHealth is a health check. One that requires a credential is one no
	//     load balancer can use.
	m.Public(
		auth+"Login",
		auth+"Logout",
		auth+"WhoAmI",
		auth+"RefreshSession",
		auth+"Reauthenticate",
		system+"GetVersion",
		system+"CheckHealth",
	)

	// --- self-service ---------------------------------------------------------
	//
	// Both are additionally behind requireSudo, which is the real control; the
	// capability is what stops an API token minted for a phone from reaching them
	// at all (CapSelfAccount is deliberately absent from every token scope).
	m.Require(auth+"ChangePassword", authz.CapSelfAccount)
	m.Require(auth+"RegenerateRecoveryCodes", authz.CapSelfAccount)
	m.Require(reader+"SetPrefs", authz.CapSelfAccount)

	// --- reading --------------------------------------------------------------
	for _, method := range []string{
		"ListFeeds", "ListItems", "GetItem", "ScrollLiveView", "Search",
		"GetPrefs", "ListTags", "ListFolders", "ListNotes", "GetFeedSettings",
	} {
		m.Require(reader+method, authz.CapReadItems)
	}

	// --- per-user state -------------------------------------------------------
	//
	// RecordEngagements is here rather than under reading, and the distinction is
	// load-bearing: engagements drive the interest model, so a viewer able to
	// write them could steer somebody else's ranked homepage by reading it.
	for _, method := range []string{
		"SetItemState", "MarkAllRead", "UndoMarkAllRead", "RecordEngagements",
	} {
		m.Require(reader+method, authz.CapWriteState)
	}
	m.Require(reader+"SetNote", authz.CapWriteNotes)
	m.Require(reader+"SetFeedTag", authz.CapManageTags)
	m.Require(reader+"UpdateTag", authz.CapManageTags)

	// --- subscriptions and organisation ---------------------------------------
	//
	// AnalyzeSite is a subscribe capability rather than a read one because it
	// makes this server fetch a URL the caller chose — the SSRF surface netguard
	// exists for. Whoever may not subscribe may not aim the fetcher either.
	for _, method := range []string{"Subscribe", "SubscribeScrape", "Unsubscribe", "AnalyzeSite"} {
		m.Require(reader+method, authz.CapSubscribe)
	}
	for _, method := range []string{"Refresh", "UpdateFeedSettings", "SetFeedFolder"} {
		m.Require(reader+method, authz.CapManageFeeds)
	}
	for _, method := range []string{"CreateFolder", "RenameFolder", "DeleteFolder"} {
		m.Require(reader+method, authz.CapManageViews)
	}

	// --- Smart+ ---------------------------------------------------------------
	//
	// The config holds the instance's OpenAI key, so it is tenant settings, not a
	// preference. SmartServer also checks the role inline; that check stays as
	// defence in depth, and the two agree by construction because admin and
	// superadmin are exactly the roles holding CapTenantSettings.
	m.Require(smart+"GetSmartConfig", authz.CapTenantSettings)
	m.Require(smart+"SetSmartConfig", authz.CapTenantSettings)
	// Choosing an interface language is not an administrative act.
	m.Require(smart+"ListLanguages", authz.CapReadItems)
	m.Require(smart+"TranslateUI", authz.CapReadItems)
	// Neither is choosing what your own reader looks like (§20.16.3). These are
	// the only two methods on this service with no inline role check, and the
	// spend is bounded per-user by ratelimit.ThemePerUser instead — the same line
	// the Smart+ voice draws, and for the same reason: a member may pay for their
	// own reading experience, and may not read the key or re-bill the instance.
	//
	// CapReadItems rather than CapSelfAccount, which SetPrefs holds: an API token
	// minted for a phone is deliberately denied CapSelfAccount, and a token that
	// can read the feed should be able to render it in the theme the account has
	// chosen.
	m.Require(smart+"ComposeTheme", authz.CapReadItems)
	m.Require(smart+"SuggestTheme", authz.CapReadItems)

	// --- diagnostics ----------------------------------------------------------
	//
	// THE FIX. Both used to require only a valid session.
	//
	// The log ring is the whole server's, not the caller's: feed URLs, host
	// names, error text, and `login failed username=… client=<ip>` for every
	// account on the box. Server stats are instance-wide too — poller lag, tunnel
	// counts, per-method latency — which is a usage profile of everyone else.
	//
	// CapReadDiagnostics belongs to superadmin alone.
	m.Require(system+"ListLogs", authz.CapReadDiagnostics)
	m.Require(system+"GetServerStats", authz.CapReadDiagnostics)

	// --- events (streaming) ---------------------------------------------------
	//
	// The only stream in the API, and the reason AuthzStream exists. A unary
	// interceptor does not see stream RPCs AT ALL — wiring only the unary one
	// would have left the single long-lived, per-scope subscription as the one
	// surface with no authorization on it, which is the opposite of the intended
	// result and would have looked completely done.
	m.Require(events+"WatchEvents", authz.CapReadItems)

	return m
}

// ScopeFunc resolves a credential from the context.
type ScopeFunc func(context.Context) (store.Scope, error)

// DenialFunc is called when a request is refused, for metrics. Nil disables it.
//
// `mapped` distinguishes "this caller may not" from "nobody wrote a rule": the
// second is a deployment error and deserves a different alert from the first,
// which is a policy working.
type DenialFunc func(ctx context.Context, method string, mapped bool)

// AuthzUnary enforces the policy before any handler runs.
//
// # Order in the chain
//
// It sits AFTER version skew and BEFORE idempotency. Before idem specifically:
// a refused call must not consume an idempotency key, or a caller could burn
// another client's key by replaying a request they are not allowed to make, and
// the legitimate retry would then be answered from a cache entry for a call that
// never happened.
//
// # Why it resolves the scope itself
//
// It does not trust anything downstream to have done it. The handler's own
// scopeOf call remains — that is not redundancy to be cleaned up, it is the
// property that a handler is safe even if this interceptor is ever unwired
// again, which is exactly the state this file was written to fix.
func AuthzUnary(policy *authz.Map, scopeOf ScopeFunc, log *slog.Logger, onDeny DenialFunc) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {

		method := info.FullMethod

		// A public method is answered without resolving anything. Login must work
		// with no credential, and resolving one first would make a malformed
		// token an authentication error on the endpoint whose job is to issue one.
		if policy.IsPublic(method) {
			return handler(ctx, req)
		}

		sc, err := scopeOf(ctx)
		if err != nil || !sc.Valid() {
			// Unauthenticated, NOT PermissionDenied, and the difference is what the
			// client does about it: Unauthenticated means show the login screen,
			// PermissionDenied means this account will never be allowed and a
			// login screen would be a lie that loops.
			if onDeny != nil {
				onDeny(ctx, method, true)
			}
			return nil, errKey(codes.Unauthenticated, "srv.notSignedIn", "not signed in", nil)
		}

		caller := authz.Caller{
			TenantID: sc.TenantID,
			UserID:   sc.UserID,
			Role:     authz.Role(strings.ToLower(sc.Role)),
		}
		if err := policy.Check(method, caller); err != nil {
			var notMapped authz.ErrNotMapped
			mapped := !errors.As(err, &notMapped)
			if !mapped {
				// A deployment error wearing a permission error's clothes. Logged
				// at ERROR with the method name because the fix is a one-line map
				// entry and the person who can make it is reading this log.
				log.Error("an RPC has no capability mapping, so it is denied",
					"method", method,
					"fix", "add it to grpcsrv.DefaultPolicy")
			} else {
				// Info, not Warn: a denial is the policy working. It becomes
				// interesting in aggregate, which is what the counter is for.
				log.Info("denied", "method", method, "role", sc.Role)
			}
			if onDeny != nil {
				onDeny(ctx, method, mapped)
			}
			// The same message either way. "This method does not exist in the map"
			// and "your role is too low" are both answered with the same words, so
			// the response cannot be used to map the server's own policy.
			return nil, errKey(codes.PermissionDenied, "srv.notAllowed",
				"your account is not allowed to do that", nil)
		}
		return handler(ctx, req)
	}
}

// AuthzStream is AuthzUnary for streaming RPCs.
//
// It exists because grpc.UnaryServerInterceptor does not see streams at all —
// not "sees them partially", not "sees the first message": a stream RPC bypasses
// the unary chain entirely. Wiring only the unary interceptor would have left
// WatchEvents — the one long-lived, per-scope subscription in the API — as the
// single surface with no authorization on it, while every dashboard and test
// said authorization was enforced.
//
// The check runs ONCE, at stream open. That is the right moment and also the
// only one available: there is no per-message hook, and a subscription whose
// authorisation is re-evaluated mid-flight would need a way to tear itself down
// that the event bus does not have. A revoked session therefore keeps its open
// stream until it disconnects — which is the same property session revocation
// has for any long-lived connection, and is why SessionTTL is not the control
// that matters here.
func AuthzStream(policy *authz.Map, scopeOf ScopeFunc, log *slog.Logger, onDeny DenialFunc) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler) error {

		method := info.FullMethod
		if policy.IsPublic(method) {
			return handler(srv, ss)
		}

		ctx := ss.Context()
		sc, err := scopeOf(ctx)
		if err != nil || !sc.Valid() {
			if onDeny != nil {
				onDeny(ctx, method, true)
			}
			return errKey(codes.Unauthenticated, "srv.notSignedIn", "not signed in", nil)
		}

		caller := authz.Caller{
			TenantID: sc.TenantID,
			UserID:   sc.UserID,
			Role:     authz.Role(strings.ToLower(sc.Role)),
		}
		if err := policy.Check(method, caller); err != nil {
			var notMapped authz.ErrNotMapped
			mapped := !errors.As(err, &notMapped)
			if !mapped {
				log.Error("a streaming RPC has no capability mapping, so it is denied",
					"method", method, "fix", "add it to grpcsrv.DefaultPolicy")
			} else {
				log.Info("denied", "method", method, "role", sc.Role)
			}
			if onDeny != nil {
				onDeny(ctx, method, mapped)
			}
			return errKey(codes.PermissionDenied, "srv.notAllowed",
				"your account is not allowed to do that", nil)
		}
		return handler(srv, ss)
	}
}

// RegisteredMethods lists every method a gRPC server actually serves, as full
// `/package.Service/Method` names.
//
// Read from the server's own reflection data rather than from a list somebody
// maintains. A hand-kept list is a second thing to forget, and the failure it
// produces — a new RPC that the boot check does not know to look for — is
// exactly the one the boot check exists to catch.
func RegisteredMethods(s *grpc.Server) []string {
	var out []string
	for name, info := range s.GetServiceInfo() {
		for _, m := range info.Methods {
			out = append(out, "/"+name+"/"+m.Name)
		}
	}
	sort.Strings(out)
	return out
}
