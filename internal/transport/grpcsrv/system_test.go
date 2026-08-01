package grpcsrv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/obs"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// systemDB opens and migrates a real database, the way GetVersion's schema
// read and CheckHealth's live query both need.
func systemDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "system.db")})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func systemScope() store.Scope {
	return store.Scope{TenantID: "t1", UserID: "u1", Role: "member"}
}

// --- GetVersion / CheckHealth (unauthenticated surface) ------------------------

func TestGetVersionIsPublicAndReportsSchema(t *testing.T) {
	db := systemDB(t)
	s := NewSystemServer("1.2.3", "deadbeef", db)

	res, err := s.GetVersion(context.Background(), &pb.GetVersionRequest{})
	if err != nil {
		t.Fatalf("GetVersion on a fresh unauthenticated server: %v", err)
	}
	if res.GetVersion() != "1.2.3" || res.GetCommit() != "deadbeef" {
		t.Errorf("Version/Commit = %q/%q, want 1.2.3/deadbeef", res.GetVersion(), res.GetCommit())
	}
	if res.GetSchemaVersion() <= 0 {
		t.Errorf("SchemaVersion = %d on a migrated db, want > 0", res.GetSchemaVersion())
	}
}

func TestGetVersionToleratesAnUnreadableSchema(t *testing.T) {
	db := systemDB(t)
	s := NewSystemServer("1.2.3", "deadbeef", db)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := s.GetVersion(context.Background(), &pb.GetVersionRequest{})
	if err != nil {
		t.Fatalf("GetVersion over a closed db returned an error instead of SchemaVersion=0: %v", err)
	}
	if res.GetSchemaVersion() != 0 {
		t.Errorf("SchemaVersion = %d, want 0 when the schema can't be read", res.GetSchemaVersion())
	}
}

func TestCheckHealthServingOverALiveDB(t *testing.T) {
	db := systemDB(t)
	s := NewSystemServer("1.0.0", "abc", db)

	res, err := s.CheckHealth(context.Background(), &pb.CheckHealthRequest{})
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if res.GetStatus() != pb.ServingStatus_SERVING_STATUS_SERVING {
		t.Errorf("status = %v, want SERVING", res.GetStatus())
	}
}

func TestCheckHealthNotServingWhenStorageIsGone(t *testing.T) {
	db := systemDB(t)
	s := NewSystemServer("1.0.0", "abc", db)
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := s.CheckHealth(context.Background(), &pb.CheckHealthRequest{})
	if err != nil {
		// CheckHealth is documented to answer NOT_SERVING rather than error;
		// a transport error here would itself be worth knowing about.
		t.Fatalf("CheckHealth over a closed db returned a transport error instead of NOT_SERVING: %v", err)
	}
	if res.GetStatus() != pb.ServingStatus_SERVING_STATUS_NOT_SERVING {
		t.Errorf("status = %v, want NOT_SERVING", res.GetStatus())
	}
	if res.GetDetail() != "storage unavailable" {
		t.Errorf("detail = %q, want %q", res.GetDetail(), "storage unavailable")
	}
}

// --- GetServerStats --------------------------------------------------------------

func TestGetServerStatsUnimplementedWithoutObservability(t *testing.T) {
	db := systemDB(t)
	s := NewSystemServer("1.0.0", "abc", db)

	_, err := s.GetServerStats(context.Background(), &pb.GetServerStatsRequest{})
	if codeOf(err) != codes.Unimplemented {
		t.Fatalf("code = %v, want Unimplemented", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.noObservability" {
		t.Errorf("key = %q, want srv.noObservability", key)
	}
}

func TestGetServerStatsWiredReturnsSaneNumbers(t *testing.T) {
	db := systemDB(t)
	repo := store.NewReaderRepo(db)
	sc := systemScope()
	if err := repo.CreateTenantAndUser(context.Background(), store.NewTenant{
		TenantID: sc.TenantID, Name: "Test", UserID: sc.UserID,
		Username: "reader", Hash: "x", Role: sc.Role,
		Now: time.Now().UTC().Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ring := obs.NewRing(slog.NewTextHandler(io.Discard, nil), 50)
	lat := obs.NewLatency()
	lat.Observe("GetServerStats", 5*time.Millisecond, false)

	s := NewSystemServer("2.0.0", "cafef00d", db).
		WithObservability(repo, ring, lat, 30*time.Second, fixedScope(sc))

	res, err := s.GetServerStats(context.Background(), &pb.GetServerStatsRequest{})
	if err != nil {
		t.Fatalf("GetServerStats: %v", err)
	}
	if res.GetStartedAt() == "" {
		t.Error("StartedAt is empty")
	}
	if res.GetDbPath() == "" {
		t.Error("DbPath is empty")
	}
	if res.GetSchemaVersion() <= 0 {
		t.Errorf("SchemaVersion = %d, want > 0", res.GetSchemaVersion())
	}
	if res.GetHeapBytes() <= 0 {
		t.Error("HeapBytes = 0, want a real heap reading")
	}
	if res.GetGoroutines() <= 0 {
		t.Error("Goroutines = 0, want at least this test's own goroutine")
	}
	if res.GetPollIntervalS() != 30 {
		t.Errorf("PollIntervalS = %d, want 30", res.GetPollIntervalS())
	}
	if len(res.GetMethods()) != 1 || res.GetMethods()[0].GetMethod() != "GetServerStats" {
		t.Errorf("Methods = %+v, want the one latency sample fed in", res.GetMethods())
	}
}

func TestGetServerStatsPropagatesAScopeOfError(t *testing.T) {
	db := systemDB(t)
	repo := store.NewReaderRepo(db)
	erroring := func(context.Context) (store.Scope, error) { return store.Scope{}, errors.New("session lookup failed") }
	s := NewSystemServer("1.0.0", "abc", db).
		WithObservability(repo, obs.NewRing(slog.NewTextHandler(io.Discard, nil), 10), obs.NewLatency(), time.Second, erroring)

	if _, err := s.GetServerStats(context.Background(), &pb.GetServerStatsRequest{}); err == nil {
		t.Fatal("a scopeOf failure was swallowed instead of propagated")
	}
}

// --- ListLogs ---------------------------------------------------------------------

func TestListLogsUnimplementedWithoutRing(t *testing.T) {
	db := systemDB(t)
	sc := systemScope()
	repo := store.NewReaderRepo(db)
	// scopeOf is wired but the ring is not — both must be present.
	s := NewSystemServer("1.0.0", "abc", db).
		WithObservability(repo, nil, nil, time.Second, fixedScope(sc))

	_, err := s.ListLogs(context.Background(), &pb.ListLogsRequest{})
	if codeOf(err) != codes.Unimplemented {
		t.Fatalf("code = %v, want Unimplemented", codeOf(err))
	}
	if key := detailKey(t, err); key != "srv.noLogBuffer" {
		t.Errorf("key = %q, want srv.noLogBuffer", key)
	}
}

func TestListLogsFiltersByMinLevel(t *testing.T) {
	db := systemDB(t)
	sc := systemScope()
	repo := store.NewReaderRepo(db)

	// Debug has to reach the ring for the filter to have anything to exclude,
	// which means the wrapped handler must itself accept Debug records.
	h := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})
	ring := obs.NewRing(h, 50)
	logger := slog.New(ring)
	logger.Debug("noisy debug line")
	logger.Warn("a warning line")

	s := NewSystemServer("1.0.0", "abc", db).
		WithObservability(repo, ring, obs.NewLatency(), time.Second, fixedScope(sc))

	res, err := s.ListLogs(context.Background(), &pb.ListLogsRequest{MinLevel: "WARN"})
	if err != nil {
		t.Fatalf("ListLogs: %v", err)
	}
	if len(res.GetRecords()) != 1 {
		t.Fatalf("got %d records, want 1 (the debug line should be filtered)", len(res.GetRecords()))
	}
	if res.GetRecords()[0].GetMessage() != "a warning line" {
		t.Errorf("message = %q, want the warning line", res.GetRecords()[0].GetMessage())
	}
	for _, r := range res.GetRecords() {
		if r.GetMessage() == "noisy debug line" {
			t.Error("a DEBUG record survived a min_level=WARN filter")
		}
	}
}

func TestListLogsPropagatesAScopeOfError(t *testing.T) {
	db := systemDB(t)
	repo := store.NewReaderRepo(db)
	erroring := func(context.Context) (store.Scope, error) { return store.Scope{}, errors.New("session lookup failed") }
	ring := obs.NewRing(slog.NewTextHandler(io.Discard, nil), 10)
	s := NewSystemServer("1.0.0", "abc", db).
		WithObservability(repo, ring, obs.NewLatency(), time.Second, erroring)

	if _, err := s.ListLogs(context.Background(), &pb.ListLogsRequest{}); err == nil {
		t.Fatal("a scopeOf failure was swallowed instead of propagated")
	}
}

// --- parseLevel / ms2 (pure functions) --------------------------------------------

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"WARN", slog.LevelWarn},
		{"Warn", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"  ", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"nonsense", slog.LevelInfo},
		{" eRrOr ", slog.LevelError},
	}
	for _, c := range cases {
		if got := parseLevel(c.in); got != c.want {
			t.Errorf("parseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMs2(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want float64
	}{
		{0, 0},
		{time.Millisecond, 1},
		{1500 * time.Microsecond, 1.5},
		{2 * time.Second, 2000},
	}
	for _, c := range cases {
		if got := ms2(c.in); got != c.want {
			t.Errorf("ms2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
