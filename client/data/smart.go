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
