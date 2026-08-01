package llm

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

// Pinned the same way TestEndpointMatchesAllowedHost pins Endpoint: the host
// AND the path, because the host check alone would pass just as happily
// against a path that returns something else entirely.
func TestModelsEndpointMatchesAllowedHost(t *testing.T) {
	u, err := url.Parse(ModelsEndpoint)
	if err != nil {
		t.Fatalf("ModelsEndpoint does not parse: %v", err)
	}
	if u.Hostname() != allowedHost {
		t.Fatalf("ModelsEndpoint host %q is not the allowlisted %q", u.Hostname(), allowedHost)
	}
	if u.Scheme != "https" {
		t.Fatalf("models list would be fetched over %q, not https", u.Scheme)
	}
	if u.Path != "/v1/models" {
		t.Fatalf("ModelsEndpoint path is %q, want /v1/models", u.Path)
	}
}

func TestModelsWithNoKeyReturnsErrNotConfigured(t *testing.T) {
	c, calls := fakeClient("", func(r *http.Request) (*http.Response, error) {
		t.Fatal("a call reached the transport with no key")
		return nil, nil
	})
	if _, err := c.Models(context.Background()); err != ErrNotConfigured {
		t.Fatalf("err = %v, want ErrNotConfigured", err)
	}
	if calls.Load() != 0 {
		t.Error("a request was made with no key configured")
	}
}

// Models filters out the provider's non-text models and returns the rest
// sorted, so the picker gets a stable, relevant list rather than the
// provider's own (frequently changing) ordering with embeddings and
// whisper mixed in.
func TestModelsFiltersAndSorts(t *testing.T) {
	c, _ := fakeClient("sk-test", func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Errorf("Models used %s, want GET", r.Method)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer sk-test"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"data": []map[string]string{
				{"id": "gpt-5-mini"},
				{"id": "text-embedding-3-large"},
				{"id": "whisper-1"},
				{"id": "gpt-5.6-luna"},
				{"id": "tts-1"},
				{"id": "dall-e-3"},
			},
		}), nil
	})
	got, err := c.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	want := []string{"gpt-5-mini", "gpt-5.6-luna"}
	if len(got) != len(want) {
		t.Fatalf("Models() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Models()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestModelsSurfacesTheProviderError(t *testing.T) {
	c, _ := fakeClient("sk-test", func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, map[string]any{
			"error": map[string]string{"message": "Incorrect API key provided"},
		}), nil
	})
	_, err := c.Models(context.Background())
	if err == nil {
		t.Fatal("no error for a 401 response")
	}
	if got := err.Error(); got == "" {
		t.Error("empty error message")
	}
}
