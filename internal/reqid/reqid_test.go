package reqid

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// capture returns a logger writing JSON into buf, through the reqid handler.
func capture() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(NewHandler(base)), buf
}

func fields(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}
	// Last record only.
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, line)
	}
	return m
}

// The point of the handler: hundreds of log calls, none of which pass the id,
// and it is there anyway. Asking each call site would mean it is missing from
// whichever ones somebody forgot — reliably the error paths that matter.
func TestTheIDArrivesWithoutTheCallSitePassingIt(t *testing.T) {
	log, buf := capture()
	ctx := With(context.Background(), "abc123")

	log.InfoContext(ctx, "something happened", "feed", "example")

	f := fields(t, buf)
	if f[Key] != "abc123" {
		t.Errorf("%s = %v, want abc123", Key, f[Key])
	}
	// And the call site's own attributes survive.
	if f["feed"] != "example" {
		t.Errorf("the handler dropped the call's own attributes: %v", f)
	}
}

// A log line with no request in flight — boot, the poller, a signal handler —
// must not carry an empty field pretending to be one.
func TestNoContextMeansNoField(t *testing.T) {
	log, buf := capture()
	log.InfoContext(context.Background(), "booting")

	if _, ok := fields(t, buf)[Key]; ok {
		t.Error("a log line with no request in flight carried a request_id")
	}
}

// The queue boundary, which is the half that is usually missed: most of what
// this application does for a reader happens later, on a worker.
func TestAJobCarriesBothItsOwnIDAndItsOrigin(t *testing.T) {
	log, buf := capture()

	// The RPC.
	rpc := With(context.Background(), "rpc-1")
	// The worker picks up the job it queued: fresh id, origin restored.
	job := WithOrigin(With(context.Background(), "job-1"), Origin(WithOrigin(rpc, From(rpc))))

	log.InfoContext(job, "fanned out")
	f := fields(t, buf)

	if f[Key] != "job-1" {
		t.Errorf("%s = %v, want the job's own id", Key, f[Key])
	}
	if f[OriginKey] != "rpc-1" {
		t.Errorf("%s = %v, want the queuing request's id", OriginKey, f[OriginKey])
	}
	// Two ids, not one. Reusing the originating id would make every job a
	// user's action fanned out into indistinguishable from the RPC and from
	// each other.
	if f[Key] == f[OriginKey] {
		t.Error("the job reused its origin's id, so 'this job' and 'everything " +
			"that request caused' are the same query and neither works")
	}
}

// Scheduler work no request asked for must not claim an origin.
func TestWorkNobodyAskedForHasNoOrigin(t *testing.T) {
	log, buf := capture()
	ctx := WithOrigin(With(context.Background(), "poll-1"), "")

	log.InfoContext(ctx, "polling")
	f := fields(t, buf)
	if _, ok := f[OriginKey]; ok {
		t.Error("scheduler work claimed an originating request, which would make " +
			"the log say a user asked for something nobody asked for")
	}
	if f[Key] != "poll-1" {
		t.Errorf("%s = %v", Key, f[Key])
	}
}

// The id identifies the RECORD. Nesting it under a group would put the same
// field at different paths depending on where the log call happened, and a
// field whose path varies cannot be filtered on.
func TestTheIDIsNotNestedUnderAGroup(t *testing.T) {
	log, buf := capture()
	ctx := With(context.Background(), "grouped")

	log.WithGroup("job").InfoContext(ctx, "working", "kind", "fanout")

	f := fields(t, buf)
	if f[Key] != "grouped" {
		t.Errorf("%s is not at the top level: %v", Key, f)
	}
	// The call's own attributes DO go in the group, which is what the group is
	// for — this asserts the handler did not flatten everything.
	group, ok := f["job"].(map[string]any)
	if !ok || group["kind"] != "fanout" {
		t.Errorf("the group was lost: %v", f)
	}
}

// WithAttrs must not lose the wrapper, or a logger built once with attrs and
// used everywhere silently stops carrying ids.
func TestWithAttrsKeepsTheWrapper(t *testing.T) {
	log, buf := capture()
	scoped := log.With("component", "poller")

	scoped.InfoContext(With(context.Background(), "kept"), "tick")

	f := fields(t, buf)
	if f[Key] != "kept" {
		t.Error("a logger built with .With lost the request id")
	}
	if f["component"] != "poller" {
		t.Errorf("the bound attribute was lost: %v", f)
	}
}

// The id is read over the phone, out of a bug report, by a person.
func TestGeneratedIDsAreShortDistinctAndReadable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		id := New()
		if len(id) != 16 {
			t.Fatalf("%q is %d characters; 64 bits of hex is 16", id, len(id))
		}
		if strings.ContainsAny(id, "ghijklmnopqrstuvwxyz-_+/=") {
			t.Fatalf("%q is not plain hex; it has to be readable aloud", id)
		}
		if seen[id] {
			t.Fatal("two generated ids collided within 2000 draws")
		}
		seen[id] = true
	}
}

func TestWithMintsWhenGivenNothing(t *testing.T) {
	ctx := With(context.Background(), "")
	if From(ctx) == "" {
		t.Error("With(ctx, \"\") left the context without an id")
	}
	if From(context.Background()) != "" {
		t.Error("a bare context reported an id")
	}
	if Origin(context.Background()) != "" {
		t.Error("a bare context reported an origin")
	}
}

// Two loggers derived from one parent must not corrupt each other. Sharing the
// ops slice's backing array makes the second derivation overwrite the first's
// group — a bug that appears only once somebody derives twice, which is exactly
// when nobody is looking for it.
func TestTwoDerivationsFromOneParentDoNotShareState(t *testing.T) {
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	parent := slog.New(NewHandler(base))

	a := parent.WithGroup("alpha")
	b := parent.WithGroup("beta")

	ctx := With(context.Background(), "shared")

	a.InfoContext(ctx, "one", "k", "va")
	fa := fields(t, buf)
	buf.Reset()
	b.InfoContext(ctx, "two", "k", "vb")
	fb := fields(t, buf)

	ga, _ := fa["alpha"].(map[string]any)
	gb, _ := fb["beta"].(map[string]any)
	if ga == nil || ga["k"] != "va" {
		t.Errorf("the first derivation's group was corrupted: %v", fa)
	}
	if gb == nil || gb["k"] != "vb" {
		t.Errorf("the second derivation's group was corrupted: %v", fb)
	}
	// And both still carry the id at the top level.
	if fa[Key] != "shared" || fb[Key] != "shared" {
		t.Errorf("a derived logger lost the request id: %v / %v", fa, fb)
	}
}

// A group nested inside a group is where a naive fix stops working.
func TestNestedGroupsStillKeepTheIDOutside(t *testing.T) {
	buf := &bytes.Buffer{}
	base := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := slog.New(NewHandler(base)).
		With("component", "worker").
		WithGroup("job").
		WithGroup("feed")

	log.InfoContext(With(context.Background(), "deep"), "ingested", "items", 4)

	f := fields(t, buf)
	if f[Key] != "deep" {
		t.Errorf("%s is not at the top level under nested groups: %v", Key, f)
	}
	if f["component"] != "worker" {
		t.Errorf("an attribute bound before the groups was lost: %v", f)
	}
	job, _ := f["job"].(map[string]any)
	feed, _ := job["feed"].(map[string]any)
	if feed == nil || feed["items"] != float64(4) {
		t.Errorf("the nested group structure was lost: %v", f)
	}
}
