//go:build js && wasm

package view

import "testing"

// The topic label is its heaviest terms, and a topic about one language is made of
// a stem and its versions: the sentence names what is being followed, once each.
func TestAttuneWhyDropsVersionsOfATermAlreadyNamed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"Rustup · Rust · Rust 1", "Rustup · Rust"},
		{"Rust · Rust 1 · Cargo", "Rust · Cargo"},
		{"rust · Rust", "rust"},
		{"Go", "Go"},
		{"", ""},
		{" · ", " · "},
	} {
		if got := attuneWhy(c.in); got != c.want {
			t.Errorf("attuneWhy(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
