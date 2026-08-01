//go:build js && wasm

package view

import (
	"errors"
	"strconv"
	"time"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The functions that turn stored preferences back into a reader's last position.
//
// Pure: a map of strings in, a value out. They live beside the type declarations
// rather than with the rest of the helpers because they are the boundary between
// what the server remembered and what the component starts from, and that is worth
// being able to find.

// searchTextFrom is gone: the search box is now seeded from the scope that
// actually won at boot (`boot.sel.Search`, see reader.go), which is the same
// value for a resumed search and the RIGHT one for an addressed one. Reading it
// back out of the preference map would have put yesterday's query in the box
// under today's /search?q= results.

// resumeScope turns the saved place into the scope the list is fetched for.
//
// Shared by the mount-time seed and the effect that runs after it, because they
// have to agree exactly: one deciding the first frame and the other deciding
// what is fetched is precisely how a reader ends up looking at a header for one
// feed above the items of another.
//
// An unrecognised or absent kind falls through to All, and a feed whose title
// was never saved takes All's title too — an empty header is worse than a
// slightly wrong one.
// Delegated to scopeOf (client/view/route.go) rather than switching here.
//
// It used to switch on `read.kind` itself, and the cost of the second copy was
// exactly what a second copy always costs: the two drifted. This one grew cases
// for unread, liked, later, notes, feed, tag, folder and search, and never grew
// one for **My Feed** — so the ranked stream, which the rail offers first and
// which §18.4 calls the point of the interest layer, was written by rememberScope
// as kind "all" and read back as All. A reader who lived on My Feed was returned
// to All every morning, silently, because All is plausible enough that it reads
// as the resume merely being imprecise. `disliked` was missing for the same
// reason and had a failing test saying so (TestResumeScopeNeverRestoresA-
// DislikedScope).
//
// One classifier (scopeKind) and one builder (scopeOf), shared with the address
// bar, is what stops a third encoding from being added to only two of them.
func resumeScope(p map[string]string, tr i18n.Runtime) scope {
	s, ok := scopeOf(p["read.kind"], p["read.value"], p["read.title"], tr)
	if !ok {
		// An unrecognised kind, or one whose argument is missing. Both fall through
		// to All below rather than to an empty scope with an empty id.
		s = scope{}
	}
	if s.Title == "" {
		s.Title = tr.T("stream", "all")
	}
	return s
}

// toggleUnreadResult decides what pressing "u" (or clicking the list-head
// toggle-unread chip) does, given the CURRENT scope and the CURRENT value of
// the persistent unread-only flag. Pure, so the decision can be pinned
// without mounting the whole Reader closure — see toggleUnreadResult_test.go.
//
// Two different fields both mean "the reader sees only unread": the
// persistent flag (unreadOnly) and the rail's dedicated Unread stream row
// (sel.Unread). loadItems already ORs them together
// (`unreadOnly.Get() || s.Unread`, reader.go), which is what makes sel.Unread
// force unread-only regardless of unreadOnly's value.
//
// That matters here because a naive toggle — just flip unreadOnly — would be
// a no-op on screen whenever sel.Unread is true: the OR stays true either
// way. But the chip and emptyUnreadHint both promise this action turns
// unread-only OFF. There is no "on the Unread stream but not unread-only"
// state to land the reader on, so the only way to keep that promise is to
// leave the stream entirely — back to All, the same destination any other
// scope switch lands on. leaving reports this so the caller knows to route
// through a scope change (selectScopeWithUnread) rather than a same-scope
// reload (loadItems).
//
// Outside that case, this is the toggle it always was: flip unreadOnly over
// whatever scope is already selected.
func toggleUnreadResult(tr i18n.Runtime, sel scope, unreadOnly bool) (dest scope, nextUnreadOnly bool, leaving bool) {
	if sel.Unread {
		return scope{Title: tr.T("stream", "all")}, false, true
	}
	return sel, !unreadOnly, false
}

// effectiveResumeScope is resumeScope, except a reader who has chosen a
// fixed landing view (Settings → Reading) gets THAT instead of A30's
// "wherever I left off" — on every boot, not just the first one after they
// set it.
//
// landing.mode/kind/value/title are four flat keys shaped exactly like
// read.kind/value/title (see rememberScope's doc comment for why four keys
// rather than one encoded string), scoped separately so a fixed landing
// choice and the ordinary place-you-left-off tracking never collide — the
// reader can pick "always open My Feed" and the app still remembers that
// they were, in fact, reading in Alpha Journal a moment ago.
//
// Delegated to scopeOf, the same codec resumeScope and the address bar
// share, so a landing choice this build cannot resolve (a deleted feed, a
// renamed build's dropped kind) falls through to the ordinary resume rather
// than to a blank scope.
func effectiveResumeScope(p map[string]string, tr i18n.Runtime) scope {
	if p["landing.mode"] == landingModeFixed {
		if sc, ok := scopeOf(p["landing.kind"], p["landing.value"], p["landing.title"], tr); ok {
			return sc
		}
	}
	return resumeScope(p, tr)
}

// effectiveResumeItem pairs with effectiveResumeScope: a fixed landing view
// opens fresh, not on whatever article the reader happened to be on last —
// the whole point of choosing "always open My Feed" is to land on the LIST,
// not to be dropped back into yesterday's piece under a different banner.
func effectiveResumeItem(p map[string]string) string {
	if p["landing.mode"] == landingModeFixed {
		return ""
	}
	return p["read.item"]
}

// landingTitleFor resolves the display name a fixed landing choice is stored
// with, the same way pickFolder/pickTag/pickCategory resolve one at the
// moment of a click — scopeOf itself cannot do this for feed/tag/folder
// kinds because, unlike a stream, their name isn't fixed copy: it is
// whatever this reader called the feed, the tag or the folder, looked up in
// the live list the settings picker was built from.
func landingTitleFor(kind, value string, tr i18n.Runtime, feeds []*pb.Feed, tags []*pb.Tag, folders []*pb.Folder) string {
	switch kind {
	case kindFeed:
		for _, f := range feeds {
			if f.GetSourceId() == value {
				return f.GetTitle()
			}
		}
	case kindTag:
		for _, t := range tags {
			if t.GetId() == value {
				return tagDisplay(t)
			}
		}
	case kindFolder:
		for _, fo := range folders {
			if fo.GetId() == value {
				return fo.GetName()
			}
		}
	default:
		if sc, ok := scopeOf(kind, value, "", tr); ok {
			return sc.Title
		}
	}
	return ""
}

func prefBool(p map[string]string, key string, def bool) bool {
	if v, ok := p[key]; ok {
		return v == "true"
	}
	return def
}

// keptOptimistic reports an error that must NOT undo what is on screen.
//
// data.ErrQueued is not a failure: the write is kept and replayed on the next
// recovery, so the optimistic value the reader is looking at IS the truth.
// Rolling it back would be the application discarding a write it has in fact
// retained — worse than the bug it replaces, because it looks deliberate.
func keptOptimistic(err error) bool { return errors.Is(err, data.ErrQueued) }

// intentKey mints an idempotency key unique to one PRESS rather than to one
// item.
//
// The keys here used to be "unread-<id>" — stable per item, which reads as
// tidy and is a replay hazard the day the server starts honouring §20.7's
// idempotency store: mark unread, mark read, mark unread again, and the third
// call is answered from the first one's cached response and silently applies
// nothing. Harmless while `idempotency_keys` is an unused table, and reachable
// the moment it is not — which is exactly the kind of latent bug an outbox
// turns into a real one, because an outbox is what replays.
func intentKey(prefix, id string) string {
	return prefix + "-" + id + "-" + strconv.FormatInt(time.Now().UnixMilli(), 36)
}

// shortDuration says how long something took in one glance.
//
// Rounded hard and deliberately: this reports cumulative downtime, a number
// nobody needs to the second and everybody reads in relative terms — "about a
// minute" is the whole message, and "1m13.482s" makes a reader parse before
// they can conclude.
// Through the `unit` catalogue rather than concatenating a letter: "5m" is
// English, and a suffix glued onto a number is exactly the shape that breaks in
// a language where the unit is a word that agrees with it.
//
// Each key is written out in full rather than assembled from a prefix, so the
// catalogue's coverage test can find it. A key built at runtime is a key nobody
// can grep for, which is exactly the state that test exists to prevent — and
// "add its prefix to the dynamic allowlist" buys a shorter function by weakening
// the check for the whole namespace.
func shortDuration(tr i18n.Runtime, d time.Duration) string {
	n := func(v float64) i18n.Args { return i18n.Args{"n": strconv.Itoa(int(v))} }
	// One Namespace handle: every branch reads from `unit`, and NS is what stops
	// a positional namespace being typo'd at one of four call sites.
	unit := tr.NS("unit")
	switch {
	case d < time.Second:
		return unit.T("ms", n(float64(d.Milliseconds())))
	case d < time.Minute:
		return unit.T("seconds", n(d.Seconds()))
	case d < time.Hour:
		return unit.T("minutes", n(d.Minutes()))
	default:
		return unit.T("hours", n(d.Hours()))
	}
}
