// Package sftest is the scripted SchemaFlux provider this repository's tests
// drive the typed operations with.
//
// # Why not schemafluxtest directly
//
// SchemaFlux v2 ships its own (schemafluxtest.Provider) and this package is
// built on it — the shaped answers come from it, and Install is its Install.
// What it adds is the one behaviour the v1 fake had and v2's does not that
// forty-odd tests in internal/smart, internal/app and internal/transport were
// written against: a fluent script (New().As(...).Shaped().Reply(...)), a reply
// function given the call number, and the recorded contexts — the per-feature
// deadlines are this repository's own and the only way to assert them is to
// look at the context a call arrived with.
//
// It is an ordinary package rather than a _test.go file so the three test
// packages can share one copy. Nothing outside a test imports it; it opens no
// connection and holds no key.
package sftest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	schemaflux "github.com/monstercameron/schemaflux/v2"
	"github.com/monstercameron/schemaflux/v2/schemafluxtest"
)

// Provider answers from a script. Safe for concurrent use.
type Provider struct {
	mu        sync.Mutex
	name      string
	replies   []string
	errs      []error
	index     int
	shaped    bool
	delay     time.Duration
	usage     schemaflux.Usage
	replyFunc func(call int, req schemaflux.Request) (string, error)
	requests  []schemaflux.Request
	contexts  []context.Context
	shaper    *schemafluxtest.Provider
}

// New answers "{}" to everything until scripted.
func New() *Provider {
	sh := &schemafluxtest.Provider{}
	sh.Shape()
	return &Provider{replies: []string{"{}"}, name: "local", shaper: sh}
}

// As names the provider.
func (p *Provider) As(name string) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.name = name
	return p
}

// Shaped answers with whatever shape the request declared when no reply is
// scripted. A scripted reply always wins.
func (p *Provider) Shaped() *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shaped, p.replies = true, nil
	return p
}

// Reply sets the bodies answered in order; the last repeats.
func (p *Provider) Reply(bodies ...string) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(bodies) == 0 {
		p.replies = []string{"{}"}
	} else {
		p.replies = append([]string(nil), bodies...)
	}
	p.errs, p.index = nil, 0
	return p
}

// Fail fails every call with err.
func (p *Provider) Fail(err error) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		err = errors.New("sftest: provider unavailable")
	}
	p.errs, p.index = []error{err}, 0
	return p
}

// FailThen fails the first `failures` calls with err, then answers body.
func (p *Provider) FailThen(failures int, err error, body string) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		err = errors.New("sftest: provider unavailable")
	}
	p.errs = make([]error, failures)
	for i := range p.errs {
		p.errs[i] = err
	}
	p.replies, p.index = []string{body}, 0
	return p
}

// Slow delays every answer by d, ending early with the context.
func (p *Provider) Slow(d time.Duration) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delay = d
	return p
}

// WithUsage sets the token count every answer reports.
func (p *Provider) WithUsage(u schemaflux.Usage) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.usage = u
	return p
}

// ReplyFunc computes each answer from the call number and the request.
func (p *Provider) ReplyFunc(fn func(call int, req schemaflux.Request) (string, error)) *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.replyFunc = fn
	return p
}

// Requests returns every request received, in order.
func (p *Provider) Requests() []schemaflux.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]schemaflux.Request(nil), p.requests...)
}

// LastRequest is the most recent request, or the zero value.
func (p *Provider) LastRequest() schemaflux.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return schemaflux.Request{}
	}
	return p.requests[len(p.requests)-1]
}

// CallCount is how many requests arrived.
func (p *Provider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

// Contexts returns the context of every call, in order.
func (p *Provider) Contexts() []context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]context.Context(nil), p.contexts...)
}

// ContextN is call n's context, or Background when there was none.
func (p *Provider) ContextN(n int) context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 || n >= len(p.contexts) {
		return context.Background()
	}
	return p.contexts[n]
}

// Reset forgets what was received and restarts the script.
func (p *Provider) Reset() *Provider {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests, p.contexts, p.index = nil, nil, 0
	return p
}

// Complete answers one request from the script.
func (p *Provider) Complete(ctx context.Context, req schemaflux.Request) (schemaflux.Response, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.contexts = append(p.contexts, ctx)
	position := p.index
	p.index++
	replyFunc, delay, errs, replies, usage, shaped := p.replyFunc, p.delay, p.errs, p.replies, p.usage, p.shaped
	p.mu.Unlock()

	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return schemaflux.Response{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return schemaflux.Response{}, err
	}
	if position < len(errs) {
		return schemaflux.Response{}, errs[position]
	}
	if len(errs) > 0 && len(replies) == 0 {
		return schemaflux.Response{}, errs[len(errs)-1]
	}
	answer := func(body string) (schemaflux.Response, error) {
		return schemaflux.Response{Content: body, Model: req.Model, FinishReason: "stop", Usage: usage}, nil
	}
	if replyFunc != nil {
		body, err := replyFunc(position, req)
		if err != nil {
			return schemaflux.Response{}, err
		}
		return answer(body)
	}
	if len(replies) > 0 {
		i := position - len(errs)
		if i < 0 {
			i = 0
		}
		if i >= len(replies) {
			i = len(replies) - 1
		}
		return answer(replies[i])
	}
	if shaped {
		if res, err := p.shaper.Complete(ctx, req); err == nil && res.Content != "" {
			return answer(res.Content)
		}
	}
	return answer("{}")
}

// Name is what As set, "local" by default.
func (p *Provider) Name() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name
}

// RetryPolicy says never: a test that scripts one failure sees one request.
// (Zero would mean the library's default of three in v2, which is why this is
// -1 where the v1 fake said 0.)
func (p *Provider) RetryPolicy() (int, time.Duration) { return -1, 0 }

// ModelFor names a model for every tier, so a call that names none still runs.
func (p *Provider) ModelFor(schemaflux.Tier) string { return "test-model" }

// Install makes p the default client's provider for the length of the test.
// The returned function restores the previous default early; it is also
// registered with t.Cleanup.
func Install(t testing.TB, p *Provider) func() {
	t.Helper()
	if p == nil {
		t.Fatalf("sftest: Install needs a provider; call New()")
		return func() {}
	}
	t.Setenv("SCHEMAFLUX_TEST_INSTALLED", "1") // refuses a parallel test: the default is process-wide
	previous := schemaflux.Default()
	schemaflux.SetDefault(schemaflux.Client{Provider: p})
	done := false
	restore := func() {
		if !done {
			done = true
			schemaflux.SetDefault(previous)
		}
	}
	t.Cleanup(restore)
	return restore
}
