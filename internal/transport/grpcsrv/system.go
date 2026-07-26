package grpcsrv

import (
	"context"

	pb "github.com/monstercameron/Tidings/internal/pb/tidings/v1"
	"github.com/monstercameron/Tidings/internal/store"
)

// SystemServer answers "is this running, and which build am I talking to"
// without a credential.
type SystemServer struct {
	pb.UnimplementedSystemServiceServer
	version string
	commit  string
	db      *store.DB
}

// NewSystemServer wires the system surface.
func NewSystemServer(version, commit string, db *store.DB) *SystemServer {
	return &SystemServer{version: version, commit: commit, db: db}
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
