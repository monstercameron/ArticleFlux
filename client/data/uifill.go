package data

import (
	"context"
	"time"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The pure half of on-the-fly UI translation (plan.md §22.16c).
//
// Untagged, like coalesce.go, for the same reason: the RPC needs the browser
// transport, but what the loop DECIDES — which keys to ask for, when to stop,
// what reaches the catalog — is testable natively, and it is the part most
// likely to be wrong.
//
// The shape: ask the server for exactly the keys this bundle has no
// translation for, merge what comes back (i18n.Import checks every entry
// against THIS bundle's English, so a server on a different build cannot put a
// stale string on screen), and, while the server says a fill is running, ask
// again for whatever is still missing. The reader sees English for those keys
// the whole time and each batch replaces its keys as it lands.

// uiStringsFetch is the one RPC the loop makes. A seam so the loop can be
// tested without a transport.
type uiStringsFetch func(ctx context.Context, req *pb.GetUIStringsRequest) (*pb.GetUIStringsResponse, error)

// FillResult is what a fill loop achieved.
type FillResult struct {
	// Applied counts entries merged into the catalog across every round.
	Applied int
	// Pending is how many keys the server could still not answer when the loop
	// stopped. Those keep rendering in English.
	Pending int
	// IdleReason is the server's class for a fill it would not run
	// ("not_owner", "no_key"), or "".
	IdleReason string
}

// Poll pacing. The first wait is short because a batch of sixty strings takes
// a reasoning model tens of seconds and the first one may already be done; the
// cap keeps a long fill from becoming a request every second for a minute.
const (
	fillFirstWait = 4 * time.Second
	fillMaxWait   = 15 * time.Second
	// fillMaxRounds bounds a loop whose server keeps saying "filling" without
	// progress — a wedged fill must not become a poll that never ends.
	fillMaxRounds = 60
)

// mergeUIStrings imports one response and reports how many entries landed.
func mergeUIStrings(res *pb.GetUIStringsResponse) int {
	if res == nil || len(res.GetMessages()) == 0 {
		return 0
	}
	entries := make([]i18n.Entry, 0, len(res.GetMessages()))
	for _, m := range res.GetMessages() {
		entries = append(entries, i18n.Entry{
			Key: m.GetKey(), Text: m.GetText(), Plural: m.GetForms(), SourceHash: m.GetSourceHash(),
		})
	}
	return i18n.Import(res.GetLocale(), entries)
}

// runUIFill is the loop. onMerged runs after every round that changed the
// catalog, with the running total — the view's cue to nudge Root so memoised
// panes re-render with the new strings (i18n.Revision).
//
// sleep is injected so a test does not wait; nil means a real, cancellable
// wait.
func runUIFill(ctx context.Context, fetch uiStringsFetch, locale string, fill bool,
	onMerged func(applied int), sleep func(context.Context, time.Duration) bool) (FillResult, error) {

	var out FillResult
	if locale == "" || locale == i18n.DefaultLocale {
		return out, nil
	}
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) bool {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(d):
				return true
			}
		}
	}
	wait := fillFirstWait
	for round := 0; round < fillMaxRounds; round++ {
		missing := i18n.Missing(locale)
		if len(missing) == 0 {
			out.Pending = 0
			return out, nil
		}
		res, err := fetch(ctx, &pb.GetUIStringsRequest{Locale: locale, Keys: missing, Fill: fill})
		if err != nil {
			return out, err
		}
		if n := mergeUIStrings(res); n > 0 {
			out.Applied += n
			if onMerged != nil {
				onMerged(out.Applied)
			}
		}
		out.Pending = int(res.GetPending())
		out.IdleReason = res.GetIdleReason()
		if out.Pending == 0 || !res.GetFilling() {
			return out, nil
		}
		// Only the first round asks for a fill; later rounds are reads, so a
		// fill that finished and left keys rejected is not started again.
		fill = false
		if !sleep(ctx, wait) {
			return out, ctx.Err()
		}
		if wait *= 2; wait > fillMaxWait {
			wait = fillMaxWait
		}
	}
	return out, nil
}
