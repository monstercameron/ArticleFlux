//go:build js && wasm

package data

import (
	"context"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/monstercameron/ArticleFlux/internal/buildver"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/platform"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Where the credential lives between reloads.
//
// Namespaced so a future second app on the same origin cannot collide, and
// versioned so a change to what is stored is a change to the key rather than a
// migration of somebody's browser.
const (
	tokenKey  = "articleflux.v1.token"
	deviceKey = "articleflux.v1.device"
)

// The token in memory. Read on every RPC by the interceptor below, and written
// by Login, SignOut, and the boot-time load.
//
// Package-level rather than a Client field on purpose: the interceptor is
// installed when the connection is dialled, which is before anyone has logged
// in, and a token that arrives later has to reach it. A mutex rather than an
// atomic because it is read far more often than written and the read is a string
// copy either way.
var (
	tokenMu sync.RWMutex
	token   string
)

// Token returns the stored credential, loading it from the browser on first use.
func Token() string {
	tokenMu.RLock()
	t := token
	tokenMu.RUnlock()
	return t
}

// LoadToken restores the credential from local storage. Call once at boot.
//
// It returns whether one was found, which is what decides between showing the
// reader and showing the login screen — the actual validity of the token is the
// server's business, and asking it is what WhoAmI is for.
func LoadToken() bool {
	t := platform.LocalGet(tokenKey)
	tokenMu.Lock()
	token = t
	tokenMu.Unlock()
	return t != ""
}

// setToken updates memory and storage together.
func setToken(t string) {
	tokenMu.Lock()
	token = t
	tokenMu.Unlock()
	if t == "" {
		platform.LocalRemove(tokenKey)
		return
	}
	platform.LocalSet(tokenKey, t)
}

// clientLabel returns this browser profile's stable presentation label,
// minting one if needed.
//
// Stable across logins so the account screen can eventually say "this laptop"
// rather than listing a fresh anonymous session for every time someone signed
// in. Deliberately NOT cleared by SignOut for the same reason — it identifies a
// browser, not a session, and it is not a credential.
//
// §7.3a SEC1: this value travels as LoginRequest.label and the server never
// treats it as anything more than presentation metadata — it is not, and must
// never become, a row identifier. Before the fix a value shaped exactly like
// this one WAS the devices table's primary key, which is what let a chosen or
// guessed id mint a session for whoever already owned that row.
func clientLabel() string {
	if id := platform.LocalGet(deviceKey); id != "" {
		return id
	}
	// Not idgen: that is a server package, and pulling it in would ship its
	// dependencies into the wasm bundle for one string. A timestamp plus the
	// tunnel's own randomness is enough — this value is a label, not a secret,
	// and a collision costs two browsers sharing a name on a settings screen,
	// nothing more, because the server no longer uses it as a lookup key.
	id := "dev-" + time.Now().UTC().Format("20060102150405.000000000")
	platform.LocalSet(deviceKey, id)
	return id
}

// onUnauthenticated is called when the server rejects the credential.
//
// Set by the view layer at boot. A package-level hook rather than a Client field
// because the interceptor that detects it is installed at dial time and outlives
// any particular screen.
var onUnauthenticated func()

// SetUnauthenticatedHandler installs the callback for a rejected credential.
func SetUnauthenticatedHandler(fn func()) { onUnauthenticated = fn }

// stamp attaches the credential and the build version to an outgoing context.
//
// Shared by both interceptors below rather than written twice, and that is the
// whole point of it existing: the two halves drifting is not a hypothetical
// failure, it is the one that happened. Only the unary interceptor was ever
// installed, so every STREAMING RPC opened with no `authorization` header at
// all — and on a server with real authentication (`-dev` off) `scopeFromContext`
// then resolved nobody and `AuthzStream` refused the call as Unauthenticated.
//
// It cost the two streams in the API, silently and only in production:
// `WatchEvents` — the live event pump — was refused several hundred times a day,
// and `GetAudioTrack` meant the broadcast's music never arrived, which the
// client is designed to render as silence rather than as an error. Locally, a
// `-dev` server answers with the first user's scope whether or not a credential
// was sent, so both worked on every machine anybody tested them on.
//
// The build stamp goes on EVERY call including the unauthenticated ones
// (§22.10, TODO 7.8). Attached here rather than in a handshake because there
// is no handshake — the tunnel multiplexes ordinary RPCs and which one is
// first varies — and attached unconditionally because the client that most
// needs to be identified as stale is the one that cannot log in.
//
// This is a constant compiled into the bundle, so a Service Worker still
// serving last month's wasm announces last month's version, which is exactly
// what the server needs to know.
func stamp(ctx context.Context) context.Context {
	if t := Token(); t != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+t)
	}
	return metadata.AppendToOutgoingContext(ctx, buildver.ClientStampHeader, buildver.Version)
}

// authInterceptor attaches the credential to every outgoing call and notices
// when the server rejects it.
//
// One interceptor rather than a header on each of the thirty-odd methods in
// client.go: the failure mode of the per-method version is that someone adds the
// thirty-first and it silently runs unauthenticated, which surfaces as one
// mysteriously empty pane rather than as a login prompt.
func authInterceptor(ctx context.Context, method string, req, reply any,
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	err := invoker(stamp(ctx), method, req, reply, cc, opts...)

	// Unauthenticated means "the credential you sent is not good". For a reader
	// RPC that is a session that has ended mid-use — expired, revoked from another
	// device, or a database replaced underneath us — and the right response is to
	// drop the token and send the page back to the login screen.
	//
	// **AuthService is excluded, entirely, and that exclusion is load-bearing.**
	// Every method on it is a QUESTION ABOUT authentication rather than a call
	// that requires it, so Unauthenticated is a valid answer and not a failure:
	//
	//   - Login answers it for a wrong password. That is a normal thing that
	//     happens on the login screen.
	//   - WhoAmI answers it for "nobody is logged in", which is precisely what
	//     Root asks at boot when local storage holds no token.
	//
	// Getting this wrong produced an infinite reload loop rather than a subtle
	// bug: Root dials, asks WhoAmI, gets the correct Unauthenticated, this handler
	// reloads the page, and the new page does the same thing forever. The login
	// screen never survived long enough to submit, so the visible symptom was
	// "Couldn't sign in. Check the server is running" — a message about the
	// transport, for a server that was answering perfectly.
	//
	// Matched on the service prefix rather than method by method: a future
	// RefreshSession or Logout must not have to remember to add itself here, and
	// the failure mode of forgetting is a reload loop.
	if status.Code(err) == codes.Unauthenticated && !isAuthMethod(method) {
		setToken("")
		if onUnauthenticated != nil {
			onUnauthenticated()
		}
	}
	return err
}

// streamAuthInterceptor is authInterceptor for streaming RPCs.
//
// It exists for the same reason `grpcsrv.AuthzStream` does, at the other end of
// the wire: `grpc.WithUnaryInterceptor` does not see streams AT ALL — not
// partially, not for the opening message. A stream bypasses the unary chain
// entirely, so for as long as this was the missing half, `WatchEvents` and
// `GetAudioTrack` were the only two calls in the application that carried no
// credential. See `stamp` for what that cost.
//
// # Why it does not watch for a rejected credential
//
// The unary interceptor drops the token and sends the page to the login screen
// on Unauthenticated. This does not, and the omission is deliberate rather than
// pending: `streamer` returns a ClientStream as soon as the call is created, and
// the server's refusal arrives later, on the first `RecvMsg`. Catching it would
// mean wrapping every stream in a decorator whose only job is to inspect an
// error that something else already handles — a session that has ended fails
// the next unary call too, and the reader makes several a minute. The wrapper
// would be code that can be wrong for a case that cannot be missed.
func streamAuthInterceptor(ctx context.Context, desc *grpc.StreamDesc,
	cc *grpc.ClientConn, method string, streamer grpc.Streamer,
	opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return streamer(stamp(ctx), desc, cc, method, opts...)
}

// authServicePrefix is "/articleflux.v1.AuthService/".
//
// Derived from a generated constant rather than written out, so renaming the
// service in the proto cannot silently turn the guard above off.
var authServicePrefix = pb.AuthService_Login_FullMethodName[:strings.LastIndexByte(
	pb.AuthService_Login_FullMethodName, '/')+1]

func isAuthMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, authServicePrefix)
}

// Login exchanges credentials for a session and stores it.
//
// The token is persisted before this returns, so a caller that navigates
// straight to the reader is already authenticated.
func (c *Client) Login(parent context.Context, username, password string) (*pb.LoginResponse, error) {
	// Longer than callTimeout's cousins elsewhere would suggest, and shorter than
	// they are: Argon2id takes a deliberate ~50ms on the server and the tunnel may
	// still be connecting, but a login that has not answered in fifteen seconds is
	// not going to.
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()

	res, err := c.auth.Login(ctx, &pb.LoginRequest{
		Username: username,
		Password: password,
		Label:    clientLabel(),
	})
	if err != nil {
		return nil, err
	}
	setToken(res.GetToken())
	return res, nil
}

// WhoAmI reports who the stored credential belongs to.
//
// This is the boot-time question: a token in local storage says nothing about
// whether the server still honours it, and the alternative — assuming it is good
// and letting the first real RPC fail — flashes the reader's furniture for a
// moment before dropping to the login screen.
// Setup claims a brand-new instance: it creates the first account and returns a
// session, so the caller lands in the reader rather than at a login screen.
//
// The recovery codes come back in the response and NOWHERE else — they are
// stored hashed — so a caller that drops this value has destroyed them. The
// screen shows them before it hands the connection on.
func (c *Client) Setup(parent context.Context, username, email, password string) (*pb.SetupResponse, error) {
	// Longer than Login's fifteen seconds. Setup hashes a password with the
	// instance's tuned Argon2id parameters AND writes a sheet of recovery codes,
	// on a box whose cost was calibrated to take a quarter-second per hash on
	// hardware that may be busier now than it was at boot.
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()

	res, err := c.auth.Setup(ctx, &pb.SetupRequest{
		Username: username,
		Email:    email,
		Password: password,
	})
	if err != nil {
		return nil, err
	}
	setToken(res.GetToken())
	return res, nil
}

func (c *Client) WhoAmI(parent context.Context) (*pb.WhoAmIResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	// Fail fast rather than WaitForReady: this runs while the page is deciding
	// what to render, and waiting twenty seconds for a server that is down would
	// hold the screen on a blank boot state. A transport failure here is reported
	// as such, not as "you are logged out".
	return c.auth.WhoAmI(ctx, &pb.WhoAmIRequest{}, grpc.WaitForReady(false))
}

// SignOut revokes the session server-side and clears it locally.
//
// The local clear happens whether or not the server call succeeds. A logout that
// leaves the credential in local storage because the network blipped is a logout
// that did not happen, and on a shared machine that is the worst possible time
// for it to have not happened. The session then expires on its own schedule
// server-side, which is a smaller problem than a live token in a browser someone
// has walked away from.
func (c *Client) SignOut(parent context.Context) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.auth.Logout(ctx, &pb.LogoutRequest{}, grpc.WaitForReady(false))
	setToken("")
	return err
}
