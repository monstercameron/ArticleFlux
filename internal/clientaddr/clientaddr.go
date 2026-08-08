// Package clientaddr answers one question: who made this request?
//
// It exists because the answer is not `r.RemoteAddr`, and the places that need
// it are far apart — the tunnel's abuse caps, the login limiter, the durable
// lockout ledger and every log line that names a client. Three of those had
// their own spelling of the answer, and two of the three were wrong in the same
// deployment.
//
// # The deployment that breaks the obvious answer
//
// `deploy/nginx.conf` terminates TLS on :443 and forwards to 127.0.0.1:9000,
// which is the way this is meant to be hosted. Under that topology
// `r.RemoteAddr` is nginx, identically, for every user on the instance — so any
// control keyed on it stops being per-client and becomes per-instance. That is
// not a degraded control; it is a different control with the same name, and it
// fails in both directions at once:
//
//   - As a LIMIT it stops limiting. One bucket shared by everyone means an
//     attacker's guesses and a legitimate reader's logins are indistinguishable,
//     and the per-IP half of §7.1a's limiter was doing nothing at all.
//   - As a CAP it starts refusing. `WithMaxConnectionsPerClient(8)` against one
//     shared key caps the WHOLE INSTANCE at eight tunnels — the ninth reader is
//     refused, and the message says "per-client connection cap exceeded".
//
// # Why the header is conditional
//
// X-Forwarded-For is a request header, so any client can send one. Believing it
// unconditionally is worse than ignoring it: it would let an attacker write a
// fresh address per attempt and walk straight through the very limiter this
// package exists to make real. So it is believed only when the operator has
// asserted `-behind-proxy`, and the systemd unit's loopback bind is what makes
// that assertion true — nothing but nginx can reach the socket, so nothing but
// nginx can set the header.
//
// # Why the RIGHTMOST entry is taken, and why the leftmost was a hole
//
// This package used to take the leftmost X-Forwarded-For entry, on the reading
// that "XFF is a chain each hop appends to, so the first entry is the original
// client". The first half is true and the conclusion does not follow, because
// the first entry is not written by a hop — it is written by whoever sent the
// request, and that is the attacker.
//
// `deploy/nginx.conf` forwards with `$proxy_add_x_forwarded_for`, which is
// defined as `$http_x_forwarded_for, $remote_addr`: nginx APPENDS the address
// it actually saw to whatever the client claimed. So a request arriving with
//
//	X-Forwarded-For: 1.2.3.4
//
// reaches this process as `1.2.3.4, <real client>` — and the leftmost read
// returned 1.2.3.4, a value the attacker chose, once per attempt. That is
// strictly worse than the shared-bucket failure this package was written to
// fix: a limiter keyed on one shared address at least counts, and a limiter
// keyed on an attacker-supplied string does not exist. It reached the login
// limiter, the durable per-address lockout, the tunnel's connection caps, the
// HTTP rate limiter, and the `client` field `audit_log` keeps as recovery
// evidence.
//
// The only entries in that list this process can believe are the ones a hop it
// trusts appended. Counting from the RIGHT is what identifies them: the
// rightmost element of `XFF..., RemoteAddr` is the immediate peer, the one
// before it is the address that peer saw, and so on. With `hops` trusted
// proxies in front, the client is the entry `hops` places left of RemoteAddr,
// and everything further left is unverifiable text.
//
// That is why the hop count is a parameter rather than a constant. The standard
// topology is one nginx, so the default is 1 and the answer is the LAST XFF
// entry; a box behind a CDN as well has two, and reading the last entry there
// would report the CDN's edge for everybody. Getting the number wrong is
// visible in exactly one direction — too many hops reads an attacker-supplied
// entry, too few reads a proxy — so the count is the operator's assertion, made
// alongside `-behind-proxy`, and the default is the topology this repository
// ships.
package clientaddr

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultHops is the number of trusted proxies in the topology this repository
// ships: one nginx, on the same box, terminating TLS.
//
// It is the default rather than the only value because a box behind a CDN has
// two, and a deployment that put a load balancer in front of nginx would have
// three. Nothing in the process can measure this — every hop looks identical
// from here — so it is an assertion the operator makes, like -behind-proxy
// itself.
const DefaultHops = 1

// Forwarded reports the client address a trusted proxy observed, if any.
//
// hops is how many proxies the operator has asserted are in front. Values below
// one are read as one: zero hops means nothing is forwarding, and in that case
// the caller should not be consulting headers at all.
//
// ok is false when no entry at the trusted position can be parsed, which is the
// caller's signal to leave r.RemoteAddr alone rather than to substitute a
// placeholder: an address the server invented is worse than the transport
// address it already had. Deliberately no sliding — a garbage entry at the
// trusted position is NOT a reason to read the one beside it. The neighbour is
// either a proxy (reporting infrastructure as a client) or attacker-supplied
// (the hole this whole rewrite closes), and "one unparseable entry unlocks the
// next" is a rule an attacker can satisfy on purpose.
func Forwarded(r *http.Request, hops int) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	if hops < 1 {
		hops = 1
	}

	// The chain as this process can verify it: every X-Forwarded-For entry,
	// then the peer that actually opened the socket. RemoteAddr is on the end
	// because it is the one element here nobody could have forged — and it is
	// what makes the count from the right mean something.
	if xff := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(xff) != "" {
		entries := strings.Split(xff, ",")
		// hops places left of RemoteAddr. With one proxy that is the last XFF
		// entry, which is the address nginx saw.
		if i := len(entries) - hops; i >= 0 && i < len(entries) {
			if a, ok := Parse(entries[i]); ok {
				return a, true
			}
		}
		// A chain SHORTER than the asserted hop count means the request did not
		// come through the whole declared path — which is either a
		// misconfiguration or somebody reaching the app around the proxy. Either
		// way there is no entry a trusted hop wrote, so nothing here is
		// believed. Falling through to X-Real-IP below is safe for the same
		// reason it is safe in general: nginx SETS that header rather than
		// appending to it.
	}

	// X-Real-IP, and only for a single trusted hop.
	//
	// `proxy_set_header X-Real-IP $remote_addr` overwrites whatever the client
	// sent, so under one nginx it is authoritative and needs no counting. With
	// two hops it is whatever the outer proxy saw — the CDN edge, not the
	// reader — so it answers a different question and is not consulted.
	if hops == 1 {
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			if a, ok := Parse(xr); ok {
				return a, true
			}
		}
	}
	return netip.Addr{}, false
}

// Of reports the client's IP as a string, honouring the proxy headers only when
// trustProxy says a proxy the operator controls is in front.
//
// The empty string is never returned: with no believable header and an
// unparseable RemoteAddr the raw RemoteAddr comes back, because a log line with
// a strange address in it is more useful than one with a blank.
func Of(r *http.Request, trustProxy bool, hops int) string {
	if r == nil {
		return ""
	}
	if trustProxy {
		if a, ok := Forwarded(r, hops); ok {
			return a.String()
		}
	}
	if a, ok := Parse(r.RemoteAddr); ok {
		return a.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// Parse turns any of the spellings an address arrives in into a bare IP.
//
// It accepts "1.2.3.4", "1.2.3.4:5678", "[::1]:5678", "::1" and "[::1]",
// because all five reach this code from somewhere: a header written by hand, a
// Go RemoteAddr, a gRPC peer, and both bracket conventions in between.
func Parse(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	// Only splits when there really is a port; SplitHostPort rejects a bare IPv6
	// as "too many colons", which is exactly the answer wanted here.
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	// Unmap so ::ffff:1.2.3.4 and 1.2.3.4 land in ONE bucket. Left mapped they
	// are two keys for one host, and an attacker who noticed could double every
	// budget in the program by alternating spellings — the same class of bug
	// netguard fixed on the outbound side.
	return a.Unmap(), true
}

// Key normalises an address string into a stable bucket key for a limiter.
//
// Both the port and the bracket convention have to go. A limiter keyed on
// "1.2.3.4:51234" is keyed on the connection rather than the client, and gives
// every new socket a fresh budget — which is no limiter at all.
func Key(s string) string {
	if a, ok := Parse(s); ok {
		return a.String()
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return s
}
