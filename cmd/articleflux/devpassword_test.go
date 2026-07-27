package main

import (
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/pwpolicy"
)

// The constraint the two halves of this program have to agree on.
//
// `devPassword` is documented in `.env.example` and prefilled on the login
// screen, and it is fed to `init` — which enforces §7.1's policy. When those
// disagree, following the documentation produces an error and then a server
// that will not start because no account was ever created. That has happened
// once already, on length alone: the value was "articleflux", eleven
// characters, against a twelve-character minimum.
//
// The policy is now more than a length rule, so the same disagreement can
// arrive through a new door — a dev password on the common-password list, or
// one containing the dev username. This is the test that closes the door rather
// than the comment that asks people to remember.
func TestTheDevPasswordSatisfiesThePolicyTheCLIEnforces(t *testing.T) {
	if err := pwpolicy.Check(devPassword, devUsername); err != nil {
		t.Errorf("devPassword %q is refused by the policy `init` enforces: %v\n"+
			"Following .env.example would fail, and then the server would not "+
			"start because no account exists.", devPassword, err)
	}
}

// And the length constant this file exposes must not drift from the policy's,
// or the help text promises a floor the check does not apply.
func TestTheDocumentedMinimumMatchesThePolicy(t *testing.T) {
	if minPasswordLen != pwpolicy.MinLength {
		t.Errorf("this command advertises a %d-character minimum and the policy "+
			"enforces %d", minPasswordLen, pwpolicy.MinLength)
	}
}
