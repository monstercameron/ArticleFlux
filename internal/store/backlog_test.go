package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A backlog window must mark exactly the articles it names and nothing else.
// Everything here is about what it must NOT touch, because "mark things read" is
// a write the reader cannot see happen: a rule that over-reached would show up
// as an article that was never read being gone from the unread list.

type backlogFixture struct {
	repo  *ReaderRepo
	ctx   context.Context
	me    Scope
	other Scope // same tenant, same feed, different reader
	a, b  string
	ids   map[string]string // label -> item id
}

func newBacklogFixture(t *testing.T) *backlogFixture {
	t.Helper()
	ctx := context.Background()
	repo, me := seedReader(t, openTest(t))
	now := time.Now().UTC()

	if err := repo.AddUser(ctx, NewTenant{
		TenantID: "t1", UserID: "u2", Username: "sam", Hash: "x", Role: "member",
		Now: stamp(now),
	}); err != nil {
		t.Fatal(err)
	}
	other := Scope{TenantID: "t1", UserID: "u2", Role: "member"}
	f := &backlogFixture{repo: repo, ctx: ctx, me: me, other: other, ids: map[string]string{}}

	// seedReader already follows feed:a and feed:b; the second reader follows A.
	feeds, err := repo.ListFeeds(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range feeds {
		switch fd.Title {
		case "Alpha Journal":
			f.a = fd.SourceID
		case "Beta Notes":
			f.b = fd.SourceID
		}
	}
	if f.a == "" || f.b == "" {
		t.Fatalf("seed feeds not found: %+v", feeds)
	}
	if _, _, err := repo.Subscribe(ctx, other, NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha Journal",
	}); err != nil {
		t.Fatal(err)
	}

	day := 24 * time.Hour
	labels := map[string]bool{}
	for _, c := range []struct {
		src, label string
		age        time.Duration
	}{
		{f.a, "a-old", 5 * day},
		{f.a, "a-old2", 40 * day},
		{f.a, "a-new", 2 * time.Hour},
		{f.a, "a-starred", 10 * day},
		{f.a, "a-rated", 10 * day},
		{f.b, "b-old", 10 * day},
	} {
		labels[c.label] = true
		if _, err := repo.IngestItems(ctx, c.src, []IngestItem{{
			GUID: c.label, URL: "https://x.example/" + c.label, Title: c.label,
			Summary: "s", PublishedAt: now.Add(-c.age),
		}}); err != nil {
			t.Fatal(err)
		}
	}
	items, _, err := repo.ListItems(ctx, me, ListQuery{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if labels[it.Title] {
			f.ids[it.Title] = it.ID
		}
	}
	yes, up := true, 1
	if _, err := repo.SetItemState(ctx, me, f.ids["a-starred"], StateChange{Starred: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetItemState(ctx, me, f.ids["a-rated"], StateChange{Rating: &up}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *backlogFixture) set(t *testing.T, sc Scope, src string, days int) {
	t.Helper()
	if _, err := f.repo.UpdateFeedSettings(f.ctx, sc, src, FeedSettingsPatch{ClearBacklogDays: &days}); err != nil {
		t.Fatalf("UpdateFeedSettings(%d): %v", days, err)
	}
}

func (f *backlogFixture) item(t *testing.T, sc Scope, label string) Item {
	t.Helper()
	it, err := f.repo.GetItem(f.ctx, sc, f.ids[label])
	if err != nil {
		t.Fatalf("GetItem(%s): %v", label, err)
	}
	return it
}

func TestClearBacklogMarksExactlyTheRowsInsideTheRule(t *testing.T) {
	cases := []struct {
		name     string
		days     int
		wantRead map[string]bool // label -> read afterwards, for MY state
		marked   int
	}{
		{"never does nothing", 0,
			map[string]bool{"a-old": false, "a-old2": false, "a-new": false, "a-starred": false, "a-rated": false, "b-old": false}, 0},
		{"a day clears everything older but the fresh one", 1,
			map[string]bool{"a-old": true, "a-old2": true, "a-new": false, "a-starred": false, "a-rated": true, "b-old": false}, 3},
		{"a week leaves the 5-day-old article alone", 7,
			map[string]bool{"a-old": false, "a-old2": true, "a-new": false, "a-starred": false, "a-rated": true, "b-old": false}, 2},
		{"a month only reaches the oldest", 30,
			map[string]bool{"a-old": false, "a-old2": true, "a-new": false, "a-starred": false, "a-rated": false, "b-old": false}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newBacklogFixture(t)
			before, err := f.repo.CountQuery(f.ctx, f.me, ListQuery{UnreadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			otherBefore, err := f.repo.CountQuery(f.ctx, f.other, ListQuery{UnreadOnly: true})
			if err != nil {
				t.Fatal(err)
			}

			f.set(t, f.me, f.a, c.days)
			got, err := f.repo.ClearBacklog(f.ctx, f.me, f.a)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.marked {
				t.Errorf("marked %d, want %d", got, c.marked)
			}
			// A second pass is what the sweep does every cycle and must find
			// nothing left to do.
			if again, err := f.repo.ClearBacklog(f.ctx, f.me, f.a); err != nil || again != 0 {
				t.Errorf("a second application marked %d more (%v)", again, err)
			}

			for label, wantRead := range c.wantRead {
				if got := f.item(t, f.me, label).Read; got != wantRead {
					t.Errorf("%s: read = %v, want %v", label, got, wantRead)
				}
			}
			// Starred stays starred, and the rating is as it was.
			if !f.item(t, f.me, "a-starred").Starred {
				t.Error("the starred article lost its star")
			}
			if got := f.item(t, f.me, "a-rated").Rating; got != 1 {
				t.Errorf("rating = %d, want 1 untouched", got)
			}

			// The other reader of the same feed is untouched, item by item.
			for label := range c.wantRead {
				if label == "b-old" {
					continue
				}
				if f.item(t, f.other, label).Read {
					t.Errorf("another reader's %s was marked read", label)
				}
			}
			otherAfter, _ := f.repo.CountQuery(f.ctx, f.other, ListQuery{UnreadOnly: true})
			if otherAfter != otherBefore {
				t.Errorf("another reader's unread went %d -> %d", otherBefore, otherAfter)
			}

			after, err := f.repo.CountQuery(f.ctx, f.me, ListQuery{UnreadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if after != before-c.marked {
				t.Errorf("unread went %d -> %d, want %d fewer", before, after, c.marked)
			}
		})
	}
}

func TestBacklogCountersAgreeWithARecount(t *testing.T) {
	f := newBacklogFixture(t)
	f.set(t, f.me, f.a, 3)
	if _, err := f.repo.ClearBacklog(f.ctx, f.me, f.a); err != nil {
		t.Fatal(err)
	}
	got, err := f.repo.CountQuery(f.ctx, f.me, ListQuery{UnreadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := recount(t, f.repo.db, f.me); got != want {
		t.Fatalf("the badge says %d unread, recomputing says %d", got, want)
	}
	// And the per-feed number the sidebar reads.
	feeds, err := f.repo.ListFeeds(f.ctx, f.me)
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range feeds {
		// The seed's three fresh articles per feed sit inside any window; feed A
		// then keeps a-new and a-starred, and B keeps its own old one.
		if fd.SourceID == f.a && fd.UnreadCount != 5 {
			t.Errorf("feed A unread = %d, want 5", fd.UnreadCount)
		}
		if fd.SourceID == f.b && fd.UnreadCount != 4 {
			t.Errorf("feed B unread = %d, want 4 (untouched)", fd.UnreadCount)
		}
	}
}

func TestClearBacklogsSweepsEachReadersOwnRules(t *testing.T) {
	f := newBacklogFixture(t)
	// A rule on B for ME only, written without applying it, as if set earlier.
	if _, err := f.repo.db.Write.ExecContext(f.ctx,
		`UPDATE subscriptions SET clear_backlog_days = 7 WHERE user_id = ? AND source_id = ?`,
		f.me.UserID, f.b); err != nil {
		t.Fatal(err)
	}

	scopes, err := f.repo.ScopesWithBacklogRules(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scopes) != 1 || scopes[0].UserID != f.me.UserID {
		t.Fatalf("scopes = %+v, want only the reader who set a rule", scopes)
	}

	cleared, err := f.repo.ClearBacklogs(f.ctx, scopes[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 1 || cleared[0].SourceID != f.b || cleared[0].Marked != 1 {
		t.Fatalf("cleared = %+v, want feed B with 1", cleared)
	}
	if !f.item(t, f.me, "b-old").Read {
		t.Error("the rule's own feed was not cleared")
	}
	if f.item(t, f.me, "a-old").Read {
		t.Error("a feed with no rule was cleared")
	}
	// A reader with no rule is not swept at all.
	if got, err := f.repo.ClearBacklogs(f.ctx, f.other); err != nil || len(got) != 0 {
		t.Errorf("ClearBacklogs(other) = %v, %v", got, err)
	}
}

func TestClearBacklogRefusesAFeedTheCallerDoesNotFollow(t *testing.T) {
	f := newBacklogFixture(t)
	if _, err := f.repo.ClearBacklog(f.ctx, f.other, f.b); !errors.Is(err, ErrNotFound) {
		t.Errorf("= %v, want ErrNotFound", err)
	}
	days := 7
	if _, err := f.repo.UpdateFeedSettings(f.ctx, f.other, f.b, FeedSettingsPatch{ClearBacklogDays: &days}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update on an unfollowed feed = %v, want ErrNotFound", err)
	}
	if _, err := f.repo.ClearBacklog(f.ctx, Scope{}, f.a); !errors.Is(err, ErrNoScope) {
		t.Errorf("no scope = %v, want ErrNoScope", err)
	}
}

func TestBacklogDaysAreBounded(t *testing.T) {
	f := newBacklogFixture(t)
	for in, want := range map[int]int{-5: 0, 0: 0, 7: 7, 9999: maxBacklogDays} {
		v := in
		got, err := f.repo.UpdateFeedSettings(f.ctx, f.me, f.a, FeedSettingsPatch{ClearBacklogDays: &v})
		if err != nil {
			t.Fatal(err)
		}
		if got.ClearBacklogDays != want {
			t.Errorf("stored %d, got %d, want %d", in, got.ClearBacklogDays, want)
		}
	}
}
