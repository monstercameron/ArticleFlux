//go:build js && wasm

package data

import (
	"context"
	"time"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// The Smart+ control surface (§10.5, §18).
//
// Separated from client.go because these are the calls that spend money, and a
// reader of this package should be able to see the whole of that in one file.

// translateTimeout bounds a first translation.
//
// Far longer than any other call in this package, because it is a different
// kind of operation: a few hundred UI messages through a language model, in
// batches, is tens of seconds of legitimate work. The per-RPC deadline every
// other call uses would abandon it halfway and charge for the batches that had
// already completed.
const translateTimeout = 5 * time.Minute

// SmartConfig reports whether Smart+ can run, and what it has spent.
func (c *Client) SmartConfig(parent context.Context) (*pb.GetSmartConfigResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.smart.GetSmartConfig(ctx, &pb.GetSmartConfigRequest{})
	return res, c.track(err)
}

// SetSmartConfig stores the API key and/or the model.
//
// An empty key with clear=false leaves the stored one alone, which is what lets
// the settings form save a model change without the reader re-pasting a
// credential they cannot see.
func (c *Client) SetSmartConfig(parent context.Context, key string, clear bool, model string) (
	*pb.GetSmartConfigResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.smart.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{
		OpenaiApiKey: key, ClearApiKey: clear, Model: model,
	})
	if err != nil {
		return nil, c.track(err)
	}
	return res.GetConfig(), nil
}

// SetTranslateArticles records the owner's consent for article translation
// (§10.5b, `smart.translate_articles`) and nothing else.
//
// Its own call rather than another argument on SetSmartConfig, because the
// field is optional on the wire for exactly this reason: a save that carries
// it is a decision about egress, and every other save on the Smart+ tab — a
// key, a model — must go out without it so that it cannot make one by
// accident.
func (c *Client) SetTranslateArticles(parent context.Context, on bool) (*pb.GetSmartConfigResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.smart.SetSmartConfig(ctx, &pb.SetSmartConfigRequest{TranslateArticlesEnabled: &on})
	if err != nil {
		return nil, c.track(err)
	}
	return res.GetConfig(), nil
}

// SetJEVConfig stores the JEV decision credential and/or its route (A47) and
// nothing else — SetTranslateArticles' reason, the other way round: a JEV save
// must not carry the OpenAI key's fields, and the OpenAI key's saves already
// go out without these. See JEVChange.Request for the field contract.
func (c *Client) SetJEVConfig(parent context.Context, ch JEVChange) (*pb.GetSmartConfigResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.smart.SetSmartConfig(ctx, ch.Request())
	if err != nil {
		return nil, c.track(err)
	}
	return res.GetConfig(), nil
}

// SmartLanguages lists the offered UI translation targets.
func (c *Client) SmartLanguages(parent context.Context) (*pb.ListLanguagesResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.smart.ListLanguages(ctx, &pb.ListLanguagesRequest{})
	return res, c.track(err)
}

// SmartModels lists the model ids this instance's key can use, for the
// picker on the Smart tab.
func (c *Client) SmartModels(parent context.Context) (*pb.ListModelsResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.smart.ListModels(ctx, &pb.ListModelsRequest{})
	return res, c.track(err)
}

// TranslateUI fetches the whole UI catalog in one language and REGISTERS it.
//
// Registering here rather than at the call site is deliberate. The catalog is
// only useful once it is in the bundle, and a caller that fetched it and forgot
// to import it would get a language switch that appeared to work and changed
// nothing — the worst possible failure for this feature, because it looks like
// a translation that came back in English.
//
// It returns whether the catalog was already cached on the server, which is the
// difference between a switch that was free and one that just spent money.
func (c *Client) TranslateUI(parent context.Context, locale string, force bool) (bool, error) {
	// Deliberately NOT c.ctx: that applies the short per-RPC deadline every
	// other call uses, and a first translation is a few hundred messages
	// through a language model — tens of seconds, legitimately. A reader who
	// asked for French should wait for French rather than be told the server is
	// unreachable.
	ctx, cancel := context.WithTimeout(parent, translateTimeout)
	defer cancel()

	res, err := c.smart.TranslateUI(ctx, &pb.TranslateUIRequest{Locale: locale, Force: force})
	if err != nil {
		return false, c.track(err)
	}

	entries := make([]i18n.Entry, 0, len(res.GetMessages()))
	for _, m := range res.GetMessages() {
		entries = append(entries, i18n.Entry{
			Key: m.GetKey(), Text: m.GetText(), Plural: m.GetForms(),
		})
	}
	i18n.Import(res.GetLocale(), entries)
	return res.GetCached(), nil
}

// FillUIStrings brings the catalog for locale up to date on the fly
// (§22.16c): it asks for exactly the keys this bundle lacks, merges what the
// server has cached, and — when fill is set and the caller is an owner — asks
// the server to translate the rest in the background, polling until it is done.
//
// The reader sees English for every missing key the whole time, so this is
// safe to start right after the locale is set rather than before. onMerged is
// called after each round that changed the catalog; the caller nudges Root
// from it (via ui.PostAsync) so memoised panes pick the new strings up without
// a reload. It returns when nothing is pending, the server will not fill
// (FillResult.IdleReason says why), or ctx ends.
//
// Each request is an ordinary short RPC — the server never waits on the model
// in this path — so the per-RPC deadline applies to each round.
func (c *Client) FillUIStrings(ctx context.Context, locale string, fill bool,
	onMerged func(applied int)) (FillResult, error) {
	fetch := func(parent context.Context, req *pb.GetUIStringsRequest) (*pb.GetUIStringsResponse, error) {
		rctx, cancel := c.ctx(parent)
		defer cancel()
		res, err := c.smart.GetUIStrings(rctx, req)
		return res, c.track(err)
	}
	return runUIFill(ctx, fetch, locale, fill, onMerged, nil)
}

// TranslateArticle asks for one article in the reader's language (§10.5b).
//
// The response carries the original beside the translation, so the reader can
// toggle without a second request. A cached translation answers at once; a
// fresh one is a model call over the whole article, so this uses the long
// translation deadline rather than the per-RPC one, for TranslateUI's reason.
func (c *Client) TranslateArticle(parent context.Context, itemID, locale string, force bool) (
	*pb.TranslateArticleResponse, error) {
	ctx, cancel := context.WithTimeout(parent, translateTimeout)
	defer cancel()
	res, err := c.smart.TranslateArticle(ctx, &pb.TranslateArticleRequest{
		ItemId: itemID, Locale: locale, Force: force,
	})
	return res, c.track(err)
}

// CachedArticleTranslation asks whether a translation of the article into
// locale has already been paid for, and returns it when one has.
//
// It can never spend: the request carries cache_only, which the server answers
// from its cache or not at all — no provider, no consent, no key, no rate
// limit. So it is safe to ask on an instance where fresh translation is off,
// which is exactly where it is for: a translation somebody else already made is
// readable whatever the owner's consent says now. ok is false for a miss, and
// a miss is not an error.
//
// The per-RPC deadline, not the translation one: nothing here waits on a
// model.
func (c *Client) CachedArticleTranslation(parent context.Context, itemID, locale string) (
	res *pb.TranslateArticleResponse, ok bool, err error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err = c.smart.TranslateArticle(ctx, CachedTranslationRequest(itemID, locale))
	if err != nil {
		return nil, false, c.track(err)
	}
	return res, CachedTranslationHit(res), nil
}
