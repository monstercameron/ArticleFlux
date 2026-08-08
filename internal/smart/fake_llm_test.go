package smart

import (
	"context"
	"sync"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// ctxSpy wraps a schemafluxtest provider and records the context each call
// arrived on.
//
// schemafluxtest records REQUESTS but not contexts, which is the right default —
// a context is not part of a prompt. This package needs them anyway: the
// per-feature timeouts (interestTimeout and friends) are the one piece of
// context handling this package OWNS rather than merely forwards, and the only
// place they are observable is the context the provider is handed. Asserting on
// a deadline the caller set would prove nothing.
type ctxSpy struct {
	*schemafluxtest.Provider

	mu   sync.Mutex
	ctxs []context.Context
}

// installProvider makes p the provider every operation in this process runs
// against, and restores the previous one when the test ends.
//
// This is what `schemafluxtest.Install` does; it exists separately only because
// Install takes the concrete `*schemafluxtest.Provider` and the two doubles
// below are wrappers around one.
func installProvider(t *testing.T, p schemaflux.Provider) {
	t.Helper()
	previous := schemaflux.GetDefaultClient()
	t.Setenv("SCHEMAFLUX_API_KEY", "articleflux-test-not-a-real-key")
	schemaflux.SetDefaultClient(schemaflux.NewClient("articleflux-test-not-a-real-key").WithProviderInstance(p))
	t.Cleanup(func() { schemaflux.SetDefaultClient(previous) })
}

// replying installs a provider answering with the given bodies, in order. The
// last one repeats once the list runs out, so a test that makes an unknown
// number of calls does not have to predict it.
func replying(t *testing.T, bodies ...string) *schemafluxtest.Provider {
	t.Helper()
	prov := schemafluxtest.New().Shaped().Reply(bodies...)
	schemafluxtest.Install(t, prov)
	return prov
}

// failing installs a provider that fails every call with err.
func failing(t *testing.T, err error) *schemafluxtest.Provider {
	t.Helper()
	prov := schemafluxtest.New().Fail(err)
	schemafluxtest.Install(t, prov)
	return prov
}

// recorder is anything that remembers what it was asked: the schemafluxtest
// provider, and the two doubles below that wrap one.
type recorder interface {
	Requests() []schemaflux.CompletionRequest
}

// requestSent returns everything the provider was asked on call n, system and
// user prompt together.
//
// Both halves, always. Which one carries the brief and which the payload is
// SchemaFlux's business — it deliberately keeps caller steering OUT of the
// system prompt as a trust boundary — so an assertion in this package can only
// honestly be "this text reached the model", never "it reached it as a system
// instruction".
func requestSent(r recorder, n int) string {
	reqs := r.Requests()
	if n >= len(reqs) {
		return ""
	}
	return reqs[n].SystemPrompt + reqs[n].UserPrompt
}

// spyOn installs a context-recording provider answering with the given bodies.
func spyOn(t *testing.T, bodies ...string) *ctxSpy {
	t.Helper()
	s := &ctxSpy{Provider: schemafluxtest.New().Shaped().Reply(bodies...)}
	installProvider(t, s)
	return s
}

// answering installs a provider that computes each reply from the request it
// was sent.
//
// `schemafluxtest.Reply` scripts a fixed list, which is the right tool when the
// answer does not depend on the question. Translation batching is the case
// where it does: Catalog splits a catalogue into batches and the reply to each
// one has to name the keys THAT batch asked for, and a fixed list cannot know
// which those are without duplicating the batching logic in the fixture.
type fnSpy struct {
	*schemafluxtest.Provider

	fn func(call int, req schemaflux.CompletionRequest) (string, error)

	mu    sync.Mutex
	calls int
	reqs  []schemaflux.CompletionRequest
}

func answering(t *testing.T, fn func(call int, req schemaflux.CompletionRequest) (string, error)) *fnSpy {
	t.Helper()
	s := &fnSpy{Provider: schemafluxtest.New(), fn: fn}
	installProvider(t, s)
	return s
}

func (s *fnSpy) Complete(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	// The cancellation check comes first, and before fn is consulted: a
	// cancelled call must be reported as one whatever the fixture would have
	// answered, which is the same order schemafluxtest itself uses.
	if err := ctx.Err(); err != nil {
		return schemaflux.CompletionResponse{}, err
	}
	s.mu.Lock()
	call := s.calls
	s.calls++
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()

	body, err := s.fn(call, req)
	if err != nil {
		return schemaflux.CompletionResponse{}, err
	}
	return schemaflux.CompletionResponse{
		Content:      body,
		Model:        req.Model,
		Provider:     s.Provider.Name(),
		FinishReason: "stop",
	}, nil
}

// CallCount counts what fnSpy answered, not what the embedded provider did —
// the embedded one never sees a call, because Complete above does not delegate.
func (s *fnSpy) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// Requests overrides the embedded provider's for the same reason CallCount
// does: these are the requests that were actually answered.
func (s *fnSpy) Requests() []schemaflux.CompletionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]schemaflux.CompletionRequest(nil), s.reqs...)
}

func (s *ctxSpy) Complete(ctx context.Context, req schemaflux.CompletionRequest) (schemaflux.CompletionResponse, error) {
	s.mu.Lock()
	s.ctxs = append(s.ctxs, ctx)
	s.mu.Unlock()
	return s.Provider.Complete(ctx, req)
}

// ctxN returns the context of the i-th call, or a background context if the
// call never happened — so a caller reads a missing deadline rather than
// panicking, and the assertion reports the real failure.
func (s *ctxSpy) ctxN(i int) context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i >= len(s.ctxs) {
		return context.Background()
	}
	return s.ctxs[i]
}

// fakeLLM is a test double for the llmClient seam defined in llmclient.go.
//
// It exists to unlock exactly the coverage this task is about: every method in
// this package that reaches a.llm.Do() previously had no way to be tested
// without either skipping (see live_test.go) or making a real, billable call to
// api.openai.com. This fake never touches a socket — Do() is answered entirely
// in-process by reply/text/err below — so a test using it can safely exercise
// the success path, malformed output, provider errors, and cancellation without
// any risk of egress.
type fakeLLM struct {
	mu         sync.Mutex
	configured bool

	// calls and ctxs are captured in call order, so a test can assert not just
	// the final answer but HOW MANY times the model was asked and with what
	// context — the thing a retry/budget bug would get wrong while the happy
	// path still looks fine.
	calls []llm.Request
	ctxs  []context.Context

	// reply is consulted first, given the 0-based index of this call, so a test
	// can script a sequence (e.g. "fail on the first attempt, succeed on the
	// second"). If reply is nil, every call returns text/err.
	reply func(call int, r llm.Request) (string, error)
	text  string
	err   error

	// opsContexts counts how many times a feature asked for an operation
	// context. It is the only observable a typed operation leaves on this seam:
	// the call itself goes to whichever provider schemafluxtest installed, not
	// through Do, so "did this feature use the client it was given" has to be
	// asked here instead.
	opsContexts int
}

func (f *fakeLLM) Configured(context.Context) bool { return f.configured }

// OpsContext returns the context untouched, deliberately.
//
// The real client's version puts ArticleFlux's own provider on the context,
// which SchemaFlux prefers over anything registered globally — and that would
// defeat `schemafluxtest.Install`, whose whole job is to be the provider a test
// gets. Leaving the context alone is what lets the installed fake answer.
func (f *fakeLLM) OpsContext(ctx context.Context) context.Context {
	f.mu.Lock()
	f.opsContexts++
	f.mu.Unlock()
	return ctx
}

func (f *fakeLLM) Do(ctx context.Context, r llm.Request) (string, error) {
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, r)
	f.ctxs = append(f.ctxs, ctx)
	f.mu.Unlock()
	if f.reply != nil {
		return f.reply(n, r)
	}
	return f.text, f.err
}

// callCount is how many times Do actually ran, which is what the retry/budget
// tests are for: the final return value can be right for the wrong reason (a
// bug that retries forever but happens to succeed eventually looks identical
// to correct code unless something counts the calls).
func (f *fakeLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeLLM) callN(i int) llm.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[i]
}

func (f *fakeLLM) ctxN(i int) context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ctxs[i]
}
