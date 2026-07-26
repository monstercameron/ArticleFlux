// Package netguard is the SSRF guard. Seven callers depend on it (§21): feed
// polling, favicon fetch, article extraction, OPML import by URL, WebSub
// verification, image proxying, and feed discovery.
//
// The threat is specific. Tidings fetches URLs that users supply, from a server
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
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
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
		"64:ff9b::/96",       // NAT64: reaches IPv4 space through an IPv6 address
		"2001:db8::/32",      // documentation
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

// IsNeverAllowed reports whether an address is blocked under every policy.
func IsNeverAllowed(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range neverAllowed {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// IsBlockedIP reports whether an address must not be connected to.
//
// IPv4-mapped IPv6 addresses (::ffff:127.0.0.1) are unwrapped first. Without
// that, a mapped loopback misses every IPv4 rule and matches no IPv6 rule — the
// single most common way an allow/deny list like this gets bypassed.
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true // an address we cannot understand is one we do not dial
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, n := range blocked {
		if n.Contains(ip) {
			return true
		}
	}
	return false
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
		Transport: &uaTransport{next: tr, ua: opt.UserAgent},
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
	next http.RoundTripper
	ua   string
}

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.ua != "" && r.Header.Get("User-Agent") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", t.ua)
	}
	return t.next.RoundTrip(r)
}

// Get is the convenience path: validate, then fetch with a guarded client.
func Get(ctx context.Context, c *http.Client, rawURL string) (*http.Response, error) {
	if err := CheckURL(rawURL); err != nil {
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
