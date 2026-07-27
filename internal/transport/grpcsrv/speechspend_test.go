package grpcsrv

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/tts"
)

// smartFor builds a SmartServer over a real (empty) database.
//
// Real rather than nil, because `config` legitimately asks the settings repo
// whether this instance can store secrets at all — that is part of the status it
// reports — and a nil repo would be testing a server that cannot exist.
func smartFor(t *testing.T, voice *tts.Client) *SmartServer {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "smart.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewSmartServer(store.NewSettingsRepo(db, nil), llm.New(func(context.Context) string { return "" }), nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if voice != nil {
		s = s.WithSpeechMeter(voice)
	}
	return s
}

// P3: the voice's spend reaches the screen, beside the model's.
//
// `internal/tts` meters paid requests, characters and cache hits, and nothing
// read any of it. §22.8 asks that "AI features are off" be answerable rather
// than mysterious; "listening has cost this instance N characters, and M% of
// listens came from cache" is the same question about the other paid surface —
// and the ratio is the number that says whether `speech-cache` is earning its
// disk.

// An instance that never configured speech reports zeroes, not nothing.
//
// A missing number reads as "not measured", which is a different claim from
// "nothing spent" — and the screen has no way to tell them apart.
func TestSpeechSpendIsZeroRatherThanAbsentWithoutAKey(t *testing.T) {
	out := smartFor(t, nil).config(context.Background())
	if out == nil {
		t.Fatal("no config at all")
	}
	if out.GetSpeechRequests() != 0 || out.GetSpeechCharacters() != 0 || out.GetSpeechCached() != 0 {
		t.Errorf("an instance with no speech client reported %d requests / %d characters / %d cached",
			out.GetSpeechRequests(), out.GetSpeechCharacters(), out.GetSpeechCached())
	}
}

// And what the meter counted is what the screen is told.
//
// The assertion is on the numbers rather than on "some number is present": a
// surface that reports a constant is worse than one that reports nothing,
// because it looks like it is working.
func TestWhatTheVoiceSpentIsWhatTheScreenSees(t *testing.T) {
	out := smartFor(t, tts.NewForTest(tts.Usage{Requests: 7, Characters: 4321, Cached: 21})).
		config(context.Background())
	if out.GetSpeechRequests() != 7 {
		t.Errorf("speech_requests = %d, want 7", out.GetSpeechRequests())
	}
	if out.GetSpeechCharacters() != 4321 {
		t.Errorf("speech_characters = %d, want 4321 — characters are what speech is billed by",
			out.GetSpeechCharacters())
	}
	if out.GetSpeechCached() != 21 {
		t.Errorf("speech_cached = %d, want 21", out.GetSpeechCached())
	}

	// The ratio the screen shows is derived from these two, so the denominator
	// is asserted here rather than left to whoever renders it: 21 of 28 listens
	// came from disk, which is the number that says the cache is worth its
	// space.
	total := out.GetSpeechCached() + out.GetSpeechRequests()
	if total != 28 {
		t.Fatalf("listens = %d, want 28", total)
	}
	if ratio := float64(out.GetSpeechCached()) / float64(total); ratio < 0.74 || ratio > 0.76 {
		t.Errorf("cache-hit ratio = %.3f, want 0.75", ratio)
	}
}
