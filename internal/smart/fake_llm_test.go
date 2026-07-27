package smart

import (
	"context"
	"sync"

	"github.com/monstercameron/ArticleFlux/internal/llm"
)

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
}

func (f *fakeLLM) Configured(context.Context) bool { return f.configured }

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
