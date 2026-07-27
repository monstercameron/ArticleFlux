package data

import (
	"context"

	"github.com/monstercameron/ArticleFlux/client/outbox"
)

// Replaying the queue, extracted from the browser so its rules can be tested.
//
// The three rules below are each a real bug if dropped, and none of them is
// visible by reading the loop — they are decisions about what a *failure* means
// halfway through a replay, which is exactly the state that is hard to reach by
// hand and easy to get wrong. Keeping this untagged is what makes them
// assertable: everything the browser supplies arrives as a parameter.
//
// Spec: plan.md §20.19.8.

// sender replays one queued write. Returns the server's error unchanged, so the
// classification below sees what §20.7 actually said.
type sender func(ctx context.Context, op outbox.Op) error

// drain replays the queue oldest-first and reports how many landed.
//
//   - **Stop at the first transport failure.** The queue is ordered because the
//     order is causality; racing on past a failure would apply later writes over
//     earlier ones and leave the reader's history rearranged rather than merely
//     delayed.
//   - **Hold everything on a terminal refusal.** An expired session is not the
//     queue's fault and not something it can fix. The reader signs in and it
//     drains then — discarding their writes because their session happened to
//     lapse while they were offline would be the app punishing them for the
//     outage.
//   - **Retire an op the server refused.** A write against an article that has
//     since been deleted will never succeed, and keeping it would block every
//     write behind it forever: one dead op turning a queue into a wall.
//
// save is called before every return, so an interrupted drain leaves storage
// agreeing with memory. A drain that crashed between the two would replay
// writes the server already has — harmless, since every queued op sets an
// absolute value, but only by luck, and luck is not a design.
func drain(ctx context.Context, q *outbox.Queue, send sender, save func()) (int, error) {
	if q == nil || q.Len() == 0 {
		return 0, nil
	}
	if save == nil {
		save = func() {}
	}
	sent := 0
	for _, op := range q.Pending() {
		err := send(ctx, op)
		if err == nil {
			// By KEY, so a write the reader has changed again mid-drain keeps
			// its newer intent — see outbox.Queue.Done.
			q.Done(op.Key)
			sent++
			continue
		}
		switch Classify(err).Class {
		case ClassTransport, ClassTerminal:
			save()
			return sent, err
		default:
			q.Done(op.Key)
		}
	}
	save()
	return sent, nil
}
