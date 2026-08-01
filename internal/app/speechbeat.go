package app

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/fluxcast"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Beat-addressed speech: /speech for a programme planned by internal/fluxcast.
//
// # Why this is a second path rather than a rewrite of the first
//
// The client is cached by a Service Worker, so old clients exist in the wild by
// construction. One of them will ask for `item` and `prev` and an intro flag for
// as long as its bundle survives, and the honest answer to that request is the
// one it has always had. So the legacy shape stays exactly as it was, and a
// request that carries a WORD BUDGET — the one thing the old client never sent,
// because the old show had no fitter — takes this path instead.
//
// # What the client sends and what it does not
//
// It sends what the beat IS: its kind, its budget, its predecessor, the stories
// it names, and the writer revision it was planned against. It does NOT send the
// cache key, and that omission is the security property rather than an economy.
// The audio cache is shared between readers, because the same article read in
// the same voice is the same recording; a server that filed audio under a
// caller-supplied key would let one reader write into another's slot. So the key
// is recomputed here from resolved inputs (fluxcast.ScriptKey) and the two ends
// agree by deriving the same answer rather than by trusting one another.
//
// Every id in the request is resolved through the reader's OWN SCOPE, which is
// what makes caller-supplied ids safe at all — the same argument prevItemParam
// has always made. The worst a forged one achieves is a handover from an article
// the caller could already read.

const (
	// beatWordsParam is the fitted word budget, and its PRESENCE is what
	// selects this path. A budget is the one thing the old client could not
	// have sent: the show it plays has no fitter, so every beat is whatever
	// length the instructions ask for.
	beatWordsParam = "w"
	// beatHandoverParam is the bridge construction the programme planned for
	// this beat, 1-based so that absent and zero mean the same thing — "the
	// writer chooses", which is the shipped behaviour.
	beatHandoverParam = "hv"
	// beatRevParam is the writer revision the programme was planned against.
	// A mismatch is refused rather than served: the beat's cache key claims a
	// revision, and answering with prose written under a different one files
	// text under a key that does not describe it.
	beatRevParam = "rev"

	// Two further values on introParam, which already answers the question
	// "where in the programme does this request sit" — see its own comment for
	// why that is one parameter rather than five.
	introTease = "3"
	introRecap = "4"
)

// castBeat reads the beat out of a request, or reports that this is not one.
//
// Returns ok=false for a request with no word budget, which is every request
// from a client that has not been rebuilt. That is not an error and must not be
// logged as one.
func castBeat(r *http.Request) (kind fluxcast.BeatKind, words, handover int, rev string, ok bool) {
	q := r.URL.Query()
	raw := strings.TrimSpace(q.Get(beatWordsParam))
	if raw == "" {
		return "", 0, 0, "", false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 || n > 5000 {
		// A budget outside anything a beat could be is a client bug, not a
		// request to write five thousand words. Falling back to the legacy
		// path would silently produce an unfitted programme, so this is
		// refused by the caller instead.
		return "", 0, 0, "", false
	}
	switch strings.TrimSpace(q.Get(introParam)) {
	case introOnly:
		kind = fluxcast.BeatOpening
	case introClose:
		kind = fluxcast.BeatSignOff
	case introTease:
		kind = fluxcast.BeatTease
	case introRecap:
		kind = fluxcast.BeatRecap
	default:
		kind = fluxcast.BeatStory
	}
	handover = -1
	if v, err := strconv.Atoi(strings.TrimSpace(q.Get(beatHandoverParam))); err == nil && v > 0 {
		handover = v - 1
	}
	return kind, n, handover, strings.TrimSpace(q.Get(beatRevParam)), true
}

// briefFor assembles the commission from a request that has already been
// authorised: the scope is resolved, the item is one this reader may read, and
// the preferences have been checked.
func (a *App) briefFor(ctx context.Context, sc store.Scope, r *http.Request,
	it store.Item, kind fluxcast.BeatKind, words, handover int, rev string) fluxcast.Brief {

	prefs, _ := a.repo.GetPrefs(ctx, sc)
	b := fluxcast.Brief{
		Kind:     kind,
		Words:    words,
		Vibe:     smart.VibeFor(prefs[podcastVibePrefKey]),
		Revision: rev,
		Handover: handover,
		Subject: fluxcast.Subject{
			ItemID: it.ID,
			Title:  it.Title,
			Source: it.SourceTitle,
			Body:   speechBody(it),
		},
	}
	// The greeting's facts. Read from the request rather than from this
	// server's clock: the listener may be three timezones away, and being
	// wished good morning at ten at night is the most obviously wrong thing
	// this feature can say.
	if o := a.openingFrom(ctx, sc, r, time.Now()); o != nil {
		b.PartOfDay, b.Date, b.Stories = o.PartOfDay, o.Date, o.Stories
	}
	// The predecessor, resolved through the same scope as everything else.
	if pid := strings.TrimSpace(r.URL.Query().Get(prevItemParam)); pid != "" && len(pid) <= 64 && pid != it.ID {
		if p, err := a.repo.GetItem(ctx, sc, pid); err == nil {
			b.Prev = fluxcast.Subject{ItemID: p.ID, Title: p.Title, Source: p.SourceTitle}
		}
	}
	// What this beat NAMES: the opening's run-through, a tease, a recap. Ids
	// rather than titles on the wire, because a query string lands in the
	// access log, in the browser's history and in any referrer, and a reader's
	// headlines are their reading (§22.11).
	b.Lineup = a.castLineup(ctx, sc, r.URL.Query().Get(openLineupParam))
	// A story beat carries a body; nothing else does. A greeting given the
	// article it introduces summarises it, which is the one thing the opening
	// must not do.
	if kind != fluxcast.BeatStory {
		b.Subject.Body = ""
	}
	return b
}

// castLineup resolves named stories, keeping their ids — the key depends on
// WHICH stories were named, so an id that is dropped for having no headline has
// to be dropped at both ends or the two keys disagree and nothing ever hits the
// cache.
func (a *App) castLineup(ctx context.Context, sc store.Scope, raw string) []fluxcast.Headline {
	raw = strings.TrimSpace(raw)
	if raw == "" || a.repo == nil {
		return nil
	}
	out := make([]fluxcast.Headline, 0, smart.MaxLineup)
	for _, id := range strings.Split(raw, ",") {
		if len(out) >= smart.MaxLineup {
			break
		}
		id = strings.TrimSpace(id)
		if id == "" || len(id) > 64 {
			continue
		}
		it, err := a.repo.GetItem(ctx, sc, id)
		if err != nil || strings.TrimSpace(it.Title) == "" {
			continue
		}
		out = append(out, fluxcast.Headline{ItemID: it.ID, Title: it.Title, Source: it.SourceTitle})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// writeBeat produces the script for one beat and the key its audio is cached
// under.
//
// The key is computed from the brief AFTER the server has resolved it, so a
// lineup entry the server dropped (no headline, swept away, not visible to this
// reader) is dropped from the key too. The client computes the same key from
// the same rules over what it believes; where the two disagree the request
// simply misses the cache and is written again, which is the correct failure —
// the alternative is serving a recording of a different script.
// # The fallback is the free-tier promise, and it applies to exactly one kind
//
// A STORY beat whose script could not be written falls back to READING THE
// ARTICLE, which is what the path this replaces has always done: worse than what
// was asked for, and far better than silence. It is also the whole reason
// FluxCast can claim to work without a key — a broadcast that went quiet the
// moment a model was unavailable would be a paid feature wearing a free one's
// name.
//
// Nothing else falls back, because nothing else has anything to fall back TO. A
// greeting, a tease and a recap cover no story; reading one out in their place
// would play an article the programme is about to play properly. A sign-off's
// fallback would be the last story a second time, which is not a degraded
// goodbye — it is a bug with a voice. Those return an error, the player skips
// the beat, and the programme carries on.
func (a *App) writeBeat(ctx context.Context, b fluxcast.Brief, it store.Item) (text, key string, err error) {
	if a.write == nil {
		if b.Kind == fluxcast.BeatStory {
			return speechText(it), it.ID, nil
		}
		return "", "", errNoWriter
	}
	draft, werr := a.write.Write(ctx, b)
	switch {
	case werr == nil && strings.TrimSpace(draft.Text) != "":
		return draft.Text, "beat:" + fluxcast.ScriptKey(b), nil
	case errors.Is(werr, smart.ErrStaleRevision):
		// Never fall back on this one. The client is running a plan this build
		// cannot honour, and reading the article would hide that behind
		// something that sounds nearly right — the programme would play, out of
		// time, with a narrator that never appears.
		return "", "", werr
	case b.Kind != fluxcast.BeatStory:
		if werr == nil {
			werr = smart.ErrNothingToSummarise
		}
		return "", "", werr
	}
	if werr != nil && !errors.Is(werr, smart.ErrNothingToSummarise) {
		a.cfg.Log.Warn("broadcast beat failed, reading the article",
			"item", it.ID, "kind", string(b.Kind), "err", werr)
	}
	return speechText(it), it.ID, nil
}

// errNoWriter is an instance with no broadcast writer at all. Distinct from a
// missing key: one is a build without the feature and one is a deployment that
// has not been configured, and only the second is something an operator can fix.
var errNoWriter = errors.New("app: no broadcast writer on this instance")

// beatError turns a writing failure into a status, and the statuses are chosen
// so the CLIENT can tell them apart without parsing prose.
//
// Three outcomes and three different things for a player to do:
//
//	409  the programme was planned against an older narrator — re-plan and
//	     start again. This is the only one that is the client's own fault and
//	     the only one it can fix on its own.
//	422  nothing to say for this beat. The player skips it; the programme
//	     carries on, because a segment that cannot be written is not a
//	     broadcast that ended.
//	502  the provider refused or failed. Same treatment as 422 from the
//	     player's side, but it is somebody else's outage and the log should
//	     say so.
//
// The provider's own message never reaches the response: it can echo request
// content, and request content here is the reader's article (§22.11).
func (a *App) beatError(w http.ResponseWriter, r *http.Request, id string, err error) {
	switch {
	case errors.Is(err, smart.ErrStaleRevision):
		http.Error(w, "this programme was planned against an older narrator; start it again",
			http.StatusConflict)
	case errors.Is(err, llm.ErrNotConfigured), errors.Is(err, errNoWriter):
		// 501 rather than 502: the server is working correctly and simply has
		// no key, which is exactly what "not implemented on this instance"
		// means. A story beat never reaches here — it reads the article
		// instead — so this only ever answers a greeting, a tease, a recap or
		// a goodbye, all of which the player skips.
		http.Error(w, "this instance cannot write a broadcast", http.StatusNotImplemented)
	case errors.Is(err, smart.ErrNothingToSummarise):
		http.Error(w, "nothing to read aloud", http.StatusUnprocessableEntity)
	default:
		a.cfg.Log.Warn("broadcast beat failed", "item", id, "err", err)
		http.Error(w, "couldn't write that", http.StatusBadGateway)
	}
}

// podcastCachedBeat reads a beat's script from disk, or reports that it is not
// there yet. Through the writer seam so a test can substitute one.
func (a *App) podcastCachedBeat(ctx context.Context, b fluxcast.Brief) (string, bool) {
	if a.podcast == nil {
		return "", false
	}
	return a.podcast.CachedBeat(ctx, b)
}

// serveScript writes a script as plain text.
//
// Private and short-lived in the cache, like the audio it belongs to: this is
// one reader's article rewritten, and a shared cache holding it would serve it
// to whoever asked next. No Content-Disposition and no filename — it is read by
// a fetch, never downloaded.
func serveScript(w http.ResponseWriter, r *http.Request, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(text)))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(text))
}
