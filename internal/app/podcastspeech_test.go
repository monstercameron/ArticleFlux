package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"

	"github.com/monstercameron/schemaflux/schemafluxtest"
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
	// The plain read's key, which carries speechRev: what the plain voice says
	// has changed at least once (it stopped announcing the publication), and a
	// key that did not move with it would answer the new words with a recording
	// of the old ones. What matters here is that it is NOT a broadcast key.
	want := ids[0] + "#" + speechRev
	if got := voice.last().key; got != want {
		t.Errorf("cache key = %q, want the plain read's key %q", got, want)
	}
	// And the plain read is the item itself: no station identifier, because
	// nothing about this request is a programme any more.
	if got := voice.last().text; strings.Contains(got, "From Cast Weekly") {
		t.Errorf("broadcast off still announced the publication: %q", got)
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

// --- the sign-off (§19) ------------------------------------------------------------
//
// A programme ends; a queue stops. `&i=2` is the request that buys the
// difference, and the failure that matters most is not it being missing — it is
// it returning the LAST STORY AGAIN, which is not a degraded goodbye but a bug
// with a voice.

// fakeWriter is the llmClient seam from internal/smart, satisfied from here.
//
// Possible because both methods on that unexported interface are exported names,
// which is what lets this test drive the real Podcast writer — the prompt, the
// cache key, the cleanup — instead of asserting against a stub of it.
// The writer runs on typed operations now (plan P3.9), so the ANSWER and the
// record of what was asked both live on a schemafluxtest provider rather than
// on Do. What is left on this seam is Configured(), which is still the gate the
// call passes through, and which is still worth satisfying from here so these
// tests drive the real Podcast writer end to end.
type fakeWriter struct {
	prov *schemafluxtest.Provider
}

func (f *fakeWriter) Configured(context.Context) bool { return true }

// OpsContext returns the context untouched, for the reason the fake in
// internal/smart does: the real client puts ArticleFlux's own provider on the
// context and SchemaFlux prefers that over anything registered globally, which
// would defeat a test-installed provider.
func (f *fakeWriter) OpsContext(ctx context.Context) context.Context { return ctx }

// Do is unreachable on the paths these tests exercise and says so, rather than
// answering plausibly: a silent fallback here would let a feature that stopped
// using its operation keep passing.
func (f *fakeWriter) Do(context.Context, llm.Request) (string, error) {
	return "", errors.New("app: the podcast writer must run through its typed operation, not Do")
}

// seen returns the last request the provider was actually sent and how many
// there have been. The prompt halves are joined because SchemaFlux decides
// which one carries the brief — see internal/smart's requestSent.
func (f *fakeWriter) seen() (string, int) {
	reqs := f.prov.Requests()
	if len(reqs) == 0 {
		return "", 0
	}
	last := reqs[len(reqs)-1]
	return last.SystemPrompt + last.UserPrompt, len(reqs)
}

// withWriter swaps in a podcast writer that always succeeds.
func withWriter(t *testing.T, a *App) *fakeWriter {
	t.Helper()
	// Whatever it is asked for, it says goodbye. The point of these tests is
	// which prompt arrives and what the handler does with the answer, not
	// whether a model can write.
	prov := schemafluxtest.New().Shaped().Reply("That's the lot. Back when there's more.")
	schemafluxtest.Install(t, prov)
	w := &fakeWriter{prov: prov}
	a.podcast = smart.NewPodcast(w, a.settings, t.TempDir())
	return w
}

func TestSignOffIsItsOwnRecordingAndNotTheStoryAgain(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)
	w := withWriter(t, a)

	rec := speak(t, a, "item="+ids[0]+"&i=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("the sign-off answered %d, want 200", rec.Code)
	}

	req, n := w.seen()
	if n != 1 {
		t.Fatalf("the writer was called %d times, want 1", n)
	}
	if !strings.Contains(req, "THE SIGN-OFF ONLY") {
		t.Errorf("the sign-off did not get its own instructions:\n%s", req)
	}
	// THE regression. The article body must not reach the model, because a model
	// handed an article covers it — and the listener would hear the story they
	// just finished, told again, as the programme's farewell.
	//
	// Matched on "story body", which every seeded item shares, rather than on one
	// item's text: itemIDs returns newest-first, so naming a particular body here
	// asserts against whichever story the ordering happens to put at ids[0] and
	// passes for free when that is not the one being requested.
	if strings.Contains(req, "story body") {
		t.Errorf("the sign-off was given the article body:\n%s", req)
	}
	// And what was actually synthesised is the goodbye, not the article.
	spoken := voice.last()
	if !strings.Contains(spoken.text, "That's the lot") {
		t.Errorf("the sign-off synthesised %q", spoken.text)
	}
	if strings.Contains(spoken.text, "story body") {
		t.Errorf("the sign-off synthesised the article: %q", spoken.text)
	}
	// Its own cache entry. Sharing one with the story would serve the goodbye as
	// the story or the story as the goodbye, and the audio cache is keyed off it.
	if !strings.HasSuffix(spoken.key, "#outro") {
		t.Errorf("the sign-off was cached as %q, which is not its own entry", spoken.key)
	}
}

// Broadcast mode gates it, exactly as it gates the opening. With the writer off
// there is no programme to end, and `&i=2` must not become a way to get a
// different rendering of an article.
func TestSignOffWithoutBroadcastModeReadsTheArticle(t *testing.T) {
	a, voice, sc, ids := broadcastApp(t)
	withWriter(t, a)
	if err := a.repo.SetPrefs(t.Context(), sc, store.Prefs{podcastPrefKey: "false"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}
	rec := speak(t, a, "item="+ids[0]+"&i=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("answered %d, want 200 and the article", rec.Code)
	}
	// "story body" rather than a named one, for the reason above: ids[0] is
	// whichever story the newest-first ordering puts there.
	spoken := voice.last()
	if !strings.Contains(spoken.text, "story body") {
		t.Errorf("with broadcast off, `i=2` did not read the article: %q", spoken.text)
	}
	if strings.Contains(spoken.text, "That's the lot") {
		t.Errorf("with broadcast off, `i=2` still produced a sign-off: %q", spoken.text)
	}
}

// A sign-off the writer cannot produce ends the programme QUIETLY.
//
// Every other broadcast failure falls back to reading the article, and that is
// right — the listener asked to hear the story. Here it would be the last story
// a second time. 204 rather than 422 because nothing went wrong: the request was
// understood, and the answer is that this programme ends without a goodbye.
func TestSignOffThatCannotBeWrittenEndsInSilenceNotInTheArticle(t *testing.T) {
	// No writer swap: the App's own podcast has no key, so Segment refuses.
	a, voice, _, ids := broadcastApp(t)
	before := voice.count()

	rec := speak(t, a, "item="+ids[0]+"&i=2")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("an unwritable sign-off answered %d, want 204", rec.Code)
	}
	if voice.count() != before {
		t.Errorf("an unwritable sign-off synthesised something anyway: %q", voice.last().text)
	}
}

// The flag is read from the same parameter as the opening, so the three states
// have to stay told apart. `i=1` opens, `i=0` says the opening is already
// recorded, `i=2` closes — and a mix-up here is a broadcast that greets the
// listener when it meant to say goodbye.
func TestTheOpeningAndTheSignOffAreNotConfused(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)
	w := withWriter(t, a)

	if rec := speak(t, a, "item="+ids[0]+"&i=1&n=4"); rec.Code != http.StatusOK {
		t.Fatalf("the opening answered %d", rec.Code)
	}
	openReq, _ := w.seen()
	openKey := voice.last().key

	if rec := speak(t, a, "item="+ids[0]+"&i=2&n=4"); rec.Code != http.StatusOK {
		t.Fatalf("the sign-off answered %d", rec.Code)
	}
	closeReq, _ := w.seen()
	closeKey := voice.last().key

	if strings.Contains(openReq, "SIGN-OFF") {
		t.Error("the opening was given the sign-off's instructions")
	}
	if strings.Contains(closeReq, "THE OPENING ONLY") {
		t.Error("the sign-off was given the opening's instructions")
	}
	if openKey == closeKey {
		t.Errorf("the opening and the sign-off share the audio cache entry %q", openKey)
	}
}

// isCloseRequest is the gate, and it is a pure function so the three states can
// be checked without a database behind them.
func TestIsCloseRequest(t *testing.T) {
	for _, tc := range []struct {
		intro   string
		podcast bool
		want    bool
	}{
		{"2", true, true},
		{" 2 ", true, true}, // trimmed, like every other value here
		{"2", false, false}, // broadcast off: no programme to end
		{"1", true, false},  // that is the opening
		{"0", true, false},  // that is "already opened"
		{"", true, false},   // an ordinary segment
		{"22", true, false}, // not a prefix match
	} {
		if got := isCloseRequest(tc.intro, tc.podcast); got != tc.want {
			t.Errorf("isCloseRequest(%q, %v) = %v, want %v",
				tc.intro, tc.podcast, got, tc.want)
		}
	}
}

// **The regression test for "I pressed play on one article and got a radio show".**
//
// Broadcast mode is ON for this account — it is what broadcastApp sets — and the
// request is a plain listen: a sealed ticket and nothing else. No handover, no
// clock, no story count, no run-through, because the reader did not open a
// programme; they pressed play on one article in their feed.
//
// Before this, the preference alone selected the broadcast writer, so the
// listener got a greeting and the date in front of an article retold rather than
// read. Now the request decides too (castRequest), and the two answers are told
// apart by the KEY as much as by the words: a programme falling back to the
// article reads it the announced way under `<id>`, and this is neither.
func TestAPlainListenIsNotABroadcastEvenWithTheSwitchOn(t *testing.T) {
	a, voice, sc, ids := broadcastApp(t)

	it, err := a.repo.GetItem(t.Context(), sc, ids[0])
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}

	rec := speak(t, a, "item="+ids[0])
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := voice.last()

	// The item, and only the item: its headline, then its content.
	if !strings.HasPrefix(got.text, it.Title+".") {
		t.Errorf("a plain listen did not open on the headline: %q", got.text)
	}
	if !strings.Contains(got.text, "story body") {
		t.Errorf("a plain listen lost the content: %q", got.text)
	}
	// Nothing this application wrote. No station identifier, and nothing that
	// only a broadcast has.
	for _, bad := range []string{"From Cast Weekly", "Good morning", "Good afternoon",
		"Good evening", "coming up"} {
		if strings.Contains(got.text, bad) {
			t.Errorf("a plain listen carried %q: %q", bad, got.text)
		}
	}
	// The plain read's key. `<id>` alone would be the broadcast's fallback, which
	// is a different rendering of the same article and must not be served for
	// this one.
	if want := ids[0] + "#" + speechRev; got.key != want {
		t.Errorf("cache key = %q, want the plain read's key %q", got.key, want)
	}
}
