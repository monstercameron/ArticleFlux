package netguard

import (
	"net"
	"net/url"
	"testing"
)

// neverAllowed exists so an operator who opts into private addresses still
// cannot reach the ranges that are dangerous under every policy (see the
// package comment on neverAllowed). That only holds if neverAllowed is a
// SUBSET of the default-strict blocked set — every CIDR listed in neverAllowed
// also appears in blocked. If a future edit added a range to neverAllowed
// without also blocking it by default, -allow-private would still refuse it
// (correct) but the STRICT policy would let it through (backwards), which is
// silent until someone audits the two lists side by side. This is that audit,
// as a test.
func TestNeverAllowedIsASubsetOfBlocked(t *testing.T) {
	probes := []string{
		"169.254.169.254", "169.254.1.1", "0.0.0.0", "224.0.0.1", "240.0.0.1",
		"255.255.255.255", "fe80::1", "ff02::1", "::",
		"::ffff:169.254.169.254", "64:ff9b::a9fe:a9fe",
	}
	for _, s := range probes {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("fixture %q does not parse", s)
		}
		if IsNeverAllowed(ip) && !IsBlockedIP(ip) {
			t.Errorf("%s is in neverAllowed but not in the strict blocked set — "+
				"the strict policy is now LESS restrictive than the permissive one", s)
		}
	}
}

// FuzzIsBlockedIPNeverAllowedImpliesBlocked is the same invariant, generalised:
// for ANY address IsNeverAllowed classifies as unreachable, IsBlockedIP must
// agree, because neverAllowed is documented as the ranges that "stay blocked
// even when an operator has opted into private addresses" — a strict policy
// that is looser than the permissive one on any single address defeats the
// point of having two policies.
func FuzzIsBlockedIPNeverAllowedImpliesBlocked(f *testing.F) {
	for _, s := range []string{
		"127.0.0.1", "10.1.2.3", "169.254.169.254", "192.168.1.1", "8.8.8.8",
		"::1", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::7f00:1", "2001:db8::1", "not-an-ip", "",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ip := net.ParseIP(s)
		if ip == nil {
			return // IsBlockedIP(nil) and IsNeverAllowed(nil) are both true; nothing to compare
		}
		if IsNeverAllowed(ip) && !IsBlockedIP(ip) {
			t.Fatalf("%s: IsNeverAllowed=true but IsBlockedIP=false", s)
		}
	})
}

// FuzzCheckURLNeverPanics feeds arbitrary strings through both policies' URL
// checks. Nothing here should panic regardless of how malformed the input is —
// this runs on every user-supplied feed URL, favicon link and OPML entry.
func FuzzCheckURLNeverPanics(f *testing.F) {
	seeds := []string{
		"http://127.0.0.1/", "https://[::1]/", "http://169.254.169.254/x",
		"ftp://x", "file:///etc/passwd", "http://", "https:///path",
		"http://[::ffff:127.0.0.1]/", "http://EXAMPLE.com:99999/",
		"http://user:pass@127.0.0.1/", "http://\x00/", "http://%zz/",
		"http://example.com/" + string(rune(0)),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CheckURL(%q) panicked: %v", raw, r)
			}
		}()
		_ = CheckURL(raw)
		_ = CheckURLPermissive(raw)
		_ = CheckScheme(raw)

		// If a literal blocked IP host slipped through CheckURL as allowed, that
		// is the SSRF hole this package exists to close.
		if err := CheckURL(raw); err == nil {
			if u, perr := url.Parse(raw); perr == nil {
				if ip := net.ParseIP(u.Hostname()); ip != nil && IsBlockedIP(ip) {
					t.Fatalf("CheckURL(%q) allowed a blocked literal address %s", raw, ip)
				}
			}
		}
	})
}
