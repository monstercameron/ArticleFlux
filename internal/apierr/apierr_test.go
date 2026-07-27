package apierr

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// §20.7's table, written out. This is a specification, so it is asserted rather
// than described.
func TestTheTaxonomyTable(t *testing.T) {
	cases := []struct {
		kind Kind
		code codes.Code
		http int
	}{
		{KindUnauthenticated, codes.Unauthenticated, 401},
		{KindPermissionDenied, codes.PermissionDenied, 403},
		{KindNotFound, codes.NotFound, 404},
		{KindInvalidArgument, codes.InvalidArgument, 400},
		{KindFailedPrecondition, codes.FailedPrecondition, 409},
		{KindResourceExhausted, codes.ResourceExhausted, 429},
		{KindAborted, codes.Aborted, 409},
		{KindUnavailable, codes.Unavailable, 503},
		{KindInternal, codes.Internal, 500},
	}
	for _, c := range cases {
		if got := c.kind.Code(); got != c.code {
			t.Errorf("kind %d code = %v, want %v", c.kind, got, c.code)
		}
		if got := c.kind.HTTP(); got != c.http {
			t.Errorf("kind %d HTTP = %d, want %d", c.kind, got, c.http)
		}
	}
	// The two that share a status, which no generic gRPC→HTTP mapping produces
	// and which third-party sync clients will act on.
	if KindFailedPrecondition.HTTP() != KindAborted.HTTP() {
		t.Error("FailedPrecondition and Aborted must both be 409")
	}
}

// THE rule. PermissionDenied on item 4711 confirms item 4711 exists, which is a
// tenant-isolation leak with good manners.
func TestCrossTenantIsNotFoundAndIndistinguishableFromAGenuineMiss(t *testing.T) {
	cross := Status(CrossTenant())
	miss := Status(NotFound())

	if status.Code(cross) != codes.NotFound {
		t.Fatalf("cross-tenant access returned %v — it must be NotFound, because "+
			"PermissionDenied confirms the object exists", status.Code(cross))
	}
	if status.Code(cross) != status.Code(miss) {
		t.Error("a cross-tenant miss and a genuine miss have different codes")
	}
	if status.Convert(cross).Message() != status.Convert(miss).Message() {
		t.Errorf("the two messages differ (%q vs %q) — the leak arrives through "+
			"a different door", status.Convert(cross).Message(), status.Convert(miss).Message())
	}
	// And the detail payload, which is the door somebody would forget.
	cd, md := detailOf(t, cross), detailOf(t, miss)
	if cd.GetKey() != md.GetKey() {
		t.Errorf("detail keys differ: %q vs %q", cd.GetKey(), md.GetKey())
	}
	if len(cd.GetArgs()) != 0 {
		t.Errorf("a cross-tenant error carries args %v; anything about the object "+
			"is the thing that must not be said", cd.GetArgs())
	}
}

// The store returns ErrNotFound for another tenant's row — that is the half the
// repositories implement. This asserts the other half survives the transport.
func TestAStoreNotFoundStaysNotFoundAtTheEdge(t *testing.T) {
	err := Status(FromDomain(fmt.Errorf("looking up item: %w", store.ErrNotFound)))
	if status.Code(err) != codes.NotFound {
		t.Errorf("= %v, want NotFound", status.Code(err))
	}
	// Wrapped several layers deep, which is how it really arrives.
	deep := fmt.Errorf("service: %w", fmt.Errorf("repo: %w", store.ErrNotFound))
	if status.Code(Status(FromDomain(deep))) != codes.NotFound {
		t.Error("a wrapped ErrNotFound was not recognised")
	}
}

// An error nobody classified must not reach a client as its own text. The
// failure mode of the alternative is a SQL error on somebody's screen.
func TestAnUnclassifiedErrorBecomesASafeInternal(t *testing.T) {
	leaky := errors.New("sqlite3: no such column: user_item_state.secret_column")
	err := Status(FromDomain(leaky))

	if status.Code(err) != codes.Internal {
		t.Errorf("= %v, want Internal", status.Code(err))
	}
	msg := status.Convert(err).Message()
	if strings.Contains(msg, "sqlite3") || strings.Contains(msg, "secret_column") {
		t.Errorf("the internal error leaked into the message: %q", msg)
	}
	if msg != "internal error" {
		t.Errorf("message = %q, want the fixed safe string", msg)
	}
	// But it is still reachable for the log, which is the whole point of
	// keeping the cause rather than discarding it.
	if !errors.Is(FromDomain(leaky), leaky) {
		t.Error("the cause was discarded; the log now has nothing to say")
	}
}

// A handler that made a deliberate choice keeps it.
func TestAnAlreadyClassifiedErrorIsNotReclassified(t *testing.T) {
	chosen := Precondition("srv.sourceDeactivated", "this feed is no longer polled")
	got := FromDomain(fmt.Errorf("wrapping: %w", chosen))
	if KindOf(got) != KindFailedPrecondition {
		t.Errorf("kind = %d, want FailedPrecondition", KindOf(got))
	}
	if status.Convert(Status(got)).Message() != "this feed is no longer polled" {
		t.Error("the deliberate message was replaced")
	}
}

// A stale cursor is InvalidArgument, never an empty page: an empty page means
// "you reached the end", and a client that believes it shows a truncated list
// with no error anywhere.
func TestAStaleCursorIsAnErrorAndNotAnEmptyPage(t *testing.T) {
	for _, e := range []error{store.ErrBadCursor, store.ErrCursorSpecMismatch} {
		err := Status(FromDomain(e))
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("%v = %v, want InvalidArgument", e, status.Code(err))
		}
		if detailOf(t, err).GetKey() != "srv.staleCursor" {
			t.Errorf("%v carries key %q", e, detailOf(t, err).GetKey())
		}
	}
}

// §20.7's structured detail, so a form can put the message beside the input.
func TestAValidationErrorNamesItsField(t *testing.T) {
	err := Status(Invalid("feed_url", "srv.badURL", "that does not look like a feed address"))
	d := detailOf(t, err)
	if d.GetField() != "feed_url" {
		t.Errorf("field = %q, want feed_url", d.GetField())
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("= %v, want InvalidArgument", status.Code(err))
	}
}

// Without a retry hint every client invents its own backoff, and the ones that
// invent badly turn a rate limit into a hot loop against the overloaded thing.
func TestARateLimitCarriesItsWait(t *testing.T) {
	err := Status(RateLimited("login", 90*time.Second))
	d := detailOf(t, err)
	if d.GetRetryAfterS() != 90 {
		t.Errorf("retry_after_s = %d, want 90", d.GetRetryAfterS())
	}
	if d.GetQuota() != "login" {
		t.Errorf("quota = %q, want login", d.GetQuota())
	}
	if status.Code(err) != codes.ResourceExhausted {
		t.Errorf("= %v, want ResourceExhausted", status.Code(err))
	}
}

// Rounding DOWN produces a hint that expires before the limit does, so a client
// obeying it exactly retries into the same refusal and learns the hint lies.
func TestTheRetryHintRoundsUp(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int32
	}{
		{time.Millisecond, 1},
		{999 * time.Millisecond, 1},
		{time.Second, 1},
		{1001 * time.Millisecond, 2},
		{1500 * time.Millisecond, 2},
		{2 * time.Second, 2},
		{2100 * time.Millisecond, 3},
	}
	for _, c := range cases {
		got := detailOf(t, Status(RateLimited("x", c.in))).GetRetryAfterS()
		if got != c.want {
			t.Errorf("retry hint for %v = %d, want %d", c.in, got, c.want)
		}
	}
	// And zero stays zero: "no useful estimate" is honest, and a client should
	// read it as "back off on your own schedule", not "retry immediately".
	if got := detailOf(t, Status(QuotaExceeded("subscriptions"))).GetRetryAfterS(); got != 0 {
		t.Errorf("a quota with no wait reported retry_after_s = %d", got)
	}
}

// Same code, different remedy: a rate limit is a limit waiting fixes, a quota
// is one it does not. A client has to tell them apart to know whether to offer
// "try again" or "upgrade".
func TestRateLimitsAndQuotasAreDistinguishable(t *testing.T) {
	rate := detailOf(t, Status(RateLimited("login", time.Minute)))
	quota := detailOf(t, Status(QuotaExceeded("subscriptions")))
	if rate.GetKey() == quota.GetKey() {
		t.Errorf("both carry key %q; the client cannot tell 'wait' from 'upgrade'", rate.GetKey())
	}
}

// A25's compare-and-set failure is Aborted, which is the code that means
// "retry the whole read-modify-write" — exactly the remedy.
func TestARevConflictIsAborted(t *testing.T) {
	if status.Code(Status(Conflict())) != codes.Aborted {
		t.Errorf("= %v, want Aborted", status.Code(Status(Conflict())))
	}
}

// The sync API's half of the table.
func TestTheHTTPMappingForTheSyncAPI(t *testing.T) {
	code, body := HTTPStatus(FromDomain(store.ErrNotFound))
	if code != http.StatusNotFound {
		t.Errorf("= %d, want 404", code)
	}
	if body["code"] != "srv.notFound" {
		t.Errorf("body code = %q", body["code"])
	}

	code, body = HTTPStatus(RateLimited("login", 30*time.Second))
	if code != http.StatusTooManyRequests {
		t.Errorf("= %d, want 429", code)
	}
	if body["retry_after_s"] != "30" {
		t.Errorf("retry_after_s = %q, want 30", body["retry_after_s"])
	}

	// And an unclassified error is still safe on this surface.
	_, body = HTTPStatus(errors.New("pq: password authentication failed for user"))
	if strings.Contains(body["message"], "password") {
		t.Errorf("the sync API leaked an internal message: %q", body["message"])
	}

	if code, body := HTTPStatus(nil); code != http.StatusOK || body != nil {
		t.Errorf("HTTPStatus(nil) = %d, %v", code, body)
	}
}

// The whole reason FromAuthz takes `sameTenant` separately: the same refusal is
// honest inside a tenant and a leak across one.
func TestAuthzRefusalsDependOnWhoseObjectItIs(t *testing.T) {
	denied := errors.New("authz: /v1/DeleteFeed requires feeds.manage")

	own := Status(FromAuthz(denied, "feeds.manage", true))
	if status.Code(own) != codes.PermissionDenied {
		t.Errorf("refusing a capability on the caller's OWN object = %v, want "+
			"PermissionDenied — inside a tenant, tell the truth", status.Code(own))
	}
	if detailOf(t, own).GetArgs()["capability"] != "feeds.manage" {
		t.Error("the denial does not name the capability, so the UI cannot explain it")
	}

	other := Status(FromAuthz(denied, "feeds.manage", false))
	if status.Code(other) != codes.NotFound {
		t.Errorf("refusing across tenants = %v, want NotFound", status.Code(other))
	}
	if len(detailOf(t, other).GetArgs()) != 0 {
		t.Error("the cross-tenant refusal named the capability, which says the " +
			"object exists and what it is")
	}
}

// The zero Kind is Internal on purpose: an error nobody classified must not
// inherit a meaning it was never given.
func TestTheZeroKindIsInternal(t *testing.T) {
	var k Kind
	if k != KindInternal {
		t.Error("the zero Kind is not KindInternal")
	}
	if k.Code() != codes.Internal {
		t.Errorf("the zero Kind maps to %v", k.Code())
	}
	// An out-of-range kind too, which is what a future constant added without a
	// table entry would be.
	if Kind(999).Code() != codes.Internal || Kind(999).HTTP() != 500 {
		t.Error("an unmapped Kind does not fall back to Internal")
	}
}

func TestNilIsNotAnError(t *testing.T) {
	if Status(nil) != nil {
		t.Error("Status(nil) returned an error")
	}
	if FromDomain(nil) != nil {
		t.Error("FromDomain(nil) returned an error")
	}
	if FromAuthz(nil, "x", false) != nil {
		t.Error("FromAuthz(nil) returned an error")
	}
}

// Every constructed error must carry a catalog key, or the client falls back to
// English and the i18n work was for nothing.
func TestEveryConstructorCarriesACatalogKey(t *testing.T) {
	errs := map[string]*Error{
		"CrossTenant":      CrossTenant(),
		"NotFound":         NotFound(),
		"Unauthenticated":  Unauthenticated(),
		"PermissionDenied": PermissionDenied("x"),
		"StaleCursor":      StaleCursor(),
		"RateLimited":      RateLimited("x", time.Second),
		"QuotaExceeded":    QuotaExceeded("x"),
		"Conflict":         Conflict(),
		"Internal":         Internal(errors.New("x")),
	}
	for name, e := range errs {
		if e.Key == "" {
			t.Errorf("%s carries no catalog key", name)
		}
		if !strings.HasPrefix(e.Key, "srv.") {
			t.Errorf("%s key %q is not in the srv namespace", name, e.Key)
		}
		if e.Message == "" {
			t.Errorf("%s carries no English fallback", name)
		}
	}
}

// detailOf pulls §20.7's structured detail off a status.
func detailOf(t *testing.T, err error) *pb.ErrorDetail {
	t.Helper()
	for _, d := range status.Convert(err).Details() {
		if ed, ok := d.(*pb.ErrorDetail); ok {
			return ed
		}
	}
	t.Fatalf("no ErrorDetail attached to %v", err)
	return nil
}
