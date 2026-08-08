package app

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/obs"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The on-disk log copy is inside retention, and under which window.
//
// # What was outside it
//
// `<db>.log.jsonl` is the ring's spill, and what spills into it includes every
// line the audit path logs: usernames, client addresses, and the text of
// authentication failures. It was bounded only by a two-megabyte rotation —
// which is not a retention policy, it is a disk budget that happens to expire
// things at a rate decided by how busy the server is. A quiet instance kept
// years.
//
// So it shares the AUDIT window, and these pin that it really is the audit
// window rather than a second setting that will drift from it.

// spillFor seeds the App's OWN spill with records at chosen ages.
//
// The App's, not a second one opened beside it. Swapping in a replacement was
// the first version and it leaked: `Close` closes whatever `a.logSpill` points
// at, so the original stayed open and Windows refused to remove the TempDir —
// the same symptom, from the same cause, as the poller tests that found the
// close/prune race in the spill itself.
func spillFor(t *testing.T, a *App, ages ...time.Duration) *obs.Spill {
	t.Helper()
	s := a.logSpill
	if s == nil {
		t.Fatal("the app opened no spill file, so there is nothing to prune")
	}
	now := time.Now()
	for i, age := range ages {
		s.Write(obs.Record{Time: now.Add(-age), Message: "record" + strconv.Itoa(i)})
	}
	return s
}

func TestTheAuditWindowPrunesTheOnDiskLog(t *testing.T) {
	a, sc := retentionApp(t)
	ctx := context.Background()

	// A 30-day audit window, set the way the settings screen would set it.
	if err := a.settings.SetSystemValue(ctx, store.KeyRetentionAuditDays, "30", sc.UserID); err != nil {
		t.Fatalf("SetSystemValue: %v", err)
	}
	s := spillFor(t, a, 100*24*time.Hour, 1*time.Hour)

	a.sweepSecurity(ctx)

	if spillHolds(s, "record0") {
		t.Error("a 100-day-old line survived a 30-day audit window")
	}
	if !spillHolds(s, "record1") {
		t.Error("a line inside the window was deleted")
	}
}

func TestForeverKeepsTheOnDiskLogToo(t *testing.T) {
	// "0" is how an operator asks for forever on the security windows, and it
	// has to mean the same thing here — a sweep that deleted the log anyway
	// would be the one place the setting silently did not apply.
	a, sc := retentionApp(t)
	ctx := context.Background()

	if err := a.settings.SetSystemValue(ctx, store.KeyRetentionAuditDays, "0", sc.UserID); err != nil {
		t.Fatalf("SetSystemValue: %v", err)
	}
	s := spillFor(t, a, 4000*24*time.Hour)

	a.sweepSecurity(ctx)

	// Substring rather than a count: the file also holds the lines this very
	// App logged while booting, and asserting on a total would be asserting on
	// how chatty startup happens to be today.
	if !spillHolds(s, "record0") {
		t.Error("an eleven-year-old line was deleted under a forever window")
	}
}

// spillHolds reports whether a message survived the sweep.
func spillHolds(s *obs.Spill, msg string) bool {
	for _, r := range s.Load(500) {
		if strings.Contains(r.Message, msg) {
			return true
		}
	}
	return false
}

func TestTheSweepSurvivesAnInstanceWithNoSpillAtAll(t *testing.T) {
	// A read-only directory costs the history and must not cost the sweep: the
	// spill is nil whenever OpenSpill failed at boot, and this runs inside the
	// poll cycle, where a panic would stop the reader getting articles.
	a, sc := retentionApp(t)
	ctx := context.Background()
	if err := a.settings.SetSystemValue(ctx, store.KeyRetentionAuditDays, "30", sc.UserID); err != nil {
		t.Fatalf("SetSystemValue: %v", err)
	}
	// Closed before it is dropped, or the handle outlives the App and Windows
	// will not let the TempDir go — see spillFor.
	if a.logSpill != nil {
		_ = a.logSpill.Close()
	}
	a.logSpill = nil

	a.sweepSecurity(ctx) // must not panic
}
