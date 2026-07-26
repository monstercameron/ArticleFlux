package secret

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Fast parameters so the suite does not spend a second per hash. The real
// parameters come from Tune at startup.
var testParams = Params{Time: 1, Memory: 8 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16}

func TestHashAndVerifyPassword(t *testing.T) {
	h, err := HashPassword("correct horse battery staple", testParams)
	if err != nil {
		t.Fatal(err)
	}
	ok, _, err := VerifyPassword("correct horse battery staple", h, testParams)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the right password should verify")
	}
	ok, _, err = VerifyPassword("Correct horse battery staple", h, testParams)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a one-character difference must not verify")
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	a, _ := HashPassword("same", testParams)
	b, _ := HashPassword("same", testParams)
	if a == b {
		t.Error("two hashes of the same password must differ — otherwise the " +
			"hash is a lookup key and a breach is a rainbow table away")
	}
}

// The hash is self-describing so parameters can be raised later without either
// locking existing users out or silently never applying to them.
func TestHashCarriesItsOwnParameters(t *testing.T) {
	h, err := HashPassword("x", testParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Errorf("hash = %q, want PHC format carrying m/t/p", h)
	}
	// It still verifies under its own parameters even when the current policy
	// has moved on.
	stronger := Params{Time: 3, Memory: 64 * 1024, Threads: 1, KeyLen: 32, SaltLen: 16}
	ok, rehash, err := VerifyPassword("x", h, stronger)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("an old hash must stay verifiable after parameters are raised")
	}
	if !rehash {
		t.Error("a weaker stored hash must report rehash=true, or raising the " +
			"cost never actually reaches existing users")
	}
}

func TestNoRehashWhenParametersMatch(t *testing.T) {
	h, _ := HashPassword("x", testParams)
	_, rehash, _ := VerifyPassword("x", h, testParams)
	if rehash {
		t.Error("matching parameters should not ask for a rehash")
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	for _, h := range []string{
		"", "not-a-hash", "$argon2id$", "$bcrypt$v=19$m=1,t=1,p=1$aa$bb",
		"$argon2id$v=99$m=1,t=1,p=1$aa$bb",
		"$argon2id$v=19$m=1,t=1,p=1$!!!$bb",
		"$argon2id$v=19$garbage$aa$bb",
	} {
		if _, _, err := VerifyPassword("x", h, testParams); err == nil {
			t.Errorf("VerifyPassword(%q) should have errored", h)
		}
	}
}

func TestEmptyPasswordIsRejected(t *testing.T) {
	if _, err := HashPassword("", testParams); !errors.Is(err, ErrEmptySecret) {
		t.Errorf("got %v, want ErrEmptySecret", err)
	}
}

// TODO 2.3's bar: the benchmark picks parameters hitting a target ms budget.
func TestTuneHitsTheBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("tuning runs real Argon2id work")
	}
	target := 40 * time.Millisecond
	p := Tune(target, Params{Memory: 64 * 1024, Time: 4})

	if p.Memory < 8*1024 {
		t.Errorf("tuned memory %d KiB is below the floor", p.Memory)
	}
	if p.Memory > 64*1024 {
		t.Errorf("tuned memory %d KiB exceeded the ceiling", p.Memory)
	}
	// The tuned parameters must actually cost something. An under-budget result
	// that returns instantly would mean the tuner silently gave up.
	d := measure(p)
	if d > 10*target {
		t.Errorf("tuned parameters take %v, far over the %v target", d, target)
	}
	t.Logf("tuned to %s in %v (target %v)", p, d, target)
}

func TestTuneRespectsCeilings(t *testing.T) {
	if testing.Short() {
		t.Skip("tuning runs real Argon2id work")
	}
	// An unreachable target must stop at the ceiling rather than looping.
	p := Tune(time.Hour, Params{Memory: 16 * 1024, Time: 2})
	if p.Memory > 16*1024 || p.Time > 2 {
		t.Errorf("tuned past the ceiling: %s", p)
	}
}

// The split that matters: tokens are hashed fast on purpose. Argon2id here would
// cost 100ms per sync poll to defend against brute-forcing 256 bits of CSPRNG
// output, which is not an attack.
func TestTokenHashingIsFast(t *testing.T) {
	tok := "0123456789ABCDEFGHJKMNPQRSTVWXYZ0123456789ABCDEFGH"
	h := HashToken(tok)
	if !strings.HasPrefix(h, "sha256:") {
		t.Errorf("hash = %q, want a sha256 prefix", h)
	}
	if !VerifyToken(tok, h) {
		t.Error("the right token should verify")
	}
	if VerifyToken(tok+"x", h) {
		t.Error("a different token must not verify")
	}

	start := time.Now()
	for i := 0; i < 1000; i++ {
		HashToken(tok)
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("1000 token hashes took %v — the sync API verifies one per poll, "+
			"so this must stay cheap", d)
	}
}

func TestTokenHashingIsDeterministic(t *testing.T) {
	// Unlike a password hash, a token hash must be a lookup key: the server finds
	// the row by hash, so it cannot be salted.
	a, b := HashToken("abc"), HashToken("abc")
	if a != b {
		t.Error("token hashing must be deterministic or lookup is impossible")
	}
}

func TestSignAndVerify(t *testing.T) {
	key := []byte("a-32-byte-key-for-hmac-testing!!")
	tag := Sign(key, "cursor-payload")

	if !VerifySignature(key, "cursor-payload", tag) {
		t.Error("a genuine signature should verify")
	}
	if VerifySignature(key, "cursor-payloae", tag) {
		t.Error("a modified message must not verify")
	}
	if VerifySignature([]byte("a-different-32-byte-key-for-hmac"), "cursor-payload", tag) {
		t.Error("a different key must not verify")
	}
	if strings.ContainsAny(tag, "+/=") {
		t.Errorf("tag %q must be URL-safe — these travel in query strings", tag)
	}
}

func TestEncryptRoundTrip(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("imap-app-password-hunter2")

	ct, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ct, "hunter2") {
		t.Fatal("plaintext survived into the ciphertext")
	}
	got, err := Decrypt(key, ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Errorf("decrypted %q, want %q", got, plain)
	}
}

// GCM nonce reuse under one key leaks the authentication key, so the nonce must
// be fresh per message. Identical ciphertexts would be the visible symptom.
func TestEncryptUsesAFreshNonce(t *testing.T) {
	key, _ := NewKey()
	a, _ := Encrypt(key, []byte("same plaintext"))
	b, _ := Encrypt(key, []byte("same plaintext"))
	if a == b {
		t.Error("two encryptions of the same plaintext must differ")
	}
}

// The reason for an AEAD rather than raw AES: tampering fails loudly instead of
// yielding garbage that the IMAP poller would then send to a mail server.
func TestDecryptRejectsTampering(t *testing.T) {
	key, _ := NewKey()
	ct, _ := Encrypt(key, []byte("secret"))

	flipped := []byte(ct)
	flipped[len(flipped)-1] ^= 0x01
	if _, err := Decrypt(key, string(flipped)); !errors.Is(err, ErrCiphertext) {
		t.Errorf("tampered ciphertext = %v, want ErrCiphertext", err)
	}

	other, _ := NewKey()
	if _, err := Decrypt(other, ct); !errors.Is(err, ErrCiphertext) {
		t.Errorf("wrong key = %v, want ErrCiphertext", err)
	}

	for _, bad := range []string{"", "!!!!", "aGk"} {
		if _, err := Decrypt(key, bad); err == nil {
			t.Errorf("Decrypt(%q) should have failed", bad)
		}
	}
}

func TestKeyLengthIsEnforced(t *testing.T) {
	for _, k := range [][]byte{nil, make([]byte, 16), make([]byte, 31), make([]byte, 33)} {
		if _, err := Encrypt(k, []byte("x")); !errors.Is(err, ErrKeyLength) {
			t.Errorf("Encrypt with a %d-byte key = %v, want ErrKeyLength", len(k), err)
		}
	}
}
