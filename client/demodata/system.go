package demodata

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// Version is what the demo reports as the build it is running.
//
// Overwritten at link time by the release workflow (-X). A default that says
// "dev" rather than a version number is the honest answer for a build nobody
// stamped, and it shows up on the settings screen where someone comparing a
// deployed demo against a tag can read it.
var Version = "dev"

// --- auth ---------------------------------------------------------------------

// authService is the demo's AuthService.
//
// There is no login, and that is a decision rather than an omission. A demo that
// asks for a password has to publish one, and a published password on a page
// with no server behind it teaches a reader that this application's credentials
// are decorative. So WhoAmI always answers, exactly the way `serve -dev` does on
// a loopback address.
type authService struct {
	pb.UnimplementedAuthServiceServer
	inst *Instance
}

func (s *authService) WhoAmI(context.Context, *pb.WhoAmIRequest) (*pb.WhoAmIResponse, error) {
	return &pb.WhoAmIResponse{
		Username: "demo",
		Role:     "owner",
		TenantId: "demo",
		// dev_mode, because it is true in the sense the flag means: this
		// instance serves its one account without asking for a credential.
		DevMode: true,
	}, nil
}

func (s *authService) Login(_ context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	// Reachable only if someone navigates to the login screen deliberately,
	// since Root never shows it when WhoAmI answers. Accepting anything is the
	// right behaviour for a demo with no accounts: refusing would strand a
	// reader who got there by accident, and minting a token that authorises
	// nothing costs nothing.
	return &pb.LoginResponse{
		Token:     "demo-session",
		ExpiresAt: stamp(s.inst.clock().Add(24 * time.Hour)),
		Username:  "demo",
		Role:      "owner",
		DeviceId:  req.GetDeviceId(),
	}, nil
}

func (s *authService) Logout(context.Context, *pb.LogoutRequest) (*pb.LogoutResponse, error) {
	return &pb.LogoutResponse{}, nil
}

// --- system -------------------------------------------------------------------

type systemService struct {
	pb.UnimplementedSystemServiceServer
	inst *Instance
}

func (s *systemService) GetVersion(context.Context, *pb.GetVersionRequest) (*pb.GetVersionResponse, error) {
	return &pb.GetVersionResponse{Version: Version, Commit: "demo", SchemaVersion: 0}, nil
}

func (s *systemService) CheckHealth(context.Context, *pb.CheckHealthRequest) (*pb.CheckHealthResponse, error) {
	return &pb.CheckHealthResponse{Status: pb.ServingStatus_SERVING_STATUS_SERVING}, nil
}

// GetServerStats reports on the instance in the tab.
//
// The numbers are real — they are counted out of the same structures the reader
// is looking at — except the ones that describe a machine, which are stated as
// zero rather than invented. A demo that made up a database size would be the
// one screen in the application that lies.
func (s *systemService) GetServerStats(context.Context, *pb.GetServerStatsRequest) (*pb.GetServerStatsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	now := in.clock()
	res := &pb.GetServerStatsResponse{
		Version:    Version,
		Commit:     "demo",
		UptimeS:    int64(now.Sub(in.started).Seconds()),
		StartedAt:  stamp(in.started),
		DbPath:     "(in this tab)",
		Feeds:      int32(len(in.feeds)),
		Tags:       int32(len(in.tags)),
		Goroutines: int32(runtime.NumGoroutine()),
		// The poller is the browser's refresh button here, so the interval is
		// the one thing about polling that is honestly zero.
		LastPollAt: stamp(now),
	}
	for _, it := range in.items {
		res.Items++
		if !it.read {
			res.Unread++
		}
		if it.note != "" {
			res.Notes++
		}
		if it.rating != 0 {
			res.Rated++
		}
		if it.starred {
			res.Saved++
		}
	}
	for _, f := range in.feeds {
		if f.failures > 0 {
			res.DormantFeeds++
		}
	}
	for method, calls := range in.calls {
		res.Methods = append(res.Methods, &pb.MethodStat{Method: method, Calls: calls})
	}
	sortStable(res.Methods, func(a, b *pb.MethodStat) bool { return a.Calls > b.Calls })

	levels := map[string]int64{}
	for _, r := range in.logs {
		levels[r.GetLevel()]++
	}
	for level, n := range levels {
		res.LogCounts = append(res.LogCounts, &pb.LevelCount{Level: level, Count: n})
	}
	sortStable(res.LogCounts, func(a, b *pb.LevelCount) bool { return a.Level < b.Level })
	return res, nil
}

func (s *systemService) ListLogs(_ context.Context, req *pb.ListLogsRequest) (*pb.ListLogsResponse, error) {
	in := s.inst
	in.mu.Lock()
	defer in.mu.Unlock()

	limit := int(req.GetLimit())
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	min := levelRank(req.GetMinLevel())
	res := &pb.ListLogsResponse{}
	// Newest first, which is the order the activity screen reads in.
	for i := len(in.logs) - 1; i >= 0 && len(res.Records) < limit; i-- {
		r := in.logs[i]
		if levelRank(r.GetLevel()) < min {
			continue
		}
		res.Records = append(res.Records, r)
	}
	return res, nil
}

func levelRank(level string) int {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return 0
	case "", "INFO":
		return 1
	case "WARN", "WARNING":
		return 2
	case "ERROR":
		return 3
	}
	return 1
}

// --- smart --------------------------------------------------------------------

// smartService is the demo's SmartService, and every method on it says no.
//
// Smart+ is the paid half of this application: it sends text to OpenAI with a
// key the instance owner supplied, and it costs them money per call. A demo
// cannot have one — there is nowhere to put a key that is not "published on a
// static page" — so the interesting question is how it refuses. It refuses the
// way an instance with no key configured refuses, through the same
// `configured: false` the settings screen already knows how to explain, rather
// than through an error that reads as broken.
type smartService struct {
	pb.UnimplementedSmartServiceServer
	inst *Instance
}

func (s *smartService) GetSmartConfig(context.Context, *pb.GetSmartConfigRequest) (*pb.GetSmartConfigResponse, error) {
	return &pb.GetSmartConfigResponse{
		Configured: false,
		// No key can be stored, because there is no server to store one in and
		// no encryption key to store it under. The settings screen reads this
		// and explains rather than offering a field that would silently fail.
		CanStoreSecrets: false,
		DefaultModel:    "gpt-5-mini",
	}, nil
}

func (s *smartService) SetSmartConfig(context.Context, *pb.SetSmartConfigRequest) (*pb.SetSmartConfigResponse, error) {
	return nil, status.Error(codes.FailedPrecondition,
		"the demo has no server to keep a key in — run your own instance for Smart+")
}

// ListLanguages offers the same targets a real instance offers, all uncached.
//
// The list comes from client/i18n rather than being written out again here: it
// is the same list UseLocale clamps to, and two copies of it would disagree the
// first time one was edited.
func (s *smartService) ListLanguages(context.Context, *pb.ListLanguagesRequest) (*pb.ListLanguagesResponse, error) {
	res := &pb.ListLanguagesResponse{SourceLocale: i18n.DefaultLocale}
	for _, l := range i18n.Languages {
		res.Languages = append(res.Languages, &pb.SmartLanguage{
			Code: l.Code, EnglishName: l.English, NativeName: l.Native,
			// Nothing is cached, because nothing can be translated.
			Cached: false,
		})
	}
	return res, nil
}

func (s *smartService) TranslateUI(_ context.Context, req *pb.TranslateUIRequest) (*pb.TranslateUIResponse, error) {
	// Refused rather than answered with the English catalog. A language switch
	// that appears to work and changes nothing is the worst possible failure for
	// this feature — it looks like a translation that came back in English.
	return nil, status.Errorf(codes.FailedPrecondition,
		"translating the interface into %s needs a server with an OpenAI key", req.GetLocale())
}

// --- the log ------------------------------------------------------------------

// logf appends one record to the instance's activity log. Caller holds the lock.
//
// Capped, and the cap is what makes it safe to call from a handler: a tab left
// open for a week pressing refresh must not grow a list nobody reads until the
// page runs out of memory.
const maxLogs = 200

func (in *Instance) logf(level, message string, attrs ...string) {
	rec := &pb.LogRecord{
		Time: stamp(in.clock()), Level: level, Message: message,
	}
	if len(attrs) > 0 {
		// The real thing carries slog attributes as JSON. Assembled by hand here
		// because there are never more than a couple, and pulling in a JSON
		// encoder for two key/value pairs would be weight in a wasm bundle.
		var b strings.Builder
		b.WriteByte('{')
		for i := 0; i+1 < len(attrs); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Quote(attrs[i]))
			b.WriteByte(':')
			b.WriteString(strconv.Quote(attrs[i+1]))
		}
		b.WriteByte('}')
		rec.Attrs = b.String()
	}
	in.logs = append(in.logs, rec)
	if len(in.logs) > maxLogs {
		in.logs = in.logs[len(in.logs)-maxLogs:]
	}
}
