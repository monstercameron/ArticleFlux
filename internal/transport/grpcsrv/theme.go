package grpcsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/client/design"
	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/themewire"
	"github.com/monstercameron/ArticleFlux/internal/topics"
)

// The theming RPCs (§20.16.3).
//
// # These are the first non-owner methods on SmartService, and the line is drawn
// where the voice drew it
//
// SmartServer's own doc comment says every method is owner-only, because the API
// key is instance configuration and a UI translation re-bills the whole instance
// for everybody. Neither is true here. A theme is a per-user preference — the same
// category as `tts.smartPlus`, which a member may switch on for themselves — and
// the spend is bounded by a per-user rate limit rather than by a role.
//
// Refusing a member would also make the feature nonsense on any instance with more
// than one account: "you may not choose what your own reader looks like" is not a
// security posture, and the reader who cannot use it is exactly the reader who is
// not going to notice the setting exists.
//
// What a member still cannot do, unchanged: read the key hint, change the model,
// see the spend counters, or start a translation.

// AttunePrefKey is the per-user opt-in for a MODEL-written drift target.
//
// Its own key rather than a reuse of `feed.smartPlus`, for the reason
// smartsettings.go gives for that one: storing an API key is not consent to use
// it, and consenting to one egress is not consenting to the next. Someone who
// agreed to have their homepage re-ranked has not agreed to have a palette written
// for them.
//
// Absent or anything but "true" means the deterministic answer, which is a complete
// feature: the base theme tinted toward the hue of what the reader reads, computed
// in Go, costing nothing and leaving the machine not at all.
const AttunePrefKey = "ui.attune.smart"

// WithTheming installs the palette generator and the repository the reader's
// interests are read from.
//
// Optional and separate from the constructor, like WithSpeechMeter and for the same
// reason: an instance with no OpenAI key still serves this surface — the
// deterministic drift needs no model at all — and a constructor that demanded a
// generator would make the absence of a paid feature a wiring error.
func (s *SmartServer) WithTheming(p *smart.Palettes, repo *store.ReaderRepo) *SmartServer {
	s.palettes = p
	s.reader = repo
	// One limiter for the whole surface, created here so nothing else can reach
	// into it. Keyed on the user id rather than on the credential — unlike
	// app.rateKey, which cannot afford a lookup on a 600-a-minute path, this rule
	// is measured in requests per hour and the scope has already been resolved by
	// the time it is consulted, so the accurate key is free.
	s.themeLimit = ratelimit.New(ratelimit.Options{})
	return s
}

// requireReader resolves the caller and refuses anyone not signed in.
//
// Deliberately NOT requireOwner. See the file comment: a theme is the reader's own
// preference, and the control on the spend is themeLimit, not a role.
func (s *SmartServer) requireReader(ctx context.Context) (store.Scope, error) {
	if s.scopeOf == nil {
		return store.Scope{}, errKey(codes.Unauthenticated, "srv.notSignedIn", "not signed in", nil)
	}
	sc, err := s.scopeOf(ctx)
	if err != nil || !sc.Valid() {
		return store.Scope{}, errKey(codes.Unauthenticated, "srv.notSignedIn", "not signed in", nil)
	}
	return sc, nil
}

// allowTheme applies the per-user budget.
//
// The number is in internal/ratelimit with the rest of the table. What it is
// protecting against is not abuse so much as enthusiasm: a reader iterating on a
// prompt will press the button ten times in two minutes, which is legitimate and
// should work, and then there is no legitimate reason for the eleventh hundred.
func (s *SmartServer) allowTheme(sc store.Scope) error {
	if s.themeLimit == nil {
		return nil
	}
	ok, wait := s.themeLimit.Allow(ratelimit.ThemePerUser, sc.UserID)
	if ok {
		return nil
	}
	// Through apierr's own constructor rather than a keyed message of this
	// package's, because it is the one that attaches `retry_after_s` — and §20.7's
	// point about that field is that a client left to invent its own backoff turns
	// a rate limit into a hot loop against the thing that was already limited.
	return apierr.Status(apierr.RateLimited(ratelimit.ThemePerUser.Name, wait))
}

// ComposeTheme turns the reader's words into a palette.
func (s *SmartServer) ComposeTheme(ctx context.Context, req *pb.ComposeThemeRequest) (
	*pb.ComposeThemeResponse, error) {

	sc, err := s.requireReader(ctx)
	if err != nil {
		return nil, err
	}
	if s.palettes == nil {
		return nil, errKey(codes.FailedPrecondition, "srv.smartNoKey",
			"Smart+ needs an OpenAI API key — add one in Settings › Smart+", nil)
	}
	prompt := strings.TrimSpace(req.GetPrompt())
	if prompt == "" {
		// Refused before the rate limit, so an empty box does not spend a slot the
		// reader will want for the prompt they meant to type.
		return nil, errKey(codes.InvalidArgument, "srv.themeNoPrompt",
			"describe the theme you want in a few words", nil)
	}
	if err := s.allowTheme(sc); err != nil {
		return nil, err
	}

	theme, reps, err := s.palettes.Compose(ctx, prompt, toneOf(req.GetTone()))
	if err != nil {
		return nil, s.themeErr(err, "compose")
	}
	// The client is told whether its prompt was cut, computed from the same cap
	// the payload applies. A theme that answers half a sentence looks exactly like
	// a model that misunderstood the whole one.
	_, trimmed := llm.ThemePayload{Prompt: prompt, Tone: req.GetTone()}.Trim()
	return &pb.ComposeThemeResponse{
		Theme:         themewire.Tokens(theme),
		Repairs:       themewire.Repairs(reps),
		PromptTrimmed: trimmed,
	}, nil
}

// SuggestTheme returns where an attuning theme should be drifting to.
//
// The ladder is §11's and §18.7's, unchanged: **deterministic first, model last.**
// The free answer is the reader's own theme tinted toward the hue of what they
// read, and it is a complete answer — the model's version is better, not
// load-bearing. So every failure below falls through to the deterministic target
// rather than out to the caller, and the response says which one it is.
func (s *SmartServer) SuggestTheme(ctx context.Context, req *pb.SuggestThemeRequest) (
	*pb.SuggestThemeResponse, error) {

	sc, err := s.requireReader(ctx)
	if err != nil {
		return nil, err
	}
	base, err := themewire.Theme(req.GetBase())
	if err != nil {
		// The client's own current palette did not survive validation, which means
		// the client is running a theme this server would refuse to store. Named,
		// because the remedy — pick a theme again — is specific and nobody would
		// guess it from "invalid argument".
		return nil, errKey(codes.InvalidArgument, "srv.themeBadBase",
			"the theme you are using could not be read", nil)
	}
	if err := s.allowTheme(sc); err != nil {
		return nil, err
	}

	labels, why := s.taste(ctx, sc)
	if len(labels) == 0 {
		// No topics yet. Not an error: it is the cold start (§18.4), and the honest
		// answer is that there is nothing to attune to — the client keeps whatever
		// target it has and says so on the screen.
		return &pb.SuggestThemeResponse{}, nil
	}
	sig := tasteSignature(labels)

	// The deterministic rung. Computed first and unconditionally, so it is the
	// value in hand when anything above it fails.
	target := design.Attune(base, design.AttuneHue(labels[0]), 1)
	usedSmart := false
	var reps []design.Repair

	if s.palettes != nil && s.attuneSmart(ctx, sc) {
		if t, r, err := s.palettes.Attune(ctx, labels, base.Tone); err == nil {
			target, reps, usedSmart = t, r, true
		} else if s.log != nil {
			// Info, not Warn, for the reason internal/derive gives about the same
			// class of event: no key, no network and a provider outage are all
			// ordinary, and logging them as warnings trains an operator to ignore
			// warnings.
			s.log.Info("smart+ palette declined; keeping the deterministic drift",
				"err", err)
		}
	}

	// Validated even on the deterministic path. design.Attune holds luminance so
	// it should not be able to break the floor, and "should not be able to" is
	// exactly the claim that deserves the assertion rather than the comment.
	safe, extra, err := design.Sanitize(target)
	if err != nil {
		if s.log != nil {
			s.log.Warn("drift target failed the readability floor", "err", err)
		}
		return &pb.SuggestThemeResponse{}, nil
	}
	reps = append(reps, extra...)

	return &pb.SuggestThemeResponse{
		Target:    themewire.Tokens(safe),
		Why:       why,
		Signature: sig,
		Smart:     usedSmart,
		Repairs:   themewire.Repairs(reps),
	}, nil
}

// attuneSmart reports whether this reader agreed to have a palette written for
// them.
//
// Read server-side rather than taken from the request, even though the caller is
// the reader's own client. The switch is stored in one place and it is the
// authority in one place; a flag on the wire would make "is Smart+ theming on"
// answerable two ways, and the answer that spends money should not be the one a
// client asserts.
func (s *SmartServer) attuneSmart(ctx context.Context, sc store.Scope) bool {
	if s.reader == nil {
		return false
	}
	prefs, err := s.reader.GetPrefs(ctx, sc)
	if err != nil {
		// Fail closed. An unreadable preference is not consent.
		return false
	}
	return prefs[AttunePrefKey] == "true"
}

// MaxTasteLabels is how many interests describe a reader here.
//
// It matches llm.MaxThemeInterests, and the cap is applied on both sides on
// purpose: this one decides what the DETERMINISTIC path sees too, and that path
// never touches internal/llm.
const MaxTasteLabels = llm.MaxThemeInterests

// taste is what the reader actually reads, in the order that matters for a drift.
//
// # Rising before large, and dormant not at all
//
// `Topics` returns largest first, which is the right order for a Trends screen and
// the wrong one here. A drifting interface is answering "what are you moving
// toward", not "what have you read the most of over two years" — §18.3's whole
// point is that interests expire, and a theme still tuned to a topic somebody
// stopped reading in March is the interface being confidently out of date about
// its owner.
//
// So rising topics lead, steady and fading follow, and **dormant ones are dropped
// entirely** — a dormant topic is not a faint interest, it is over (the same
// reading internal/topics takes when Explore refuses to serve one). Suppressed
// topics are dropped too, and that one is not an optimisation: marking a topic
// "not an interest" is a reader telling the app it is wrong about them, and a
// feature that then painted their interface in its colours would be the app
// arguing.
func (s *SmartServer) taste(ctx context.Context, sc store.Scope) (labels []string, why string) {
	if s.reader == nil {
		return nil, ""
	}
	rows, err := s.reader.Topics(ctx, sc)
	if err != nil || len(rows) == 0 {
		return nil, ""
	}

	type ranked struct {
		row  store.TopicRow
		rank int
	}
	var keep []ranked
	for _, r := range rows {
		if r.Suppressed || strings.TrimSpace(r.Label) == "" {
			continue
		}
		switch topics.Trend(r.Trend) {
		case topics.TrendDormant:
			continue
		case topics.TrendRising:
			keep = append(keep, ranked{r, 0})
		default:
			keep = append(keep, ranked{r, 1})
		}
	}
	// Stable: rank first, then the order Topics gave, which is member count
	// descending with a label tie-break. A signature that reordered itself between
	// two identical derivations would look like the reader's taste had changed and
	// would restart the drift for nothing.
	sort.SliceStable(keep, func(i, j int) bool { return keep[i].rank < keep[j].rank })

	for _, k := range keep {
		if len(labels) >= MaxTasteLabels {
			break
		}
		labels = append(labels, k.row.Label)
	}
	if len(labels) > 0 {
		why = labels[0]
	}
	return labels, why
}

// tasteSignature fingerprints an ordered label list.
//
// The client stores it and compares it, and that comparison is what stops a
// feature which repaints daily from being a REQUEST which happens daily: the
// target only moves when the taste behind it does. Order is part of the
// fingerprint on purpose — the first label chooses the hue on the deterministic
// path, so two lists with the same members in a different order are two different
// rooms.
//
// Truncated to sixteen hex characters. It is a cache key, not a credential, and it
// travels in a preference value that is read on every boot.
func tasteSignature(labels []string) string {
	sum := sha256.Sum256([]byte(strings.Join(labels, "\x00")))
	return hex.EncodeToString(sum[:8])
}

// themeErr turns a generator failure into something the screen can act on.
//
// The provider's own text can echo the request, so it goes to the log with the
// request id and a flat keyed message goes to the client (§20.7).
func (s *SmartServer) themeErr(err error, what string) error {
	switch {
	case errors.Is(err, llm.ErrNotConfigured):
		return errKey(codes.FailedPrecondition, "srv.smartNoKey",
			"Smart+ needs an OpenAI API key — add one in Settings › Smart+", nil)
	case errors.Is(err, smart.ErrNoPrompt):
		return errKey(codes.InvalidArgument, "srv.themeNoPrompt",
			"describe the theme you want in a few words", nil)
	case errors.Is(err, llm.ErrTruncated):
		return errKey(codes.ResourceExhausted, "srv.themeTruncated",
			"the palette was cut off before it finished — try again", nil)
	case errors.Is(err, design.ErrNotAPalette):
		// The model answered, and what it answered was not a palette. Named
		// separately from a provider failure because the remedy is different:
		// pressing the button again genuinely helps, where it would not if the key
		// were wrong.
		return errKey(codes.Unavailable, "srv.themeNotAPalette",
			"that came back as something other than a palette — try again", nil)
	}
	if s.log != nil {
		s.log.Error("theme generation failed", "what", what, "err", err)
	}
	return errKey(codes.Unavailable, "srv.themeFailed",
		"couldn't make a theme just now", nil)
}

// --- the wire mapping -------------------------------------------------------------

// toneOf reads a requested tone, defaulting to dark.
//
// Dark rather than "whatever the model prefers": the house theme is dark, so an
// absent tone is almost certainly a client that did not send one rather than a
// reader on paper, and a light palette handed to a dark base cannot be drifted
// toward at all (design.Blend's tone guard).
func toneOf(s string) design.Tone {
	if strings.EqualFold(strings.TrimSpace(s), string(design.ToneLight)) {
		return design.ToneLight
	}
	return design.ToneDark
}

// The Theme↔ThemeTokens conversion lives in internal/themewire, because the
// CLIENT needs it too — it sends the palette it is drifting from and reads the
// target back. Two copies of a hand-written eighteen-field mapping agree until
// somebody adds a token, and then one side silently sends an empty string for it,
// which for a colour means the declaration is dropped and the element inherits.
