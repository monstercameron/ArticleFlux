// Package secret holds every cryptographic primitive Tidings uses, so there is
// one place to audit and one place to change.
//
// Four jobs, deliberately distinct:
//
//   - **Passwords** — Argon2id, slow on purpose, parameters tuned to the box.
//   - **Tokens** — SHA-256, deliberately *fast*, because a 256-bit random token
//     has nothing to brute-force. Running Argon2id on an API token would cost
//     100ms per sync request to defend against an attack that does not exist.
//   - **HMAC** — for values that must be tamper-evident but readable: signed
//     cursors, WebSub callback verification, unsubscribe links.
//   - **Symmetric encryption** — mailbox credentials (§20.7 / A20), which the
//     IMAP poller must be able to *use*, so they cannot be hashed.
//
// The password/token split is the one people get wrong in both directions, and
// both directions are bad: Argon2id on tokens is a self-inflicted DoS, and
// SHA-256 on passwords is a breach waiting for a wordlist.
//
// A9 note: no 2FA (cost). §7.3's rate limits and lockout are the compensating
// control, which makes the password parameters here load-bearing.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Params are Argon2id cost parameters.
type Params struct {
	Time    uint32 // iterations
	Memory  uint32 // KiB
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultParams follow the OWASP recommendation for Argon2id: 19 MiB, 2
// iterations, 1 degree of parallelism.
//
// Memory-hard rather than time-hard is the whole point — GPU and ASIC attackers
// are bottlenecked on memory bandwidth, not on clock speed, so raising Memory
// buys far more than raising Time.
var DefaultParams = Params{
	Time:    2,
	Memory:  19 * 1024,
	Threads: 1,
	KeyLen:  32,
	SaltLen: 16,
}

var (
	ErrMismatch    = errors.New("secret: password does not match")
	ErrBadHash     = errors.New("secret: malformed hash")
	ErrUnsupported = errors.New("secret: unsupported algorithm or version")
	ErrCiphertext  = errors.New("secret: ciphertext is invalid")
	ErrKeyLength   = errors.New("secret: key must be 32 bytes")
	ErrEmptySecret = errors.New("secret: empty input")
)

// HashPassword returns a PHC-format string that carries its own parameters:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// Self-describing matters because parameters get raised over time. A stored hash
// has to remain verifiable under the parameters it was made with, and Verify has
// to be able to tell the caller that a rehash is due — otherwise raising the
// cost either locks everyone out or silently never applies to existing users.
func HashPassword(password string, p Params) (string, error) {
	if password == "" {
		return "", ErrEmptySecret
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	sum := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads,
		b64(salt), b64(sum)), nil
}

// VerifyPassword checks a password against a stored hash.
//
// The second return value is `rehash`: true when the stored hash used weaker
// parameters than `want`. Callers should re-hash on a successful login, which is
// the only moment the plaintext is available.
func VerifyPassword(password, encoded string, want Params) (ok bool, rehash bool, err error) {
	p, salt, sum, err := decodeHash(encoded)
	if err != nil {
		return false, false, err
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(sum)))
	// Constant time: a length-varying or early-exit comparison leaks how much of
	// the hash matched, which is enough to recover it byte by byte.
	if subtle.ConstantTimeCompare(got, sum) != 1 {
		return false, false, nil
	}
	weaker := p.Memory < want.Memory || p.Time < want.Time
	return true, weaker, nil
}

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" {
		return Params{}, nil, nil, ErrBadHash
	}
	if parts[1] != "argon2id" {
		return Params{}, nil, nil, fmt.Errorf("%w: %s", ErrUnsupported, parts[1])
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Params{}, nil, nil, ErrBadHash
	}
	if version != argon2.Version {
		return Params{}, nil, nil, fmt.Errorf("%w: v=%d", ErrUnsupported, version)
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return Params{}, nil, nil, ErrBadHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Params{}, nil, nil, ErrBadHash
	}
	sum, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Params{}, nil, nil, ErrBadHash
	}
	p.SaltLen = uint32(len(salt))
	p.KeyLen = uint32(len(sum))
	return p, salt, sum, nil
}

// Tune finds Argon2id parameters that hit a target duration on this machine.
//
// TODO 2.3's bar. It exists because the right parameters are a property of the
// hardware, not of the source tree: DefaultParams might take 8ms on a modern
// server and 400ms on the small box this is likely to be self-hosted on. Shipping
// one constant means either a weak hash on fast hardware or a login that feels
// broken on slow hardware.
//
// It scales memory rather than iterations, for the same reason DefaultParams is
// memory-hard: memory is what actually costs an attacker.
func Tune(target time.Duration, ceiling Params) Params {
	p := Params{Time: 1, Memory: 8 * 1024, Threads: DefaultParams.Threads,
		KeyLen: DefaultParams.KeyLen, SaltLen: DefaultParams.SaltLen}
	if ceiling.Memory == 0 {
		ceiling.Memory = 256 * 1024 // 256 MiB; beyond this a login costs real RAM
	}
	if ceiling.Time == 0 {
		ceiling.Time = 10
	}

	best := p
	for {
		if measure(p) >= target {
			break
		}
		best = p
		switch {
		case p.Memory*2 <= ceiling.Memory:
			p.Memory *= 2
		case p.Time+1 <= ceiling.Time:
			p.Time++
		default:
			return p // ceilings reached; this is the strongest allowed
		}
	}
	// p just crossed the target. Prefer the stronger of the two, since being a
	// little over budget is better than being under it.
	if measure(p) <= target*2 {
		return p
	}
	return best
}

func measure(p Params) time.Duration {
	salt := make([]byte, p.SaltLen)
	start := time.Now()
	argon2.IDKey([]byte("benchmark-password"), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return time.Since(start)
}

// HashToken hashes a bearer token for storage.
//
// SHA-256, not Argon2id, and that is not a shortcut. A token from idgen.Token is
// 256 bits of CSPRNG output, so there is no dictionary to run and no keyspace to
// search — the slow-hash property defends against an attack that cannot happen.
// Meanwhile the sync API verifies a token on every poll, so a 100ms hash would
// be a self-inflicted denial of service.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + b64(sum[:])
}

// VerifyToken compares in constant time.
func VerifyToken(token, stored string) bool {
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(stored)) == 1
}

// Sign returns a URL-safe HMAC-SHA256 tag over msg.
//
// For values that travel through somewhere untrusted and must come back intact:
// pagination cursors, WebSub callback tokens, unsubscribe links.
func Sign(key []byte, msg string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// VerifySignature checks a tag in constant time.
func VerifySignature(key []byte, msg, tag string) bool {
	want := Sign(key, msg)
	return subtle.ConstantTimeCompare([]byte(want), []byte(tag)) == 1
}

// Encrypt seals plaintext with AES-256-GCM under a 32-byte key.
//
// Used for mailbox credentials (A20): the IMAP poller has to present the actual
// password to the mail server, so hashing is not an option. The trade is
// explicit — these are recoverable by anyone holding the key, so the key lives
// outside the database (§21) and never in a backup alongside it.
//
// The nonce is random per message and prefixed to the ciphertext. GCM nonce reuse
// under the same key is catastrophic rather than merely weak — it leaks the
// authentication key — so it is never derived from a counter here.
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens what Encrypt sealed. A tampered ciphertext fails rather than
// returning garbage, which is the entire reason for using an AEAD.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrCiphertext
	}
	if len(raw) < gcm.NonceSize() {
		return nil, ErrCiphertext
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	out, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrCiphertext
	}
	return out, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrKeyLength
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// NewKey mints a 32-byte encryption key.
func NewKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

func b64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

// String makes Params printable for the startup log, so the tuned values are
// visible rather than guessed at.
func (p Params) String() string {
	return "m=" + strconv.Itoa(int(p.Memory)) +
		",t=" + strconv.Itoa(int(p.Time)) +
		",p=" + strconv.Itoa(int(p.Threads))
}
