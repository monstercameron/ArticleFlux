package smart

import (
	"context"
	"sync"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/schemaflux"
	"github.com/monstercameron/schemaflux/schemafluxtest"
)

// liveProviderName is what the fake calls itself, and it matches production.
//
// It was "local", schemafluxtest's default — and "local" is in SchemaFlux's
// per-provider model table while ArticleFlux's real provider name is not. That
// one difference hid a shipped bug: translate.go named no model, so every call
// resolved one from the table in tests and NONE in production, where the whole
// feature failed with "no default model for provider \"articleflux\"". The unit
// suite was green the entire time.
//
// Naming the fake after the real thing makes a forgotten `.Model(...)` fail
// here, for free, instead of on a reader's machine.
const liveProviderName = "articleflux"

// replying installs a provider answering with the given bodies, in order. The
// last one repeats once the list runs out, so a test that makes an unknown
// number of calls does not have to predict it.
func replying(t *testing.T, bodies ...string) *schemafluxtest.Provider {
	t.Helper()
	prov := schemafluxtest.New().As(liveProviderName).Shaped().Reply(bodies...)
	schemafluxtest.Install(t, prov)
	return prov
}

// failing installs a provider that fails every call with err.
func failing(t *testing.T, err error) *schemafluxtest.Provider {
	t.Helper()
	prov := schemafluxtest.New().As(liveProviderName).Fail(err)
	schemafluxtest.Install(t, prov)
	return prov
}

// requestSent returns everything the provider was asked on call n, system and
// user prompt together.
//
// Both halves, always. Which one carries the brief and which the payload is
// SchemaFlux's business — it deliberately keeps caller steering OUT of the
// system prompt as a trust boundary — so an assertion in this package can only
// honestly be "this text reached the model", never "it reached it as a system
// instruction".
func requestSent(prov *schemafluxtest.Provider, n int) string {
	reqs := prov.Requests()
	if n >= len(reqs) {
		return ""
	}
	return reqs[n].SystemPrompt + reqs[n].UserPrompt
}

// spyOn installs a provider that records the context of every call.
//
// It is `replying` plus context recording, and it exists as its own name only
// because the per-feature timeouts (interestTimeout and friends) are the one
// piece of context handling this package OWNS rather than merely forwards, so a
// test asserting on them wants to say so.
//
// The recording itself is upstream now (SchemaFlux DX-005). This used to be a
// hand-rolled wrapper around the provider, along with a second one for
// request-dependent replies; both were reported and both are methods on
// schemafluxtest.Provider today, so what is left here is a name.
func spyOn(t *testing.T, bodies ...string) *schemafluxtest.Provider {
	t.Helper()
	return replying(t, bodies...)
}

// answering installs a provider that computes each reply from the request it
// was sent, for the batching paths where a fixed list cannot know what to say.
func answering(t *testing.T, fn func(call int, req schemaflux.CompletionRequest) (string, error)) *schemafluxtest.Provider {
	t.Helper()
	prov := schemafluxtest.New().As(liveProviderName).ReplyFunc(fn)
	schemafluxtest.Install(t, prov)
	return prov
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

// OpsModel answers with a fixed, obviously-fake model.
//
// A model name has to arrive at the provider or SchemaFlux refuses the call
// before making it — the provider is named "articleflux" now, which is not in
// the library's per-provider tier table, deliberately (see
// internal/llm/ops.go). That is the loud failure a forgotten `.Model(...)`
// should produce, and the tests that assert on the model want a value they can
// recognise rather than a plausible one.
func (f *fakeLLM) OpsModel(context.Context) string { return "test-model" }

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
