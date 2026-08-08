//go:build js && wasm

package data

import (
	"context"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Spending the refresh token, exactly once at a time, across every open tab.
//
// # The two ways a session is renewed
//
// PROACTIVELY, by the keeper below, thirty minutes before the access token
// expires. This is the path that should carry every renewal in practice: it
// runs on a background goroutine, on a tunnel that is already up, and the
// reader never sees it.
//
// REACTIVELY, from the interceptor, when an RPC comes back Unauthenticated. A
// laptop that was closed for a day wakes with an expired token and no warning,
// and the alternative to renewing here is dropping that reader on the login
// screen for a session they could have kept.
//
// Both go through `renew`, which is where the coordination lives — two entry
// points spending a single-use token independently is the same disaster as two
// tabs doing it.
//
// # Why a failed renewal is a logout and not a retry
//
// `RefreshSession` answers one error for reuse, an unknown record and a revoked
// family alike, deliberately, so a caller cannot use it as an oracle. That
// means the client cannot tell "the network hiccuped" from "the family was
// revoked because somebody stole this token" — and only one of those is safe to
// retry. It is treated as the second. The cost is a login screen after a bad
// minute of connectivity; the cost of guessing the other way is a client that
// hammers a revoked credential.
//
// A transport failure is distinguished, though: `Unavailable` means the call
// never reached the server, so nothing was spent and there is nothing to
// invalidate.

// renewMu serialises renewal WITHIN this tab.
//
// The localStorage lease handles other tabs; this handles the keeper's timer
// firing at the same moment an RPC comes back Unauthenticated, which is not
// hypothetical — that is exactly what a laptop waking up does.
var renewMu sync.Mutex

// renew exchanges the refresh token for a new session, if one is needed.
//
// Returns true when the bundle in memory now holds a credential that was not
// there when the caller started — whether this tab minted it or another one
// did. That is the signal the interceptor retries on.
func (c *Client) renew(parent context.Context, was string) bool {
	renewMu.Lock()
	defer renewMu.Unlock()

	// Somebody already did it while we were waiting for the mutex — the keeper,
	// or a sibling RPC that failed a moment earlier. Checked FIRST, before the
	// cross-tab lock and before any RPC, because this is the common case in a
	// reader that makes several calls a minute.
	if cur := currentSession(); cur.Access != "" && cur.Access != was {
		return true
	}

	got := false
	took := withRefreshLock(func() {
		// And again, from STORAGE this time, now that we hold the lock. Another
		// tab may have rotated between our first check and our acquisition, and
		// its new bundle is in local storage rather than in this tab's memory.
		// Spending our copy of the refresh token after that would present a
		// token the server has already rotated — a replay, from the client's own
		// racing halves, and the server would correctly revoke the family.
		stored := readStoredSession()
		if stored.Access != "" && stored.Access != was {
			sessionMu.Lock()
			current = stored
			sessionMu.Unlock()
			got = true
			return
		}
		if stored.Refresh == "" || stored.Record == "" {
			// Nothing to spend. An instance that issues no refresh tokens, or a
			// bundle migrated from v1. Not a failure and not a logout: that
			// session is valid until it expires.
			return
		}
		got = c.spendRefresh(parent, stored)
	})

	if !took {
		// Another tab holds the lock. Wait for ITS answer rather than racing it:
		// the winner publishes a new bundle to local storage, and adopting that
		// is free where minting a second one costs the whole family.
		return waitForRotation(was)
	}
	return got
}

// spendRefresh performs the exchange. Called only under both locks.
func (c *Client) spendRefresh(parent context.Context, s session) bool {
	// Its own timeout, not the caller's. A renewal triggered by an RPC that was
	// about to give up must not inherit two hundred milliseconds of that
	// request's remaining budget — and one triggered by the keeper has no
	// deadline at all.
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()

	res, err := c.auth.RefreshSession(ctx, &pb.RefreshSessionRequest{
		RefreshRecordId: s.Record,
		RefreshToken:    s.Refresh,
	}, grpc.WaitForReady(false))
	if err != nil {
		if status.Code(err) == codes.Unavailable {
			// The call never landed, so the token was not spent. Leave the
			// bundle exactly as it is and let the next attempt try again — this
			// is the one failure here that is safe to survive.
			return false
		}
		// Anything else means the credential is no good, and the client cannot
		// tell which flavour of no good. See the file comment.
		storeSession(session{})
		if onUnauthenticated != nil {
			onUnauthenticated()
		}
		return false
	}

	// One write, all four fields. The record id does NOT come back in the
	// response — the family is the same one — so it is carried over from the
	// bundle that was just spent.
	storeSession(sessionFrom(res.GetToken(), res.GetExpiresAt(),
		res.GetRefreshToken(), s.Record))
	return true
}

// waitForRotation watches local storage for the winning tab's new bundle.
//
// Polling, because there is no storage-event plumbing in `client/platform` and
// adding one for this would be a browser API surface to maintain for a case
// that resolves in under a second. Ten short looks rather than one long sleep,
// so the common case — the winner finishing in 200ms — costs 200ms.
//
// Giving up returns false rather than logging out. The winner may have failed
// for its own reasons and will have handled that; a loser that also concluded
// "log out" would double-report it.
func waitForRotation(was string) bool {
	for range 20 {
		time.Sleep(100 * time.Millisecond)
		stored := readStoredSession()
		if stored.Access == "" {
			// The winner cleared the bundle: its renewal failed and it has
			// already sent the page to the login screen. Adopt the clearing so
			// this tab stops presenting a credential it knows is dead.
			sessionMu.Lock()
			current = session{}
			sessionMu.Unlock()
			return false
		}
		if stored.Access != was {
			sessionMu.Lock()
			current = stored
			sessionMu.Unlock()
			return true
		}
	}
	return false
}

// keeperOnce makes StartSessionKeeper idempotent.
//
// It is called from the view layer when a connection is handed over, and that
// happens on login AND on a boot with a stored token — two paths that can both
// run in one page load. Two keepers would double every renewal attempt, which
// is the one thing this whole file exists to prevent.
var keeperOnce sync.Once

// StartSessionKeeper renews the session in the background for as long as ctx
// lives.
//
// # Why a poll rather than a timer set to the expiry
//
// A timer computed at login is wrong by the time it fires. The tab may have
// been suspended — a backgrounded tab's timers are throttled to minutes and a
// sleeping laptop's do not fire at all — so the wake-up arrives late, after the
// token it was scheduled against has already expired. A poll asks the only
// question that stays true across a suspend: is the credential in front of me
// close to expiring RIGHT NOW.
//
// Every thirty seconds, which against a twelve-hour lifetime and a thirty-minute
// lead is generous and costs a comparison. The RPC only happens inside the
// lead, and only in whichever tab wins the lease.
func (c *Client) StartSessionKeeper(ctx context.Context) {
	keeperOnce.Do(func() {
		go func() {
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					s := currentSession()
					if !s.dueForRefresh(time.Now()) {
						continue
					}
					c.renew(ctx, s.Access)
				}
			}
		}()
	})
}
