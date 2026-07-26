package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TODO 2.7's bar: every reject case has a test, including redirect-to-localhost.
func TestBlockedRanges(t *testing.T) {
	blockedAddrs := []struct{ ip, why string }{
		{"127.0.0.1", "loopback — the admin interface bound to localhost"},
		{"127.1.2.3", "all of 127/8 is loopback, not just .0.1"},
		{"0.0.0.0", "this network"},
		{"10.1.2.3", "RFC1918"},
		{"172.16.0.1", "RFC1918"},
		{"172.31.255.254", "RFC1918 upper bound"},
		{"192.168.1.1", "RFC1918 — the router admin page"},
		{"169.254.169.254", "the cloud metadata endpoint; the whole reason this exists"},
		{"169.254.1.1", "link-local generally"},
		{"100.64.0.1", "CGNAT — a real host on many home connections"},
		{"192.0.2.1", "TEST-NET-1"},
		{"198.51.100.1", "TEST-NET-2"},
		{"203.0.113.1", "TEST-NET-3"},
		{"198.18.0.1", "benchmarking"},
		{"224.0.0.1", "multicast"},
		{"240.0.0.1", "reserved"},
		{"255.255.255.255", "broadcast"},
		{"::1", "IPv6 loopback"},
		{"::", "IPv6 unspecified"},
		{"fc00::1", "IPv6 unique local"},
		{"fd12:3456::1", "IPv6 unique local"},
		{"fe80::1", "IPv6 link-local"},
		{"ff02::1", "IPv6 multicast"},
		{"64:ff9b::7f00:1", "NAT64 reaches IPv4 space through IPv6"},
		{"::ffff:127.0.0.1", "IPv4-mapped loopback — the classic bypass"},
		{"::ffff:169.254.169.254", "IPv4-mapped metadata endpoint"},
		{"::ffff:10.0.0.1", "IPv4-mapped RFC1918"},
		{"2001:db8::1", "documentation range"},
	}
	for _, c := range blockedAddrs {
		t.Run(c.ip, func(t *testing.T) {
			if !IsBlockedIP(net.ParseIP(c.ip)) {
				t.Errorf("%s must be blocked (%s)", c.ip, c.why)
			}
		})
	}
	// A nil address is one we could not understand, and we do not dial those.
	if !IsBlockedIP(nil) {
		t.Error("an unparseable address must be treated as blocked")
	}
}

func TestPublicAddressesAreAllowed(t *testing.T) {
	for _, ip := range []string{
		"1.1.1.1", "8.8.8.8", "93.184.216.34", "172.32.0.1", "100.63.255.255",
		"2606:4700:4700::1111", "2a00:1450:4001:800::200e",
	} {
		if IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s is publicly routable and must be allowed", ip)
		}
	}
}

func TestCheckURLScheme(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"gopher://example.com/",
		"ftp://example.com/x",
		"dict://example.com:11211/",
		"jar:http://example.com!/",
		"data:text/plain,hi",
	} {
		if err := CheckURL(raw); !errors.Is(err, ErrScheme) {
			t.Errorf("CheckURL(%q) = %v, want ErrScheme", raw, err)
		}
	}
	for _, raw := range []string{"http://example.com/a", "https://example.com/a"} {
		if err := CheckURL(raw); err != nil {
			t.Errorf("CheckURL(%q) = %v, want nil", raw, err)
		}
	}
}

func TestCheckURLRejectsLiteralBlockedHosts(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1/admin",
		"http://127.0.0.1:8080/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:9000/",
		"http://[::ffff:127.0.0.1]/",
		"http://192.168.0.1/",
		"http://10.0.0.1/",
	} {
		if err := CheckURL(raw); !errors.Is(err, ErrBlockedIP) {
			t.Errorf("CheckURL(%q) = %v, want ErrBlockedIP", raw, err)
		}
	}
}

func TestCheckURLRejectsHostlessURLs(t *testing.T) {
	for _, raw := range []string{"http://", "https:///path"} {
		if err := CheckURL(raw); !errors.Is(err, ErrNoHost) {
			t.Errorf("CheckURL(%q) = %v, want ErrNoHost", raw, err)
		}
	}
}

// The dialer is the layer that actually holds, because it sees the resolved
// address. A hostname pointing at loopback must be refused with no packet sent —
// this is the DNS-rebinding case that URL checking cannot catch.
func TestDialerRejectsResolvedLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the request reached the server; the dialer should have refused it")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := Client(Options{Timeout: 5 * time.Second})
	// srv.URL is http://127.0.0.1:PORT. Bypass CheckURL deliberately and go
	// straight to the client, so this exercises Control rather than the URL check.
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, err := c.Do(req)
	if err == nil {
		t.Fatal("expected the dial to be refused")
	}
	if !strings.Contains(err.Error(), "not publicly routable") {
		t.Errorf("err = %v, want a netguard refusal", err)
	}
}

// The redirect case: a public-looking URL that 302s to the metadata endpoint.
// Validating only the first URL is the most common way a guard like this fails.
func TestRedirectToLocalhostIsRefused(t *testing.T) {
	var target string
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer redirector.Close()
	target = "http://169.254.169.254/latest/meta-data/"

	c := Client(Options{Timeout: 5 * time.Second})
	// Dial the redirector directly by disabling Control for the first hop would
	// be artificial; instead assert the redirect hook itself.
	req, _ := http.NewRequest("GET", "http://example.invalid/", nil)
	via := []*http.Request{req}
	next, _ := http.NewRequest("GET", target, nil)
	if err := c.CheckRedirect(next, via); !errors.Is(err, ErrBlockedIP) {
		t.Errorf("redirect hook = %v, want ErrBlockedIP", err)
	}
}

func TestRedirectChainIsBounded(t *testing.T) {
	c := Client(Options{})
	next, _ := http.NewRequest("GET", "https://example.com/", nil)
	via := make([]*http.Request, MaxRedirects)
	for i := range via {
		via[i] = next
	}
	if err := c.CheckRedirect(next, via); !errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("got %v, want ErrTooManyRedirects", err)
	}
}

// A proxy configured in the environment would route around Control entirely,
// because the address dialed becomes the proxy's rather than the target's.
func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	c := Client(Options{})
	tr, ok := c.Transport.(*uaTransport).next.(*http.Transport)
	if !ok {
		t.Fatal("unexpected transport shape")
	}
	if tr.Proxy != nil {
		t.Error("a proxy would bypass the dialer guard and must stay disabled")
	}
}

func TestGetValidatesBeforeDialing(t *testing.T) {
	c := Client(Options{Timeout: 2 * time.Second})
	_, err := Get(context.Background(), c, "http://169.254.169.254/latest/meta-data/")
	if !errors.Is(err, ErrBlockedIP) {
		t.Errorf("Get = %v, want ErrBlockedIP", err)
	}
}

func TestUserAgentIsSet(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.UserAgent()
	}))
	defer srv.Close()

	// Reach the test server through a client with the guard's transport but a
	// permissive dialer, since the whole point here is the header, not the guard.
	c := &http.Client{Transport: &uaTransport{next: http.DefaultTransport, ua: "Tidings/0.1"}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got != "Tidings/0.1" {
		t.Errorf("User-Agent = %q, want Tidings/0.1 — publishers can only respond to an anonymous fetcher by blocking it", got)
	}
}

// The permissive policy is a narrower deny list, not the absence of one. An
// operator asking for "feeds on my LAN" must not thereby hand out the cloud
// metadata endpoint — the exact attack this package exists to stop.
func TestPermissivePolicyStillBlocksTheDangerousRanges(t *testing.T) {
	stillBlocked := []struct{ ip, why string }{
		{"169.254.169.254", "the cloud metadata endpoint"},
		{"169.254.1.1", "link-local generally"},
		{"0.0.0.0", "this network"},
		{"224.0.0.1", "multicast"},
		{"240.0.0.1", "reserved"},
		{"fe80::1", "IPv6 link-local"},
		{"ff02::1", "IPv6 multicast"},
		{"::ffff:169.254.169.254", "IPv4-mapped metadata endpoint"},
	}
	for _, c := range stillBlocked {
		t.Run(c.ip, func(t *testing.T) {
			if !IsNeverAllowed(net.ParseIP(c.ip)) {
				t.Errorf("%s must stay blocked even in permissive mode (%s)", c.ip, c.why)
			}
		})
	}

	// And the ranges the policy exists to unlock.
	nowAllowed := []string{"127.0.0.1", "192.168.1.10", "10.0.0.5", "172.16.0.1", "100.64.0.1"}
	for _, ip := range nowAllowed {
		if IsNeverAllowed(net.ParseIP(ip)) {
			t.Errorf("%s should be reachable under the permissive policy", ip)
		}
		// ...and still blocked under the default one.
		if !IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s must stay blocked by default", ip)
		}
	}
}

func TestPermissiveURLCheck(t *testing.T) {
	if err := CheckURLPermissive("http://127.0.0.1:9011/feed.xml"); err != nil {
		t.Errorf("loopback should be allowed under the permissive policy: %v", err)
	}
	if err := CheckURLPermissive("http://169.254.169.254/latest/meta-data/"); !errors.Is(err, ErrBlockedIP) {
		t.Errorf("metadata endpoint = %v, want ErrBlockedIP", err)
	}
	// The scheme allowlist is policy-independent.
	if err := CheckURLPermissive("file:///etc/passwd"); !errors.Is(err, ErrScheme) {
		t.Errorf("file:// = %v, want ErrScheme", err)
	}
}
