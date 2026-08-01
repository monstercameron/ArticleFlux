package mailparse

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func parse(t *testing.T, raw string) *Item {
	t.Helper()
	it, err := Parse(strings.NewReader(normalise(raw)), now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return it
}

// Mail uses CRLF line endings, and multipart boundary detection depends on it.
// The fixtures below are written with plain newlines for readability.
func normalise(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

// What a commercial newsletter actually looks like: multipart/alternative, a
// quoted-printable HTML part, an encoded-word subject, a tracking pixel, a
// List-Unsubscribe header, and remote images throughout.
const realNewsletter = `From: "Weekly Digest" <news@example.com>
To: reader@example.org
Subject: =?UTF-8?Q?This_week=3A_caf=C3=A9s_and_=E2=80=9Cculture=E2=80=9D?=
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <abc123@example.com>
List-Unsubscribe: <https://example.com/unsub?u=42>, <mailto:unsub@example.com>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="BOUND"

--BOUND
Content-Type: text/plain; charset=UTF-8

View this in your browser: https://example.com/view/42
--BOUND
Content-Type: text/html; charset=UTF-8
Content-Transfer-Encoding: quoted-printable

<html><body>
<h1>This week</h1>
<p>We visited three caf=C3=A9s and had opinions about =E2=80=9Cculture=E2=80=
=9D.</p>
<img src=3D"https://track.example.com/open.gif?u=3D42" width=3D"1" height=3D=
"1">
<img src=3D"https://cdn.example.com/photo.jpg" width=3D"600">
<a href=3D"https://example.com/article">Read on</a>
<script>alert(1)</script>
</body></html>
--BOUND--
`

func TestRealNewsletter(t *testing.T) {
	it := parse(t, realNewsletter)

	t.Run("encoded-word subject is decoded", func(t *testing.T) {
		want := `This week: cafés and “culture”`
		if it.Subject != want {
			t.Errorf("subject = %q, want %q", it.Subject, want)
		}
	})

	t.Run("sender", func(t *testing.T) {
		if it.SenderKey != "news@example.com" {
			t.Errorf("sender key = %q", it.SenderKey)
		}
		if it.SenderName != "Weekly Digest" {
			t.Errorf("sender name = %q", it.SenderName)
		}
	})

	t.Run("guid is the Message-ID, brackets stripped", func(t *testing.T) {
		if it.GUID != "abc123@example.com" {
			t.Errorf("guid = %q", it.GUID)
		}
	})

	t.Run("date", func(t *testing.T) {
		if it.PublishedAt.Format("2006-01-02") != "2026-07-20" {
			t.Errorf("published = %v", it.PublishedAt)
		}
	})

	// The HTML part must win. The text part on a real newsletter is usually a
	// single "view this in your browser" line, and taking it loses the issue.
	t.Run("the HTML part beats the text part", func(t *testing.T) {
		if !strings.Contains(it.Text, "We visited three cafés") {
			t.Errorf("text = %q", it.Text)
		}
		if strings.Contains(it.Text, "View this in your browser") {
			t.Errorf("the plain-text fallback was used instead of the HTML part: %q", it.Text)
		}
	})

	// Quoted-printable is not decoded by net/mail, and undecoded it renders
	// =3D everywhere without erroring.
	t.Run("quoted-printable is decoded", func(t *testing.T) {
		if strings.Contains(it.HTML, "=3D") || strings.Contains(it.HTML, "=C3=A9") {
			t.Errorf("quoted-printable survived: %q", it.HTML)
		}
		if !strings.Contains(it.Text, "cafés") {
			t.Errorf("the é did not decode: %q", it.Text)
		}
		if !strings.Contains(it.Text, "“culture”") {
			t.Errorf("the smart quotes did not decode: %q", it.Text)
		}
	})

	// §14.1: newsletters are the most tracker-dense HTML on the internet, and a
	// proxied pixel still tells the sender the message was opened.
	t.Run("every remote image is gone, pixel and photo alike", func(t *testing.T) {
		if strings.Contains(it.HTML, "<img") {
			t.Errorf("an image survived the newsletter policy: %q", it.HTML)
		}
		if strings.Contains(it.HTML, "track.example.com") {
			t.Errorf("the tracking pixel's host survived: %q", it.HTML)
		}
	})

	t.Run("stripping is reported so the render does not look broken", func(t *testing.T) {
		if !it.HadRemoteImages {
			t.Error("images were stripped without saying so")
		}
	})

	t.Run("scripts do not survive", func(t *testing.T) {
		if strings.Contains(strings.ToLower(it.HTML), "<script") {
			t.Errorf("a script survived: %q", it.HTML)
		}
	})

	t.Run("links survive, hardened", func(t *testing.T) {
		if !strings.Contains(it.HTML, "example.com/article") {
			t.Errorf("the article link was dropped: %q", it.HTML)
		}
		if !strings.Contains(it.HTML, "noopener") {
			t.Errorf("links were not hardened: %q", it.HTML)
		}
	})

	t.Run("list-unsubscribe is kept", func(t *testing.T) {
		if !strings.Contains(it.ListUnsubscribe, "example.com/unsub") {
			t.Errorf("list-unsubscribe = %q", it.ListUnsubscribe)
		}
	})
}

// RFC 2046 orders alternatives worst-to-best, so a first-wins reader takes the
// plain-text fallback on every message that has one.
func TestLastAlternativeWins(t *testing.T) {
	msg := `From: a@b.com
Subject: Ordering
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: multipart/alternative; boundary="B"

--B
Content-Type: text/plain

plain fallback
--B
Content-Type: text/html

<p>rich version</p>
--B--
`
	it := parse(t, msg)
	if !strings.Contains(it.Text, "rich version") {
		t.Errorf("took the wrong alternative: %q", it.Text)
	}
}

func TestBase64Body(t *testing.T) {
	// "<p>Base64 body</p>"
	msg := `From: a@b.com
Subject: b64
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: text/html; charset=UTF-8
Content-Transfer-Encoding: base64

PHA+QmFzZTY0IGJvZHk8L3A+
`
	it := parse(t, msg)
	if !strings.Contains(it.Text, "Base64 body") {
		t.Errorf("base64 was not decoded: %q", it.Text)
	}
}

// multipart/mixed wrapping multipart/alternative is what a newsletter with an
// attachment looks like, and the body is two levels down.
func TestNestedMultipart(t *testing.T) {
	msg := `From: a@b.com
Subject: Nested
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: multipart/mixed; boundary="OUT"

--OUT
Content-Type: multipart/alternative; boundary="IN"

--IN
Content-Type: text/plain

plain
--IN
Content-Type: text/html

<p>nested html</p>
--IN--
--OUT
Content-Type: application/pdf; name="report.pdf"
Content-Transfer-Encoding: base64

JVBERi0xLjQKJcfsj6IK
--OUT--
`
	it := parse(t, msg)
	if !strings.Contains(it.Text, "nested html") {
		t.Errorf("the nested body was not found: %q", it.Text)
	}
	// A PDF's bytes decoded as text would be indexed, searched and read aloud.
	if strings.Contains(it.Text, "PDF") || strings.Contains(it.Text, "JVBER") {
		t.Errorf("an attachment leaked into the body: %q", it.Text)
	}
}

func TestPlainTextOnly(t *testing.T) {
	msg := `From: a@b.com
Subject: Plain
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: text/plain; charset=UTF-8

First paragraph.

Second paragraph
with a wrapped line.
`
	it := parse(t, msg)
	if !strings.Contains(it.Text, "First paragraph") {
		t.Errorf("text = %q", it.Text)
	}
	// Paragraph structure has to survive, or the reading pane renders a wall.
	if strings.Count(it.HTML, "<p>") != 2 {
		t.Errorf("paragraphs were not preserved: %q", it.HTML)
	}
	if !strings.Contains(it.HTML, "<br>") {
		t.Errorf("the wrapped line lost its break: %q", it.HTML)
	}
}

// Plain text containing angle brackets must not become markup.
func TestPlainTextIsEscaped(t *testing.T) {
	msg := `From: a@b.com
Subject: Escaping
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: text/plain

Use <script>alert(1)</script> and a && b to break things.
`
	it := parse(t, msg)
	if strings.Contains(strings.ToLower(it.HTML), "<script") {
		t.Errorf("plain text became markup: %q", it.HTML)
	}
	if !strings.Contains(it.Text, "alert(1)") {
		t.Errorf("the literal text was lost: %q", it.Text)
	}
}

// §6.4, and the reason it gets its own function: global keying would merge two
// people's private mail into one shared source row.
func TestNaturalKeyIsPerMailbox(t *testing.T) {
	a := NaturalKey("mbox-alice", "News@Example.com")
	b := NaturalKey("mbox-bob", "news@example.com")

	if a == b {
		t.Fatal("two users' mailboxes produced the same source key")
	}
	if !strings.HasPrefix(a, "mailbox:mbox-alice:") {
		t.Errorf("key = %q", a)
	}
	// Case-folded, or the same sender writing News@ and news@ becomes two
	// sources in one mailbox.
	if a != NaturalKey("mbox-alice", "  news@example.com  ") {
		t.Errorf("key is not normalised: %q", a)
	}
}

func TestMissingHeadersDegradeGracefully(t *testing.T) {
	t.Run("no Message-ID falls back to a content hash", func(t *testing.T) {
		msg := `From: a@b.com
Subject: No ID
Date: Mon, 20 Jul 2026 09:00:00 +0000
Content-Type: text/plain

body
`
		it := parse(t, msg)
		if it.GUID == "" {
			t.Error("no guid at all; the message can never be marked read")
		}
		if !strings.HasPrefix(it.GUID, "mail:") {
			t.Errorf("guid = %q", it.GUID)
		}
		// Stable across polls, or the unread count climbs forever.
		again := parse(t, msg)
		if it.GUID != again.GUID {
			t.Errorf("the fallback guid changed between reads: %q then %q", it.GUID, again.GUID)
		}
	})

	t.Run("no Date falls through to first-seen, never the zero time", func(t *testing.T) {
		msg := `From: a@b.com
Subject: No date
Message-ID: <x@y>
Content-Type: text/plain

body
`
		it := parse(t, msg)
		if it.PublishedAt.IsZero() {
			t.Error("zero published time; the item sorts to the bottom forever")
		}
		if !it.PublishedAt.Equal(now) {
			t.Errorf("published %v, want first-seen", it.PublishedAt)
		}
	})

	t.Run("no Subject falls back to the first words", func(t *testing.T) {
		msg := `From: a@b.com
Message-ID: <x@y>
Date: Mon, 20 Jul 2026 09:00:00 +0000
Content-Type: text/plain

The opening line of an untitled message.
`
		it := parse(t, msg)
		if it.Subject == "" {
			t.Error("an untitled row would render blank")
		}
		if !strings.Contains(it.Subject, "The opening line") {
			t.Errorf("subject = %q", it.Subject)
		}
	})

	t.Run("a malformed From still yields a key", func(t *testing.T) {
		msg := `From: not an address at all
Subject: Broken sender
Message-ID: <x@y>
Date: Mon, 20 Jul 2026 09:00:00 +0000
Content-Type: text/plain

body
`
		it := parse(t, msg)
		if it.SenderKey == "" {
			t.Error("no sender key, so the item cannot be filed under any source")
		}
	})

	t.Run("no Content-Type is text/plain per RFC 2045", func(t *testing.T) {
		msg := `From: a@b.com
Subject: Bare
Message-ID: <x@y>
Date: Mon, 20 Jul 2026 09:00:00 +0000

just a body
`
		it := parse(t, msg)
		if !strings.Contains(it.Text, "just a body") {
			t.Errorf("text = %q", it.Text)
		}
	})
}

// A future date is clamped, exactly as a feed's is.
func TestFutureDatesAreClamped(t *testing.T) {
	msg := `From: a@b.com
Subject: From the future
Message-ID: <x@y>
Date: Mon, 20 Jul 2087 09:00:00 +0000
Content-Type: text/plain

body
`
	it := parse(t, msg)
	if it.PublishedAt.After(now) {
		t.Errorf("a 2087 date was not clamped: %v", it.PublishedAt)
	}
}

func TestJunkInputIsRefusedNotGuessed(t *testing.T) {
	for _, in := range []string{"", "not a message", "\r\n\r\n"} {
		if _, err := Parse(strings.NewReader(in), now); err == nil {
			t.Errorf("Parse(%q) accepted it", in)
		}
	}

	// Headers but no body at all: there is nothing to read, and saying so beats
	// storing an empty item.
	empty := "From: a@b.com\r\nSubject: Nothing\r\nMessage-ID: <x@y>\r\n\r\n"
	if _, err := Parse(strings.NewReader(empty), now); err == nil {
		t.Error("a message with no body was accepted")
	}
}

// Deeply nested multipart is either broken or hostile, and a mail parser that
// recurses on attacker-controlled structure without a limit is a stack overflow
// waiting for the right message.
func TestDeepNestingIsBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("From: a@b.com\nSubject: Deep\nMessage-ID: <x@y>\n")
	b.WriteString("Date: Mon, 20 Jul 2026 09:00:00 +0000\n")
	const depth = 40
	for i := 0; i < depth; i++ {
		if i == 0 {
			b.WriteString("Content-Type: multipart/mixed; boundary=\"B0\"\n\n")
		}
		b.WriteString("--B" + itoa(i) + "\n")
		if i < depth-1 {
			b.WriteString("Content-Type: multipart/mixed; boundary=\"B" + itoa(i+1) + "\"\n\n")
		} else {
			b.WriteString("Content-Type: text/plain\n\ndeep body\n")
		}
	}

	// The assertion is only that this terminates without exploding.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		_, _ = Parse(strings.NewReader(normalise(b.String())), now)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("parsing deeply nested multipart did not terminate")
	}
}

// mime/multipart auto-decodes quoted-printable transfer encoding on any part
// it reads, so decodeTransfer's own quoted-printable branch is only reachable
// on a top-level, non-multipart message.
func TestQuotedPrintableTopLevelBody(t *testing.T) {
	msg := `From: a@b.com
Subject: QP top level
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: text/html; charset=UTF-8
Content-Transfer-Encoding: quoted-printable

<p>caf=C3=A9</p>
`
	it := parse(t, msg)
	if !strings.Contains(it.Text, "café") {
		t.Errorf("quoted-printable top-level body was not decoded: %q", it.Text)
	}
}

// A part that lies about its own encoding is common in the wild (a sender's
// base64 encoder truncates, a proxy mangles the body). walk must drop that one
// part rather than failing the whole message.
func TestCorruptTransferEncodingPartIsSkipped(t *testing.T) {
	msg := `From: a@b.com
Subject: Corrupt
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: multipart/alternative; boundary="B"

--B
Content-Type: text/plain
Content-Transfer-Encoding: base64

!!!not-valid-base64-at-all!!!
--B
Content-Type: text/html

<p>good part</p>
--B--
`
	it := parse(t, msg)
	if !strings.Contains(it.Text, "good part") {
		t.Errorf("the readable alternative was lost alongside the corrupt one: %q", it.Text)
	}
}

// A multipart Content-Type with no boundary parameter cannot be walked at
// all; the message has to fail cleanly rather than panic on multipart.NewReader.
func TestMultipartMissingBoundaryFailsCleanly(t *testing.T) {
	msg := `From: a@b.com
Subject: NoBoundary
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: multipart/alternative

--B
Content-Type: text/plain

body
--B--
`
	if _, err := Parse(strings.NewReader(normalise(msg)), now); err == nil {
		t.Error("a multipart message with no boundary parameter was accepted")
	}
}

// RFC 2047's CharsetReader hook only fires for charsets net/mime does not
// already decode by itself — utf-8, us-ascii and, as of Go's mime package,
// iso-8859-1 are all built in. windows-1252 is not, so a subject encoded in
// it is what actually exercises decodeHeader's own CharsetReader closure.
func TestDecodeHeaderNonUTF8Charset(t *testing.T) {
	msg := `From: a@b.com
Subject: =?windows-1252?Q?caf=E9?=
Date: Mon, 20 Jul 2026 09:00:00 +0000
Message-ID: <x@y>
Content-Type: text/plain

body
`
	it := parse(t, msg)
	if it.Subject != "café" {
		t.Errorf("subject = %q, want %q", it.Subject, "café")
	}
}

// Excess blank lines between paragraphs must not produce an empty <p></p>;
// a wall of empty tags is exactly the "renders a wall" failure this function
// exists to avoid.
func TestTextToHTMLSkipsBlankParagraphs(t *testing.T) {
	got := textToHTML("First.\n\n\n\nSecond.")
	if strings.Contains(got, "<p></p>") {
		t.Errorf("an empty paragraph survived: %q", got)
	}
	if strings.Count(got, "<p>") != 2 {
		t.Errorf("want exactly two paragraphs, got %q", got)
	}
}

// truncate is exercised directly because Parse's Subject/Summary fallbacks
// only reach the truncation branch with lengths tuned exactly to trip the
// word-boundary logic, and getting that indirectly right is more fragile
// than asserting it here.
func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"under the limit is returned unchanged", "short", 10, "short"},
		{"exactly at the limit is returned unchanged", "exactlyten", 10, "exactlyten"},
		{
			"cuts at the last space when it is past the midpoint",
			"hello world foo", 12, "hello world…",
		},
		{
			"a space too close to the start is not used as the cut point",
			"hello world foo", 10, "hello worl…",
		},
		{
			"no space at all cuts at exactly n",
			"abcdefghijklmnop", 10, "abcdefghij…",
		},
		{
			"trailing punctuation left by the word-boundary cut is trimmed",
			"One, two, three, four, five", 6, "One…",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncate(tt.s, tt.n); got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
