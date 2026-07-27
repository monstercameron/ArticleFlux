package grpcsrv

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/monstercameron/ArticleFlux/internal/events"
	pb "github.com/monstercameron/ArticleFlux/internal/pb/articleflux/v1"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The streaming half of §20.3 (TODO 8.7): the server end of live updates.
//
// `internal/events` has been a complete, tested bus with nothing on either side
// of it — no publisher and no transport. This is the transport.
//
// # The ordering that makes a resume correct
//
// Subscribe FIRST, replay SECOND. The obvious order is the other one, and it
// loses events: anything published between the end of the replay and the start
// of the subscription belongs to neither, and nothing afterwards notices. Doing
// it this way produces the opposite problem — an event can appear in both — and
// that one is fixable, by dropping anything at or below the highest sequence
// already sent. A duplicate the client ignores is cheap; a gap it cannot see is
// the bug that makes people distrust live updates and reload out of habit.

// eventBatchWait is how long the handler waits for more events before sending.
//
// A batch rather than a message per event, because the events that matter arrive
// in bursts: a poll cycle finishing across forty feeds is forty events in a few
// milliseconds, and forty tunnel frames to say "the list changed" is forty
// wake-ups on a phone. Twenty-five milliseconds is below the threshold where
// anyone perceives a delay and above the width of the bursts that matter.
const eventBatchWait = 25 * time.Millisecond

// eventBatchMax bounds one message.
//
// An OPML import can publish hundreds of events in a second. Sending them as one
// message would be a single enormous frame at exactly the moment the client is
// busiest; sending them in chunks lets it start reacting to the first while the
// rest arrive.
const eventBatchMax = 128

// EventServer implements pb.EventServiceServer.
type EventServer struct {
	pb.UnimplementedEventServiceServer
	bus     *events.Bus
	scopeOf func(context.Context) (store.Scope, error)
	log     *slog.Logger
}

// NewEventServer wires the live-update stream.
func NewEventServer(bus *events.Bus, scopeOf func(context.Context) (store.Scope, error),
	log *slog.Logger) *EventServer {
	return &EventServer{bus: bus, scopeOf: scopeOf, log: log}
}

// WatchEvents streams changes for the caller's scope until the client goes away.
func (s *EventServer) WatchEvents(req *pb.WatchEventsRequest,
	stream pb.EventService_WatchEventsServer) error {

	ctx := stream.Context()
	sc, err := s.scopeOf(ctx)
	if err != nil || !sc.Valid() {
		return errKey(codes.Unauthenticated, "srv.noSession", "sign in first", nil)
	}

	sub := s.bus.Subscribe(sc.TenantID, sc.UserID)
	// The subscription outlives this function only if this is forgotten, and
	// then it holds a channel the bus keeps trying to fill for the life of the
	// process — one leaked tab at a time until publishing costs real work.
	defer sub.Close()

	var sent uint64

	// The catch-up. Only for a client that asked for one: a fresh tab passing
	// zero is about to fetch the current state anyway, and replaying a thousand
	// buffered events into it is work that ends in the same place.
	if req.GetSince() > 0 {
		past, rerr := s.bus.Replay(sc.TenantID, sc.UserID, req.GetSince())
		var resync events.ErrResyncRequired
		switch {
		case errors.As(rerr, &resync):
			// Too far behind to be caught up. Said out loud rather than papered
			// over with a partial batch, because a client handed events starting
			// in the middle believes it has the whole story.
			s.log.Info("client fell out of the event buffer; asking it to resync",
				"asked", resync.Asked, "oldest", resync.Oldest, "user", sc.UserID)
			if serr := stream.Send(&pb.WatchEventsResponse{ResyncRequired: true}); serr != nil {
				return serr
			}
		case rerr != nil:
			s.log.Error("replaying events", "err", rerr)
			return errKey(codes.Internal, "srv.internal", "internal error", nil)
		default:
			for _, chunk := range chunkEvents(past, eventBatchMax) {
				if serr := stream.Send(&pb.WatchEventsResponse{Events: toPB(chunk)}); serr != nil {
					return serr
				}
				sent = chunk[len(chunk)-1].Seq
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			// The tab closed, the tunnel dropped, or the server is shutting
			// down. Not an error: this is how every one of these ends.
			return nil
		case e, ok := <-sub.C:
			if !ok {
				return nil
			}
			batch := []events.Event{e}
			// Gather whatever else is already waiting, then wait a moment more
			// for the rest of the burst.
			batch = drainEvents(sub, batch, eventBatchMax)
			if len(batch) < eventBatchMax {
				timer := time.NewTimer(eventBatchWait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
				batch = drainEvents(sub, batch, eventBatchMax)
			}

			// Drop what the replay already delivered. This is the duplicate the
			// subscribe-first ordering trades for, and dropping it here is the
			// whole cost of not having a gap.
			out := make([]events.Event, 0, len(batch))
			for _, ev := range batch {
				if ev.Seq > sent {
					out = append(out, ev)
				}
			}
			if len(out) == 0 {
				continue
			}
			if serr := stream.Send(&pb.WatchEventsResponse{Events: toPB(out)}); serr != nil {
				return serr
			}
			sent = out[len(out)-1].Seq
		}
	}
}

// drainEvents takes whatever is already queued, without blocking.
func drainEvents(sub *events.Subscription, into []events.Event, max int) []events.Event {
	for len(into) < max {
		select {
		case e, ok := <-sub.C:
			if !ok {
				return into
			}
			into = append(into, e)
		default:
			return into
		}
	}
	return into
}

// chunkEvents splits a replay into sendable messages.
func chunkEvents(all []events.Event, size int) [][]events.Event {
	if len(all) == 0 {
		return nil
	}
	var out [][]events.Event
	for start := 0; start < len(all); start += size {
		end := start + size
		if end > len(all) {
			end = len(all)
		}
		out = append(out, all[start:end])
	}
	return out
}

// toPB converts events for the wire.
//
// Ids only, never rows — see events.proto. The kind crosses as a string so an
// old client meeting a new kind can invalidate broadly and carry on, rather than
// receiving an enum's zero value and acting on the wrong one.
func toPB(in []events.Event) []*pb.Event {
	out := make([]*pb.Event, 0, len(in))
	for _, e := range in {
		out = append(out, &pb.Event{
			Seq:      e.Seq,
			Kind:     string(e.Kind),
			SourceId: e.SourceID,
			ItemIds:  e.ItemIDs,
			At:       e.At.UTC().Format(time.RFC3339),
		})
	}
	return out
}
