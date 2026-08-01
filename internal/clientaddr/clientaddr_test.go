package clientaddr

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/grpc", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestParseAcceptsEverySpellingAnAddressArrivesIn(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1.2.3.4", "1.2.3.4"},
		{"1.2.3.4:51234", "1.2.3.4"},
		{" 1.2.3.4 ", "1.2.3.4"},
		{"::1", "::1"},
		{"[::1]", "::1"},
		{"[::1]:51234", "::1"},
		{"2001:db8::1", "2001:db8::1"},
		{"[2001:db8::1]:443", "2001:db8::1"},
	} {
		got, ok := Parse(tc.in)
		if !ok {
			t.Errorf("Parse(%q) failed; want %s", tc.in, tc.want)
			continue
		}
		if got.String() != tc.want {
			t.Errorf("Parse(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// A mapped address is the same host as the bare one, and must key the same.
// Two keys for one host means an attacker who alternates spellings gets two
// budgets, which is the outbound bug of §netguard arriving on the inbound side.
func TestMappedAndBareAddressesShareOneKey(t *testing.T) {
	if a, b := Key("::ffff:1.2.3.4"), Key("1.2.3.4"); a != b {
		t.Errorf("mapped %q and bare %q are different keys; one host must be one bucket", a, b)
	}
	if a, b := Key("[::ffff:1.2.3.4]:9999"), Key("1.2.3.4:1111"); a != b {
		t.Errorf("mapped-with-port %q and bare-with-port %q are different keys", a, b)
	}
}

// The port must not reach the key. Keyed on the connection instead of the
// client, every new socket would get a fresh budget.
func TestKeyDropsThePort(t *testing.T) {
	if got := Key("1.2.3.4:51234"); got != "1.2.3.4" {
		t.Errorf("Key = %q, want 1.2.3.4", got)
	}
	if got := Key("[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Errorf("Key = %q, want 2001:db8::1", got)
	}
}

// The regression that motivates Key existing at all: stripping at the last
// colon turns a bare IPv6 address into a truncated prefix, and every client on
// that /64 collapses into it.
func TestKeyDoesNotTruncateABareIPv6Address(t *testing.T) {
	if got := Key("2001:db8::1"); got != "2001:db8::1" {
		t.Errorf("Key = %q, want the address intact — a last-colon strip gives 2001:db8:", got)
	}
}

func TestForwardedTakesTheLeftmostEntry(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "203.0.113.7, 70.41.3.18, 150.172.238.178",
	})
	a, ok := Forwarded(r)
	if !ok || a.String() != "203.0.113.7" {
		t.Fatalf("Forwarded = %v/%v, want 203.0.113.7 — the last entry is a proxy", a, ok)
	}
}

func TestForwardedFallsBackToXRealIP(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{"X-Real-IP": "203.0.113.9"})
	a, ok := Forwarded(r)
	if !ok || a.String() != "203.0.113.9" {
		t.Fatalf("Forwarded = %v/%v, want 203.0.113.9", a, ok)
	}
}

// A garbage leftmost entry does not promote the second. The second is a proxy,
// and naming a proxy as the client is the whole bug.
func TestGarbageLeftmostDoesNotPromoteTheNextHop(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "not-an-address, 70.41.3.18",
	})
	if a, ok := Forwarded(r); ok {
		t.Fatalf("Forwarded = %v, want no answer rather than the next hop", a)
	}
}

// The header is only believed on the operator's say-so. Without -behind-proxy
// anybody could write their own address and walk through the limiter.
func TestHeadersAreIgnoredUnlessTheOperatorTrustsTheProxy(t *testing.T) {
	r := req("198.51.100.5:44444", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if got := Of(r, false); got != "198.51.100.5" {
		t.Errorf("Of(untrusted) = %q, want the transport address 198.51.100.5", got)
	}
	if got := Of(r, true); got != "203.0.113.7" {
		t.Errorf("Of(trusted) = %q, want the forwarded address 203.0.113.7", got)
	}
}

// Trusting the proxy is not the same as requiring it to speak. A request that
// arrives with no header at all still has a usable transport address.
func TestTrustedButHeaderlessFallsBackToTheTransportAddress(t *testing.T) {
	r := req("127.0.0.1:9000", nil)
	if got := Of(r, true); got != "127.0.0.1" {
		t.Errorf("Of = %q, want 127.0.0.1", got)
	}
}

func TestOfNeverReturnsEmptyForAnUnparseableRemoteAddr(t *testing.T) {
	r := req("pipe", nil)
	if got := Of(r, true); got != "pipe" {
		t.Errorf("Of = %q, want the raw value rather than a blank", got)
	}
}

// A nil request has no headers and no RemoteAddr to fall back to; both
// entry points must report "nothing usable" rather than dereference nil.
func TestNilRequestIsHandledNotDereferenced(t *testing.T) {
	if a, ok := Forwarded(nil); ok {
		t.Errorf("Forwarded(nil) = %v, want no answer", a)
	}
	if got := Of(nil, true); got != "" {
		t.Errorf("Of(nil, true) = %q, want empty", got)
	}
}

func TestParseRejectsEmptyAndGarbage(t *testing.T) {
	for _, s := range []string{"", "   ", "not-an-address", "999.999.999.999"} {
		if a, ok := Parse(s); ok {
			t.Errorf("Parse(%q) = %v, want no answer", s, a)
		}
	}
}

// Key has to produce a bucket for callers that never validated the address —
// an empty limiter key is worse than a labelled placeholder, and a garbage
// address should still bucket by its literal text rather than vanish.
func TestKeyFallsBackWhenNothingParses(t *testing.T) {
	if got := Key(""); got != "unknown" {
		t.Errorf(`Key("") = %q, want "unknown"`, got)
	}
	if got := Key("   "); got != "unknown" {
		t.Errorf(`Key("   ") = %q, want "unknown"`, got)
	}
	if got := Key("not-an-address"); got != "not-an-address" {
		t.Errorf("Key(garbage) = %q, want the literal text back", got)
	}
}
