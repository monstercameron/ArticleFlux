//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/html"
	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The My Feed settings tab (§18.2, §18.9).
//
// What these assert is the screen's ARGUMENT, not its markup: that a reader can
// see the evidence behind a judgement, that the dial shows where it currently
// stands, and that a judgement they have struck out is still on the page. Each
// of those has a way of quietly disappearing under a refactor, and each of them
// is the difference between a control and a decoration.

func renderMyFeed(t *testing.T, p myFeedProps) string {
	t.Helper()
	return renderView(t, func(tr i18n.Runtime) ui.Node {
		return html.Div(html.Props{}, settingsMyFeed(tr, p)...)
	})
}

func demoProfile() *pb.GetInterestProfileResponse {
	return &pb.GetInterestProfileResponse{
		RankedCount: 99, TopicCount: 1, EntityCount: 3,
		Topics: []*pb.InterestTopic{{
			Id: "tp1", Key: "maxpro90s", Label: "Max · Pro · 90s",
			Terms:       []string{"max", "pro", "90s", "huawei", "pura"},
			MemberCount: 3, Trend: "rising",
			Level: pb.SteerLevel_STEER_LEVEL_NORMAL,
		}},
		Entities: []*pb.InterestEntity{
			{Name: "pro max", Label: "Pro Max", Kind: "phrase", Weight: 37, Mentions: 2,
				Level: pb.SteerLevel_STEER_LEVEL_NORMAL},
			{Name: "apple watch", Label: "Apple Watch", Kind: "phrase", Weight: 1.8, Mentions: 2,
				Level: pb.SteerLevel_STEER_LEVEL_NEVER},
		},
		Feeds: []*pb.InterestFeed{
			{SourceId: "s1", Title: "Liliputing", Score: 0.62, Opens: 12, Impressions: 340, OnMyFeed: true},
			{SourceId: "s2", Title: "Hackline Daily", Score: 0.11, Opens: 1, Impressions: 90},
		},
		Factors: []*pb.InterestFactor{
			{Term: "fresh", Items: 99}, {Term: "entity", Items: 37},
		},
	}
}

// The evidence has to be on the row. A name and four buttons is a control with
// nothing to judge it by — and the numbers here are the ones that make "Pro Max"
// legible as a misread rather than as a subject somebody follows.
func TestMyFeedShowsTheEvidenceBehindEachJudgement(t *testing.T) {
	out := renderMyFeed(t, myFeedProps{profile: demoProfile()})

	for _, want := range []string{
		"Pro Max",
		"named in 2 headlines you read", // the mention count …
		"reading weight 37.0",           // … beside the weight it is out of scale with
		"found in headlines",            // which half of the system named it
		"Max · Pro · 90s",
		"3 articles",
		"max · pro · 90s · huawei",   // the terms the cluster was named from
		"Named something you follow", // a factor label, keyed off the wire's term
		"37 of 99",
		"12 opened of 340 shown",
		"Not on My Feed", // a feed the reader has already excluded
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the tab does not show %q", want)
		}
	}
}

// A row set to Never stays on the page, marked. This is the assertion behind
// store.AllEntities existing at all: a correction that removes its own row is
// one nothing can undo.
func TestMyFeedKeepsAStruckOutRowVisible(t *testing.T) {
	out := renderMyFeed(t, myFeedProps{profile: demoProfile()})
	if !strings.Contains(out, "Apple Watch") {
		t.Fatal("a struck-out thing vanished from the screen that struck it out")
	}
	if !strings.Contains(out, `data-level="never"`) {
		t.Error("nothing marks the struck-out row as struck out")
	}
	// And the dial says where it stands. Four unpressed chips is a segmented
	// control with no current answer.
	if strings.Count(out, `aria-pressed="true"`) < 3 {
		t.Errorf("not every row's dial shows a current position:\n%s", out)
	}
}

// The dial addresses rows by the handle the SERVER takes: a topic's fingerprint
// and an entity's normalised name.
//
// The topic half pins a bug. Addressing a topic by its row id worked once and
// then answered NotFound on every press afterwards, because every derivation —
// including the one each steer schedules — deletes and reinserts the topics
// table with fresh ids. A reader met that as "Could not read the profile: not
// found" seconds after a correction that had actually landed.
func TestMyFeedDialAddressesRowsByTheirKeys(t *testing.T) {
	out := renderMyFeed(t, myFeedProps{profile: demoProfile()})
	for _, want := range []string{
		`data-action="mf-topic"`,
		"data-for-item=\"maxpro90s\"",
		`data-action="mf-entity"`,
		`data-for-item="pro max"`,
		`data-value="never"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(out, `data-for-item="tp1"`) {
		t.Error("a topic row is still addressed by its row id, which the next derivation will retire")
	}
}

// Cold start is said, not shown as an empty list. An instance with nothing
// ranked yet is a normal state and looks exactly like a broken one.
func TestMyFeedSaysWhenItIsStillLearning(t *testing.T) {
	out := renderMyFeed(t, myFeedProps{profile: &pb.GetInterestProfileResponse{ColdStart: true}})
	for _, want := range []string{
		"No pick has matched a topic yet",
		"No topics yet.",
		"Nothing named yet.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a cold-start profile does not say %q", want)
		}
	}
}

// An error replaces the screen rather than decorating a stale one: everything
// this tab shows is a claim about the reader, and a claim from before the
// failure is worse than none.
func TestMyFeedErrorReplacesTheScreen(t *testing.T) {
	out := renderMyFeed(t, myFeedProps{profile: demoProfile(), err: "Could not read the profile: no route"})
	if !strings.Contains(out, "no route") {
		t.Error("the error is not shown")
	}
	if strings.Contains(out, "Pro Max") {
		t.Error("a failed load left the previous profile on screen")
	}
}

// Every scoring term internal/rank can emit has a label, so a new factor shows
// up as a word rather than as a blank row with a number beside it.
func TestEveryRankTermHasAFactorLabel(t *testing.T) {
	// The list in reader.proto's Item.rank_reason_terms, which is the documented
	// contract between the scorer and this screen.
	terms := []string{
		"topic", "entity", "feed", "domain", "fresh", "corroboration", "manual",
		"volume", "duplicate", "negative", "skipped", "external", "deliberate",
		"smartplus",
	}
	renderView(t, func(tr i18n.Runtime) ui.Node {
		for _, term := range terms {
			if got := factorLabel(tr, term); got == term {
				t.Errorf("scoring term %q has no label; the row would read as a machine key", term)
			}
		}
		// An unknown one falls back to itself rather than to nothing.
		if got := factorLabel(tr, "not-a-term"); got != "not-a-term" {
			t.Errorf("unknown term rendered as %q, want the term itself", got)
		}
		return html.Div(html.Props{})
	})
}
