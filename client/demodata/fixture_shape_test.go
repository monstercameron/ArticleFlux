package demodata

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/design"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// This file pins the seeded instance's documented shape (seed.go's own header
// comment) as a set of assertions, so a fixture edit that quietly breaks one of
// the properties the demo exists to demonstrate fails a test instead of only
// showing up when someone happens to look at that particular screen.
//
// Deliberately NOT re-pinned here, because existing tests already cover them
// precisely: exactly one failing feed (TestOneFeedIsFailing), at least two
// articles over the 900-word clamp (TestSomethingIsLongEnoughToClamp), and
// SetSmartConfig's refusal (TestSmartPlusRefusesInTheShapeTheScreenExpects).

// TestFixtureSevenSourcesEachGetADistinctNamedHue ties the fixture's own stated
// reason for having seven feeds ("seven is the size of the hand-picked hue
// palette", seed.go) to design.HueFor, the function that actually assigns the
// colour. Seven is not just a count worth being exactly seven; the point of the
// number is that no two sidebar dots collide and no source falls through to a
// generated (non-named) hue — either of those would be invisible in the fixture
// count alone.
//
// CURRENTLY FAILING, and left in rather than loosened — this is a real defect,
// not a bad test. design.HueFor assigns a slot with hash(id) % 24 (tokens.go),
// which has no way to honour tokens.go's own claim that "a small subscription
// list gets exactly the mockup's palette": that would require the first N
// DISTINCT ids to claim slots 0..N-1 in order, and the function has no such
// state — it is a stateless hash, deliberately, so the server and the wasm
// client can agree on a colour without coordinating. The demo's seven source
// ids are sequential ("src-5", "src-7", ... "src-17", assigned in seed.go via
// Instance.nextID, interleaved with three folder ids allocated first), and two
// of the seven — "Hackline Daily" (src-9) and "Paper & Ink" (src-17) — hash
// into the generated range and render as washed-out oklch() swatches instead
// of two of the seven hand-picked colours (coral #FF8A6B and lilac #C4A6FF
// never appear at all). That directly contradicts seed.go's own stated reason
// for picking exactly seven feeds. See client/design/tokens.go:75 (the "a small
// subscription list gets exactly the mockup's palette" doc claim) and
// client/design/tokens.go:110-119 (HueFor's actual hash(id)%24 slot logic), and
// client/demodata/seed.go:21-24 (the seven-feed rationale) with the sequential
// id assignment in seed.go's seed() function (client/demodata/seed.go:456-486).
// Fixing it is a product decision (give the demo's seven sources ids chosen to
// land in the named range, change HueFor to assign the first few distinct ids
// deterministically, or correct the doc claim) — not this test's call.
func TestFixtureSevenSourcesEachGetADistinctNamedHue(t *testing.T) {
	_, r := newTest(t)
	res, err := r.ListFeeds(context.Background(), &pb.ListFeedsRequest{})
	if err != nil {
		t.Fatalf("ListFeeds: %v", err)
	}
	if len(res.GetFeeds()) != 7 {
		t.Fatalf("got %d feeds, want exactly 7 — the fixture's own reason for the "+
			"count is that 7 is the size of the hand-picked hue palette", len(res.GetFeeds()))
	}

	named := map[string]bool{}
	for _, sw := range design.AccentsFor(design.ToneDark) {
		named[sw.Hex] = true
	}
	seen := map[string]string{} // hue -> first source title that claimed it
	for _, f := range res.GetFeeds() {
		hue := design.HueFor(f.GetSourceId())
		if !named[hue] {
			t.Errorf("%q's hue %s is not one of the seven named source hues — "+
				"with exactly 7 sources this should never fall through to a generated one",
				f.GetTitle(), hue)
			continue
		}
		if other, dup := seen[hue]; dup {
			t.Errorf("%q and %q share hue %s — the whole point of the seven-source "+
				"fixture is that the sidebar has no colour collision", f.GetTitle(), other, hue)
		}
		seen[hue] = f.GetTitle()
	}
}

// TestFixtureHasThreeCategoriesAndThreeTags pins the two counts seed.go's
// comment calls out by name, and the reason both are three: "the difference
// between them — where a feed LIVES versus what you SAY about it — is visible
// rather than described." Three of each, sized so a reader can see one feed
// carrying a tag its category-mates do not (see TestTagsAreCreatedByUseAndRetiredTheSameWay's
// "keep" tag, which spans Technology and Culture).
func TestFixtureHasThreeCategoriesAndThreeTags(t *testing.T) {
	_, r := newTest(t)
	folders, err := r.ListFolders(context.Background(), &pb.ListFoldersRequest{})
	if err != nil {
		t.Fatalf("ListFolders: %v", err)
	}
	if len(folders.GetFolders()) != 3 {
		t.Errorf("got %d categories, want 3", len(folders.GetFolders()))
	}
	tags, err := r.ListTags(context.Background(), &pb.ListTagsRequest{})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if len(tags.GetTags()) != 3 {
		t.Errorf("got %d tags, want 3", len(tags.GetTags()))
	}
}

// TestFixtureArticleCountAndAgeSpan pins the 22-visible/4-held/26-total split
// and the age range: the oldest visible article is exactly eleven days old (the
// scale at which relative time stops counting days and would need a different
// format) and the newest is well under an hour (the scale at which it renders
// in minutes). Both ends matter for the same reason: a fixture that drifted to
// "everything published this morning" would still pass every other test here
// while silently no longer showing relative time at more than one scale.
func TestFixtureArticleCountAndAgeSpan(t *testing.T) {
	c, r := newTest(t)

	visible, err := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: maxLimit,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	const wantVisible = 22
	if len(visible.GetItems()) != wantVisible {
		t.Errorf("got %d visible items, want %d", len(visible.GetItems()), wantVisible)
	}
	if int(visible.GetTotal()) != wantVisible {
		t.Errorf("total is %d, want %d — the virtual list would size its scrollbar wrong",
			visible.GetTotal(), wantVisible)
	}

	const wantHeld = 4
	if held := len(c.Instance().incoming); held != wantHeld {
		t.Errorf("got %d held-back articles, want %d", held, wantHeld)
	}
	const wantTotal = wantVisible + wantHeld
	if wantTotal != 26 {
		t.Fatalf("test arithmetic is wrong: %d + %d != 26", wantVisible, wantHeld)
	}

	var min, max time.Duration
	for i, it := range visible.GetItems() {
		published, err := time.Parse(time.RFC3339, it.GetPublishedAt())
		if err != nil {
			t.Fatalf("%q has an unparseable stamp %q", it.GetTitle(), it.GetPublishedAt())
		}
		age := epoch.Sub(published)
		if i == 0 || age < min {
			min = age
		}
		if i == 0 || age > max {
			max = age
		}
	}
	// seed.go's own comment says "twenty minutes to eleven days"; the newest
	// item is actually 22 minutes old (Hackline Daily's "Standards body
	// publishes revised timeline..."), which still renders at the minutes
	// scale the comment is describing — this pins the real value rather than
	// the rounded one in the prose.
	if want := 22 * time.Minute; min != want {
		t.Errorf("newest visible article is %s old, want exactly %s", min, want)
	}
	if want := 11 * 24 * time.Hour; max != want {
		t.Errorf("oldest visible article is %s old, want exactly %s", max, want)
	}
}

// TestFixtureRepresentsEveryReadingState pins "a mix of read, unread, starred,
// rated and annotated, so no screen in the application is empty on arrival"
// (seed.go) as an actual assertion over the wire shape, rather than trusting
// that whoever next edits seedItems preserves a property the file only asserts
// in a comment. Both directions of rating are checked because SetItemState
// clamps to [-1, 1] and a fixture with only positive ratings would never have
// exercised the negative branch of anything a screenshot could catch.
func TestFixtureRepresentsEveryReadingState(t *testing.T) {
	_, r := newTest(t)
	all, err := r.ListItems(context.Background(), &pb.ListItemsRequest{
		Scope: pb.ListScope_LIST_SCOPE_ALL, Limit: maxLimit,
	})
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	var hasRead, hasUnread, hasStarred, hasPositive, hasNegative bool
	for _, it := range all.GetItems() {
		switch {
		case it.GetRead():
			hasRead = true
		default:
			hasUnread = true
		}
		if it.GetStarred() {
			hasStarred = true
		}
		if it.GetRating() > 0 {
			hasPositive = true
		}
		if it.GetRating() < 0 {
			hasNegative = true
		}
	}
	for _, tc := range []struct {
		name string
		got  bool
	}{
		{"read", hasRead}, {"unread", hasUnread}, {"starred", hasStarred},
		{"positively rated", hasPositive}, {"negatively rated", hasNegative},
	} {
		if !tc.got {
			t.Errorf("no visible article is %s — that screen or state opens empty in the demo", tc.name)
		}
	}

	notes, err := r.ListNotes(context.Background(), &pb.ListNotesRequest{})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes.GetItems()) == 0 {
		t.Error("no visible article is annotated — the notes screen opens empty in the demo")
	}
}

// TestTranslateUIRefusesForTheDemo covers the one Smart+ method the rest of
// the suite never calls. GetSmartConfig and SetSmartConfig are exercised by
// TestSmartPlusRefusesInTheShapeTheScreenExpects, but TranslateUI — the other
// half of "the demo cannot hold a key" — had no test calling it at all, so a
// change that made it silently return the English catalog instead of refusing
// (the worst failure mode: a language switch that appears to work and changes
// nothing, per the doc comment on smartService.TranslateUI) would have shipped
// with every existing test green.
func TestTranslateUIRefusesForTheDemo(t *testing.T) {
	c := New(func() time.Time { return epoch })
	s := pb.NewSmartServiceClient(c)
	_, err := s.TranslateUI(context.Background(), &pb.TranslateUIRequest{
		Locale: "es",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("TranslateUI returned %v, want FailedPrecondition — a demo with no "+
			"OpenAI key must refuse, not silently return the English catalog", err)
	}
}
