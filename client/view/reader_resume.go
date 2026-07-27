//go:build js && wasm

package view

import (
	"errors"
	"strconv"
	"time"

	"github.com/monstercameron/ArticleFlux/client/data"
	"github.com/monstercameron/ArticleFlux/client/i18n"
)

// The functions that turn stored preferences back into a reader's last position.
//
// Pure: a map of strings in, a value out. They live beside the type declarations
// rather than with the rest of the helpers because they are the boundary between
// what the server remembered and what the component starts from, and that is worth
// being able to find.

func searchTextFrom(p map[string]string) string {
	if p["read.kind"] == "search" {
		return p["read.value"]
	}
	return ""
}

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
func resumeScope(p map[string]string, tr i18n.Runtime) scope {
	var s scope
	switch p["read.kind"] {
	case "unread":
		s = scope{Title: tr.T("stream", "unread"), Unread: true}
	case "liked":
		s = scope{Title: tr.T("stream", "liked"), Rating: 1}
	case "later":
		s = scope{Title: tr.T("stream", "later"), Later: true}
	case "notes":
		s = scope{Title: tr.T("stream", "notes"), Notes: true}
	case "feed":
		if v := p["read.value"]; v != "" {
			s = scope{SourceID: v, Title: p["read.title"]}
		}
	case "tag":
		if v := p["read.value"]; v != "" {
			s = scope{TagID: v, Title: p["read.title"]}
		}
	case "folder":
		if v := p["read.value"]; v != "" {
			s = scope{FolderID: v, Title: p["read.title"]}
		}
	case "search":
		if v := p["read.value"]; v != "" {
			s = scope{Search: v, Title: p["read.title"]}
		}
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
