//go:build js && wasm

package data

import (
	"testing"
	"time"
)

// The credential bundle, which is the client half of §7.3a SEC4.

// clearSession swaps the storage seam for a map and empties the in-memory
// bundle for the duration of one test.
//
// A map rather than the real `localStorage`, and that is not a shortcut: this
// test binary runs under Node, where `localStorage` does not exist unless the
// runner was started with `--localstorage-file`. Every write to it is a silent
// no-op there, so a test written against the real thing would pass while
// asserting nothing — and would keep passing through a rewrite that broke the
// bundle completely. See the seam's own note in session.go.
func clearSession(t *testing.T) map[string]string {
	t.Helper()
	store := map[string]string{}
	oldGet, oldSet, oldRemove := storageGet, storageSet, storageRemove
	restore := currentSession()

	storageGet = func(k string) string { return store[k] }
	storageSet = func(k, v string) { store[k] = v }
	storageRemove = func(k string) { delete(store, k) }
	sessionMu.Lock()
	current = session{}
	sessionMu.Unlock()

	t.Cleanup(func() {
		storageGet, storageSet, storageRemove = oldGet, oldSet, oldRemove
		sessionMu.Lock()
		current = restore
		sessionMu.Unlock()
	})
	return store
}

// One value, written once. Four keys would mean four writes, and a tab that
// reloads between the second and the third reads a bundle that is half old and
// half new — which presents a spent refresh token, which the server treats as a
// replay, which revokes the family and signs the reader out everywhere.
func TestTheBundleRoundTripsAsOneValue(t *testing.T) {
	store := clearSession(t)

	want := session{
		Access: "access-1", ExpiresUnix: time.Now().Add(time.Hour).Unix(),
		Refresh: "refresh-1", Record: "rec-1",
	}
	storeSession(want)

	got := readStoredSession()
	if got.Access != want.Access || got.Refresh != want.Refresh || got.Record != want.Record {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
	if got.ExpiresUnix != want.ExpiresUnix {
		t.Errorf("expiry = %d, want %d", got.ExpiresUnix, want.ExpiresUnix)
	}
	if got.V != sessionVersion {
		t.Errorf("version = %d, want %d", got.V, sessionVersion)
	}
	// ONE key. The whole argument for the bundle is that a rotation cannot be
	// observed half-applied, and four keys would make that untrue while every
	// assertion above still passed.
	if len(store) != 1 {
		t.Errorf("the bundle occupies %d storage keys, want 1: %v", len(store), store)
	}
}

// A reader upgrading from the bare-token build keeps their session, and the old
// key is consumed so this happens exactly once per browser.
func TestAV1TokenIsMigratedOnceAndThenGone(t *testing.T) {
	store := clearSession(t)
	store[tokenKey] = "old-bare-token"

	got := readStoredSession()
	if got.Access != "old-bare-token" {
		t.Fatalf("Access = %q; an upgrade logged an existing reader out", got.Access)
	}
	if got.Refresh != "" || got.Record != "" {
		t.Error("a migrated v1 token was given a refresh half it never had")
	}
	if store[tokenKey] != "" {
		t.Error("the v1 key survived the migration; it would be re-adopted on every boot")
	}
	// And it must not renew. There is nothing to renew WITH, so a bundle in this
	// shape asking to be refreshed would be one failed RPC per check, forever.
	if got.dueForRefresh(time.Now()) {
		t.Error("a migrated bundle with no refresh half reported itself due for renewal")
	}
}

// Unparseable storage is removed rather than left to fail identically on every
// boot. Whatever wrote it is not this code, and a credential that cannot be
// read is not a credential.
func TestAnUnreadableBundleIsDiscarded(t *testing.T) {
	store := clearSession(t)
	store[sessionKey] = "{not json"

	if got := readStoredSession(); got.Access != "" {
		t.Errorf("readStoredSession = %+v, want empty", got)
	}
	if store[sessionKey] != "" {
		t.Error("the unparseable value was left in place to fail again next boot")
	}
}

// A bundle written by a NEWER build is used for the one field that cannot
// change meaning, and not for rotation. Discarding it instead would log
// somebody out for having opened a newer tab first.
func TestABundleFromAFutureBuildIsUsedButNotSpent(t *testing.T) {
	store := clearSession(t)
	store[sessionKey] = `{"v":99,"a":"future-access","e":1,"r":"future-refresh","d":"future-rec"}`

	got := readStoredSession()
	if got.Access != "future-access" {
		t.Errorf("Access = %q; a newer tab's session was thrown away", got.Access)
	}
	if got.Refresh != "" || got.Record != "" {
		t.Error("a future bundle's refresh half was adopted; its fields may not mean what they mean here")
	}
}

// Renewal starts before expiry, not at it. A client that waited for the token
// to die would show a login screen every time a laptop woke up.
func TestRenewalIsDueBeforeExpiryAndNotLongBefore(t *testing.T) {
	now := time.Now()
	full := func(d time.Duration) session {
		return session{Access: "a", Refresh: "r", Record: "d", ExpiresUnix: now.Add(d).Unix()}
	}
	if full(12 * time.Hour).dueForRefresh(now) {
		t.Error("a freshly minted twelve-hour token reported itself due immediately")
	}
	if !full(refreshLead / 2).dueForRefresh(now) {
		t.Error("a token inside the renewal lead did not report itself due")
	}
	if !full(-time.Minute).dueForRefresh(now) {
		t.Error("an already-expired token did not report itself due")
	}
	// No refresh half, nothing to do — checked here as well as in the migration
	// test because this is the branch a real instance with issuance off takes on
	// every single check.
	if (session{Access: "a", ExpiresUnix: now.Add(-time.Hour).Unix()}).dueForRefresh(now) {
		t.Error("a bundle with no refresh half reported itself due")
	}
}

// Only one tab may rotate. A refresh token is single use and the server cannot
// tell a racing sibling from a thief — it revokes the family either way — so
// two tabs waking at the same moment must not both spend.
func TestTheRotationLockAdmitsOneHolder(t *testing.T) {
	store := clearSession(t)

	ran := 0
	if !withRefreshLock(func() { ran++ }) {
		t.Fatal("an uncontended lock was refused")
	}
	if ran != 1 {
		t.Fatalf("the body ran %d times", ran)
	}
	// Released on the way out, so the next attempt is not blocked by the last.
	if store[lockKey] != "" {
		t.Error("the lock was not released")
	}

	// A live claim from another tab is respected.
	store[lockKey] = "other-tab|" + itoa(time.Now().Add(lockTTL).UnixMilli())
	if withRefreshLock(func() { ran++ }) {
		t.Error("the lock was taken while another tab held it")
	}
	if ran != 1 {
		t.Errorf("the body ran under a held lock (%d times)", ran)
	}

	// An EXPIRED claim is a tab that crashed mid-refresh. Refusing to ever take
	// it again would leave the session permanently unrenewable, which is worse
	// than the double rotation the short TTL makes unlikely.
	store[lockKey] = "dead-tab|" + itoa(time.Now().Add(-time.Minute).UnixMilli())
	if !withRefreshLock(func() { ran++ }) {
		t.Error("an expired lock was treated as held; the session could never renew")
	}
	if ran != 2 {
		t.Errorf("ran = %d, want 2", ran)
	}
}

// Clearing the session must take the refresh half with it. Dropping only the
// access token would leave a renewal authority in local storage on a machine
// somebody has walked away from — which is the shape SEC4 objects to, restored
// by the logout that was supposed to end it.
func TestClearingTheSessionRemovesTheRefreshHalfToo(t *testing.T) {
	store := clearSession(t)
	storeSession(session{Access: "a", Refresh: "r", Record: "d"})

	setToken("")

	if store[sessionKey] != "" {
		t.Error("local storage still holds a bundle after sign-out")
	}
	if s := currentSession(); s.Access != "" || s.Refresh != "" || s.Record != "" {
		t.Errorf("memory still holds %+v after sign-out", s)
	}
}

// A malformed lock value is not a lock. Treating it as one would wedge renewal
// for the life of the browser profile.
func TestAMalformedLockIsIgnored(t *testing.T) {
	store := clearSession(t)
	store[lockKey] = "no-pipe-here"

	ran := false
	if !withRefreshLock(func() { ran = true }) || !ran {
		t.Error("a malformed lock value blocked rotation forever")
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
