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
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/signals"
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

// How the tunnel comes back.
//
// This is a self-hosted reader: the server is a box in someone's house that gets
// restarted, updated, suspended with the laptop lid, and moved between networks,
// while the tab stays open for days. Giving up is never the right answer, so
// there is no attempt limit anywhere here — the connection retries for as long
// as the page is open, and the only thing that changes is how often.
//
// The shape: fast enough that a server restart is invisible (half a second, then
// a second, then two), capped low enough that coming back from a lid-close or a
// changed network is a few seconds' wait rather than a couple of minutes. gRPC's
// own default caps at 120s, which is correct for a datacentre client with
// thousands of peers and wrong for one browser tab and one server — a reader who
// opens the laptop should not have to reload the page because the backoff
// happens to be two minutes into a wait.
//
// Jitter stays on. There is usually one client, so a thundering herd is not the
// risk; a phone and a laptop both waking to the same Wi-Fi and hammering the
// same lock-step schedule is.
const (
	reconnectInitial  = 500 * time.Millisecond
	reconnectMax      = 20 * time.Second
	reconnectFactor   = 1.6
	reconnectJitter   = 0.2
	reconnectMinTries = 5 * time.Second
)

// Dial opens the tunnel.
//
// onState is called on every transition so the UI can render the connection
// indicator without polling.
//
// The returned Client is usable immediately, before the socket is up: gRPC dials
// lazily and the calls below wait for the connection rather than failing while
// it is being established (see WaitForReady). Watch is what turns the underlying
// transport's state into something the reader can see.
func Dial(ctx context.Context, tunnelURL string, onState func(ConnState)) (*Client, error) {
	c := &Client{state: Connecting, onState: onState}
	c.notify(Connecting)

	conn, err := grpctunnel.DialContext(ctx, tunnelURL,
		// The transport is the WebSocket; TLS is the page's, not gRPC's. Asking
		// gRPC for its own credentials here would double-wrap and fail.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpctunnel.WithReconnectPolicy(grpctunnel.ReconnectConfig{
			InitialDelay:      reconnectInitial,
			MaxDelay:          reconnectMax,
			Multiplier:        reconnectFactor,
			Jitter:            reconnectJitter,
			MinConnectTimeout: reconnectMinTries,
		}),
		// Wait for the tunnel instead of failing on it.
		//
		// gRPC is fail-fast by default: a call made while the connection is
		// down returns Unavailable immediately rather than waiting for the
		// reconnect that is already in progress. In a datacentre that is right
		// — there is another backend to try. Here there is one server, the
		// reconnect is seconds away, and failing fast means a reader who
		// clicked a feed during a blip gets an error for something that fixed
		// itself before they finished reading the message.
		//
		// This is bounded, not unbounded: callTimeout still applies, so a call
		// against a server that is genuinely gone fails in twenty seconds with
		// the indicator already showing why. The two long user-initiated calls
		// opt back out — see Refresh and Subscribe.
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		// A dial error here is a configuration error — a target gRPC cannot
		// parse, or an option this build rejects — not an unreachable server.
		// Retrying it would loop on something no amount of waiting fixes.
		c.notify(Down)
		return nil, err
	}
	c.conn = conn
	c.reader = pb.NewReaderServiceClient(conn)
	c.system = pb.NewSystemServiceClient(conn)
	// Not Live: the socket is not up yet, and saying it is would make the
	// indicator's one job — never let "silently disconnected" look like "quiet
	// news day" — a lie in the other direction. Watch reports what is true.
	conn.Connect()
	return c, nil
}

// Watch drives the connection indicator from the transport, and reports when the
// tunnel comes back.
//
// Without it the only evidence of the connection's state is whether the last RPC
// worked, which means a reader who is not clicking anything is told "live" by a
// page that has been disconnected for an hour — and, worse, a reconnect is
// invisible until they click something, so a tab left open overnight shows
// yesterday's articles until touched.
//
// onRecover fires on each transition INTO a working connection, and is where the
// caller refetches: the reconnect is the moment the screen became stale.
//
// It returns when ctx is cancelled. Nothing here counts attempts or gives up;
// gRPC re-dials on the backoff configured above for as long as this runs.
func (c *Client) Watch(ctx context.Context, onRecover func()) {
	if c.conn == nil {
		return
	}
	for {
		s := c.conn.GetState()
		switch s {
		case connectivity.Ready:
			if c.state != Live {
				c.notify(Live)
				if onRecover != nil {
					onRecover()
				}
			}
		case connectivity.Idle:
			// Idle is not broken — it is gRPC waiting to be asked. Nothing will
			// re-dial until an RPC is made, so a tab sitting untouched after a
			// drop would show "connecting" indefinitely and then serve the
			// reader a stale screen. Ask.
			c.conn.Connect()
		case connectivity.Connecting:
			if c.state != Connecting {
				c.notify(Connecting)
			}
		case connectivity.TransientFailure, connectivity.Shutdown:
			if c.state != Down {
				c.notify(Down)
			}
		}
		// Blocks until the state is no longer s, and returns false only when
		// ctx ends — which is the page going away.
		if !c.conn.WaitForStateChange(ctx, s) {
			return
		}
	}
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
func (c *Client) MarkAllRead(parent context.Context, sourceID string) (int32, string, error) {
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
		return 0, "", err
	}
	return res.GetMarked(), res.GetUndoToken(), nil
}

// Subscribe adds a feed.
func (c *Client) Subscribe(parent context.Context, url string) (*pb.SubscribeResponse, error) {
	// Subscribing polls the feed synchronously on the server, so it gets the
	// full budget rather than the shared one.
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	// Fail fast, against the connection-level default. This one waits on the
	// public internet, so its budget is 45 seconds — and 45 seconds of nothing,
	// because the tunnel is down and the call is patiently waiting for it, is
	// indistinguishable from a feed that will not answer. Better to say
	// "disconnected" at once and let the reader press it again.
	res, err := c.reader.Subscribe(ctx, &pb.SubscribeRequest{Url: url}, grpc.WaitForReady(false))
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
	// Fail fast for the same reason Subscribe does: two minutes of a "Refreshing"
	// chip is a long time to spend not telling someone the server is unreachable.
	res, err := c.reader.Refresh(ctx, &pb.RefreshRequest{SourceIds: sourceIDs}, grpc.WaitForReady(false))
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

// UpdateTag changes a tag's rail label and glyph, leaving the tag itself alone.
//
// It takes the request rather than the two fields because both are OPTIONAL and
// an empty string is a real value on either: the panel's "clear the name" and
// its "do not touch the name" are different requests, and a (label, glyph)
// signature cannot express the difference.
func (c *Client) UpdateTag(parent context.Context, req *pb.UpdateTagRequest) (*pb.Tag, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.UpdateTag(ctx, req)
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetTag(), nil
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

// engagementTimeout bounds one signals batch.
//
// Much shorter than callTimeout, and deliberately so: this is the one call in
// the client whose result nobody is waiting for. A slow batch should give up and
// be retried on the next flush rather than occupy a slot for twenty seconds, and
// nothing on screen changes either way.
const engagementTimeout = 8 * time.Second

// RecordEngagements ships a batch of observations (plan.md §18.1).
//
// Note what this does NOT do: it does not call c.track. Every other method here
// reports a transport failure to the connection indicator, and that is right for
// them — a failed ListItems means the reader is looking at stale data and
// deserves to know. A failed signals batch means nothing to the person reading;
// the outbox keeps it and retries. Flipping the indicator to "down" because
// analytics could not be delivered would be the signals layer degrading the
// reading experience, which is exactly the thing it is not allowed to do.
func (c *Client) RecordEngagements(parent context.Context, evs []signals.Event) error {
	if len(evs) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, engagementTimeout)
	defer cancel()

	req := &pb.RecordEngagementsRequest{Events: make([]*pb.Engagement, 0, len(evs))}
	for _, e := range evs {
		req.Events = append(req.Events, &pb.Engagement{
			Id:        e.ID,
			ItemId:    e.ItemID,
			SourceId:  e.SourceID,
			Kind:      string(e.Kind),
			Value:     e.Value,
			Surface:   string(e.Surface),
			Context:   e.Context,
			SessionId: e.SessionID,
			At:        e.At,
		})
	}
	_, err := c.reader.RecordEngagements(ctx, req)
	return err
}

// GetServerStats and ListLogs back the settings screen's Server and Activity
// sections. Both are authenticated: they disclose row counts, storage size, feed
// URLs and error text.
func (c *Client) GetServerStats(parent context.Context) (*pb.GetServerStatsResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.system.GetServerStats(ctx, &pb.GetServerStatsRequest{})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) ListLogs(parent context.Context, minLevel string, limit int32) ([]*pb.LogRecord, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.system.ListLogs(ctx, &pb.ListLogsRequest{MinLevel: minLevel, Limit: limit})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetRecords(), nil
}

// UndoMarkAllRead puts back exactly the rows one bulk mark touched.
func (c *Client) UndoMarkAllRead(parent context.Context, token string) (int32, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.UndoMarkAllRead(ctx, &pb.UndoMarkAllReadRequest{UndoToken: token})
	if err := c.track(err); err != nil {
		return 0, err
	}
	return res.GetRestored(), nil
}
