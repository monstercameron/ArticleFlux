package grpcsrv

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/obs"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// SystemServer answers "is this running, and which build am I talking to"
// without a credential.
type SystemServer struct {
	pb.UnimplementedSystemServiceServer
	version string
	commit  string
	db      *store.DB

	// The observability surface. All optional: a server wired without them still
	// answers GetVersion and CheckHealth, which is what the readiness probe
	// needs, and the settings screen degrades to the fields it can fill.
	repo    *store.ReaderRepo
	ring    *obs.Ring
	lat     *obs.Latency
	started time.Time
	pollS   int
	scopeOf func(context.Context) (store.Scope, error)
}

// NewSystemServer wires the system surface.
func NewSystemServer(version, commit string, db *store.DB) *SystemServer {
	return &SystemServer{version: version, commit: commit, db: db, started: time.Now()}
}

// WithObservability attaches everything the settings screen reads. Separate from
// the constructor so the unauthenticated health surface stays constructible
// without any of it.
func (s *SystemServer) WithObservability(repo *store.ReaderRepo, ring *obs.Ring,
	lat *obs.Latency, pollInterval time.Duration,
	scopeOf func(context.Context) (store.Scope, error)) *SystemServer {
	s.repo, s.ring, s.lat = repo, ring, lat
	s.pollS = int(pollInterval / time.Second)
	s.scopeOf = scopeOf
	return s
}

func (s *SystemServer) GetVersion(ctx context.Context, _ *pb.GetVersionRequest) (*pb.GetVersionResponse, error) {
	// Schema version is included because version skew is real here (T12): a wasm
	// client cached by a Service Worker can outlive several deploys and needs a
	// way to notice it is talking to a different schema than it started on.
	v, err := s.db.SchemaVersion(ctx)
	if err != nil {
		v = 0
	}
	return &pb.GetVersionResponse{
		Version:       s.version,
		Commit:        s.commit,
		SchemaVersion: int64(v),
	}, nil
}

func (s *SystemServer) CheckHealth(ctx context.Context, _ *pb.CheckHealthRequest) (*pb.CheckHealthResponse, error) {
	// A real query, not a constant: the point of a health check is to notice that
	// storage has gone away, and returning SERVING unconditionally makes the
	// check worse than none at all.
	var one int
	if err := s.db.Read.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return &pb.CheckHealthResponse{
			Status: pb.ServingStatus_SERVING_STATUS_NOT_SERVING,
			Detail: "storage unavailable",
		}, nil
	}
	return &pb.CheckHealthResponse{
		Status: pb.ServingStatus_SERVING_STATUS_SERVING,
		Detail: "ok",
	}, nil
}

// GetServerStats answers "what is this thing doing".
//
// Authenticated, unlike GetVersion: it discloses row counts and database size,
// which are tenant state. GetVersion stays information-free because the
// readiness probe calls it without a credential.
func (s *SystemServer) GetServerStats(ctx context.Context, _ *pb.GetServerStatsRequest) (*pb.GetServerStatsResponse, error) {
	if s.scopeOf == nil {
		return nil, errKey(codes.Unimplemented, "srv.noObservability", "observability not wired", nil)
	}
	sc, err := s.scopeOf(ctx)
	if err != nil {
		return nil, toStatus(err)
	}

	out := &pb.GetServerStatsResponse{
		Version: s.version, Commit: s.commit,
		UptimeS:       int64(time.Since(s.started).Seconds()),
		StartedAt:     s.started.UTC().Format(time.RFC3339),
		DbPath:        s.db.Path(),
		PollIntervalS: int32(s.pollS),
	}
	if v, err := s.db.SchemaVersion(ctx); err == nil {
		out.SchemaVersion = int64(v)
	}
	out.DbBytes, out.WalBytes = s.db.FileSizes()

	if s.repo != nil {
		if st, err := s.repo.Stats(ctx, sc); err == nil {
			out.Feeds = int32(st.Feeds)
			out.Items = int32(st.Items)
			out.Unread = int32(st.Unread)
			out.Notes = int32(st.Notes)
			out.Tags = int32(st.Tags)
			out.Rated = int32(st.Rated)
			out.Saved = int32(st.Saved)
			out.DormantFeeds = int32(st.Dormant)
		}
		if at, err := s.repo.LastPollAt(ctx, sc); err == nil {
			out.LastPollAt = at
		}
	}

	// Go's own numbers. "Is it the reader or is it my box" is the first question
	// anyone asks about a slow self-hosted app, and a heap figure answers it.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	out.HeapBytes = int64(ms.HeapAlloc)
	out.Goroutines = int32(runtime.NumGoroutine())
	out.GcCycles = int32(ms.NumGC)

	if s.lat != nil {
		for _, m := range s.lat.Snapshot() {
			out.Methods = append(out.Methods, &pb.MethodStat{
				Method: m.Method, Calls: m.Calls, Errors: m.Errors,
				P50Ms: ms2(m.P50), P95Ms: ms2(m.P95),
				MaxMs: ms2(m.Max), MeanMs: ms2(m.Mean),
			})
		}
	}
	if s.ring != nil {
		for lvl, n := range s.ring.Counts() {
			out.LogCounts = append(out.LogCounts, &pb.LevelCount{
				Level: lvl.String(), Count: n,
			})
		}
	}
	return out, nil
}

// ListLogs returns the tail of the in-memory ring.
func (s *SystemServer) ListLogs(ctx context.Context, req *pb.ListLogsRequest) (*pb.ListLogsResponse, error) {
	if s.scopeOf == nil || s.ring == nil {
		return nil, errKey(codes.Unimplemented, "srv.noLogBuffer", "log buffer not wired", nil)
	}
	// Authenticated: log lines carry feed URLs, error text and host names.
	if _, err := s.scopeOf(ctx); err != nil {
		return nil, toStatus(err)
	}
	limit := int(req.GetLimit())
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	out := &pb.ListLogsResponse{}
	for _, rec := range s.ring.Recent(limit, parseLevel(req.GetMinLevel())) {
		out.Records = append(out.Records, &pb.LogRecord{
			Time:    rec.Time.UTC().Format(time.RFC3339),
			Level:   rec.Level.String(),
			Message: rec.Message,
			Attrs:   rec.Attrs,
		})
	}
	return out, nil
}

// parseLevel defaults to INFO. Debug is opt-in because it is where the volume
// is, and a screen that opens on debug shows nothing but noise.
func parseLevel(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ms2 renders a duration in milliseconds to two decimals. Microsecond precision
// on a screen a person reads is noise.
func ms2(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}
