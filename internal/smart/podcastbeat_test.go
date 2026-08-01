package smart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/cast"
)

// seed writes a segment's answer into the cache, so a keyless writer can be
// driven end to end without spending anything. It returns what was written.
//
// The cache is what makes this test possible at all, and exercising it is not
// incidental: Write's whole job is to build the Segment that decides the cache
// key, so a mapping bug shows up here as a miss rather than as wrong prose.
func seed(t *testing.T, p *Podcast, seg Segment, text string) string {
	t.Helper()
	path := p.cachePath(seg, p.model(context.Background()))
	if path == "" {
		t.Fatal("no cache path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return text
}

func brief(kind cast.BeatKind) cast.Brief {
	return cast.Brief{
		BeatID: "show/x", Key: "k", Kind: kind, Words: 120, Vibe: "calm",
		Revision: PromptVersion, Handover: 2,
		Subject:   cast.Subject{ItemID: "item-2", Title: "Fsyncgate", Source: "LWN", Body: "the body"},
		Prev:      cast.Subject{ItemID: "item-1", Title: "Postgres", Source: "Hacker News"},
		PartOfDay: "evening", Date: "1 August 2026", Stories: 9, Position: 3,
	}
}

func TestWriteMapsEveryBeatKindToItsOwnScript(t *testing.T) {
	// Five kinds, five different pieces of writing. The failure this guards
	// against is the one the modes exist for: a beat served as the wrong kind
	// of script — a sign-off where a story should be, an opening served as the
	// first segment — which sounds like the feature not working rather than
	// like a bug.
	p := keylessPodcast(t)
	ctx := context.Background()

	cases := []struct {
		kind cast.BeatKind
		seg  func(Segment) Segment
		text string
	}{
		{cast.BeatOpening, func(s Segment) Segment {
			s.OpenOnly = true
			s.Open = &Opening{PartOfDay: "evening", Date: "1 August 2026", Stories: 9,
				Lineup: []Headline{{Source: "A", Title: "One"}, {Source: "B", Title: "Two"}}}
			s.Body = "the body"
			return s
		}, "Good evening."},
		{cast.BeatStory, func(s Segment) Segment { return s }, "Staying with databases."},
		{cast.BeatTease, func(s Segment) Segment {
			s.TeaseOnly = true
			s.Names = []Headline{{Source: "A", Title: "One"}, {Source: "B", Title: "Two"}}
			return s
		}, "Still to come."},
		{cast.BeatRecap, func(s Segment) Segment {
			s.RecapOnly = true
			s.Names = []Headline{{Source: "A", Title: "One"}, {Source: "B", Title: "Two"}}
			return s
		}, "What you missed."},
		{cast.BeatSignOff, func(s Segment) Segment {
			s.CloseOnly = true
			s.Open = &Opening{Stories: 9}
			return s
		}, "That's the lot."},
	}

	for _, c := range cases {
		b := brief(c.kind)
		if c.kind == cast.BeatTease || c.kind == cast.BeatRecap {
			b.Lineup = []cast.Headline{{ItemID: "a", Title: "One", Source: "A"},
				{ItemID: "b", Title: "Two", Source: "B"}}
		}
		if c.kind == cast.BeatOpening {
			b.Lineup = []cast.Headline{{ItemID: "a", Title: "One", Source: "A"},
				{ItemID: "b", Title: "Two", Source: "B"}}
			b.Prev = cast.Subject{}
		}
		base := Segment{
			ItemID: "item-2", Source: "LWN", Title: "Fsyncgate", Body: "the body",
			PrevID: "item-1", PrevSource: "Hacker News", PrevTitle: "Postgres",
			Vibe: "calm",
		}
		if c.kind == cast.BeatOpening {
			base.PrevID, base.PrevSource, base.PrevTitle = "", "", ""
		}
		want := seed(t, p, c.seg(base), c.text)

		got, err := p.Write(ctx, b)
		if err != nil {
			t.Errorf("%s: %v", c.kind, err)
			continue
		}
		if got.Text != want {
			t.Errorf("%s: got %q, want %q — the brief mapped to the wrong script", c.kind, got.Text, want)
		}
		if got.Words != len(strings.Fields(want)) {
			t.Errorf("%s: counted %d words in %q", c.kind, got.Words, got.Text)
		}
	}
}

func TestABreakHasNoWords(t *testing.T) {
	// Asking a writer for a break is a caller that has lost track of what it is
	// playing. Answering would put prose under a beat the player never speaks.
	p := keylessPodcast(t)
	if _, err := p.Write(context.Background(), brief(cast.BeatBreak)); err == nil {
		t.Error("the writer wrote something for a break")
	}
}

func TestAStaleRevisionIsRefusedRatherThanServed(t *testing.T) {
	// The beat's cache key claims a revision. Serving prose written under a
	// different one would put text under a key that does not describe it —
	// permanently, and invisibly.
	p := keylessPodcast(t)
	b := brief(cast.BeatStory)
	b.Revision = "v1"
	_, err := p.Write(context.Background(), b)
	if !errors.Is(err, ErrStaleRevision) {
		t.Errorf("err = %v, want ErrStaleRevision", err)
	}
}

func TestAFirstStoryWithNoGreetingIsToldTheShowAlreadyOpened(t *testing.T) {
	// Without this the listener hears the date twice in ninety seconds: once in
	// the opening's own recording and again at the top of the first story,
	// because a segment with no predecessor infers it is the top of the show.
	p := keylessPodcast(t)
	b := brief(cast.BeatStory)
	b.Prev = cast.Subject{}
	b.Lineup = nil

	opened := Segment{ItemID: "item-2", Source: "LWN", Title: "Fsyncgate", Body: "the body",
		Vibe: "calm", Opened: true}
	want := seed(t, p, opened, "The government has backed down.")

	got, err := p.Write(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != want {
		t.Error("a first story after a split opening was not told the broadcast had already opened")
	}
}

func TestATeaseAndARecapOverTheSameStoriesAreDifferentRecordings(t *testing.T) {
	// One is selling and one is catching somebody up. Sharing a cache entry
	// would have a recap telling a listener to look forward to something they
	// have already heard.
	p := keylessPodcast(t)
	names := []Headline{{Source: "A", Title: "One"}, {Source: "B", Title: "Two"}}
	base := Segment{ItemID: "item-2", Source: "LWN", Title: "Fsyncgate", Vibe: "calm",
		PrevID: "item-1", PrevSource: "Hacker News", PrevTitle: "Postgres", Names: names}

	tease := base
	tease.TeaseOnly = true
	recap := base
	recap.RecapOnly = true

	if p.cachePath(tease, "m") == p.cachePath(recap, "m") {
		t.Error("a tease and a recap over the same stories share a cache entry")
	}
	// …and two teases naming different stories are different scripts too.
	other := tease
	other.Names = []Headline{{Source: "C", Title: "Three"}}
	if p.cachePath(tease, "m") == p.cachePath(other, "m") {
		t.Error("two teases naming different stories share a cache entry")
	}
}
