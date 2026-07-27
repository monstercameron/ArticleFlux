//go:build js && wasm

// Package data is the client's connection to the server.
//
// It owns exactly one thing: a gRPC connection over the GoGRPCBridge tunnel, and
// the connection state the UI shows. Everything above it works in domain terms
// and never touches a stub.
//
// Why a tunnel rather than plain gRPC: browsers cannot open the HTTP/2
// connection gRPC-over-HTTP normally requires. The tunnel carries gRPC frames
// over one WebSocket, which is also why the standard Service Worker offline
// recipe does not apply here — a Service Worker cannot see WebSocket frames, so
// offline packs travel over plain HTTPS instead.
package data

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/ArticleFlux/v1"
)

// ConnState is what the always-visible indicator shows.
//
// It exists because a reader that has silently stopped receiving looks exactly
// like a quiet news day, and those two must never be confusable.
type ConnState string

const (
	Connecting ConnState = "connecting"
	Live       ConnState = "live"
	Down       ConnState = "down"
)

// Client wraps the tunnel connection.
type Client struct {
	conn   *grpc.ClientConn
	reader pb.ReaderServiceClient
	system pb.SystemServiceClient

	state   ConnState
	onState func(ConnState)
}

// TunnelURL derives the WebSocket endpoint from the page origin.
//
// Derived rather than configured so the client works unchanged on localhost,
// on a LAN address, and behind a reverse proxy on a real domain. A hardcoded
// host is the single most common reason a self-hosted app works for its author
// and nobody else.
func TunnelURL(origin string) string {
	switch {
	case strings.HasPrefix(origin, "https://"):
		return "wss://" + strings.TrimPrefix(origin, "https://") + "/grpc"
	case strings.HasPrefix(origin, "http://"):
		return "ws://" + strings.TrimPrefix(origin, "http://") + "/grpc"
	default:
		return "ws://127.0.0.1:9000/grpc"
	}
}

// Dial opens the tunnel.
//
// onState is called on every transition so the UI can render the connection
// indicator without polling.
func Dial(ctx context.Context, tunnelURL string, onState func(ConnState)) (*Client, error) {
	c := &Client{state: Connecting, onState: onState}
	c.notify(Connecting)

	conn, err := grpctunnel.DialContext(ctx, tunnelURL,
		// The transport is the WebSocket; TLS is the page's, not gRPC's. Asking
		// gRPC for its own credentials here would double-wrap and fail.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		c.notify(Down)
		return nil, err
	}
	c.conn = conn
	c.reader = pb.NewReaderServiceClient(conn)
	c.system = pb.NewSystemServiceClient(conn)
	c.notify(Live)
	return c, nil
}

func (c *Client) notify(s ConnState) {
	c.state = s
	if c.onState != nil {
		c.onState(s)
	}
}

// State is the current connection state.
func (c *Client) State() ConnState { return c.state }

// Close shuts the tunnel down.
func (c *Client) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// callTimeout bounds a single RPC.
//
// Twenty seconds rather than five: Refresh polls real feeds over the network on
// the server side, and a timeout shorter than a slow publisher's response turns
// a working refresh into a permanent error.
const callTimeout = 20 * time.Second

func (c *Client) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, callTimeout)
}

// track marks the connection down on a transport failure so the indicator tells
// the truth, and live again on any success.
func (c *Client) track(err error) error {
	if err != nil {
		c.notify(Down)
		return err
	}
	if c.state != Live {
		c.notify(Live)
	}
	return nil
}

// ListFeeds returns the sidebar.
func (c *Client) ListFeeds(parent context.Context) (*pb.ListFeedsResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ListFeeds(ctx, &pb.ListFeedsRequest{})
	return res, c.track(err)
}

// ListItems returns a page of items.
func (c *Client) ListItems(parent context.Context, req *pb.ListItemsRequest) (*pb.ListItemsResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ListItems(ctx, req)
	return res, c.track(err)
}

// GetItem returns one item with its content.
func (c *Client) GetItem(parent context.Context, id string) (*pb.Item, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.GetItem(ctx, &pb.GetItemRequest{Id: id})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetItem(), nil
}

// SetItemState marks read/starred. Nil leaves a flag alone.
func (c *Client) SetItemState(parent context.Context, itemID string, read, starred *bool, rating *int32, key string) (*pb.Item, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.SetItemState(ctx, &pb.SetItemStateRequest{
		ItemId: itemID, Read: read, Starred: starred, Rating: rating,
		IdempotencyKey: key,
	})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetItem(), nil
}

// MarkAllRead marks a feed or everything read.
func (c *Client) MarkAllRead(parent context.Context, sourceID string) (int32, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	scope := pb.ListScope_LIST_SCOPE_ALL
	if sourceID != "" {
		scope = pb.ListScope_LIST_SCOPE_FEED
	}
	res, err := c.reader.MarkAllRead(ctx, &pb.MarkAllReadRequest{
		Scope: scope, SourceId: sourceID,
		// The server defaults `before` to now. Sending it explicitly from the
		// client would use the client's clock, which may be wrong.
	})
	if err := c.track(err); err != nil {
		return 0, err
	}
	return res.GetMarked(), nil
}

// Subscribe adds a feed.
func (c *Client) Subscribe(parent context.Context, url string) (*pb.SubscribeResponse, error) {
	// Subscribing polls the feed synchronously on the server, so it gets the
	// full budget rather than the shared one.
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	res, err := c.reader.Subscribe(ctx, &pb.SubscribeRequest{Url: url})
	return res, c.track(err)
}

// Unsubscribe removes a subscription.
func (c *Client) Unsubscribe(parent context.Context, sourceID string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.Unsubscribe(ctx, &pb.UnsubscribeRequest{SourceId: sourceID})
	return c.track(err)
}

// Refresh polls now.
func (c *Client) Refresh(parent context.Context, sourceIDs []string) (*pb.RefreshResponse, error) {
	// A full refresh fans out over every subscribed feed on the server, six at a
	// time, each over the public internet. This is the one call that legitimately
	// takes a while.
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	res, err := c.reader.Refresh(ctx, &pb.RefreshRequest{SourceIds: sourceIDs})
	return res, c.track(err)
}

// Search runs a full-text query.
func (c *Client) Search(parent context.Context, query string) (*pb.SearchResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.Search(ctx, &pb.SearchRequest{Query: query, Limit: 50})
	return res, c.track(err)
}

// Version identifies the server build, so the client can notice schema skew.
func (c *Client) Version(parent context.Context) (*pb.GetVersionResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.system.GetVersion(ctx, &pb.GetVersionRequest{})
	return res, c.track(err)
}

// GetPrefs returns this user's UI preferences.
func (c *Client) GetPrefs(parent context.Context) (map[string]string, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.GetPrefs(ctx, &pb.GetPrefsRequest{})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetPrefs(), nil
}

// SetPrefs merges preferences server-side.
func (c *Client) SetPrefs(parent context.Context, p map[string]string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.SetPrefs(ctx, &pb.SetPrefsRequest{Prefs: p})
	return c.track(err)
}

// ListTags returns this user's tags and which feeds carry them.
func (c *Client) ListTags(parent context.Context) (*pb.ListTagsResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ListTags(ctx, &pb.ListTagsRequest{})
	return res, c.track(err)
}

// SetFeedTag adds or removes a tag on a feed.
func (c *Client) SetFeedTag(parent context.Context, sourceID, name string, on bool) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.SetFeedTag(ctx, &pb.SetFeedTagRequest{
		SourceId: sourceID, Name: name, On: on,
	})
	return c.track(err)
}

// SetNote writes or clears an item's note.
func (c *Client) SetNote(parent context.Context, itemID, body string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: body})
	return c.track(err)
}

// ListNotes returns everything this user has annotated.
func (c *Client) ListNotes(parent context.Context) ([]*pb.Item, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ListNotes(ctx, &pb.ListNotesRequest{Limit: 100})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetItems(), nil
}

// GetFeedSettings loads one feed's configuration panel.
func (c *Client) GetFeedSettings(parent context.Context, sourceID string) (*pb.FeedSettings, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.GetFeedSettings(ctx, &pb.GetFeedSettingsRequest{SourceId: sourceID})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetSettings(), nil
}

// UpdateFeedSettings patches a feed. Nil fields are left alone, so a client that
// only knows about some of them cannot blank the rest.
func (c *Client) UpdateFeedSettings(parent context.Context, req *pb.UpdateFeedSettingsRequest) (*pb.FeedSettings, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.UpdateFeedSettings(ctx, req)
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetSettings(), nil
}
