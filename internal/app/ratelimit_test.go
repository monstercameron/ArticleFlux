package app

import (
	"context"
	"net"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func ctxWithAuth(token string) context.Context {
	md := metadata.Pairs("authorization", token)
	return metadata.NewIncomingContext(context.Background(), md)
}

func ctxFromAddr(ip string, port int) context.Context {
	return peer.NewContext(context.Background(),
		&peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: port}})
}

// The same session is one bucket however it spells its scheme. A client
// sending "bearer" must not be handed a second budget by capitalisation.
func TestTheBearerSchemeIsNotPartOfTheKey(t *testing.T) {
	a := &App{}
	upper := a.rateKey(ctxWithAuth("Bearer tok-abc"))
	lower := a.rateKey(ctxWithAuth("bearer tok-abc"))
	if upper == "" {
		t.Fatal("no key was derived from a bearer token")
	}
	if upper != lower {
		t.Errorf("Bearer gave %q and bearer gave %q; one session, two budgets", upper, lower)
	}
}

func TestDifferentSessionsGetDifferentKeys(t *testing.T) {
	a := &App{}
	if one, two := a.rateKey(ctxWithAuth("Bearer tok-one")),
		a.rateKey(ctxWithAuth("Bearer tok-two")); one == two {
		t.Errorf("two sessions share the key %q; one busy client would limit the other", one)
	}
}

// The key ends up in a log line, and the session token is a bearer credential.
// Writing it out hands live sessions to anyone who can read the log.
func TestTheRawTokenNeverAppearsInTheKey(t *testing.T) {
	const token = "a-secret-session-token"
	a := &App{}
	if key := a.rateKey(ctxWithAuth("Bearer " + token)); strings.Contains(key, token) {
		t.Errorf("the key %q contains the raw token", key)
	}
}

// An unauthenticated call — Login, before there is a session — keys on the
// address. Behind a proxy that is now the caller's own rather than nginx's,
// which is the other half of this ticket.
func TestAnUnauthenticatedCallKeysOnTheAddress(t *testing.T) {
	a := &App{}
	key := a.rateKey(ctxFromAddr("203.0.113.7", 51234))
	if key != "c:203.0.113.7" {
		t.Errorf("key = %q, want c:203.0.113.7", key)
	}
}

// The port must not reach the key, or every new socket is a fresh budget.
func TestTheAddressKeyIgnoresThePort(t *testing.T) {
	a := &App{}
	if one, two := a.rateKey(ctxFromAddr("203.0.113.7", 1111)),
		a.rateKey(ctxFromAddr("203.0.113.7", 2222)); one != two {
		t.Errorf("two ports gave %q and %q; each reconnect would reset the limit", one, two)
	}
}

// Neither a credential nor an address. Empty means "cannot tell", and the
// interceptor lets those through rather than refusing everyone.
func TestAnUnidentifiableCallerYieldsNoKey(t *testing.T) {
	a := &App{}
	if key := a.rateKey(context.Background()); key != "" {
		t.Errorf("key = %q, want empty so the call fails open", key)
	}
}

// A session and an address must not collide: the prefixes are what keep a
// token that happens to hash to an address's spelling out of its bucket.
func TestSessionAndAddressKeysAreInDifferentNamespaces(t *testing.T) {
	a := &App{}
	session := a.rateKey(ctxWithAuth("Bearer tok"))
	address := a.rateKey(ctxFromAddr("203.0.113.7", 1))
	if !strings.HasPrefix(session, "s:") || !strings.HasPrefix(address, "c:") {
		t.Errorf("keys are not namespaced: session=%q address=%q", session, address)
	}
}
