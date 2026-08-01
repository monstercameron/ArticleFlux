package idem

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

const setItemState = "/articleflux.v1.ReaderService/SetItemState"

type fixture struct {
	repo   *store.ReaderRepo
	sc     store.Scope
	ctx    context.Context
	inter  grpc.UnaryServerInterceptor
	info   *grpc.UnaryServerInfo
	called int
}

func setup(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "idem.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewReaderRepo(db)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "T", UserID: "u1", Username: "u", Hash: "x",
		Role: "member", Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}

	f := &fixture{repo: repo, sc: sc, ctx: ctx,
		info: &grpc.UnaryServerInfo{FullMethod: setItemState}}
	f.inter = Unary(repo, func(context.Context) (store.Scope, error) { return sc, nil })
	return f
}

// call runs the interceptor over a handler that counts its invocations and
// returns a response whose rev increases every time — so a replay is
// distinguishable from a second execution by the VALUE, not just the count.
func (f *fixture) call(t *testing.T, req *pb.SetItemStateRequest) (*pb.SetItemStateResponse, error) {
	t.Helper()
	res, err := f.inter(f.ctx, req, f.info,
		func(ctx context.Context, r any) (any, error) {
			f.called++
			return &pb.SetItemStateResponse{Rev: int64(f.called)}, nil
		})
	if err != nil {
		return nil, err
	}
	out, ok := res.(*pb.SetItemStateResponse)
	if !ok {
		t.Fatalf("handler returned %T", res)
	}
	return out, nil
}

// The whole point. A drain that reconnects mid-flight resends, and the second
// attempt must not apply a second time.
func TestAReplayedRequestRunsOnceAndReturnsTheFirstAnswer(t *testing.T) {
	f := setup(t)
	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-1"}

	first, err := f.call(t, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.call(t, req)
	if err != nil {
		t.Fatal(err)
	}

	if f.called != 1 {
		t.Errorf("the handler ran %d times for one key — the mutation applied twice", f.called)
	}
	// Verbatim, not recomputed. A client receiving a DIFFERENT response to the
	// same request cannot tell a replay from a second effect.
	if first.GetRev() != second.GetRev() {
		t.Errorf("replay returned rev %d, first call returned %d — the stored "+
			"response was not replayed verbatim", second.GetRev(), first.GetRev())
	}
}

// A key is chosen by the client, so two different requests can arrive under one:
// a bug, a reused constant, a truncated uuid. Replaying the first answer for the
// second write drops the write AND reports success.
func TestTheSameKeyWithADifferentRequestIsRefused(t *testing.T) {
	f := setup(t)
	if _, err := f.call(t, &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-2"}); err != nil {
		t.Fatal(err)
	}

	_, err := f.call(t, &pb.SetItemStateRequest{ItemId: "i2", IdempotencyKey: "k-2"})
	if err == nil {
		t.Fatal("a different request under the same key was accepted — the write " +
			"was silently dropped and the caller told it succeeded")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("= %v, want FailedPrecondition", got)
	}
	if f.called != 1 {
		t.Errorf("the handler ran %d times; the conflicting call must not execute", f.called)
	}
}

// Protobuf marshalling is not canonical by default. Non-deterministic bytes
// would hash the same request differently on retry, so every replay would come
// back as a spurious conflict — worse than no idempotency, because the write is
// refused and the caller is blamed.
func TestTheRequestHashIsStableAcrossMarshals(t *testing.T) {
	f := setup(t)
	// Two separately constructed but equal messages, which is what a retry
	// actually produces — the client rebuilds the message, it is not the same
	// object.
	a := &pb.SetItemStateRequest{ItemId: "i9", IdempotencyKey: "k-3", Read: ptr(true)}
	b := &pb.SetItemStateRequest{ItemId: "i9", IdempotencyKey: "k-3", Read: ptr(true)}

	if _, err := f.call(t, a); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(t, b); err != nil {
		t.Fatalf("an identical retry was refused as a conflict: %v", err)
	}
	if f.called != 1 {
		t.Errorf("the handler ran %d times", f.called)
	}
}

// A read has nothing to replay, and forcing a key onto every call would make
// the table a log of every request the instance ever served.
func TestCallsWithoutAKeyPassStraightThrough(t *testing.T) {
	f := setup(t)

	// Same message, no key: runs every time.
	for i := 0; i < 3; i++ {
		if _, err := f.call(t, &pb.SetItemStateRequest{ItemId: "i1"}); err != nil {
			t.Fatal(err)
		}
	}
	if f.called != 3 {
		t.Errorf("the handler ran %d times for three unkeyed calls, want 3", f.called)
	}

	// A message type with no key field at all.
	res, err := f.inter(f.ctx, &pb.ListFeedsRequest{},
		&grpc.UnaryServerInfo{FullMethod: "/articleflux.v1.ReaderService/ListFeeds"},
		func(ctx context.Context, r any) (any, error) {
			return &pb.ListFeedsResponse{TotalUnread: 7}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.(*pb.ListFeedsResponse).GetTotalUnread() != 7 {
		t.Error("an unkeyed method was not passed through")
	}
}

// Storing failures would make a transient failure permanent for 24 hours: the
// client retries the same key — which is what a client with a queued mutation
// does — and gets the stored failure back forever.
func TestAFailureIsNotStoredSoARetryCanSucceed(t *testing.T) {
	f := setup(t)
	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-4"}

	boom := errors.New("the database was busy")
	_, err := f.inter(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		return nil, boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("= %v, want the handler's error", err)
	}

	// The retry, same key, now succeeding.
	out, err := f.call(t, req)
	if err != nil {
		t.Fatalf("a retry after a transient failure was refused: %v", err)
	}
	if out.GetRev() != 1 {
		t.Errorf("rev = %d; the retry did not actually run", out.GetRev())
	}
}

// Keys are per user. One reader's key must not answer another's request — that
// would be a cross-tenant response replay, which is worse than a leak: it is
// somebody else's data returned as your own.
func TestKeysAreScopedToTheirUser(t *testing.T) {
	f := setup(t)
	if err := f.repo.AddUser(f.ctx, store.NewTenant{
		TenantID: "t1", UserID: "u2", Username: "other", Hash: "x", Role: "member",
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}

	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "shared-key"}
	if _, err := f.call(t, req); err != nil {
		t.Fatal(err)
	}

	// The same key, the same request, a different user.
	other := Unary(f.repo, func(context.Context) (store.Scope, error) {
		return store.Scope{TenantID: "t1", UserID: "u2", Role: "member"}, nil
	})
	ran := false
	res, err := other(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		ran = true
		return &pb.SetItemStateResponse{Rev: 99}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("another user's key replayed a response they never asked for")
	}
	if res.(*pb.SetItemStateResponse).GetRev() != 99 {
		t.Error("the second user got the first user's stored response")
	}
}

// The interceptor never sees the response type on a replay, because it does not
// call the handler. It resolves it from the protobuf registry, and this asserts
// that resolution actually works for a real method rather than silently falling
// back to re-running.
func TestTheStoredResponseIsDecodedIntoItsRealType(t *testing.T) {
	f := setup(t)
	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-5"}

	if _, err := f.inter(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		return &pb.SetItemStateResponse{
			Rev:  42,
			Item: &pb.Item{Id: "i1", Title: "A stored title"},
		}, nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := f.inter(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		t.Fatal("the handler ran on a replay")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, ok := res.(*pb.SetItemStateResponse)
	if !ok {
		t.Fatalf("replay returned %T, not the method's declared response type", res)
	}
	if out.GetRev() != 42 || out.GetItem().GetTitle() != "A stored title" {
		t.Errorf("the nested message did not survive the round trip: %+v", out)
	}
}

// A drain reconnecting mid-flight can send the same key twice at once.
//
// # What this asserts, and what it deliberately does not
//
// It does NOT assert one execution. Two callers presenting the same key at the
// same instant are the SAME request, and the second one has no answer to be
// given yet — `BeginIdempotent` says so in as many words: reporting a conflict
// would be wrong and replaying nothing would be wrong, so it proceeds.
// Serialising them would mean holding the second caller on a lock for the
// duration of the first mutation, which turns a rare double-apply into a
// routine head-of-line stall on the single writer.
//
// An earlier version of this test asserted "exactly 1" and passed five times in
// a row before failing on the sixth. That is worse than a wrong assertion: it is
// a wrong assertion that looks confirmed. What actually makes concurrent
// duplicates safe is the property the outbox already relies on — every queued
// mutation writes an ABSOLUTE value, so applying it twice lands on the same
// state (§20.19.8).
//
// So: both callers get an answer, neither is told they conflicted, and the
// replay guarantee that IS made — sequential retries run once — is asserted by
// TestAReplayedRequestRunsOnceAndReturnsTheFirstAnswer.
func TestConcurrentDuplicatesBothGetAnAnswerAndNeitherConflicts(t *testing.T) {
	f := setup(t)
	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-race"}

	var mu sync.Mutex
	runs := 0
	handler := func(ctx context.Context, r any) (any, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		return &pb.SetItemStateResponse{Rev: 1}, nil
	}

	const racers = 6
	var wg sync.WaitGroup
	errs := make([]error, racers)
	wg.Add(racers)
	for i := 0; i < racers; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = f.inter(f.ctx, req, f.info, handler)
		}(i)
	}
	wg.Wait()

	if runs < 1 {
		t.Error("the handler never ran; the interceptor refused everything")
	}
	if runs > racers {
		t.Errorf("the handler ran %d times for %d callers", runs, racers)
	}
	// A conflict would be wrong: these are the SAME request, so a caller that
	// loses the race must still get an answer rather than an error.
	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d failed: %v", i, err)
		}
		if err != nil && status.Code(err) == codes.FailedPrecondition {
			t.Errorf("racer %d was told it conflicted with an identical request", i)
		}
	}

	// And once the dust settles, the key is spent: a LATER retry replays rather
	// than running again. That is the guarantee that matters for a drained
	// outbox, and it is the one this can actually promise.
	before := runs
	if _, err := f.inter(f.ctx, req, f.info, handler); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	after := runs
	mu.Unlock()
	if after != before {
		t.Errorf("a retry after the concurrent burst ran the handler again "+
			"(%d -> %d); the key was never recorded", before, after)
	}
}

// An unauthenticated call is a case Unary deliberately does not adjudicate:
// the handler decides what an unauthenticated caller gets told, so scopeOf
// erroring must still reach the handler rather than being swallowed here.
func TestScopeResolutionFailurePassesThrough(t *testing.T) {
	f := setup(t)
	inter := Unary(f.repo, func(context.Context) (store.Scope, error) {
		return store.Scope{}, errors.New("unauthenticated")
	})
	called := false
	_, err := inter(f.ctx, &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-noauth"}, f.info,
		func(ctx context.Context, r any) (any, error) {
			called = true
			return &pb.SetItemStateResponse{}, nil
		})
	if err != nil {
		t.Fatalf("Unary surfaced the scope error itself: %v", err)
	}
	if !called {
		t.Error("a scopeOf failure did not reach the handler")
	}
}

// keyed is satisfied by the generated getter alone — a hand-written type
// can implement it without being a real protobuf message. Unary must not
// assume every keyed request is also marshalable.
type notAProtoMessage struct{}

func (notAProtoMessage) GetIdempotencyKey() string { return "k" }

func TestNonProtoRequestPassesThrough(t *testing.T) {
	f := setup(t)
	called := false
	_, err := f.inter(f.ctx, notAProtoMessage{}, f.info, func(ctx context.Context, r any) (any, error) {
		called = true
		return &pb.SetItemStateResponse{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("a keyed-but-non-proto request was not passed straight through")
	}
}

// A handler's result is not always a proto.Message in principle — CompleteIdempotent
// only knows how to store one — and skipping the store must not be mistaken for
// an error: the call still succeeds and returns the real result.
func TestNonProtoResponseIsReturnedButNotReplayable(t *testing.T) {
	f := setup(t)
	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-nonproto"}

	res, err := f.inter(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		return "not a protobuf message", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res != "not a protobuf message" {
		t.Errorf("res = %v, want the handler's literal return", res)
	}

	// Nothing storable was recorded, so a second call under the same key must
	// run the handler again rather than replaying an answer that was never saved.
	called := false
	if _, err := f.inter(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		called = true
		return "second", nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("a second call under the same key replayed, but nothing proto-shaped was ever stored")
	}
}

// The fallback path: a stored response that no longer decodes (a message
// shape that changed across a deploy) must re-run the handler rather than
// fail the retry outright — a client stuck retrying a key it cannot change
// needs SOME answer.
func TestUndecodableStoredResponseFallsBackToRunningTheHandler(t *testing.T) {
	f := setup(t)
	req := &pb.SetItemStateRequest{ItemId: "i1", IdempotencyKey: "k-corrupt"}
	reqBytes, err := marshal.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// A byte with field number 0 is not a legal protobuf tag under any wire
	// type, so Unmarshal is guaranteed to reject it rather than silently
	// accepting it as an empty message.
	if err := f.repo.CompleteIdempotent(f.ctx, f.sc, "k-corrupt", setItemState,
		reqBytes, []byte{0x00}, 0); err != nil {
		t.Fatal(err)
	}

	called := false
	if _, err := f.inter(f.ctx, req, f.info, func(ctx context.Context, r any) (any, error) {
		called = true
		return &pb.SetItemStateResponse{Rev: 9}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("an undecodable stored response did not fall back to re-running the handler")
	}
}

// decode is exercised directly for the branches Unary's happy paths never
// reach: a replay only ever asks it about a method that just ran
// successfully, so a malformed or unknown method name never occurs through
// the interceptor itself.
func TestDecodeRejectsAMalformedFullMethod(t *testing.T) {
	if _, err := decode("no-slash-at-all", nil); err == nil {
		t.Error("a full method with no /service/method separator was accepted")
	}
}

func TestDecodeRejectsAnUnregisteredService(t *testing.T) {
	if _, err := decode("/no.such.Service/Method", nil); err == nil {
		t.Error("an unregistered service name was accepted")
	}
}

// The registry has no notion of "service vs. message" until asked — a full
// method whose service segment names a MESSAGE, not a service, must be
// rejected rather than type-asserted into a panic.
func TestDecodeRejectsANonServiceDescriptor(t *testing.T) {
	if _, err := decode("/articleflux.v1.SetItemStateRequest/Foo", nil); err == nil {
		t.Error("a message's full name was accepted as a service descriptor")
	}
}

func TestDecodeRejectsAnUnknownMethodName(t *testing.T) {
	if _, err := decode(setItemState+"Bogus", nil); err == nil {
		t.Error("an unregistered method name on a real service was accepted")
	}
}

func TestDecodeRejectsUnparseableBytes(t *testing.T) {
	if _, err := decode(setItemState, []byte{0x00}); err == nil {
		t.Error("garbage response bytes were accepted as a valid message")
	}
}

func TestErrBadMethodNamesTheMethodInItsMessage(t *testing.T) {
	err := errBadMethod{"/weird.Service/Method"}
	if !strings.Contains(err.Error(), "/weird.Service/Method") {
		t.Errorf("Error() = %q, does not name the offending method", err.Error())
	}
}

func ptr[T any](v T) *T { return &v }
