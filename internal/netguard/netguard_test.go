package netguard

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	_, err := Get(context.Background(), c, "http://169.254.169.254/latest/meta-data/", false)
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
	c := &http.Client{Transport: &uaTransport{next: http.DefaultTransport, ua: "ArticleFlux/0.1"}}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got != "ArticleFlux/0.1" {
		t.Errorf("User-Agent = %q, want ArticleFlux/0.1 — publishers can only respond to an anonymous fetcher by blocking it", got)
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

// TestWrappedIPv4FormsAreUnwrapped covers the three IPv6 spellings of an IPv4
// address. Two of them used to slip both deny lists: Go's To4 only unwraps the
// ::ffff: form, so ::169.254.169.254 and 64:ff9b::a9fe:a9fe reached the cloud
// metadata endpoint — the first under EVERY policy, the second whenever
// -allow-private was set, which `-dev` sets automatically.
func TestWrappedIPv4FormsAreUnwrapped(t *testing.T) {
	metadata := []struct{ ip, form string }{
		{"::ffff:169.254.169.254", "IPv4-mapped"},
		{"::169.254.169.254", "IPv4-compatible (RFC 4291, deprecated)"},
		{"64:ff9b::a9fe:a9fe", "NAT64 well-known prefix (RFC 6052)"},
	}
	for _, c := range metadata {
		t.Run(c.ip, func(t *testing.T) {
			if !IsBlockedIP(net.ParseIP(c.ip)) {
				t.Errorf("%s (%s) must be blocked: it is the metadata endpoint", c.ip, c.form)
			}
			// The stronger claim, and the one SECURITY.md makes: the metadata
			// endpoint is unreachable under EVERY configuration, not merely the
			// strict one.
			if !IsNeverAllowed(net.ParseIP(c.ip)) {
				t.Errorf("%s (%s) must stay blocked even under -allow-private", c.ip, c.form)
			}
		})
	}

	// Wrapped loopback follows the SAME policy as bare loopback rather than a
	// blanket ban: refused by default, reachable when an operator opted in. A
	// per-wrapper CIDR could not express this, which is why unwrapping is the
	// mechanism.
	loopback := []string{"::ffff:127.0.0.1", "::127.0.0.1", "64:ff9b::7f00:1"}
	for _, ip := range loopback {
		if !IsBlockedIP(net.ParseIP(ip)) {
			t.Errorf("%s must be blocked by default, exactly like 127.0.0.1", ip)
		}
		if IsNeverAllowed(net.ParseIP(ip)) {
			t.Errorf("%s should be reachable under -allow-private, exactly like 127.0.0.1", ip)
		}
	}
}

// TestUnwrappingDoesNotSwallowRealAddresses is the other half of the test the
// ::ffff:0:0/96 comment asks for. An unwrap rule that is too eager is worse than
// the hole it closes: it takes the public internet offline and presents as "all
// my feeds stopped working".
func TestUnwrappingDoesNotSwallowRealAddresses(t *testing.T) {
	public := []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34",
		"2606:4700:4700::1111", // public IPv6
		"2001:4860:4860::8888",
		"64:ff9b::8080:8080", // NAT64 wrapping a PUBLIC address: 128.128.128.128
	}
	for _, ip := range public {
		if IsNeverAllowed(net.ParseIP(ip)) {
			t.Errorf("%s must not be caught by the never-allowed list", ip)
		}
	}
	// 64:ff9b:: wrapping a public address is still refused by DEFAULT, because
	// the prefix itself is a translator we did not choose — but it must not be
	// in neverAllowed, which the loop above asserts.
	if !IsBlockedIP(net.ParseIP("64:ff9b::8080:8080")) {
		t.Error("NAT64 prefix should stay blocked under the strict policy")
	}

	// The two reserved addresses RFC 4291 carves out of ::/96. Unwrapping ::1 to
	// 0.0.0.1 would land it in 0.0.0.0/8 — which is in neverAllowed — and break
	// -allow-private for IPv6 loopback, the most ordinary self-hosted case there
	// is.
	if IsNeverAllowed(net.ParseIP("::1")) {
		t.Error("::1 is IPv6 loopback and must follow the loopback policy, not the 0.0.0.0/8 one")
	}
	if !IsBlockedIP(net.ParseIP("::1")) {
		t.Error("::1 must still be blocked by default")
	}
	if !IsBlockedIP(net.ParseIP("::")) || !IsNeverAllowed(net.ParseIP("::")) {
		t.Error(":: is the unspecified address and must be blocked under every policy")
	}
}

// A nil address is one that could not be understood, and IsNeverAllowed makes the
// same fail-closed call IsBlockedIP does — a caller cannot tell "unparseable" from
// "reachable" by getting false back.
func TestIsNeverAllowedRejectsNil(t *testing.T) {
	if !IsNeverAllowed(nil) {
		t.Error("a nil address must be treated as blocked under the permissive policy too")
	}
}

// unwrapV4's length guard only matters for a net.IP that is neither 4 nor 16 bytes —
// To4 already handles every ordinary form net.ParseIP produces, so this is a
// defensive branch reachable only with a hand-built, malformed-length IP.
func TestUnwrapV4LeavesNonStandardLengthsAlone(t *testing.T) {
	odd := net.IP([]byte{1, 2, 3, 4, 5})
	if got := unwrapV4(odd); len(got) != len(odd) {
		t.Errorf("unwrapV4 mangled a non-standard-length address: %v", got)
	}
}

// Dialer's Control is exercised directly rather than only through a live dial —
// that is the only way to reach its own error branches (a non-TCP network, an
// address with no parseable port) without depending on OS-level dial failures.
func TestDialerControlBranches(t *testing.T) {
	d := Dialer(time.Second)

	t.Run("non-tcp network is refused outright", func(t *testing.T) {
		if err := d.Control("udp", "8.8.8.8:53", nil); !errors.Is(err, ErrBlockedIP) {
			t.Errorf("udp = %v, want ErrBlockedIP", err)
		}
	})
	t.Run("an address with no splittable port errors", func(t *testing.T) {
		if err := d.Control("tcp", "not-a-host-port", nil); err == nil {
			t.Error("expected a SplitHostPort error")
		}
	})
	t.Run("a blocked resolved address is refused", func(t *testing.T) {
		if err := d.Control("tcp", "127.0.0.1:80", nil); !errors.Is(err, ErrBlockedIP) {
			t.Errorf("= %v, want ErrBlockedIP", err)
		}
	})
	t.Run("a public resolved address is allowed", func(t *testing.T) {
		if err := d.Control("tcp4", "8.8.8.8:443", nil); err != nil {
			t.Errorf("= %v, want nil", err)
		}
	})
}

// PermissiveDialer's Control has the same three branches as Dialer's, but against
// the narrower neverAllowed list — untested directly before this, since every
// existing test reached it (if at all) only through IsNeverAllowed.
func TestPermissiveDialerControlBranches(t *testing.T) {
	d := PermissiveDialer(time.Second)

	t.Run("non-tcp network is refused outright", func(t *testing.T) {
		if err := d.Control("udp", "8.8.8.8:53", nil); !errors.Is(err, ErrBlockedIP) {
			t.Errorf("udp = %v, want ErrBlockedIP", err)
		}
	})
	t.Run("an address with no splittable port errors", func(t *testing.T) {
		if err := d.Control("tcp", "not-a-host-port", nil); err == nil {
			t.Error("expected a SplitHostPort error")
		}
	})
	t.Run("loopback is allowed under the permissive policy", func(t *testing.T) {
		if err := d.Control("tcp", "127.0.0.1:80", nil); err != nil {
			t.Errorf("= %v, want nil — loopback is exactly what -allow-private unlocks", err)
		}
	})
	t.Run("the metadata endpoint is still refused", func(t *testing.T) {
		if err := d.Control("tcp", "169.254.169.254:80", nil); !errors.Is(err, ErrBlockedIP) {
			t.Errorf("= %v, want ErrBlockedIP even under the permissive policy", err)
		}
	})
}

// Client(Options{AllowPrivate: true}) must actually swap in PermissiveDialer, not
// merely accept the option — the only way to see that is a live request that
// succeeds against loopback where the strict client (TestDialerRejectsResolvedLoopback)
// fails, plus the redirect hook taking the permissive branch too.
func TestClientAllowPrivateReachesLoopbackAndRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := Client(Options{Timeout: 5 * time.Second, AllowPrivate: true})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("a permissive client could not reach loopback: %v", err)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("GET", "http://example.invalid/", nil)
	via := []*http.Request{req}

	loopbackNext, _ := http.NewRequest("GET", srv.URL, nil)
	if err := c.CheckRedirect(loopbackNext, via); err != nil {
		t.Errorf("permissive redirect to loopback = %v, want nil", err)
	}
	metadataNext, _ := http.NewRequest("GET", "http://169.254.169.254/latest/meta-data/", nil)
	if err := c.CheckRedirect(metadataNext, via); !errors.Is(err, ErrBlockedIP) {
		t.Errorf("permissive redirect to the metadata endpoint = %v, want ErrBlockedIP", err)
	}
}

// The Observer hook fires on every request, per hop, whether or not one ever
// produced a response — a request refused at Control never reaches a status code,
// and the zero-status case is what lets a dashboard tell "blocked" apart from
// "the metric was never recorded".
func TestObserverReceivesEveryRequest(t *testing.T) {
	type call struct {
		purpose string
		status  int
		err     error
	}
	var calls []call
	orig := Observer
	Observer = func(purpose string, _ time.Duration, status int, err error) {
		calls = append(calls, call{purpose, status, err})
	}
	t.Cleanup(func() { Observer = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	permissive := Client(Options{Timeout: 5 * time.Second, AllowPrivate: true, Purpose: "ok"})
	resp, err := permissive.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	strict := Client(Options{Timeout: 2 * time.Second, Purpose: "blocked"})
	req, _ := http.NewRequest("GET", srv.URL, nil)
	_, _ = strict.Do(req) // expected to be refused by Control before any response

	if len(calls) != 2 {
		t.Fatalf("got %d observer calls, want 2: %+v", len(calls), calls)
	}
	if calls[0].purpose != "ok" || calls[0].status != 200 || calls[0].err != nil {
		t.Errorf("successful call = %+v, want purpose=ok status=200 err=nil", calls[0])
	}
	if calls[1].purpose != "blocked" || calls[1].status != 0 || calls[1].err == nil {
		t.Errorf("refused call = %+v, want purpose=blocked status=0 with a non-nil error", calls[1])
	}
}

// CheckURL only inspects a LITERAL IP host (its own doc comment: "A hostname is
// NOT resolved here"), so a hostname like "localhost" sails past Get's own
// pre-check and reaches the client — which is exactly where Control catches the
// resolved loopback address. This is the two-layer design working as documented,
// and it is what lets Get's request-construction and c.Do lines run at all.
func TestGetPassesNonLiteralHostsToTheClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	c := Client(Options{Timeout: 2 * time.Second})
	_, err = Get(context.Background(), c, "http://localhost:"+u.Port()+"/", false)
	if err == nil {
		t.Fatal("expected the dialer to refuse the resolved loopback address")
	}
	if !strings.Contains(err.Error(), "not publicly routable") {
		t.Errorf("err = %v, want a dial-layer refusal, not a CheckURL rejection", err)
	}
}

// BUG (netguard.go:408): Get always validates against the STRICT policy via
// CheckURL, regardless of the policy the caller's *http.Client was actually built
// with. A caller who built an AllowPrivate client — exactly what Client(Options{
// AllowPrivate: true}) is for — still has Get refuse a private address before the
// request ever reaches that client's permissive dialer. This is fail-closed (not an
// SSRF hole) but it is a real inconsistency: every other entry point in this
// package (the redirect hook in Client, CheckParsedURL vs CheckParsedURLPermissive)
// threads AllowPrivate through consistently; Get does not. Left FAILING per this
// task's instructions rather than silently patched.
func TestGetShouldRespectThePermissiveClientsPolicy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := Client(Options{Timeout: 5 * time.Second, AllowPrivate: true})
	_, err := Get(context.Background(), c, srv.URL, true) // srv.URL is a literal 127.0.0.1 address
	if err != nil {
		t.Errorf("Get refused a loopback URL despite an AllowPrivate client: %v", err)
	}
}
