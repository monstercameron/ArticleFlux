package apierr

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc/status"
)

// FuzzUnclassifiedErrorsNeverLeak is the package's central promise under
// hostile input: "An error that is not an *Error becomes Internal with a safe
// message" (Status's doc comment) and FromDomain's "does NOT wrap an
// unrecognised error's text into the response." The failure mode of getting
// this wrong is a database error, a file path or a stack fragment landing on
// somebody's screen — which is exactly the shape of thing an attacker would
// try to provoke by feeding weird input upstream until an error message
// echoes it back.
//
// A freshly constructed errors.New(s) is never == (nor wraps, nor is wrapped
// by) any of the sentinel errors FromDomain's switch matches on those are
// package-level vars compared by identity via errors.Is/As, and a fuzz-built
// string can never equal one of those pointers. So every input here is
// guaranteed to fall through FromDomain's default case, and the assertion is
// unconditional: the classified message is ALWAYS the fixed "internal error"
// string, never the input.
func FuzzUnclassifiedErrorsNeverLeak(f *testing.F) {
	seeds := []string{
		"", "sqlite3: no such column: user_item_state.secret_column",
		"pq: password authentication failed for user \"admin\"",
		"open /etc/passwd: permission denied",
		"internal error", // the exact safe string itself, as an adversarial seed
		"srv.internal", "\x00\x00", "日本語のエラー",
		strings.Repeat("x", 10_000),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 20_000 {
			return
		}
		if raw == "" {
			return // errors.New("") is a valid but uninteresting probe; skip
		}
		leaky := errors.New(raw)

		classified := FromDomain(leaky)
		if KindOf(classified) != KindInternal {
			t.Fatalf("errors.New(%q) classified as %d, want KindInternal", raw, KindOf(classified))
		}

		// The message must be EXACTLY the fixed safe string — not merely "does
		// not contain raw", which is a check a short or common substring (a
		// single letter, a space) trivially satisfies without proving anything.
		// Equality is the only assertion that actually rules out a leak.
		grpcErr := Status(classified)
		msg := status.Convert(grpcErr).Message()
		if msg != "internal error" {
			t.Fatalf("gRPC message for input %q = %q, want the fixed safe string", raw, msg)
		}

		_, body := HTTPStatus(classified)
		if body["message"] != "internal error" {
			t.Fatalf("HTTP message for input %q = %q, want the fixed safe string", raw, body["message"])
		}

		// The cause must still be reachable for the log — safety to the client
		// must not come at the cost of losing the diagnostic entirely.
		if !errors.Is(classified, leaky) {
			t.Fatalf("input %q: the cause was discarded, the log has nothing to say", raw)
		}
	})
}
