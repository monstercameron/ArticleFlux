//go:build js && wasm

package data

import (
	"context"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
)

// /discover (§18.7, M16): sites the reader does not follow yet.

// Recommendations lists the reader's open suggestions.
func (c *Client) Recommendations(parent context.Context) (*pb.ListRecommendationsResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ListRecommendations(ctx, &pb.ListRecommendationsRequest{})
	return res, c.track(err)
}

// RefreshRecommendations enqueues a fresh scoring pass. The pass runs on the
// server's job pool — this call returns as soon as it is queued, and the
// caller re-polls Recommendations to see the result.
func (c *Client) RefreshRecommendations(parent context.Context) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.RefreshRecommendations(ctx, &pb.RefreshRecommendationsRequest{})
	return c.track(err)
}

// AcceptRecommendation subscribes to a suggestion's validated feed URL.
func (c *Client) AcceptRecommendation(parent context.Context, domain string) (*pb.Feed, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.AcceptRecommendation(ctx, &pb.AcceptRecommendationRequest{Domain: domain})
	if err != nil {
		return nil, c.track(err)
	}
	return res.GetFeed(), nil
}

// RejectRecommendation dismisses a suggestion permanently (§18.7).
func (c *Client) RejectRecommendation(parent context.Context, domain string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.RejectRecommendation(ctx, &pb.RejectRecommendationRequest{Domain: domain})
	return c.track(err)
}
