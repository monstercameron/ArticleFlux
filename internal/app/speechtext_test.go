package app

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/smart"
)

// `as=text`: the script over the wire, for the captions (§19, TODO 11.47).
//
// Two properties carry the whole feature and neither is visible from the
// outside, which is why they are tested here rather than left to the browser:
// it must never write, and it must never answer with a text the narrator is not
// going to speak. The first is a bill; the second is a slide showing one thing
// while a voice says another, which is the exact mismatch captions exist to
// remove.

func TestScriptIsNotServedBeforeItIsWritten(t *testing.T) {
	// The rule that makes as=text free: a script that is GOING to be written
	// but has not been yet answers 204, and the display runs without captions
	// rather than reaching a model to put text on a screen.
	//
	// A configured writer with an empty cache is the only way to reach that
	// state — see the test below for what a keyless instance does instead, which
	// is a different answer for a different reason.
	a, voice, _, ids := broadcastApp(t)
	a.podcast = smart.NewPodcast(llm.New(func(context.Context) string { return "a-key" }),
		nil, t.TempDir())

	// A handover, because that is what makes this a request the broadcast
	// writer will answer. Without one it is a plain listen — the article gets
	// read, so the article IS the caption and 200 is the right answer (see
	// castRequest, and the test below for the same 200 arrived at differently).
	// The client sends the handover here too: the caption URL is the audio URL
	// plus `as=text`, so the two describe one recording by construction.
	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(prevItemParam, ids[0])
	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204 for an unwritten script: %s", rec.Code, rec.Body.String())
	}
	if voice.count() != 0 {
		t.Errorf("a caption request synthesised %d times", voice.count())
	}
}

// The same instance, the same configured writer, and a request that is NOT part
// of a programme: no handover, no opening, no beat — one article somebody
// pressed play on.
//
// It answers with the article rather than 204, and that is the whole point of
// the fix this test was written for. Nothing is going to be written for a
// request like this, so there is no script to wait for: the reader hears the
// article (or its summary), and the article is the honest caption for it.
func TestAPlainListenIsCaptionedWithTheArticle(t *testing.T) {
	a, voice, _, ids := broadcastApp(t)
	a.podcast = smart.NewPodcast(llm.New(func(context.Context) string { return "a-key" }),
		nil, t.TempDir())

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "story body") {
		t.Errorf("a plain listen was not captioned with the article: %q", rec.Body.String())
	}
	if voice.count() != 0 {
		t.Errorf("a caption request synthesised %d times", voice.count())
	}
}

func TestOnAnInstanceThatCannotWriteTheArticleIsTheCaption(t *testing.T) {
	// The other fork, and it is not a degradation. With no key the audio path
	// falls back to reading the article, so the article IS what the listener
	// hears — captioning it is correct, and answering 204 here would leave a
	// silent-looking display on an instance where everything is working as
	// designed.
	a, _, _, ids := broadcastApp(t)

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "story body") {
		t.Errorf("the caption is not the article: %q", rec.Body.String())
	}
}

func TestScriptRequestNeverReachesTheWriter(t *testing.T) {
	// The same rule, from the other side: no model call either. There is no key
	// in these tests, so a write would surface as an error rather than a bill —
	// but the assertion that matters is that the request is answered without one
	// being attempted at all.
	a, _, _, ids := broadcastApp(t)
	a.write = &stubWriter{}

	q := url.Values{}
	q.Set("item", ids[1])
	q.Set(beatWordsParam, "95")
	q.Set(beatRevParam, "v7")
	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusNoContent {
		t.Errorf("status %d, want 204 — the beat path must not write to answer a caption", rec.Code)
	}
}

func TestTheScriptServedIsTheScriptSpoken(t *testing.T) {
	// The property captions exist for. The text on screen and the audio have to
	// come from one script; if they can diverge, the feature is worse than not
	// having it.
	a, voice, _, ids := broadcastApp(t)

	// One ordinary listening request, which writes the script and synthesises it.
	q := url.Values{}
	q.Set("item", ids[1])
	if rec := speak(t, a, q.Encode()); rec.Code != http.StatusOK {
		t.Fatalf("audio: status %d", rec.Code)
	}
	spoken := voice.last().text
	if strings.TrimSpace(spoken) == "" {
		t.Fatal("nothing was spoken")
	}

	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())
	if rec.Code != http.StatusOK {
		t.Fatalf("script: status %d, want 200 once the audio exists: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != spoken {
		t.Errorf("the caption text and the spoken text differ:\n caption %q\n spoken  %q", got, spoken)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content type %q", ct)
	}
	// Private, like the audio: this is one reader's article rewritten.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "private") {
		t.Errorf("cache-control %q, want private", cc)
	}
}

func TestAScriptRequestIsGatedLikeTheAudio(t *testing.T) {
	// It is not a second read path. Same ticket, same scope, same refusals —
	// an item this reader cannot see is 404 and never 403, because a permission
	// error would confirm the item exists.
	a, _, _, _ := broadcastApp(t)

	q := url.Values{}
	q.Set("item", "an-item-from-another-tenant")
	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())

	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestABrokenTicketCannotReadAScript(t *testing.T) {
	a, _, _, _ := broadcastApp(t)
	q := url.Values{}
	q.Set("t", "not-a-sealed-ticket")
	q.Set(asParam, asText)
	rec := speak(t, a, q.Encode())
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
}
