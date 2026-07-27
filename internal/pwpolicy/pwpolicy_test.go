package pwpolicy

import (
	"errors"
	"strings"
	"testing"
)

// The folding is where the coverage comes from: a few hundred entries only
// works if each one covers the family of decorations people put on it.
func TestFoldingCollapsesTheDecorations(t *testing.T) {
	cases := []struct{ in, want string }{
		{"password", "password"},
		{"Password", "password"},
		{"PASSWORD", "password"},
		{"P@ssword", "password"},
		{"P@ssw0rd", "password"},
		{"pa$$word", "password"},
		{"Password1", "password"},
		{"Password123", "password"},
		{"P@ssw0rd123!", "password"},
		{"password!!!", "password"},
		{"  password  ", "password"},
		{"...password", "password"},
		{"l3tm3in", "letmein"},
		{"1L0V3Y0U", "iloveyou"},
	}
	for _, c := range cases {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The point of the whole package, stated as the passwords it must refuse.
func TestTheObviousOnesAreRefused(t *testing.T) {
	for _, p := range []string{
		"password1234", "Password123!", "P@ssw0rd1234", "pa$$word1234",
		"letmein12345", "Welcome12345", "qwertyuiop12", "administrator",
		"trustno112345", "iloveyou1234", "correcthorsebatterystaple",
		"articleflux1", "changeme1234", "SuperSecret1",
	} {
		if err := Check(p, "cam"); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
}

// And what it must NOT refuse, which matters just as much: a policy that
// rejects good passwords teaches people to write them down.
func TestReasonablePassphrasesAreAccepted(t *testing.T) {
	for _, p := range []string{
		"gravel-lantern-quixotic",
		"my dog ate the entire sandwich",
		"Th3 qu1ck br0wn f0x jump5",
		"xK7#mQ2vP9$wL4nR",
		"読書は楽しいことですね本当に",
		"ferrous seventeen candlestick",
	} {
		if err := Check(p, "cam"); err != nil {
			t.Errorf("%q was refused: %v", p, err)
		}
	}
}

// "cameron2026" is the single most guessable password for an account called
// cameron, and no generic list can contain it.
func TestAPasswordCannotBeTheUsername(t *testing.T) {
	for _, p := range []string{
		"cameron2026!", "CameronCameron", "xx-cameron-xx", "mycameronlogin",
	} {
		if err := Check(p, "cameron"); !errors.Is(err, ErrLikeName) {
			t.Errorf("Check(%q, cameron) = %v, want ErrLikeName", p, err)
		}
	}
	// A short username must not veto every password containing those letters.
	if err := Check("gravel-lantern-quixotic", "ca"); err != nil {
		t.Errorf("a two-letter username rejected an unrelated password: %v", err)
	}
}

// Length is counted in RUNES: bytes would tell somebody writing in Japanese
// that four characters is long enough, and somebody writing in Greek that
// twelve is too short.
func TestLengthIsCountedInCharactersNotBytes(t *testing.T) {
	// 12 runes, 36 bytes.
	twelve := "日本語日本語日本語日本語"
	if n := len([]rune(twelve)); n != 12 {
		t.Fatalf("the fixture is %d runes", n)
	}
	if err := Check(twelve, "cam"); err != nil {
		t.Errorf("a twelve-character passphrase was refused: %v", err)
	}
	// 11 runes must still be too short.
	if err := Check(string([]rune(twelve)[:11]), "cam"); !errors.Is(err, ErrTooShort) {
		t.Errorf("eleven characters = %v, want ErrTooShort", err)
	}
	// And bytes must not be mistaken for characters in the other direction.
	if err := Check("abcdefghijk", "cam"); !errors.Is(err, ErrTooShort) {
		t.Errorf("eleven ASCII characters = %v, want ErrTooShort", err)
	}
}

func TestLengthBounds(t *testing.T) {
	if err := Check(strings.Repeat("a", MaxLength+1), "cam"); !errors.Is(err, ErrTooLong) {
		t.Errorf("an over-long password = %v, want ErrTooLong", err)
	}
	if err := Check("            ", "cam"); !errors.Is(err, ErrWhitespce) {
		t.Errorf("twelve spaces = %v, want ErrWhitespce", err)
	}
}

// Long enough to pass the length rule and no better than the three characters
// it repeats.
func TestPaddingIsNotStrength(t *testing.T) {
	if err := Check("aaaaaaaaaaaaaaaa", "cam"); !errors.Is(err, ErrOneRune) {
		t.Errorf("one character sixteen times = %v, want ErrOneRune", err)
	}
	for _, p := range []string{"abcabcabcabc", "123412341234", "qwerqwerqwer"} {
		if err := Check(p, "cam"); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
}

func TestStraightKeyboardRunsAreRefused(t *testing.T) {
	for _, p := range []string{
		"abcdefghijkl", "qwertyuiopas", "0123456789012", "9876543210987",
	} {
		if err := Check(p, "cam"); err == nil {
			t.Errorf("%q was accepted", p)
		}
	}
}

// Entries shorter than MinLength still have to be in the list: folding removes
// the digits from "password1234" and what is left is the short entry.
func TestShortEntriesStillCatchLongDecorations(t *testing.T) {
	if !common["password"] {
		t.Fatal("the list does not contain the folded form of the most common password")
	}
	if err := Check("password1234", "cam"); !errors.Is(err, ErrCommon) {
		t.Errorf("= %v, want ErrCommon", err)
	}
}

// Every message is shown to whoever is choosing a password, so none of them may
// quote it back — a refusal that echoes the attempt puts it in a screenshot, a
// log, or a support ticket.
func TestNoMessageQuotesThePassword(t *testing.T) {
	const secret = "P@ssw0rd1234"
	err := Check(secret, "cam")
	if err == nil {
		t.Fatal("the fixture password was accepted")
	}
	if strings.Contains(err.Error(), secret) ||
		strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secret)) {
		t.Errorf("the refusal quotes the password: %q", err)
	}
	for _, e := range []error{ErrTooShort, ErrTooLong, ErrCommon, ErrLikeName,
		ErrOneRune, ErrSequence, ErrWhitespce} {
		if e.Error() == "" {
			t.Error("an error has no message")
		}
	}
}

// The list is only useful if it is actually consulted in folded form, so a
// malformed entry that folds to "" would silently match nothing — or, worse,
// match every password that folds to empty.
func TestNoEntryFoldsToNothing(t *testing.T) {
	if common[""] {
		t.Error("an entry folded to the empty string, which would match anything that folds to empty")
	}
	for _, w := range list {
		if Fold(w) == "" {
			t.Errorf("%q folds to nothing", w)
		}
	}
	if len(list) < 100 {
		t.Errorf("the list has %d entries; it was written with far more, so "+
			"something truncated it", len(list))
	}
}
