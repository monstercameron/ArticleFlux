package smart

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
)

// The format's direction, on its way to the model.
//
// The failure this guards against is the expensive kind: a format file full of
// audience, house style and per-block direction that resolves correctly, hashes
// correctly, prints correctly in a cue sheet — and stops one layer short of the
// only thing that could act on it. It would look like it worked.

func directionInput(t *testing.T, d fluxcast.Direction) string {
	t.Helper()
	return podcastInput(Segment{
		ItemID: "i1", Source: "LWN", Title: "Fsyncgate",
		PrevID: "i0", PrevSource: "HN", PrevTitle: "Postgres",
		Vibe: "calm", Direction: d,
	}, "the article body")
}

func TestTheDirectionReachesThePrompt(t *testing.T) {
	got := directionInput(t, fluxcast.Direction{
		Audience: "a working software engineer",
		Standing: []string{"assumes technical fluency"},
		Avoid:    []string{"no weather framing"},
		Always:   []string{"name the publication once"},
		Never:    []string{"never open with a question"},
		Note:     "open on the fact, not the setup",
		Energy:   1,
		Colour:   "light",
		Address:  "second-person",
		Locale:   "en-GB",
		Depth:    "analytical",
	})

	for _, want := range []string{
		"a working software engineer",
		"assumes technical fluency",
		"no weather framing",
		"name the publication once",
		"never open with a question",
		"open on the fact, not the setup",
		"en-GB",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt does not carry %q:\n%s", want, got)
		}
	}
	// The ladders are expanded rather than passed through as bare words: a
	// model told "depth: analytical" has been told nothing.
	if !strings.Contains(got, "what to watch next") {
		t.Errorf("depth was passed through unexpanded:\n%s", got)
	}
	if !strings.Contains(got, "more urgent") {
		t.Errorf("energy was passed through unexpanded:\n%s", got)
	}
}

func TestTheDirectionCannotOverrideTheNeverRules(t *testing.T) {
	// A format may ADD a constraint and may never remove one. The prompt says
	// so where a reader of it would look, because the direction arrives after
	// the instructions and a model reading in order treats the last thing it
	// was told as the operative one.
	got := directionInput(t, fluxcast.Direction{Note: "say whatever you like"})
	if !strings.Contains(got, "NEVER rules") {
		t.Errorf("the direction does not defer to the writer's own rules:\n%s", got)
	}
}

func TestNoFormatMeansNoChangeToThePrompt(t *testing.T) {
	// Adopting formats has to be inaudible until somebody writes one. An empty
	// direction must leave the commission byte-identical to what it was before
	// any of this existed.
	//
	// No predecessor, deliberately: with one, the input carries a handover
	// SHAPE chosen at random per call (handoverShapeFor, v6), so two otherwise
	// identical prompts differ by design and this comparison would be measuring
	// that rather than the direction.
	top := Segment{ItemID: "i1", Source: "LWN", Title: "Fsyncgate", Vibe: "calm"}
	withDir := top
	withDir.Direction = fluxcast.Direction{}

	with := podcastInput(withDir, "the article body")
	without := podcastInput(top, "the article body")
	if with != without {
		t.Errorf("an empty direction changed the prompt:\n%q\nvs\n%q", with, without)
	}
}

func TestTheDirectionGoesBeforeTheStory(t *testing.T) {
	// A model shown an article and then told how to treat it has already
	// decided how to treat it. Same reason the sign-off's own inputs go first.
	got := directionInput(t, fluxcast.Direction{Audience: "an engineer"})
	d := strings.Index(got, "an engineer")
	body := strings.Index(got, "the article body")
	if d < 0 || body < 0 || d > body {
		t.Errorf("the direction is not ahead of the article (%d vs %d):\n%s", d, body, got)
	}
}

func TestChangingTheDirectionChangesTheRecording(t *testing.T) {
	// The direction is part of the text, so it is part of the key. Without
	// this, editing a house style serves back every segment written under the
	// old one — which is indistinguishable from the format not being read.
	p := keylessPodcast(t)
	base := Segment{ItemID: "i1", Source: "LWN", Title: "Fsyncgate", Vibe: "calm"}

	plain := p.cachePath(base, "m")
	styled := base
	styled.Direction = fluxcast.Direction{Colour: "none"}
	if p.cachePath(styled, "m") == plain {
		t.Error("a house style change did not change the cache key")
	}

	other := base
	other.Direction = fluxcast.Direction{Colour: "full"}
	if p.cachePath(other, "m") == p.cachePath(styled, "m") {
		t.Error("two different house styles share one cache entry")
	}
}

func TestNoFormatMeansNoChangeToTheCacheKey(t *testing.T) {
	// The other half of "adopting formats is inaudible", and the half that
	// costs money when it is wrong.
	//
	// An unconditional separator in the key changed it for every reader with no
	// format at all, so the entire cached library missed at once and every
	// segment somebody had already paid for was rewritten to say the same
	// thing. The prompt had this rule from the start; the key did not, because
	// nothing asserted it.
	p := keylessPodcast(t)
	base := Segment{ItemID: "i1", Source: "LWN", Title: "Fsyncgate", Vibe: "calm",
		PrevID: "i0", PrevSource: "HN", PrevTitle: "Postgres"}

	withEmpty := base
	withEmpty.Direction = fluxcast.Direction{}
	if p.cachePath(withEmpty, "m") != p.cachePath(base, "m") {
		t.Error("an empty direction moved the cache entry, invalidating everything already written")
	}
}
