package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/events"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// onIngested is the publisher half of §20.3: a poll's new items get queued for
// analysis and announced to whoever subscribes. Neither half may fail the poll,
// so these tests go through a real App rather than mocking the collaborators —
// what matters is that the wiring in Open actually reaches both.

func eventsApp(t *testing.T) (*App, store.Scope, string) {
	t.Helper()
	a, err := Open(t.Context(), Config{
		DBPath: filepath.Join(t.TempDir(), "events.db"), DevMode: true, Log: testLogger(),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	sc, err := a.EnsureDevUser(t.Context(), "cam", "articleflux-events-test")
	if err != nil {
		t.Fatalf("dev user: %v", err)
	}
	feed, _, err := a.Repo().Subscribe(t.Context(), sc, store.NewSubscription{
		NaturalKey: "feed:events", FeedURL: "https://events.example/feed", Title: "Events fixture",
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return a, sc, feed.SourceID
}

// queueAnalysis puts new items on the analysis queue. Split out of onIngested
// so that assertion is about analyze's own state, not the bus's.
func TestQueueAnalysisEnqueuesAJob(t *testing.T) {
	a, _, sourceID := eventsApp(t)

	a.queueAnalysis(sourceID, []string{"item-1", "item-2"})

	depth, err := a.Repo().QueueDepth(t.Context())
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth[store.JobAnalyze].Queued == 0 {
		t.Error("no analysis job was queued for the new items")
	}
}

// An empty batch — a poll that produced nothing new — must not queue a job for
// nothing to analyse.
func TestQueueAnalysisNoopWithNoItems(t *testing.T) {
	a, _, sourceID := eventsApp(t)

	a.queueAnalysis(sourceID, nil)

	depth, err := a.Repo().QueueDepth(t.Context())
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth[store.JobAnalyze].Queued != 0 {
		t.Errorf("queued %d analysis jobs for zero items", depth[store.JobAnalyze].Queued)
	}
}

// publishItemsAdded delivers one event per subscriber of the source — not a
// tenant-wide broadcast, since a source belongs to no tenant (A14) and most
// readers on a shared instance are not subscribed to most feeds.
func TestPublishItemsAddedReachesASubscriber(t *testing.T) {
	a, sc, sourceID := eventsApp(t)

	sub := a.bus.Subscribe(sc.TenantID, sc.UserID)
	defer sub.Close()

	a.publishItemsAdded(sourceID, []string{"item-1", "item-2"})

	select {
	case ev := <-sub.C:
		if ev.Kind != events.KindItemsAdded {
			t.Errorf("Kind = %v, want KindItemsAdded", ev.Kind)
		}
		if ev.SourceID != sourceID {
			t.Errorf("SourceID = %q, want %q", ev.SourceID, sourceID)
		}
		if len(ev.ItemIDs) != 2 {
			t.Errorf("ItemIDs = %v, want 2 ids", ev.ItemIDs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the subscriber never received the announcement")
	}
}

// onIngested runs both halves, in order — analysis queued before the
// announcement fans out, so the durable queue holds the work before anything
// that can block on a slow subscriber.
func TestOnIngestedRunsBothHalves(t *testing.T) {
	a, sc, sourceID := eventsApp(t)
	sub := a.bus.Subscribe(sc.TenantID, sc.UserID)
	defer sub.Close()

	a.onIngested(sourceID, []string{"item-1"})

	depth, err := a.Repo().QueueDepth(t.Context())
	if err != nil {
		t.Fatalf("QueueDepth: %v", err)
	}
	if depth[store.JobAnalyze].Queued == 0 {
		t.Error("onIngested did not queue analysis")
	}
	select {
	case <-sub.C:
	case <-time.After(2 * time.Second):
		t.Fatal("onIngested did not announce the new item")
	}
}

// publishItemsAdded on an instance with no bus (a zero-value App, as at some
// point during construction) must not panic — the caller is mid-poll and a
// crash there is worse than a missed live update.
func TestPublishItemsAddedWithNoBusDoesNotPanic(t *testing.T) {
	a := &App{log: testLogger()}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publishItemsAdded panicked with no bus: %v", r)
		}
	}()
	a.publishItemsAdded("source-x", []string{"item-1"})
}

// A failure to queue analysis (a slow or dead database) must be logged and
// swallowed — a poll that failed because classification could not be queued
// would trade articles the reader wants for labels they can live without.
func TestQueueAnalysisSurvivesAnEnqueueFailure(t *testing.T) {
	a, _, sourceID := eventsApp(t)
	if err := a.DB().Close(); err != nil {
		t.Fatalf("closing the db early: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("queueAnalysis panicked on a failed enqueue: %v", r)
		}
	}()
	a.queueAnalysis(sourceID, []string{"item-1"})
}

// A failure to read a source's subscribers must not crash the poll path —
// the reader finds the new items on their next ordinary fetch instead of a
// live update, which is a smaller loss than a poll that panics.
func TestPublishItemsAddedSurvivesASubscribersLookupFailure(t *testing.T) {
	a, _, sourceID := eventsApp(t)
	if err := a.DB().Close(); err != nil {
		t.Fatalf("closing the db early: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("publishItemsAdded panicked on a failed lookup: %v", r)
		}
	}()
	a.publishItemsAdded(sourceID, []string{"item-1"})
}

// EventsDropped reports zero rather than panicking when the bus was never
// constructed, and reflects the real bus's counter otherwise.
func TestEventsDroppedReflectsTheBus(t *testing.T) {
	if got := (&App{}).EventsDropped(); got != 0 {
		t.Errorf("EventsDropped with no bus = %d, want 0", got)
	}
	a, _, _ := eventsApp(t)
	if got := a.EventsDropped(); got != 0 {
		t.Errorf("EventsDropped on a fresh bus = %d, want 0", got)
	}
}
