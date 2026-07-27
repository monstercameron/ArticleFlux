package mailparse

import (
	"strings"
	"testing"
	"time"
)

// FuzzParse feeds arbitrary bytes through Parse as a raw message. This is the
// most hostile input surface in this package's scope: a mailbox source polls
// whatever a stranger emailed, MIME-decodes it, walks an attacker-controlled
// multipart tree, and unwraps two different transfer encodings — all before
// anything gets to sanitize.HTML. The only assertion is that arbitrary bytes,
// including ones that are almost but not quite a valid message, never panic;
// Parse returning an error is the normal, expected outcome for junk.
func FuzzParse(f *testing.F) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	seeds := []string{
		realNewsletterForFuzz,
		"", "not a message", "\r\n\r\n",
		"From: a@b.com\r\nSubject: x\r\n\r\nbody",
		"From: a@b.com\r\nContent-Type: multipart/mixed; boundary=\"B\"\r\n\r\n--B\r\n--B--\r\n",
		"From: not an address\r\nSubject: =?UTF-8?Q?broken=?=\r\n\r\nbody",
	}
	for _, s := range seeds {
		f.Add(normalise(s))
	}
	f.Fuzz(func(t *testing.T, raw string) {
		done := make(chan struct{})
		var panicked any
		go func() {
			defer close(done)
			defer func() { panicked = recover() }()
			_, _ = Parse(strings.NewReader(raw), now)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("Parse did not terminate on: %q", raw)
		}
		if panicked != nil {
			t.Fatalf("Parse(%q) panicked: %v", raw, panicked)
		}
	})
}

// realNewsletterForFuzz mirrors realNewsletter from mailparse_test.go without
// depending on package-level ordering across files; kept as its own seed
// string so the fuzzer starts from a message with real structure.
const realNewsletterForFuzz = `From: "Weekly Digest" <news@example.com>
Subject: =?UTF-8?Q?This_week=3A_caf=C3=A9s?=
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <abc123@example.com>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="BOUND"

--BOUND
Content-Type: text/plain; charset=UTF-8

View this in your browser
--BOUND
Content-Type: text/html; charset=UTF-8
Content-Transfer-Encoding: quoted-printable

<html><body><p>hello=3D</p></body></html>
--BOUND--
`
