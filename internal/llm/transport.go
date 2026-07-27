package llm

import "net/http"

// NewWithTransport returns a client whose outbound calls go through rt.
//
// # Why this exists, and why it is not an endpoint override
//
// `internal/analyze` and `internal/smart` need to exercise the whole Smart+ path
// end to end — consent, payload assembly, the egress audit, the union schema, the
// reply split, the fail-soft fallback — without reaching OpenAI. The obvious way
// is a client pointed at an `httptest` server, and `llm_test.go` rejects it in as
// many words: a test that aims the client somewhere else "designs a hole around
// the allowlist check instead of exercising it".
//
// That reasoning is right and it applies just as much to callers outside this
// package, which previously had no way to honour it — `Client.http` is
// unexported, so the transport trick was available only to tests in this package.
//
// Swapping the TRANSPORT keeps every one of those checks live. `Endpoint` is
// still the constant, `req.URL.Hostname()` is still compared against
// `allowedHost`, the key is still required, the body is still marshalled by the
// real code. A request that a future change addressed anywhere other than
// api.openai.com still fails here, in a test, exactly as it would in production.
//
// It is exported because the alternative — an `Endpoint` that is a variable, or a
// `WithBaseURL` option — is the hole. This one cannot be used to send content
// somewhere else; it can only be used to answer a correctly-addressed request
// without a network.
func NewWithTransport(keyOf KeyFunc, rt http.RoundTripper) *Client {
	c := New(keyOf)
	if rt != nil {
		c.http = &http.Client{Transport: rt, Timeout: requestTimeout}
	}
	return c
}
