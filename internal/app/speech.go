package app

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// ttsPrefKey is the per-user opt-in. Absent or anything but "true" means off.
//
// Default-off is the whole point: this endpoint sends the article you are
// reading to a third party, and a feature like that must never become active
// because of a default someone forgot to change.
const ttsPrefKey = "tts.smartPlus"

// digestPrefKey turns a long article into about a minute of spoken summary
// instead of the whole thing (§10.7).
//
// A SECOND opt-in rather than a mode of the first, and default off like it, for
// the same reason: it is a second egress and a second bill. Smart+ voice sends
// the article to have it spoken; this also sends it to have it read and
// rewritten. Someone who consented to the first has not thereby consented to
// the second, and a reader watching their usage should be able to tell the two
// apart on the settings screen.
const digestPrefKey = "tts.digest"

// digestFor returns the spoken summary of an item.
//
// Split out of the handler so the fallback logic there stays legible: every
// error from here is recoverable by reading the article itself, and the handler
// says so in one place.
func (a *App) digestFor(ctx context.Context, it store.Item) (string, error) {
	if a.digest == nil {
		return "", smart.ErrNothingToSummarise
	}
	// The article's own text, already stripped of markup — the same thing the
	// voice would otherwise read. Sending the raw HTML instead would bill for
	// `<div class="wp-block-group">` at the summariser as well as at the voice.
	body := speechBody(it)
	return a.digest.Speakable(ctx, it.ID, it.SourceTitle, it.Title, body)
}

// speechTTL is how long a minted listening ticket stays valid.
//
// Between the asset proxy's twelve hours and the page proxy's two, and the
// reasoning is the shape of listening rather than a security number: you start
// an article, you pause it, you come back after lunch and press play — and the
// browser refetches, because an <audio> element does not hold a decoded stream
// forever. Two hours would expire mid-afternoon on an article opened at noon;
// twelve is longer than this needs to be for a capability that carries who the
// listener is.
const speechTTL = 6 * time.Hour

// SpeechURL mints a listening ticket for one item.
//
// Returns "" when this instance cannot synthesise anything, which is the signal
// the client uses to leave the Smart+ control off the article entirely rather
// than offering a button that answers 501.
//
// # Why the ticket is sealed and not signed
//
// /asset and /p sign a plaintext target: the URL says what it fetches, the HMAC
// says we minted it, and neither carries identity — which is exactly why they
// can be shared, logged and cached without telling anyone anything.
//
// This one cannot work that way. Reading an item requires a scope, so the
// capability has to carry one, and a scope is the reader's identity. Signing it
// would put a tenant and user id in every <audio src> — and therefore in browser
// history, in the referrer, and in every access log between here and the
// listener. So the payload is SEALED with AES-256-GCM instead: authenticated
// like a signature, and opaque as well. Tampering fails to open rather than
// verifying against a different message, which is the property that matters.
func (a *App) SpeechURL(ctx context.Context, sc store.Scope, itemID string) string {
	if a.tts == nil || len(a.speechKey) != 32 || itemID == "" || !sc.Valid() {
		return ""
	}
	// Checked against the key that exists NOW. An instance whose key is pasted
	// in at runtime starts minting tickets from that moment, and one whose key
	// is cleared stops — without a restart, and without handing out tickets for
	// a feature that would answer 501.
	if !a.tts.Configured(ctx) {
		return ""
	}
	exp := time.Now().Add(speechTTL).Unix()
	sealed, err := secret.Encrypt(a.speechKey, []byte(speechTicket(sc, itemID, exp)))
	if err != nil {
		return ""
	}
	// QueryEscape because Encrypt returns standard base64, which contains `+`
	// and `/`. A raw `+` in a query string decodes to a space and the ticket
	// silently fails to open.
	return "/speech?t=" + url.QueryEscape(sealed)
}

// speechTicket is what gets sealed.
//
// The "speech" prefix is the same domain separation /p uses against /asset: a
// ciphertext minted for one purpose must not open as another if these keys ever
// get shared. The expiry is inside the sealed payload rather than beside it,
// which is the whole point — a client that could edit the expiry without
// invalidating the ticket would hold a permanent one.
func speechTicket(sc store.Scope, itemID string, exp int64) string {
	return "speech\n" + sc.TenantID + "\n" + sc.UserID + "\n" + sc.Role + "\n" +
		itemID + "\n" + strconv.FormatInt(exp, 10)
}

// openSpeechTicket returns the scope and item a ticket authorises.
//
// Every failure is the same error on purpose. A caller holding a corrupt,
// forged, expired or foreign ticket learns only that it did not work — telling
// them which would turn this into an oracle for probing what the key accepts.
func (a *App) openSpeechTicket(tok string) (store.Scope, string, error) {
	if len(a.speechKey) != 32 || tok == "" {
		return store.Scope{}, "", errBadTicket
	}
	raw, err := secret.Decrypt(a.speechKey, tok)
	if err != nil {
		return store.Scope{}, "", errBadTicket
	}
	parts := strings.Split(string(raw), "\n")
	if len(parts) != 6 || parts[0] != "speech" {
		return store.Scope{}, "", errBadTicket
	}
	exp, err := strconv.ParseInt(parts[5], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return store.Scope{}, "", errBadTicket
	}
	sc := store.Scope{TenantID: parts[1], UserID: parts[2], Role: parts[3]}
	if !sc.Valid() || parts[4] == "" {
		return store.Scope{}, "", errBadTicket
	}
	return sc, parts[4], nil
}

// errBadTicket is unexported and never reaches the reader: the handler turns it
// into a 403 with a fixed message.
var errBadTicket = errors.New("speech: ticket will not open")

// serveSpeech returns spoken audio for one item.
//
//	GET /speech?t=<sealed ticket>      — how the client actually calls it
//	GET /speech?item=<id>              — header-authenticated, for tools and tests
//
// A plain HTTPS endpoint rather than an RPC over the tunnel, because the client
// is an <audio> element: giving the browser a URL lets it stream, seek, buffer
// and cache the audio itself, none of which we would get for free by shipping
// megabytes of MP3 through a WebSocket and back out through a blob URL.
//
// That same fact is why the ticket exists. An <audio src> cannot send an
// Authorization header, so the header path below can only ever identify a
// caller through the DevMode fallback — which made this whole feature work on a
// laptop and answer 401 on any instance with real login. The ticket carries the
// scope instead, sealed, minted by GetItem for a reader who was already allowed
// to see the article. See SpeechURL.
//
// Four gates, in order, and each one is a different failure the reader needs
// told apart:
//
//  1. Authenticated — 401, or 403 for a ticket that will not open. Article
//     text is private.
//  2. Item visible to this scope — 404, never 403 (§20.7): a permission error
//     would confirm the item exists.
//  3. The instance has a key at all — 501. Nothing the reader can do.
//  4. This user opted in — 403. Something the reader CAN do, in settings.
func (a *App) serveSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}

	var (
		sc  store.Scope
		id  string
		err error
	)
	if tok := strings.TrimSpace(r.URL.Query().Get("t")); tok != "" {
		// The ticket names its own item. Deliberately not cross-checked against
		// an `item` parameter: honouring both would let a caller pair a valid
		// ticket with someone else's id, and the resulting ambiguity is exactly
		// the kind a capability is supposed to remove.
		sc, id, err = a.openSpeechTicket(tok)
		if err != nil {
			// 403 rather than 401: the caller presented a credential and it was
			// refused. 401 invites a client to retry with the login it already
			// has, which would loop.
			http.Error(w, "this listening link is not valid or has expired", http.StatusForbidden)
			return
		}
	} else {
		id = strings.TrimSpace(r.URL.Query().Get("item"))
		if id == "" || len(id) > 64 {
			http.Error(w, "bad item", http.StatusBadRequest)
			return
		}
		sc, err = a.speechScope(r)
		if err != nil {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
	}

	if a.tts == nil || !a.tts.Configured(r.Context()) {
		// 501 rather than 500: the server is working correctly and simply has no
		// key. "Not implemented on this instance" is exactly what that means.
		http.Error(w, "speech is not configured on this server", http.StatusNotImplemented)
		return
	}

	prefs, err := a.repo.GetPrefs(r.Context(), sc)
	if err != nil || prefs[ttsPrefKey] != "true" {
		http.Error(w, "Smart+ speech is off for this account", http.StatusForbidden)
		return
	}

	it, err := a.repo.GetItem(r.Context(), sc, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Two things to read aloud and they are different artifacts, not two
	// renderings of one. `cacheKey` carries the difference, because the audio
	// cache is keyed by it: without that, turning the digest on would serve the
	// full article from yesterday's cache, and turning it off would serve the
	// digest — each looking exactly like the toggle silently not working.
	text, cacheKey := speechText(it), it.ID
	if prefs[digestPrefKey] == "true" {
		if d, derr := a.digestFor(r.Context(), it); derr == nil {
			text, cacheKey = d, it.ID+"#digest"
		} else if errors.Is(derr, smart.ErrNothingToSummarise) {
			// Nothing to condense is not a failure — an item with two lines of
			// body IS its own summary. Read it.
			a.cfg.Log.Debug("digest skipped, reading the article", "item", id)
		} else {
			// A summariser that is down must not take listening down with it.
			// The reader asked to hear the article; they get the article, which
			// is longer than they wanted and infinitely better than silence.
			a.cfg.Log.Warn("digest failed, falling back to the full article",
				"item", id, "err", derr)
		}
	}
	if text == "" {
		http.Error(w, "nothing to read aloud", http.StatusUnprocessableEntity)
		return
	}

	voice := strings.TrimSpace(prefs["tts.voice"])
	audio, err := a.tts.Speak(r.Context(), cacheKey, text, prefs["tts.model"], voice)
	if err != nil {
		// The provider's own message can echo request content, and request
		// content here is the user's article — so it goes to the log and never
		// to the response (§22.11).
		a.cfg.Log.Warn("speech synthesis failed", "item", id, "err", err)
		http.Error(w, "couldn't synthesise that", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
	// Private, not public: this is one user's article read aloud, and a shared
	// cache holding it would serve it to whoever asked next.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(audio)
}

// speechScope resolves the caller for a plain HTTP request.
//
// The gRPC path reads its token from metadata; here it comes from the
// Authorization header, or from the dev account when the server is running
// without login. A query-string token is deliberately NOT accepted: this URL
// ends up in an <audio src>, which means the browser history, the referrer and
// every access log.
func (a *App) speechScope(r *http.Request) (store.Scope, error) {
	tok := strings.TrimSpace(r.Header.Get("Authorization"))
	if tok == "" {
		return a.devScope(r.Context())
	}
	if len(tok) > 7 && strings.EqualFold(tok[:7], "bearer ") {
		tok = tok[7:]
	}
	return a.scopeForToken(r.Context(), tok)
}

// Compiled once. These used to be built inside speechText, which recompiled
// five patterns per article — on a continuous session that is five compilations
// per track, for patterns that never change.
var (
	tagRE   = regexp.MustCompile(`(?is)<[^>]*>`)
	blockRE = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|blockquote|tr)>`)
	brRE    = regexp.MustCompile(`(?i)<br\s*/?>`)
	runRE   = regexp.MustCompile(`[ \t\r\f\v]+`)
	paraRE  = regexp.MustCompile(`\n\s*\n\s*`)
)

// speechText reduces an item to what should be read aloud.
//
// The title first, then the body, because a voice that starts mid-article gives
// the listener nothing to hang it on. Markup is stripped rather than sent: the
// provider bills per character, and `<div class="wp-block-group">` is neither
// speech nor cheap.
func speechText(it store.Item) string {
	body := speechBody(it)
	if intro := speechIntro(it); intro != "" {
		if body == "" {
			return intro
		}
		return intro + "\n\n" + body
	}
	return body
}

// speechIntro is the announcement that opens a segment.
//
// Source AND title, not the title alone, because of how this is actually used:
// a continuous session plays one article after another with no visual, and
// "Fsyncgate" arriving cold tells a listener what it is called but not where it
// came from — which is most of how anyone decides whether to keep listening.
// Naming the publication is the difference between a queue and a radio station.
//
// Written as a sentence with a full stop rather than a colon: a synthesiser
// reads a colon as a pause of no particular length, and "From Hacker News:
// Fsyncgate" comes out as one run-on. The full stop is what makes it land as a
// station identifier followed by a headline.
func speechIntro(it store.Item) string {
	title := strings.TrimSpace(it.Title)
	source := strings.TrimSpace(it.SourceTitle)
	switch {
	case title == "" && source == "":
		return ""
	case title == "":
		return "From " + source + "."
	case source == "":
		return title + "."
	default:
		return "From " + source + ". " + title + "."
	}
}

// speechBody is the article itself, with the markup taken out.
//
// Separate from speechText because the summariser wants exactly this and none
// of the announcement: a digest that has been fed "From Hacker News." will
// dutifully summarise the fact that it came from Hacker News.
func speechBody(it store.Item) string {
	body := it.ContentHTML
	if strings.TrimSpace(body) == "" {
		body = it.Summary
	}
	// Block-level tags become paragraph breaks first, so sentences do not run
	// into each other once the tags are gone.
	body = blockRE.ReplaceAllString(body, "\n\n")
	body = brRE.ReplaceAllString(body, "\n")
	body = tagRE.ReplaceAllString(body, " ")
	body = htmlUnescape(body)
	return strings.TrimSpace(collapseSpace(body))
}

// collapseSpace squeezes runs of whitespace, keeping paragraph breaks.
func collapseSpace(s string) string {
	return paraRE.ReplaceAllString(runRE.ReplaceAllString(s, " "), "\n\n")
}

// htmlUnescape resolves entities. Feeds are full of them, and a synthesiser
// reading "&amp;#8217;" aloud is unmistakable.
func htmlUnescape(s string) string { return html.UnescapeString(s) }
