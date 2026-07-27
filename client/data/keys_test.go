package data

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The guard the key comment used to be.
//
// Adding a filter to `ListItemsRequest` and forgetting to put it in the key
// produces a cache that serves one filter's answer to another's question. That
// is not a crash — it is the reader seeing the wrong articles under the right
// heading, which is the failure mode this application has already had once
// (TODO H10) by a different route, and which nobody notices from the code.
//
// So the message's fields are enumerated and each one must either change the
// key or be listed here with a reason.
func TestEveryListFilterIsInTheKey(t *testing.T) {
	// Fields that legitimately do not change WHICH items come back.
	exempt := map[string]string{
		"cursor": "a cursor page is not cached at all; KeyItems returns \"\" for one",
		"limit":  "changes how many items come back, not which — a shorter page is a prefix of a longer one",
	}

	fields := (&pb.ListItemsRequest{}).ProtoReflect().Descriptor().Fields()
	if fields.Len() == 0 {
		t.Fatal("no fields found on ListItemsRequest; this test has stopped checking anything")
	}

	base := &pb.ListItemsRequest{}
	baseKey := KeyItems(base)

	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		name := string(f.Name())
		if why, ok := exempt[name]; ok {
			t.Logf("exempt: %s — %s", name, why)
			continue
		}

		// Set the field to something non-zero and check the key moves.
		mutated := &pb.ListItemsRequest{}
		m := mutated.ProtoReflect()
		switch {
		case f.IsList():
			l := m.Mutable(f).List()
			l.Append(protoreflect.ValueOfString("x"))
		case f.Kind() == protoreflect.StringKind:
			m.Set(f, protoreflect.ValueOfString("x"))
		case f.Kind() == protoreflect.BoolKind:
			m.Set(f, protoreflect.ValueOfBool(true))
		case f.Kind() == protoreflect.EnumKind:
			// The second value, because the first is the zero the base already has.
			vals := f.Enum().Values()
			if vals.Len() < 2 {
				t.Logf("skipping %s: its enum has one value, so it cannot vary", name)
				continue
			}
			m.Set(f, protoreflect.ValueOfEnum(vals.Get(1).Number()))
		case f.Kind() == protoreflect.Int32Kind:
			m.Set(f, protoreflect.ValueOfInt32(1))
		default:
			t.Errorf("%s has kind %v, which this test does not know how to vary — "+
				"teach it, or the field is unchecked", name, f.Kind())
			continue
		}

		if got := KeyItems(mutated); got == baseKey {
			t.Errorf("setting %s does not change the cache key (%q).\n"+
				"A request that filters differently would be served the other "+
				"filter's cached answer — the reader sees the wrong articles under "+
				"the right heading. Add it to KeyItems, or to `exempt` above with "+
				"a reason.", name, got)
		}
	}
}

// A cursor page must not be cached: the key would have to carry the cursor, and
// a cursor is only meaningful against the exact list that issued it.
func TestCursorPagesAreNotCacheable(t *testing.T) {
	if got := KeyItems(&pb.ListItemsRequest{Cursor: "abc"}); got != "" {
		t.Errorf("KeyItems for a cursor page = %q, want \"\"", got)
	}
	if got := KeyItems(&pb.ListItemsRequest{}); got == "" {
		t.Error("the first page is not cacheable, so nothing works offline")
	}
}

// The prefix hierarchy is what makes invalidation possible without enumerating
// what exists.
func TestKeysNestUnderTheirPrefix(t *testing.T) {
	items := KeyItems(&pb.ListItemsRequest{SourceId: "s1"})
	if !strings.HasPrefix(items, KeyPrefixItems) {
		t.Errorf("%q is not under %q, so dropping every list would miss it", items, KeyPrefixItems)
	}
	if !strings.HasPrefix(KeyItem("i1"), KeyPrefixItem) {
		t.Errorf("%q is not under %q", KeyItem("i1"), KeyPrefixItem)
	}
	// And the two families must not overlap, or dropping one drops the other:
	// "item:" is not a prefix of "items:" only because of the colon.
	if strings.HasPrefix(items, KeyPrefixItem) {
		t.Errorf("a list key %q begins with the ARTICLE prefix %q — dropping one "+
			"article's body would drop every list", items, KeyPrefixItem)
	}
}

// Two requests that mean the same thing must produce the same key, or the cache
// never hits.
func TestEqualRequestsProduceEqualKeys(t *testing.T) {
	a := &pb.ListItemsRequest{SourceIds: []string{"s1", "s2"}, UnreadOnly: true}
	b := &pb.ListItemsRequest{SourceIds: []string{"s1", "s2"}, UnreadOnly: true}
	if KeyItems(a) != KeyItems(b) {
		t.Errorf("%q != %q", KeyItems(a), KeyItems(b))
	}
	// And two that differ must not collide. `src=` and `s=` are different
	// prefixes precisely so that one source id in `source_id` and the same id in
	// `source_ids` are different questions.
	one := KeyItems(&pb.ListItemsRequest{SourceId: "s1"})
	many := KeyItems(&pb.ListItemsRequest{SourceIds: []string{"s1"}})
	if one == many {
		t.Errorf("source_id and source_ids collide on %q", one)
	}
}

// DropPrefix is what makes the key hierarchy usable — and the one thing it must
// never do is treat an empty prefix as "everything".
func TestDropPrefixRemovesAFamilyAndNothingElse(t *testing.T) {
	c := &Cache{}
	now := time.Now()
	c.Put(KeyItems(&pb.ListItemsRequest{SourceId: "s1"}), []byte("list one"), now)
	c.Put(KeyItems(&pb.ListItemsRequest{SourceId: "s2"}), []byte("list two"), now)
	c.Put(KeyItem("i1"), []byte("an article"), now)
	c.Put(KeyFeeds, []byte("the rail"), now)

	if got := c.DropPrefix(KeyPrefixItems); got != 2 {
		t.Errorf("dropped %d lists, want 2", got)
	}
	if _, _, ok := c.Get(KeyItem("i1"), now); !ok {
		t.Error("dropping every list also dropped the article body the reader is looking at")
	}
	if _, _, ok := c.Get(KeyFeeds, now); !ok {
		t.Error("dropping every list also dropped the rail")
	}

	// An empty prefix matches everything. A key builder that returned "" for an
	// uncacheable request would then silently empty the cache, so it is refused.
	if got := c.DropPrefix(""); got != 0 {
		t.Errorf("an empty prefix dropped %d entries; nobody means \"everything\" by passing nothing", got)
	}
	if c.Len() == 0 {
		t.Error("an empty prefix emptied the cache")
	}
}
