package grpcsrv

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// SmartServer is the Smart+ control surface.
//
// Every method here is owner-only, and the check is at the top of each one
// rather than in a shared wrapper. That is deliberate while there is no
// capability interceptor yet (§7): a gate that lives somewhere else is a gate
// somebody adds an RPC without. When the interceptor lands, these become
// redundant and can go — until then they are the only thing between a member
// account and the instance's API key.
type SmartServer struct {
	pb.UnimplementedSmartServiceServer

	settings *store.SettingsRepo
	llm      *llm.Client
	tr       *smart.Translator
	scopeOf  func(context.Context) (store.Scope, error)
	log      *slog.Logger
}

// NewSmartServer wires the Smart+ surface.
func NewSmartServer(settings *store.SettingsRepo, client *llm.Client, tr *smart.Translator,
	scopeOf func(context.Context) (store.Scope, error), log *slog.Logger) *SmartServer {
	return &SmartServer{settings: settings, llm: client, tr: tr, scopeOf: scopeOf, log: log}
}

// ownerRoles are the roles allowed to see or change instance configuration.
//
// "member" and "viewer" are not here on purpose. A member can turn Smart+ voice
// on for themselves — that is a per-user preference and it costs the instance
// nothing they cannot already spend — but they cannot read the key, change the
// model, or start a translation that bills the instance.
var ownerRoles = map[string]bool{"superadmin": true, "admin": true}

// requireOwner resolves the caller and refuses anyone who is not an owner.
func (s *SmartServer) requireOwner(ctx context.Context) (store.Scope, error) {
	if s.scopeOf == nil {
		return store.Scope{}, status.Error(codes.Unauthenticated, "not signed in")
	}
	sc, err := s.scopeOf(ctx)
	if err != nil || !sc.Valid() {
		return store.Scope{}, status.Error(codes.Unauthenticated, "not signed in")
	}
	if !ownerRoles[sc.Role] {
		// Names the requirement rather than the failure. "Permission denied" on
		// a settings screen sends someone hunting for a bug; "this is an
		// administrator setting" sends them to whoever runs the server.
		return store.Scope{}, status.Error(codes.PermissionDenied,
			"only an administrator can change Smart+ settings")
	}
	return sc, nil
}

func (s *SmartServer) GetSmartConfig(ctx context.Context, _ *pb.GetSmartConfigRequest) (
	*pb.GetSmartConfigResponse, error) {
	if _, err := s.requireOwner(ctx); err != nil {
		return nil, err
	}
	return s.config(ctx), nil
}

func (s *SmartServer) SetSmartConfig(ctx context.Context, req *pb.SetSmartConfigRequest) (
	*pb.SetSmartConfigResponse, error) {
	sc, err := s.requireOwner(ctx)
	if err != nil {
		return nil, err
	}

	switch {
	case req.GetClearApiKey():
		if err := s.settings.SetSystemSecret(ctx, store.KeyOpenAIAPIKey, "", sc.UserID); err != nil {
			return nil, s.secretErr(err)
		}
		s.log.Info("smart+ api key cleared", "by", sc.UserID)
	case strings.TrimSpace(req.GetOpenaiApiKey()) != "":
		key := strings.TrimSpace(req.GetOpenaiApiKey())
		// A shape check, not a validity check. The server cannot tell a live key
		// from a revoked one without spending a request, but it can tell a key
		// from a pasted URL or a whole shell line — which is what actually gets
		// pasted into this box.
		if !strings.HasPrefix(key, "sk-") {
			return nil, status.Error(codes.InvalidArgument,
				"that does not look like an OpenAI API key — they begin with sk-")
		}
		if err := s.settings.SetSystemSecret(ctx, store.KeyOpenAIAPIKey, key, sc.UserID); err != nil {
			return nil, s.secretErr(err)
		}
		s.log.Info("smart+ api key set", "by", sc.UserID)
	}

	if m := strings.TrimSpace(req.GetModel()); m != "" {
		if err := s.settings.SetSystemValue(ctx, store.KeySmartModel, m, sc.UserID); err != nil {
			return nil, status.Error(codes.Internal, "couldn't save the model")
		}
	}

	return &pb.SetSmartConfigResponse{Config: s.config(ctx)}, nil
}

func (s *SmartServer) ListLanguages(ctx context.Context, _ *pb.ListLanguagesRequest) (
	*pb.ListLanguagesResponse, error) {
	if _, err := s.requireOwner(ctx); err != nil {
		return nil, err
	}
	cached := s.tr.CachedLocales(ctx)
	out := make([]*pb.SmartLanguage, 0, len(smart.Languages))
	for _, l := range smart.Languages {
		out = append(out, &pb.SmartLanguage{
			Code:        l.Code,
			EnglishName: l.English,
			NativeName:  l.Native,
			Cached:      cached[l.Code],
		})
	}
	return &pb.ListLanguagesResponse{Languages: out, SourceLocale: i18n.DefaultLocale}, nil
}

func (s *SmartServer) TranslateUI(ctx context.Context, req *pb.TranslateUIRequest) (
	*pb.TranslateUIResponse, error) {
	if _, err := s.requireOwner(ctx); err != nil {
		return nil, err
	}

	locale := strings.TrimSpace(req.GetLocale())
	// Whether this was free is decided BEFORE the call, because afterwards
	// there is no way to tell a cache hit from a fresh translation — and "that
	// switch just cost you a dollar" is a thing the reader should be told.
	wasCached := !req.GetForce() && s.tr.CachedLocales(ctx)[locale]

	entries, err := s.tr.Catalog(ctx, locale, req.GetForce())
	switch {
	case errors.Is(err, smart.ErrUnsupportedLanguage):
		return nil, status.Errorf(codes.InvalidArgument, "%q is not one of the offered languages", locale)
	case errors.Is(err, smart.ErrIsSource):
		return nil, status.Error(codes.InvalidArgument, "the interface is already in English")
	case errors.Is(err, llm.ErrNotConfigured):
		return nil, status.Error(codes.FailedPrecondition,
			"Smart+ needs an OpenAI API key — add one in Settings › Smart+")
	case errors.Is(err, llm.ErrTruncated):
		// Named, because the remedy is specific and nobody would guess it: the
		// batch was too big for the model's output ceiling.
		return nil, status.Error(codes.ResourceExhausted,
			"the translation was cut off before it finished — try again")
	case err != nil:
		// The provider's text can echo the request, so it goes to the log with
		// the request id and a flat message goes to the client (§20.7).
		s.log.Error("ui translation failed", "locale", locale, "err", err)
		return nil, status.Error(codes.Unavailable, "couldn't translate the interface just now")
	}

	msgs := make([]*pb.UIMessage, 0, len(entries))
	for _, e := range entries {
		msgs = append(msgs, &pb.UIMessage{Key: e.Key, Text: e.Text, Forms: e.Plural})
	}
	return &pb.TranslateUIResponse{Locale: locale, Messages: msgs, Cached: wasCached}, nil
}

// config assembles the status the settings screen shows.
func (s *SmartServer) config(ctx context.Context) *pb.GetSmartConfigResponse {
	out := &pb.GetSmartConfigResponse{
		CanStoreSecrets: s.settings.CanStoreSecrets(),
		DefaultModel:    llm.DefaultModel,
	}
	if m, err := s.settings.SystemValue(ctx, store.KeySmartModel); err == nil {
		out.Model = m
	}

	stored, err := s.settings.SystemSecret(ctx, store.KeyOpenAIAPIKey)
	if err != nil && !errors.Is(err, store.ErrNoSetting) {
		// An undecryptable row is reported as "not configured" here but it is
		// LOGGED, because the operator's next move — paste the key again —
		// works and silently abandons whatever the old key was protecting.
		s.log.Warn("stored Smart+ key could not be read", "err", err)
	}
	key := strings.TrimSpace(stored)
	if key == "" {
		// The environment is still honoured, and the screen says so. An
		// instance configured by OPENAI_API_KEY that showed "not configured"
		// while Smart+ plainly worked would be unexplainable.
		if envKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); envKey != "" {
			key = envKey
			out.FromEnvironment = true
		}
	}
	out.Configured = key != ""
	if n := len(key); n >= 4 {
		out.KeyHint = key[n-4:]
	}

	u := s.llm.Usage()
	out.InputTokens, out.OutputTokens, out.Requests = u.InputTokens, u.OutputTokens, u.Requests
	return out
}

// secretErr turns a storage refusal into something the screen can act on.
func (s *SmartServer) secretErr(err error) error {
	if errors.Is(err, secret.ErrKeyLength) {
		return status.Error(codes.FailedPrecondition,
			"this server has no encryption key, so it will not store a credential — "+
				"set OPENAI_API_KEY in the environment instead")
	}
	s.log.Error("couldn't save the Smart+ key", "err", err)
	return status.Error(codes.Internal, "couldn't save that")
}
