package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
	"google.golang.org/grpc"
	// Registers the gzip codec, so the server can answer in it — and only
	// when grpcCompressUnary asks it to.
	grpcgzip "google.golang.org/grpc/encoding/gzip"

	"github.com/monstercameron/ArticleFlux/internal/webasset"
)

// Server-side compression: content negotiation, the on-the-fly fallback for
// static files with no precompressed sibling, and one middleware for dynamic
// responses.
//
// # Measured on this build (bin/web, 2026-11-19)
//
//	app.wasm   33,455,770 raw   gzip(default) 7,368,667 in 172ms   br5 5,874,729 in 626ms
//	                            br4 6,528,682 in 595ms   br6 5,804,805 in 1.01s
//	index.html     26,212 raw   gzip 10,720   br5 9,975   (br11, precompressed: 8,743)
//
// Brotli 5 rather than 4 for the on-the-fly path: 10% smaller on the bundle
// for 5% more time, while 6 buys 1% more for 60% more time. 11 is what
// cmd/precompress spends at build time and is far too slow to spend on a
// request. Gzip at the default level, because level 9 took 1.9s for a 4%
// gain on the bundle.

// encBrotli and encGzip are the Content-Encoding tokens this server produces.
const (
	encBrotli = "br"
	encGzip   = "gzip"
)

// brotliLiveLevel is the brotli quality for compression done at request time.
const brotliLiveLevel = 5

// negotiateEncoding picks the response encoding for an Accept-Encoding header:
// "br", "gzip", or "" for identity.
//
// Parsed as RFC 9110 §12.5.3 tokens with q-values, because every shortcut gets
// a real header wrong: a substring test serves brotli to `br;q=0` (an explicit
// refusal), a prefix test on "q=0" accepts `q=0.0` and `q=0.000`, and ignoring
// `*` refuses brotli to a client that said it takes anything.
//
// Among acceptable encodings the server's own preference wins — brotli, then
// gzip — rather than the client's q ranking: the q-values say what the client
// CAN decode, and brotli is ~20% smaller than gzip on everything measured here.
// An explicit token overrides `*` for that coding, so `*, br;q=0` means gzip.
//
// Identity is always the fallback, even when the client wrote `identity;q=0`:
// the alternative is a 406, and no browser sends that header.
func negotiateEncoding(header string) string {
	if header == "" {
		return ""
	}
	var (
		qs       = map[string]float64{}
		wildcard = -1.0
	)
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(part, ";")
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "q") {
				continue
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil || f < 0 || f > 1 {
				// A malformed weight is not a reason to guess in the client's
				// favour; treat the coding as not offered.
				f = 0
			}
			q = f
		}
		if token == "x-gzip" {
			token = encGzip
		}
		if token == "*" {
			wildcard = q
			continue
		}
		qs[token] = q
	}
	for _, enc := range []string{encBrotli, encGzip} {
		q, named := qs[enc]
		if !named {
			q = wildcard
		}
		if q > 0 {
			return enc
		}
	}
	return ""
}

// addVary adds Accept-Encoding to Vary unless it is already listed. Sent on
// every response whose bytes depend on the header — compressed AND identity —
// or a shared cache may store the identity copy and hand it to everybody, or
// the brotli copy and hand it to a client that cannot read it.
func addVary(h http.Header) {
	for _, v := range h.Values("Vary") {
		for _, f := range strings.Split(v, ",") {
			f = strings.TrimSpace(f)
			if f == "*" || strings.EqualFold(f, "Accept-Encoding") {
				return
			}
		}
	}
	h.Add("Vary", "Accept-Encoding")
}

// compressibleType is the allowlist of media types worth compressing.
//
// An allowlist, for webasset.Compressible's reason: an unknown type defaults to
// being left alone. Images, audio, video, woff/woff2 and archives are already
// compressed, and recompressing them costs CPU to make them larger.
// text/event-stream is text but a stream; see compressHandler.
func compressibleType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch {
	case mt == "text/event-stream":
		return false
	case strings.HasPrefix(mt, "text/"):
		return true
	case strings.HasSuffix(mt, "+json"), strings.HasSuffix(mt, "+xml"):
		// application/manifest+json, image/svg+xml, application/atom+xml, …
		return true
	}
	switch mt {
	case "application/json", "application/javascript", "application/x-javascript",
		"application/wasm", "application/xml", "application/x-ndjson",
		"font/ttf", "font/otf", "application/x-font-ttf", "application/vnd.ms-fontobject":
		return true
	}
	return false
}

// newEncoder wraps w in a compressor for enc.
func newEncoder(w io.Writer, enc string, brLevel int) io.WriteCloser {
	if enc == encBrotli {
		return brotli.NewWriterLevel(w, brLevel)
	}
	z, _ := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	return z
}

// ---------------------------------------------------------------------------
// The gRPC tunnel
// ---------------------------------------------------------------------------

// grpcCompressedMethods are the RPCs whose responses are compressed when the
// client says it can decompress them.
//
// # Why these, measured
//
// A 200-item ListItems page (store.MaxLimit) built from the development
// database's real rows is 74,854 bytes of protobuf and 28,945 gzipped — 61%
// off the response a reader waits on most, for about a millisecond of server
// CPU. The tunnel carries nothing else of that size: the WebSocket is not
// negotiating permessage-deflate (GoGRPCBridge's WithBridgeWebSocketCompression
// is off), so before this every byte of every list went over the wire as it
// was marshalled.
//
// # Why an allowlist rather than every response
//
// BREACH. A compressed message whose length an observer can see, and which
// holds both a secret and text an attacker can influence, leaks the secret a
// guess at a time. The session credential never rides in a body — it is
// `authorization` metadata on every call (client/data/auth.go), sent as
// HTTP/2 headers inside the tunnel — but some RESPONSES carry secrets: Login
// and the session RPCs return a token, and GetItem returns signed proxy URLs
// next to publisher-controlled article HTML. The methods here return list rows
// — titles, summaries, URLs, counts — with no credential, no signed URL and no
// per-session value in them, so there is nothing for a length side channel to
// find. A new method joins this list only after the same check.
var grpcCompressedMethods = map[string]bool{
	"/articleflux.v1.ReaderService/ListItems": true,
	"/articleflux.v1.ReaderService/ListFeeds": true,
	"/articleflux.v1.ReaderService/Search":    true,
	"/articleflux.v1.ReaderService/ListNotes": true,
}

// grpcCompressUnary turns on gzip for an allowlisted response when, and only
// when, the client advertised it in grpc-accept-encoding.
//
// Negotiated, never required, in both directions: a client that does not list
// gzip — every client cached before this build — gets identity exactly as
// before, and a new client talking to an old server is never sent gzip because
// the old server never sets a compressor. Requests are not compressed at all;
// they are small, and a request compressed to a server without the decoder is
// the one direction that would fail.
func grpcCompressUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if grpcCompressedMethods[info.FullMethod] {
			if names, err := grpc.ClientSupportedCompressors(ctx); err == nil {
				for _, n := range names {
					if n == grpcgzip.Name {
						_ = grpc.SetSendCompressor(ctx, grpcgzip.Name)
						break
					}
				}
			}
		}
		return handler(ctx, req)
	}
}

// ---------------------------------------------------------------------------
// Static files: sibling, else compressed once and cached
// ---------------------------------------------------------------------------

// freshSibling reports whether the precompressed sibling at sibPath may stand
// in for orig.
//
// A sibling older than its original is last build's bytes under this build's
// name — the build script's own history has a stale .gz making the browser
// run the previous client with nothing to say so — and an empty one is a
// write that never finished. Either is refused, and the caller compresses the
// current original instead, which costs CPU and is never wrong.
func freshSibling(orig os.FileInfo, sibPath string) bool {
	sib, err := os.Stat(sibPath)
	if err != nil || sib.IsDir() || sib.Size() == 0 {
		return false
	}
	return !sib.ModTime().Before(orig.ModTime())
}

// encodedKey identifies one compressed rendering of one version of a file.
// mtime AND size, because a rebuild within the filesystem's timestamp
// resolution changes the size if it changes anything worth noticing.
type encodedKey struct {
	path  string
	mtime int64
	size  int64
	enc   string
}

// encodedCache holds compressed renderings of static files that have no
// precompressed sibling — the dev loop, a hand-copied web root, the desktop
// edition, any file added after the build.
//
// # The bound
//
// maxEncodedBytes of compressed output, least recently used out first. 64 MB
// holds both renderings of the 33 MB bundle (5.9 MB brotli + 7.4 MB gzip) four
// times over — so a rebuild, which changes the key, can sit beside the
// previous build's entries until they age out — plus every other file in the
// web root, which together compress to well under a megabyte. It is a ceiling,
// not a reservation: an instance with precompressed siblings never fills it.
// A rendering larger than half the budget is served but not kept, so one
// enormous file cannot flush everything else.
//
// # Why compute-once matters
//
// Compressing the bundle takes ~0.6s (brotli 5) on this machine. Per request
// that is a CPU second per visitor; once per build it is nothing. Concurrent
// first requests for the same key wait for one compression rather than each
// starting their own.
//
// The zero value is ready to use, because tests build App by literal.
type encodedCache struct {
	mu       sync.Mutex
	entries  map[encodedKey]*list.Element
	lru      list.List // of *encodedEntry, front = most recent
	used     int64
	inflight map[encodedKey]*encodedCall
	budget   int64 // 0 means maxEncodedBytes; tests set it smaller
}

const maxEncodedBytes = 64 << 20

type encodedEntry struct {
	key  encodedKey
	data []byte
}

type encodedCall struct {
	done chan struct{}
	data []byte
	err  error
}

func (c *encodedCache) limit() int64 {
	if c.budget > 0 {
		return c.budget
	}
	return maxEncodedBytes
}

// get returns the compressed bytes for k, producing them with build at most
// once per key however many callers arrive together.
func (c *encodedCache) get(k encodedKey, build func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[encodedKey]*list.Element{}
		c.inflight = map[encodedKey]*encodedCall{}
	}
	if el, ok := c.entries[k]; ok {
		c.lru.MoveToFront(el)
		data := el.Value.(*encodedEntry).data
		c.mu.Unlock()
		return data, nil
	}
	if call, ok := c.inflight[k]; ok {
		c.mu.Unlock()
		<-call.done
		return call.data, call.err
	}
	call := &encodedCall{done: make(chan struct{})}
	c.inflight[k] = call
	c.mu.Unlock()

	call.data, call.err = build()

	c.mu.Lock()
	delete(c.inflight, k)
	if call.err == nil && int64(len(call.data)) <= c.limit()/2 {
		c.entries[k] = c.lru.PushFront(&encodedEntry{key: k, data: call.data})
		c.used += int64(len(call.data))
		for c.used > c.limit() {
			oldest := c.lru.Back()
			e := oldest.Value.(*encodedEntry)
			c.lru.Remove(oldest)
			delete(c.entries, e.key)
			c.used -= int64(len(e.data))
		}
	}
	c.mu.Unlock()
	close(call.done)
	return call.data, call.err
}

// size reports the bytes held, for tests.
func (c *encodedCache) size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}

func compressFile(path, enc string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	w := newEncoder(&buf, enc, brotliLiveLevel)
	if _, err := io.Copy(w, f); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// serveEncodedStatic serves the file at full (URL path served) in the
// negotiated encoding, from a fresh sibling or the cache. It returns false —
// having written nothing — when the caller should serve identity instead.
//
// Range requests are always identity. A byte range of a brotli stream is
// meaningless to anything but a cache that stores the whole encoded
// representation, and browsers only send Range for media; the simple correct
// answer is to range over the real bytes. HEAD goes through the same path as
// GET, so its headers — Content-Encoding, Content-Length of what a GET would
// send — match.
func (a *App) serveEncodedStatic(w http.ResponseWriter, r *http.Request, full, served string) bool {
	if !webasset.Compressible(served) {
		return false
	}
	// From here the bytes depend on Accept-Encoding, identity included.
	addVary(w.Header())
	if r.Header.Get("Range") != "" {
		return false
	}
	enc := negotiateEncoding(r.Header.Get("Accept-Encoding"))
	if enc == "" {
		return false
	}
	orig, err := os.Stat(full)
	if err != nil || orig.IsDir() {
		return false
	}
	if w.Header().Get("Content-Type") == "" {
		if ct := mime.TypeByExtension(strings.ToLower(extOf(served))); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}

	for _, e := range webasset.Encodings {
		if e.Name != enc {
			continue
		}
		if sib := full + e.Ext; freshSibling(orig, sib) {
			f, err := os.Open(sib)
			if err == nil {
				defer func() { _ = f.Close() }()
				w.Header().Set("Content-Encoding", enc)
				// Last-Modified of the ORIGINAL: it is the representation's
				// version, and conditional requests should not see a different
				// date per encoding of the same build.
				http.ServeContent(w, r, served, orig.ModTime(), f)
				return true
			}
		}
	}

	// Below the floor a compressed copy is not worth making (webasset.MinSize);
	// a sibling, above, is used whatever its size because somebody already
	// paid for it.
	if orig.Size() < webasset.MinSize {
		w.Header().Del("Content-Type")
		return false
	}
	data, err := a.encoded.get(encodedKey{full, orig.ModTime().UnixNano(), orig.Size(), enc},
		func() ([]byte, error) { return compressFile(full, enc) })
	if err != nil {
		w.Header().Del("Content-Type")
		return false
	}
	w.Header().Set("Content-Encoding", enc)
	http.ServeContent(w, r, served, orig.ModTime(), bytes.NewReader(data))
	return true
}

func extOf(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && !strings.ContainsAny(p[i:], `/\`) {
		return p[i:]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Dynamic responses
// ---------------------------------------------------------------------------

// compressHandler compresses dynamic responses: the HTML shell, the share
// pages, the proxies, error bodies, /metrics — anything whose Content-Type is
// on compressibleType's allowlist and whose body reaches webasset.MinSize.
//
// What it never touches, and why:
//
//   - An Upgrade request (the gRPC tunnel's WebSocket). It is not a response
//     body at all, and the handler hijacks the connection; this does not even
//     wrap the writer for one.
//   - A response that already carries Content-Encoding (the static handler's
//     own siblings and cache, a proxied upstream that came compressed).
//   - A Range request, a 206, or anything with Content-Range — see
//     serveEncodedStatic.
//   - HEAD, so a HEAD never advertises an encoding its GET might not use.
//   - Cache-Control: no-transform, which is the sender asking exactly this.
//   - A stream. The decision waits for the first MinSize bytes; a handler that
//     Flushes before then gets identity, unbuffered, from that point on, so an
//     MJPEG or event stream is never held back. text/event-stream is off the
//     allowlist regardless. A compressed response that Flushes flushes the
//     encoder too, so the bytes reach the client.
//
// The writer keeps http.Flusher, http.Hijacker and http.Pusher (and Unwrap, so
// http.ResponseController reaches the real one).
func compressHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" || r.Method == http.MethodHead || r.Header.Get("Range") != "" ||
			breachExcluded[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		enc := negotiateEncoding(r.Header.Get("Accept-Encoding"))
		cw := &compressWriter{ResponseWriter: w, enc: enc}
		defer cw.finish()
		next.ServeHTTP(cw, r)
	})
}

// breachExcluded are the routes never compressed, whatever their type.
//
// /p and /asset are the page and image proxies. A proxied page is the
// publisher's HTML — content an attacker fully controls and can change on
// every request — with its links rewritten into URLs carrying this server's
// HMAC signatures (servePage passes a.PageURL and a.AssetURL to the
// rewriter). Secret-bearing bytes next to attacker-chosen bytes, in a body
// whose compressed length an on-path observer can measure, is the exact BREACH
// shape. Nothing else this server answers over HTTP qualifies: the session
// credential is never in an HTTP body or cookie (it is gRPC metadata inside
// the tunnel), the shell and static files are the same for everybody, and
// /pub/ reflects nothing from the request — its only secret is the slug the
// requester already sent in the URL.
var breachExcluded = map[string]bool{
	"/p":     true,
	"/asset": true,
}

type compressWriter struct {
	http.ResponseWriter
	enc string // negotiated; "" means identity whatever happens

	status      int
	wroteHeader bool // the handler called WriteHeader (or Write)
	decided     bool // headers have gone to the client
	hijacked    bool
	buf         []byte
	encoder     io.WriteCloser
}

func (c *compressWriter) WriteHeader(code int) {
	if c.wroteHeader || c.hijacked {
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		// Informational responses (103 Early Hints) go straight through and
		// do not count as the final header.
		c.ResponseWriter.WriteHeader(code)
		return
	}
	c.wroteHeader = true
	c.status = code
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if c.hijacked {
		return 0, http.ErrHijacked
	}
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	if c.decided {
		if c.encoder != nil {
			return c.encoder.Write(p)
		}
		return c.ResponseWriter.Write(p)
	}
	c.buf = append(c.buf, p...)
	if len(c.buf) >= webasset.MinSize {
		if err := c.decide(true); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// decide commits the headers, choosing compression only when enough is known:
// full is true when at least MinSize bytes are buffered (or the handler is
// done and the whole body is in hand).
func (c *compressWriter) decide(full bool) error {
	if c.decided {
		return nil
	}
	c.decided = true
	h := c.Header()
	if h.Get("Content-Type") == "" && len(c.buf) > 0 && h.Get("Content-Encoding") == "" {
		// What net/http would do on the first Write; done here because the
		// decision needs the type.
		h.Set("Content-Type", http.DetectContentType(c.buf))
	}
	eligible := compressibleType(h.Get("Content-Type")) &&
		h.Get("Content-Encoding") == "" && h.Get("Content-Range") == "" &&
		!strings.Contains(strings.ToLower(h.Get("Cache-Control")), "no-transform") &&
		c.status != http.StatusPartialContent && c.status != http.StatusNoContent &&
		c.status != http.StatusNotModified && c.status >= 200
	if eligible {
		addVary(h)
	}
	if eligible && full && c.enc != "" && len(c.buf) >= webasset.MinSize {
		h.Del("Content-Length")
		h.Set("Content-Encoding", c.enc)
		// A strong validator names exact bytes, and these are not the bytes
		// the handler hashed. Weakened, as nginx does: If-None-Match compares
		// weakly (RFC 9110 §13.1.2), so a poller sending W/"x" back still gets
		// the handler's 304 — servePublicShare matches by containment.
		if et := h.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
			h.Set("ETag", "W/"+et)
		}
		c.ResponseWriter.WriteHeader(c.status)
		c.encoder = newEncoder(c.ResponseWriter, c.enc, brotliLiveLevel)
		_, err := c.encoder.Write(c.buf)
		c.buf = nil
		return err
	}
	c.ResponseWriter.WriteHeader(c.status)
	var err error
	if len(c.buf) > 0 {
		_, err = c.ResponseWriter.Write(c.buf)
	}
	c.buf = nil
	return err
}

// finish runs when the handler returns.
func (c *compressWriter) finish() {
	if c.hijacked {
		return
	}
	if !c.wroteHeader {
		if len(c.buf) == 0 {
			// The handler wrote nothing at all; let net/http write its
			// implicit 200 exactly as it would have without this wrapper.
			return
		}
		c.WriteHeader(http.StatusOK)
	}
	if !c.decided {
		_ = c.decide(true)
	}
	if c.encoder != nil {
		_ = c.encoder.Close()
	}
}

// Flush sends what is buffered. Before the decision that means identity from
// here on — a handler that flushes is streaming, and a stream must not wait
// for a compressor's block to fill.
func (c *compressWriter) Flush() {
	if c.hijacked {
		return
	}
	if !c.decided {
		if !c.wroteHeader {
			c.WriteHeader(http.StatusOK)
		}
		_ = c.decide(false)
	}
	if f, ok := c.encoder.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := c.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("app: the underlying ResponseWriter cannot hijack")
	}
	if c.decided {
		return nil, nil, errors.New("app: cannot hijack after the response has started")
	}
	conn, rw, err := hj.Hijack()
	if err == nil {
		c.hijacked = true
	}
	return conn, rw, err
}

func (c *compressWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := c.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
