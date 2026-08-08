package app

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// settingsSpend is the concrete half of llm.SpendStore, and it was the
// uncovered half. internal/llm's tests drive the interface against a map, which
// proves the meter arithmetic and nothing about whether the number survives a
// process. This is the part that has to.
//
// The property that carries the most weight here is not the round trip — it is
// that the write outlives its caller's context. SaveSpend is handed the LLM
// call's context, that call has just finished, and a handler returning
// immediately afterwards cancels it. A write that inherited that cancellation
// would lose exactly the spend that was just incurred, on every call, and the
// meter would read zero forever while money was going out.

func spendStore(t *testing.T) (settingsSpend, *bytes.Buffer) {
	t.Helper()
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "spend.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	return settingsSpend{
		settings: store.NewSettingsRepo(db, nil),
		log:      slog.New(slog.NewTextHandler(&logged, nil)),
	}, &logged
}

func TestTheSpendTotalSurvivesARoundTrip(t *testing.T) {
	s, _ := spendStore(t)
	ctx := context.Background()

	want := llm.Cost{USD: 12.3456, Priced: 41, Unpriced: 7}
	if err := s.SaveSpend(ctx, want); err != nil {
		t.Fatalf("SaveSpend: %v", err)
	}
	got, err := s.LoadSpend(ctx)
	if err != nil {
		t.Fatalf("LoadSpend: %v", err)
	}
	if got != want {
		t.Errorf("loaded %+v, saved %+v", got, want)
	}

	// And it replaces rather than accumulating. The client sends its whole
	// running total on every call, so a store that added would double the meter
	// on the second call of every session.
	next := llm.Cost{USD: 13.0, Priced: 42, Unpriced: 7}
	if err := s.SaveSpend(ctx, next); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.LoadSpend(ctx); got != next {
		t.Errorf("after a second save the total reads %+v, want %+v", got, next)
	}
}

// The bug this file was written into.
//
// SaveSpend used to record the author of the write as the literal string
// "system". `settings.updated_by` is `TEXT REFERENCES users(id)`, and no
// instance has a user whose id is "system" — so every save violated the foreign
// key and was rejected. internal/llm discards SaveSpend's error, and it has to:
// a settings write cannot be allowed to fail an LLM call that already happened
// and already cost money. So the durable meter never held a number, on any
// instance, and the only symptom was a cap that reset on restart — which is
// indistinguishable from not having persistence at all, which is the thing
// SpendStore was built to fix.
//
// This asserts the write REACHES THE TABLE, not merely that it returned nil.
func TestTheSpendTotalIsActuallyWrittenToTheSettingsTable(t *testing.T) {
	s, _ := spendStore(t)
	ctx := context.Background()

	if err := s.SaveSpend(ctx, llm.Cost{USD: 1.25, Priced: 2}); err != nil {
		t.Fatalf("SaveSpend: %v", err)
	}
	raw, err := s.settings.SystemValue(ctx, store.KeySmartSpendTotal)
	if err != nil {
		t.Fatalf("the spend row is not in the settings table: %v", err)
	}
	if !strings.Contains(raw, "1.25") {
		t.Errorf("the stored row is %q, which does not carry the total", raw)
	}
}

// The headline property. A cancelled context is the NORMAL case at this call
// site, not an edge one.
func TestTheSpendWriteOutlivesTheCallThatCausedIt(t *testing.T) {
	s, _ := spendStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the LLM call has returned and its context is done

	want := llm.Cost{USD: 4.5, Priced: 3}
	if err := s.SaveSpend(ctx, want); err != nil {
		t.Fatalf("SaveSpend under a cancelled context: %v — the spend that was just "+
			"incurred is lost, and it is lost on every call", err)
	}
	if got, _ := s.LoadSpend(context.Background()); got != want {
		t.Errorf("the total reads %+v after a cancelled write, want %+v", got, want)
	}
}

// A fresh instance has spent nothing, and that is not a failure to report.
func TestAnAbsentSpendRowReadsAsZero(t *testing.T) {
	s, logged := spendStore(t)

	got, err := s.LoadSpend(context.Background())
	if err != nil {
		t.Fatalf("LoadSpend on a fresh instance: %v", err)
	}
	if (got != llm.Cost{}) {
		t.Errorf("a fresh instance reports %+v spent", got)
	}
	if strings.Contains(logged.String(), "could not be read") {
		t.Errorf("a first run warned about a row that was never written:\n%s", logged.String())
	}
}

// A row that will not parse is reported and treated as zero. Refusing to serve
// would turn a corrupt number into an outage, and the number is a meter rather
// than a credential — but it must SAY so, or the meter silently restarts and
// the ceiling silently moves.
func TestACorruptSpendRowIsReportedAndTreatedAsZero(t *testing.T) {
	s, logged := spendStore(t)
	ctx := context.Background()

	if err := s.settings.SetSystemValue(ctx, store.KeySmartSpendTotal,
		"{not json at all", ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadSpend(ctx)
	if err != nil {
		t.Errorf("LoadSpend returned an error for a corrupt row: %v — a bad meter "+
			"reading must not stop the instance", err)
	}
	if (got != llm.Cost{}) {
		t.Errorf("a corrupt row produced %+v", got)
	}
	out := logged.String()
	if !strings.Contains(out, "could not be read") {
		t.Errorf("the corrupt row was swallowed silently:\n%s", out)
	}
	if !strings.Contains(out, string(store.KeySmartSpendTotal)) {
		t.Errorf("the warning does not name the setting to go and look at:\n%s", out)
	}
}

// A blank row — written by hand, or left behind by something that cleared it —
// is the same case as no row, and specifically NOT a parse failure to warn
// about.
func TestABlankSpendRowIsNotTreatedAsCorrupt(t *testing.T) {
	s, logged := spendStore(t)
	ctx := context.Background()

	for _, raw := range []string{"", "   ", "\n\t"} {
		if err := s.settings.SetSystemValue(ctx, store.KeySmartSpendTotal, raw, ""); err != nil {
			t.Fatal(err)
		}
		got, err := s.LoadSpend(ctx)
		if err != nil || (got != llm.Cost{}) {
			t.Errorf("LoadSpend(%q) = (%+v, %v)", raw, got, err)
		}
	}
	if strings.Contains(logged.String(), "could not be read") {
		t.Errorf("a blank row was reported as corrupt:\n%s", logged.String())
	}
}

// An app assembled without a settings repository degrades to forgetting rather
// than panicking on the first priced call. That is the shape every CLI
// subcommand opens, and the one a nil check exists for.
func TestASpendStoreWithNoSettingsIsInertRatherThanFatal(t *testing.T) {
	s := settingsSpend{}
	ctx := context.Background()

	if err := s.SaveSpend(ctx, llm.Cost{USD: 1}); err != nil {
		t.Errorf("SaveSpend with no settings repository: %v", err)
	}
	got, err := s.LoadSpend(ctx)
	if err != nil || (got != llm.Cost{}) {
		t.Errorf("LoadSpend with no settings repository = (%+v, %v)", got, err)
	}
	// And the warning path tolerates a missing logger, which is the same
	// assembly with one more piece absent.
	s.warn("this must not panic", context.Canceled)
}

// A write that fails is both reported to the caller AND logged, because the two
// audiences are different: the client uses the error to stop trusting its
// total, and the operator needs the sentence explaining why the ceiling is
// about to be short.
func TestAFailedSpendWriteIsBothReturnedAndLogged(t *testing.T) {
	db, err := store.Open(store.Options{Path: filepath.Join(t.TempDir(), "closed.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	s := settingsSpend{
		settings: store.NewSettingsRepo(db, nil),
		log:      slog.New(slog.NewTextHandler(&logged, nil)),
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := s.SaveSpend(context.Background(), llm.Cost{USD: 1}); err == nil {
		t.Error("a write to a closed database reported success")
	}
	if !strings.Contains(logged.String(), "was not saved") {
		t.Errorf("the failed write was not logged:\n%s", logged.String())
	}
}
