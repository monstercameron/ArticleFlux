package app

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/smart"
)

// What the server has actually observed, so a screen can stop claiming more
// than it knows.
//
// # The confusion this ends
//
// `configured` on the Smart+ screen means "a key is stored". It cannot mean
// more without spending money to find out, so it does not claim to — but an
// expired key, a revoked key, a project with no credit and a model this account
// cannot reach all read as configured, because from where that check stands
// they are. Beside a silent player that says only "the voice didn't start",
// that produced a reader looking at four green ticks with no way to learn
// anything, and an answer that existed only in a log they had to know to open.
//
// This is the missing half: not a better GUESS at whether the key works, but a
// record of what happened the last time it was used. "A key is stored, and the
// last attempt two minutes ago was refused" is a sentence somebody can act on.
//
// # It is a CLASS, never the provider's message
//
// The provider's text can quote the article being read aloud, and §22.11 keeps
// request content out of anything that leaves this process. So what is kept is
// which KIND of refusal it was — enough to decide what to do, useless to
// anybody who intercepts it. The verbatim message stays in the log and in
// `articleflux speech`, both of which belong to the operator.
//
// # Cleared by success
//
// A stale failure is worse than none: an operator who fixed their key an hour
// ago and still sees "refused" learns to ignore the field, and then it is
// furniture. So a successful call clears it, and the screen's claim is always
// about the most recent attempt rather than the worst one.
type refusal struct {
	mu   sync.RWMutex
	kind string
	at   time.Time
}

// Refusal classes. Short, stable, and chosen so each names a DIFFERENT remedy —
// a class two people would fix the same way is a class that should be one.
const (
	// refusedKey is the provider rejecting the credential itself: expired,
	// revoked, wrong project, or an account with nothing left on it.
	refusedKey = "key-refused"
	// refusedModel is a valid key that may not use the model this instance is
	// configured for. Invisible on every screen, because every screen is about
	// the key.
	refusedModel = "model-unavailable"
	// refusedQuota is a valid key over its limit — the same remedy as a billing
	// problem and a different one from a bad key, which is why it is not folded
	// into refusedKey.
	refusedQuota = "quota"
	// refusedReach is the provider not answering: a timeout, a DNS failure, an
	// egress rule. Nothing about the account is wrong.
	refusedReach = "unreachable"
	// refusedOther is everything else, and it exists so an unrecognised failure
	// is still REPORTED rather than swallowed for not matching a pattern.
	refusedOther = "refused"
)

// note records a failure, or clears the record on success.
//
// nil clears. That is the whole cleared-by-success rule in one call site, so
// there is no path that records a failure and forgets to un-record it.
func (r *refusal) note(err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.kind, r.at = "", time.Time{}
		return
	}
	r.kind, r.at = classifyRefusal(err), time.Now().UTC()
}

// last reports the class and when, or "" when nothing has failed since this
// process started.
func (r *refusal) last() (string, time.Time) {
	if r == nil {
		return "", time.Time{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.kind, r.at
}

// classifyRefusal turns a provider error into one of the classes above.
//
// String matching, which is unlovely and is the honest tool for the job: the
// provider returns prose, the HTTP status is already folded into it by the time
// it reaches here, and a class that guessed wrong is still better than a green
// tick beside a silent player. Anything unrecognised is refusedOther rather
// than dropped — the fact that a call failed is the useful half, and the class
// is the refinement.
func classifyRefusal(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, llm.ErrNotConfigured):
		return refusedKey
	case errors.Is(err, smart.ErrNothingToSummarise):
		// Not a refusal at all — an article with nothing in it. Recorded as
		// such would make every link post look like a broken key.
		return ""
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "invalid_api_key"), strings.Contains(s, "incorrect api key"),
		strings.Contains(s, "invalid api key"), strings.Contains(s, "401"),
		strings.Contains(s, "unauthorized"):
		return refusedKey
	case strings.Contains(s, "insufficient_quota"), strings.Contains(s, "quota"),
		strings.Contains(s, "billing"), strings.Contains(s, "429"):
		return refusedQuota
	case strings.Contains(s, "model_not_found"), strings.Contains(s, "does not exist"),
		strings.Contains(s, "do not have access"), strings.Contains(s, "unsupported model"):
		return refusedModel
	case strings.Contains(s, "timeout"), strings.Contains(s, "deadline"),
		strings.Contains(s, "no such host"), strings.Contains(s, "connection refused"),
		strings.Contains(s, "dial tcp"), strings.Contains(s, "eof"):
		return refusedReach
	}
	return refusedOther
}

// LastRefusal is the class and time of the most recent Smart+ failure, for the
// screen that would otherwise only be able to say a key is stored.
func (a *App) LastRefusal() (string, time.Time) {
	if a == nil {
		return "", time.Time{}
	}
	return a.refused.last()
}
