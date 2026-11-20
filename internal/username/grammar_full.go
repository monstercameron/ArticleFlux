//go:build !js

package username

import "net/mail"

// grammar asks the standard library whether s is an address and nothing else.
//
// This is the authority. Every place a name is CHOSEN goes through the server
// (setup, an admin adding a user, a rename), and the server is built with this
// file.
func grammar(s string) error {
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return ErrNotMail
	}
	// ParseAddress returns the address without decoration; if that differs from
	// what was handed in, the input carried something extra.
	if addr.Address != s {
		return ErrDisplayName
	}
	return nil
}
