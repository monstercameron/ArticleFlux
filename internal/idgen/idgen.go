// Package idgen mints identifiers.
//
// Two families, and conflating them is a security bug rather than a style
// mistake:
//
//   - **Sortable ids** (New) are k-sortable and therefore *guessable by design* —
//     adjacent ids are adjacent in time. They are fine as primary keys and never
//     fine as a capability.
//   - **Unguessable slugs** (Slug, Token) are 128 bits of CSPRNG output with no
//     structure at all. Public share links (§16) are reachable by strangers with
//     no other authentication, so the URL *is* the credential.
//
// Anything a stranger can fetch gets a Slug. Anything the database sorts gets a
// New.
package idgen

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"
)

// enc is Crockford-ish base32: no padding, no I/L/O/U, case-insensitive on read.
// Chosen over base64url because these ids get read aloud, typed from a phone
// screen, and pasted into support messages.
var enc = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

var (
	mu       sync.Mutex
	lastMS   int64
	lastRand [10]byte
)

// Now is the clock, replaceable in tests.
var Now = func() time.Time { return time.Now().UTC() }

// New returns a 26-character ULID-shaped id: 48 bits of millisecond timestamp
// followed by 80 bits of randomness.
//
// Monotonic within a millisecond: if two ids are minted in the same millisecond
// the random half is incremented rather than redrawn. Without that, two rows
// inserted in the same millisecond have no defined order, and §20.7's keyset
// pagination sorts on (sort_key, id) — an undefined tiebreak there means a
// paging cursor can skip or repeat a row.
func New() string {
	mu.Lock()
	defer mu.Unlock()

	ms := Now().UnixMilli()
	if ms == lastMS {
		incr(&lastRand)
	} else {
		lastMS = ms
		if _, err := rand.Read(lastRand[:]); err != nil {
			panic("idgen: crypto/rand failed: " + err.Error())
		}
	}

	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(ms)<<16)
	copy(b[6:], lastRand[:])
	return enc.EncodeToString(b[:])
}

// incr adds one to the random half, carrying. Overflow across all ten bytes
// would need 2^80 ids inside one millisecond, so the wrap is unreachable rather
// than merely unlikely.
func incr(b *[10]byte) {
	for i := len(b) - 1; i >= 0; i-- {
		b[i]++
		if b[i] != 0 {
			return
		}
	}
}

// TimeOf recovers the mint time from a New id. Useful for debugging and for
// retention sweeps that would otherwise need an extra column.
func TimeOf(id string) (time.Time, error) {
	b, err := enc.DecodeString(strings.ToUpper(id))
	if err != nil {
		return time.Time{}, err
	}
	if len(b) != 16 {
		return time.Time{}, errors.New("idgen: not a sortable id")
	}
	var padded [8]byte
	copy(padded[2:], b[:6])
	return time.UnixMilli(int64(binary.BigEndian.Uint64(padded[:]))).UTC(), nil
}

// Slug returns 128 bits of unguessable randomness, 26 characters.
//
// This is what a public share URL carries (§16). At 128 bits, guessing one is
// not a rate-limiting problem — it is arithmetic that does not finish.
func Slug() string { return random(16) }

// Token returns 256 bits, for anything that behaves like a password: API tokens
// for the sync API, recovery tokens, session tokens. Only its hash is stored
// (§7.1), so length costs nothing at rest.
func Token() string { return random(32) }

// IdempotencyKey is what a client stamps on a mutating RPC so a replayed offline
// outbox entry applies once (§20.7). Client-generated normally; this exists for
// the server's own retries.
func IdempotencyKey() string { return random(16) }

// DeviceID names one browser profile on one machine, for the device family that
// session revocation operates on (§7.4).
func DeviceID() string { return random(16) }

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// A failed CSPRNG means we cannot mint a credential. Continuing with a
		// predictable one is strictly worse than not starting.
		panic("idgen: crypto/rand failed: " + err.Error())
	}
	return enc.EncodeToString(b)
}
