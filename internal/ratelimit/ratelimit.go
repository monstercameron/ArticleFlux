// Package ratelimit enforces the §20.7 limits (TODO 7.3d).
//
// # Why a token bucket rather than a fixed window
//
// A fixed window ("60 per minute") lets a client send 60 at 11:59:59 and 60 more
// at 12:00:00 — 120 in one second, twice the limit, at exactly the moment the
// counter resets. That is not a corner case: a polling client with a round
// minute in its schedule finds it immediately.
//
// A token bucket refills continuously, so the limit holds over every window and
// not just the ones that start on a boundary. The burst size is separate from
// the rate, which is what makes "60 a minute" tolerate a client that wakes up
// and sends five at once.
//
// # Every limit is per-KEY, and the key is the decision
//
// §20.7's table is mostly "per user" but the interesting ones are not. Login is
// limited per username AND per IP, because per-username alone lets one attacker
// lock out every account by name, and per-IP alone does nothing about a slow
// distributed guess at one password. §7.3 calls this the main compensation for
// having no second factor, so getting the key wrong is not a performance
// mistake.
//
// # Refusals carry retry_after
//
// §20.7 maps exhaustion to ResourceExhausted with `retry_after_s`, and §20.11
// notes a client backing off on its own schedule against a server that knows the
// answer is the limiter's problem rather than its subject's. So Allow returns
// the wait rather than a bare boolean.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

// Rule is one limit.
type Rule struct {
	// Name identifies the surface, for the error and for §9.
	Name string
	// Per is the window the Limit is expressed over — almost always a minute or
	// an hour, because those are the units the table is written in and a rule
	// nobody can read against the spec is a rule nobody maintains.
	Per time.Duration
	// Limit is how many are permitted per window.
	Limit int
	// Burst is how many may arrive at once. Zero means Limit, which is the
	// forgiving default; a smaller burst is for surfaces where a spike is itself
	// the abuse.
	Burst int
}

// The §20.7 table, as code. Named so a call site says which row it is enforcing
// rather than repeating a number that then drifts from the document.
var (
	LoginPerUser     = Rule{Name: "login", Per: time.Minute, Limit: 5, Burst: 5}
	LoginPerIP       = Rule{Name: "login (address)", Per: time.Minute, Limit: 20, Burst: 20}
	RecoveryPerUser  = Rule{Name: "recovery", Per: time.Hour, Limit: 3, Burst: 3}
	RecoveryPerIP    = Rule{Name: "recovery (address)", Per: time.Hour, Limit: 10, Burst: 10}
	SyncAPIPerToken  = Rule{Name: "sync API", Per: time.Minute, Limit: 60, Burst: 10}
	PublicSharePerIP = Rule{Name: "public share", Per: time.Minute, Limit: 30, Burst: 10}
	WebSubPerSource  = Rule{Name: "websub callback", Per: time.Minute, Limit: 120, Burst: 20}
	PackBuildPerUser = Rule{Name: "pack build", Per: time.Hour, Limit: 10, Burst: 3}
	DefaultPerUser   = Rule{Name: "requests", Per: time.Minute, Limit: 600, Burst: 60}

	// ProxyPerClient covers /asset and /p — TODO 6.14 and 6.15 both record a
	// per-user rate limit as owed to 7.3d.
	//
	// The rate is §20.7's "everything else" backstop. The BURST is not from the
	// table, because the table has no row for a surface where one legitimate
	// user action produces hundreds of requests at once, and getting that
	// number wrong does not throttle an abuser — it breaks the images in an
	// article and looks like the proxy is broken.
	//
	// So it was measured rather than chosen. Running the real rewriter over
	// every article body in a 3,878-item corpus, counting the asset URLs each
	// one mints:
	//
	//	with any assets   1,353 of 3,878 (35%)
	//	mean              3.0
	//	p50 / p90 / p95   0 / 6 / 13
	//	p99               43
	//	MAX               396
	//
	// The distribution is the point. Almost every article is free, and the tail
	// is enormous — a photo essay mints 396 capabilities that the browser
	// requests in one go. A burst sized on the p99 would work for a year and
	// then break the one article somebody actually wanted to look at.
	//
	// 500 clears the corpus maximum with room, so no article that exists here
	// can trip it, while a runaway client is still held to the sustained 10 a
	// second. Both halves matter: the burst is what makes it invisible to a
	// reader, and the rate is what makes it a limit at all.
	ProxyPerClient = Rule{Name: "proxy", Per: time.Minute, Limit: 600, Burst: 500}
)

// rate returns tokens per second.
func (r Rule) rate() float64 {
	if r.Per <= 0 || r.Limit <= 0 {
		return 0
	}
	return float64(r.Limit) / r.Per.Seconds()
}

func (r Rule) burst() float64 {
	if r.Burst > 0 {
		return float64(r.Burst)
	}
	return float64(r.Limit)
}

// Limiter enforces a set of rules across many keys.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
	// maxKeys bounds memory. See sweep.
	maxKeys int
}

type bucket struct {
	tokens float64
	last   time.Time
	// seen is when this key was last touched, for eviction.
	seen time.Time
}

// Options configure a Limiter.
type Options struct {
	// Now is injectable so refill is testable without sleeping.
	Now func() time.Time
	// MaxKeys bounds how many distinct keys are tracked. Zero means 50,000.
	MaxKeys int
}

// New builds a Limiter.
func New(opt Options) *Limiter {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.MaxKeys == 0 {
		opt.MaxKeys = 50_000
	}
	return &Limiter{
		buckets: map[string]*bucket{},
		now:     opt.Now,
		maxKeys: opt.MaxKeys,
	}
}

// Allow consumes one token for (rule, key).
//
// Returns whether it was permitted and, when not, how long to wait — which is
// what §20.7's `retry_after_s` carries. A client told to wait exactly long
// enough stops guessing, and a client left guessing backs off on a schedule
// unrelated to when it would actually be served.
func (l *Limiter) Allow(rule Rule, key string) (bool, time.Duration) {
	rate := rule.rate()
	if rate <= 0 {
		// A rule with no limit permits everything. Treating a misconfigured rule
		// as "deny" would take a surface down over a typo in a constant.
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	id := rule.Name + "\x00" + key

	b, ok := l.buckets[id]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.sweepLocked(now)
		}
		b = &bucket{tokens: rule.burst(), last: now}
		l.buckets[id] = b
	}

	// Refill for the elapsed time, capped at the burst.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(rule.burst(), b.tokens+elapsed*rate)
		b.last = now
	}
	b.seen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// How long until one token exists. Rounded UP to the nearest second, because
	// retry_after_s is whole seconds and rounding down tells the client to retry
	// slightly too early — which produces one guaranteed wasted request per
	// refusal, at exactly the moment the surface is under pressure.
	need := (1 - b.tokens) / rate
	wait := time.Duration(math.Ceil(need)) * time.Second
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

// sweepLocked evicts keys that have refilled to full and gone quiet.
//
// A limiter that never forgets is a memory leak keyed by attacker-controlled
// strings: an IP-keyed rule facing a botnet grows a bucket per address forever.
// A key whose bucket is full has no state worth keeping — it is
// indistinguishable from a key that has never been seen.
func (l *Limiter) sweepLocked(now time.Time) {
	for id, b := range l.buckets {
		if now.Sub(b.seen) > time.Hour {
			delete(l.buckets, id)
		}
	}
	// Still full: the sweep found nothing stale, which means the limiter is
	// genuinely tracking maxKeys active clients. Dropping the oldest is the only
	// remaining option, and it fails OPEN for that key — which is correct here.
	// A limiter that started denying everyone because it ran out of map space
	// would turn a traffic spike into an outage.
	if len(l.buckets) >= l.maxKeys {
		var oldestID string
		var oldest time.Time
		for id, b := range l.buckets {
			if oldest.IsZero() || b.seen.Before(oldest) {
				oldestID, oldest = id, b.seen
			}
		}
		delete(l.buckets, oldestID)
	}
}

// Keys reports how many buckets are live, for §9.
func (l *Limiter) Keys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Reset clears a key, for a successful login clearing its own limiter.
//
// Deliberately NOT called on every success. The login limiter counts FAILURES
// only, and the reason is recorded because the first version got it backwards:
// clearing on success means one household behind a shared address can lock the
// instance out with ordinary typos, because the per-IP key is shared by everyone
// who lives there.
func (l *Limiter) Reset(rule Rule, key string) {
	l.mu.Lock()
	delete(l.buckets, rule.Name+"\x00"+key)
	l.mu.Unlock()
}
