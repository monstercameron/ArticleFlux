//go:build js && wasm

package view

import (
	"strings"
	"testing"
)

// The pipeline's arithmetic, and the URL it has to agree with.
//
// A segment is two paid round trips — the model writes it, then it is spoken —
// and both happen the first time its audio is asked for. Asked at the seam that
// is ten to thirty seconds of silence; asked while the previous story runs it is
// nothing. The whole feature therefore rests on warming the URL that will
// actually be played, and warming it early enough.

func TestTheWarmedURLIsTheOneThatWillBePlayed(t *testing.T) {
	// A segment is written per ordered PAIR, so a warm that forgets the
	// predecessor pays for a recording of "this story after nothing" — a
	// different file, which still has to be synthesised when it is wanted, and
	// has now been paid for twice.
	const ticket = "/speech?t=SEALED"
	real := speechFrom(ticket, speechAsk{prevID: "a", podcast: true, intro: askIntroDone})
	warm := speechFrom(ticket, speechAsk{prevID: "a", podcast: true, intro: askIntroDone})
	if real != warm {
		t.Errorf("warm %q != played %q", warm, real)
	}
	if !strings.Contains(real, "&p=a") {
		t.Errorf("the handover is missing from %q", real)
	}
}

func TestMidBroadcastCarriesNothingButTheHandover(t *testing.T) {
	// The opening parameters are deliberately not sent once there is a
	// predecessor — the server decides where the top of the show is by whether
	// there is one. A warm that added them would ask for a URL the real request
	// never uses.
	const ticket = "/speech?t=SEALED"
	got := speechFrom(ticket, speechAsk{
		prevID: "a", podcast: true, intro: askIntroDone,
		now: "2026-08-01T10:00:00-04:00", stories: 9, lineup: []string{"x", "y"},
	})
	if strings.Contains(got, "now=") || strings.Contains(got, "&n=") || strings.Contains(got, "&q=") {
		t.Errorf("a mid-broadcast request carried opening parameters: %q", got)
	}
}

func TestABroadcastOffRequestIsNotRewritten(t *testing.T) {
	// The browser caches audio by URL, so appending a pointless parameter would
	// re-download every segment a reader has already heard.
	const ticket = "/speech?t=SEALED"
	if got := speechFrom(ticket, speechAsk{prevID: "a", podcast: false}); got != ticket {
		t.Errorf("a non-broadcast URL was rewritten to %q", got)
	}
}

func TestTheFirstStoryOfASplitBroadcastSaysDoNotGreet(t *testing.T) {
	// It genuinely has no predecessor, so nothing else about the request tells
	// the server the opening has already been recorded. Without this the
	// listener hears the date twice in ninety seconds.
	const ticket = "/speech?t=SEALED"
	got := speechFrom(ticket, speechAsk{podcast: true, intro: askIntroDone})
	if !strings.Contains(got, "&i=0") {
		t.Errorf("the first story after a split opening does not say so: %q", got)
	}
}

func TestWarmDepthIsAtLeastOneAndBounded(t *testing.T) {
	// One is the minimum that covers a seam at all. The ceiling is money: a
	// reader who stops after story three has paid for everything warmed beyond
	// it, so depth is a trade rather than a dial to turn up.
	if warmDepth < 1 {
		t.Errorf("warmDepth = %d, which warms nothing", warmDepth)
	}
	if warmDepth > 3 {
		t.Errorf("warmDepth = %d — a reader who leaves early pays for all of it", warmDepth)
	}
}

// A listen outside the show is a listen, not a programme.
//
// Both halves have to be true, and the half that was missing is the occasion:
// the preference alone made pressing play on a single article in the feed
// produce a greeting, the date and a run-through of stories nobody had asked to
// hear, in place of the article.
func TestOnlyTheShowProducesABroadcast(t *testing.T) {
	for _, c := range []struct {
		name         string
		pref, inShow bool
		want         bool
	}{
		{"the show, with the preference on", true, true, true},
		{"the show, with it off — a narrated slideshow, not a programme", false, true, false},
		{"the feed, with the preference on", true, false, false},
		{"the feed, with it off", false, false, false},
	} {
		if got := castMode(c.pref, c.inShow); got != c.want {
			t.Errorf("%s: castMode = %v, want %v", c.name, got, c.want)
		}
	}
}

// The consequence, at the URL that is actually sent: a plain listen carries
// none of the broadcast's parameters, so the server reads the article (or its
// summary) and the browser plays the same recording it would have cached.
func TestAPlainListenAsksForTheArticle(t *testing.T) {
	const ticket = "/speech?t=SEALED"
	got := speechFrom(ticket, speechAsk{
		podcast: castMode(true, false),
		now:     "2026-08-01T10:00:00-04:00",
		stories: 11,
		lineup:  []string{"a", "b", "c"},
		intro:   askIntroWith,
	})
	if got != ticket {
		t.Errorf("a listen outside the show asked for a broadcast: %q", got)
	}
}

// The summary and the article are two recordings, so they must be two addresses.
//
// The server has always filed them apart on disk. The browser could not tell
// them apart at all: the sealed ticket names the item and nothing else, so both
// modes asked for the same URL, and an audio cache answers by URL. Turn the
// summary on after hearing the article and the article came back, byte for byte,
// which reads as the switch doing nothing.
func TestTheSummaryAndTheArticleAreDifferentURLs(t *testing.T) {
	const ticket = "/speech?t=SEALED"
	article := speechFrom(ticket, speechAsk{})
	summary := speechFrom(ticket, speechAsk{digest: true})
	if article == summary {
		t.Errorf("the summary and the article share one URL: %q", article)
	}
	// The article's URL is the ticket untouched: a reader who has never turned
	// the summary on must not re-download everything they have already heard.
	if article != ticket {
		t.Errorf("a plain listen rewrote its URL to %q", article)
	}
}

// And the broadcast is not fragmented by a preference it ignores.
//
// A segment already IS the short form, so the server's precedence has the
// broadcast outranking the summary. A marker on those URLs would split the
// programme's cache by a switch that changes none of its words.
func TestTheSummaryMarkerStaysOffBroadcastURLs(t *testing.T) {
	const ticket = "/speech?t=SEALED"
	on := speechFrom(ticket, speechAsk{podcast: true, prevID: "a", digest: true})
	off := speechFrom(ticket, speechAsk{podcast: true, prevID: "a"})
	if on != off {
		t.Errorf("the summary preference changed a broadcast URL: %q vs %q", on, off)
	}
}
