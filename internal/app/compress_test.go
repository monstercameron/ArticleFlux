package app

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func TestNegotiateEncoding(t *testing.T) {
	for _, c := range []struct{ header, want string }{
		{"", ""},
		{"identity", ""},
		{"gzip", "gzip"},
		{"br", "br"},
		{"gzip, deflate, br", "br"},
		{"gzip, deflate, br, zstd", "br"},
		{"br;q=1.0, gzip;q=0.8", "br"},
		{"gzip;q=1.0, br;q=0.1", "br"}, // acceptable is enough: the server prefers br
		{"gzip, br;q=0", "gzip"},
		{"gzip, br;q=0.0", "gzip"},
		{"gzip, br;q=0.000", "gzip"},
		{"gzip, br; q=0", "gzip"},
		{"br;q=0, gzip;q=0", ""},
		{"br; q=0.5", "br"},
		{"BR, GZIP", "br"},
		{"x-gzip", "gzip"},
		{"*", "br"},
		{"*;q=0.5", "br"},
		{"*, br;q=0", "gzip"},
		{"*;q=0", ""},
		{"*;q=0, gzip", "gzip"},
		{"deflate", ""},
		{"gzip;q=bogus", ""},
		{"gzip;q=2", ""},
		{"identity;q=0", ""}, // nothing else offered: identity anyway, never a 406
		{" , ,gzip", "gzip"},
	} {
		if got := negotiateEncoding(c.header); got != c.want {
			t.Errorf("negotiateEncoding(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}

func decode(t *testing.T, enc string, body []byte) []byte {
	t.Helper()
	var r io.Reader
	switch enc {
	case "br":
		r = brotli.NewReader(bytes.NewReader(body))
	case "gzip":
		z, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("gzip: %v", err)
		}
		r = z
	case "":
		return body
	default:
		t.Fatalf("unexpected Content-Encoding %q", enc)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("%s decode: %v", enc, err)
	}
	return out
}

// compressRoot is a web root with a large enough wasm, js and html for the
// on-the-fly path, and helpers to add siblings with a chosen mtime.
func compressRoot(t *testing.T) (string, map[string][]byte) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{
		"app.wasm":   append([]byte("\x00asm"), bytes.Repeat([]byte("module bytes "), 4000)...),
		"app.js":     []byte(strings.Repeat("console.log('hello');\n", 300)),
		"index.html": []byte("<!doctype html><title>x</title>" + strings.Repeat("<p>shell</p>", 300)),
		"tiny.js":    []byte("x()"),
		"logo.png":   bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 1000),
	}
	old := time.Now().Add(-time.Hour)
	for name, b := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return dir, files
}

func writeSibling(t *testing.T, dir, name, enc string, body []byte, mtime time.Time) []byte {
	t.Helper()
	var b bytes.Buffer
	var w io.WriteCloser
	if enc == "br" {
		w = brotli.NewWriterLevel(&b, 11)
	} else {
		w, _ = gzip.NewWriterLevel(&b, gzip.BestCompression)
	}
	_, _ = w.Write(body)
	_ = w.Close()
	ext := ".gz"
	if enc == "br" {
		ext = ".br"
	}
	p := filepath.Join(dir, name+ext)
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fetchEnc(h http.Handler, method, path, ae string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if ae != "" {
		r.Header.Set("Accept-Encoding", ae)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func hasVary(h http.Header) bool {
	for _, v := range h.Values("Vary") {
		if strings.Contains(strings.ToLower(v), "accept-encoding") {
			return true
		}
	}
	return false
}

// Every path a static file can take, for each encoding: the bytes decode to
// the original, the type is the original's, the length is what was sent, and
// Vary is present on the compressed AND the identity answer.
func TestStaticCompressionRoundTrips(t *testing.T) {
	dir, files := compressRoot(t)
	writeSibling(t, dir, "app.wasm", "br", files["app.wasm"], time.Now())
	writeSibling(t, dir, "app.wasm", "gzip", files["app.wasm"], time.Now())
	a := &App{cfg: Config{WebRoot: dir}}
	h := compressHandler(a.static(dir))

	for _, tc := range []struct {
		path, file, ae, wantEnc, wantType string
	}{
		{"/app.wasm", "app.wasm", "gzip, deflate, br", "br", "application/wasm"}, // br sibling
		{"/app.wasm", "app.wasm", "gzip", "gzip", "application/wasm"},            // gz sibling
		{"/app.js", "app.js", "br", "br", "text/javascript; charset=utf-8"},      // on the fly
		{"/app.js", "app.js", "br", "br", "text/javascript; charset=utf-8"},      // cached
		{"/app.js", "app.js", "gzip", "gzip", "text/javascript; charset=utf-8"},  // on the fly
		{"/", "index.html", "gzip, br", "br", "text/html; charset=utf-8"},        // the shell
		{"/feed/123", "index.html", "gzip", "gzip", "text/html; charset=utf-8"},  // deep link
		{"/app.wasm", "app.wasm", "", "", "application/wasm"},                    // identity
		{"/app.js", "app.js", "br;q=0, gzip;q=0", "", "text/javascript; charset=utf-8"},
		{"/tiny.js", "tiny.js", "br", "", "text/javascript; charset=utf-8"}, // under the floor
	} {
		t.Run(tc.path+" "+tc.ae, func(t *testing.T) {
			rec := fetchEnc(h, http.MethodGet, tc.path, tc.ae)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			enc := rec.Header().Get("Content-Encoding")
			if enc != tc.wantEnc {
				t.Fatalf("Content-Encoding %q, want %q", enc, tc.wantEnc)
			}
			if got := rec.Header().Get("Content-Type"); got != tc.wantType {
				t.Errorf("Content-Type %q, want %q (the original's, not the sibling's)", got, tc.wantType)
			}
			if !hasVary(rec.Header()) {
				t.Error("no Vary: Accept-Encoding")
			}
			if cl := rec.Header().Get("Content-Length"); cl != "" && cl != strconv.Itoa(rec.Body.Len()) {
				t.Errorf("Content-Length %s but %d bytes sent", cl, rec.Body.Len())
			}
			if got := decode(t, enc, rec.Body.Bytes()); !bytes.Equal(got, files[tc.file]) {
				t.Errorf("decoded %d bytes, original is %d — not the same file", len(got), len(files[tc.file]))
			}
		})
	}
	if a.encoded.size() == 0 {
		t.Error("nothing was cached by the on-the-fly path")
	}
}

func TestStaticRefusesAStaleOrEmptySibling(t *testing.T) {
	dir, files := compressRoot(t)
	// A sibling from BEFORE the original: last build's bytes.
	writeSibling(t, dir, "app.js", "br", []byte("previous build"), time.Now().Add(-2*time.Hour))
	if err := os.WriteFile(filepath.Join(dir, "app.js.gz"), nil, 0o644); err != nil { // never finished
		t.Fatal(err)
	}
	a := &App{cfg: Config{WebRoot: dir}}
	h := a.static(dir)
	for _, ae := range []string{"br", "gzip"} {
		rec := fetchEnc(h, http.MethodGet, "/app.js", ae)
		if got := decode(t, rec.Header().Get("Content-Encoding"), rec.Body.Bytes()); !bytes.Equal(got, files["app.js"]) {
			t.Errorf("%s: served %q — the stale sibling won over a newer original", ae, got[:min(len(got), 20)])
		}
	}
}

func TestStaticRangeAndHead(t *testing.T) {
	dir, files := compressRoot(t)
	writeSibling(t, dir, "app.wasm", "br", files["app.wasm"], time.Now())
	a := &App{cfg: Config{WebRoot: dir}}
	h := compressHandler(a.static(dir))

	// Range: identity, over the real bytes.
	rec := fetchEnc(h, http.MethodGet, "/app.wasm", "br, gzip", "Range", "bytes=0-9")
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("Range: status %d encoding %q, want 206 identity", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(rec.Body.Bytes(), files["app.wasm"][:10]) {
		t.Errorf("Range body %q", rec.Body.Bytes())
	}
	if !hasVary(rec.Header()) {
		t.Error("Range response without Vary")
	}

	// HEAD: the headers a GET would send, and no body.
	for _, path := range []string{"/app.wasm", "/app.js"} {
		g := fetchEnc(h, http.MethodGet, path, "br")
		hd := fetchEnc(h, http.MethodHead, path, "br")
		for _, k := range []string{"Content-Encoding", "Content-Type", "Content-Length", "Vary", "Last-Modified"} {
			if g.Header().Get(k) != hd.Header().Get(k) {
				t.Errorf("%s %s: GET %q, HEAD %q", path, k, g.Header().Get(k), hd.Header().Get(k))
			}
		}
		if hd.Body.Len() != 0 {
			t.Errorf("%s: HEAD sent a body", path)
		}
	}

	// Conditional: Last-Modified is the original's, so If-Modified-Since works
	// the same way whichever encoding answered.
	g := fetchEnc(h, http.MethodGet, "/app.wasm", "br")
	nm := fetchEnc(h, http.MethodGet, "/app.wasm", "br", "If-Modified-Since", g.Header().Get("Last-Modified"))
	if nm.Code != http.StatusNotModified {
		t.Errorf("If-Modified-Since: %d, want 304", nm.Code)
	}
}

func TestEncodedCacheIsBoundedAndComputesOnce(t *testing.T) {
	c := &encodedCache{budget: 10_000}
	for i := 0; i < 50; i++ {
		k := encodedKey{path: fmt.Sprint(i), enc: "br"}
		if _, err := c.get(k, func() ([]byte, error) { return make([]byte, 1000), nil }); err != nil {
			t.Fatal(err)
		}
		if c.size() > 10_000 {
			t.Fatalf("cache holds %d bytes, budget 10000", c.size())
		}
	}
	// Larger than half the budget: served, not kept.
	big, _ := c.get(encodedKey{path: "big"}, func() ([]byte, error) { return make([]byte, 6000), nil })
	if len(big) != 6000 || c.size() > 10_000 {
		t.Errorf("big entry: %d bytes back, cache %d", len(big), c.size())
	}

	var builds atomic.Int32
	var wg sync.WaitGroup
	gate := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.get(encodedKey{path: "shared"}, func() ([]byte, error) {
				builds.Add(1)
				<-gate
				return []byte("x"), nil
			})
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()
	if n := builds.Load(); n != 1 {
		t.Errorf("16 concurrent first requests compressed %d times, want 1", n)
	}
}

func TestCompressHandlerDynamic(t *testing.T) {
	big := strings.Repeat("<p>hello reader</p>", 200)
	for _, tc := range []struct {
		name    string
		ae      string
		handler http.HandlerFunc
		wantEnc string
		vary    bool
	}{
		{"html", "gzip, br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, big)
		}, "br", true},
		{"sniffed type", "gzip", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<!doctype html>"+big)
		}, "gzip", true},
		{"json in small writes", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			for i := 0; i < 300; i++ {
				_, _ = io.WriteString(w, `{"a":1},`)
			}
		}, "br", true},
		{"under the floor", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}, "", true},
		{"image", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(bytes.Repeat([]byte{1}, 5000))
		}, "", false},
		{"woff2", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "font/woff2")
			_, _ = w.Write(bytes.Repeat([]byte{1}, 5000))
		}, "", false},
		{"already encoded", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = io.WriteString(w, big)
		}, "gzip", false},
		{"no-transform", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Cache-Control", "no-transform")
			_, _ = io.WriteString(w, big)
		}, "", false},
		{"error body", "br", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, strings.Repeat("something went wrong ", 100), http.StatusInternalServerError)
		}, "br", true},
		{"not modified", "br", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotModified)
		}, "", false},
		{"identity client", "", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, big)
		}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			ref := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.ae != "" {
				req.Header.Set("Accept-Encoding", tc.ae)
			}
			compressHandler(tc.handler).ServeHTTP(rec, req)
			tc.handler(ref, httptest.NewRequest(http.MethodGet, "/x", nil))

			enc := rec.Header().Get("Content-Encoding")
			if enc != tc.wantEnc {
				t.Fatalf("Content-Encoding %q, want %q", enc, tc.wantEnc)
			}
			if rec.Code != ref.Code {
				t.Errorf("status %d, the handler wrote %d", rec.Code, ref.Code)
			}
			if hasVary(rec.Header()) != tc.vary {
				t.Errorf("Vary present = %v, want %v", hasVary(rec.Header()), tc.vary)
			}
			body := rec.Body.Bytes()
			if enc != "" && enc != ref.Header().Get("Content-Encoding") {
				body = decode(t, enc, body)
			}
			if !bytes.Equal(body, ref.Body.Bytes()) {
				t.Errorf("body differs from the handler's own output (%d vs %d bytes)", len(body), ref.Body.Len())
			}
		})
	}
}

func TestCompressHandlerExclusionsAndValidators(t *testing.T) {
	big := strings.Repeat("<a href=\"/p?s=signature\">link</a>", 100)
	h := compressHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", `"abc123"`)
		if strings.Contains(r.Header.Get("If-None-Match"), `"abc123"`) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = io.WriteString(w, big)
	}))
	for _, p := range []string{"/p", "/asset"} {
		if rec := fetchEnc(h, http.MethodGet, p, "br, gzip"); rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s was compressed; it carries signed URLs beside attacker-controlled HTML", p)
		}
	}
	rec := fetchEnc(h, http.MethodGet, "/pub/x", "br")
	if rec.Header().Get("Content-Encoding") != "br" || rec.Header().Get("ETag") != `W/"abc123"` {
		t.Fatalf("encoding %q ETag %q, want br and the weakened validator",
			rec.Header().Get("Content-Encoding"), rec.Header().Get("ETag"))
	}
	// The weakened validator sent back still earns the 304.
	if nm := fetchEnc(h, http.MethodGet, "/pub/x", "br", "If-None-Match", rec.Header().Get("ETag")); nm.Code != http.StatusNotModified {
		t.Errorf("If-None-Match with the weak ETag: %d, want 304", nm.Code)
	}
	// Identity keeps the strong one.
	if id := fetchEnc(h, http.MethodGet, "/pub/x", ""); id.Header().Get("ETag") != `"abc123"` {
		t.Errorf("identity ETag %q", id.Header().Get("ETag"))
	}
}

// A handler that flushes is streaming: its first bytes must reach the client
// before the handler finishes, compressor or no compressor.
func TestCompressHandlerDoesNotHoldBackAStream(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(compressHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "first chunk\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "second chunk\n")
	})))
	defer srv.Close()
	defer close(release)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept-Encoding", "br, gzip")
	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	line := make(chan string, 1)
	go func() {
		l, _ := bufio.NewReader(resp.Body).ReadString('\n')
		line <- l
	}()
	select {
	case l := <-line:
		if l != "first chunk\n" {
			t.Errorf("first line %q (encoding %q)", l, resp.Header.Get("Content-Encoding"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the flushed chunk never arrived: the middleware buffered a stream")
	}
}

// The tunnel's WebSocket upgrade hijacks the connection. It must still be able
// to, with the middleware in the chain — both for an Upgrade request (not
// wrapped at all) and for a handler that hijacks without one.
func TestCompressHandlerLetsAHandlerHijack(t *testing.T) {
	for _, upgrade := range []bool{true, false} {
		srv := httptest.NewServer(compressHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			conn, rw, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: test\r\nConnection: Upgrade\r\n\r\nhello")
			_ = rw.Flush()
		})))
		conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		hdr := "GET / HTTP/1.1\r\nHost: x\r\nAccept-Encoding: br\r\n"
		if upgrade {
			hdr += "Connection: Upgrade\r\nUpgrade: test\r\n"
		}
		_, _ = io.WriteString(conn, hdr+"\r\n")
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		got, _ := io.ReadAll(conn)
		if !bytes.HasPrefix(got, []byte("HTTP/1.1 101")) || !bytes.HasSuffix(got, []byte("hello")) {
			t.Errorf("upgrade=%v: got %q", upgrade, got)
		}
		_ = conn.Close()
		srv.Close()
	}
}
