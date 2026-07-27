// Package authn holds the authentication policy §7.1a shipped without
// (TODO 6.1, plan.md §7.2, §7.3).
//
// # What this package is for
//
// §7.3 states the premise plainly: with one factor on a public service, a
// phished or stuffed password is total account compromise. There is no 2FA, so
// four controls have to do the work of one — lockout, refresh-token reuse
// detection, sudo mode, and rate limiting. Three of those are decisions rather
// than storage, and this is where the decisions live.
//
// Policy is separated from `internal/store` so the numbers can be read, argued
// with and tested without a database. A lockout curve buried in a SQL file is a
// lockout curve nobody reviews.
//
// # The shape of the lockout, and why it is not just a bigger limiter
//
// A rate limiter blunts a burst; it forgets. A lockout accumulates. §7.1a
// shipped a fixed-window limiter in memory and recorded that a restart clears
// it, which is honest and is exactly the hole: an attacker who can provoke a
// restart, or who waits for a deploy, gets an unlimited budget against a
// counter that forgets. So the count is a table, and this computes a delay
// from it.
//
// The curve doubles and then stops. It stops because an uncapped lockout is a
// denial of service against the ACCOUNT OWNER that anyone can trigger by typing
// a wrong password enough times — and unlike the attacker, the owner cannot
// simply move to another address. A cap of fifteen minutes costs an attacker
// four attempts an hour, which defeats guessing, and costs a locked-out owner
// fifteen minutes, which is survivable.
package authn

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

// Lockout is the per-account curve.
type Lockout struct {
	// Free is how many failures cost nothing. Three, because two typos in a row
	// is an ordinary morning and a policy that punishes it teaches people to
	// keep passwords somewhere convenient.
	Free int
	// Base is the first delay, applied at Free+1.
	Base time.Duration
	// Max caps it. See the package comment: an uncapped lockout is a denial of
	// service against the account owner that any stranger can trigger.
	Max time.Duration
	// Window bounds the per-address count. Unlike the per-account count, which
	// runs since the last success, an address gets no absolution from guessing
	// one password correctly.
	Window time.Duration
	// AddressLimit is failures from one address inside Window before it is
	// refused outright. Generous, because behind a reverse proxy or a carrier
	// NAT this is shared by a whole household or building.
	AddressLimit int
}

// DefaultLockout implements §7.3's table: 5/min per user, 20/min per IP, then
// exponential lockout.
//
// The published numbers are per-minute rates and this is a per-failure curve,
// which is the stricter reading: three free attempts and then a delay means an
// attacker gets nowhere near five a minute, while someone mistyping twice is
// unaffected. The address limit is 20 in a minute as written.
var DefaultLockout = Lockout{
	Free:         3,
	Base:         5 * time.Second,
	Max:          15 * time.Minute,
	Window:       time.Minute,
	AddressLimit: 20,
}

// Delay returns how long an account must wait after `failures` consecutive
// failures. Zero means no lockout applies.
func (l Lockout) Delay(failures int) time.Duration {
	if failures <= l.Free {
		return 0
	}
	d := l.Base
	for i := l.Free + 1; i < failures; i++ {
		d *= 2
		if d >= l.Max {
			return l.Max
		}
	}
	if d > l.Max {
		return l.Max
	}
	return d
}

// Decision is the answer to "may this attempt proceed".
type Decision struct {
	// Allowed is false when the attempt must be refused without checking the
	// password.
	Allowed bool
	// RetryAfter is how long until it would be allowed. Zero when allowed.
	RetryAfter time.Duration
	// Reason distinguishes an account lockout from an address block for the
	// AUDIT LOG. It is deliberately not for the user: see the package note on
	// uniform errors — telling someone "this account is locked" confirms the
	// account exists.
	Reason string
}

// Check decides whether a login attempt may proceed.
//
// `sinceLastFailure` is how long ago the most recent failure was, which is what
// turns a delay into a decision: five consecutive failures earn a ten-second
// lockout, and eleven seconds later that lockout is spent.
func (l Lockout) Check(userFailures int, sinceLastFailure time.Duration, addressFailures int) Decision {
	// The address check comes first and is a hard refusal. It is the control
	// that survives an attacker rotating usernames, which the per-account
	// counter by construction cannot see.
	if l.AddressLimit > 0 && addressFailures >= l.AddressLimit {
		return Decision{RetryAfter: l.Window, Reason: "address"}
	}
	if d := l.Delay(userFailures); d > 0 && sinceLastFailure < d {
		return Decision{RetryAfter: d - sinceLastFailure, Reason: "account"}
	}
	return Decision{Allowed: true}
}

// ---------------------------------------------------------------------------
// Recovery codes (§7.2)
// ---------------------------------------------------------------------------

// RecoveryCodeCount is how many are issued at once. Ten is the number every
// service that does this uses, and matching it means the printed sheet looks
// like the printed sheets people already know to keep.
const RecoveryCodeCount = 10

// Crockford base32, unpadded: 0-9 plus 22 letters, omitting I, L, O and U.
//
// These get written on paper and typed back months later by someone already
// locked out and already annoyed. Every character pair that looks alike is a
// failed attempt, and there is nobody to file a support request with on a
// self-hosted box. Crockford exists for exactly this and is worth using instead
// of inventing one: it drops the letters that collide with digits, and U as
// well, which is what keeps a random code from spelling something unfortunate
// on a sheet the user has to hand to somebody.
var recoveryEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// GenerateRecoveryCodes returns codes in plaintext. The CALLER hashes them and
// shows them once — they are unrecoverable afterwards by design, which is the
// whole reason they are safe to write on paper.
//
// 80 bits each, which is far beyond a password because a recovery code is not
// rate-limited the way a login is: it is checked by an equality test against a
// hash, so its only defence is being unguessable.
func GenerateRecoveryCodes(n int) ([]string, error) {
	if n <= 0 {
		n = RecoveryCodeCount
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		b := make([]byte, 10) // 80 bits
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		s := recoveryEncoding.EncodeToString(b)
		// Grouped, because a sixteen-character run gets transcribed wrong.
		out = append(out, s[:4]+"-"+s[4:8]+"-"+s[8:12]+"-"+s[12:])
	}
	return out, nil
}

// NormalizeRecoveryCode makes typing forgiving without making guessing easier.
//
// Case and the grouping dashes carry no entropy — they are presentation — so
// accepting them in any form costs nothing. What it buys is that someone typing
// their code in lowercase, or without the dashes, or with spaces because that
// is how they wrote it down, gets in.
func NormalizeRecoveryCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s)
	// Crockford's own decoding rule: the alphabet omits I, L and O precisely so
	// that a reader who writes one can be mapped back to the digit it was
	// mistaken for. Applying it here is what makes "typed what I saw" work.
	return strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(s)
}

// ---------------------------------------------------------------------------
// Reset tokens (§7.2)
// ---------------------------------------------------------------------------

// ResetTokenLifetime is how long an admin-minted reset is usable.
//
// One hour. Long enough to hand someone a link and have them act on it, short
// enough that a token pasted into a chat log is dead before the log is
// archived. D14 rules out SMTP, so these travel by whatever channel the admin
// already has — which is a channel with no security properties at all, and the
// lifetime is the only compensation available.
const ResetTokenLifetime = time.Hour

// GenerateToken returns a URL-safe high-entropy token for a reset link.
func GenerateToken() (string, error) {
	b := make([]byte, 32) // 256 bits
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return recoveryEncoding.EncodeToString(b), nil
}

// ---------------------------------------------------------------------------
// Sudo mode (§7.3)
// ---------------------------------------------------------------------------

// SudoWindow is how long a re-authentication stays valid.
//
// Fifteen minutes: long enough to change three roles and suspend an account
// without typing a password four times, short enough that a walked-away-from
// laptop is not an admin console. §7.3 requires it before role changes,
// suspension, impersonation, deletion, or a full export — the operations where
// a stolen session becomes a stolen instance.
const SudoWindow = 15 * time.Minute

// SudoAction names an operation that requires fresh authentication.
type SudoAction string

const (
	SudoChangeRole   SudoAction = "role.change"
	SudoSuspendUser  SudoAction = "user.suspend"
	SudoImpersonate  SudoAction = "user.impersonate"
	SudoDeleteUser   SudoAction = "user.delete"
	SudoExportAll    SudoAction = "export.all"
	SudoChangePasswd SudoAction = "password.change"
	SudoMintToken    SudoAction = "token.mint"
	SudoManageMail   SudoAction = "mailbox.manage"
)

// sudoRequired is the list, as a set, so the check is a lookup rather than a
// condition somebody has to remember to write at each call site.
//
// Adding an operation here is how it becomes protected. A list is used rather
// than a per-handler flag because the question "what needs sudo" should be
// answerable by reading one thing.
var sudoRequired = map[SudoAction]bool{
	SudoChangeRole: true, SudoSuspendUser: true, SudoImpersonate: true,
	SudoDeleteUser: true, SudoExportAll: true, SudoChangePasswd: true,
	SudoMintToken: true, SudoManageMail: true,
}

// NeedsSudo reports whether an action requires fresh authentication.
//
// An UNKNOWN action requires it. Same reasoning as authz's fail-closed map: a
// caller passing an action this package has never heard of is either a typo or
// a new operation nobody classified, and both are safer answered "yes".
func NeedsSudo(a SudoAction) bool {
	if a == "" {
		return true
	}
	if req, ok := sudoRequired[a]; ok {
		return req
	}
	return true
}

// SudoFresh reports whether an authentication at `authAt` still counts as
// fresh at `now`.
//
// A zero authAt is never fresh, which is what makes "the session has no
// re-authentication recorded" behave like "it was too long ago" rather than
// like the zero time being infinitely far in the past... or infinitely recent,
// depending on which way the subtraction is written. That ambiguity is the bug
// this function exists to not have.
func SudoFresh(authAt, now time.Time) bool {
	if authAt.IsZero() {
		return false
	}
	if authAt.After(now) {
		// A clock that went backwards, or a stamp from the future. Refusing is
		// the only safe reading: treating it as fresh makes a bad clock a
		// permanent sudo session.
		return false
	}
	return now.Sub(authAt) < SudoWindow
}

// ErrSudoRequired is returned to a caller that must re-authenticate.
type ErrSudoRequired struct{ Action SudoAction }

func (e ErrSudoRequired) Error() string {
	return fmt.Sprintf("authn: %s requires re-authentication", e.Action)
}

// ---------------------------------------------------------------------------
// Constant-time comparison
// ---------------------------------------------------------------------------

// EqualToken compares two hashes without leaking their contents through timing.
//
// The hashes are not secret and an attacker cannot mount this attack over a
// network against a SHA-256 comparison — but the cost is one function call, and
// the alternative is a reader having to work out whether that is true here.
func EqualToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
