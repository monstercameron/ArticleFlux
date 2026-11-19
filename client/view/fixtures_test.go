//go:build js && wasm

package view

// Deterministic inputs for the golden snapshots (golden_test.go) and the render
// benchmarks (bench_test.go).
//
// Deterministic is the whole requirement. Every timestamp here is years in the
// past, so relTime takes its "day month" branch and ageOf says "stale" no matter
// when the suite runs — a fixture dated "an hour ago" would make the snapshot a
// function of the wall clock, and a golden that drifts on its own teaches
// whoever reads the diff to stop reading it.

import (
	"strconv"
	"strings"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

const fixtureOld = "2020-01-15T10:00:00Z"

// fixtureFeeds is n feeds: every fourth filed into one of three categories, a
// spread of unread counts including zero, and a site URL on most so the icon
// host map has something to hold.
func fixtureFeeds(n int) []*pb.Feed {
	out := make([]*pb.Feed, 0, n)
	for i := 0; i < n; i++ {
		f := &pb.Feed{
			Id:          "f" + strconv.Itoa(i),
			SourceId:    "src-" + strconv.Itoa(i),
			Title:       "Feed number " + strconv.Itoa(i),
			UnreadCount: int32(i % 7),
		}
		if i%3 != 0 {
			f.SiteUrl = "https://site" + strconv.Itoa(i) + ".example/"
		}
		if i%4 == 1 {
			f.FolderId = "cat-" + strconv.Itoa(i%3)
		}
		out = append(out, f)
	}
	return out
}

func fixtureFolders() []*pb.Folder {
	return []*pb.Folder{
		{Id: "cat-0", Name: "News"},
		{Id: "cat-1", Name: "Tech"},
		{Id: "cat-2", Name: "Empty one"},
	}
}

func fixtureTags() []*pb.Tag {
	return []*pb.Tag{
		{Id: "t1", Name: "golang", FeedCount: 2},
		{Id: "t2", Name: "longreads", Label: "Long reads", FeedCount: 1},
	}
}

// fixtureItems is n list rows exercising every optional piece of itemRow: read
// and unread, starred, both verdicts, a note, a summary, a word count either side
// of the 50-word floor, a category, and a ranked pick with reasons on every
// fifth row.
func fixtureItems(n int) []*pb.Item {
	slugs := categorySlugs()
	out := make([]*pb.Item, 0, n)
	for i := 0; i < n; i++ {
		it := &pb.Item{
			Id:          "i" + strconv.Itoa(i),
			SourceId:    "src-" + strconv.Itoa(i%9),
			SourceTitle: "Feed number " + strconv.Itoa(i%9),
			Title:       "Headline " + strconv.Itoa(i) + " about something",
			PublishedAt: fixtureOld,
			Read:        i%2 == 0,
			Starred:     i%5 == 2,
			WordCount:   int32((i % 4) * 300),
		}
		switch i % 3 {
		case 1:
			it.Rating = 1
		case 2:
			it.Rating = -1
		}
		switch {
		case i%6 == 3:
			it.Note = "A note the reader wrote about this one, long enough to need cutting off at a word boundary somewhere around here."
		case i%2 == 1:
			it.Summary = "The publisher's summary of headline " + strconv.Itoa(i) + "."
		}
		if i%4 == 0 && len(slugs) > 0 {
			it.Category = slugs[i%len(slugs)]
		}
		if i%5 == 0 {
			it.RankReasons = []string{"published today", "about Go, which you follow"}
			it.RankReasonTerms = []string{"freshness", "topic"}
			it.RankSlot = "top"
			if i%10 == 0 {
				it.RankTier = "smart_plus"
				it.RankReasons = append([]string{"explains the mechanism"}, it.RankReasons...)
				it.RankReasonTerms = append([]string{smartPlusTerm}, it.RankReasonTerms...)
			}
		}
		out = append(out, it)
	}
	return out
}

// fixtureBody is an article body of roughly paras paragraphs, with the inline
// markup a real feed carries (links, emphasis, a list, an image) so the
// sanitiser and parser do representative work.
func fixtureBody(paras int) string {
	var b strings.Builder
	for i := 0; i < paras; i++ {
		b.WriteString("<p>Paragraph ")
		b.WriteString(strconv.Itoa(i))
		b.WriteString(" has <em>some emphasis</em>, a <a href=\"https://example.com/")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\">link to elsewhere</a>, and enough ordinary words to wrap across a line or two of the reading column.</p>")
		if i%10 == 5 {
			b.WriteString("<ul><li>one</li><li>two</li><li>three</li></ul><img src=\"https://example.com/i.png\" alt=\"figure\">")
		}
	}
	return b.String()
}

// fixtureArticle is an article pane with two items in the stream, the first
// one fully loaded and carrying every optional control.
func fixtureArticle(paras int) articleProps {
	first := &pb.Item{
		Id: "a1", SourceId: "src-1", SourceTitle: "Feed number 1",
		Title: "The long article", PublishedAt: fixtureOld, Url: "https://example.com/a1",
		Author: "A. Writer", WordCount: int32(paras * 22), Read: true, Rating: 1,
		ProxyUrl: "/proxy/a1", Revision: 2, EditedAt: fixtureOld,
		ContentHtml: fixtureBody(paras),
	}
	second := &pb.Item{
		Id: "a2", SourceId: "src-2", SourceTitle: "Feed number 2",
		Title: "The one still loading", PublishedAt: fixtureOld,
	}
	return articleProps{
		stream:       []*pb.Item{first, second},
		bodies:       map[string]*pb.Item{"a1": first},
		currentID:    "a1",
		notes:        map[string]string{"a1": "my draft"},
		sync:         map[string]string{"a1": "saved"},
		feedTags:     map[string][]tagRef{"src-1": {{Label: "golang", Name: "golang", ID: "t1"}}},
		iconHosts:    map[string]string{"src-1": "site1.example"},
		speakID:      "a1",
		speakState:   "playing",
		speakVisible: true,
		speakSmart:   true,
		atEnd:        true,
		expanded:     map[string]bool{"a1": true},
		noteOpen:     map[string]bool{"a1": true},
	}
}
