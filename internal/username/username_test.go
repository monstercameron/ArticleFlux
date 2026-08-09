package username

import "testing"

// The rule is "a username is an email address", and the tests worth having are
// the ones about what that does NOT mean — every refusal here is a string that
// looks close enough to an address that a looser check would take it.

func TestAcceptsOrdinaryAddresses(t *testing.T) {
	for _, s := range []string{
		"cam@example.com",
		"cam.reads@example.co.uk",
		"cam+articleflux@example.com",
		"c@a.io",
		// A plus, dots and digits are all ordinary in a local part, and a rule
		// that refused them would refuse the addresses people actually use to
		// keep mail sorted.
		"cam.reads+news2026@mail.example.com",
	} {
		if err := Check(Normalise(s)); err != nil {
			t.Errorf("Check(%q) = %v, want accepted", s, err)
		}
	}
}

func TestRefusesWhatIsNotAnAddress(t *testing.T) {
	for _, c := range []struct{ in, why string }{
		{"", "empty"},
		{"   ", "only whitespace"},
		{"cam", "no address at all — the case the rule exists for"},
		{"cam@", "nothing to deliver to"},
		{"@example.com", "nobody to deliver to"},
		{"cam@laptop", "parses, but a local hostname is not reachable"},
		{"Cam <cam@example.com>", "a display name would have to be typed back exactly"},
		{"cam @example.com", "whitespace inside"},
		{"cam@exa mple.com", "whitespace inside the domain"},
		{"cam@@example.com", "two separators"},
	} {
		if err := Check(Normalise(c.in)); err == nil {
			t.Errorf("Check(%q) accepted; %s", c.in, c.why)
		}
	}
}

// TestNormaliseLowercasesOnlyTheDomain.
//
// Domains are case-insensitive by definition; local parts are case-SENSITIVE by
// definition, and while almost every provider ignores that, "almost every" is
// not a property to quietly edit somebody's identity on. Uniqueness does not
// depend on it either — the index is on `lower(username)`.
func TestNormaliseLowercasesOnlyTheDomain(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Cam.Reads@Example.COM", "Cam.Reads@example.com"},
		{"  cam@EXAMPLE.com  ", "cam@example.com"},
		{"CAM@example.com", "CAM@example.com"},
		// No separator: nothing to split on, so it comes back trimmed and
		// otherwise untouched — Check is what refuses it, not this.
		{"  cam  ", "cam"},
	} {
		if got := Normalise(c.in); got != c.want {
			t.Errorf("Normalise(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRefusesSomethingLongerThanCanBeDelivered(t *testing.T) {
	long := "c"
	for len(long) < MaxLength {
		long += "c"
	}
	if err := Check(long + "@example.com"); err == nil {
		t.Error("accepted a username past the deliverable length")
	}
}
