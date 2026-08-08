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

// THE regression, and the reason the rule is counted from the right.
//
// nginx forwards with `$proxy_add_x_forwarded_for`, which appends the peer it
// saw to whatever the caller sent. So a client who writes their own
// X-Forwarded-For gets it PREPENDED to the truth, and a leftmost read returns
// the value they chose — a fresh limiter bucket per attempt, a spoofed `client`
// in the audit trail, and a per-address lockout that never fires.
func TestForwardedIgnoresAnEntryTheClientSupplied(t *testing.T) {
	// What arrives at the app when a caller at 198.51.100.5 sends
	// "X-Forwarded-For: 1.2.3.4" through one nginx.
	r := req("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "1.2.3.4, 198.51.100.5",
	})
	a, ok := Forwarded(r, 1)
	if !ok || a.String() != "198.51.100.5" {
		t.Fatalf("Forwarded = %v/%v, want 198.51.100.5 — 1.2.3.4 is whatever the caller typed", a, ok)
	}
}

// One trusted proxy means the LAST entry, which is the address nginx saw.
func TestForwardedTakesTheEntryTheTrustedHopAppended(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "203.0.113.7, 70.41.3.18, 150.172.238.178",
	})
	a, ok := Forwarded(r, 1)
	if !ok || a.String() != "150.172.238.178" {
		t.Fatalf("Forwarded = %v/%v, want 150.172.238.178 — everything left of it is unverified", a, ok)
	}
}

// Two hops — a CDN in front of nginx — moves the trusted position one place
// left, because the last entry is then the CDN's edge rather than a reader.
func TestASecondTrustedHopMovesThePositionLeft(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "203.0.113.7, 70.41.3.18, 150.172.238.178",
	})
	a, ok := Forwarded(r, 2)
	if !ok || a.String() != "70.41.3.18" {
		t.Fatalf("Forwarded(hops=2) = %v/%v, want 70.41.3.18", a, ok)
	}
}

// A chain shorter than the asserted path did not come through it. Nothing in it
// was written by a trusted hop, so nothing in it is believed.
func TestAChainShorterThanTheHopCountIsNotBelieved(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if a, ok := Forwarded(r, 3); ok {
		t.Fatalf("Forwarded = %v, want no answer for a chain that skipped the declared path", a)
	}
}

// Zero and negative read as one. Zero hops means nothing is forwarding, and a
// caller in that state should not be consulting headers at all — reading it as
// "index past the end of the list" would be an off-by-one with a CVE attached.
func TestAHopCountBelowOneIsReadAsOne(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.5"})
	for _, hops := range []int{0, -1} {
		a, ok := Forwarded(r, hops)
		if !ok || a.String() != "198.51.100.5" {
			t.Errorf("Forwarded(hops=%d) = %v/%v, want 198.51.100.5", hops, a, ok)
		}
	}
}

// X-Real-IP is SET rather than appended, so under one nginx it needs no
// counting and is authoritative on its own.
func TestForwardedFallsBackToXRealIP(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{"X-Real-IP": "203.0.113.9"})
	a, ok := Forwarded(r, 1)
	if !ok || a.String() != "203.0.113.9" {
		t.Fatalf("Forwarded = %v/%v, want 203.0.113.9", a, ok)
	}
}

// With two hops it answers a different question — what the OUTER proxy saw,
// which is the CDN edge — so it is not consulted.
func TestXRealIPIsNotConsultedBeyondOneHop(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{"X-Real-IP": "203.0.113.9"})
	if a, ok := Forwarded(r, 2); ok {
		t.Fatalf("Forwarded(hops=2) = %v, want no answer: X-Real-IP is the outer proxy's peer", a)
	}
}

// A garbage entry at the trusted position does not slide to its neighbour. The
// neighbour is either a proxy or attacker-supplied, and "one bad entry unlocks
// the next" is a rule an attacker can satisfy deliberately.
func TestGarbageAtTheTrustedPositionDoesNotSlide(t *testing.T) {
	r := req("127.0.0.1:9000", map[string]string{
		"X-Forwarded-For": "70.41.3.18, not-an-address",
	})
	if a, ok := Forwarded(r, 1); ok {
		t.Fatalf("Forwarded = %v, want no answer rather than the entry beside it", a)
	}
}

// The header is only believed on the operator's say-so. Without -behind-proxy
// anybody could write their own address and walk through the limiter.
func TestHeadersAreIgnoredUnlessTheOperatorTrustsTheProxy(t *testing.T) {
	r := req("198.51.100.5:44444", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if got := Of(r, false, 1); got != "198.51.100.5" {
		t.Errorf("Of(untrusted) = %q, want the transport address 198.51.100.5", got)
	}
	if got := Of(r, true, 1); got != "203.0.113.7" {
		t.Errorf("Of(trusted) = %q, want the forwarded address 203.0.113.7", got)
	}
}

// Trusting the proxy is not the same as requiring it to speak. A request that
// arrives with no header at all still has a usable transport address.
func TestTrustedButHeaderlessFallsBackToTheTransportAddress(t *testing.T) {
	r := req("127.0.0.1:9000", nil)
	if got := Of(r, true, 1); got != "127.0.0.1" {
		t.Errorf("Of = %q, want 127.0.0.1", got)
	}
}

func TestOfNeverReturnsEmptyForAnUnparseableRemoteAddr(t *testing.T) {
	r := req("pipe", nil)
	if got := Of(r, true, 1); got != "pipe" {
		t.Errorf("Of = %q, want the raw value rather than a blank", got)
	}
}

// A nil request has no headers and no RemoteAddr to fall back to; both
// entry points must report "nothing usable" rather than dereference nil.
func TestNilRequestIsHandledNotDereferenced(t *testing.T) {
	if a, ok := Forwarded(nil, 1); ok {
		t.Errorf("Forwarded(nil) = %v, want no answer", a)
	}
	if got := Of(nil, true, 1); got != "" {
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
