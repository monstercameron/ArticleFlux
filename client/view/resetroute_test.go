//go:build js && wasm

package view

import "testing"

// The grammar of the link `articleflux reset -origin` prints.
//
// The CLI has printed `<origin>/reset?token=…` since §7.2 and this client had no
// route for it: the link landed on the plain sign-in card with no recovery mode
// and no prefill, so the only way through was to read the token out of the URL
// bar and paste it — the exact step the link exists to remove, asked of somebody
// who is locked out.
//
// Pure, and therefore testable without a browser, which is the same split
// route.go is built on. `isHomePath` is the sibling this mirrors, including the
// base-relative handling: an instance mounted under /reader/ answers at
// /reader/reset, and hard-coding "/reset" would be correct on exactly one
// deployment shape.
func TestResetTokenFrom(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		path  string
		query string
		want  string
	}{
		{"the link as printed", "/", "/reset", "token=abc123", "abc123"},
		{"trailing slash", "/", "/reset/", "token=abc123", "abc123"},
		{"mounted under a subpath", "/reader/", "/reader/reset", "token=abc123", "abc123"},
		{"another parameter first", "/", "/reset", "utm=x&token=abc123", "abc123"},
		{"another parameter after", "/", "/reset", "token=abc123&utm=x", "abc123"},

		// Everything below must NOT be read as a reset, because returning a
		// token here sends the reader to the recovery screen instead of the app.
		{"the reader itself", "/", "/", "", ""},
		{"a feed", "/", "/feed/123", "", ""},
		{"the front door", "/", "/home", "", ""},
		{"the route with no token", "/", "/reset", "", ""},
		{"a token on some other route", "/", "/settings", "token=abc123", ""},
		{"a parameter that merely ends in token", "/", "/reset", "csrftoken=abc123", ""},
		{"the subpath route on a root mount", "/", "/reader/reset", "token=abc123", ""},
		{"an empty base behaves as root", "", "/reset", "token=abc123", "abc123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resetTokenFrom(tc.base, tc.path, tc.query); got != tc.want {
				t.Errorf("resetTokenFrom(%q, %q, %q) = %q, want %q",
					tc.base, tc.path, tc.query, got, tc.want)
			}
		})
	}
}
