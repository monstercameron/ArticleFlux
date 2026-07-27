package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The broadcast path, end to end (§19).
//
// Everything else in speech_test.go stops at the gates: it proves a bad ticket
// is refused, an opted-out account is refused, a missing key is 501. None of it
// ever reached the half that produces sound — the script choice, the fallback
// chain, the cache key, the bytes — because that half ends in a paid call to
// OpenAI.
//
// So the `speaker` seam (see speech.go) is stood in for here, and these tests
// ask the only question a reader actually cares about: **when broadcast mode is
// on, and the client sends everything it now sends, does audio come back?** That
// is the question a report of "it stopped playing" is asking, and until this
// file existed nothing could answer it.

// fakeVoice is a speaker that records what it was asked for and returns bytes.
//
// It records the CACHE KEY as well as the text, because the key is half the
// contract: a mode that changed the script without changing the key would serve
// yesterday's rendering of a different thing, which is indistinguishable from
// the preference not working.
type fakeVoice struct {
	mu    sync.Mutex
	calls []voiceCall
	// fail makes Speak error, for the path where synthesis is down.
	fail error
	// off makes Configured answer false, which is an instance with no key.
	off bool
}

type voiceCall struct{ key, text, model, voice string }

func (f *fakeVoice) Configured(context.Context) bool { return !f.off }

func (f *fakeVoice) Speak(_ context.Context, key, text, model, voice string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, voiceCall{key, text, model, voice})
	if f.fail != nil {
		return nil, f.fail
	}
	// Not real MP3, and it does not need to be: the handler copies bytes and
	// sets a content type. What matters is that something non-empty came back.
	return []byte("ID3-fake-audio-bytes"), nil
}

func (f *fakeVoice) last() voiceCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return voiceCall{}
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeVoice) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// broadcastApp is a real App with a real database, a real feed, four real
// items, and a fake voice — opted in to Smart+ and to broadcast mode.
//
// DevMode, so a header-less `?item=` request resolves to the seeded user. That
// is what lets these drive the handler directly rather than minting tickets, and
// the ticket path has its own tests.
func broadcastApp(t *testing.T) (*App, *fakeVoice, store.Scope, []string) {
	t.Helper()
	ctx := t.Context()

	a, err := Open(ctx, Config{
		DBPath: filepath.Join(t.TempDir(), "cast.db"), DevMode: true, Log: testLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	key, err := secret.NewKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	a.speechKey = key
	voice := &fakeVoice{}
	a.speak = voice

	if err := a.repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	feed, _, err := a.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:cast", FeedURL: "https://cast.example/feed", Title: "Cast Weekly",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	ingest := make([]store.IngestItem, 0, 4)
	for _, s := range []struct{ guid, title, body string }{
		{"g1", "Fsyncgate, ten years on", "<p>The first story body, with enough words to be spoken.</p>"},
		{"g2", "Postgres durability revisited", "<p>The second story body.</p>"},
		{"g3", "A third thing entirely", "<p>The third story body.</p>"},
		{"g4", "And a fourth", "<p>The fourth story body.</p>"},
	} {
		ingest = append(ingest, store.IngestItem{
			GUID: s.guid, URL: "https://cast.example/" + s.guid, DupeKey: s.guid,
			Title: s.title, ContentHTML: s.body,
			PublishedAt: time.Now().UTC().Add(-time.Hour),
		})
	}
	if _, err := a.repo.IngestItems(ctx, feed.SourceID, ingest); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if err := a.repo.SetPrefs(ctx, sc, store.Prefs{
		ttsPrefKey:     "true",
		podcastPrefKey: "true",
	}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	return a, voice, sc, itemIDs(t, a, sc, 4)
}

// itemIDs reads the seeded items back, so the tests use ids the database
// actually minted rather than ones they invented.
// least is how many the caller expects, because the second tenant in the
// isolation test legitimately has one.
func itemIDs(t *testing.T, a *App, sc store.Scope, least int) []string {
	t.Helper()
	items, _, err := a.repo.ListItems(t.Context(), sc, store.ListQuery{Limit: 20})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) < least {
		t.Fatalf("seeded %d items, want at least %d", len(items), least)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// speak drives the handler and returns the recorder.
func speak(t *testing.T, a *App, query string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodGet, "/speech?"+query, nil))
	return rec
}

// **The regression test for "it stopped playing".**
//
// Broadcast mode on, with every parameter the client now sends: the handover,
// the listener's clock, the queue size and the headline run-through. If any of
// them makes the handler answer anything but audio, the reader hears silence and
// the display sits on a title card with no way to tell why.
func TestBroadcastModeReturnsAudioWithEverythingTheClientSends(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(prevItemParam, ids[0])
	q.Set(openNowParam, "2026-07-27T08:30:00-04:00")
	q.Set(openStoriesParam, "11")
	q.Set(openLineupParam, strings.Join(ids, ","))

	rec := speak(t, a, q.Encode())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", got)
	}
	if rec.Body.Len() == 0 {
		t.Error("200 with an empty body — the reader hears silence")
	}
	if voice.count() != 1 {
		t.Fatalf("the voice was asked %d times, want 1", voice.count())
	}
	// Private, never public: this is one reader's article read aloud, and a
	// shared cache holding it would serve it to whoever asked next.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control = %q, want it private", cc)
	}
}

// Each opening parameter on its own, and none of them may turn a working
// request into a failing one. These are HINTS for a greeting; a malformed one
// must cost a nicety, never the audio.
func TestOpeningParametersNeverBreakPlayback(t *testing.T) {
	a, _, _, ids := broadcastApp(t)

	for _, c := range []struct{ name, extra string }{
		{"nothing but the item", ""},
		{"a clock", "&now=" + url.QueryEscape("2026-07-27T08:30:00-04:00")},
		{"a clock that is not a time", "&now=half+past+tuesday"},
		{"an empty clock", "&now="},
		{"a count", "&n=11"},
		{"an absurd count", "&n=99999999"},
		{"a negative count", "&n=-4"},
		{"a count that is not a number", "&n=eleven"},
		{"a run-through", "&q=" + strings.Join(ids, ",")},
		{"a run-through with unknown ids", "&q=nope,also-nope"},
		{"a run-through with empty entries", "&q=,,," + ids[0] + ",,"},
		{"a run-through far past the cap", "&q=" + strings.Repeat(ids[0]+",", 40)},
		{"a handover", "&p=" + ids[0]},
		{"a handover naming this same story", "&p=" + ids[1]},
		{"a handover that does not exist", "&p=nope"},
		{"everything at once", "&p=" + ids[0] + "&n=4&now=" +
			url.QueryEscape("2026-07-27T20:00:00Z") + "&q=" + strings.Join(ids, ",")},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := speak(t, a, "item="+ids[1]+c.extra)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if rec.Body.Len() == 0 {
				t.Error("no audio came back")
			}
		})
	}
}

// With no Smart+ key the broadcast writer cannot write anything, and the reader
// must still hear the article. Silence is the one answer that is never
// acceptable, and the cache key has to fall back WITH the text — a key that said
// "podcast" over the plain article would poison the cache for the day the writer
// starts working.
func TestBroadcastFallsBackToTheArticleAndKeysItThatWay(t *testing.T) {
	a, voice, sc, ids := broadcastApp(t)

	// Read the item back rather than assuming which one ids[0] is: ListItems
	// orders newest first, and four stories ingested in the same second are in
	// whatever order the store settled on. A test that hard-codes the answer to
	// that is a test about the store's ordering wearing a broadcast's clothes.
	it, err := a.repo.GetItem(t.Context(), sc, ids[0])
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}

	rec := speak(t, a, "item="+ids[0]+"&p="+ids[1])
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := voice.last()
	if !strings.Contains(got.text, "story body") {
		t.Errorf("the article was not read aloud: %q", got.text)
	}
	// The source and the title lead, because a voice starting mid-article gives
	// the listener nothing to hang it on.
	if !strings.HasPrefix(got.text, "From Cast Weekly. "+it.Title) {
		t.Errorf("the announcement is missing or wrong: %q", got.text)
	}
	if got.key != ids[0] {
		t.Errorf("cache key = %q, want the plain item id %q", got.key, ids[0])
	}
}

// The run-through is resolved through the reader's OWN SCOPE, which is what
// makes a caller-supplied list of ids safe. Another tenant's story must be
// invisible here for exactly the reason it is invisible everywhere else.
func TestTheRunThroughCannotReachAnotherTenant(t *testing.T) {
	a, _, sc, ids := broadcastApp(t)
	ctx := t.Context()

	if err := a.repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t2", Name: "Other", UserID: "u2", Username: "stranger",
		Hash: "x", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("second tenant: %v", err)
	}
	other := store.Scope{TenantID: "t2", UserID: "u2", Role: "member"}
	feed, _, err := a.repo.Subscribe(ctx, other, store.NewSubscription{
		NaturalKey: "feed:secret", FeedURL: "https://secret.example/feed", Title: "Secret",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := a.repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
		GUID: "s1", URL: "https://secret.example/s1", DupeKey: "s1",
		Title:       "A HEADLINE THE FIRST READER MUST NEVER HEAR",
		ContentHTML: "<p>secret</p>", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	stranger := itemIDs(t, a, other, 1)

	// The first reader asks for the stranger's story in their run-through.
	lineup := a.lineupFrom(ctx, sc, strings.Join([]string{ids[0], stranger[0], ids[1]}, ","))
	for _, h := range lineup {
		if strings.Contains(h.Title, "MUST NEVER HEAR") {
			t.Fatal("another tenant's headline reached the run-through")
		}
	}
	// And the reader's own two are still there — the foreign id is skipped, not
	// fatal, because one swept-away story must not silence a broadcast.
	if len(lineup) != 2 {
		t.Errorf("lineup has %d entries, want the reader's own 2", len(lineup))
	}
}

// The cap is enforced on the SERVER, not trusted to the client. A bulletin that
// recites forty headlines before covering any of them has told the listener
// nothing they can keep — and it is forty database reads per broadcast.
func TestTheRunThroughIsCapped(t *testing.T) {
	a, _, sc, ids := broadcastApp(t)
	many := strings.Repeat(ids[0]+",", 50)
	if got := len(a.lineupFrom(t.Context(), sc, many)); got > 5 {
		t.Errorf("the run-through resolved %d headlines, want at most 5", got)
	}
}

// A story with no headline cannot be run through, and "and something from Cast
// Weekly" is not a headline.
func TestTheRunThroughSkipsUntitledStories(t *testing.T) {
	a, _, sc, _ := broadcastApp(t)
	ctx := t.Context()

	feed, _, err := a.repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:untitled", FeedURL: "https://untitled.example/feed", Title: "Untitled",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := a.repo.IngestItems(ctx, feed.SourceID, []store.IngestItem{{
		GUID: "u1", URL: "https://untitled.example/u1", DupeKey: "u1",
		Title: "", ContentHTML: "<p>body</p>", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	for _, h := range a.lineupFrom(ctx, sc, strings.Join(itemIDs(t, a, sc, 4), ",")) {
		if strings.TrimSpace(h.Title) == "" {
			t.Error("an untitled story reached the run-through")
		}
	}
}

// A provider that is down must not be cached as a success, and must be reported
// as a gateway failure rather than as our own — the reader can retry a 502 and
// cannot do anything about a 500.
func TestSynthesisFailureIsAGatewayError(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)
	voice.fail = context.DeadlineExceeded

	rec := speak(t, a, "item="+ids[0])
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502: %s", rec.Code, rec.Body.String())
	}
	// The provider's own message can echo the reader's article, so it goes to
	// the log and never to the response (§22.11).
	if strings.Contains(rec.Body.String(), "deadline") {
		t.Errorf("the provider's error text reached the reader: %s", rec.Body.String())
	}
}

// HEAD is how a browser's audio element probes before it streams. It must
// answer with the same headers and no body — a HEAD that 405s stops playback
// before it starts.
func TestHeadIsAnsweredWithoutABody(t *testing.T) {
	a, _, _, ids := broadcastApp(t)

	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodHead, "/speech?item="+ids[0], nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned %d bytes of body", rec.Body.Len())
	}
}

// An instance whose key is removed at runtime stops minting and stops speaking,
// without a restart — which is the whole reason Configured is consulted per
// request rather than at boot.
func TestAKeyRemovedAtRuntimeStopsPlayback(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)

	if rec := speak(t, a, "item="+ids[0]); rec.Code != http.StatusOK {
		t.Fatalf("status %d before the key went away", rec.Code)
	}
	voice.off = true
	rec := speak(t, a, "item="+ids[0])
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501 once the instance has no key: %s",
			rec.Code, rec.Body.String())
	}
}

// Turning broadcast mode OFF must go back to reading the article, with the
// plain cache key — otherwise the audio cache keeps serving the last thing the
// mode produced.
func TestTurningBroadcastOffGoesBackToTheArticle(t *testing.T) {
	a, voice, sc, ids := broadcastApp(t)

	if err := a.repo.SetPrefs(t.Context(), sc, store.Prefs{podcastPrefKey: "false"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	rec := speak(t, a, "item="+ids[0]+"&p="+ids[1]+"&q="+strings.Join(ids, ","))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if got := voice.last().key; got != ids[0] {
		t.Errorf("cache key = %q, want the plain item id", got)
	}
}

// --- the split opening ----------------------------------------------------------
//
// The greeting became its own recording so the theme music has an end to be
// timed against (§19). Two things have to hold, and the second is the one that
// would be embarrassing: the flag must never break playback, and a broadcast
// must never greet the listener twice.

func TestTheOpeningIsDecidedByTheRequestNotByGuessing(t *testing.T) {
	for _, c := range []struct {
		name    string
		intro   string
		hasPrev bool
		want    bool
	}{
		{"top of the show", "", false, true},
		{"asking for the opening alone", introOnly, false, true},
		{"the first story after a recorded opening", introDone, false, false},
		{"mid-broadcast", "", true, false},
		// A predecessor wins over anything the flag says: a story with something
		// before it is not the top of the show whatever a client claims.
		{"mid-broadcast with a stale flag", introOnly, true, false},
		// Anything unrecognised is the old behaviour rather than a refusal. The
		// worst case for a wrong answer here is a greeting, and the worst case
		// for a strict one is silence.
		{"nonsense", "banana", false, true},
	} {
		if got := wantsOpening(c.intro, c.hasPrev); got != c.want {
			t.Errorf("%s: wantsOpening(%q, %v) = %v", c.name, c.intro, c.hasPrev, got)
		}
	}

	if !isIntroRequest(introOnly, true) {
		t.Error("a broadcast asking for the opening alone was not recognised")
	}
	// Without broadcast mode there is no writer, so there is no greeting to
	// record — the flag has to mean nothing rather than something unwritable.
	if isIntroRequest(introOnly, false) {
		t.Error("read-to-me without a broadcast was treated as an opening request")
	}
	if isIntroRequest(introDone, true) {
		t.Error("the already-recorded marker was read as a request for one")
	}
}

// Neither half of the split may turn a working request into a failing one. This
// server has no writer configured, so both fall back to reading the article —
// which is exactly the path a reader hits when the model is down, and it has to
// end in audio either way.
func TestTheSplitOpeningNeverBreaksPlayback(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)

	for _, c := range []struct{ name, extra string }{
		{"the opening alone", "&" + introParam + "=1"},
		{"the first story after it", "&" + introParam + "=0"},
		{"a flag this server does not know", "&" + introParam + "=banana"},
	} {
		t.Run(c.name, func(t *testing.T) {
			before := voice.count()
			q := "item=" + ids[0] +
				"&" + openNowParam + "=2026-07-27T08:30:00-04:00" +
				"&" + openStoriesParam + "=4" +
				"&" + openLineupParam + "=" + strings.Join(ids, ",") + c.extra
			rec := speak(t, a, q)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			if rec.Body.Len() == 0 {
				t.Error("200 with an empty body — the reader hears silence")
			}
			if voice.count() == before {
				t.Error("nothing was synthesised")
			}
		})
	}
}
