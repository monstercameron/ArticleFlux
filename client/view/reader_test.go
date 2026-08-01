//go:build js && wasm

package view

import (
	"strconv"
	"testing"
	"time"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// --- resumeScope: the saved-place -> scope mapping ------------------------------

func TestResumeScope(t *testing.T) {
	tr := mustRuntime(t)

	cases := []struct {
		name string
		p    map[string]string
		want scope
	}{
		{"nil prefs map falls back to All", nil, scope{Title: "All articles"}},
		{"empty prefs map falls back to All", map[string]string{}, scope{Title: "All articles"}},
		{"unrecognised kind falls back to All", map[string]string{"read.kind": "not-a-real-kind"}, scope{Title: "All articles"}},
		{"unread", map[string]string{"read.kind": "unread"}, scope{Title: "Unread", Unread: true}},
		{"liked", map[string]string{"read.kind": "liked"}, scope{Title: "Liked", Rating: 1}},
		{"later", map[string]string{"read.kind": "later"}, scope{Title: "Read later", Later: true}},
		{"notes", map[string]string{"read.kind": "notes"}, scope{Title: "Notes", Notes: true}},
		{
			"feed with a value and a saved title",
			map[string]string{"read.kind": "feed", "read.value": "src-1", "read.title": "Ars Technica"},
			scope{SourceID: "src-1", Title: "Ars Technica"},
		},
		{
			"tag with a value and a saved title",
			map[string]string{"read.kind": "tag", "read.value": "tag-1", "read.title": "Go"},
			scope{TagID: "tag-1", Title: "Go"},
		},
		{
			"folder with a value and a saved title",
			map[string]string{"read.kind": "folder", "read.value": "cat-1", "read.title": "Tech"},
			scope{FolderID: "cat-1", Title: "Tech"},
		},
		{
			"search with a value and a saved title",
			map[string]string{"read.kind": "search", "read.value": "rust", "read.title": "rust"},
			scope{Search: "rust", Title: "rust"},
		},
		{
			"feed kind with an EMPTY value is treated as unset, not as a feed with an empty id",
			map[string]string{"read.kind": "feed", "read.value": "", "read.title": "should be ignored"},
			scope{Title: "All articles"},
		},
		{
			"tag kind with an empty value is likewise unset",
			map[string]string{"read.kind": "tag", "read.value": ""},
			scope{Title: "All articles"},
		},
		{
			"folder kind with an empty value is likewise unset",
			map[string]string{"read.kind": "folder", "read.value": ""},
			scope{Title: "All articles"},
		},
		{
			"search kind with an empty value is likewise unset",
			map[string]string{"read.kind": "search", "read.value": ""},
			scope{Title: "All articles"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resumeScope(c.p, tr); got != c.want {
				t.Errorf("resumeScope(%v) = %+v, want %+v", c.p, got, c.want)
			}
		})
	}
}

// TestResumeScopeFeedWithNoSavedTitleTakesAllsTitle pins a documented,
// deliberate quirk rather than a bug: resumeScope's own comment says "a feed
// whose title was never saved takes All's title too — an empty header is
// worse than a slightly wrong one." So the SourceID is honoured (the list
// really does filter to that feed) while the visible title says "All articles"
// — an inconsistency the code accepts on purpose. If this ever changes, it
// should change on purpose, which is what this test is for.
func TestResumeScopeFeedWithNoSavedTitleTakesAllsTitle(t *testing.T) {
	tr := mustRuntime(t)
	got := resumeScope(map[string]string{"read.kind": "feed", "read.value": "src-1", "read.title": ""}, tr)
	if got.SourceID != "src-1" {
		t.Errorf("resumeScope kept SourceID = %q, want \"src-1\"", got.SourceID)
	}
	if got.Title != "All articles" {
		t.Errorf("resumeScope.Title = %q, want the documented fallback %q", got.Title, "All articles")
	}
}

// TestResumeScopeNeverRestoresADislikedScope is the reader.go half of the
// confirmed disliked-stream defect (see palette_test.go for the palette
// half). resumeScope has an explicit case for every other verdict/membership
// kind — unread, liked, later, notes — but none for "disliked", so even if
// something upstream someday persists that kind, there is no path back to
// scope.Rating < 0. EXPECTED TO FAIL until the product grows a "disliked"
// case (or a documented decision that there deliberately isn't one).
func TestResumeScopeNeverRestoresADislikedScope(t *testing.T) {
	tr := mustRuntime(t)
	got := resumeScope(map[string]string{"read.kind": "disliked"}, tr)
	if got.Rating < 0 {
		return // fixed
	}
	t.Errorf("BUG: resumeScope(read.kind=\"disliked\") = %+v, want a scope with "+
		"Rating < 0 — resumeScope handles unread/liked/later/notes but has no "+
		"case for disliked, consistent with scope.Rating never being set to -1 "+
		"anywhere in client/view", got)
}

// --- toggleUnreadResult: what pressing "u" (or the chip) actually does ----------

// TestToggleUnreadResult is Bug 5 (and the click half of Bug 4): the
// toggle-unread action used to just flip unreadOnly regardless of scope. That
// is a no-op on screen when sel.Unread is true, because loadItems ORs the two
// together (`unreadOnly.Get() || s.Unread`) — so pressing u on the rail's
// Unread stream looked like it did nothing, contradicting both the chip
// (TestListHeadToggleUnreadChipReflectsEffectiveFilter, components_test.go)
// and emptyUnreadHint's promise "Press u to show everything again."
//
// toggleUnreadResult is the pure decision extracted from that action so this
// can be pinned without mounting the whole Reader closure.
func TestToggleUnreadResult(t *testing.T) {
	tr := mustRuntime(t)
	allScope := scope{Title: "All articles"}

	cases := []struct {
		name           string
		sel            scope
		unreadOnly     bool
		wantDest       scope
		wantUnreadOnly bool
		wantLeaving    bool
	}{
		{
			name: "the plain toggle, currently off, stays on the same scope",
			sel:  scope{Title: "Ars Technica", SourceID: "src-1"}, unreadOnly: false,
			wantDest: scope{Title: "Ars Technica", SourceID: "src-1"}, wantUnreadOnly: true, wantLeaving: false,
		},
		{
			name: "the plain toggle, currently on, stays on the same scope",
			sel:  scope{Title: "Ars Technica", SourceID: "src-1"}, unreadOnly: true,
			wantDest: scope{Title: "Ars Technica", SourceID: "src-1"}, wantUnreadOnly: false, wantLeaving: false,
		},
		{
			// The bug: sel.Unread forces unread-only through loadItems' OR
			// independently of unreadOnly, so there is no "Unread stream but
			// not unread-only" state — the only way off is to leave the
			// stream, back to All.
			name: "the rail's Unread stream, unreadOnly off, leaves the stream",
			sel:  scope{Title: "Unread", Unread: true}, unreadOnly: false,
			wantDest: allScope, wantUnreadOnly: false, wantLeaving: true,
		},
		{
			name: "the rail's Unread stream with unreadOnly ALSO on, still leaves the stream",
			sel:  scope{Title: "Unread", Unread: true}, unreadOnly: true,
			wantDest: allScope, wantUnreadOnly: false, wantLeaving: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest, nextUnreadOnly, leaving := toggleUnreadResult(tr, c.sel, c.unreadOnly)
			if dest != c.wantDest {
				t.Errorf("toggleUnreadResult(sel=%+v, unreadOnly=%v) dest = %+v, want %+v",
					c.sel, c.unreadOnly, dest, c.wantDest)
			}
			if nextUnreadOnly != c.wantUnreadOnly {
				t.Errorf("toggleUnreadResult(sel=%+v, unreadOnly=%v) nextUnreadOnly = %v, want %v",
					c.sel, c.unreadOnly, nextUnreadOnly, c.wantUnreadOnly)
			}
			if leaving != c.wantLeaving {
				t.Errorf("toggleUnreadResult(sel=%+v, unreadOnly=%v) leaving = %v, want %v",
					c.sel, c.unreadOnly, leaving, c.wantLeaving)
			}
			// The promise this whole fix exists to keep: after leaving,
			// unread-only must actually be OFF and the destination scope must
			// not itself be forcing it back on.
			if leaving && (nextUnreadOnly || dest.Unread) {
				t.Errorf("toggleUnreadResult(sel=%+v) leaves the stream but the result "+
					"still forces unread-only (nextUnreadOnly=%v, dest.Unread=%v) — "+
					"\"press u to show everything again\" would still be false",
					c.sel, nextUnreadOnly, dest.Unread)
			}
		})
	}
}

// --- relTime -----------------------------------------------------------------

func TestRelTime(t *testing.T) {
	tr := mustRuntime(t)

	t.Run("unparsable timestamp is the empty string", func(t *testing.T) {
		for _, in := range []string{"", "not-a-date", "2020-13-99"} {
			if got := relTime(tr, in); got != "" {
				t.Errorf("relTime(%q) = %q, want empty string", in, got)
			}
		}
	})

	t.Run("under a minute old is \"now\"", func(t *testing.T) {
		ts := time.Now().Add(-10 * time.Second).Format(time.RFC3339Nano)
		if got := relTime(tr, ts); got != "now" {
			t.Errorf("relTime(10s ago) = %q, want %q", got, "now")
		}
	})

	t.Run("under an hour old is in minutes", func(t *testing.T) {
		// 90s -> int(1.5) = 1 minute; chosen away from any rounding boundary.
		ts := time.Now().Add(-90 * time.Second).Format(time.RFC3339Nano)
		if got := relTime(tr, ts); got != "1m" {
			t.Errorf("relTime(90s ago) = %q, want %q", got, "1m")
		}
	})

	t.Run("under a day old is in hours", func(t *testing.T) {
		// Just over 1h so elapsed test time cannot push it under the 1h
		// threshold and land in the minutes bucket instead.
		ts := time.Now().Add(-90 * time.Minute).Format(time.RFC3339Nano)
		if got := relTime(tr, ts); got != "1h" {
			t.Errorf("relTime(90m ago) = %q, want %q", got, "1h")
		}
	})

	t.Run("under a week old is in days", func(t *testing.T) {
		ts := time.Now().Add(-50 * time.Hour).Format(time.RFC3339Nano) // ~2.08 days
		if got := relTime(tr, ts); got != "2d" {
			t.Errorf("relTime(50h ago) = %q, want %q", got, "2d")
		}
	})

	t.Run("a week or older is day-and-month, in the reader's language", func(t *testing.T) {
		when := time.Now().Add(-10 * 24 * time.Hour) // safely past the 7-day cutoff
		ts := when.Format(time.RFC3339Nano)
		want := strconv.Itoa(when.Day()) + " " + monthAbbrev(when.Month())
		if got := relTime(tr, ts); got != want {
			t.Errorf("relTime(10 days ago) = %q, want %q", got, want)
		}
	})

	t.Run("does NOT use Go's own English month name (t.Format), it uses the catalog", func(t *testing.T) {
		// A regression to `t.Format("2 Jan")` would still pass every other
		// case in this file (English happens to agree), so this pins it
		// directly: the catalog's own abbreviations are what relTime must
		// consult, not time.Time.Format.
		when := time.Date(2026, time.March, 3, 12, 0, 0, 0, time.UTC)
		// Far enough in the past relative to "now" in this repo's era to
		// land in the day-and-month bucket regardless of when the suite runs.
		if time.Since(when) < 7*24*time.Hour {
			t.Skip("clock skew: `when` is not yet outside the 7-day window")
		}
		got := relTime(tr, when.Format(time.RFC3339Nano))
		want := "3 Mar"
		if got != want {
			t.Errorf("relTime(2026-03-03) = %q, want %q", got, want)
		}
	})
}

// monthAbbrev mirrors the English catalog's month namespace (client/i18n's
// en_time.go), independently of relTime, so the day-and-month test above
// derives its expectation from the same input rather than a hand-copied
// hardcoded date that would rot.
func monthAbbrev(m time.Month) string {
	names := [...]string{
		"", "Jan", "Feb", "Mar", "Apr", "May", "Jun",
		"Jul", "Aug", "Sep", "Oct", "Nov", "Dec",
	}
	return names[int(m)]
}

// --- navItem: the j/k and list-arrow index math -----------------------------
//
// navItem is the pure function the keyboard listener's j/k and up/down-arrow
// cases call THROUGH act.Get().navStep rather than computing inline — see the
// actions struct's navStep field in reader.go. The bug on record ("j not
// advancing on the second press", and k opening the wrong article's title)
// was never in this math: given a fresh list and a fresh current article it
// has always resolved correctly. It was that the OLD inline code read
// items.Get()/current.Get() directly inside the keyboard listener, which is
// registered exactly once — a ui.State read from inside a closure like that
// answers with the render that CREATED the closure, forever, not the render
// that is live when a key is actually pressed (plan.md §20.10; the same
// hazard reader.go's pageLanded, fill, speakSeen and the signals tracker are
// all routed around already). That class of bug cannot be reproduced by
// calling a pure function directly — it only exists in the wiring between the
// listener and ui.State — so this table pins the math's contract instead: the
// thing the wiring now has to keep feeding fresh, correctly.
func idAt(list []*pb.Item, i int) *pb.Item {
	if i < 0 || i >= len(list) {
		return nil
	}
	return list[i]
}

func mkList(ids ...string) []*pb.Item {
	list := make([]*pb.Item, len(ids))
	for i, id := range ids {
		list[i] = &pb.Item{Id: id}
	}
	return list
}

func TestNavItem(t *testing.T) {
	abc := mkList("a", "b", "c")

	cases := []struct {
		name    string
		list    []*pb.Item
		current *pb.Item
		delta   int
		want    *pb.Item
	}{
		{"empty list, advancing, nothing current", nil, nil, 1, nil},
		{"empty list, retreating, nothing current", nil, nil, -1, nil},
		{"single item, nothing current, advancing opens it", mkList("a"), nil, 1, idAt(mkList("a"), 0)},
		{"single item, it is current, advancing has nowhere to go", mkList("a"), mkList("a")[0], 1, nil},
		{"single item, it is current, retreating has nowhere to go", mkList("a"), mkList("a")[0], -1, nil},
		{"first of three, advancing goes to the second", abc, idAt(abc, 0), 1, idAt(abc, 1)},
		{"first of three, retreating has nowhere to go", abc, idAt(abc, 0), -1, nil},
		{"last of three, advancing has nowhere to go", abc, idAt(abc, 2), 1, nil},
		{"last of three, retreating goes to the middle one", abc, idAt(abc, 2), -1, idAt(abc, 1)},
		{"current not in the list, advancing opens the first item", abc, &pb.Item{Id: "not-in-list"}, 1, idAt(abc, 0)},
		{"current not in the list, retreating has nowhere to go", abc, &pb.Item{Id: "not-in-list"}, -1, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := navItem(c.list, c.current, c.delta)
			gotID, wantID := "<nil>", "<nil>"
			if got != nil {
				gotID = got.GetId()
			}
			if c.want != nil {
				wantID = c.want.GetId()
			}
			if gotID != wantID {
				t.Errorf("navItem(list, current=%v, delta=%d) = %v, want %v", c.current, c.delta, gotID, wantID)
			}
		})
	}
}

// TestNavItemRepeatedAdvancesWalkTheWholeList pins the exact shape of the
// on-record bug: EACH call must be fed the article the PREVIOUS call opened
// (which is what act.Get().navStep, read fresh, gives the real listener) — if
// this instead fed every call the same stale "current" the old inline code
// was frozen on, j would keep landing on the second item forever, which is
// precisely "not advancing on the second press".
func TestNavItemRepeatedAdvancesWalkTheWholeList(t *testing.T) {
	list := mkList("a", "b", "c", "d")

	var current *pb.Item
	var got []string
	for i := 0; i < len(list)+1; i++ {
		next := navItem(list, current, 1)
		if next == nil {
			got = append(got, "<nil>")
			break
		}
		got = append(got, next.GetId())
		current = next
	}
	want := []string{"a", "b", "c", "d", "<nil>"}
	if len(got) != len(want) {
		t.Fatalf("advanced through %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("advance #%d = %q, want %q (full sequence: %v)", i+1, got[i], want[i], got)
		}
	}
}

// TestNavItemRepeatedRetreatsWalkBackToTheStart is the k-direction mirror:
// repeated retreats, each fed the article the previous retreat opened, must
// walk backward one item at a time and then stop, never skip an item and
// never open one that is not adjacent to where the reader actually is.
func TestNavItemRepeatedRetreatsWalkBackToTheStart(t *testing.T) {
	list := mkList("a", "b", "c", "d")
	current := idAt(list, 3) // starting on the last item

	var got []string
	for i := 0; i < len(list); i++ {
		prev := navItem(list, current, -1)
		if prev == nil {
			got = append(got, "<nil>")
			break
		}
		got = append(got, prev.GetId())
		current = prev
	}
	want := []string{"c", "b", "a", "<nil>"}
	if len(got) != len(want) {
		t.Fatalf("retreated through %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("retreat #%d = %q, want %q (full sequence: %v)", i+1, got[i], want[i], got)
		}
	}
}

// TestNavItemGivenAStaleCurrentReproducesTheOnRecordBug demonstrates that
// navItem's math is not, and never was, where the bug lived: feeding it the
// SAME "current" on every call — which is exactly what the old inline
// `items.Get()`/`current.Get()` read did, frozen at whichever render mounted
// the keyboard listener — reproduces "j not advancing on the second press"
// deterministically. The fix is not in this function; it is that
// act.Get().navStep (reader.go) now re-reads current fresh on every keypress
// instead of handing navItem a stale one, which is what
// TestNavItemRepeatedAdvancesWalkTheWholeList proves it does correctly.
func TestNavItemGivenAStaleCurrentReproducesTheOnRecordBug(t *testing.T) {
	list := mkList("a", "b", "c")
	stale := (*pb.Item)(nil) // whatever was current when the listener mounted

	first := navItem(list, stale, 1)
	second := navItem(list, stale, 1) // the bug: fed the SAME stale current again

	if first == nil || second == nil {
		t.Fatalf("first=%v second=%v, want both non-nil", first, second)
	}
	if first.GetId() != second.GetId() {
		t.Fatalf("first=%q second=%q, want them EQUAL — that repeat is the bug this reproduces", first.GetId(), second.GetId())
	}
	if first.GetId() != "a" {
		t.Fatalf("both presses opened %q, want %q (list[0], since a stale/absent current can only ever resolve to the first item)", first.GetId(), "a")
	}
}
