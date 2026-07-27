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
// The LEFTMOST entry is taken. XFF is a chain each hop appends to, so the first
// entry is the original client and the rest are proxies. Taking the last one —
// which reads as "most recent, therefore most trustworthy" — gets you nginx's
// own address on every line, which is the bug this package is here to remove.
package clientaddr

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Forwarded reports the client address a trusted proxy declared, if any.
//
// ok is false when there is no usable header, which is the caller's signal to
// leave r.RemoteAddr alone rather than to substitute a placeholder: an address
// the server invented is worse than the transport address it already had.
func Forwarded(r *http.Request) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Leftmost, and only the leftmost. A chain with a garbage first entry is
		// not a reason to fall through to the second — the second is a proxy,
		// and reporting a proxy as the client is the failure being fixed.
		head := xff
		if i := strings.IndexByte(head, ','); i >= 0 {
			head = head[:i]
		}
		if a, ok := Parse(head); ok {
			return a, true
		}
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		if a, ok := Parse(xr); ok {
			return a, true
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
func Of(r *http.Request, trustProxy bool) string {
	if r == nil {
		return ""
	}
	if trustProxy {
		if a, ok := Forwarded(r); ok {
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
