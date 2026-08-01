package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/tts"
)

// speechApp is the smallest App that can mint and open a listening ticket.
//
// The tts client is real but keyless unless a key is given: Configured() is one
// of the things SpeechURL checks, and faking it would test a different function
// than the one that runs.
func speechApp(t *testing.T, apiKey string) *App {
	t.Helper()
	key, err := secret.NewKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	a := &App{
		cfg:       Config{Log: testLogger()},
		log:       testLogger(),
		speechKey: key,
		tts: tts.New(t.TempDir(), func(context.Context) string {
			return apiKey
		}),
	}
	// The listening path calls the narrow seam, so a fixture that set only the
	// concrete client would answer 501 to everything past the gates.
	a.speak = a.tts
	return a
}

func testScope() store.Scope {
	return store.Scope{TenantID: "tenant-1", UserID: "user-1", Role: "member"}
}

// The round trip: what SpeechURL mints, openSpeechTicket must open back into the
// same scope and the same item.
func TestSpeechTicketRoundTrip(t *testing.T) {
	a := speechApp(t, "sk-test")
	sc := testScope()

	minted := a.SpeechURL(context.Background(), sc, "item-1")
	if minted == "" {
		t.Fatal("SpeechURL returned nothing for a configured instance")
	}

	u, err := url.Parse(minted)
	if err != nil {
		t.Fatalf("minted URL does not parse: %v", err)
	}
	if u.Path != "/speech" {
		t.Fatalf("minted path = %q", u.Path)
	}

	got, item, err := a.openSpeechTicket(u.Query().Get("t"))
	if err != nil {
		t.Fatalf("openSpeechTicket: %v", err)
	}
	if got != sc {
		t.Fatalf("scope = %+v, want %+v", got, sc)
	}
	if item != "item-1" {
		t.Fatalf("item = %q, want item-1", item)
	}
}

// The reason this is sealed rather than signed: an <audio src> lands in browser
// history, in the referrer and in every access log between here and the
// listener. A tenant or user id in the clear there would tell all of them who
// was reading.
func TestSpeechTicketLeaksNoIdentity(t *testing.T) {
	a := speechApp(t, "sk-test")
	minted := a.SpeechURL(context.Background(), testScope(), "item-1")

	for _, secretish := range []string{"tenant-1", "user-1", "item-1", "member"} {
		if strings.Contains(minted, secretish) {
			t.Errorf("the minted URL contains %q in the clear: %s", secretish, minted)
		}
	}
}

// A ticket must not survive being edited. AES-GCM is what makes this a decode
// failure rather than a verification against some other message.
func TestTamperedTicketWillNotOpen(t *testing.T) {
	a := speechApp(t, "sk-test")
	u, _ := url.Parse(a.SpeechURL(context.Background(), testScope(), "item-1"))
	tok := u.Query().Get("t")

	// Flip a character in the middle of the ciphertext.
	mid := len(tok) / 2
	swap := byte('A')
	if tok[mid] == 'A' {
		swap = 'B'
	}
	tampered := tok[:mid] + string(swap) + tok[mid+1:]

	if _, _, err := a.openSpeechTicket(tampered); err == nil {
		t.Fatal("a tampered ticket opened")
	}
	if _, _, err := a.openSpeechTicket(""); err == nil {
		t.Fatal("an empty ticket opened")
	}
	if _, _, err := a.openSpeechTicket("not base64 at all !!"); err == nil {
		t.Fatal("garbage opened")
	}
}

// A ticket minted by one instance must be worthless at another. This is what
// stops a listening link from being portable between deployments.
func TestTicketFromAnotherInstanceWillNotOpen(t *testing.T) {
	a := speechApp(t, "sk-test")
	b := speechApp(t, "sk-test")

	u, _ := url.Parse(a.SpeechURL(context.Background(), testScope(), "item-1"))
	if _, _, err := b.openSpeechTicket(u.Query().Get("t")); err == nil {
		t.Fatal("another instance's key opened this ticket")
	}
}

// The expiry is inside the sealed payload, so a client cannot extend it. The
// only way to prove that is to seal an expired one with the real key.
func TestExpiredTicketWillNotOpen(t *testing.T) {
	a := speechApp(t, "sk-test")
	sc := testScope()

	past := time.Now().Add(-time.Minute).Unix()
	sealed, err := secret.Encrypt(a.speechKey, []byte(speechTicket(sc, "item-1", past)))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, _, err := a.openSpeechTicket(sealed); err == nil {
		t.Fatal("an expired ticket opened")
	}

	// And one that has not expired does, so the test above is about the expiry
	// rather than about the hand-sealed payload being malformed.
	future := time.Now().Add(time.Minute).Unix()
	ok, _ := secret.Encrypt(a.speechKey, []byte(speechTicket(sc, "item-1", future)))
	if _, _, err := a.openSpeechTicket(ok); err != nil {
		t.Fatalf("a live hand-sealed ticket did not open: %v", err)
	}
}

// The "speech" prefix is domain separation. Without it a payload sealed for some
// other purpose under a shared key would open here as a listening ticket.
func TestTicketOfAnotherKindWillNotOpen(t *testing.T) {
	a := speechApp(t, "sk-test")
	exp := time.Now().Add(time.Hour).Unix()
	wrong := "page\ntenant-1\nuser-1\nmember\nitem-1\n" +
		time.Unix(exp, 0).Format("20060102")
	sealed, _ := secret.Encrypt(a.speechKey, []byte(wrong))
	if _, _, err := a.openSpeechTicket(sealed); err == nil {
		t.Fatal("a ticket of another kind opened as speech")
	}
}

// An instance with no OpenAI key must mint nothing: an empty speech_url is how
// the client knows to leave the control on the browser's own synthesiser rather
// than offering an upgrade that answers 501.
func TestNoKeyMintsNoTicket(t *testing.T) {
	if got := speechApp(t, "").SpeechURL(context.Background(), testScope(), "item-1"); got != "" {
		t.Fatalf("an unconfigured instance minted %q", got)
	}
}

// Nothing incomplete gets a ticket. Each of these would otherwise produce a URL
// that fails later, at the point where it is hardest to explain.
func TestMintRefusesIncompleteInput(t *testing.T) {
	a := speechApp(t, "sk-test")
	ctx := context.Background()

	if got := a.SpeechURL(ctx, testScope(), ""); got != "" {
		t.Errorf("minted for an empty item: %q", got)
	}
	if got := a.SpeechURL(ctx, store.Scope{}, "item-1"); got != "" {
		t.Errorf("minted for an invalid scope: %q", got)
	}

	noKey := speechApp(t, "sk-test")
	noKey.speechKey = nil
	if got := noKey.SpeechURL(ctx, testScope(), "item-1"); got != "" {
		t.Errorf("minted without a ticket key: %q", got)
	}

	// Both fields, because the listening path reads the `speaker` seam and the
	// spend meter reads the concrete client. Clearing only one is an instance
	// that half exists, which is not a state Open can produce — and the test
	// would then be asserting about a shape nothing builds.
	noTTS := speechApp(t, "sk-test")
	noTTS.tts, noTTS.speak = nil, nil
	if got := noTTS.SpeechURL(ctx, testScope(), "item-1"); got != "" {
		t.Errorf("minted without a voice: %q", got)
	}
}

// Gate 1, the ticket half. A bad ticket is 403 and not 401: the caller did
// present a credential, and 401 invites a retry with a login it already has.
func TestServeSpeechRejectsABadTicket(t *testing.T) {
	a := speechApp(t, "sk-test")
	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodGet, "/speech?t=garbage", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	// The refusal must not say which part failed — that would make this an
	// oracle for probing what the key accepts.
	for _, leak := range []string{"expired ticket", "decrypt", "tenant", "signature"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), leak) {
			t.Errorf("the refusal names %q: %s", leak, rec.Body.String())
		}
	}
}

// Gate 1, the header half. No ticket and no credentials outside DevMode is 401.
func TestServeSpeechWithoutCredentials(t *testing.T) {
	a := speechApp(t, "sk-test")
	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodGet, "/speech?item=item-1", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

// A ticket names its own item, and an `item` parameter alongside it is ignored
// rather than honoured — pairing a valid ticket with someone else's id is the
// exact ambiguity a capability exists to remove.
func TestTicketNamesItsOwnItem(t *testing.T) {
	a := speechApp(t, "sk-test")
	u, _ := url.Parse(a.SpeechURL(context.Background(), testScope(), "item-1"))

	_, item, err := a.openSpeechTicket(u.Query().Get("t"))
	if err != nil {
		t.Fatalf("openSpeechTicket: %v", err)
	}
	if item != "item-1" {
		t.Fatalf("item = %q; the ticket did not name its own item", item)
	}
}

// speechAppWithUser is a real App (real repo, DevMode on) with a seeded
// tenant and user, for the two gates speechApp's repo-less fixture cannot
// reach: whether the item exists (gate 2, 404) and whether the account opted
// in (gate 4, 403). DevMode means a header-less `?item=` request resolves to
// this seeded user via devScope.
func speechAppWithUser(t *testing.T) (*App, store.Scope) {
	t.Helper()
	ctx := context.Background()
	a, err := Open(ctx, Config{
		DBPath: filepath.Join(t.TempDir(), "speech.db"), DevMode: true, Log: testLogger(),
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
	a.tts = tts.New(t.TempDir(), func(context.Context) string { return "sk-test" })
	a.speak = a.tts

	if err := a.repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "reader",
		Hash: "x", Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	return a, store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
}

// Gate 4 runs before gate 2 in the actual handler (opt-in is checked before
// the item is even looked up), and its own status is the reader's to fix —
// unlike gate 3's 501, this is "turn Smart+ speech on in settings". A caller
// who has never opted in must be refused here, before an item id is ever
// consulted, or the 403/404 boundary could be used to probe whether an id
// exists ahead of opting in.
func TestServeSpeechRejectsAnOptedOutAccount(t *testing.T) {
	a, _ := speechAppWithUser(t)

	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodGet, "/speech?item=whatever", nil))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for an account that has not opted in: %s",
			rec.Code, rec.Body.String())
	}
}

// Gate 2: an opted-in account asking for an item that does not exist (or is
// outside its scope) gets 404, never 403 — a permission error here would
// confirm the item exists to someone probing ids (§20.7). This only reaches
// the item lookup at all because the account IS opted in; an opted-out
// account asking for the same missing id must still get gate 4's 403, which
// TestServeSpeechRejectsAnOptedOutAccount pins.
func TestServeSpeechRejectsAnUnknownItemWithNotFoundNot403(t *testing.T) {
	a, sc := speechAppWithUser(t)
	if err := a.repo.SetPrefs(context.Background(), sc, store.Prefs{ttsPrefKey: "true"}); err != nil {
		t.Fatalf("SetPrefs: %v", err)
	}

	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodGet, "/speech?item=does-not-exist", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// Gate 3. No key is 501 and not 500: the server is working correctly and simply
// has nothing to synthesise with, which is the one thing the reader cannot fix.
func TestServeSpeechWithoutAKeyIs501(t *testing.T) {
	a := speechApp(t, "")
	// A ticket sealed by hand, because an unconfigured instance mints none.
	sealed, _ := secret.Encrypt(a.speechKey,
		[]byte(speechTicket(testScope(), "item-1", time.Now().Add(time.Hour).Unix())))

	rec := httptest.NewRecorder()
	a.serveSpeech(rec, httptest.NewRequest(http.MethodGet,
		"/speech?t="+url.QueryEscape(sealed), nil))

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501: %s", rec.Code, rec.Body.String())
	}
}

// Only GET and HEAD. A POST here would be a request to spend money with no
// capability shown.
func TestServeSpeechRejectsOtherMethods(t *testing.T) {
	a := speechApp(t, "sk-test")
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		a.serveSpeech(rec, httptest.NewRequest(m, "/speech?item=item-1", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", m, rec.Code)
		}
	}
}

// The URL must survive being put in an <audio src> and parsed back out.
// Encrypt returns standard base64, which contains `+` and `/`; a raw `+` in a
// query string decodes to a space and the ticket silently fails to open.
func TestMintedURLSurvivesQueryParsing(t *testing.T) {
	a := speechApp(t, "sk-test")
	// Several mints, because whether the ciphertext happens to contain a `+`
	// depends on the random nonce.
	for i := range 40 {
		minted := a.SpeechURL(context.Background(), testScope(), "item-1")
		req := httptest.NewRequest(http.MethodGet, minted, nil)
		if _, _, err := a.openSpeechTicket(req.URL.Query().Get("t")); err != nil {
			t.Fatalf("mint %d did not survive a round trip through the query string: %v\n%s",
				i, err, minted)
		}
	}
}

// --- what actually gets read aloud -------------------------------------------

// Markup is stripped rather than sent: the provider bills per character, and
// `<div class="wp-block-group">` is neither speech nor cheap.
func TestSpeechTextStripsMarkup(t *testing.T) {
	got := speechText(store.Item{
		Title:       "The Headline",
		ContentHTML: `<div class="wp-block-group"><p>First para.</p><p>Second &amp; last.</p></div>`,
	})
	for _, bad := range []string{"<", ">", "wp-block-group", "&amp;"} {
		if strings.Contains(got, bad) {
			t.Errorf("markup survived (%q): %q", bad, got)
		}
	}
	for _, want := range []string{"The Headline", "First para.", "Second & last."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in: %q", want, got)
		}
	}
	// The title leads, so a voice starting mid-article gives the listener
	// something to hang it on.
	if !strings.HasPrefix(got, "The Headline") {
		t.Errorf("the title does not lead: %q", got)
	}
}

// A continuous session plays one article after another with no visual, so a
// segment that opens with the headline alone tells the listener what it is
// called but not where it came from — which is most of how anyone decides
// whether to keep listening.
func TestSpeechIntroNamesTheSource(t *testing.T) {
	got := speechIntro(store.Item{SourceTitle: "Hacker News", Title: "Fsyncgate"})
	if got != "From Hacker News. Fsyncgate." {
		t.Fatalf("intro = %q", got)
	}
	// A full stop rather than a colon, because a synthesiser reads a colon as a
	// pause of no particular length and the two run together.
	if strings.Contains(got, ":") {
		t.Errorf("the intro uses a colon, which does not survive being spoken: %q", got)
	}
}

// Every combination has to produce something sayable. A feed with no title and
// an item with no headline are both real, and "From ." read aloud is a fault.
func TestSpeechIntroDegradesCleanly(t *testing.T) {
	cases := []struct{ source, title, want string }{
		{"Hacker News", "Fsyncgate", "From Hacker News. Fsyncgate."},
		{"", "Fsyncgate", "Fsyncgate."},
		{"Hacker News", "", "From Hacker News."},
		{"", "", ""},
		{"  ", "  ", ""},
	}
	for _, c := range cases {
		got := speechIntro(store.Item{SourceTitle: c.source, Title: c.title})
		if got != c.want {
			t.Errorf("speechIntro(%q, %q) = %q, want %q", c.source, c.title, got, c.want)
		}
	}
}

// The summariser is fed the article and nothing else. Handed the announcement
// too, a model will dutifully summarise the fact that it came from Hacker News.
func TestSpeechBodyExcludesTheAnnouncement(t *testing.T) {
	it := store.Item{
		SourceTitle: "Hacker News",
		Title:       "Fsyncgate",
		ContentHTML: "<p>The kernel drops the page.</p>",
	}
	body := speechBody(it)
	if strings.Contains(body, "From Hacker News") {
		t.Errorf("the announcement leaked into the summariser's input: %q", body)
	}
	if strings.Contains(body, "Fsyncgate") {
		t.Errorf("the headline leaked into the body: %q", body)
	}
	if !strings.Contains(body, "The kernel drops the page.") {
		t.Errorf("the body is missing its own text: %q", body)
	}

	// And speechText is the two of them joined, in that order.
	full := speechText(it)
	if !strings.HasPrefix(full, "From Hacker News. Fsyncgate.") {
		t.Errorf("speechText does not lead with the announcement: %q", full)
	}
	if !strings.Contains(full, "The kernel drops the page.") {
		t.Errorf("speechText lost the body: %q", full)
	}
}

// Block tags become paragraph breaks, or sentences run into each other once the
// tags are gone: "First para.Second" is read as one word.
func TestSpeechTextSeparatesBlocks(t *testing.T) {
	got := speechText(store.Item{ContentHTML: `<p>One.</p><p>Two.</p>`})
	if strings.Contains(got, "One.Two.") {
		t.Errorf("blocks ran together: %q", got)
	}
}

// An item with no body falls back to the summary rather than being silent.
func TestSpeechTextFallsBackToSummary(t *testing.T) {
	got := speechText(store.Item{Title: "T", Summary: "Just the summary."})
	if !strings.Contains(got, "Just the summary.") {
		t.Errorf("summary not used: %q", got)
	}
}

// Nothing to read aloud must be detectable, so the handler can answer 422
// rather than paying for an empty synthesis.
func TestSpeechTextEmptyItem(t *testing.T) {
	if got := speechText(store.Item{}); got != "" {
		t.Errorf("an empty item produced %q", got)
	}
}

// --- the writers, configured and not -----------------------------------------

// digestFor with no digest writer wired (an instance with no Smart+ key) must
// say so with the same sentinel a configured-but-empty item gets, so the
// caller's single fallback path handles both.
func TestDigestForWithNoWriterConfigured(t *testing.T) {
	a := speechApp(t, "sk-test")
	if _, err := a.digestFor(context.Background(), store.Item{ID: "x", ContentHTML: "<p>body</p>"}); !errors.Is(err, smart.ErrNothingToSummarise) {
		t.Errorf("err = %v, want ErrNothingToSummarise", err)
	}
}

// With a digest writer actually wired but no Smart+ key configured, the LLM
// seam itself refuses — deterministically and without a network call — which
// is what proves the a.digest != nil path is reached at all.
func TestDigestForWithAWriterButNoKeyIsAnLLMError(t *testing.T) {
	a := speechApp(t, "") // unconfigured: llm.Configured() is false
	a.digest = smart.NewDigest(a.llm, a.settings, t.TempDir())
	_, err := a.digestFor(context.Background(), store.Item{ID: "x", ContentHTML: "<p>Enough body text to summarise.</p>"})
	if err == nil {
		t.Fatal("digestFor succeeded with no Smart+ key configured")
	}
}

func TestPodcastIntroAndOutroWithNoWriterConfigured(t *testing.T) {
	a := speechApp(t, "sk-test")
	it := store.Item{ID: "x", Title: "T", SourceTitle: "S"}
	if _, err := a.podcastIntro(context.Background(), it, "calm", nil); !errors.Is(err, smart.ErrNothingToSummarise) {
		t.Errorf("podcastIntro err = %v, want ErrNothingToSummarise", err)
	}
	if _, err := a.podcastOutro(context.Background(), it, "calm", nil); !errors.Is(err, smart.ErrNothingToSummarise) {
		t.Errorf("podcastOutro err = %v, want ErrNothingToSummarise", err)
	}
}

// With a podcast writer wired but no key, both still fail deterministically
// with no network call — proving the a.podcast != nil path is reached.
func TestPodcastIntroAndOutroWithAWriterButNoKey(t *testing.T) {
	a := speechApp(t, "")
	a.podcast = smart.NewPodcast(a.llm, a.settings, t.TempDir())
	it := store.Item{ID: "x", Title: "T", SourceTitle: "S"}

	// An intro with a real lineup gets past its own precondition and then
	// meets the missing key.
	open := &smart.Opening{PartOfDay: "morning", Date: "Monday", Stories: 3,
		Lineup: []smart.Headline{{Source: "S", Title: "Headline"}}}
	if _, err := a.podcastIntro(context.Background(), it, "calm", open); err == nil {
		t.Error("podcastIntro succeeded with no Smart+ key configured")
	}
	if _, err := a.podcastOutro(context.Background(), it, "calm", nil); err == nil {
		t.Error("podcastOutro succeeded with no Smart+ key configured")
	}
}

// speechScript falls back to the plain article not just when nothing CAN
// rewrite it (the writers are nil), but also when a writer IS wired and
// fails for a reason other than "nothing to summarise" — an expired key, a
// provider outage. Both must land the reader on the article, and the two
// failure shapes go through different branches in speechScript.
func TestSpeechScriptFallsBackWhenAWiredWriterFails(t *testing.T) {
	a := speechApp(t, "") // no Smart+ key: every writer call fails deterministically
	a.digest = smart.NewDigest(a.llm, a.settings, t.TempDir())
	a.podcast = smart.NewPodcast(a.llm, a.settings, t.TempDir())
	it := store.Item{ID: "item-2", Title: "Fsyncgate", SourceTitle: "LWN", ContentHTML: "<p>The body.</p>"}
	prev := store.Item{ID: "item-1", Title: "Postgres", SourceTitle: "Hacker News"}

	text, key := a.speechScript(context.Background(), map[string]string{digestPrefKey: "true"},
		it, prev, nil, false, false, false)
	if !strings.Contains(text, "The body.") {
		t.Errorf("digest configured-but-failing: article was not read aloud: %q", text)
	}
	if key != it.ID {
		t.Errorf("digest configured-but-failing: cache key = %q, want the plain item id", key)
	}

	text2, key2 := a.speechScript(context.Background(), map[string]string{podcastPrefKey: "true"},
		it, prev, nil, false, false, false)
	if !strings.Contains(text2, "The body.") {
		t.Errorf("podcast configured-but-failing: article was not read aloud: %q", text2)
	}
	if key2 != it.ID {
		t.Errorf("podcast configured-but-failing: cache key = %q, want the plain item id", key2)
	}
}

// speechScope reads the Authorization header rather than a query parameter —
// this URL ends up in an <audio src>, which means browser history, referrers
// and every access log between here and the listener.
func TestSpeechScopeReadsTheAuthorizationHeader(t *testing.T) {
	a, _ := speechAppWithUser(t)

	// No header at all falls back to the dev account.
	noHeader := httptest.NewRequest(http.MethodGet, "/speech?item=x", nil)
	if _, err := a.speechScope(noHeader); err != nil {
		t.Errorf("no header: err = %v, want the dev account", err)
	}

	// A bearer-prefixed token that names no real session must not resolve.
	withBearer := httptest.NewRequest(http.MethodGet, "/speech?item=x", nil)
	withBearer.Header.Set("Authorization", "Bearer not-a-real-session-token")
	if _, err := a.speechScope(withBearer); err == nil {
		t.Error("a forged bearer token resolved to a scope")
	}

	// And a token with no "Bearer " prefix is read as the token itself.
	bare := httptest.NewRequest(http.MethodGet, "/speech?item=x", nil)
	bare.Header.Set("Authorization", "not-a-real-session-token")
	if _, err := a.speechScope(bare); err == nil {
		t.Error("a forged bare token resolved to a scope")
	}
}

// --- broadcast mode (§19) ---------------------------------------------------

// The recording is named by the PAIR, not the article. Serving the segment
// written for a different predecessor would have the narrator hand over from a
// story the listener never heard — which sounds completely convincing and is
// completely wrong, the worst combination available.
func TestPodcastKeyIsPerOrderedPair(t *testing.T) {
	const calm = "calm"
	base := podcastKey("item-2", "item-1", calm, nil)

	if other := podcastKey("item-2", "item-9", calm, nil); other == base {
		t.Error("the same story after two different stories shares one audio key")
	}
	if opening := podcastKey("item-2", "", calm, nil); opening == base {
		t.Error("the opening segment shares an audio key with a mid-broadcast one")
	}
	if other := podcastKey("item-3", "item-1", calm, nil); other == base {
		t.Error("two different stories share an audio key")
	}
	// The manner changes the words, so it changes the recording. A reader who
	// switches from calm to brisk and hears the calm audio would conclude,
	// reasonably, that the setting does nothing.
	if brisk := podcastKey("item-2", "item-1", "brisk", nil); brisk == base {
		t.Error("two manners share one audio key")
	}
	// So does the greeting, and it carries the DATE — so the same broadcast
	// restarted an hour later costs nothing and tomorrow's opens fresh.
	morning := podcastKey("item-2", "", calm,
		&smart.Opening{PartOfDay: "morning", Date: "Monday, 27 July 2026", Stories: 11})
	if morning == podcastKey("item-2", "", calm, nil) {
		t.Error("a greeted opening shares a key with an ungreeted one")
	}
	tomorrow := podcastKey("item-2", "", calm,
		&smart.Opening{PartOfDay: "morning", Date: "Tuesday, 28 July 2026", Stories: 11})
	if tomorrow == morning {
		t.Error("two different days share one opening")
	}
	// And it must not collide with either of the other two things this item can
	// be, which are keyed `item` and `item#digest`.
	for _, mode := range []string{"item-2", "item-2#digest"} {
		if base == mode {
			t.Errorf("the broadcast key collides with %q", mode)
		}
	}
}

// **The degradation chain.** Every writer this feature can use is optional, and
// an instance that has none must still read the article aloud — the reader asked
// to hear it, and silence is the one answer that is never acceptable.
//
// speechApp builds an App with neither a digest nor a podcast writer, which is
// exactly the shape of an instance whose Smart+ key was never set.
func TestSpeechScriptFallsBackToTheArticleWhenNothingCanRewriteIt(t *testing.T) {
	a := speechApp(t, "sk-test")
	it := store.Item{ID: "item-2", Title: "Fsyncgate", SourceTitle: "LWN",
		ContentHTML: "<p>The body.</p>"}
	prev := store.Item{ID: "item-1", Title: "Postgres", SourceTitle: "Hacker News"}

	for _, prefs := range []map[string]string{
		nil,
		{podcastPrefKey: "true"},
		{digestPrefKey: "true"},
		{podcastPrefKey: "true", digestPrefKey: "true"},
	} {
		text, key := a.speechScript(context.Background(), prefs, it, prev, nil, false, false, false)
		if !strings.Contains(text, "The body.") {
			t.Errorf("prefs %v: the article was not read aloud: %q", prefs, text)
		}
		// The key has to fall back with the text. A key that said "podcast" over
		// the plain article would poison the audio cache for the day the writer
		// starts working.
		if key != it.ID {
			t.Errorf("prefs %v: cache key = %q, want the plain item id", prefs, key)
		}
	}
}

// --- the greeting (§19) ------------------------------------------------------

// "Good night" is a farewell in English, so a broadcast opened at one in the
// morning says "good evening" — which is what someone on air actually says at
// that hour, and what a listener at that hour expects.
// partOfDay now has a fourth bucket for genuinely late hours. "night" is not
// itself a farewell here — the model's instructions (podcastIntroInstructions,
// podcastInstructionsFor) are what forbid literally saying "good night";
// this test only checks the bucketing, which is what decides what the model
// is TOLD the hour is.
func TestPartOfDayHasFourBuckets(t *testing.T) {
	for hour := 0; hour < 24; hour++ {
		got := partOfDay(hour)
		want := ""
		switch {
		case hour >= 22 || hour < 5:
			want = "night"
		case hour < 12:
			want = "morning"
		case hour < 18:
			want = "afternoon"
		default:
			want = "evening"
		}
		if got != want {
			t.Errorf("%02d:00 greeted with %q, want %q", hour, got, want)
		}
	}
}

// **The greeting follows the LISTENER'S clock, not the server's.** A self-hosted
// reader on a VPS three timezones away being wished good morning at ten at night
// is the single most obviously wrong thing this feature could say.
func TestOpeningPrefersTheListenersClock(t *testing.T) {
	// The server thinks it is mid-afternoon UTC; the listener says it is half
	// past eight in the morning, four hours west.
	server := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
	r := httptest.NewRequest(http.MethodGet,
		"/speech?item=x&now="+url.QueryEscape("2026-07-27T08:30:00-04:00")+"&n=11", nil)

	got := speechApp(t, "sk-test").openingFrom(context.Background(), testScope(), r, server)
	if got.PartOfDay != "morning" {
		t.Errorf("part of day = %q, want morning — the server's clock won", got.PartOfDay)
	}
	if !strings.Contains(got.Date, "27 July 2026") {
		t.Errorf("date = %q, want the listener's day", got.Date)
	}
	if got.Stories != 11 {
		t.Errorf("stories = %d, want 11", got.Stories)
	}
}

// A missing or unparseable clock falls back to the server's rather than dropping
// the greeting. Being an hour out on "afternoon" is a small wrongness; having no
// opening at all is a missing feature.
func TestOpeningFallsBackToTheServerClock(t *testing.T) {
	server := time.Date(2026, 7, 27, 20, 0, 0, 0, time.UTC)
	for _, q := range []string{
		"/speech?item=x",
		"/speech?item=x&now=",
		"/speech?item=x&now=not-a-time",
		"/speech?item=x&now=1785155400",
	} {
		got := speechApp(t, "sk-test").openingFrom(context.Background(), testScope(), httptest.NewRequest(http.MethodGet, q, nil), server)
		if got == nil {
			t.Fatalf("%s: no opening at all", q)
		}
		if got.PartOfDay != "evening" {
			t.Errorf("%s: part of day = %q, want the server's evening", q, got.PartOfDay)
		}
	}
}

// A queue size that is absent, zero, negative or absurd is UNKNOWN rather than
// announced — "zero stories this morning" read aloud is worse than saying
// nothing, and an unbounded number is somebody's typo in a sentence.
func TestOpeningIgnoresAnImplausibleQueueSize(t *testing.T) {
	server := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	for _, q := range []string{"", "0", "-3", "nine", "999999999"} {
		r := httptest.NewRequest(http.MethodGet, "/speech?item=x&n="+url.QueryEscape(q), nil)
		if got := speechApp(t, "sk-test").openingFrom(context.Background(), testScope(), r, server).Stories; got != 0 {
			t.Errorf("n=%q became %d, want 0 (unknown)", q, got)
		}
	}
}
