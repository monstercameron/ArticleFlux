// Package netguard is the SSRF guard. Seven callers depend on it (§21): feed
// polling, favicon fetch, article extraction, OPML import by URL, WebSub
// verification, image proxying, and feed discovery.
//
// The threat is specific. ArticleFlux fetches URLs that users supply, from a server
// that sits inside a network the user cannot otherwise reach. "Subscribe to
// http://169.254.169.254/latest/meta-data/iam/security-credentials/" is a
// complete cloud-credential exfiltration attack delivered through a feature
// whose entire purpose is fetching user-supplied URLs.
//
// # Two layers, because one is not enough
//
// CheckURL validates a URL before the request. That catches the direct attempt
// and, re-run per redirect hop, the redirect-to-localhost variant.
//
// It does NOT catch **DNS rebinding**: an attacker controls evil.com, which
// resolves to a public address when we validate and to 127.0.0.1 a moment later
// when we dial. Nothing checkable in the URL differs between those two cases.
//
// So the real defence is at the socket: Dialer.Control runs after resolution
// with the actual IP about to be connected to, and rejects there. CheckURL is a
// fast rejection with a good error message; Control is the guarantee.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var (
	// ErrScheme is returned for anything that is not http or https.
	ErrScheme = errors.New("netguard: only http and https are allowed")
	// ErrNoHost is returned for a URL with no host.
	ErrNoHost = errors.New("netguard: url has no host")
	// ErrBlockedIP is returned when an address is in a blocked range.
	ErrBlockedIP = errors.New("netguard: address is not publicly routable")
	// ErrTooManyRedirects bounds a redirect chain.
	ErrTooManyRedirects = errors.New("netguard: too many redirects")
)

// blocked is every range that must never be reachable through a user-supplied
// URL. Each entry is here for a reason, not for completeness.
var blocked = func() []*net.IPNet {
	cidrs := []string{
		"0.0.0.0/8",          // "this network"
		"10.0.0.0/8",         // RFC1918
		"100.64.0.0/10",      // CGNAT — a real host on many home connections
		"127.0.0.0/8",        // loopback: the admin interface bound to localhost
		"169.254.0.0/16",     // link-local, which contains 169.254.169.254
		"172.16.0.0/12",      // RFC1918
		"192.0.0.0/24",       // IETF protocol assignments
		"192.0.2.0/24",       // TEST-NET-1
		"192.88.99.0/24",     // 6to4 relay anycast
		"192.168.0.0/16",     // RFC1918
		"198.18.0.0/15",      // benchmarking
		"198.51.100.0/24",    // TEST-NET-2
		"203.0.113.0/24",     // TEST-NET-3
		"224.0.0.0/4",        // multicast
		"240.0.0.0/4",        // reserved
		"255.255.255.255/32", // broadcast
		"::/128",             // unspecified
		"::1/128",            // IPv6 loopback
		"fc00::/7",           // unique local — the IPv6 RFC1918
		"fe80::/10",          // IPv6 link-local
		"ff00::/8",           // IPv6 multicast
		// NAT64 (RFC 6052) reaches IPv4 space through an IPv6 address. The whole
		// prefix is listed for the strict policy, but the rule that actually
		// decides is unwrapV4, which reduces 64:ff9b::a.b.c.d to a.b.c.d so the
		// embedded address is judged on its own merits under BOTH policies. This
		// entry is what catches a NAT64 address whose embedded IPv4 is public —
		// still a route into a translator we did not choose.
		"64:ff9b::/96",
		"2001:db8::/32", // documentation
		// NOTE: "::ffff:0:0/96" (IPv4-mapped IPv6) deliberately does NOT appear
		// here, and adding it back is a severe bug rather than defence in depth.
		// net.IPNet.Contains calls To4() on the network address, and
		// ::ffff:0:0.To4() is 0.0.0.0 — so the /96 collapses to 0.0.0.0/0 and
		// blocks the entire IPv4 internet. Mapped addresses are handled correctly
		// by the To4() unwrap in IsBlockedIP, which turns ::ffff:127.0.0.1 into
		// 127.0.0.1 and matches it against 127.0.0.0/8. Tested both ways.
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("netguard: bad CIDR " + c)
		}
		nets = append(nets, n)
	}
	return nets
}()

// neverAllowed are the ranges that stay blocked even when an operator has opted
// into private addresses.
//
// The distinction matters. "Let me subscribe to a feed on my LAN" is a
// reasonable request from someone self-hosting. "Let me subscribe to
// 169.254.169.254" is the cloud-credential attack this package exists to stop,
// and no amount of self-hosting makes it reasonable. Collapsing the two into one
// boolean — which the first version of AllowPrivateAddresses did — hands away
// the whole guard to buy a LAN feed.
var neverAllowed = func() []*net.IPNet {
	cidrs := []string{
		"169.254.0.0/16", // link-local, and therefore the metadata endpoint
		"0.0.0.0/8",      // "this network"
		"224.0.0.0/4",    // multicast
		"240.0.0.0/4",    // reserved
		"fe80::/10",      // IPv6 link-local
		"ff00::/8",       // IPv6 multicast
		"::/128",         // unspecified
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("netguard: bad CIDR " + c)
		}
		nets = append(nets, n)
	}
	return nets
}()

// unwrapV4 reduces every IPv6 form that carries an IPv4 address to that
// address, so one deny list covers all of them.
//
// There are THREE such forms, and Go's To4 only knows the first:
//
//   - IPv4-mapped, ::ffff:127.0.0.1 — handled by To4.
//   - IPv4-compatible, ::127.0.0.1 — deprecated by RFC 4291 and NOT handled by
//     To4, which requires bytes 10 and 11 to be 0xff. So it matched no IPv4 rule
//     (wrong length after no unwrap) and no IPv6 rule (no CIDR covers ::/96),
//     and ::169.254.169.254 reached the metadata endpoint under EVERY policy.
//   - NAT64, 64:ff9b::127.0.0.1 — the well-known prefix from RFC 6052. It was in
//     `blocked` as a whole /96, which held for the strict policy, but it was
//     absent from `neverAllowed`, so -allow-private let 64:ff9b::a9fe:a9fe
//     through — the metadata endpoint again, wearing a different hat.
//
// Unwrapping is better than adding two more CIDRs because it makes the embedded
// address subject to the SAME rules as the bare one, in both directions: a
// NAT64-wrapped 127.0.0.1 is loopback, so it is refused under the strict policy
// and permitted under the permissive one, exactly as 127.0.0.1 itself is. A CIDR
// per wrapper cannot express that — it can only block the whole prefix, which
// would have made -allow-private inconsistent with itself.
//
// :: and ::1 are excluded. RFC 4291 reserves both (the unspecified address and
// IPv6 loopback), and unwrapping ::1 to 0.0.0.1 would put IPv6 loopback in
// 0.0.0.0/8 — which is in neverAllowed — and so break -allow-private for the
// most ordinary self-hosted case there is.
func unwrapV4(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	if len(ip) != net.IPv6len {
		return ip
	}
	isZero := func(b []byte) bool {
		for _, c := range b {
			if c != 0 {
				return false
			}
		}
		return true
	}
	// IPv4-compatible: ::a.b.c.d, excluding :: and ::1.
	if isZero(ip[0:12]) {
		if ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && (ip[15] == 0 || ip[15] == 1) {
			return ip // :: and ::1 are reserved, not embedded addresses
		}
		return ip[12:16]
	}
	// NAT64 well-known prefix 64:ff9b::/96 (RFC 6052).
	if ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b && isZero(ip[4:12]) {
		return ip[12:16]
	}
	return ip
}

// matchesAny reports whether an address falls in any of `nets`, testing both the
// address as given and its unwrapped form.
//
// BOTH, and the order does not matter — what matters is that neither is skipped.
// Unwrapping alone would make a rule about a wrapper prefix unreachable, since
// the wrapper is gone by the time the CIDRs are consulted: `64:ff9b::/96` in
// `blocked` stopped matching anything the moment unwrapV4 landed, and a test
// caught it. Testing the raw form alone is the original bug. So: a wrapped
// address is refused if EITHER the wrapper or what it carries is refused.
func matchesAny(ip net.IP, nets []*net.IPNet) bool {
	candidates := [2]net.IP{ip, unwrapV4(ip)}
	for _, c := range candidates {
		for _, n := range nets {
			if n.Contains(c) {
				return true
			}
		}
	}
	return false
}

// IsNeverAllowed reports whether an address is blocked under every policy.
func IsNeverAllowed(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return matchesAny(ip, neverAllowed)
}

// IsBlockedIP reports whether an address must not be connected to.
//
// Every IPv6 form that carries an IPv4 address is unwrapped first — see
// unwrapV4. Without that, a wrapped loopback misses every IPv4 rule and matches
// no IPv6 rule, which is the single most common way an allow/deny list like this
// gets bypassed.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // an address we cannot understand is one we do not dial
	}
	return matchesAny(ip, blocked)
}

// CheckURL validates scheme and, when the host is a literal address, the address.
//
// A hostname is NOT resolved here. Resolving would double the DNS load and still
// prove nothing, because the resolution that matters is the one the dialer does.
// The dialer's Control is what enforces the rule; this is the early, legible
// rejection.
func CheckURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("netguard: %w", err)
	}
	return CheckParsedURL(u)
}

// CheckParsedURL is CheckURL for an already-parsed URL. This is what the redirect
// hook calls, since net/http hands it a *url.URL.
func CheckParsedURL(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: got %q", ErrScheme, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return ErrNoHost
	}
	// A literal address can be rejected now, before any DNS or TCP work.
	if ip := net.ParseIP(host); ip != nil && IsBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedIP, ip)
	}
	return nil
}

// MaxRedirects bounds a chain. Ten is what browsers use; the limit exists so a
// redirect loop is a bounded error rather than a hung fetch.
const MaxRedirects = 10

// Dialer returns a dialer whose Control rejects a blocked address after
// resolution and immediately before connect.
//
// This is the layer that actually holds: Control receives the concrete address
// the kernel is about to connect to, so a hostname that resolved to 127.0.0.1 —
// whether by rebinding, by a CNAME, or because the user simply pointed a domain
// at loopback — is refused with no packet sent.
func Dialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			switch network {
			case "tcp", "tcp4", "tcp6":
			default:
				return fmt.Errorf("%w: network %q", ErrBlockedIP, network)
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("netguard: %w", err)
			}
			ip := net.ParseIP(host)
			if IsBlockedIP(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedIP, host)
			}
			return nil
		},
	}
}

// Options configure the guarded client.
type Options struct {
	// AllowPrivate permits RFC1918, CGNAT and loopback — for a self-hosted
	// instance whose feeds live on the same network.
	//
	// It does NOT permit link-local, the cloud metadata endpoint, multicast or
	// reserved space: those stay blocked under every policy (see neverAllowed).
	AllowPrivate bool

	// Timeout bounds the whole request including redirects. Zero means 30s.
	Timeout time.Duration
	// DialTimeout bounds one connect. Zero means 10s.
	DialTimeout time.Duration
	// UserAgent identifies us to publishers. A reader that fetches anonymously is
	// one a publisher can only respond to by blocking.
	UserAgent string

	// Purpose names what this client is for — "feed", "favicon", "extract",
	// "asset", "page", "discover" — and is passed to Observer.
	//
	// A fixed word chosen by the CALLER, never anything derived from a URL: it
	// becomes a metric label, and a label taken from user input is both an
	// unbounded series count and, here, a published reading history.
	Purpose string
}

// Observer is called after every outbound request, if set.
//
// A package-level hook rather than an Options field because there are seven
// constructors across six packages and threading a recorder through all of them
// would mean six packages importing the telemetry layer — for a guard whose job
// is to be the one thing every fetch passes through. Set it once at startup,
// before any client is built.
//
// `status` is 0 when the request never produced a response. `err` is the
// transport error, and it is NOT safe to use as a metric label: it contains the
// host and often the URL.
var Observer func(purpose string, d time.Duration, status int, err error)

func observe(purpose string, start time.Time, res *http.Response, err error) {
	if Observer == nil {
		return
	}
	status := 0
	if res != nil {
		status = res.StatusCode
	}
	Observer(purpose, time.Since(start), status, err)
}

// Client returns an http.Client safe to point at a user-supplied URL.
//
// The redirect hook re-runs CheckParsedURL on **every hop**. Validating only the
// first URL is the single most common way an SSRF guard is defeated: the
// attacker submits a public URL that 302s to 169.254.169.254.
func Client(opt Options) *http.Client {
	if opt.Timeout == 0 {
		opt.Timeout = 30 * time.Second
	}
	if opt.DialTimeout == 0 {
		opt.DialTimeout = 10 * time.Second
	}
	d := Dialer(opt.DialTimeout)
	if opt.AllowPrivate {
		d = PermissiveDialer(opt.DialTimeout)
	}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          64,
		IdleConnTimeout:       90 * time.Second,
		// Proxies are disabled: an env-configured proxy would route around
		// Control entirely, since the connect target becomes the proxy.
		Proxy: nil,
	}
	return &http.Client{
		Timeout:   opt.Timeout,
		Transport: &uaTransport{next: tr, ua: opt.UserAgent, purpose: opt.Purpose},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return ErrTooManyRedirects
			}
			if opt.AllowPrivate {
				return CheckParsedURLPermissive(req.URL)
			}
			return CheckParsedURL(req.URL)
		},
	}
}

type uaTransport struct {
	next    http.RoundTripper
	ua      string
	purpose string
}

// tracer is resolved through OTel's GLOBAL provider, deliberately.
//
// The alternative is threading a tracer through seven constructors in six
// packages, which is the same argument the Observer hook above already makes
// and lost. The global is safe to take at package init: OTel returns a
// delegating tracer that becomes real if and when a provider is installed, and
// stays a no-op otherwise — which is every instance with no collector
// configured, i.e. the default.
var tracer = otel.Tracer("github.com/monstercameron/ArticleFlux/internal/netguard")

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.ua != "" && r.Header.Get("User-Agent") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", t.ua)
	}

	// A span per hop, so a trace shows WHERE an outbound fetch went slow rather
	// than only that it did. This is the largest unexplained interval in most
	// of this program's work: polling a feed, extracting a page and warming a
	// favicon are all mostly waiting on somebody else's server.
	//
	// The span name is the PURPOSE, which is a fixed word chosen by the caller
	// — never the URL, which would give every article its own span name and
	// make the trace list a reading history.
	//
	// The host IS recorded, and the path is not. That line is where it is for a
	// reason: the host is the publisher, which the logs already record as
	// feed_url and which is what "who is slow" means; the path is the article,
	// which is the reading history itself. Spans also go to a collector the
	// operator deliberately configured, unlike /metrics which answers anyone
	// who can reach it — but that is an argument for recording the host, not
	// for recording everything.
	ctx, span := tracer.Start(r.Context(), "egress."+t.purpose,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("server.address", r.URL.Hostname()),
			attribute.String("egress.purpose", t.purpose),
		))
	// **NOT `defer span.End()`.** A RoundTripper returns as soon as the response
	// HEADERS have arrived, and for the fetches this guards — a feed document, an
	// article page, an image being proxied — the headers are the cheap part. A
	// span that closed here answered "where did the fetch go slow" with
	// everything except the transfer, which is most of it: a 3 MB image over a
	// slow link read as a fast request, because the milliseconds it reported were
	// the milliseconds before the download started.
	//
	// So the span outlives this function and is ended by whoever finishes with
	// the body (see spanBody). Every path below ends it exactly once.
	r = r.WithContext(ctx)

	// Per HOP, which is what makes a redirect chain and a blocked address both
	// visible: a fetch refused by Control never reaches a status code, and
	// counting only the final response would record it as nothing at all.
	start := time.Now()
	res, err := t.next.RoundTrip(r)
	// The METRIC still stops here, and the span no longer does. That is
	// deliberate and worth stating, because the two now disagree by the transfer
	// time and somebody will notice: a histogram bucketed on total transfer
	// would mix "the publisher is slow to answer" with "the publisher's article
	// is large", and the first is the one an operator can act on. The span keeps
	// the whole story for the one fetch being looked at.
	observe(t.purpose, start, res, err)

	switch {
	case err != nil:
		// The transport error carries the host and often the URL, which is why
		// it must never become a metric label — but a span is not a label, and
		// "connection refused" versus "certificate expired" is the whole
		// answer when a feed stops updating.
		span.RecordError(err)
		span.SetStatus(codes.Error, "request failed")
		span.End()
	case res != nil:
		span.SetAttributes(attribute.Int("http.response.status_code", res.StatusCode))
		// 4xx and 5xx from a publisher are not THIS server erroring, so the
		// span is left Unset rather than Error: marking them red would make
		// every trace containing a dead feed look like an outage here.
		if res.StatusCode >= 500 {
			span.SetStatus(codes.Error, "upstream server error")
		}
		// Handed to the body, which ends it. `Body` is never nil from a
		// RoundTripper — http.NoBody stands in for an empty one — but the guard
		// costs a line and the failure it prevents is a span that never closes.
		if res.Body != nil {
			res.Body = &spanBody{ReadCloser: res.Body, span: span}
		} else {
			span.End()
		}
	default:
		// Neither a response nor an error is a transport contract violation, and
		// this exists so it costs a closed span rather than a leaked one.
		span.End()
	}
	return res, err
}

// spanBody ends the egress span when the response body is done with, which is
// the actual end of the fetch — see the note in RoundTrip.
//
// # Why Read ends it too, rather than only Close
//
// A reader that consumes to EOF and a reader that closes early are both
// finished, and only one of them is guaranteed to happen: `io.ReadAll` followed
// by a deferred `Close` does both, but a caller that reads to EOF inside a
// `defer`-less path would otherwise leave the span open until the request that
// created it was garbage. Ending on the first of the two — with `sync.Once`
// making the second free — is what makes this correct for both.
//
// The bytes read are recorded because they are the answer to the question the
// duration raises. "This fetch took nine seconds" is a different problem when it
// moved 40 KB than when it moved 12 MB, and without the size the two are
// indistinguishable.
type spanBody struct {
	io.ReadCloser
	span trace.Span
	once sync.Once
	n    int64
}

func (b *spanBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	switch {
	case errors.Is(err, io.EOF):
		b.end(nil)
	case err != nil:
		// A body that stopped mid-transfer. This is the case the old span could
		// not see AT ALL: the headers had arrived, so it had already recorded a
		// successful fast request, and the connection dying halfway through a
		// 3 MB article left no trace anywhere.
		b.end(err)
	}
	return n, err
}

func (b *spanBody) Close() error {
	err := b.ReadCloser.Close()
	// The close error is deliberately not recorded as a span error. A caller
	// that has finished early — a size cap, a parse failure, a cancelled poll —
	// closes a body it did not drain, and the transport reports that; calling it
	// a failed egress would paint every bounded read red.
	b.end(nil)
	return err
}

func (b *spanBody) end(err error) {
	b.once.Do(func() {
		b.span.SetAttributes(attribute.Int64("http.response.body.size", b.n))
		if err != nil {
			b.span.RecordError(err)
			b.span.SetStatus(codes.Error, "response body did not finish")
		}
		b.span.End()
	})
}

// Get is the convenience path: validate, then fetch with a guarded client.
//
// allowPrivate must match the policy c was built with (Client(Options{AllowPrivate: ...})).
// A *http.Client carries no field this function can read the policy back out of — its
// AllowPrivate is baked into closures over Dialer/CheckRedirect — so the caller states it
// again here rather than this function silently assuming the strict policy and refusing a
// private address the client itself was configured to reach.
func Get(ctx context.Context, c *http.Client, rawURL string, allowPrivate bool) (*http.Response, error) {
	check := CheckURL
	if allowPrivate {
		check = CheckURLPermissive
	}
	if err := check(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// CheckScheme validates only the scheme, for the case where an operator has
// deliberately allowed private addresses (a self-hosted feed on the same LAN).
//
// The address rules are theirs to relax; the scheme allowlist is not. file://,
// data: and gopher:// are never feeds under any configuration, and treating them
// as merely "private" would turn an opt-in convenience into an arbitrary-file-
// read primitive.
func CheckScheme(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("netguard: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: got %q", ErrScheme, u.Scheme)
	}
	if u.Hostname() == "" {
		return ErrNoHost
	}
	return nil
}

// PermissiveDialer allows private addresses but still refuses the ranges in
// neverAllowed. Same socket-level enforcement as Dialer, narrower deny list.
func PermissiveDialer(timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			switch network {
			case "tcp", "tcp4", "tcp6":
			default:
				return fmt.Errorf("%w: network %q", ErrBlockedIP, network)
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("netguard: %w", err)
			}
			if IsNeverAllowed(net.ParseIP(host)) {
				return fmt.Errorf("%w: %s", ErrBlockedIP, host)
			}
			return nil
		},
	}
}

// CheckURLPermissive validates a URL under the permissive policy.
func CheckURLPermissive(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("netguard: %w", err)
	}
	return CheckParsedURLPermissive(u)
}

// CheckParsedURLPermissive is CheckParsedURL with the private ranges allowed and
// neverAllowed still enforced.
func CheckParsedURLPermissive(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: got %q", ErrScheme, u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return ErrNoHost
	}
	if ip := net.ParseIP(host); ip != nil && IsNeverAllowed(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedIP, ip)
	}
	return nil
}
