// Package mailparse turns a MIME message into a normalised item
// (TODO 4.8, plan.md §14.1, §6.4).
//
// Pure: bytes in, an item out. No IMAP, no network, no storage — 6.x owns the
// connection and the UID bookkeeping. A message is a file, and parsing it
// should not need a mailbox.
//
// # Why newsletters are their own thing
//
// §14.1's decision is IMAP polling rather than an SMTP listener, so a mailbox
// becomes another `sources.kind` and needs no inbound port, no MX record, no
// SPF/DKIM and no spam pipeline. What arrives is ordinary email, which means:
//
//   - **The most tracker-dense HTML on the internet.** Every commercial
//     newsletter carries an open-tracking pixel, and many carry several. That is
//     why the body leaves here marked for `sanitize.Newsletter`, which drops
//     remote images outright rather than proxying them — a proxied pixel still
//     tells the sender the message was opened.
//   - **Per-user sources, never global.** A20's natural key is
//     `mailbox:<mailbox_id>:<sender>` (§6.4). A14's "one row per feed, polled
//     once" logic is right for a public feed and catastrophic for private mail:
//     global keying would merge two people's newsletters into one shared row.
//     NaturalKey below is the only correct way to build that string, which is
//     why it lives here rather than being formatted at three call sites.
//
// # Encodings, and why this does not use net/mail alone
//
// `net/mail` parses headers and hands back a body reader. It does not decode
// quoted-printable or base64 transfer encodings, does not walk multipart trees,
// and does not decode RFC 2047 encoded-words in headers. All three appear in
// essentially every real newsletter, so all three are handled here.
package mailparse

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/charsetdec"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/timeutil"
)

// MaxBodyBytes bounds one message.
//
// Newsletters are HTML documents with inline styles and are routinely several
// hundred kilobytes. 8 MiB is generous for that and small enough that a mailbox
// full of attachments cannot exhaust memory during a poll.
const MaxBodyBytes = 8 << 20

// maxDepth bounds multipart nesting.
//
// multipart/mixed containing multipart/alternative containing multipart/related
// is three, and is what a normal newsletter with inline images looks like.
// Anything past eight is either broken or hostile, and a mail parser that
// recurses on attacker-controlled structure without a limit is a stack overflow
// waiting for the right message.
const maxDepth = 8

// Item is a message, normalised into the shape the rest of the pipeline uses.
type Item struct {
	// GUID is the Message-ID, which is globally unique by specification and is
	// what makes re-polling a mailbox idempotent.
	GUID string
	// SenderKey identifies the sender for source keying: the bare address,
	// lowercased.
	SenderKey string
	// SenderName is the display name, for the source title.
	SenderName string
	Subject    string
	// HTML is sanitised under sanitize.Newsletter — remote images already gone.
	HTML string
	// Text is plain text for search, ranking and TTS.
	Text        string
	Summary     string
	PublishedAt time.Time
	// ListUnsubscribe is the List-Unsubscribe header, so the UI can offer to
	// unsubscribe from the newsletter rather than only from the source.
	ListUnsubscribe string
	// HadRemoteImages reports that images were stripped. Worth surfacing: a
	// newsletter rendering without its illustrations looks broken unless the
	// reader is told it was deliberate.
	HadRemoteImages bool
}

// NaturalKey builds the per-user source key for a sender (§6.4).
//
// The single correct way to construct it. Formatting this string at each call
// site is how one of them ends up missing the mailbox id, at which point two
// users' newsletters from the same sender converge on one row — which is a
// privacy failure rather than a bug.
func NaturalKey(mailboxID, sender string) string {
	return "mailbox:" + mailboxID + ":" + strings.ToLower(strings.TrimSpace(sender))
}

// Parse reads one RFC 5322 message.
func Parse(r io.Reader, now time.Time) (*Item, error) {
	msg, err := mail.ReadMessage(io.LimitReader(r, MaxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("mailparse: %w", err)
	}

	it := &Item{
		Subject:         decodeHeader(msg.Header.Get("Subject")),
		ListUnsubscribe: strings.TrimSpace(msg.Header.Get("List-Unsubscribe")),
	}

	if addr, err := mail.ParseAddress(msg.Header.Get("From")); err == nil {
		it.SenderKey = strings.ToLower(strings.TrimSpace(addr.Address))
		it.SenderName = decodeHeader(addr.Name)
	} else {
		// A malformed From is common in bulk mail. The raw header is a worse
		// key than a parsed address and a much better one than nothing — an
		// item with no sender cannot be filed under a source at all.
		it.SenderKey = strings.ToLower(strings.TrimSpace(msg.Header.Get("From")))
	}
	if it.SenderName == "" {
		it.SenderName = it.SenderKey
	}

	// Message-ID is unique by specification, which makes re-polling idempotent.
	// Without one, identity falls back to a hash of what identifies the message
	// to a person — the same last-resort ladder the feed parser uses.
	it.GUID = strings.Trim(strings.TrimSpace(msg.Header.Get("Message-ID")), "<>")
	if it.GUID == "" {
		it.GUID = "mail:" + hashOf(it.SenderKey, it.Subject, msg.Header.Get("Date"))
	}

	it.PublishedAt = parseDate(msg.Header.Get("Date"), now)

	htmlBody, textBody, err := walk(msg.Header.Get("Content-Type"),
		msg.Header.Get("Content-Transfer-Encoding"), msg.Body, 0)
	if err != nil {
		return nil, err
	}

	// §14.1: prefer text/html, fall back to text/plain. The HTML part carries
	// the structure and the links; the text part is a courtesy copy that in
	// practice is often just a URL saying "view this in your browser".
	switch {
	case strings.TrimSpace(htmlBody) != "":
		it.HadRemoteImages = strings.Contains(strings.ToLower(htmlBody), "<img")
		it.HTML = sanitize.HTML(htmlBody, sanitize.Newsletter)
		it.Text = sanitize.Text(it.HTML)
	case strings.TrimSpace(textBody) != "":
		it.Text = strings.TrimSpace(textBody)
		it.HTML = sanitize.HTML(textToHTML(textBody), sanitize.Newsletter)
	default:
		return nil, fmt.Errorf("mailparse: no readable body")
	}

	if it.Subject == "" {
		// An untitled row is worse than a truncated first line.
		it.Subject = truncate(collapse(it.Text), 80)
	}
	it.Summary = truncate(collapse(it.Text), 280)
	return it, nil
}

// walk descends the MIME tree, returning the best HTML and text parts found.
//
// Depth-first and last-wins within a multipart/alternative, because RFC 2046
// orders alternatives worst-to-best: the richest representation is last, and a
// first-wins reader gets the plain-text fallback on every message that has one.
func walk(contentType, encoding string, body io.Reader, depth int) (htmlPart, textPart string, err error) {
	if depth > maxDepth {
		return "", "", nil
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		// A missing or unparseable Content-Type means text/plain per RFC 2045,
		// and plenty of hand-rolled senders omit it entirely.
		mediaType, params = "text/plain", map[string]string{}
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", "", nil
		}
		mr := multipart.NewReader(body, boundary)
		for {
			part, perr := mr.NextPart()
			if perr == io.EOF {
				break
			}
			if perr != nil {
				// A truncated or malformed part ends the walk with whatever was
				// found so far. Refusing the whole message because its fourth
				// attachment is broken loses the newsletter to save nothing.
				break
			}
			h, t, werr := walk(part.Header.Get("Content-Type"),
				part.Header.Get("Content-Transfer-Encoding"), part, depth+1)
			_ = part.Close()
			if werr != nil {
				continue
			}
			if h != "" {
				htmlPart = h
			}
			if t != "" {
				textPart = t
			}
		}
		return htmlPart, textPart, nil
	}

	// Attachments are not the newsletter. A PDF's bytes decoded as text would be
	// indexed, searched and read aloud.
	if !strings.HasPrefix(mediaType, "text/") {
		return "", "", nil
	}

	raw, err := io.ReadAll(io.LimitReader(decodeTransfer(body, encoding), MaxBodyBytes))
	if err != nil {
		return "", "", nil
	}

	// Charset per part: a message can legitimately mix a UTF-8 HTML part with a
	// Latin-1 text part, and the part's own header is the only place that says so.
	decoded, _, derr := charsetdec.Decode(raw, contentType)
	if derr != nil {
		decoded = raw
	}

	if mediaType == "text/html" {
		return string(decoded), "", nil
	}
	return "", string(decoded), nil
}

// decodeTransfer unwraps Content-Transfer-Encoding.
//
// net/mail does not do this, and without it a quoted-printable newsletter
// renders with =3D everywhere and a base64 one renders as base64 — neither of
// which errors, which is why it is worth stating.
func decodeTransfer(r io.Reader, encoding string) io.Reader {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		return quotedprintable.NewReader(r)
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, r)
	default:
		// 7bit, 8bit, binary, and anything unrecognised: pass through. An
		// unknown encoding is more likely to be a typo than a real format, and
		// raw bytes are recoverable where a refusal is not.
		return r
	}
}

// decodeHeader resolves RFC 2047 encoded-words.
//
// "=?UTF-8?B?SGVsbG8=?=" is what a subject with a non-ASCII character looks
// like on the wire, and it appears in a large fraction of real newsletters.
// Undecoded, it becomes the item's title verbatim.
func decodeHeader(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	dec := &mime.WordDecoder{
		CharsetReader: func(charset string, input io.Reader) (io.Reader, error) {
			raw, err := io.ReadAll(io.LimitReader(input, 64<<10))
			if err != nil {
				return nil, err
			}
			out, _, err := charsetdec.Decode(raw, "text/plain; charset="+charset)
			if err != nil {
				return strings.NewReader(string(raw)), nil
			}
			return strings.NewReader(string(out)), nil
		},
	}
	if out, err := dec.DecodeHeader(s); err == nil {
		return collapse(out)
	}
	return collapse(s)
}

// parseDate reads the Date header, falling through to first-seen.
//
// Never the zero time, for the reason it is never the zero time anywhere else:
// an item stamped year 1 sorts to the bottom of every list forever.
func parseDate(s string, now time.Time) time.Time {
	if s = strings.TrimSpace(s); s != "" {
		if t, err := mail.ParseDate(s); err == nil {
			return timeutil.ClampPublished(t, now)
		}
	}
	return now
}

// textToHTML wraps a plain-text body so it can be rendered in a pane built for
// HTML, preserving the paragraph breaks that carry the structure.
func textToHTML(s string) string {
	var b strings.Builder
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(escapeText(strings.ReplaceAll(para, "\n", "<br>")))
		b.WriteString("</p>")
	}
	return b.String()
}

// escapeText escapes everything except the <br> just inserted above.
var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeText(s string) string {
	return strings.ReplaceAll(textEscaper.Replace(s), "&lt;br&gt;", "<br>")
}

func hashOf(parts ...string) string {
	// FNV-1a inline: this needs a stable short identifier, not a cryptographic
	// one, and pulling in crypto/sha256 for a fallback guid is more than the job
	// asks for.
	const offset, prime = uint64(14695981039346656037), uint64(1099511628211)
	h := offset
	for _, p := range parts {
		for i := 0; i < len(p); i++ {
			h ^= uint64(p[i])
			h *= prime
		}
		h ^= 0
		h *= prime
	}
	return fmt.Sprintf("%016x", h)
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:") + "…"
}
