//go:build js && wasm

// Dialling and the stub: the browser half of the package described in doc.go.
package data

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/monstercameron/GoGRPCBridge/pkg/grpctunnel"

	"github.com/monstercameron/ArticleFlux/client/outbox"
	"github.com/monstercameron/ArticleFlux/client/platform"
	"github.com/monstercameron/ArticleFlux/internal/connpolicy"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/signals"
)

// phase is what the TRANSPORT is doing, which is only one of the four inputs to
// what the reader is told (§20.19). Kept separate from ConnState because the
// other three inputs — the browser's network status, a terminal refusal, and the
// grace period below — can all outrank it, and a single field would have to be
// overwritten by whichever of them spoke last.
type phase int

const (
	phaseConnecting phase = iota
	phaseReady
	phaseFailed
)

// connectingGrace is how long a redial is allowed to be invisible.
//
// A server restart reconnects in about half a second. Painting "connecting" for
// it converts an event nobody needed to see into a flicker on every deploy, so
// the indicator lags the transport ON THE WAY DOWN only. There is no grace in
// the other direction: Live and Blocked paint at once, because delaying good
// news, or a remedy someone has to act on, is only ever confusing.
const connectingGrace = time.Second

// Client wraps the tunnel connection.
type Client struct {
	conn   *grpc.ClientConn
	reader pb.ReaderServiceClient
	system pb.SystemServiceClient
	auth   pb.AuthServiceClient
	smart  pb.SmartServiceClient
	// events is the live-update stream (§20.3). The only streaming stub here,
	// and the only one whose call outlives the function that made it.
	events pb.EventServiceClient

	// pending holds writes made while the connection was not working, so an
	// outage delays them instead of discarding them (§20.19.8). Its own lock —
	// it is touched by click handlers and by the drain, and neither has anything
	// to say about the connection state mu guards.
	pending *outbox.Queue
	// cache holds the last answer to each read, so an outage degrades the app
	// instead of emptying it. The read half of the same idea, and it carries its
	// own lock for the same reason.
	cache *Cache

	// mu guards everything below it. The connection is written from three
	// places that do not coordinate — the Watch goroutine, every RPC's
	// completion, and the browser's own online/offline callbacks — and in wasm
	// that is a single-threaded scheduler where the lock is nearly free. Nearly
	// free and correct beats reasoning about which callbacks can interleave.
	mu       sync.Mutex
	raw      phase
	offline  bool
	blocked  Reason
	shown    ConnState
	gen      uint64
	attempts int
	failedAt time.Time
	// Session health, for the Settings screen (§20.19.10). Cheap enough to keep
	// always: three fields, updated on a transition that happens seconds apart
	// at worst.
	// lastEventSeq is the highest event sequence this client has processed, so a
	// reconnect resumes rather than reloads. Under mu because the pump's
	// goroutine writes it and the pump's next dial reads it.
	lastEventSeq uint64
	reconnects   int
	downtime     time.Duration
	downAt       time.Time
	onState      func(ConnState)
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

// How the client finds out the connection is dead (§20.19.3).
//
// There are two keepalives in this system and they answer different questions.
// The server's WebSocket ping (30s/90s, internal/app) answers "can the server
// reclaim this slot?", and the browser replies to it in its own stack — no
// JavaScript, no wasm, no application timer — so the SERVER always finds out.
// These constants are the other one, and they exist because the client
// otherwise never does.
//
// The failure they catch is a half-open socket: a CGNAT reclaim, a VPN drop, a
// laptop's Wi-Fi vanishing without an RST. No FIN, no close event, so the
// browser's WebSocket stays open and gRPC stays READY, and the indicator reports
// a live connection over a socket that has been dead for an hour. That is
// precisely the "silently disconnected looks like a quiet news day" failure the
// indicator exists to prevent, and it was the one case it could not see.
//
//	30s + 10s ⇒ the truth within 40 seconds, worst case.
//
// **These may not ship without the server's KeepaliveEnforcementPolicy**, which
// is why the numbers are not written here: both ends read them from
// `internal/connpolicy`, which holds the invariant between them and a test that
// fails when someone breaks it. grpctunnel sets PermitWithoutStream, which is
// the whole point — an idle tunnel is exactly when a dead one goes unnoticed —
// but gRPC servers default to MinTime 5m and PermitWithoutStream false, and a
// client pinging an idle connection against those defaults collects two ping
// strikes and is sent GOAWAY ENHANCE_YOUR_CALM. It reconnects, pings, and is
// kicked again. Enabling this half alone converts a silent bug into a visible
// flap every sixty seconds.

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
	c := &Client{shown: Connecting, raw: phaseConnecting, onState: onState}
	if onState != nil {
		onState(Connecting)
	}

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
		// The credential rides on every call from here, and a rejected one is
		// noticed in one place. See client/data/auth.go.
		//
		// BOTH, and the pair is load-bearing: a unary interceptor does not see
		// streaming calls at all, so for as long as only the first line was here
		// `WatchEvents` and `GetAudioTrack` opened with no credential and a server
		// with real authentication refused them. Adding a stream RPC and only the
		// unary option is the shape of that bug; there is no third kind of call,
		// so these two lines cover the surface.
		grpc.WithUnaryInterceptor(authInterceptor),
		grpc.WithStreamInterceptor(streamAuthInterceptor),
		// The only thing that can notice a socket the browser still believes in.
		// Pairs with the server's KeepaliveEnforcementPolicy — see the constants.
		grpctunnel.WithTunnelKeepalive(connpolicy.ClientInterval, connpolicy.ClientTimeout),
	)
	if err != nil {
		// A dial error here is a configuration error — a target gRPC cannot
		// parse, or an option this build rejects — not an unreachable server.
		// Retrying it would loop on something no amount of waiting fixes.
		c.setPhase(phaseFailed)
		return nil, err
	}
	c.conn = conn
	c.reader = pb.NewReaderServiceClient(conn)
	c.system = pb.NewSystemServiceClient(conn)
	c.auth = pb.NewAuthServiceClient(conn)
	c.smart = pb.NewSmartServiceClient(conn)
	c.events = pb.NewEventServiceClient(conn)
	// Writes that outlived the last tab. Restored BEFORE the connection is
	// asked for, so a reader who closed the laptop mid-outage and opened it the
	// next morning has their marks replayed by the first recovery rather than
	// discovering they were never saved.
	c.loadOutbox()
	// And the reads, for the same reason in the other direction: a tab reopened
	// with no network shows what it last showed, rather than an empty rail that
	// reads as data loss.
	c.loadCache()
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
	go c.watchNetwork(ctx)
	for {
		s := c.conn.GetState()
		switch s {
		case connectivity.Ready:
			recovered := c.setPhase(phaseReady)
			if recovered && onRecover != nil {
				onRecover()
			}
		case connectivity.Idle:
			// Idle is not broken — it is gRPC waiting to be asked. Nothing will
			// re-dial until an RPC is made, so a tab sitting untouched after a
			// drop would show "connecting" indefinitely and then serve the
			// reader a stale screen. Ask.
			c.conn.Connect()
		case connectivity.Connecting:
			c.setPhase(phaseConnecting)
		case connectivity.TransientFailure, connectivity.Shutdown:
			c.setPhase(phaseFailed)
		}
		// Blocks until the state is no longer s, and returns false only when
		// ctx ends — which is the page going away.
		if !c.conn.WaitForStateChange(ctx, s) {
			return
		}
	}
}

// networkPoll is how often the browser's own network flag is re-read.
//
// A property read, not a request: `navigator.onLine` is a synchronous field, so
// this costs about as much as reading a Go struct and is nothing beside the
// render it might cause.
const networkPoll = 2 * time.Second

// watchNetwork keeps the offline flag true by POLLING as well as by listening.
//
// The `online`/`offline` events are the obvious source and they are not a
// reliable one. Headless Chrome under CDP emulation delivers them about half the
// time — measured, over a dozen runs of the connection spec, which is what
// turned this from a suspicion into a fix — and real browsers are worse in the
// cases that matter most: waking from sleep, a VPN dropping, iOS switching
// between cellular and a wifi network it cannot actually reach.
//
// A missed event does not correct itself. Nothing else reads the flag, so a
// reader whose wifi is off gets a countdown toward a reconnect that cannot
// happen — the exact confusion §20.19.5 separates `offline` from `down` to
// avoid, arriving through the back door of an event that never fired.
//
// It only ever REPORTS what the browser says; `SetOffline` ignores a value that
// has not changed, so the steady state is one property read every two seconds
// and no repaints at all.
func (c *Client) watchNetwork(ctx context.Context) {
	t := time.NewTicker(networkPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			online := platform.Online()
			// The same two steps the event handler takes, because a poll that
			// only updates the flag fixes half the problem and leaves the worse
			// half: the network comes back, the badge stops saying `offline`,
			// and it says `down` forever instead — the phase a failed call left
			// behind, with nothing to correct it because the event that would
			// have kicked the connection is the event that never arrived.
			if c.SetOffline(!online) && online {
				c.Kick()
			}
		}
	}
}

// setPhase records what the transport is doing and repaints if that changed
// what the reader should be told. It reports whether this was a RECOVERY — a
// transition into a working connection from one that was not — which is the
// moment everything on screen became stale.
func (c *Client) setPhase(p phase) (recovered bool) {
	c.mu.Lock()
	if c.raw == p {
		c.mu.Unlock()
		return false
	}
	was := c.raw
	c.raw = p
	c.gen++
	gen := c.gen

	switch p {
	case phaseFailed:
		// Each arrival here is one failed connection attempt, which is what the
		// countdown counts. Stamped rather than timed: the estimate has to be
		// computed at render time, and a render can happen at any point in the
		// wait.
		c.attempts++
		c.failedAt = time.Now()
	case phaseReady:
		c.attempts = 0
		// A working call is proof the refusal has lifted — a fresh login over
		// the same connection is exactly this.
		c.blocked = ReasonNone
		// Count the recovery and bank the outage (§20.19.10). "It feels flaky"
		// is unfalsifiable without these, and a self-hosted app has no
		// dashboard behind it: one reconnect an hour is a network, forty is a
		// bug, and nothing could previously tell them apart.
		if was != phaseConnecting || !c.downAt.IsZero() {
			c.reconnects++
		}
		if !c.downAt.IsZero() {
			c.downtime += time.Since(c.downAt)
			c.downAt = time.Time{}
		}
	}
	if p != phaseReady && was == phaseReady {
		c.downAt = time.Now()
	}

	// The grace period, and the one place the indicator is allowed to lag the
	// transport. Only on the way out of a working connection: everything else
	// paints at once.
	delay := time.Duration(0)
	if p == phaseConnecting && c.shown == Live {
		delay = connectingGrace
	}
	c.mu.Unlock()

	if delay == 0 {
		c.publish()
	} else {
		// If the redial lands inside the grace period the generation has moved
		// on and this fires into nothing, which is the flicker not happening.
		time.AfterFunc(delay, func() {
			c.mu.Lock()
			stale := c.gen != gen
			c.mu.Unlock()
			if !stale {
				c.publish()
			}
		})
	}
	return p == phaseReady && was != phaseReady
}

// publish resolves the four inputs into one state and tells the UI, if it has
// changed. The callback is invoked OUTSIDE the lock: it hops to the render
// thread through ui.PostAsync, and holding a mutex across that is how a UI
// framework and a transport deadlock each other.
func (c *Client) publish() {
	c.mu.Lock()
	s := c.resolveLocked()
	if s == c.shown {
		c.mu.Unlock()
		return
	}
	c.shown = s
	fn := c.onState
	c.mu.Unlock()
	if fn != nil {
		fn(s)
	}
}

// resolveLocked is the precedence order, and the order is the argument.
//
// Blocked outranks everything: a reader whose session has expired does not need
// to know the socket is healthy, they need the sign-in button, and a redial that
// succeeds must not overwrite that with "connected" over an app that cannot load
// a single article. Offline outranks the transport for the same reason in
// reverse — "can't reach the server" sends someone to check a server that is
// fine.
func (c *Client) resolveLocked() ConnState {
	switch {
	case c.blocked != ReasonNone:
		return Blocked
	case c.offline:
		return Offline
	}
	switch c.raw {
	case phaseReady:
		return Live
	case phaseFailed:
		return Down
	default:
		return Connecting
	}
}

// block records a refusal no amount of retrying will lift (§20.19.6).
func (c *Client) block(r Reason) {
	c.mu.Lock()
	if c.blocked == r {
		c.mu.Unlock()
		return
	}
	c.blocked = r
	c.mu.Unlock()
	c.publish()
}

// Unblock clears a terminal refusal.
//
// Called when the reader has done the thing the refusal asked for — signing in
// again, on the same Client, is the case that matters. Without it a successful
// login would render behind an indicator still reporting the session that
// expired before it.
func (c *Client) Unblock() { c.block(ReasonNone) }

// SetOffline reports what the browser thinks of the network (§20.19.5).
//
// Separate from the transport rather than derived from it, because the two
// disagree in both directions and the disagreement is the useful part: the
// socket can be dead while the network is fine (the server is down) and the
// network can be gone while gRPC has not noticed yet (which is most of the first
// second of every disconnection).
// It reports whether this CHANGED anything, so a caller that polls can tell a
// transition from a repetition — the network coming back is worth acting on and
// its still being there is not.
func (c *Client) SetOffline(off bool) bool {
	c.mu.Lock()
	if c.offline == off {
		c.mu.Unlock()
		return false
	}
	c.offline = off
	c.mu.Unlock()
	c.publish()
	return true
}

// Kick abandons the current backoff and re-dials now.
//
// The reason this exists rather than calling Connect: `Connect` is a NO-OP while
// a subchannel sits in TRANSIENT_FAILURE — the backoff timer owns the redial and
// nothing can shorten it. So a client that has just been told by the operating
// system that the network came back has, without this, no way to act on knowing
// it, and waits out a timer that may have grown to twenty seconds for a
// connection that would succeed immediately.
//
// Called on `online`, on a tab becoming visible, on a bfcache restore, and by
// the reader pressing Retry.
func (c *Client) Kick() {
	if c.conn == nil {
		return
	}
	c.conn.ResetConnectBackoff()
	c.conn.Connect()
	go c.verify()
}

// verify makes one cheap call so the indicator can be corrected by evidence.
//
// Resetting the backoff is not enough, and the gap it leaves is a badge that
// says "down" over a connection that is fine — for as long as the reader does
// not click anything, which on a reading app is exactly the situation the badge
// exists for.
//
// How it happens: an RPC fails during an outage and marks the phase failed.
// gRPC itself never noticed, because a socket that stops carrying traffic looks
// identical to one nobody is using until the keepalive probes it forty seconds
// later. So `Watch` sits in WaitForStateChange(READY) and never wakes, the
// transport recovers silently, and the only thing that could correct the badge
// is a successful call that nobody is making.
//
// The transport's own READY is NOT sufficient evidence — it is the state that
// lies about a socket the browser has abandoned, which is why `track` prefers a
// completed call to it. So this makes a real one and lets the ordinary path
// judge the outcome: `Version` is the cheapest, needs no session, and is
// answered by the same server on the same connection every other call uses.
func (c *Client) verify() {
	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()
	// The result is discarded on purpose. What matters is that it completed or
	// did not, which `track` has already recorded by the time this returns.
	_, _ = c.Version(ctx)
}

// State is the current connection state.
func (c *Client) State() ConnState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.shown
}

// Health reports how the connection has behaved this session.
//
// Reconnects and cumulative downtime, in that order: the count is what
// distinguishes a network from a bug, and the duration is what distinguishes a
// bug that matters. An outage in progress is included, so the numbers do not
// sit still while the reader is looking at the screen that explains them.
func (c *Client) Health() (reconnects int, downtime time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.downtime
	if !c.downAt.IsZero() {
		d += time.Since(c.downAt)
	}
	return c.reconnects, d
}

// BlockedReason names what a Blocked state needs from the reader, so the view
// can offer "Sign in" or "Reload" rather than a redder version of "down".
func (c *Client) BlockedReason() Reason {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blocked
}

// RetryIn estimates how long until gRPC's next connection attempt.
//
// An estimate, and it must be presented as one: gRPC picks its own jitter and
// does not publish the number. It is accurate enough to answer the only
// question a reader has while looking at it — wait, or press the button — and
// the button is why being a second out costs nothing. Reports false when there
// is nothing to wait for.
func (c *Client) RetryIn(now time.Time) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw != phaseFailed || c.attempts == 0 || c.offline || c.blocked != ReasonNone {
		return 0, false
	}
	return max(backoffAt(c.attempts)-now.Sub(c.failedAt), 0), true
}

// OnState installs (or replaces) the connection-state callback after Dial.
//
// It exists because the connection is now opened before there is a UI to report
// to: Root dials to validate a stored credential and Login dials to sign in,
// both with a nil callback, and only then is the reader mounted with its
// indicator. Without this the indicator would sit on "connecting" forever over a
// perfectly live tunnel — which is precisely the "silently disconnected looks
// like a quiet news day" failure the indicator exists to prevent, inverted.
//
// The current state is delivered immediately rather than waiting for the next
// transition. By the time the reader mounts the socket is usually already up,
// and a callback that only fires on change would never fire at all.
func (c *Client) OnState(fn func(ConnState)) {
	c.mu.Lock()
	c.onState = fn
	s := c.shown
	c.mu.Unlock()
	if fn != nil {
		fn(s)
	}
}

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

// downTimeout bounds a call STARTED while the connection is already known to be
// down, and it is the difference between riding out a blip and looking broken.
//
// `WaitForReady(true)` is right and stays: a call made during a half-second
// reconnect should wait for it rather than erroring at something that fixed
// itself. But the same option applied to a genuine outage means every click
// hangs for the full twenty seconds and *then* fails — a reader gets a skeleton
// for twenty seconds and bad news at the end of it, which is the worst possible
// ordering of those two events.
//
// Four seconds is chosen against the backoff schedule rather than as a round
// number: retries land at roughly 0.5s, 1.3s, 2.6s and 4.6s, so this window
// contains three or four complete attempts. Anything that was going to come
// back has come back; anything that has not is an outage, and the reader is
// better served by being told now.
const downTimeout = 4 * time.Second

// verifyTimeout is a backstop on Kick's probe, not its real deadline.
//
// The probe goes through the ordinary call path, so `ctx` above already bounds
// it — four seconds while the indicator says down, which is the case it runs in.
// This exists so the goroutine cannot outlive its usefulness if that ever
// changes: a probe still running when the reader has pressed Retry twice more is
// three probes racing to report on three different moments.
const verifyTimeout = 10 * time.Second

// ctx bounds one RPC, tightening the deadline when the connection is already
// known to be down.
func (c *Client) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	if c.State() != Live {
		return context.WithTimeout(parent, downTimeout)
	}
	return context.WithTimeout(parent, callTimeout)
}

// track lets a completed RPC correct the indicator — but only when the error it
// carries is actually about the connection (§20.19.6).
//
// This used to be `if err != nil { down }`, one test standing in for §20.7's
// whole taxonomy. Of the codes the server can return, exactly one is about the
// transport; the rest are the server ANSWERING, which is positive evidence the
// connection works. A `NotFound` painting the indicator red is the small half of
// that bug. The large half is that a refusal no amount of waiting can lift was
// indistinguishable from an outage, and so was waited on forever.
func (c *Client) track(err error) error {
	switch v := Classify(err); v.Class {
	case ClassOK:
		// A completed call is the only positive proof of a live transport
		// there is — better evidence than the connectivity state, which lags.
		c.setPhase(phaseReady)
	case ClassTransport:
		// Ask the browser BEFORE reporting, because this is the one moment the
		// answer matters and the one moment it is cheap.
		//
		// `offline` and `down` send the reader to different places, and the
		// difference is decided by a flag kept up to date by an EVENT — which
		// is not always delivered. Headless Chrome drops it outright often
		// enough to reproduce in this suite, and a real browser waking from
		// sleep is no better. When the event is missed, a reader with their wifi
		// off gets a countdown toward a reconnect that cannot happen until they
		// do something the countdown does not mention.
		//
		// A failed call is positive evidence that SOMETHING is wrong; the
		// browser's own opinion is what says which thing.
		c.SetOffline(!platform.Online())
		c.setPhase(phaseFailed)
	case ClassTerminal:
		c.block(v.Reason)
	case ClassApplication, ClassNone:
		// The server answered, or we cancelled. Neither says anything about
		// the connection, and saying something anyway is the bug.
	}
	return err
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

// ItemRevisions fetches what an article used to say (TODO F34).
//
// Not prefetched with the article and not cached: it is a click on the "edited"
// badge, which most articles never show, and each revision is a full body. A
// reader who opens the history twice pays for it twice, which is cheaper than
// every reader carrying it once.
func (c *Client) ItemRevisions(parent context.Context, id string) ([]*pb.ItemRevision, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.GetItemRevisions(ctx, &pb.GetItemRevisionsRequest{ItemId: id})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetRevisions(), nil
}

// ScrollLiveView scrolls a §10.1d live view, reporting whether it is still
// running.
//
// Deliberately NOT queued when offline, unlike every mutation in this file. A
// scroll is a gesture at a live thing: replaying one after the connection comes
// back would move a page the reader stopped looking at minutes ago, and the
// session it named is long gone anyway. Dropped is the correct outcome.
func (c *Client) ScrollLiveView(parent context.Context, sessionID string, dx, dy float64) (bool, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ScrollLiveView(ctx, &pb.ScrollLiveViewRequest{
		SessionId: sessionID, DeltaX: dx, DeltaY: dy,
	})
	if err := c.track(err); err != nil {
		return false, err
	}
	return res.GetLive(), nil
}

// SetItemState marks read/starred. Nil leaves a flag alone.
// It never discards the write. When the connection is not working the change is
// queued and ErrQueued is returned, which callers must NOT treat as a failure —
// see ErrQueued.
//
// The known-down case short-circuits before the RPC, and that is the difference
// a reader actually feels. `WaitForReady(true)` means a call made on a dead
// connection waits out the full twenty-second deadline before failing, so
// pressing `m` during an outage used to freeze the row for twenty seconds and
// then undo itself. Now it queues in microseconds and stays marked.
func (c *Client) SetItemState(parent context.Context, itemID string, read, starred *bool, rating *int32, key string) (*pb.Item, error) {
	op := outbox.Op{
		Key: key, Kind: outbox.KindItemState, ItemID: itemID,
		Read: read, Star: starred, Rating: rating, At: nowMS(),
	}
	// The cached copy is now wrong, however this call goes. Dropped before the
	// attempt rather than after a success, because the queued path never has a
	// success to hang it on — and a confidently-served cached article showing a
	// reader the rating they just changed looks like the app forgot.
	c.InvalidateItem(itemID)
	if s := c.State(); s != Live {
		return nil, c.queue(op)
	}
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.SetItemState(ctx, &pb.SetItemStateRequest{
		ItemId: itemID, Read: read, Starred: starred, Rating: rating,
		IdempotencyKey: key,
	})
	if err := c.track(err); err != nil {
		// The connection died under the call — the race the check above cannot
		// close, and the one that loses a write silently if nothing catches it.
		if Classify(err).Class == ClassTransport {
			return nil, c.queue(op)
		}
		return nil, err
	}
	return res.GetItem(), nil
}

// MarkAllRead marks a feed or everything read.
//
// Refused outright while disconnected rather than queued (ErrOffline). This is
// the one mutation that is not idempotent: each call mints a NEW undo batch, so
// a replayed one would leave two batches and an undo offer that reverses half
// its own work. Refusing in the same frame as the click is also strictly better
// than what queueing would buy — there is no optimistic state to preserve here,
// only a count the reader is waiting to see.
// MarkAllRead marks read everything the given selection matches.
//
// The selection is the caller's — the list the reader is actually looking at,
// resolved by the same function that resolves it for ListItems — rather than
// derived here from a source id. Deriving it here is what this used to do, and
// it could only ever say "one feed" or "everything": every other list the
// reader can be on arrived as an empty source id and was marked as
// EVERYTHING. See client/view/reader.go's selectorFor.
func (c *Client) MarkAllRead(parent context.Context, scope pb.ListScope, sourceID string, sourceIDs []string, categorySlug string, uncategorised bool) (int32, string, error) {
	if c.State() != Live {
		return 0, "", ErrOffline
	}
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.MarkAllRead(ctx, &pb.MarkAllReadRequest{
		Scope: scope, SourceId: sourceID, SourceIds: sourceIDs,
		CategorySlug:  categorySlug,
		Uncategorised: uncategorised,
		// The server defaults `before` to now. Sending it explicitly from the
		// client would use the client's clock, which may be wrong.
	})
	if err := c.track(err); err != nil {
		return 0, "", err
	}
	return res.GetMarked(), res.GetUndoToken(), nil
}

// UnreadByCategory returns the rail's per-label unread counts.
//
// Its own call rather than riding on ListFeeds: it costs a few hundred
// milliseconds against a real database and the sidebar must not wait behind
// it. Errors are the caller's to shrug at — a rail with no numbers is a rail.
func (c *Client) UnreadByCategory(parent context.Context, slugs []string) (map[string]int32, int32, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.CountUnreadByCategory(ctx, &pb.CountUnreadByCategoryRequest{Slugs: slugs})
	if err != nil {
		return nil, 0, c.track(err)
	}
	return res.GetCounts(), res.GetUncategorised(), nil
}

// Subscribe adds a feed, optionally named and filed on the way in.
//
// title and folderID are both "leave it to the defaults" when empty: the
// publisher's own title, and no category. The add-a-feed form sends whatever the
// reader filled in and nothing more.
func (c *Client) Subscribe(parent context.Context, url, title, folderID string) (*pb.SubscribeResponse, error) {
	// Refused rather than queued, and the reason is stronger than Refresh's: the
	// server VALIDATES the feed before anything is stored, so a queued subscribe
	// would have to show a feed in the rail that might turn out not to be one.
	// Optimism is only honest when the outcome is not in doubt.
	if c.State() != Live {
		return nil, ErrOffline
	}
	// Subscribing polls the feed synchronously on the server, so it gets the
	// full budget rather than the shared one.
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	// Fail fast, against the connection-level default. This one waits on the
	// public internet, so its budget is 45 seconds — and 45 seconds of nothing,
	// because the tunnel is down and the call is patiently waiting for it, is
	// indistinguishable from a feed that will not answer. Better to say
	// "disconnected" at once and let the reader press it again.
	res, err := c.reader.Subscribe(ctx,
		&pb.SubscribeRequest{Url: url, Title: title, FolderId: folderID},
		grpc.WaitForReady(false))
	return res, c.track(err)
}

// AnalyzeSite asks what can be followed at an address (§11).
//
// It gets the long budget for the same reason Subscribe does: the server is
// fetching a page and then up to half a dozen candidate feeds over the public
// internet, and when smart is set it is also waiting on a model. Fail-fast,
// again for the same reason — a stalled tunnel and a slow site are
// indistinguishable to someone watching a spinner.
func (c *Client) AnalyzeSite(parent context.Context, url string, smart bool) (
	*pb.AnalyzeSiteResponse, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	res, err := c.reader.AnalyzeSite(ctx,
		&pb.AnalyzeSiteRequest{Url: url, Smart: smart},
		grpc.WaitForReady(false))
	return res, c.track(err)
}

// SubscribeScrape follows a page using a rule the reader accepted.
func (c *Client) SubscribeScrape(parent context.Context, indexURL, title, folderID string,
	rule *pb.ScrapeRule) (*pb.SubscribeScrapeResponse, error) {
	// It re-runs the rule against the live page and then fetches the full text
	// of what it finds, so it is the slowest call in the client.
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	res, err := c.reader.SubscribeScrape(ctx, &pb.SubscribeScrapeRequest{
		IndexUrl: indexURL, Title: title, FolderId: folderID, Rule: rule,
	}, grpc.WaitForReady(false))
	return res, c.track(err)
}

// ImportOPML subscribes to a whole OPML file (F1).
//
// Refused offline for Subscribe's reason and more of it: the server validates
// and stores every row, so there is nothing a disconnected client could
// optimistically show. Queuing 151 subscriptions that may each fail is not an
// offline feature, it is a report the reader would have to read twice.
//
// The long budget is the import's own: subscribing does not fetch, but it is
// still one transaction per feed against SQLite plus the categories, and a
// hundred and fifty of those on a loaded server outlast the default.
func (c *Client) ImportOPML(parent context.Context, data []byte) (*pb.ImportOpmlResponse, error) {
	if c.State() != Live {
		return nil, ErrOffline
	}
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	res, err := c.reader.ImportOpml(ctx, &pb.ImportOpmlRequest{Opml: data},
		grpc.WaitForReady(false))
	return res, c.track(err)
}

// ExportOPML returns this reader's subscriptions as an OPML file.
func (c *Client) ExportOPML(parent context.Context) (*pb.ExportOpmlResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ExportOpml(ctx, &pb.ExportOpmlRequest{})
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
	// Nothing to fetch with. The fetching happens on the SERVER, over the public
	// internet, so a disconnected client is not merely unable to hear the answer
	// — it cannot cause the work to happen at all. Said in the same frame as the
	// press rather than after a wait that could only ever end this way.
	if c.State() != Live {
		return nil, ErrOffline
	}
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

// ListFolders returns this user's categories.
//
// Separate from ListFeeds rather than embedded in it: the rail asks for feeds on
// every navigation and after every state change, where the categories move only
// when someone edits them. One call reloads on a click; the other reloads when
// the taxonomy changes.
func (c *Client) ListFolders(parent context.Context) ([]*pb.Folder, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.ListFolders(ctx, &pb.ListFoldersRequest{})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetFolders(), nil
}

// CreateFolder makes a category, or returns the one that already has that name.
func (c *Client) CreateFolder(parent context.Context, name string) (*pb.Folder, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.CreateFolder(ctx, &pb.CreateFolderRequest{Name: name})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetFolder(), nil
}

// RenameFolder changes a category's name.
func (c *Client) RenameFolder(parent context.Context, folderID, name string) (*pb.Folder, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.RenameFolder(ctx, &pb.RenameFolderRequest{FolderId: folderID, Name: name})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res.GetFolder(), nil
}

// DeleteFolder removes a category. The feeds in it are unfiled, never
// unsubscribed.
func (c *Client) DeleteFolder(parent context.Context, folderID string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.DeleteFolder(ctx, &pb.DeleteFolderRequest{FolderId: folderID})
	return c.track(err)
}

// SetFeedFolder files a feed, or unfiles it when folderID is empty.
func (c *Client) SetFeedFolder(parent context.Context, sourceID, folderID string) error {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.SetFeedFolder(ctx, &pb.SetFeedFolderRequest{
		SourceId: sourceID, FolderId: folderID,
	})
	return c.track(err)
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
//
// Queued like the reading flags when the connection is down, and it is the one
// that matters most: a flag can be set again in a keystroke, and prose cannot be
// retyped. The note field's own sync glyph is what tells the reader it is
// waiting rather than saved.
func (c *Client) SetNote(parent context.Context, itemID, body string) error {
	op := outbox.Op{
		Key:  "note-" + itemID + "-" + strconv.FormatInt(nowMS(), 36),
		Kind: outbox.KindNote, ItemID: itemID, Note: &body, At: nowMS(),
	}
	if s := c.State(); s != Live {
		return c.queue(op)
	}
	ctx, cancel := c.ctx(parent)
	defer cancel()
	_, err := c.reader.SetNote(ctx, &pb.SetNoteRequest{ItemId: itemID, Body: body})
	if err := c.track(err); err != nil {
		if Classify(err).Class == ClassTransport {
			return c.queue(op)
		}
		return err
	}
	return nil
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

// InterestProfile is what the ranking believes about this reader (§18.2, §18.9).
//
// Uncached, unlike the feed and item reads. The profile is the answer to "is the
// model right about me", and a reader who has just corrected it and pressed back
// into the screen must not be shown the picture they disagreed with — a stale
// answer here is worse than a slow one, which is the reverse of the trade the
// list cache makes.
func (c *Client) InterestProfile(parent context.Context) (*pb.GetInterestProfileResponse, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.GetInterestProfile(ctx, &pb.GetInterestProfileRequest{})
	if err := c.track(err); err != nil {
		return nil, err
	}
	return res, nil
}

// SteerTopic and SteerEntity turn the reader's dial on one thing.
//
// Not queued through the outbox, and that is a deliberate exception to how this
// client handles writes. The outbox exists so a state change made offline still
// lands; a steer made offline would land minutes later against a profile the
// reader can no longer see, and the screen's whole job is to show the corrected
// picture immediately after. Offline, this fails and says so.
// SteerTopic takes the topic's KEY, not its id — see InterestTopic.key. An id is
// this derivation's and the next one will not agree.
func (c *Client) SteerTopic(parent context.Context, topicKey string, level pb.SteerLevel) (bool, error) {
	return c.steer(parent, &pb.SteerInterestRequest{TopicKey: topicKey, Level: level})
}

func (c *Client) SteerEntity(parent context.Context, name string, level pb.SteerLevel) (bool, error) {
	return c.steer(parent, &pb.SteerInterestRequest{EntityName: name, Level: level})
}

func (c *Client) steer(parent context.Context, req *pb.SteerInterestRequest) (bool, error) {
	ctx, cancel := c.ctx(parent)
	defer cancel()
	res, err := c.reader.SteerInterest(ctx, req)
	if err := c.track(err); err != nil {
		return false, err
	}
	return res.GetRebuilding(), nil
}
