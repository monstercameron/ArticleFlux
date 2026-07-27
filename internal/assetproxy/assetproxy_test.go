package assetproxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// onePixelPNG is a real PNG, because the sniffing path is part of what is under
// test and a fake would sniff as octet-stream.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0, 0, 0, 0x0d, 'I', 'H', 'D', 'R',
	0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 0x1f, 0x15, 0xc4, 0x89,
	0, 0, 0, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0, 0, 0, 0, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

// newFetcher builds one pointed at a temp cache. AllowPrivate is on because
// httptest binds loopback, which the guard blocks by design — the guard itself
// is tested in netguard, not here.
func newFetcher(t *testing.T, opts ...func(*Options)) *Fetcher {
	t.Helper()
	opt := Options{Dir: t.TempDir(), AllowPrivate: true}
	for _, f := range opts {
		f(&opt)
	}
	return New(opt)
}

func TestFetchesAndCaches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(onePixelPNG)
	}))
	defer srv.Close()

	f := newFetcher(t)
	a, err := f.Get(context.Background(), srv.URL+"/pic.png")
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if a.ContentType != "image/png" {
		t.Errorf("content type = %q", a.ContentType)
	}
	if len(a.Bytes) != len(onePixelPNG) {
		t.Errorf("got %d bytes, want %d", len(a.Bytes), len(onePixelPNG))
	}
	if a.ETag == "" {
		t.Error("no etag")
	}

	b, err := f.Get(context.Background(), srv.URL+"/pic.png")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("origin was hit %d times; the cache did not serve the second read", hits.Load())
	}
	if b.ETag != a.ETag || string(b.Bytes) != string(a.Bytes) || b.ContentType != a.ContentType {
		t.Error("cached asset differs from the fetched one")
	}
}

// A CDN serving a real JPEG as application/octet-stream is common, and with
// nosniff on our response a passed-through octet-stream renders as nothing.
func TestGenericContentTypeIsSniffed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(onePixelPNG)
	}))
	defer srv.Close()

	a, err := newFetcher(t).Get(context.Background(), srv.URL+"/x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if a.ContentType != "image/png" {
		t.Errorf("content type = %q, want image/png", a.ContentType)
	}
}

func TestNonImageIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
	}))
	defer srv.Close()

	_, err := newFetcher(t).Get(context.Background(), srv.URL+"/page")
	if !errors.Is(err, ErrNotAnImage) {
		t.Fatalf("err = %v, want ErrNotAnImage", err)
	}
}

func TestSizeCapIsEnforcedWhileReading(t *testing.T) {
	// No Content-Length is set, so the only way to catch this is on the way in
	// — which is the point: Content-Length is a claim by the server we are
	// defending against.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		buf := make([]byte, 4096)
		for range 64 {
			_, _ = w.Write(buf)
		}
	}))
	defer srv.Close()

	f := newFetcher(t, func(o *Options) { o.MaxBytes = 16 << 10 })
	_, err := f.Get(context.Background(), srv.URL+"/big.png")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestExactlyAtTheCapSucceeds(t *testing.T) {
	const size = 4096
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, size))
	}))
	defer srv.Close()

	f := newFetcher(t, func(o *Options) { o.MaxBytes = size })
	if _, err := f.Get(context.Background(), srv.URL+"/edge.png"); err != nil {
		t.Fatalf("a body exactly at the cap must succeed, got %v", err)
	}
}

func TestUpstreamErrorIsNegativeCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer srv.Close()

	f := newFetcher(t)
	for range 3 {
		if _, err := f.Get(context.Background(), srv.URL+"/missing.png"); err == nil {
			t.Fatal("expected an error")
		}
	}
	if hits.Load() != 1 {
		t.Errorf("origin was hit %d times; a dead image must be asked for once, not once per read", hits.Load())
	}
}

func TestCacheSurvivesANewFetcher(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/webp")
		_, _ = w.Write(onePixelPNG)
	}))
	defer srv.Close()

	dir := t.TempDir()
	first := New(Options{Dir: dir, AllowPrivate: true})
	if _, err := first.Get(context.Background(), srv.URL+"/a.webp"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// A restart must not re-fetch: the cache lives beside the database
	// precisely so it survives one.
	second := New(Options{Dir: dir, AllowPrivate: true})
	a, err := second.Get(context.Background(), srv.URL+"/a.webp")
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("origin hit %d times across a restart", hits.Load())
	}
	if a.ContentType != "image/webp" {
		t.Errorf("declared content type lost across the cache: %q", a.ContentType)
	}
}

func TestBlockedAddressIsRefused(t *testing.T) {
	// The guard is netguard's, but the wiring is ours, and a proxy that
	// forgot to pass AllowPrivate=false through would be an SSRF hole with a
	// passing test suite.
	f := New(Options{Dir: t.TempDir()})
	_, err := f.Get(context.Background(), "http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("the metadata endpoint must never be fetchable")
	}
}

func TestSchemeIsRefused(t *testing.T) {
	f := newFetcher(t)
	for _, u := range []string{"file:///etc/passwd", "gopher://x/1", "ftp://x/y.png"} {
		if _, err := f.Get(context.Background(), u); err == nil {
			t.Errorf("%s was not refused", u)
		}
	}
}

func TestCacheFilesAreFannedOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(onePixelPNG)
	}))
	defer srv.Close()

	dir := t.TempDir()
	f := New(Options{Dir: dir, AllowPrivate: true})
	if _, err := f.Get(context.Background(), srv.URL+"/pic.png"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	var found string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".fail") {
			found = p
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("no cache file written (walk err %v)", err)
	}
	rel, _ := filepath.Rel(dir, found)
	if len(filepath.SplitList(rel)) == 0 || !strings.ContainsAny(rel, `/\`) {
		t.Errorf("cache file %q is not in a fanned-out subdirectory", rel)
	}
}

func TestNoCacheDirStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(onePixelPNG)
	}))
	defer srv.Close()

	f := New(Options{AllowPrivate: true})
	if _, err := f.Get(context.Background(), srv.URL+"/pic.png"); err != nil {
		t.Fatalf("a fetcher with no cache dir must still fetch: %v", err)
	}
}
