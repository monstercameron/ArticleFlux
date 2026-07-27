package app

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/metadata"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// TestIdemPrecedesTheLatencyTimerOnTheRealServer pins the third documented
// ordering property: idem sits ahead of the per-call latency/tracing
// interceptor, so a replay — which idem answers WITHOUT calling the next
// interceptor in the chain — is never recorded as a call at all. If the two
// were swapped, the latency interceptor would run first on every call
// including a replay, and a drained outbox would show up as an extra,
// suspiciously fast sample on a method that did no work — exactly what the
// comment above buildHandler's chain warns against.
//
// The proof is a real RPC, twice, through the real tunnel and the real
// chain, read back through the same *obs.Latency buildHandler wires into the
// tracing interceptor (a.lat). A fake handler could assert the interceptor's
// own behaviour (idem_test.go already does, thoroughly); it cannot assert
// that THIS is the interceptor sitting in front of THAT one in the assembled
// server.
func TestIdemPrecedesTheLatencyTimerOnTheRealServer(t *testing.T) {
	a, err := Open(t.Context(), Config{
		DBPath:       filepath.Join(t.TempDir(), "test.db"),
		PollInterval: 0,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	const username = "cam"
	const password = "correct-horse-battery-staple"
	hash, err := secret.HashPassword(password, secret.DefaultParams)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := a.Repo().CreateTenantAndUser(t.Context(), store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: username,
		Hash: hash, Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("CreateTenantAndUser: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}

	// Seed one real item directly on the repo — the fixture, not the thing
	// under test. SetItemState needs a row that actually exists so the first
	// call SUCCEEDS and leaves something for a true replay to answer from;
	// idem never stores a failure (idem.go), so a call against a missing item
	// would never become a genuine replay on the second attempt.
	feed, _, err := a.Repo().Subscribe(t.Context(), sc, store.NewSubscription{
		NaturalKey: "feed:chain-order", FeedURL: "https://chain-order.example/feed",
		Title: "Chain order fixture",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := a.Repo().IngestItems(t.Context(), feed.SourceID, []store.IngestItem{{
		GUID: "g1", URL: "https://chain-order.example/1", DupeKey: "d1",
		Title: "Fixture article", ContentHTML: "<p>x</p>", PublishedAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	items, _, err := a.Repo().ListItems(t.Context(), sc, store.ListQuery{Limit: 10})
	if err != nil || len(items) == 0 {
		t.Fatalf("seed list: %v (%d items)", err, len(items))
	}
	itemID := items[0].ID

	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	url := "ws://" + strings.TrimPrefix(srv.URL, "http://") + "/grpc"
	conn := dialFor(t, url)
	authClient := pb.NewAuthServiceClient(conn)
	login, err := authClient.Login(t.Context(), &pb.LoginRequest{
		Username: username, Password: password,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	authed := metadata.AppendToOutgoingContext(t.Context(), "authorization", "Bearer "+login.GetToken())

	readerClient := pb.NewReaderServiceClient(conn)
	callsFor := func(method string) int64 {
		for _, s := range a.lat.Snapshot() {
			if s.Method == method {
				return s.Calls
			}
		}
		return 0
	}

	before := callsFor("SetItemState")

	first, err := readerClient.SetItemState(authed, &pb.SetItemStateRequest{
		ItemId: itemID, Read: boolPtr(true), IdempotencyKey: "chain-order-key-1",
	})
	if err != nil {
		t.Fatalf("first SetItemState: %v", err)
	}
	afterFirst := callsFor("SetItemState")
	if afterFirst != before+1 {
		t.Fatalf("a first, non-replayed call changed the call count by %d, want 1 — the "+
			"latency interceptor is not observing ordinary calls, so a replay's absence "+
			"below would prove nothing", afterFirst-before)
	}

	// The replay: same key, same body. idem must answer from the store WITHOUT
	// calling the next interceptor — so the count must not move.
	second, err := readerClient.SetItemState(authed, &pb.SetItemStateRequest{
		ItemId: itemID, Read: boolPtr(true), IdempotencyKey: "chain-order-key-1",
	})
	if err != nil {
		t.Fatalf("replayed SetItemState: %v", err)
	}
	if second.GetRev() != first.GetRev() {
		t.Errorf("replay returned rev %d, first call returned %d — this was not answered "+
			"from the stored response, so the count assertion below is not testing a replay",
			second.GetRev(), first.GetRev())
	}
	afterReplay := callsFor("SetItemState")
	if afterReplay != afterFirst {
		t.Errorf("the replay changed SetItemState's recorded call count from %d to %d. "+
			"idem must sit ahead of the latency timer so a replay — which does no work — "+
			"is never recorded as a call; a swapped order would show exactly this: the "+
			"drained replay counted as a suspiciously fast sample", afterFirst, afterReplay)
	}

	// Control: a genuinely different key DOES do work and must be counted, so
	// the assertion above is not merely "the counter never moves".
	if _, err := readerClient.SetItemState(authed, &pb.SetItemStateRequest{
		ItemId: itemID, Starred: boolPtr(true), IdempotencyKey: "chain-order-key-2",
	}); err != nil {
		t.Fatalf("second, distinct SetItemState: %v", err)
	}
	afterSecondReal := callsFor("SetItemState")
	if afterSecondReal != afterReplay+1 {
		t.Errorf("a second, genuinely distinct call changed the count by %d, want 1 — "+
			"the counter is not live, so the replay result above is inconclusive",
			afterSecondReal-afterReplay)
	}
}
