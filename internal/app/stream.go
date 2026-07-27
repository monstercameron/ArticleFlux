package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/render"
	"github.com/monstercameron/ArticleFlux/internal/secret"
)

// streamTTL is how long a minted stream capability stays valid.
//
// The shortest of the three. An asset URL has to survive an article left open
// over lunch and a page URL has to survive a click; this one is spent the
// instant it is handed out, because the `<img>` that uses it connects
// immediately or not at all.
const streamTTL = 30 * time.Minute

// mjpegBoundary separates frames. Any token works as long as it cannot occur in
// the payload; this one is ours and obvious in a packet capture.
const mjpegBoundary = "articlefluxframe"

// serveStream streams a live browser view of a page as MJPEG (§10.1d).
//
//	GET /stream?u=<base64url(url)>&e=<unix expiry>&s=<signature>
//
// # Why multipart/x-mixed-replace rather than a bidi RPC
//
// §10.1d specified frames over the gRPC tunnel, and for an interactive remote
// browser that is still right — input has to go back up somewhere. For a view-
// only stream it is a great deal of machinery to rebuild something the browser
// already does: `multipart/x-mixed-replace` in an `<img>` is decoded natively,
// frame by frame, with no client code at all. No proto change, no wasm
// streaming, no canvas compositor, no tile format.
//
// What that costs is real and worth stating: no input channel, so this is
// look-don't-touch, and whole frames rather than the tile diff §10.1d wants.
// Both are additive later; neither blocks the toggle this exists to serve.
//
// # The connection IS the session
//
// There is no session table, no id, and nothing to clean up on a timer. The
// browser tab lives exactly as long as the HTTP response: when the reader
// switches away or closes the tab, the `<img>` disconnects, the request context
// cancels, and the tab dies with it. A stream that outlived its viewer would be
// a browser nobody is watching, which on a home box is the expensive kind of
// leak.
func (a *App) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if a.renderer == nil {
		http.Error(w, "live view is not configured on this server", http.StatusNotImplemented)
		return
	}

	q := r.URL.Query()
	raw, err := base64.RawURLEncoding.DecodeString(q.Get("u"))
	if err != nil || len(raw) == 0 {
		http.Error(w, "bad url", http.StatusBadRequest)
		return
	}
	exp, err := strconv.ParseInt(q.Get("e"), 10, 64)
	if err != nil {
		http.Error(w, "bad expiry", http.StatusBadRequest)
		return
	}
	if !secret.VerifySignature(a.assetKey, streamMessage(string(raw), exp), q.Get("s")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if time.Now().Unix() > exp {
		http.Error(w, "this link has expired", http.StatusGone)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without flushing, every frame buffers and the reader gets one image
		// at the end of the stream, which is a screenshot with extra steps.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "multipart/x-mixed-replace; boundary="+mjpegBoundary)
	h.Set("Cache-Control", "no-store, private")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Connection", "close")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// The request context is the session's lifetime: it cancels when the client
	// disconnects, which is what makes the browser tab go away.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	frames := make(chan render.Frame, 2)
	errc := make(chan error, 1)
	go func() { errc <- a.renderer.Stream(ctx, string(raw), frames) }()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errc:
			if err != nil && !errors.Is(err, context.Canceled) {
				a.log.Warn("live view ended", "err", err)
			}
			return
		case f := <-frames:
			if _, werr := fmt.Fprintf(w,
				"--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n",
				mjpegBoundary, len(f.JPEG)); werr != nil {
				return
			}
			if _, werr := w.Write(f.JPEG); werr != nil {
				return
			}
			if _, werr := w.Write([]byte("\r\n")); werr != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// StreamURL mints a capability for a live view of one page.
func (a *App) StreamURL(rawURL string) string {
	if a.renderer == nil || rawURL == "" {
		return ""
	}
	if strings.HasPrefix(rawURL, a.streamPrefix()) {
		return ""
	}
	exp := time.Now().Add(streamTTL).Unix()
	return a.streamPrefix() +
		"?u=" + base64.RawURLEncoding.EncodeToString([]byte(rawURL)) +
		"&e=" + strconv.FormatInt(exp, 10) +
		"&s=" + url.QueryEscape(secret.Sign(a.assetKey, streamMessage(rawURL, exp)))
}

func (a *App) streamPrefix() string {
	if a.cfg.ProxyOrigin != "" {
		return strings.TrimRight(a.cfg.ProxyOrigin, "/") + "/stream"
	}
	return "/stream"
}

// streamMessage is what gets signed. Third prefix, same reason as the first
// two: a capability for one rung must not be spendable on another.
func streamMessage(rawURL string, exp int64) string {
	return "stream\n" + rawURL + "\n" + strconv.FormatInt(exp, 10)
}
