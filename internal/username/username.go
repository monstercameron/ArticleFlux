// Package username decides what an account may be called.
//
// # The rule, and why it is forward-only
//
// A username must be an email address. Not because the server sends mail — it
// does not — but because the address is the only handle that survives losing
// the password: recovery, an operator-minted reset link, and any future "we
// noticed a sign-in from somewhere new" all need somewhere to arrive, and an
// account called `cam` gives them nowhere.
//
// It applies to names set FROM NOW ON, never retroactively. Every instance
// already running has an owner whose username predates the rule, and a check
// applied at login would lock exactly those people out of their own servers —
// a rule that fires on the account it was written to protect. So the enforcement
// points are the three places a name is CHOSEN (setup, an admin adding a user,
// and a rename), and nowhere a name is merely presented.
//
// # Why not the `email` column
//
// `users.email` already exists and is nullable, which is the shape of a field
// nothing depends on. Making the username the address means there is one string
// to keep true rather than two that can disagree — and the failure of two is
// silent: an account recovers to an address the owner stopped reading a year
// ago, while the name they type every day is the one they would have corrected.
package username

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode"
)

// MaxLength bounds what is stored and displayed.
//
// 254 is the maximum length of an email address that can actually be delivered
// (RFC 5321's path limit), so anything longer is not a strict rule being applied
// to a valid address — it is an address that was never going to work.
const MaxLength = 254

var (
	ErrEmpty   = errors.New("a username is required")
	ErrTooLong = fmt.Errorf("a username can be at most %d characters", MaxLength)
	ErrNotMail = errors.New("a username must be an email address")
	// ErrDisplayName is `Cam <cam@example.com>`, which mail.ParseAddress accepts
	// and which must not become a username: the stored string would contain a
	// space and a name, the login lookup would be against the whole of it, and
	// the reader would have to type the decoration back exactly to sign in.
	ErrDisplayName = errors.New("a username must be just the address, with no name in front of it")
)

// Normalise is what gets stored: trimmed, with the DOMAIN lowercased.
//
// The local part is left exactly as typed, and that asymmetry is the standard
// rather than an oversight. Domains are case-insensitive by definition (RFC
// 1035); local parts are case-SENSITIVE by definition (RFC 5321 §2.4), and
// while almost every real provider ignores that, "almost every" is not a
// property to normalise somebody's identity on. Uniqueness does not depend on
// this — the index is on `lower(username)` — so the only thing lowercasing the
// local part would buy is quietly editing what somebody typed.
func Normalise(s string) string {
	s = strings.TrimSpace(s)
	at := strings.LastIndexByte(s, '@')
	if at < 0 {
		return s
	}
	return s[:at] + "@" + strings.ToLower(s[at+1:])
}

// Check validates a candidate username.
//
// Call it on the NORMALISED form, since that is what will be stored — checking
// one string and storing another is how a validator ends up guarding nothing.
func Check(s string) error {
	if strings.TrimSpace(s) == "" {
		return ErrEmpty
	}
	if len([]rune(s)) > MaxLength {
		return ErrTooLong
	}
	// Whitespace anywhere is refused before parsing, because ParseAddress is
	// happy to read `Cam <cam@example.com>` and a quoted local part may legally
	// contain a space. Neither belongs in something somebody types into a login
	// box every day.
	if strings.IndexFunc(s, unicode.IsSpace) >= 0 {
		return ErrDisplayName
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return ErrNotMail
	}
	// ParseAddress returns the address without decoration; if that differs from
	// what was handed in, the input carried something extra.
	if addr.Address != s {
		return ErrDisplayName
	}
	// One `@`, and something either side of it. ParseAddress is looser than the
	// intuition here — it accepts forms nothing will deliver to — and the point
	// of this rule is a reachable human, not conformance to the grammar.
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return ErrNotMail
	}
	// A domain with no dot is a local hostname (`cam@laptop`), which parses and
	// is not somewhere a stranger can reach.
	if !strings.Contains(s[at+1:], ".") {
		return ErrNotMail
	}
	return nil
}
