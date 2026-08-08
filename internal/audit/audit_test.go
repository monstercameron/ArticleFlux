package audit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The Recorder's contract, which is mostly about what it must NOT do.
//
// Every caller of this package is in the middle of something more important
// than logging — a login, a password change, a recovery that has already
// committed. So the interesting properties are the failure ones: it must not
// return an error anybody has to handle, must not panic on a half-assembled
// server, and must not lose the log line when the database is the thing that
// broke.

type fakeSink struct {
	got  []store.AuditEntry
	fail error
}

func (f *fakeSink) Audit(_ context.Context, e store.AuditEntry) error {
	if f.fail != nil {
		return f.fail
	}
	f.got = append(f.got, e)
	return nil
}

func TestRecordWritesTheRowItWasGiven(t *testing.T) {
	sink := &fakeSink{}
	r := New(sink, slog.New(slog.NewTextHandler(io.Discard, nil)))

	r.Record(context.Background(), Event{
		Action: ActionPasswordChanged, Actor: "u1", Tenant: "t1",
		Detail: map[string]string{"client": "203.0.113.7"},
	})

	if len(sink.got) != 1 {
		t.Fatalf("wrote %d rows, want 1", len(sink.got))
	}
	e := sink.got[0]
	if e.Action != string(ActionPasswordChanged) || e.ActorUserID != "u1" || e.TenantID != "t1" {
		t.Errorf("the row lost its identity: %+v", e)
	}
	if !strings.Contains(e.Detail, "203.0.113.7") {
		t.Errorf("the detail was not encoded: %q", e.Detail)
	}
	// Stamped by the store layer when empty, which is what keeps every caller
	// from having to format a time correctly.
	if e.At != "" {
		t.Errorf("the recorder set its own timestamp (%q); that is store.Audit's job "+
			"and two sources of 'now' is how a trail ends up out of order", e.At)
	}
}

// An action with no name is dropped rather than written as an empty string. A
// row that does not say what happened is worse than no row: it takes up space
// in the trail and answers nothing.
func TestRecordIgnoresAnEventWithNoAction(t *testing.T) {
	sink := &fakeSink{}
	New(sink, slog.New(slog.NewTextHandler(io.Discard, nil))).
		Record(context.Background(), Event{Actor: "u1"})

	if len(sink.got) != 0 {
		t.Errorf("wrote %d rows for an event with no action", len(sink.got))
	}
}

// A failing sink must not take the caller down with it. The login that was in
// progress has to finish.
func TestAFailingSinkIsSurvivable(t *testing.T) {
	var logged strings.Builder
	r := New(&fakeSink{fail: errors.New("disk full")},
		slog.New(slog.NewTextHandler(&logged, nil)))

	// The assertion is that this returns at all, and that the failure is not
	// silent — an instance unable to record what is happening to it is a bigger
	// problem than the thing it failed to record.
	r.Record(context.Background(), Event{Action: ActionLogin, Actor: "u1"})

	if !strings.Contains(logged.String(), "audit trail could not be written") {
		t.Errorf("a failed audit write was swallowed silently:\n%s", logged.String())
	}
}

// The log line survives a dead database, because it is the half that reaches a
// human and the database is the half more likely to be what is broken.
func TestTheLogLineIsEmittedEvenWhenTheSinkFails(t *testing.T) {
	var logged strings.Builder
	New(&fakeSink{fail: errors.New("disk full")},
		slog.New(slog.NewTextHandler(&logged, nil))).
		Record(context.Background(), Event{Action: ActionRecoveryRedeemed, Actor: "u1"})

	if !strings.Contains(logged.String(), "security_event=auth.recovery.redeemed") {
		t.Errorf("the security event never reached the log when the sink failed:\n%s",
			logged.String())
	}
}

// A half-assembled Recorder degrades instead of panicking. This runs on paths
// somebody reaches once a year — an admin CLI, a recovery — and a nil-pointer
// panic there would turn a logging gap into an outage.
func TestANilRecorderAndNilHalvesAreSafe(t *testing.T) {
	var nilRecorder *Recorder
	nilRecorder.Record(context.Background(), Event{Action: ActionLogin})

	New(nil, slog.New(slog.NewTextHandler(io.Discard, nil))).
		Record(context.Background(), Event{Action: ActionLogin})

	New(&fakeSink{}, nil).Record(context.Background(), Event{Action: ActionLogin})
}

// The stable key is part of the interface: it is what an operator's alerting
// rule matches on, so renaming it silently breaks somebody's paging.
func TestTheSecurityEventKeyIsStable(t *testing.T) {
	var logged strings.Builder
	New(&fakeSink{}, slog.New(slog.NewTextHandler(&logged, nil))).
		Record(context.Background(), Event{Action: ActionLockout, Detail: map[string]string{
			"username": "cam",
		}})

	out := logged.String()
	if !strings.Contains(out, "security_event=auth.lockout") {
		t.Errorf("the stable alerting key is missing:\n%s", out)
	}
	// Alert severity means WARN, which is what a rule filters on to skip the
	// routine sign-in traffic.
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("a lockout was not logged at WARN:\n%s", out)
	}
}

// Routine events stay at INFO. If signing in paged somebody, they would turn
// the channel off, and then nothing would page them.
func TestRoutineEventsDoNotLogAtWarn(t *testing.T) {
	var logged strings.Builder
	New(&fakeSink{}, slog.New(slog.NewTextHandler(&logged, nil))).
		Record(context.Background(), Event{Action: ActionLogin, Actor: "u1"})

	if strings.Contains(logged.String(), "level=WARN") {
		t.Errorf("an ordinary sign-in was logged as an alert:\n%s", logged.String())
	}
}

// Subject rides in acting_as_user_id and doubles as the object id, so "what
// happened to this account" is one query rather than a JSON scan.
func TestSubjectIsRecordedAsTheObject(t *testing.T) {
	sink := &fakeSink{}
	New(sink, slog.New(slog.NewTextHandler(io.Discard, nil))).
		Record(context.Background(), Event{
			Action: ActionPasswordReset, Subject: "victim-id", Tenant: "t1",
		})

	e := sink.got[0]
	if e.ActingAsUser != "victim-id" || e.ObjectID != "victim-id" {
		t.Errorf("the subject is not queryable: acting_as=%q object_id=%q",
			e.ActingAsUser, e.ObjectID)
	}
	if e.ActorUserID != "" {
		t.Error("an operator acting from a shell was given a user id they do not have")
	}
}
