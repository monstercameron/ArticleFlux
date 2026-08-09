// Package pwpolicy rejects passwords that are already known (TODO 6.1,
// plan.md §7.1, §7.3).
//
// # Why this is not optional here
//
// §7.3's premise: with one factor on a public service, a phished or stuffed
// password is total account compromise. The lockout curve buys 4 guesses an
// hour against an ONLINE attacker — which is decisive against a wordlist and
// worth nothing against someone who guesses "password1" on the first try. The
// controls compose: lockout makes guessing slow, and this makes the first guess
// not work.
//
// # Bundled, not HIBP
//
// §7.1 offers two: a bundled list, or the Have I Been Pwned k-anonymity range
// API — free, no key, and it sends only a 5-character SHA-1 prefix. The prefix
// really is anonymous in the sense the name claims.
//
// This is a self-hosted reader whose entire premise is that your reading does
// not leave the box, running behind an egress allowlist (§18.8) that exists so
// that what leaves is a decision rather than an accident. Making a request to a
// third party at the moment somebody types a password — even a request that
// discloses nothing — is the wrong default for that application, and an
// operator who wants it can be given the option later.
//
// The cost is honest and worth stating: a bundled list covers the HEAD of the
// distribution, not the tail. It will not know that a particular person's
// password appeared in a particular breach.
//
// # The normalisation is what makes a short list work
//
// A few hundred literal entries would be nearly useless, because nobody types
// "password" any more — they type "P@ssw0rd1!" and believe they have solved
// something. So a candidate is folded first: case dropped, leet substitutions
// undone, and trailing digits and punctuation removed. "P@ssw0rd1!" and
// "passw0rd" and "Password123" all reduce to "password" and are all refused by
// one entry.
//
// That folding is ONLY for rejection. Nothing here ever stores, logs or
// transmits a password.
package pwpolicy

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MinLength is §7.1's floor.
//
// Twelve, and no upper-case/digit/symbol requirement to go with it. Composition
// rules produce "Password1!" — they push people toward the exact shapes the
// folding above defeats, while a long passphrase that breaks every rule is
// stronger than anything they generate. Length plus a known-password check is
// the pair that actually works.
const MinLength = 12

// MaxLength bounds what gets hashed.
//
// Argon2id's cost is set by its parameters rather than by input length, so this
// is not about the KDF — it is about not accepting a megabyte of text into a
// field, hashing it, and storing the result.
const MaxLength = 256

// Errors, distinguished so a form can say which rule was broken. All of them
// are safe to display: none quotes the password back.
var (
	ErrTooShort  = fmt.Errorf("a password needs at least %d characters", MinLength)
	ErrTooLong   = fmt.Errorf("a password can be at most %d characters", MaxLength)
	ErrCommon    = errors.New("that password appears on lists attackers try first")
	ErrLikeName  = errors.New("a password cannot be your username")
	ErrOneRune   = errors.New("that password is one character repeated")
	ErrSequence  = errors.New("that password is a straight run of the keyboard")
	ErrWhitespce = errors.New("a password cannot be only spaces")
)

// Check validates a candidate.
//
// `username` is compared against, because "cameron2026" is the single most
// guessable password for an account called cameron and no generic list can
// contain it.
func Check(password, username string) error {
	// Length is counted in RUNES. Counting bytes would tell somebody writing in
	// Japanese that a twelve-character passphrase is long enough at four
	// characters, and somebody writing in Greek that theirs is too short.
	n := len([]rune(password))
	switch {
	case n < MinLength:
		return ErrTooShort
	case n > MaxLength:
		return ErrTooLong
	case strings.TrimSpace(password) == "":
		return ErrWhitespce
	}

	folded := Fold(password)
	lowered := strings.ToLower(strings.TrimSpace(password))

	// An all-digit or all-symbol password folds to NOTHING, because folding
	// strips exactly those. Left unhandled it escapes every check below: it
	// matches no list entry and reaches no sequence test, so "123412341234"
	// would be accepted as a strong password. Refused here, before anything
	// depends on the folded form being a word.
	if folded == "" {
		if isSequence(lowered) || isOneRune(password) {
			return ErrSequence
		}
		return ErrCommon
	}

	if username != "" {
		u := Fold(username)
		if u != "" && (folded == u || strings.Contains(folded, u) && len(u) >= 4) {
			return ErrLikeName
		}
	}
	if common[folded] {
		return ErrCommon
	}
	if isOneRune(password) {
		return ErrOneRune
	}
	// On `lowered`, not `folded`: folding strips the digits that make a run a
	// run, so "0123456789012" folds to "" and "abc123abc123" folds to
	// "abc123abc" — neither of which looks like a sequence any more.
	if isSequence(lowered) || isSequence(folded) {
		return ErrSequence
	}
	return nil
}

// Fold reduces a password to the form the list is keyed on.
//
// Exported for tests, which is the only honest reason: the folding is where the
// coverage comes from, and a folding nobody can inspect is a list nobody can
// reason about.
func Fold(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	// STRIP FIRST, THEN SUBSTITUTE. The order is the whole function.
	//
	// Substituting first turns the trailing "1!" of "Password1!" into "ii" —
	// letters, which the strip can no longer remove — so it folds to
	// "passwordii" and matches nothing. Every decorated variant would sail
	// through a list that looks like it covers them, which is the worst outcome
	// available: a check that reports success while doing nothing.
	trimTail := func(s string) string {
		return strings.TrimRightFunc(s, func(r rune) bool {
			return unicode.IsDigit(r) || unicode.IsPunct(r) ||
				unicode.IsSymbol(r) || unicode.IsSpace(r)
		})
	}
	// The "1!" people append when told their password needs a number and a
	// symbol.
	s = trimTail(s)
	// And leading punctuation, for the same reason in the other direction.
	s = strings.TrimLeftFunc(s, func(r rune) bool {
		return unicode.IsPunct(r) || unicode.IsSymbol(r) || unicode.IsSpace(r)
	})

	// Now undo leet substitutions, on what is left. Deliberately one direction
	// only — "0" becomes "o" and never the reverse — because the goal is to
	// collapse decorated variants onto their base word, not to enumerate every
	// spelling.
	s = strings.NewReplacer(
		"@", "a", "4", "a",
		"3", "e",
		"0", "o",
		"1", "i", "!", "i",
		"$", "s", "5", "s",
		"7", "t",
		"8", "b",
	).Replace(s)

	// Once more, because a substitution can expose a tail that was hidden
	// behind a symbol: "p@ssword!1" trims to "p@ssword", but "p@ssword!x1"
	// trims to "p@ssword!x" and only becomes "passwordix" after substitution.
	return trimTail(s)
}

func isOneRune(s string) bool {
	r := []rune(s)
	for i := 1; i < len(r); i++ {
		if r[i] != r[0] {
			return false
		}
	}
	return len(r) > 0
}

// keyboardRuns are the sequences a person produces when asked for something
// long and reaching for the nearest thing.
var keyboardRuns = []string{
	"abcdefghijklmnopqrstuvwxyz",
	"qwertyuiopasdfghjklzxcvbnm",
	"qwertyuiop",
	"asdfghjkl",
	"zxcvbnm",
	"0123456789",
	"9876543210",
}

// isSequence reports whether the whole candidate is a slice of a keyboard run,
// possibly repeated.
func isSequence(folded string) bool {
	if len(folded) < 4 {
		return false
	}
	for _, run := range keyboardRuns {
		if strings.Contains(run, folded) {
			return true
		}
		// "abcabcabcabc" — long enough to pass the length rule, and no better
		// than "abc".
		if unit := smallestRepeat(folded); unit != folded && strings.Contains(run, unit) {
			return true
		}
	}
	return false
}

// smallestRepeat returns the shortest string whose repetition is s.
func smallestRepeat(s string) string {
	for size := 1; size <= len(s)/2; size++ {
		if len(s)%size != 0 {
			continue
		}
		unit, ok := s[:size], true
		for i := size; i < len(s); i += size {
			if s[i:i+size] != unit {
				ok = false
				break
			}
		}
		if ok {
			return unit
		}
	}
	return s
}

// --- the recovery passphrase (§7.2b) --------------------------------------------

// PassphraseMinLength is the floor for a recovery passphrase.
//
// Higher than MinLength, and the reason is what the two things ARE. A password
// is one factor guarded by a lockout curve, a session, and a person who notices
// when their reader signs itself out. A recovery passphrase is the thing that
// gets somebody into the account WITHOUT any of that — it is presented by a
// stranger by definition, since the whole premise is that the password is gone.
//
// Sixteen rather than twelve is not a large jump and is not meant to be: it is
// the difference between "a password" and "a short sentence", which is also the
// shape the copy asks for. The lockout, the Argon2id hash and this floor are the
// three controls; none of them is sufficient and the floor is the cheapest.
const PassphraseMinLength = 16

// ErrPassphraseTooShort is its own error so a form can name the right rule.
var ErrPassphraseTooShort = fmt.Errorf(
	"a recovery passphrase needs at least %d characters", PassphraseMinLength)

// ErrSameAsPassword refuses a passphrase equal to the account password.
//
// Not a stylistic objection. The two credentials exist so that losing one
// leaves the other, and a passphrase equal to the password is a second copy of
// the first — it survives nothing the password does not survive, while doubling
// the number of places the same secret is stored.
var ErrSameAsPassword = errors.New(
	"a recovery passphrase has to be different from your password")

// CheckPassphrase validates a recovery passphrase.
//
// It applies everything Check applies — the known-password list with its
// folding, the username rule, the repeated character and keyboard-run refusals —
// and then the two rules that are its own. Sharing the body rather than
// restating it matters here: a passphrase that "password123" would fail as a
// password must not sail through as a recovery credential, which is the exact
// mistake a second, looser validator invites.
//
// `password` may be empty when the caller does not have it to compare against;
// the equality rule is then skipped rather than guessed at.
func CheckPassphrase(passphrase, username, password string) error {
	if n := len([]rune(passphrase)); n < PassphraseMinLength {
		return ErrPassphraseTooShort
	}
	if password != "" && passphrase == password {
		return ErrSameAsPassword
	}
	return Check(passphrase, username)
}
