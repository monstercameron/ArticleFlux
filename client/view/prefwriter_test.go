//go:build js && wasm

package view

import (
	"errors"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/client/data"
)

// The Podcast tab's requirement chips are switches, and a switch that is ON has
// to turn OFF when pressed. The Smart+ voice and Keep playing rows only ever
// wrote "true": pressed while showing "on" they did nothing at all, so a reader
// who had read-to-me set up could not take either back from the screen that
// lists them and was sent to the Listening tab to find a switch this one drew.
func TestRequirementChipsTurnTheirSwitchBothWays(t *testing.T) {
	for _, tc := range []struct {
		key      string
		pref     string
		smart    bool
		podcast  bool
		auto     bool
		wantNext bool
	}{
		{prereqSmartVoice, "tts.smartPlus", true, false, false, false},
		{prereqSmartVoice, "tts.smartPlus", false, false, false, true},
		{prereqPodcast, "tts.podcast", false, true, false, false},
		{prereqPodcast, "tts.podcast", false, false, false, true},
		{prereqKeepPlaying, "tts.autoplay", false, false, true, false},
		{prereqKeepPlaying, "tts.autoplay", false, false, false, true},
	} {
		pref, next, ok := slideNeedsFixWrite(tc.key, tc.smart, tc.podcast, tc.auto)
		if !ok || pref != tc.pref || next != tc.wantNext {
			t.Errorf("%s (smart=%v podcast=%v auto=%v): got %q=%v ok=%v, want %q=%v",
				tc.key, tc.smart, tc.podcast, tc.auto, pref, next, ok, tc.pref, tc.wantNext)
		}
	}
	if _, _, ok := slideNeedsFixWrite(prereqServerKey, false, false, false); ok {
		t.Error("the server's key is a switch")
	}
}

// gate is a send that blocks until released, so a test can hold a write in
// flight and press again behind it.
type gate struct {
	sent chan map[string]string
	err  chan error
}

func newGate() *gate { return &gate{sent: make(chan map[string]string, 8), err: make(chan error, 8)} }

func (g *gate) send(kv map[string]string) error {
	g.sent <- kv
	return <-g.err
}

func waitVerdict(t *testing.T, ch chan prefVerdict) prefVerdict {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("the writer never reported")
		return prefVerdict{}
	}
}

// Two presses on one setting are two writes in the order they were pressed. Each
// used to be its own goroutine, so a switch flipped on and straight back off
// could be stored as "on" — the screen said off, the next reload said on.
func TestPrefWritesGoInPressOrder(t *testing.T) {
	g := newGate()
	w := newPrefWriter(g.send)
	done := make(chan prefVerdict, 2)
	w.submit(map[string]string{"tts.podcast": "true"}, map[string]string{"tts.podcast": "false"}, func(v prefVerdict) { done <- v })
	w.submit(map[string]string{"tts.podcast": "false"}, map[string]string{"tts.podcast": "true"}, func(v prefVerdict) { done <- v })

	first := <-g.sent
	select {
	case <-g.sent:
		t.Fatal("the second write was sent while the first was still in flight")
	case <-time.After(50 * time.Millisecond):
	}
	g.err <- nil
	second := <-g.sent
	g.err <- nil
	if first["tts.podcast"] != "true" || second["tts.podcast"] != "false" {
		t.Errorf("writes out of order: %v then %v", first, second)
	}
	waitVerdict(t, done)
	waitVerdict(t, done)
}

// A write that fails puts the control back to what the server still has.
func TestAFailedPrefWriteIsRestored(t *testing.T) {
	g := newGate()
	w := newPrefWriter(g.send)
	done := make(chan prefVerdict, 1)
	w.submit(map[string]string{podcastVibePref: "brisk"}, map[string]string{podcastVibePref: "calm"}, func(v prefVerdict) { done <- v })
	<-g.sent
	g.err <- errors.New("offline")
	v := waitVerdict(t, done)
	if v.Err == nil || v.Restore[podcastVibePref] != "calm" {
		t.Errorf("verdict = %+v, want a failure restoring calm", v)
	}
}

// With presses A then B and only A failing, the server holds B and the screen
// shows B: restoring A's "before" would put the control back to a value the
// server no longer has.
func TestAnEarlierFailureDoesNotUndoALaterSuccess(t *testing.T) {
	g := newGate()
	w := newPrefWriter(g.send)
	done := make(chan prefVerdict, 2)
	w.submit(map[string]string{"tts.digest": "true"}, map[string]string{"tts.digest": "false"}, func(v prefVerdict) { done <- v })
	w.submit(map[string]string{"tts.digest": "false"}, map[string]string{"tts.digest": "true"}, func(v prefVerdict) { done <- v })
	<-g.sent
	g.err <- errors.New("blip")
	a := waitVerdict(t, done)
	<-g.sent
	g.err <- nil
	b := waitVerdict(t, done)
	if a.Err == nil || len(a.Restore) != 0 {
		t.Errorf("first verdict = %+v: it failed, but a newer press is still waiting, so nothing may be restored yet", a)
	}
	if b.Err != nil || len(b.Restore) != 0 {
		t.Errorf("second verdict = %+v, want a clean success", b)
	}
}

// Both fail: the control goes back to what the server had before EITHER press.
func TestTwoFailedPressesRestoreTheOriginal(t *testing.T) {
	g := newGate()
	w := newPrefWriter(g.send)
	done := make(chan prefVerdict, 2)
	w.submit(map[string]string{"tts.digest": "true"}, map[string]string{"tts.digest": "false"}, func(v prefVerdict) { done <- v })
	w.submit(map[string]string{"tts.digest": "false"}, map[string]string{"tts.digest": "true"}, func(v prefVerdict) { done <- v })
	<-g.sent
	g.err <- errors.New("down")
	waitVerdict(t, done)
	<-g.sent
	g.err <- errors.New("down")
	b := waitVerdict(t, done)
	if b.Restore["tts.digest"] != "false" {
		t.Errorf("restore = %v, want the value before the first press", b.Restore)
	}
}

// A write the client keeps for later is the truth, not a failure — rolling it
// back would discard a write the application has in fact retained.
func TestAKeptPrefWriteIsNotRolledBack(t *testing.T) {
	g := newGate()
	w := newPrefWriter(g.send)
	done := make(chan prefVerdict, 1)
	w.submit(map[string]string{"tts.autoplay": "true"}, map[string]string{"tts.autoplay": "false"}, func(v prefVerdict) { done <- v })
	<-g.sent
	g.err <- data.ErrQueued
	v := waitVerdict(t, done)
	if v.Err != nil || len(v.Restore) != 0 {
		t.Errorf("verdict = %+v, want no failure and no restore", v)
	}
}

// A write with no on-screen control (the saved place, say) has nothing to put
// back and must not invent a value to restore.
func TestAWriteWithNoPrevIsNeverRestored(t *testing.T) {
	g := newGate()
	w := newPrefWriter(g.send)
	done := make(chan prefVerdict, 1)
	w.submit(map[string]string{"read.kind": "feed"}, nil, func(v prefVerdict) { done <- v })
	<-g.sent
	g.err <- errors.New("down")
	v := waitVerdict(t, done)
	if v.Err == nil || len(v.Restore) != 0 {
		t.Errorf("verdict = %+v", v)
	}
}
