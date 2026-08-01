//go:build js && wasm

package view

import (
	"strconv"
	"strings"
	"testing"
)

// The pipeline, as the two promises it makes: nothing stalls at a seam, and the
// words on screen are the words being said.
//
// Both used to be untestable, because both lived as lines inside a 500-line
// closure in reader.go — which is why the second was wrong for as long as it
// was: the slide rendered the ARTICLE while the narrator read a rewritten
// segment, and both are plausible prose on a screen.

// urlFor is a resolver that can answer for some beats and not others, which is
// the situation the walk actually runs in: a body arrives per story, and one
// that has not landed has no listening ticket and therefore no URL.
func urlFor(known ...string) func(id, prev string) string {
	have := map[string]bool{}
	for _, id := range known {
		have[id] = true
	}
	return func(id, prev string) string {
		if !have[id] {
			return ""
		}
		return "/speech?t=" + id + "&p=" + prev
	}
}

func TestTheWarmChainCarriesEachBeatsOwnPredecessor(t *testing.T) {
	// A segment is written per ordered PAIR. Warming "c after nothing" buys a
	// file the show will never play, and the one it does play still has to be
	// synthesised when it is wanted — paid for twice, and the seam stalls
	// anyway.
	got := warmURLs([]string{"a", "b", "c"}, 0, 2, urlFor("b", "c"))
	want := []string{"/speech?t=b&p=a", "/speech?t=c&p=b"}
	if len(got) != len(want) {
		t.Fatalf("warmed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("beat %d warmed %q, want %q", i, got[i], want[i])
		}
	}
}

func TestAnUnresolvedBeatDoesNotStopTheChain(t *testing.T) {
	// The regression this exists for: the walk used to RETURN at the first beat
	// whose body had not landed, so one slow fetch left everything behind it
	// cold and the next seam was a full write-and-synthesise of silence. The
	// running order is known from the queue whether or not a body has arrived,
	// so a gap costs one beat its warm and nothing else.
	got := warmURLs([]string{"a", "b", "c"}, 0, 2, urlFor("c"))
	if len(got) != 1 {
		t.Fatalf("warmed %v, want just c", got)
	}
	// And with the RIGHT predecessor: c still follows b in the running order,
	// whether or not b's body ever arrived. A c warmed after `a` is a different
	// recording from the one that will be played.
	if got[0] != "/speech?t=c&p=b" {
		t.Errorf("warmed %q — the predecessor did not advance across the gap", got[0])
	}
}

func TestTheWarmChainStopsAtTheEndOfTheShow(t *testing.T) {
	// Warming past the last story would ask for a beat that does not exist, and
	// on a server that answers 404 it is a red line in the console for a
	// programme that is running perfectly.
	if got := warmURLs([]string{"a", "b"}, 0, 3, urlFor("b")); len(got) != 1 {
		t.Errorf("warmed %v past the end of a two-story show", got)
	}
	if got := warmURLs([]string{"a"}, 0, 2, urlFor("a")); len(got) != 0 {
		t.Errorf("warmed %v on a one-story show", got)
	}
}

func TestNothingIsWarmedWithoutAPlace(t *testing.T) {
	// A negative index is "the story playing has left the loaded list and there
	// is no remembered position either". Warming from the top of the queue there
	// would pay for the beginning of a show the reader is in the middle of.
	if got := warmURLs([]string{"a", "b"}, -1, 2, urlFor("a", "b")); len(got) != 0 {
		t.Errorf("warmed %v from nowhere", got)
	}
	if got := warmURLs([]string{"a", "b"}, 0, 0, urlFor("b")); len(got) != 0 {
		t.Errorf("warmed %v at depth zero", got)
	}
	if got := warmURLs([]string{"a", "b"}, 0, 2, nil); len(got) != 0 {
		t.Errorf("warmed %v with no resolver", got)
	}
}

func TestTheScriptAskedForBelongsToTheAudioPlayed(t *testing.T) {
	// One beat key, both halves. The caption is addressed by appending to the
	// URL that will actually be played rather than by rebuilding the request
	// from its parts and hoping the two agree — which is how a caption ends up
	// belonging to the story before this one.
	const ticket = "/speech?t=SEALED"
	played := speechFrom(ticket, speechAsk{prevID: "a", podcast: true, intro: askIntroDone})
	script := scriptURL(played)
	if !strings.HasPrefix(script, played) {
		t.Fatalf("the script URL %q does not address the recording %q", script, played)
	}
	if !strings.HasSuffix(script, "as=text") {
		t.Errorf("the script URL %q does not ask for the script", script)
	}
}

func TestAScriptURLIsAskedForOnceAndCorrectly(t *testing.T) {
	// A ticket URL always has a query; a bare path does not, and the two need
	// different separators. Getting it wrong makes `as=text` part of the path,
	// which answers 404 and captions nothing, silently.
	if got := scriptURL("/speech?t=x"); got != "/speech?t=x&as=text" {
		t.Errorf("scriptURL on a query URL = %q", got)
	}
	if got := scriptURL("/speech"); got != "/speech?as=text" {
		t.Errorf("scriptURL on a bare path = %q", got)
	}
	if got := scriptURL(""); got != "" {
		t.Errorf("scriptURL(\"\") = %q — there is no recording to caption", got)
	}
}

func TestAWarmedScriptIsThereWhenTheBeatPlays(t *testing.T) {
	// The whole point of warming the words: by the time the beat starts, its
	// captions are a map lookup rather than a round trip after the first
	// sentence has already been spoken.
	c := newScriptCache()
	c.put("/speech?t=b", "Meanwhile, in the second story.")
	if got, ok := c.get("/speech?t=b"); !ok || got != "Meanwhile, in the second story." {
		t.Errorf("warmed script came back %q, %v", got, ok)
	}
	if _, ok := c.get("/speech?t=c"); ok {
		t.Error("a beat nobody warmed reported a script")
	}
}

func TestAnEmptyScriptIsNotCached(t *testing.T) {
	// "" is how the rest of the client says "no captions". Caching that answer
	// would make a beat whose script arrived a moment late permanently
	// uncaptioned, which is a worse failure than the one round trip it saves.
	c := newScriptCache()
	c.put("/speech?t=b", "   ")
	if _, ok := c.get("/speech?t=b"); ok {
		t.Error("an empty script was cached as an answer")
	}
}

func TestTheScriptCacheIsBoundedAndKeepsTheNewest(t *testing.T) {
	// A session left open for a week, not a show: a programme is finite and
	// nothing in one is ever evicted. What must not happen is unbounded growth,
	// and what must not be evicted is the beat about to play.
	c := newScriptCache()
	for i := 0; i < scriptCacheMax+10; i++ {
		c.put("/speech?t="+strconv.Itoa(i), "words "+strconv.Itoa(i))
	}
	if len(c.byURL) > scriptCacheMax {
		t.Errorf("cache holds %d scripts, over the %d bound", len(c.byURL), scriptCacheMax)
	}
	newest := "/speech?t=" + strconv.Itoa(scriptCacheMax+9)
	if _, ok := c.get(newest); !ok {
		t.Error("the most recently warmed script was evicted — that is the one about to play")
	}
	if _, ok := c.get("/speech?t=0"); ok {
		t.Error("the oldest script survived a full cache")
	}
}

func TestTheSlideShowsTheScriptRatherThanTheArticle(t *testing.T) {
	// §19's correction, and it is a bug fix wearing a feature's clothes: the
	// audio is a rewritten segment and the article is the article, and they
	// diverge within a paragraph.
	useScript, hold := slideProse(true, "The words being said.", false)
	if !useScript || hold {
		t.Errorf("slideProse with a script = script:%v hold:%v", useScript, hold)
	}
}

func TestTheArticleIsNotAPlaceholderForAScriptOnItsWay(t *testing.T) {
	// The window this closes. The article is not a rough draft of the segment —
	// it is a different text, and showing it for the moment before the right
	// words arrive is a caption that is briefly, confidently wrong.
	useScript, hold := slideProse(true, "", true)
	if useScript || !hold {
		t.Errorf("slideProse while the words are in flight = script:%v hold:%v", useScript, hold)
	}
}

func TestTheArticleIsTheBodyWhenNoScriptIsComing(t *testing.T) {
	// Not a compromise, and this is why "waiting" had to be its own state. With
	// no voice this is the mode's whole content; and on an instance with no key
	// the audio path falls back to READING THE ARTICLE, so the article is what
	// is being said and captioning it is correct.
	if useScript, hold := slideProse(true, "", false); useScript || hold {
		t.Errorf("slideProse with nothing coming = script:%v hold:%v", useScript, hold)
	}
	if useScript, hold := slideProse(false, "a script from a previous show", false); useScript || hold {
		t.Errorf("slideProse in silent mode = script:%v hold:%v", useScript, hold)
	}
}
