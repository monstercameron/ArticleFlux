package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ReadFrom is the third method statusRecorder has to declare for the reason
// Hijack and Flush do — httpobs_test.go covers those two — and it was the one
// with no coverage at all.
//
// net/http serves a file by asking whether the ResponseWriter implements
// io.ReaderFrom and taking a fast path when it does. Wrapping the writer hid
// that interface, so every static asset this server sends, including the
// multi-megabyte wasm binary, was copied through a userspace buffer instead.
// That was fixed; nothing was checking that it stays fixed, and the symptom is
// a response that is entirely correct and merely slower, which no test of the
// response can see.

// readerFromWriter is a ResponseWriter that also implements io.ReaderFrom, the
// way net/http's own does over a real connection.
type readerFromWriter struct {
	http.ResponseWriter
	used bool
}

func (w *readerFromWriter) ReadFrom(src io.Reader) (int64, error) {
	w.used = true
	return io.Copy(w.ResponseWriter, src)
}

// The fast path is TAKEN when the writer underneath offers one.
func TestReadFromUsesTheUnderlyingFastPathWhenThereIsOne(t *testing.T) {
	rec := httptest.NewRecorder()
	under := &readerFromWriter{ResponseWriter: rec}
	r := &statusRecorder{ResponseWriter: under}

	body := strings.Repeat("wasm bytes ", 1000)
	n, err := r.ReadFrom(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	if !under.used {
		t.Error("the underlying io.ReaderFrom was not used; the copy went through " +
			"a userspace buffer despite a fast path being available")
	}
	if n != int64(len(body)) {
		t.Errorf("ReadFrom reported %d bytes, want %d", n, len(body))
	}
	if r.written != int64(len(body)) {
		t.Errorf("the recorder counted %d bytes, want %d — a response served this "+
			"way would be logged as smaller than it was", r.written, len(body))
	}
	if r.status != http.StatusOK {
		t.Errorf("status = %d, want 200 — a handler that only ever calls ReadFrom "+
			"has still sent a 200", r.status)
	}
	if rec.Body.String() != body {
		t.Error("the body did not reach the client intact")
	}
}

// And it still works when there is no fast path underneath. That fallback is
// not hypothetical: httptest's recorder is one, and so is anything else that
// wraps the writer between here and the socket.
func TestReadFromFallsBackToACopyAndStillCounts(t *testing.T) {
	rec := httptest.NewRecorder()
	r := &statusRecorder{ResponseWriter: rec}

	body := "a handful of bytes"
	n, err := r.ReadFrom(strings.NewReader(body))
	if err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if n != int64(len(body)) || r.written != int64(len(body)) {
		t.Errorf("ReadFrom = %d, written = %d, want %d for both", n, r.written, len(body))
	}
	if rec.Body.String() != body {
		t.Errorf("body = %q, want %q", rec.Body.String(), body)
	}
}

// An explicit status set before the body must survive. A file handler that
// answers 206 to a range request and then hands the body to ReadFrom would
// otherwise be recorded as a 200, and every partial response would disappear
// into the success bucket.
func TestReadFromDoesNotOverwriteAStatusAlreadySent(t *testing.T) {
	r := &statusRecorder{ResponseWriter: httptest.NewRecorder()}

	r.WriteHeader(http.StatusPartialContent)
	if _, err := r.ReadFrom(strings.NewReader("partial")); err != nil {
		t.Fatal(err)
	}
	if r.status != http.StatusPartialContent {
		t.Errorf("status = %d, want 206", r.status)
	}
}

// The byte count adds up across both ways a handler can write. A response that
// writes a preamble and then streams a file uses each of them, and counting
// only one under-reports every time.
func TestWritesAndReadFromsAccumulateTogether(t *testing.T) {
	r := &statusRecorder{ResponseWriter: httptest.NewRecorder()}

	if _, err := r.Write([]byte("head")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFrom(bytes.NewReader([]byte("body"))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}

	if r.written != 12 {
		t.Errorf("written = %d, want 12 (4+4+4)", r.written)
	}
}

// The zero value means 200, and the FIRST status wins.
//
// Recording a bare Write as status 0 puts every successful request into an
// "other" bucket, which is the classic version of this bug and the one the
// type's own comment names. Recording the SECOND WriteHeader would attribute
// the response to whatever an error path tried to send after the headers had
// already gone out.
func TestTheZeroStatusIsRecordedAsTwoHundredAndTheFirstStatusWins(t *testing.T) {
	r := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, err := r.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if r.status != http.StatusOK {
		t.Errorf("status = %d after a bare Write, want 200", r.status)
	}

	r2 := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	r2.WriteHeader(http.StatusNotFound)
	r2.WriteHeader(http.StatusInternalServerError)
	if r2.status != http.StatusNotFound {
		t.Errorf("status = %d, want the first one sent", r2.status)
	}
}
