package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
	"github.com/monstercameron/ArticleFlux/internal/smart"
)

// The beat-addressed path (internal/fluxcast), driven the way the player drives it.
//
// These tests exist for the same reason podcastspeech_test.go does: everything
// upstream of them stops at the gates, and the question a reader is actually
// asking when they report "it stopped playing" is whether audio comes back. The
// difference here is that a beat carries a WORD BUDGET, so there is a second
// question underneath the first — whether the two ends agree on which recording
// they are talking about, because a cache key that disagrees is a bill that gets
// paid twice and a fitted programme that plays unfitted audio.

// beat drives the handler with the parameters a planned beat carries.
func beat(t *testing.T, a *App, item string, words int, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{}
	q.Set("item", item)
	q.Set(beatWordsParam, strconv.Itoa(words))
	q.Set(beatRevParam, smart.PromptVersion)
	for k, v := range extra {
		q[k] = v
	}
	return speak(t, a, q.Encode())
}

func TestABeatRequestReturnsAudio(t *testing.T) {
	// The whole path, with everything a planned story beat sends.
	a, voice, _, ids := broadcastApp(t)

	rec := beat(t, a, ids[1], 95, url.Values{
		prevItemParam:    {ids[0]},
		openNowParam:     {"2026-08-01T18:30:00-04:00"},
		openStoriesParam: {"9"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Fatal("no audio came back")
	}
	if voice.count() == 0 {
		t.Fatal("nothing was synthesised")
	}
}

func TestTheServerNeverTakesTheClientsCacheKey(t *testing.T) {
	// The audio cache is shared between readers, because the same article read
	// in the same voice is the same recording. A server that filed audio under
	// a caller-supplied key would let one reader write into another's slot, so
	// the key is DERIVED here and a forged one changes nothing.
	a, voice, _, ids := broadcastApp(t)

	beat(t, a, ids[1], 95, url.Values{prevItemParam: {ids[0]}})
	honest := voice.last().key

	beat(t, a, ids[1], 95, url.Values{
		prevItemParam: {ids[0]},
		// Not a parameter the server reads. If it ever became one, this test
		// fails and the reason is in this comment.
		"key": {"somebody-elses-slot"},
	})
	if voice.last().key != honest {
		t.Errorf("a caller-supplied key changed the cache slot: %q vs %q", voice.last().key, honest)
	}
}

func TestTheWordBudgetIsPartOfTheCacheSlot(t *testing.T) {
	// The same story fitted to two lengths is two recordings. Sharing a slot
	// would have a twenty-minute bulletin quietly playing unfitted audio.
	a, voice, _, ids := broadcastApp(t)
	a.write = &stubWriter{}

	beat(t, a, ids[1], 95, url.Values{prevItemParam: {ids[0]}})
	short := voice.last().key
	beat(t, a, ids[1], 220, url.Values{prevItemParam: {ids[0]}})
	long := voice.last().key

	if short == long {
		t.Errorf("two word budgets share one cache slot (%q)", short)
	}
	// …and the fallback, which every beat takes on an instance with no model
	// key, deliberately does NOT vary by budget: it is the article, and the
	// article is the same length whatever the programme wanted.
	a.write = nil
	beat(t, a, ids[1], 95, url.Values{prevItemParam: {ids[0]}})
	first := voice.last().key
	beat(t, a, ids[1], 220, url.Values{prevItemParam: {ids[0]}})
	if voice.last().key != first {
		t.Error("the read-the-article fallback varied by a budget it does not honour")
	}
}

func TestTheKeyTheServerComputesIsTheOneTheClientPlanned(t *testing.T) {
	// Both ends derive the key from the same rules over the same inputs. If
	// they ever disagree, every request misses the cache and every segment is
	// paid for twice — silently, because the audio still plays.
	a, voice, sc, ids := broadcastApp(t)

	item, err := a.repo.GetItem(t.Context(), sc, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	prev, err := a.repo.GetItem(t.Context(), sc, ids[0])
	if err != nil {
		t.Fatal(err)
	}

	a.write = &stubWriter{}
	beat(t, a, ids[1], 95, url.Values{prevItemParam: {ids[0]}})
	got := voice.last().key

	// What a client would compute for the same beat.
	want := "beat:" + fluxcast.ScriptKey(fluxcast.Brief{
		Kind: fluxcast.BeatStory, Words: 95, Vibe: smart.DefaultVibe,
		Revision: smart.PromptVersion, Handover: -1,
		Subject: fluxcast.Subject{ItemID: item.ID},
		Prev:    fluxcast.Subject{ItemID: prev.ID},
	})
	if got != want {
		t.Errorf("server key %q, client key %q", got, want)
	}
}

func TestAStoryBeatFallsBackToReadingTheArticle(t *testing.T) {
	// The free-tier promise: a broadcast that went quiet the moment a model was
	// unavailable would be a paid feature wearing a free one's name. There is no
	// API key in these tests, so every beat here takes the fallback.
	a, voice, sc, ids := broadcastApp(t)

	beat(t, a, ids[1], 95, url.Values{prevItemParam: {ids[0]}})
	item, err := a.repo.GetItem(t.Context(), sc, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if voice.last().key != item.ID {
		t.Errorf("fell back under key %q, want the plain article key %q", voice.last().key, item.ID)
	}
	if !strings.Contains(voice.last().text, item.Title) {
		t.Errorf("the fallback did not read %q: %q", item.Title, voice.last().text)
	}
}

func TestAGreetingDoesNotFallBackToTheArticle(t *testing.T) {
	// Nothing else has anything to fall back TO. Reading the first article in
	// place of a greeting would play a story the programme is about to play
	// properly, and the listener would hear it twice.
	a, _, _, ids := broadcastApp(t)

	rec := beat(t, a, ids[0], 89, url.Values{
		introParam:      {introOnly},
		openLineupParam: {strings.Join(ids[:3], ",")},
		openNowParam:    {"2026-08-01T08:30:00-04:00"},
	})
	// 501, not 502: the server is working correctly and simply has no model
	// key. Either way the player skips the beat and the programme carries on;
	// the status is what tells a reader whose problem it is.
	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status %d, want 501 — a greeting on an instance with no writer", rec.Code)
	}
}

func TestAStalePlanIsRefusedRatherThanServed(t *testing.T) {
	// A programme planned against an older narrator has word budgets and cache
	// keys this build cannot honour. 409 tells the player to re-plan; reading
	// the article instead would hide it behind something that sounds nearly
	// right.
	a, _, _, ids := broadcastApp(t)

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(beatWordsParam, "95")
	q.Set(beatRevParam, "v1")
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409", rec.Code)
	}
}

func TestARequestWithNoBudgetTakesTheOldPath(t *testing.T) {
	// Old clients are cached by a Service Worker and will keep asking the old
	// way for as long as their bundle survives. The honest answer to that
	// request is the one it has always had.
	a, voice, _, ids := broadcastApp(t)

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(prevItemParam, ids[0])
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if strings.HasPrefix(voice.last().key, "beat:") {
		t.Error("a request with no word budget was answered on the beat path")
	}
}

func TestAnAbsurdBudgetIsNotHonoured(t *testing.T) {
	// A budget outside anything a beat could be is a client bug. It must not
	// become a request to write five thousand words.
	a, voice, _, ids := broadcastApp(t)

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(beatWordsParam, "50000")
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 on the legacy path", rec.Code)
	}
	if strings.HasPrefix(voice.last().key, "beat:") {
		t.Error("an absurd budget was taken as a beat")
	}
}

// stubWriter answers every commission without a model, so a test can reach the
// half of this path that lies past the writer — the cache key, the status, the
// bytes — which is otherwise reachable only through a paid call.
type stubWriter struct{ err error }

func (s *stubWriter) Write(_ context.Context, b fluxcast.Brief) (fluxcast.Draft, error) {
	if s.err != nil {
		return fluxcast.Draft{}, s.err
	}
	text := "Written for " + b.Subject.ItemID + " at " + strconv.Itoa(b.Words) + " words."
	return fluxcast.Draft{Text: text, Words: fluxcast.CountWords(text), Model: "stub"}, nil
}
