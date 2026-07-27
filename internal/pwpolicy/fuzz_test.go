package pwpolicy

import "testing"

// The order-of-operations bug named in the package comment, pinned directly:
// "STRIP FIRST, THEN SUBSTITUTE." Substituting first turns the trailing "1!"
// people append into letters ("ii"), which strip can no longer remove, so the
// decorated password folds to "passwordii" and matches nothing on the list —
// a check that reports success while doing nothing. Every case here is chosen
// so that reversing the order in Fold would produce a DIFFERENT (wrong) result.
func TestFoldStripsBeforeSubstituting(t *testing.T) {
	cases := []struct{ in, want string }{
		// substitute-first would yield "passwordii" (trailing "1!" -> "i","i")
		{"Password1!", "password"},
		// substitute-first would yield "passwordoi" (trailing "0!" -> "o","i")
		{"password0!", "password"},
		// substitute-first would leave "password4" unstripped as "passworda"
		// only by accident (a is a letter either way); use a case where the
		// substituted character is NOT stripped by the second trim: "3" (->"e")
		// at the tail must still be removed as a digit before substitution.
		{"password3", "password"},
		// A symbol tail that substitutes into letters: "$" -> "s", "@" -> "a".
		{"password$@", "password"},
	}
	for _, c := range cases {
		if got := Fold(c.in); got != c.want {
			t.Errorf("Fold(%q) = %q, want %q — a substitute-before-strip bug would "+
				"leave decoration in the output and match nothing on the list", c.in, got, c.want)
		}
	}
}

// Fold must be idempotent: nothing it does to a string should expose new
// material for a second pass to act on. If it were not, a future caller that
// folds twice (or a list entry compared against a re-folded value) would see a
// different, unreviewed string.
func TestFoldIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"Password1!", "P@ssw0rd123!!!", "...leading...", "trailing444",
		"", "   ", "already_plain", "MiXeD-Case_1$",
	} {
		once := Fold(in)
		twice := Fold(once)
		if once != twice {
			t.Errorf("Fold(%q) = %q, but Fold of that = %q — folding is not idempotent",
				in, once, twice)
		}
	}
}

// FuzzCheckNeverPanics exercises Check and Fold against hostile input: this
// runs on every password submission, unauthenticated at registration.
func FuzzCheckNeverPanics(f *testing.F) {
	seeds := []string{
		"", " ", "password", "P@ssw0rd1234", "aaaaaaaaaaaa", "123412341234",
		"日本語日本語日本語日本語", "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00",
		"cameron2026!", "a very long passphrase with many words in it indeed",
	}
	for _, s := range seeds {
		f.Add(s, "cam")
	}
	f.Fuzz(func(t *testing.T, password, username string) {
		if len(password) > 10_000 || len(username) > 10_000 {
			return // pathological sizes are a perf question, not a correctness one
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Check(%q, %q) panicked: %v", password, username, r)
			}
		}()
		_ = Check(password, username)

		folded := Fold(password)
		if Fold(folded) != folded {
			t.Fatalf("Fold(%q) = %q is not a fixed point: Fold of it = %q",
				password, folded, Fold(folded))
		}
	})
}
