//go:build js && wasm

package data

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/monstercameron/ArticleFlux/client/platform"
)

// The credential, as one value, renewed before it expires.
//
// # What was here before, and why it was the gap
//
// One string in local storage: a thirty-day bearer token, written at login and
// read on every RPC. `LoginResponse.RefreshToken` was read once and dropped on
// the floor, nothing ever called `RefreshSession`, and the server gated
// issuance off BECAUSE of that — handing out a credential nothing consumes is a
// second thing to steal, not a control. So the rotation, the reuse detection
// and the short access lifetimes §7.3a specifies were all built, all tested,
// and all unreachable. `internal/app/headers.go` was honest about it: CSP was
// the only compensating control on a token good for a month.
//
// This is the client half that makes those reachable — the versioned bundle,
// atomic rotation and cross-tab coordination that `WithRefreshTokens`'s own
// comment named as the condition for turning issuance on.
//
// # Why ONE json value rather than four keys
//
// The access token, its expiry, the refresh token and the record id change
// TOGETHER, on every rotation, and they are only meaningful together. Four keys
// means four writes, and a tab that reloads between the second and the third
// reads a bundle that is half old and half new — which presents a spent refresh
// token, which the server correctly treats as a replay, which revokes the whole
// family and signs the reader out of every device. One `localStorage.setItem`
// of one JSON string cannot be observed half-done.
//
// # Why it is versioned
//
// `v2` is a different key from `v1`, so a browser holding the old bare-string
// token is not asked to parse it as JSON. The old value is adopted once, as an
// access token with no refresh half, and then removed — see loadSession. A
// migration is cheaper than a forced logout for every existing reader, and a
// version in the key is what makes the NEXT change cheap too.

// The storage seam.
//
// Three function values rather than direct `platform.Local*` calls, and the
// reason is testability rather than abstraction: the wasm test binary runs
// under Node, where `localStorage` does not exist unless the runner was started
// with `--localstorage-file`. Every `platform.LocalSet` there is a silent
// no-op, so a test written against the real thing asserts nothing — it passes
// vacuously today and would keep passing through a rewrite that broke the
// bundle entirely.
//
// They are bound to `platform` at declaration and production never rebinds
// them. A test swaps in a map, exercises the actual JSON, migration,
// versioning and locking logic, and puts them back.
var (
	storageGet    = platform.LocalGet
	storageSet    = func(k, v string) { platform.LocalSet(k, v) }
	storageRemove = platform.LocalRemove
)

const (
	// sessionKey holds the bundle. v2 because v1 was the bare token string.
	sessionKey = "articleflux.v2.session"
	// lockKey coordinates which tab is allowed to rotate. See withRefreshLock.
	lockKey = "articleflux.v2.refresh.lock"
)

// refreshLead is how far before expiry a renewal is attempted.
//
// The access token lives twelve hours (grpcsrv.AccessTTL); this starts trying
// with thirty minutes left. Wide enough that a laptop that wakes, renews and
// sleeps again has many chances, and far enough from zero that a renewal
// failing once — a flaky tunnel, a server mid-restart — is not the same event
// as being logged out.
const refreshLead = 30 * time.Minute

// lockTTL is how long a tab may hold the rotation lock.
//
// Five seconds. It bounds a single RPC, and the only thing it is protecting
// against is two tabs rotating at once — so the cost of it being too SHORT is a
// rare double rotation (which the server answers by revoking the family, the
// outcome this whole file exists to avoid) and the cost of it being too LONG is
// a tab waiting a few extra seconds after another one crashed mid-refresh. The
// asymmetry says: generous.
const lockTTL = 5 * time.Second

// session is what is stored, and the JSON tags are a wire format between two
// versions of this application — a browser can hold a bundle written by
// yesterday's build. Renaming a field is a breaking change; add and default
// instead, or bump the key.
type session struct {
	// V is the bundle's own version, in addition to the key's. Belt and braces:
	// the key stops a v1 value being parsed as JSON, and this stops a v2 value
	// written by a future build being read as though its fields meant what they
	// mean today.
	V int `json:"v"`
	// Access is the bearer token every RPC carries.
	Access string `json:"a"`
	// ExpiresUnix is when Access stops working, as reported by the server.
	// Zero means "unknown", which is what a migrated v1 token has — and it
	// disables proactive renewal rather than triggering it, because a renewal
	// against a bundle with no refresh half can only fail.
	ExpiresUnix int64 `json:"e"`
	// Refresh is the single-use renewal secret. Empty on an instance that does
	// not issue them, which is a supported state: the reader keeps the long
	// session it was given and never renews.
	Refresh string `json:"r"`
	// Record names which device row is refreshing. Server-generated and never
	// derived from anything the client chose (§7.3a SEC1).
	Record string `json:"d"`
}

const sessionVersion = 2

// The bundle in memory, read on every RPC by the interceptor.
//
// Package-level for the reason the old `token` was: the interceptor is
// installed when the connection is dialled, which is before anyone has logged
// in, and a credential that arrives later has to reach it.
var (
	sessionMu sync.RWMutex
	current   session
)

// currentSession returns a copy of what is held.
func currentSession() session {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	return current
}

// storeSession writes memory and local storage together, in that order.
//
// Memory first so an RPC issued between the two lines carries the NEW token
// rather than the old one — the reverse order has a window in which storage is
// correct and the thing every request actually reads is not.
func storeSession(s session) {
	sessionMu.Lock()
	current = s
	sessionMu.Unlock()

	if s.Access == "" {
		storageRemove(sessionKey)
		return
	}
	s.V = sessionVersion
	if raw, err := json.Marshal(s); err == nil {
		storageSet(sessionKey, string(raw))
	}
}

// readStoredSession reads the bundle from local storage, migrating v1 if that
// is all there is.
//
// It does NOT touch the in-memory copy. Two callers want different things from
// it: boot wants to adopt whatever is there, and a tab that lost the rotation
// lock wants to see whether the winner has published something newer WITHOUT
// overwriting its own state until it has decided to.
func readStoredSession() session {
	if raw := storageGet(sessionKey); raw != "" {
		var s session
		if err := json.Unmarshal([]byte(raw), &s); err == nil && s.Access != "" {
			// A bundle from a FUTURE build. Its fields may not mean what they
			// mean here, so it is used for the one thing that cannot change
			// meaning — the access token — and not for rotation. The alternative
			// is discarding it, which logs somebody out for having opened a
			// newer tab first.
			if s.V > sessionVersion {
				return session{V: sessionVersion, Access: s.Access}
			}
			return s
		}
		// Unparseable. Removed rather than left to fail identically on every
		// boot: whatever wrote it is not this code, and a credential that
		// cannot be read is not a credential.
		storageRemove(sessionKey)
	}

	// v1: a bare token string, no expiry, no refresh half. Adopted once so an
	// existing reader is not logged out by an upgrade, and the old key is
	// removed so this branch runs exactly once per browser.
	if old := storageGet(tokenKey); old != "" {
		storageRemove(tokenKey)
		return session{V: sessionVersion, Access: old}
	}
	return session{}
}

// dueForRefresh reports whether the bundle should be renewed now.
//
// False for a bundle with no refresh half — including a migrated v1 one —
// because there is nothing to renew WITH, and an attempt could only produce a
// failed RPC per check forever.
func (s session) dueForRefresh(now time.Time) bool {
	if s.Access == "" || s.Refresh == "" || s.Record == "" {
		return false
	}
	if s.ExpiresUnix == 0 {
		// A refresh half but no expiry. Not a shape this client writes; treat
		// it as due, because the alternative is a bundle that renews never.
		return true
	}
	return now.Add(refreshLead).After(time.Unix(s.ExpiresUnix, 0))
}

// tabID identifies this browser tab for the duration of the page.
//
// Not persisted, unlike `clientLabel`: this names a TAB and a reload is a new
// one. Deliberately not `idgen` — that is a server package and would ship its
// dependencies into the bundle for one string — and the value only has to be
// unique among the handful of tabs one person has open, not unguessable: the
// worst a forged value can do is make its forger lose the lock race.
var tabID = "tab-" + strconv.FormatInt(time.Now().UnixNano(), 36)

// withRefreshLock runs fn as the only tab rotating, or not at all.
//
// # Why a lock is not optional here
//
// A refresh token is SINGLE USE and the server treats a second presentation as
// theft — it cannot tell a racing tab from a thief, so it revokes the whole
// family and signs the reader out everywhere. Two tabs waking from sleep at the
// same moment is not an edge case; it is Monday morning. Without coordination
// the ordinary behaviour of this application would be to log people out.
//
// # How it works, and what it is not
//
// `localStorage` is shared across tabs of one origin and its writes are
// serialised, so the classic best-effort lease works: write your own claim,
// pause, read it back, and the tab whose value survived owns the lock. It is
// not a mutex and cannot be — there is no compare-and-swap in the storage API —
// but the window in which two tabs can both believe they won is the microseconds
// between two synchronous writes, and each of them re-reads the bundle before
// spending anything.
//
// The real backstop is that layer: a loser waits for the winner's bundle, and a
// winner that finds the bundle already renewed does nothing. The lock removes
// the collision; the re-read makes the residue harmless.
func withRefreshLock(fn func()) bool {
	now := time.Now()
	if held := storageGet(lockKey); held != "" {
		if id, until, ok := parseLock(held); ok && now.Before(until) && id != tabID {
			return false
		}
		// Expired, or ours. An expired lock is a tab that crashed mid-refresh,
		// and refusing to ever take it again would leave the session
		// permanently unrenewable — which is a worse failure than the double
		// rotation the TTL is short enough to make unlikely.
	}

	claim := tabID + "|" + strconv.FormatInt(now.Add(lockTTL).UnixMilli(), 10)
	storageSet(lockKey, claim)
	// The settle. Long enough that another tab's write has landed, short enough
	// that it is invisible — this runs on a background goroutine, not on the
	// render path.
	time.Sleep(25 * time.Millisecond)
	if storageGet(lockKey) != claim {
		return false
	}

	defer func() {
		// Only if it is still ours. Releasing a lock somebody else has taken
		// since is how two tabs end up rotating half a second apart.
		if storageGet(lockKey) == claim {
			storageRemove(lockKey)
		}
	}()
	fn()
	return true
}

func parseLock(raw string) (id string, until time.Time, ok bool) {
	i := strings.IndexByte(raw, '|')
	if i < 0 {
		return "", time.Time{}, false
	}
	ms, err := strconv.ParseInt(raw[i+1:], 10, 64)
	if err != nil {
		return "", time.Time{}, false
	}
	return raw[:i], time.UnixMilli(ms), true
}
