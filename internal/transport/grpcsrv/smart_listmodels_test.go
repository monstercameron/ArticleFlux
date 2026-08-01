package grpcsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// roundTripFunc and jsonResponse mirror internal/llm's own test helpers —
// this package needs the same fake-transport trick to exercise ListModels'
// live provider call without reaching the network, and llm.NewWithTransport
// is what makes that possible from outside internal/llm (see its own doc
// comment for why that seam is safe rather than a hole in the allowlist).
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body any) *http.Response {
	b, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

// listModelsServer is smartConfigServer's shape, but with an llm.Client this
// file controls the transport of — smartConfigServer's is deliberately
// unconfigured (empty key), which is right for every OTHER test in this
// package and wrong for exercising ListModels' provider call.
func listModelsServer(t *testing.T, sc store.Scope, rt http.RoundTripper) *SmartServer {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "smart.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.NewReaderRepo(db).CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: sc.TenantID, Name: "Test", UserID: sc.UserID,
		Username: "owner", Hash: "x", Role: sc.Role,
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	settings := store.NewSettingsRepo(db, nil)
	client := llm.NewWithTransport(func(context.Context) string { return "sk-test" }, rt)
	return NewSmartServer(settings, client, smart.NewTranslator(client, settings),
		fixedScope(sc), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestListModelsDeniedForNonOwner(t *testing.T) {
	sc := ownerScope("member")
	s := listModelsServer(t, sc, nil)
	_, err := s.ListModels(context.Background(), &pb.ListModelsRequest{})
	if codeOf(err) != codes.PermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", codeOf(err))
	}
}

// No key at all (smartConfigServer's ordinary, unconfigured client): the
// picker still has to report the default and whatever was saved, because a
// picker that goes blank without a key is worse than one that cannot list.
func TestListModelsUnconfiguredReturnsCurrentAndDefaultOnly(t *testing.T) {
	sc := ownerScope("admin")
	s, _ := smartConfigServer(t, nil, sc, fixedScope(sc))
	res, err := s.ListModels(context.Background(), &pb.ListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(res.GetModels()) != 0 {
		t.Errorf("Models = %v, want empty with no key configured", res.GetModels())
	}
	if res.GetDefaultModel() == "" {
		t.Error("DefaultModel is empty")
	}
}

func TestListModelsReturnsProviderListAndSavedCurrent(t *testing.T) {
	sc := ownerScope("admin")
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("request path = %q, want /v1/models", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"data": []map[string]string{{"id": "gpt-5-mini"}, {"id": "gpt-5.6-luna"}},
		}), nil
	})
	s := listModelsServer(t, sc, rt)

	if _, err := s.SetSmartConfig(context.Background(),
		&pb.SetSmartConfigRequest{Model: "gpt-5.6-luna"}); err != nil {
		t.Fatalf("SetSmartConfig: %v", err)
	}

	res, err := s.ListModels(context.Background(), &pb.ListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if res.GetCurrent() != "gpt-5.6-luna" {
		t.Errorf("Current = %q, want the saved model", res.GetCurrent())
	}
	want := []string{"gpt-5-mini", "gpt-5.6-luna"}
	if len(res.GetModels()) != len(want) {
		t.Fatalf("Models = %v, want %v", res.GetModels(), want)
	}
	for i, m := range want {
		if res.GetModels()[i] != m {
			t.Errorf("Models[%d] = %q, want %q", i, res.GetModels()[i], m)
		}
	}
}

// A provider outage on the listing call must not take the whole screen down
// with it — see ListModels' own comment.
func TestListModelsFailsSoftOnProviderError(t *testing.T) {
	sc := ownerScope("admin")
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, map[string]any{
			"error": map[string]string{"message": "boom"},
		}), nil
	})
	s := listModelsServer(t, sc, rt)
	res, err := s.ListModels(context.Background(), &pb.ListModelsRequest{})
	if err != nil {
		t.Fatalf("ListModels returned an RPC error rather than failing soft: %v", err)
	}
	if len(res.GetModels()) != 0 {
		t.Errorf("Models = %v, want empty on a provider error", res.GetModels())
	}
	if res.GetDefaultModel() == "" {
		t.Error("DefaultModel is empty even though the provider call failed")
	}
}
