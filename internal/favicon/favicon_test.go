package favicon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// png1x1 is a minimal valid-looking PNG payload. The fetcher never decodes
// the image, so any distinct byte string is fine for telling two "icons"
// apart in a test — what matters is the Content-Type header and the bytes
// round-tripping unchanged.
var png1x1 = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 'A'}

// svgPayload is a minimal SVG carrying an inline script, i.e. the actual
// stored-XSS shape the package doc warns about.
const svgPayload = `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.cookie)</script></svg>`

func newFetcher(allowPrivate bool) *Fetcher { return New(allowPrivate) }

func fetch(t *testing.T, f *Fetcher, srv *httptest.Server, timeout time.Duration) (*Icon, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return f.Fetch(ctx, srv.URL)
}

// --- no icon / discovery -----------------------------------------------------

func TestFetch_NoIconAnywhere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("Fetch() = %v, %v; want (nil, ErrNoIcon)", icon, err)
	}
}

func TestFetch_MalformedHTMLFallsBackToFaviconICO(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Deliberately broken markup: unclosed tags, a stray '<', no </html>.
		_, _ = w.Write([]byte(`<htm<l><hea<d><titl>broken<link rel=icon href=`))
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(png1x1)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != nil {
		t.Fatalf("Fetch() with malformed HTML should still find /favicon.ico, got err %v", err)
	}
	if string(icon.Bytes) != string(png1x1) {
		t.Errorf("icon bytes = %q, want the /favicon.ico payload", icon.Bytes)
	}
}

func TestFetch_DeclaredIconPreferredOverFaviconICO(t *testing.T) {
	declared := append([]byte(nil), png1x1...)
	declared = append(declared, 'D') // distinguish from the .ico payload
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="icon" href="/site-icon.png"></head></html>`))
	})
	mux.HandleFunc("/site-icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(declared)
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(png1x1)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != nil {
		t.Fatalf("Fetch() = err %v", err)
	}
	if string(icon.Bytes) != string(declared) {
		t.Errorf("Fetch() returned the .ico fallback instead of the declared <link> icon")
	}
}

func TestFetch_AppleTouchIconDiscovered(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="apple-touch-icon" href="/apple.png"></head></html>`))
	})
	mux.HandleFunc("/apple.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png1x1)
	})
	mux.HandleFunc("/favicon.ico", http.NotFound)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != nil {
		t.Fatalf("apple-touch-icon should be discovered, got err %v", err)
	}
	if string(icon.Bytes) != string(png1x1) {
		t.Error("apple-touch-icon bytes did not round-trip")
	}
}

func TestFetch_ETagPropagated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write(png1x1)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != nil {
		t.Fatalf("Fetch() = err %v", err)
	}
	if icon.ETag != `"abc123"` {
		t.Errorf("ETag = %q, want %q", icon.ETag, `"abc123"`)
	}
}

// --- the SVG security rule ---------------------------------------------------

// TestFetch_SVGRefused pins the headline security property: a site that
// honestly declares its icon as SVG must never have it stored, because
// internal/app/favicons.go serves stored bytes back from our own origin.
func TestFetch_SVGRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><link rel="icon" href="/icon.svg"></head></html>`))
	})
	mux.HandleFunc("/icon.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(svgPayload))
	})
	mux.HandleFunc("/favicon.ico", http.NotFound)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("SVG-declared icon: Fetch() = %v, %v; want (nil, ErrNoIcon) — SVG must be refused", icon, err)
	}
}

// TestFetch_SVGRefused_CaseAndCharsetVariants makes sure the header match
// is not defeated by trivial variation: uppercase, and a charset parameter.
func TestFetch_SVGRefused_CaseAndCharsetVariants(t *testing.T) {
	for _, ct := range []string{
		"image/svg+xml",
		"IMAGE/SVG+XML",
		"Image/Svg+Xml; charset=utf-8",
		" image/svg+xml ",
	} {
		t.Run(ct, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/", http.NotFound)
			mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", ct)
				_, _ = w.Write([]byte(svgPayload))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			f := newFetcher(true)
			icon, err := fetch(t, f, srv, 5*time.Second)
			if err != ErrNoIcon || icon != nil {
				t.Fatalf("Content-Type %q: Fetch() = %v, %v; want refusal", ct, icon, err)
			}
		})
	}
}

// TestFetch_RedirectToSVGRefused: the favicon.ico candidate itself 302s to a
// URL that serves SVG. The guard must evaluate the FINAL response's
// Content-Type, not just the immediate one.
func TestFetch_RedirectToSVGRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/actually.svg", http.StatusFound)
	})
	mux.HandleFunc("/actually.svg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(svgPayload))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("redirect-to-SVG: Fetch() = %v, %v; want refusal", icon, err)
	}
}

// TestFetch_ContentTypeLieIsTrustedNotSniffed documents — rather than
// asserts a security property that does not exist in the code — the actual
// behaviour of get() in favicon.go: it decides purely from the Content-Type
// HEADER (favicon.go:169-172) and never inspects the bytes. A server that
// serves real SVG (with an inline <script>) but CLAIMS "image/png" is
// therefore accepted and stored as a "png".
//
// This is not exploitable end-to-end today only because the sole consumer,
// internal/app/favicons.go, echoes back the SAME claimed Content-Type it
// received (never the sniffed/actual type) together with
// "X-Content-Type-Options: nosniff" on every response — so a browser loading
// the icon is told, and forced to honour, "image/png" and will not render or
// execute the SVG/script inside. If any future caller ever re-sniffs the
// bytes, or serves them without nosniff, or trusts a DIFFERENT content-type
// than the one that was validated here, this becomes a live stored-XSS path.
// Pinned so a future change to this trust model is a deliberate decision,
// not a silent one.
func TestFetch_ContentTypeLieIsTrustedNotSniffed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		// Real SVG bytes, mislabeled as an allowed image type.
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(svgPayload))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != nil {
		t.Fatalf("current behaviour: mislabeled content is accepted; got err %v instead", err)
	}
	if icon.ContentType != "image/png" {
		t.Errorf("ContentType = %q, want the claimed %q (header is trusted verbatim)", icon.ContentType, "image/png")
	}
	if !strings.Contains(string(icon.Bytes), "<script>") {
		t.Error("expected the raw SVG/script bytes to have been stored unchanged")
	}
}

// --- content-type / size gates -----------------------------------------------

func TestFetch_NonImageContentTypeRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not an icon</html>"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("Fetch() = %v, %v; want ErrNoIcon for a non-image Content-Type", icon, err)
	}
}

// TestFetch_MissingContentTypeRefused sends a response with NO Content-Type
// header on the wire at all. This has to be done by hijacking the
// connection and writing raw bytes: net/http's normal ResponseWriter
// auto-fills Content-Type via content sniffing on the first Write() when the
// handler never sets one (confirmed experimentally — a handler that just
// writes real PNG bytes ends up with a response carrying
// "Content-Type: image/png" supplied by the Go server itself, not by the
// handler), which would silently defeat this test.
func TestFetch_MissingContentTypeRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()
		body := png1x1
		resp := "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: " +
			itoa(len(body)) + "\r\n\r\n"
		_, _ = buf.WriteString(resp)
		_, _ = buf.Write(body)
		_ = buf.Flush()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("Fetch() = %v, %v; want ErrNoIcon when Content-Type is absent", icon, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestFetch_OversizedIconRefused(t *testing.T) {
	huge := make([]byte, MaxBytes+1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(huge)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 10*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("Fetch() = %v, %v; want ErrNoIcon for a response over MaxBytes", icon, err)
	}
}

func TestFetch_ExactlyMaxBytesAccepted(t *testing.T) {
	exact := make([]byte, MaxBytes)
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(exact)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 10*time.Second)
	if err != nil {
		t.Fatalf("a response of exactly MaxBytes should be accepted, got err %v", err)
	}
	if len(icon.Bytes) != MaxBytes {
		t.Errorf("len(icon.Bytes) = %d, want %d", len(icon.Bytes), MaxBytes)
	}
}

// --- SSRF: redirects into blocked ranges must not be followed ---------------

// TestFetch_RedirectToMetadataAddressRefused proves the redirect hop is
// re-validated even under the permissive (AllowPrivate: true) policy: the
// link-local range, and specifically the cloud metadata address, is in
// netguard's neverAllowed list and stays blocked under every policy.
func TestFetch_RedirectToMetadataAddressRefused(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Even the permissive fetcher (used so the httptest server on 127.0.0.1
	// is itself reachable) must refuse to follow this redirect.
	f := newFetcher(true)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("redirect to the metadata address: Fetch() = %v, %v; want ErrNoIcon", icon, err)
	}
}

// TestFetch_RedirectToLoopbackRefusedUnderStrictPolicy shows the strict
// (AllowPrivate: false) policy — production's default when the operator has
// not opted into private-network feeds — refuses a redirect back into
// loopback/RFC1918, not just the never-allowed ranges.
//
// Note: under the strict policy the *initial* request to the httptest server
// (itself on 127.0.0.1) is refused at the dialer too, so this test does not
// exercise favicon-specific logic beyond confirming Fetch surfaces that as
// a plain ErrNoIcon rather than hanging or panicking. The redirect-specific
// guarantee is covered by internal/netguard's own tests; this only confirms
// favicon.Fetch degrades to ErrNoIcon rather than something worse when the
// whole site is unreachable under the strict policy.
func TestFetch_StrictPolicyRefusesLoopbackSite(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png1x1)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(false)
	icon, err := fetch(t, f, srv, 5*time.Second)
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("strict policy against a loopback site: Fetch() = %v, %v; want ErrNoIcon", icon, err)
	}
}

// --- slow / hanging servers ---------------------------------------------------

// TestFetch_SlowServerRespectsContextDeadline ensures a hanging publisher
// cannot hold the calling request open: Fetch must return once the CALLER's
// context expires, well before the client's own 10s/30s timeouts.
func TestFetch_SlowServerRespectsContextDeadline(t *testing.T) {
	block := make(chan struct{})
	defer close(block) // release the handler goroutine so httptest.Close doesn't hang
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := newFetcher(true)
	start := time.Now()
	icon, err := fetch(t, f, srv, 300*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil || icon != nil {
		t.Fatalf("Fetch() against a hanging server = %v, %v; want an error", icon, err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Fetch() took %s to respect a 300ms context deadline", elapsed)
	}
}

// --- Host() ------------------------------------------------------------------

func TestHost(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://Example.COM/feed.xml", "example.com"},
		{"http://example.com:8080/x", "example.com"},
		{"  https://example.com  ", "example.com"},
		{"ftp://example.com/x", "example.com"}, // Host only cares about the authority, not the scheme
		{"not a url at all", ""},
		{"", ""},
		{"example.com/no-scheme", ""}, // no scheme: parsed as a path, no Host
		{"https://[2001:db8::1]:443/x", "2001:db8::1"},
	}
	for _, c := range cases {
		if got := Host(c.in); got != c.want {
			t.Errorf("Host(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFetch_EmptySiteURLIsNoIcon(t *testing.T) {
	f := newFetcher(true)
	icon, err := f.Fetch(context.Background(), "")
	if err != ErrNoIcon || icon != nil {
		t.Fatalf("Fetch(\"\") = %v, %v; want (nil, ErrNoIcon)", icon, err)
	}
}
