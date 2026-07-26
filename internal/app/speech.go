package app

import (
	"errors"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/monstercameron/Tidings/internal/store"
)

// ttsPrefKey is the per-user opt-in. Absent or anything but "true" means off.
//
// Default-off is the whole point: this endpoint sends the article you are
// reading to a third party, and a feature like that must never become active
// because of a default someone forgot to change.
const ttsPrefKey = "tts.smartPlus"

// serveSpeech returns spoken audio for one item.
//
//	GET /speech?item=<id>
//
// A plain HTTPS endpoint rather than an RPC over the tunnel, because the client
// is an <audio> element: giving the browser a URL lets it stream, seek, buffer
// and cache the audio itself, none of which we would get for free by shipping
// megabytes of MP3 through a WebSocket and back out through a blob URL.
//
// Four gates, in order, and each one is a different failure the reader needs
// told apart:
//
//  1. Authenticated — 401. Article text is private.
//  2. Item visible to this scope — 404, never 403 (§20.7): a permission error
//     would confirm the item exists.
//  3. The instance has a key at all — 501. Nothing the reader can do.
//  4. This user opted in — 403. Something the reader CAN do, in settings.
func (a *App) serveSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("item"))
	if id == "" || len(id) > 64 {
		http.Error(w, "bad item", http.StatusBadRequest)
		return
	}

	sc, err := a.speechScope(r)
	if err != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	if a.tts == nil || !a.tts.Configured() {
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

	text := speechText(it)
	if text == "" {
		http.Error(w, "nothing to read aloud", http.StatusUnprocessableEntity)
		return
	}

	voice := strings.TrimSpace(prefs["tts.voice"])
	audio, err := a.tts.Speak(r.Context(), it.ID, text, prefs["tts.model"], voice)
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

var tagRE = regexp.MustCompile(`(?is)<[^>]*>`)

// speechText reduces an item to what should be read aloud.
//
// The title first, then the body, because a voice that starts mid-article gives
// the listener nothing to hang it on. Markup is stripped rather than sent: the
// provider bills per character, and `<div class="wp-block-group">` is neither
// speech nor cheap.
func speechText(it store.Item) string {
	body := it.ContentHTML
	if strings.TrimSpace(body) == "" {
		body = it.Summary
	}
	// Block-level tags become paragraph breaks first, so sentences do not run
	// into each other once the tags are gone.
	body = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|blockquote|tr)>`).
		ReplaceAllString(body, "\n\n")
	body = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(body, "\n")
	body = tagRE.ReplaceAllString(body, " ")
	body = htmlUnescape(body)

	var b strings.Builder
	if t := strings.TrimSpace(it.Title); t != "" {
		b.WriteString(t)
		b.WriteString(".\n\n")
	}
	b.WriteString(strings.TrimSpace(collapseSpace(body)))
	return strings.TrimSpace(b.String())
}

// collapseSpace squeezes runs of whitespace, keeping paragraph breaks.
func collapseSpace(s string) string {
	s = regexp.MustCompile(`[ \t\r\f\v]+`).ReplaceAllString(s, " ")
	return regexp.MustCompile(`\n\s*\n\s*`).ReplaceAllString(s, "\n\n")
}

// htmlUnescape resolves entities. Feeds are full of them, and a synthesiser
// reading "&amp;#8217;" aloud is unmistakable.
func htmlUnescape(s string) string { return html.UnescapeString(s) }
