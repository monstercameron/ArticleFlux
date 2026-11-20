package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/netguard"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
	"github.com/monstercameron/ArticleFlux/internal/secret"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/tts"
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
	// tts meters the voice. Optional — an instance with no key has no client —
	// and nil-safe, because "speech has cost nothing" is the honest answer for
	// one that has never spoken.
	tts     *tts.Client
	tr      *smart.Translator
	scopeOf func(context.Context) (store.Scope, error)
	log     *slog.Logger

	// refusalOf reports the most recent Smart+ failure this process observed.
	//
	// Optional and nil-safe. It exists because `configured` can only ever mean
	// "a key is stored" — proving one works costs money, so it does not claim
	// to — and an expired key, a revoked key, a project with no credit and a
	// model this account cannot reach all read as configured. Beside a silent
	// player that can only say "the voice didn't start", that left a reader
	// with four green ticks and nowhere to look.
	refusalOf func() (string, time.Time)

	// The theming half (§20.16.3, theme.go). All three are optional and all three
	// are installed together by WithTheming.
	//
	// `palettes` is nil on an instance with no key, and the drift still works —
	// the deterministic rung needs no model. `reader` is where the interests and
	// the consent preference are read from. `themeLimit` is the per-user budget,
	// which is what makes these the only non-owner RPCs on this service that are
	// safe to serve.
	palettes   *smart.Palettes
	reader     *store.ReaderRepo
	themeLimit *ratelimit.Limiter

	// Article translation (§10.5b, translate.go). Built on first use from
	// llm, settings and reader, so an instance wired before this existed gets
	// it without a constructor change.
	articleOnce    sync.Once
	articleTr      *smart.ArticleTranslator
	translateLimit *ratelimit.Limiter
	// mintAsset is ReaderServer.mintAsset, for the same rewrite on a
	// translation's two bodies (§10.1a, translate.go). Nil leaves them as the
	// publisher wrote them, which is what an instance with no asset proxy does
	// on GetItem too.
	mintAsset func(absURL string) string
}

// WithAssetProxy points a translated article's images at the local proxy,
// exactly as ReaderServer.WithAssetProxy does for the article itself. Install
// the same minting function on both: two servers that proxied differently
// would show the reader a translation fetching from a host its original does
// not.
func (s *SmartServer) WithAssetProxy(mint func(absURL string) string) *SmartServer {
	s.mintAsset = mint
	return s
}

// NewSmartServer wires the Smart+ surface.
func NewSmartServer(settings *store.SettingsRepo, client *llm.Client, tr *smart.Translator,
	scopeOf func(context.Context) (store.Scope, error), log *slog.Logger) *SmartServer {
	if tr != nil {
		tr.WithLog(log)
	}
	return &SmartServer{settings: settings, llm: client, tr: tr, scopeOf: scopeOf, log: log,
		translateLimit: ratelimit.New(ratelimit.Options{})}
}

// WithRefusals installs the observation the screen needs to stop claiming more
// than it knows: what happened the last time the key was actually used.
func (s *SmartServer) WithRefusals(f func() (string, time.Time)) *SmartServer {
	s.refusalOf = f
	return s
}

// WithSpeechMeter installs the voice client whose spend the screen reports.
//
// Optional and separate from the constructor, like every other capability on
// these servers: an instance with no OpenAI key has no speech client, and a
// constructor that demanded one would make the absence of a paid feature a
// wiring error.
func (s *SmartServer) WithSpeechMeter(c *tts.Client) *SmartServer {
	s.tts = c
	return s
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
		return store.Scope{}, errKey(codes.Unauthenticated, "srv.notSignedIn", "You are not signed in.", nil)
	}
	sc, err := s.scopeOf(ctx)
	if err != nil || !sc.Valid() {
		return store.Scope{}, errKey(codes.Unauthenticated, "srv.notSignedIn", "You are not signed in.", nil)
	}
	if !ownerRoles[sc.Role] {
		// Names the requirement rather than the failure. "Permission denied" on
		// a settings screen sends someone hunting for a bug; "this is an
		// administrator setting" sends them to whoever runs the server.
		return store.Scope{}, errKey(codes.PermissionDenied, "srv.adminOnly",
			"Only an administrator can change Smart+ settings.", nil)
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
	// The JEV half is validated before ANYTHING is written, so a refused base
	// URL cannot leave behind the OpenAI key or the model saved by the same
	// press — a half-applied save reads, on the screen, as a save that worked.
	jev, err := s.jevChange(ctx, req)
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
			return nil, errKey(codes.InvalidArgument, "srv.badApiKeyShape",
				"That does not look like an OpenAI API key. Keys begin with “sk-”.", nil)
		}
		if err := s.settings.SetSystemSecret(ctx, store.KeyOpenAIAPIKey, key, sc.UserID); err != nil {
			return nil, s.secretErr(err)
		}
		s.log.Info("smart+ api key set", "by", sc.UserID)
	}

	// An empty `model` has always meant "leave it alone" — a key-only save sends
	// it empty, and so does every client that predates reset_model — so putting
	// the model BACK to the default needed a word of its own. Without one the
	// field said "leave it blank for the default", saved nothing, and the old
	// model stayed (G8). Stored as the empty string, which is what "no
	// override" has always been: WithModel reads it as unset and the llm layer
	// falls to DefaultModel.
	switch m := strings.TrimSpace(req.GetModel()); {
	case m != "":
		if err := s.settings.SetSystemValue(ctx, store.KeySmartModel, m, sc.UserID); err != nil {
			return nil, errKey(codes.Internal, "srv.saveModelFailed", "Couldn't save the model.", nil)
		}
	case req.GetResetModel():
		if err := s.settings.SetSystemValue(ctx, store.KeySmartModel, "", sc.UserID); err != nil {
			return nil, errKey(codes.Internal, "srv.saveModelFailed", "Couldn't save the model.", nil)
		}
		s.log.Info("smart+ model reset to the default", "by", sc.UserID)
	}

	// T2. The key was read on every classification batch and written by nothing,
	// which is why the Classification tab's switch has been rendering disabled:
	// there was no honest way to offer a control that could not take.
	//
	// Optional in the request so that "leave it as it is" stays expressible —
	// otherwise every unrelated save, a model change or a new key, would carry a
	// decision about egress that nobody made.
	if req.ClassifyEnabled != nil {
		v := "false"
		if req.GetClassifyEnabled() {
			v = "true"
		}
		if err := s.settings.SetSystemValue(ctx, smart.KeyClassifyEnabled, v, sc.UserID); err != nil {
			return nil, errKey(codes.Internal, "srv.saveClassifyFailed",
				"Couldn't save that setting.", nil)
		}
		// Logged at the same level as the key, because it is the same kind of
		// decision: it is what makes article text leave this machine.
		s.log.Info("smart+ classification consent changed", "enabled", req.GetClassifyEnabled(), "by", sc.UserID)
	}

	// The article-translation consent (§10.5b), the same shape and the same
	// log line as classification's: it is what makes article text leave.
	if req.TranslateArticlesEnabled != nil {
		v := "false"
		if req.GetTranslateArticlesEnabled() {
			v = "true"
		}
		if err := s.settings.SetSystemValue(ctx, smart.KeyTranslateArticles, v, sc.UserID); err != nil {
			return nil, errKey(codes.Internal, "srv.saveClassifyFailed",
				"Couldn't save that setting.", nil)
		}
		s.log.Info("smart+ article translation consent changed",
			"enabled", req.GetTranslateArticlesEnabled(), "by", sc.UserID)
	}

	// T4. Negative is refused rather than clamped: somebody typing -5 meant
	// something, and silently reading it as "no ceiling" is the opposite of what
	// they were reaching for.
	if req.BudgetUsd != nil {
		usd := req.GetBudgetUsd()
		if usd < 0 {
			return nil, errKey(codes.InvalidArgument, "srv.badBudget",
				"A spend limit cannot be negative. Use 0 for no limit.", nil)
		}
		if err := s.settings.SetSystemValue(ctx, store.KeySmartBudgetUSD,
			strconv.FormatFloat(usd, 'f', -1, 64), sc.UserID); err != nil {
			return nil, errKey(codes.Internal, "srv.saveBudgetFailed",
				"Couldn't save the spend limit.", nil)
		}
		s.log.Info("smart+ spend limit changed", "usd", usd, "by", sc.UserID)
	}

	if err := s.applyJEV(ctx, sc, jev); err != nil {
		return nil, err
	}

	return &pb.SetSmartConfigResponse{Config: s.config(ctx)}, nil
}

// jevPlan is a validated JEV change, ready to write. Nil pointers leave the
// stored value alone.
type jevPlan struct {
	key      string // non-empty: store it
	clearKey bool
	provider *string
	baseURL  *string
}

// jevDefaultProvider is what an unset route means — the same default
// internal/app's DeciderKeyFunc applies, stated once more here because the
// screen has to report the route actually in force, not an empty string.
const jevDefaultProvider = "jev"

// jevChange validates the JEV half of a SetSmartConfig against what is stored,
// because the rules are about the RESULT: a LiteLLM route needs a base URL
// whether this request sends one or one is already there.
func (s *SmartServer) jevChange(ctx context.Context, req *pb.SetSmartConfigRequest) (jevPlan, error) {
	var plan jevPlan
	stored := s.jevStored(ctx)
	provider, base := stored.provider, stored.base

	if req.JevProvider != nil {
		p := strings.TrimSpace(req.GetJevProvider())
		// The enumeration is llm.DeciderHosts, the egress test's own list, so a
		// route this accepts is one that test has already seen.
		if _, known := llm.DeciderHosts[p]; !known {
			return plan, errKey(codes.InvalidArgument, "srv.jevBadProvider",
				"The decision route must be jev, jev-openrouter or jev-litellm.", nil)
		}
		plan.provider, provider = &p, p
	}
	if req.JevBaseUrl != nil {
		b := strings.TrimSpace(req.GetJevBaseUrl())
		if b != "" {
			if err := checkDeciderBaseURL(b); err != nil {
				return plan, err
			}
		}
		plan.baseURL, base = &b, b
	}

	// Only when this request touches the route: a stored state these rules
	// would refuse (written before them, or by hand) must not make every
	// unrelated save — a model, a spend limit — fail with a message about a
	// field nobody touched.
	touched := req.JevProvider != nil || req.JevBaseUrl != nil
	switch {
	case !touched:
	case provider == "jev-litellm" && base == "":
		// The ladder would skip rung 2 silently (llm.Client.decider), which is
		// a setting that looks saved and does nothing. Said now instead.
		return plan, errKey(codes.InvalidArgument, "srv.jevBaseURLRequired",
			"A LiteLLM route needs the base URL of your proxy.", nil)
	case provider != "jev-litellm" && req.JevBaseUrl != nil && base != "":
		// The other two routes have fixed hosts. A base URL beside them would be
		// a way to point decisions at a host llm.DeciderHosts does not list.
		return plan, errKey(codes.InvalidArgument, "srv.jevBaseURLNotUsed",
			"Only a LiteLLM route takes a base URL. The other two have fixed addresses.", nil)
	case provider != "jev-litellm" && base != "" && plan.baseURL == nil:
		// Leaving LiteLLM: the proxy's address goes with it, for the reason
		// above — a stored base URL that "only LiteLLM reads" is one code
		// change away from being read by the others.
		empty := ""
		plan.baseURL = &empty
	}

	switch {
	case req.GetClearJevApiKey():
		plan.clearKey = true
	case strings.TrimSpace(req.GetJevApiKey()) != "":
		key := strings.TrimSpace(req.GetJevApiKey())
		// A shape check only, like the OpenAI key's, and looser, because
		// TypeSafe publishes no prefix to check against. What it catches is
		// what actually gets pasted into this box by mistake: a URL, a whole
		// shell line, a key with its variable name in front.
		if strings.Contains(key, "://") || strings.ContainsAny(key, " \t\r\n=") {
			return plan, errKey(codes.InvalidArgument, "srv.badJevKeyShape",
				"That does not look like a JEV API key. Paste the key alone.", nil)
		}
		plan.key = key
	}
	return plan, nil
}

// checkDeciderBaseURL is the LiteLLM base URL's whole validation: https only,
// a host, no credentials in the URL, and nothing netguard.CheckURL refuses —
// the SSRF guard's own early check, reused rather than restated, so a literal
// loopback, private, link-local or metadata address is refused here exactly
// as a feed URL naming one is. Like CheckURL this does not resolve the name;
// "localhost" is refused by name because it is the one name every resolver
// agrees on.
func checkDeciderBaseURL(raw string) error {
	bad := errKey(codes.InvalidArgument, "srv.jevBadBaseURL",
		"The base URL must be an https:// address on a public host.", nil)
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Hostname() == "" || u.User != nil {
		return bad
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return bad
	}
	if netguard.CheckURL(raw) != nil {
		return bad
	}
	return nil
}

// applyJEV writes a validated plan, the route before the key, so a save that
// sets both never leaves a moment where the new key is paired with the old
// route.
func (s *SmartServer) applyJEV(ctx context.Context, sc store.Scope, plan jevPlan) error {
	if plan.provider != nil {
		if err := s.settings.SetSystemValue(ctx, store.KeyJEVProvider, *plan.provider, sc.UserID); err != nil {
			return errKey(codes.Internal, "srv.saveFailed", "Couldn't save that.", nil)
		}
		s.log.Info("smart+ decision route changed", "provider", *plan.provider, "by", sc.UserID)
	}
	if plan.baseURL != nil {
		var err error
		if *plan.baseURL == "" {
			err = s.settings.DeleteSystemValue(ctx, store.KeyJEVBaseURL)
		} else {
			err = s.settings.SetSystemValue(ctx, store.KeyJEVBaseURL, *plan.baseURL, sc.UserID)
		}
		if err != nil {
			return errKey(codes.Internal, "srv.saveFailed", "Couldn't save that.", nil)
		}
		// Logged in full (it can carry no credentials — checkDeciderBaseURL
		// refuses them): it is a destination for article text, which is what
		// every other line at this level is about.
		s.log.Info("smart+ decision base URL changed", "url", *plan.baseURL, "by", sc.UserID)
	}
	switch {
	case plan.clearKey:
		if err := s.settings.SetSystemSecret(ctx, store.KeyJEVAPIKey, "", sc.UserID); err != nil {
			return s.jevSecretErr(err)
		}
		s.log.Info("smart+ jev key cleared", "by", sc.UserID)
	case plan.key != "":
		if err := s.settings.SetSystemSecret(ctx, store.KeyJEVAPIKey, plan.key, sc.UserID); err != nil {
			return s.jevSecretErr(err)
		}
		s.log.Info("smart+ jev key set", "by", sc.UserID)
	}
	return nil
}

// jevState is the JEV configuration as the ladder will see it: the route with
// its default applied, and the key wherever it came from.
type jevState struct {
	provider, base string
	key            string
	fromEnv        bool
}

// jevStored reads the JEV credential the way internal/app's DeciderKeyFunc
// resolves it for a call — the sealed setting first, then
// SCHEMAFLUX_TYPESAFE_API_KEY, then TYPESAFE_API_KEY — so the screen reports
// the credential the ladder will actually use.
func (s *SmartServer) jevStored(ctx context.Context) jevState {
	st := jevState{provider: jevDefaultProvider}
	if p, err := s.settings.SystemValue(ctx, store.KeyJEVProvider); err == nil && strings.TrimSpace(p) != "" {
		st.provider = strings.TrimSpace(p)
	}
	if b, err := s.settings.SystemValue(ctx, store.KeyJEVBaseURL); err == nil {
		st.base = strings.TrimSpace(b)
	}
	k, err := s.settings.SystemSecret(ctx, store.KeyJEVAPIKey)
	if err != nil && !errors.Is(err, store.ErrNoSetting) && !errors.Is(err, secret.ErrKeyLength) {
		// The OpenAI key's reason: undecryptable reads as absent here, and is
		// logged, because pasting it again works and abandons the old one.
		s.log.Warn("stored JEV key could not be read", "err", err)
	}
	st.key = strings.TrimSpace(k)
	if st.key == "" {
		for _, env := range []string{"SCHEMAFLUX_TYPESAFE_API_KEY", "TYPESAFE_API_KEY"} {
			if v := strings.TrimSpace(os.Getenv(env)); v != "" {
				st.key, st.fromEnv = v, true
				break
			}
		}
	}
	return st
}

// jevSecretErr is secretErr for the JEV key, whose environment fallback has a
// different name — telling an owner to set OPENAI_API_KEY for a JEV key would
// be advice that changes nothing.
func (s *SmartServer) jevSecretErr(err error) error {
	if errors.Is(err, secret.ErrKeyLength) {
		return errKey(codes.FailedPrecondition, "srv.cannotStoreJevSecret",
			"This server has no encryption key, so it will not store a credential. "+
				"Set SCHEMAFLUX_TYPESAFE_API_KEY in the environment instead.", nil)
	}
	s.log.Error("couldn't save the JEV key", "err", err)
	return errKey(codes.Internal, "srv.saveFailed", "Couldn't save that.", nil)
}

// ListModels asks the provider which model ids this key can use.
//
// Fails soft on the provider call: a picker that cannot populate itself
// falls back to `current`/`default_model` alone, which the client renders as
// the free-text field this replaces — an operator can still type a model id
// by hand when the live list cannot be fetched, and a listing failure must
// never make the model unconfigurable.
func (s *SmartServer) ListModels(ctx context.Context, _ *pb.ListModelsRequest) (
	*pb.ListModelsResponse, error) {
	if _, err := s.requireOwner(ctx); err != nil {
		return nil, err
	}
	out := &pb.ListModelsResponse{DefaultModel: llm.DefaultModel}
	if m, err := s.settings.SystemValue(ctx, store.KeySmartModel); err == nil {
		out.Current = m
	}
	if s.llm == nil || !s.llm.Configured(ctx) {
		return out, nil
	}
	models, err := s.llm.Models(ctx)
	if err != nil {
		// Logged, not returned: the screen still has current/default to show,
		// and a provider hiccup on a listing call must not make the model
		// field disappear from a settings screen that otherwise loaded fine.
		s.log.Warn("listing smart+ models", "err", err)
		// The one failure worth saying out loud: the provider has looked at the
		// stored key and refused it. Everything else about a listing is a hiccup
		// the picker can absorb; this is the key row's "ready" being untrue (G9).
		out.KeyRejected = errors.Is(err, llm.ErrKeyRejected)
		return out, nil
	}
	out.Models = models
	return out, nil
}

// ListLanguages is readable by any signed-in reader, which is what the
// capability map (CapReadItems) always said and this method's own owner check
// did not. Coverage is a fact about the application's text, the same cache
// GetUIStrings already serves a member; and the article-translation flag is
// the one thing a member needs from the Smart+ config, without the config.
func (s *SmartServer) ListLanguages(ctx context.Context, _ *pb.ListLanguagesRequest) (
	*pb.ListLanguagesResponse, error) {
	if _, err := s.requireReader(ctx); err != nil {
		return nil, err
	}
	cov := s.tr.Coverage(ctx)
	out := make([]*pb.SmartLanguage, 0, len(smart.Languages))
	for _, l := range smart.Languages {
		c := cov[l.Code]
		out = append(out, &pb.SmartLanguage{
			Code:        l.Code,
			EnglishName: l.English,
			NativeName:  l.Native,
			Cached:      c.Cached,
			Translated:  int32(c.Done),
			Total:       int32(c.Total),
		})
	}
	// Consent AND a key, read the way config() reads each — the same
	// two-gate order smart.ArticleTranslator.Translate refuses in, so this can
	// only say "on" when a press would actually be attempted.
	consent := false
	if v, err := s.settings.SystemValue(ctx, smart.KeyTranslateArticles); err == nil {
		consent = strings.TrimSpace(v) == "true"
	}
	return &pb.ListLanguagesResponse{Languages: out, SourceLocale: i18n.DefaultLocale,
		TranslateArticlesEnabled: consent && s.llm != nil && s.llm.Configured(ctx)}, nil
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
		return nil, errKey(codes.InvalidArgument, "srv.unsupportedLanguage",
			fmt.Sprintf("%q is not one of the offered languages.", locale),
			map[string]string{"locale": locale})
	case errors.Is(err, smart.ErrIsSource):
		return nil, errKey(codes.InvalidArgument, "srv.alreadySourceLanguage",
			"The interface is already in English.", nil)
	case errors.Is(err, llm.ErrNotConfigured):
		return nil, errKey(codes.FailedPrecondition, "srv.smartNoKey",
			"Smart+ needs an OpenAI API key. Add one in Settings › Smart+.", nil)
	case errors.Is(err, llm.ErrTruncated):
		// Named, because the remedy is specific and nobody would guess it: the
		// batch was too big for the model's output ceiling.
		return nil, errKey(codes.ResourceExhausted, "srv.translationTruncated",
			"The translation was cut off before it finished. Try again.", nil)
	case errors.Is(err, llm.ErrCircuitOpen):
		return nil, smartPaused()
	case err != nil:
		// The provider's text can echo the request, so it goes to the log with
		// the request id and a flat message goes to the client (§20.7).
		s.log.Error("ui translation failed", "locale", locale, "err", err)
		return nil, errKey(providerFailed, "srv.translateFailed",
			"Couldn't translate the interface just now.", nil)
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
	// Anything but "true" is off, which is how the classifier itself reads it —
	// two implementations of "is this on" is how a screen comes to disagree with
	// the job.
	if v, err := s.settings.SystemValue(ctx, smart.KeyClassifyEnabled); err == nil {
		out.ClassifyEnabled = strings.TrimSpace(v) == "true"
	}
	if v, err := s.settings.SystemValue(ctx, smart.KeyTranslateArticles); err == nil {
		out.TranslateArticlesEnabled = strings.TrimSpace(v) == "true"
	}
	if v, err := s.settings.SystemValue(ctx, store.KeySmartBudgetUSD); err == nil {
		// An unparseable row reads as no ceiling, matching what the middleware
		// does with it. A screen that showed a limit the enforcement does not
		// apply would be worse than one that shows none.
		if usd, perr := strconv.ParseFloat(strings.TrimSpace(v), 64); perr == nil && usd > 0 {
			out.BudgetUsd = usd
		}
	}
	// Money, beside the tokens. See llm.Cost for why the two counts travel with
	// it: an unpriced call is not a free one, and the screen has to be able to
	// say which it saw.
	if s.llm != nil {
		cost := s.llm.Cost()
		out.CostUsd = cost.USD
		out.PricedCalls = int64(cost.Priced)
		out.UnpricedCalls = int64(cost.Unpriced)
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
	// What was actually observed, beside what can be cheaply asserted. Evidence
	// rather than a second opinion: set when a call fails, cleared when one
	// succeeds, so the screen speaks about the most recent attempt rather than
	// the worst one. A CLASS, never the provider's message — that can quote the
	// article being read aloud (§22.11).
	if s.refusalOf != nil {
		if kind, at := s.refusalOf(); kind != "" {
			out.LastError = kind
			out.LastErrorAt = at.UTC().Format(time.RFC3339)
		}
	}
	if n := len(key); n >= 4 {
		out.KeyHint = key[n-4:]
	}

	// Rung 2, reported the way the key above is: presence, the last four, and
	// where it came from — never the key.
	jev := s.jevStored(ctx)
	out.JevConfigured = jev.key != ""
	out.JevFromEnvironment = jev.fromEnv
	if n := len(jev.key); n >= 4 {
		out.JevKeyHint = jev.key[n-4:]
	}
	out.JevProvider, out.JevBaseUrl = jev.provider, jev.base
	_, knownRoute := llm.DeciderHosts[jev.provider]
	out.JevReady = out.JevConfigured && knownRoute && (jev.provider != "jev-litellm" || jev.base != "")

	u := s.llm.Usage()
	out.InputTokens, out.OutputTokens, out.Requests = u.InputTokens, u.OutputTokens, u.Requests

	// The voice's half of the same question. `tts.Usage` is nil-safe, so an
	// instance that never configured speech reports zeroes rather than being
	// absent from the screen — a missing number reads as "not measured", which
	// is a different claim from "nothing spent".
	v := s.tts.Usage()
	out.SpeechRequests = int64(v.Requests)
	out.SpeechCharacters = int64(v.Characters)
	out.SpeechCached = int64(v.Cached)
	return out
}

// secretErr turns a storage refusal into something the screen can act on.
func (s *SmartServer) secretErr(err error) error {
	if errors.Is(err, secret.ErrKeyLength) {
		return errKey(codes.FailedPrecondition, "srv.cannotStoreSecret",
			"This server has no encryption key, so it will not store a credential. "+
				"Set OPENAI_API_KEY in the environment instead.", nil)
	}
	s.log.Error("couldn't save the Smart+ key", "err", err)
	return errKey(codes.Internal, "srv.saveFailed", "Couldn't save that.", nil)
}
