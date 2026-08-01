//go:build js && wasm

package view

import (
	"strings"
	"testing"

	"github.com/monstercameron/GoWebComponents/v5/ui"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// --- effectiveResumeScope / effectiveResumeItem ---------------------------------

func TestEffectiveResumeScopeFallsBackToResumeWhenNoLandingChosen(t *testing.T) {
	tr := mustRuntime(t)
	cases := []map[string]string{
		nil,
		{},
		{"read.kind": "unread"},
		// landing.mode present but not "fixed" — still ordinary resume, not a
		// half-applied landing choice.
		{"landing.mode": "", "landing.kind": "myfeed"},
	}
	for _, p := range cases {
		got := effectiveResumeScope(p, tr)
		want := resumeScope(p, tr)
		if got != want {
			t.Errorf("effectiveResumeScope(%v) = %+v, want resumeScope's own answer %+v", p, got, want)
		}
	}
}

func TestEffectiveResumeScopeHonoursAFixedLandingView(t *testing.T) {
	tr := mustRuntime(t)
	cases := []struct {
		name string
		p    map[string]string
		want scope
	}{
		{
			"my feed",
			map[string]string{"landing.mode": landingModeFixed, "landing.kind": "myfeed",
				// A stale read.* pointing somewhere else must NOT leak through —
				// the whole point of a fixed choice is that it wins outright.
				"read.kind": "feed", "read.value": "src-9", "read.title": "Somewhere else"},
			scope{Title: "My Feed", MyFeed: true},
		},
		{
			"a specific feed",
			map[string]string{"landing.mode": landingModeFixed, "landing.kind": "feed",
				"landing.value": "src-1", "landing.title": "Ars Technica"},
			scope{SourceID: "src-1", Title: "Ars Technica"},
		},
		{
			"a specific tag",
			map[string]string{"landing.mode": landingModeFixed, "landing.kind": "tag",
				"landing.value": "tag-1", "landing.title": "Go"},
			scope{TagID: "tag-1", Title: "Go"},
		},
		{
			"a specific folder",
			map[string]string{"landing.mode": landingModeFixed, "landing.kind": "folder",
				"landing.value": "cat-1", "landing.title": "Tech"},
			scope{FolderID: "cat-1", Title: "Tech"},
		},
		{
			// A deleted feed's landing choice can no longer resolve — falls
			// through to the ordinary resume rather than an empty-id scope.
			"an unresolvable fixed choice falls back to resume",
			map[string]string{"landing.mode": landingModeFixed, "landing.kind": "feed",
				"landing.value": "", "read.kind": "unread"},
			scope{Title: "Unread", Unread: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveResumeScope(c.p, tr); got != c.want {
				t.Errorf("effectiveResumeScope(%v) = %+v, want %+v", c.p, got, c.want)
			}
		})
	}
}

func TestEffectiveResumeItemIsClearedByAFixedLandingView(t *testing.T) {
	// Resuming as usual carries the last-open article along.
	if got := effectiveResumeItem(map[string]string{"read.item": "item-1"}); got != "item-1" {
		t.Errorf("effectiveResumeItem (no landing override) = %q, want %q", got, "item-1")
	}
	// A fixed landing view opens the LIST, not yesterday's article under a
	// different banner — see effectiveResumeItem's doc comment.
	got := effectiveResumeItem(map[string]string{
		"landing.mode": landingModeFixed, "landing.kind": "myfeed", "read.item": "item-1",
	})
	if got != "" {
		t.Errorf("effectiveResumeItem (fixed landing) = %q, want empty", got)
	}
}

// --- landingTitleFor --------------------------------------------------------------

func TestLandingTitleForResolvesLiveNames(t *testing.T) {
	tr := mustRuntime(t)
	feeds := []*pb.Feed{{SourceId: "src-1", Title: "Ars Technica"}}
	tags := []*pb.Tag{{Id: "tag-1", Name: "golang"}}
	folders := []*pb.Folder{{Id: "cat-1", Name: "Tech"}}

	cases := []struct {
		kind, value, want string
	}{
		{"feed", "src-1", "Ars Technica"},
		{"tag", "tag-1", "golang"},
		{"folder", "cat-1", "Tech"},
		{"feed", "src-missing", ""},
		{"myfeed", "", "My Feed"},
		{"unread", "", "Unread"},
	}
	for _, c := range cases {
		got := landingTitleFor(c.kind, c.value, tr, feeds, tags, folders)
		if got != c.want {
			t.Errorf("landingTitleFor(%q, %q) = %q, want %q", c.kind, c.value, got, c.want)
		}
	}
}

// --- landingViewPicker: the settings control ---------------------------------

func TestLandingViewPickerDefaultsToResumeAndOffersEveryScope(t *testing.T) {
	p := settingsProps{
		landingFeeds:   []*pb.Feed{{SourceId: "src-1", Title: "Ars Technica"}},
		landingTags:    []*pb.Tag{{Id: "tag-1", Name: "golang"}},
		landingFolders: []*pb.Folder{{Id: "cat-1", Name: "Tech"}},
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return landingViewPicker(tr, p) })

	resumeOpt := elementTag(t, out, `value="resume"`)
	if !strings.Contains(resumeOpt, `selected`) {
		t.Errorf("with no landing.mode set, the resume option is not selected: %s", resumeOpt)
	}
	for _, want := range []string{
		"Resume where I left off", "All articles", "Unread", "My Feed", "Read later", "Notes",
		"Ars Technica", "golang", "Tech",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("landingViewPicker output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestLandingViewPickerMarksTheFixedChoiceSelected(t *testing.T) {
	p := settingsProps{
		landingMode:  landingModeFixed,
		landingKind:  "feed",
		landingValue: "src-1",
		landingFeeds: []*pb.Feed{{SourceId: "src-1", Title: "Ars Technica"}},
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return landingViewPicker(tr, p) })

	feedOpt := elementTag(t, out, `value="feed:src-1"`)
	if !strings.Contains(feedOpt, "selected") {
		t.Errorf("the chosen feed option is not selected: %s", feedOpt)
	}
	resumeOpt := elementTag(t, out, `value="resume"`)
	if strings.Contains(resumeOpt, "selected") {
		t.Errorf("the resume option is selected alongside a fixed choice: %s", resumeOpt)
	}
}

// A landing choice naming a feed the current lists no longer contain — the
// feed was unsubscribed since — must not silently fall back to "All": that
// would rewrite the reader's stored choice the next time this select is
// touched, without their ever having chosen that.
func TestLandingViewPickerKeepsAnUnresolvedChoiceVisible(t *testing.T) {
	p := settingsProps{
		landingMode:  landingModeFixed,
		landingKind:  "feed",
		landingValue: "src-gone",
		landingTitle: "A Feed I Unsubscribed From",
	}
	out := renderView(t, func(tr i18n.Runtime) ui.Node { return landingViewPicker(tr, p) })

	if !strings.Contains(out, "A Feed I Unsubscribed From") {
		t.Errorf("an unresolvable landing choice's saved title was dropped:\n%s", out)
	}
	opt := elementTag(t, out, `value="feed:src-gone"`)
	if !strings.Contains(opt, "selected") {
		t.Errorf("the unresolved choice's own option is not selected: %s", opt)
	}
}
