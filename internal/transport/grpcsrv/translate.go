package grpcsrv

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/apierr"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/ratelimit"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// On-the-fly translation (plan.md §22.16c UI strings, §10.5b articles).
//
// The line drawn here is the one theme.go draws: READING something already paid
// for is open to any signed-in reader, because it costs nothing and sends
// nothing; SPENDING is gated. For the UI catalog the spend stays owner-only, as
// TranslateUI always was. For an article the owner decides once, with the
// `smart.translate_articles` consent, and readers then spend within a per-user
// limit — an article in your language is your own reading experience, the same
// argument the Smart+ voice and theming make.

// articles returns the article translator, built on first use from what the
// server already holds. Nil when the reader repository was never installed
// (WithTheming), which is an instance that cannot translate articles.
func (s *SmartServer) articles() *smart.ArticleTranslator {
	s.articleOnce.Do(func() {
		if s.reader == nil {
			return
		}
		s.articleTr = smart.NewArticleTranslator(s.llm, s.settings, s.reader)
		if s.translateLimit == nil {
			s.translateLimit = ratelimit.New(ratelimit.Options{})
		}
	})
	return s.articleTr
}

// GetUIStrings answers from the UI translation cache and, for an owner, starts
// filling what it lacks.
func (s *SmartServer) GetUIStrings(ctx context.Context, req *pb.GetUIStringsRequest) (
	*pb.GetUIStringsResponse, error) {
	sc, err := s.requireReader(ctx)
	if err != nil {
		return nil, err
	}
	locale := strings.TrimSpace(req.GetLocale())
	entries, pending, err := s.tr.Strings(ctx, locale, req.GetKeys())
	if err != nil {
		return nil, s.localeErr(locale, err)
	}
	out := &pb.GetUIStringsResponse{Locale: locale, Pending: int32(pending)}
	for _, e := range entries {
		out.Messages = append(out.Messages, &pb.UIMessage{
			Key: e.Key, Text: e.Text, Forms: e.Plural, SourceHash: e.SourceHash,
		})
	}
	out.Filling = s.tr.Filling(locale)
	if pending == 0 || out.Filling {
		return out, nil
	}
	switch {
	case !req.GetFill():
	case !ownerRoles[sc.Role]:
		// Not an error: the reader still gets every cached string, and the rest
		// in English. Starting a translation re-bills the instance, which is the
		// owner's decision (§10.5a).
		out.IdleReason = "not_owner"
	default:
		running, ferr := s.tr.StartFill(ctx, locale)
		switch {
		case errors.Is(ferr, llm.ErrNotConfigured):
			out.IdleReason = "no_key"
		case ferr != nil:
			return nil, s.localeErr(locale, ferr)
		default:
			out.Filling = running
		}
	}
	return out, nil
}

// TranslateArticle returns an article in the reader's language.
func (s *SmartServer) TranslateArticle(ctx context.Context, req *pb.TranslateArticleRequest) (
	*pb.TranslateArticleResponse, error) {
	sc, err := s.requireReader(ctx)
	if err != nil {
		return nil, err
	}
	at := s.articles()
	if at == nil {
		return nil, errKey(codes.FailedPrecondition, "srv.articleTranslateOff",
			"Translating articles is not enabled on this server.", nil)
	}
	locale := strings.TrimSpace(req.GetLocale())

	// A cache hit first, before the limit: re-reading a translation already
	// paid for is not what the limit protects.
	//
	// cache_only stops here whatever happens, and that is the whole of its
	// guarantee: at.Cached never reaches the provider (it reads the item and the
	// cache row, both through the caller's scope), so a lookup needs no consent
	// and no key and cannot spend. It wins over force, which asks for exactly
	// the call this flag promises not to make.
	if req.GetCacheOnly() || !req.GetForce() {
		got, ok, cerr := at.Cached(ctx, sc, req.GetItemId(), locale)
		if cerr != nil {
			return nil, s.articleErr(locale, cerr)
		}
		if ok {
			return s.articleResponse(ctx, sc, got), nil
		}
		if req.GetCacheOnly() {
			// An answer, not a refusal: "nobody has paid for this yet" is the
			// common case, and an error here would put a failure on screen for a
			// question the client asked only to decide what to offer.
			return &pb.TranslateArticleResponse{ItemId: got.ItemID, Locale: got.Locale, NotCached: true}, nil
		}
	}
	if ok, wait := s.translateLimit.Allow(ratelimit.ArticleTranslatePerUser, sc.UserID); !ok {
		return nil, apierr.Status(apierr.RateLimited(ratelimit.ArticleTranslatePerUser.Name, wait))
	}
	got, err := at.Translate(ctx, sc, req.GetItemId(), locale, req.GetForce())
	if err != nil {
		return nil, s.articleErr(locale, err)
	}
	return s.articleResponse(ctx, sc, got), nil
}

// articleResponse puts a translation on the wire, with both bodies' images
// repointed at the local proxy the way GetItem's are (§10.1a).
//
// Here, on the way out, and not in the cache: the cache row is shared by every
// reader who can see the item, the proxy URLs are minted with an expiry, and
// whether to proxy at all is each reader's own `proxy.images` preference. A
// translation that fetched its pictures straight from the publisher would be
// the one article view that ignored that preference.
func (s *SmartServer) articleResponse(ctx context.Context, sc store.Scope, t smart.ArticleTranslation) *pb.TranslateArticleResponse {
	body, orig := t.BodyHTML, t.OriginalHTML
	if s.mintAsset != nil && s.reader != nil {
		// The item's own URL is what relative references resolve against. The
		// translator has just read the item through this scope, so a failure
		// here is a race with an unsubscribe, and the answer degrades to the
		// publisher's URLs exactly as GetItem's does.
		if it, err := s.reader.GetItem(ctx, sc, t.ItemID); err == nil {
			optOut := false
			if prefs, perr := s.reader.GetPrefs(ctx, sc); perr == nil && prefs[proxyImagesPref] == "false" {
				optOut = true
			}
			// One mint per URL for both bodies. A minted URL carries its expiry
			// to the second, so minting the same picture twice can give two
			// different strings — and a client pairing the translation with its
			// original by reference would then see a picture the original
			// "does not have".
			minted := map[string]string{}
			mint := func(abs string) string {
				if v, ok := minted[abs]; ok {
					return v
				}
				v := s.mintAsset(abs)
				minted[abs] = v
				return v
			}
			body = proxySubresources(mint, it.URL, optOut, body)
			orig = proxySubresources(mint, it.URL, optOut, orig)
		}
	}
	return &pb.TranslateArticleResponse{
		ItemId: t.ItemID, Locale: t.Locale, SourceLocale: t.SourceLocale,
		Title: t.Title, ContentHtml: body,
		OriginalTitle: t.OriginalTitle, OriginalContentHtml: orig,
		Cached: t.Cached, Partial: t.Partial,
	}
}

// localeErr maps the translator's locale refusals.
func (s *SmartServer) localeErr(locale string, err error) error {
	switch {
	case errors.Is(err, smart.ErrUnsupportedLanguage):
		return errKey(codes.InvalidArgument, "srv.unsupportedLanguage",
			fmt.Sprintf("%q is not one of the offered languages.", locale),
			map[string]string{"locale": locale})
	case errors.Is(err, smart.ErrIsSource):
		return errKey(codes.InvalidArgument, "srv.alreadySourceLanguage",
			"The interface is already in English.", nil)
	}
	s.log.Error("ui translation lookup failed", "locale", locale, "err", err)
	return errKey(codes.Unavailable, "srv.translateFailed",
		"Couldn't translate the interface just now.", nil)
}

// articleErr maps an article translation's failures to keyed refusals. The
// provider's own text never goes to the client: it can quote the article
// (§22.11), so it is logged and a class is sent.
func (s *SmartServer) articleErr(locale string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrNoScope):
		return toStatus(err)
	case errors.Is(err, smart.ErrUnsupportedLanguage):
		return errKey(codes.InvalidArgument, "srv.unsupportedLanguage",
			fmt.Sprintf("%q is not one of the offered languages.", locale),
			map[string]string{"locale": locale})
	case errors.Is(err, smart.ErrSameLanguage):
		return errKey(codes.FailedPrecondition, "srv.articleSameLanguage",
			"This article is already in that language.", nil)
	case errors.Is(err, smart.ErrNothingToTranslate):
		return errKey(codes.FailedPrecondition, "srv.articleNothingToTranslate",
			"This article has no text to translate.", nil)
	case errors.Is(err, smart.ErrTranslateOff):
		return errKey(codes.FailedPrecondition, "srv.articleTranslateOff",
			"Translating articles is not enabled on this server.", nil)
	case errors.Is(err, llm.ErrNotConfigured):
		return errKey(codes.FailedPrecondition, "srv.smartNoKey",
			"Smart+ needs an OpenAI API key. Add one in Settings › Smart+.", nil)
	case errors.Is(err, llm.ErrOverBudget):
		return errKey(codes.ResourceExhausted, "srv.smartOverBudget",
			"This server's Smart+ spend limit has been reached.", nil)
	case errors.Is(err, llm.ErrTruncated):
		return errKey(codes.ResourceExhausted, "srv.translationTruncated",
			"The translation was cut off before it finished. Try again.", nil)
	}
	s.log.Error("article translation failed", "locale", locale, "err", err)
	return errKey(codes.Unavailable, "srv.articleTranslateFailed",
		"Couldn't translate this article just now.", nil)
}
