package app

import (
	"bufio"
	"fmt"
	"net"
	"net/http"

	"github.com/monstercameron/ArticleFlux/internal/clientaddr"
)

// trueClientAddr makes every per-client control downstream of it see the client
// rather than the proxy. TODO 7.3d.
//
// # What was broken
//
// Behind `deploy/nginx.conf` — the way this is meant to be hosted — every
// request arrives from 127.0.0.1, so everything keyed on the transport address
// keyed on one value for the entire instance. Three controls were affected and
// two of them were not merely weakened:
//
//	the login limiter (§7.1a)   one bucket for every account on the instance
//	the durable lockout (§7.3)  the per-address half counted the whole instance
//	the tunnel's abuse caps     8 connections and 30 upgrades A MINUTE, TOTAL
//
// The last one is not a security weakening, it is an outage: the ninth reader
// to open a tab is refused, and the log says "per-client connection cap
// exceeded" about a client that is nine different people.
//
// # Why the socket has to be rewritten and not just the request
//
// Rewriting `r.RemoteAddr` fixes the abuse caps, because those are evaluated on
// the upgrade request. It does nothing for the login limiter, because RPCs are
// not this request — they are synthesised by an HTTP/2 server reading the
// WebSocket, and that server takes their RemoteAddr from the CONNECTION's
// RemoteAddr, which traces back through the tunnel's net.Conn to whatever
// `Hijack()` returned. So the hijacked socket is wrapped too, and reports the
// forwarded address as its own. The gRPC peer, and therefore
// grpcsrv.clientKey, then reads the truth without knowing anything about
// proxies.
//
// That is also why this cannot live in the tunnel library: the ResponseWriter
// handed to it is the last place the two halves are still reachable from one
// piece of code.
//
// # Why it is inert without -behind-proxy
//
// With nothing in front, `r.RemoteAddr` is already the client and the headers
// are attacker-controlled. The middleware returns `next` unchanged in that
// case, so a direct bind carries no wrapper, no allocation and no new way to be
// wrong — and the dangerous configuration is the one that has to be asked for.
//
// # And why the hop count travels with it
//
// `-behind-proxy` answers "is anything forwarding"; `TrustedProxyHops` answers
// "how many". X-Forwarded-For is a list whose left-hand end the CALLER writes —
// nginx appends rather than replaces — so the second question decides which
// entry is believed, and getting it wrong reinstates exactly the hole this
// middleware exists to close. `clientaddr` holds the rule and the argument.
func (a *App) trueClientAddr(next http.Handler) http.Handler {
	if !a.cfg.BehindProxy {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, ok := clientaddr.Forwarded(r, a.cfg.TrustedProxyHops)
		if !ok {
			// A trusted proxy that said nothing leaves the transport address
			// alone. Substituting a placeholder would be inventing a client.
			next.ServeHTTP(w, r)
			return
		}
		// Port 0 rather than the proxy's ephemeral port, which belongs to a
		// different connection and would read as this client's. Zero says
		// "known host, unknown port", and every consumer keys on the host.
		addr := &net.TCPAddr{IP: net.IP(ip.AsSlice()), Zone: ip.Zone()}

		// A shallow copy: the handler downstream must not see a mutated
		// original, and r.Clone would deep-copy a context and header set on
		// every request to no purpose.
		fixed := *r
		fixed.RemoteAddr = addr.String()
		next.ServeHTTP(&hijackAddrWriter{ResponseWriter: w, remote: addr}, &fixed)
	})
}

// hijackAddrWriter hands out a socket that lies about where it came from, on
// purpose and in the client's favour.
type hijackAddrWriter struct {
	http.ResponseWriter
	remote net.Addr
}

// Unwrap keeps http.ResponseController working through this layer.
func (h *hijackAddrWriter) Unwrap() http.ResponseWriter { return h.ResponseWriter }

// Flush and Hijack are declared rather than inherited, and that is not
// bookkeeping: embedding an interface promotes only that interface's methods,
// so a wrapper that omits these does not satisfy http.Flusher or http.Hijacker
// no matter what it holds. The WebSocket upgrade asserts for http.Hijacker
// directly, so omitting Hijack here turns every tunnel into a 500 — which is
// exactly how the metrics middleware broke the client once already.
func (h *hijackAddrWriter) Flush() {
	if f, ok := h.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *hijackAddrWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := h.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("app: %T does not support hijacking", h.ResponseWriter)
	}
	c, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	return addrConn{Conn: c, remote: h.remote}, rw, nil
}

// addrConn is the socket, with one answer replaced.
//
// Only RemoteAddr changes. Everything else — deadlines, reads, writes, close —
// is the real connection, because this is a relabelling and not a proxy.
type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.remote }
