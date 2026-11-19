//go:build js && wasm

package view

import (
	"errors"
	"sync"
)

// What the Podcast tab says about the last press. The empty string is "nothing
// has been pressed yet", which is not a status and draws nothing.
const (
	prefSaving = "saving"
	prefSaved  = "saved"
	prefFailed = "failed"
)

// errPrefsNoClient is a press made with no connection object at all, which is a
// failure like any other and gets the same words.
var errPrefsNoClient = errors.New("not connected")

// prefWriter is how a settings press reaches the server: one write at a time, in
// the order the reader pressed, with a verdict on each.
//
// # What it replaces
//
// `go func() { _ = c.SetPrefs(...) }()` per press. Two things were wrong with
// that and both read, to the reader, as "it did not stick":
//
//   - The result was thrown away. A press that never reached the server left the
//     switch showing the new value until the next reload, when it quietly went
//     back — no message, nothing to retry, and no way to tell which presses had
//     landed.
//   - Each press was its own goroutine, so two presses on one setting (a
//     double-click, a switch flipped on and straight back off) were two
//     concurrent calls with no ordering between them. Whichever the server
//     happened to finish last was what was stored, and half the time that was
//     the older one.
//
// # Rolling back
//
// A write that fails puts the control back where the SERVER still has it, not
// where it was before the last press: with presses A then B and A failing but B
// landing, the stored value is B's and the screen already shows it, so there is
// nothing to undo. `server` tracks the last value known to be stored for every
// key with a write in flight, and a key is only restored once nothing newer is
// waiting behind the one that failed.
//
// A write the client has kept for later (keptOptimistic) is NOT a failure and is
// not rolled back: what is on screen is what will be stored.
type prefWriter struct {
	send func(kv map[string]string) error

	mu       sync.Mutex
	queue    []prefJob
	running  bool
	inflight map[string]int
	server   map[string]string
	failed   bool
}

// prefJob is one press. prev is what each key held before it — absent for a
// write that has no on-screen control to put back.
type prefJob struct {
	kv, prev map[string]string
	done     func(prefVerdict)
}

// prefVerdict is what became of one press.
type prefVerdict struct {
	// Err is the failure, nil when the write is stored or kept for later.
	Err error
	// Restore is the stored value of every key whose control must be put back:
	// the keys this press failed to change and that no later press has changed.
	Restore map[string]string
	// Settled is true when nothing is queued behind this press, and Failed that
	// some press since the queue was last empty did not make it. Together they
	// are what the screen reports: "saved" only once everything is, and "could
	// not save" even if a later, different press went through.
	Settled, Failed bool
}

func newPrefWriter(send func(map[string]string) error) *prefWriter {
	return &prefWriter{send: send, inflight: map[string]int{}, server: map[string]string{}}
}

// submit queues a write. done is called once, from the writer's own goroutine —
// the caller marshals it onto the UI thread.
func (w *prefWriter) submit(kv, prev map[string]string, done func(prefVerdict)) {
	w.mu.Lock()
	for k := range kv {
		if w.inflight[k] == 0 {
			// The stored value, as far as this client knows, is the one on screen
			// just before the first unsent press.
			if v, ok := prev[k]; ok {
				w.server[k] = v
			} else {
				delete(w.server, k)
			}
		}
		w.inflight[k]++
	}
	w.queue = append(w.queue, prefJob{kv: kv, prev: prev, done: done})
	start := !w.running
	w.running = true
	w.mu.Unlock()
	if start {
		go w.drain()
	}
}

func (w *prefWriter) drain() {
	for {
		w.mu.Lock()
		if len(w.queue) == 0 {
			w.running = false
			w.mu.Unlock()
			return
		}
		job := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()

		err := w.send(job.kv)
		stored := err == nil || keptOptimistic(err)

		verdict := prefVerdict{}
		if err != nil && !stored {
			verdict.Err = err
		}
		w.mu.Lock()
		if verdict.Err != nil {
			w.failed = true
		}
		for k, v := range job.kv {
			w.inflight[k]--
			if stored {
				w.server[k] = v
			}
			if w.inflight[k] > 0 {
				continue
			}
			delete(w.inflight, k)
			if stored {
				delete(w.server, k)
				continue
			}
			if old, ok := w.server[k]; ok {
				if verdict.Restore == nil {
					verdict.Restore = map[string]string{}
				}
				verdict.Restore[k] = old
			}
			delete(w.server, k)
		}
		if verdict.Settled = len(w.queue) == 0; verdict.Settled {
			verdict.Failed = w.failed
			w.failed = false
		}
		w.mu.Unlock()
		if job.done != nil {
			job.done(verdict)
		}
	}
}
