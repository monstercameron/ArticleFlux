//go:build js && wasm

package view

import "strings"

// The broadcast pipeline, as the four decisions it actually is.
//
// # What the pipeline has to do
//
// A segment is TWO paid round trips — a model writes it, then it is spoken —
// and both happen the first time its audio is asked for. Asked at the seam that
// is ten to thirty seconds of silence between stories; asked while the previous
// story is still running it is nothing. So the shape is: while beat N plays,
// write and synthesise N+1 and N+2, and fetch the WORDS of each as soon as its
// audio has landed, because the request that warms the audio is the request that
// writes the script and `as=text` is a cache read from that moment on.
//
// # Why these four are functions rather than lines inside the component
//
// Every one of them is a rule that was previously a line in a 500-line closure
// in reader.go, which is to say a rule nothing could test. The bug this file
// exists to fix was invisible for exactly that reason: the slide rendered the
// ARTICLE while the narrator read a rewritten segment, and both are plausible
// prose on a screen.

// warmURLs is which segments to warm after the one playing, in order.
//
// `url` resolves one beat to the exact address that will be played, or "" if it
// cannot be resolved yet — no body has landed, so no listening ticket, so no
// URL. It is a parameter rather than a lookup so that this rule can be checked
// without a DOM, a store or a network.
//
// Two properties, and both were bugs:
//
//   - Every beat is warmed with the beat BEFORE IT as its predecessor, because a
//     segment is written per ordered PAIR. A warm that forgets the predecessor
//     pays for a recording of "this story after nothing" — a different file,
//     which still has to be synthesised when it is wanted.
//   - A beat that cannot be resolved is SKIPPED, not treated as the end of the
//     chain. The running order is known from the queue whether or not a body has
//     arrived, so one slow fetch costs that one beat its warm rather than every
//     beat behind it. The previous version returned at the first gap and only
//     recovered if a body happened to land and drive the whole walk again.
func warmURLs(q []string, at, depth int, url func(id, prev string) string) []string {
	if at < 0 || depth <= 0 || url == nil {
		return nil
	}
	var out []string
	prev := ""
	if at < len(q) {
		prev = q[at]
	}
	for n := 1; n <= depth && at+n < len(q); n++ {
		id := q[at+n]
		if u := url(id, prev); u != "" {
			out = append(out, u)
		}
		// Advanced whether or not this one resolved: the NEXT beat still hands
		// over from this one, and warming it as though it followed something
		// else would buy the wrong recording.
		prev = id
	}
	return out
}

// scriptURL is the address of the WORDS of a recording.
//
// The same URL as the audio plus `as=text`, so the two cannot describe different
// beats. Rebuilding the request from its parts and hoping the two agree is how a
// caption ends up belonging to the story before this one.
func scriptURL(audioURL string) string {
	if audioURL == "" {
		return ""
	}
	sep := "&"
	if !strings.Contains(audioURL, "?") {
		sep = "?"
	}
	return audioURL + sep + "as=text"
}

// scriptCacheMax is how many scripts are held.
//
// A show is finite and a script is a couple of kilobytes, so this is not about
// memory — it is about a session left open for a week. Generous enough that
// nothing in one programme is ever evicted, which is the only case that matters:
// an evicted script is not wrong, it is one small request again.
const scriptCacheMax = 64

// scriptCache holds the words of segments already fetched, keyed by the exact
// audio URL they belong to.
//
// Keyed by URL rather than by item id, and that is the same correction the
// caption state itself needed: the opening is played AS the first story — it
// introduces it, and carries its id — so two different scripts share one id, and
// a map keyed by id serves the greeting's words under the story that follows.
type scriptCache struct {
	byURL map[string]string
	// order is insertion order, for eviction. A slice rather than a heap or a
	// clock: the whole structure holds a few dozen entries and is walked once
	// per beat.
	order []string
}

func newScriptCache() *scriptCache {
	return &scriptCache{byURL: map[string]string{}}
}

func (c *scriptCache) get(url string) (string, bool) {
	if c == nil || url == "" {
		return "", false
	}
	s, ok := c.byURL[url]
	return s, ok
}

// put stores a script, evicting the oldest once the cache is full. An empty
// script is not stored: "" is how the rest of the client says "no captions", and
// caching that answer would make a beat whose script arrived late permanently
// uncaptioned.
func (c *scriptCache) put(url, text string) {
	if c == nil || url == "" || strings.TrimSpace(text) == "" {
		return
	}
	if _, seen := c.byURL[url]; !seen {
		c.order = append(c.order, url)
	}
	c.byURL[url] = text
	for len(c.order) > scriptCacheMax {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.byURL, oldest)
	}
}

// slideProse decides what text the slide shows under the headline.
//
// Three answers, not two, and the third is the fix:
//
//   - The SCRIPT, whenever read-to-me has one. It replaces the article because
//     they are different texts — the audio is a rewritten segment — and
//     scrolling one at the pace of the other is the bug §19 names.
//   - NOTHING, while the script for the beat on screen is genuinely in flight.
//     The article is not a placeholder for the script: it is a different text,
//     presented with the same confidence, and showing it for the second before
//     the right words arrive is a caption that is briefly, confidently wrong.
//     The card, the headline and the article's picture all stay.
//   - The ARTICLE, otherwise. Not a compromise: with no voice this is the mode's
//     whole content, and on an instance with no key the audio path falls back to
//     reading the article, so the article IS what is being said.
func slideProse(audio bool, script string, waiting bool) (useScript, hold bool) {
	if audio && strings.TrimSpace(script) != "" {
		return true, false
	}
	if audio && waiting {
		return false, true
	}
	return false, false
}
