package netguard

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// When the egress span ends, and why it is not when the headers arrive.
//
// # The measurement this was reporting wrongly
//
// A RoundTripper returns as soon as the response HEADERS are in, and for
// everything this guards — a feed document, an article page, a proxied image —
// the headers are the cheap part. `defer span.End()` therefore timed the
// milliseconds BEFORE the download and called it the fetch: a 12 MB image over a
// slow link and a 40 KB one read identically, and "where did the fetch go slow"
// excluded the only part that was slow.
//
// Nothing failed. The traces were simply wrong, which is the class of defect a
// test has to be written for deliberately, because no user reports it.

// recordingSpan is the smallest thing that can answer "was End called, once, and
// what was on it". Embedding noop.Span satisfies the rest of the interface
// without this file having to track OTel's method set.
type recordingSpan struct {
	noop.Span
	mu    sync.Mutex
	ends  int
	attrs []attribute.KeyValue
	errs  []error
	code  codes.Code
}

func (s *recordingSpan) End(...trace.SpanEndOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ends++
}

func (s *recordingSpan) SetAttributes(kv ...attribute.KeyValue) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attrs = append(s.attrs, kv...)
}

func (s *recordingSpan) RecordError(err error, _ ...trace.EventOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
}

func (s *recordingSpan) SetStatus(c codes.Code, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = c
}

func (s *recordingSpan) endCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ends
}

func (s *recordingSpan) size() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kv := range s.attrs {
		if kv.Key == "http.response.body.size" {
			return kv.Value.AsInt64(), true
		}
	}
	return 0, false
}

func TestTheSpanOutlivesTheHeaders(t *testing.T) {
	// The property, stated as the bug: at the moment RoundTrip returns, the span
	// must still be open, because the body has not been read yet.
	sp := &recordingSpan{}
	body := &spanBody{ReadCloser: io.NopCloser(strings.NewReader("0123456789")), span: sp}

	if got := sp.endCount(); got != 0 {
		t.Fatalf("the span ended before the body was touched (%d)", got)
	}

	n, err := io.Copy(io.Discard, body)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("read %d bytes, want 10", n)
	}
	if got := sp.endCount(); got != 1 {
		t.Errorf("after reading to EOF the span ended %d times, want 1", got)
	}
	if size, ok := sp.size(); !ok || size != 10 {
		t.Errorf("body size recorded as %d (present=%v), want 10", size, ok)
	}
}

func TestReadingThenClosingEndsTheSpanOnce(t *testing.T) {
	// `io.ReadAll` followed by a deferred `Close` is what every caller here
	// actually does, so both paths fire. A span ended twice is a panic in some
	// SDKs and a duplicated trace in others; the sync.Once is what stops it, and
	// this is what stops the sync.Once from being deleted as redundant.
	sp := &recordingSpan{}
	body := &spanBody{ReadCloser: io.NopCloser(strings.NewReader("hello")), span: sp}

	if _, err := io.ReadAll(body); err != nil {
		t.Fatal(err)
	}
	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sp.endCount(); got != 1 {
		t.Errorf("the span ended %d times, want exactly 1", got)
	}
}

func TestClosingWithoutReadingEndsTheSpan(t *testing.T) {
	// A caller that gave up early — a size cap, a parse failure, a cancelled
	// poll. The span must close, or every bounded read leaks one.
	sp := &recordingSpan{}
	body := &spanBody{ReadCloser: io.NopCloser(strings.NewReader("unread")), span: sp}

	if err := body.Close(); err != nil {
		t.Fatal(err)
	}
	if got := sp.endCount(); got != 1 {
		t.Errorf("closing an unread body ended the span %d times, want 1", got)
	}
	// And it is NOT an error. A bounded read is the normal case here, and
	// painting every one of them red is how a trace view becomes noise.
	if sp.code == codes.Error {
		t.Error("abandoning a body was recorded as a failed egress")
	}
}

func TestABodyThatDiesMidTransferIsRecorded(t *testing.T) {
	// The case the old span could not see AT ALL: the headers had arrived, so it
	// had already closed green, and a connection dying halfway through a 3 MB
	// article left no trace anywhere.
	boom := errors.New("connection reset by peer")
	sp := &recordingSpan{}
	body := &spanBody{ReadCloser: io.NopCloser(&failingReader{after: 4, err: boom}), span: sp}

	if _, err := io.ReadAll(body); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the transport error", err)
	}
	if got := sp.endCount(); got != 1 {
		t.Fatalf("the span ended %d times, want 1", got)
	}
	if sp.code != codes.Error {
		t.Error("a body that died mid-transfer was recorded as a success")
	}
	if size, ok := sp.size(); !ok || size != 4 {
		t.Errorf("bytes transferred before the failure recorded as %d (present=%v), want 4", size, ok)
	}
}

// failingReader delivers `after` bytes and then fails, which is a truncated
// transfer rather than a refused connection — the failure that used to be
// invisible.
type failingReader struct {
	after int
	err   error
	n     int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n >= r.after {
		return 0, r.err
	}
	n := min(len(p), r.after-r.n)
	for i := range n {
		p[i] = 'x'
	}
	r.n += n
	return n, nil
}

func TestRoundTripHandsTheBodyToTheSpan(t *testing.T) {
	// The wiring, from the transport's side: whatever comes back must be the
	// wrapper, or none of the above runs in production.
	tr := &uaTransport{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("body")),
			Header:     http.Header{},
		}, nil
	}), purpose: "test"}

	req, err := http.NewRequest(http.MethodGet, "https://example.com/feed.xml", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()

	if _, ok := res.Body.(*spanBody); !ok {
		t.Fatalf("RoundTrip returned a bare body (%T): the span still ends at the headers", res.Body)
	}
}

// TestRoundTripWithNoBodyStillClosesTheSpan covers the contract violation
// branch. A RoundTripper is required to return a non-nil Body, so this is
// defensive — and the cost of being wrong about it is a span that never ends,
// which is worse than the line that prevents it.
func TestRoundTripWithNoBodyStillClosesTheSpan(t *testing.T) {
	tr := &uaTransport{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Header: http.Header{}}, nil
	}), purpose: "test"}

	req, err := http.NewRequest(http.MethodGet, "https://example.com/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Body != nil {
		t.Errorf("expected the nil-body branch, got %T", res.Body)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
