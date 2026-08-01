package app

import (
	"strings"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The speech doctor.
//
// Its whole value is that it answers the same question the running server
// answers, from the same place. A second implementation of "is there a key"
// would put it in the same category as the screen it exists to explain — and
// the first version of it did exactly that, reported "no key on this instance"
// against a server that plainly had one, and sent its operator to look at
// settings that were never the problem.

func TestTheDoctorReadsTheKeyTheServerReads(t *testing.T) {
	a, _, _, _ := broadcastApp(t)
	ctx := t.Context()

	// Nothing stored and nothing in this process's environment.
	key, source := a.smartKeyAndSource(ctx)
	if key != "" {
		t.Fatalf("a key appeared from nowhere: %d chars from %q", len(key), source)
	}

	// Stored. The stored key WINS over the environment — someone who pastes a
	// key into the running server means it — so this is also the assertion that
	// the doctor agrees with app.go's own resolver about precedence.
	const stored = "sk-test-value-not-a-real-credential"
	if err := a.settings.SetSystemSecret(ctx, store.KeyOpenAIAPIKey, stored, "test"); err != nil {
		t.Skipf("this instance cannot store secrets: %v", err)
	}
	key, source = a.smartKeyAndSource(ctx)
	if key != stored {
		t.Errorf("the doctor read %d characters, want the stored key", len(key))
	}
	if !strings.Contains(source, "Settings") {
		t.Errorf("source = %q, want the stored setting", source)
	}
}

func TestTheDoctorNeverPrintsTheKey(t *testing.T) {
	// It runs in a terminal, and a terminal is read over shoulders, pasted into
	// issues and captured by screen recorders. The length and the source are
	// enough to act on; the value never is.
	a, _, _, _ := broadcastApp(t)
	ctx := t.Context()
	const stored = "sk-test-a-very-recognisable-secret-value"
	if err := a.settings.SetSystemSecret(ctx, store.KeyOpenAIAPIKey, stored, "test"); err != nil {
		t.Skipf("this instance cannot store secrets: %v", err)
	}

	step := DoctorStep{Name: "smart+ key", OK: true, Detail: "39 characters, from Settings → Smart+"}
	if strings.Contains(step.String(), stored) {
		t.Fatal("the key reached the output")
	}
	// And the same for the whole run, on the path that does not need a network.
	for _, s := range a.DoctorSpeech(ctx, store.Scope{}, false) {
		if strings.Contains(s.String(), stored) {
			t.Fatalf("the key reached %q", s.Name)
		}
	}
}

func TestWithNoKeyTheDoctorSaysWhereToLook(t *testing.T) {
	// The failure mode this command exists to prevent is an operator sent to
	// the wrong screen. A server started from a shell that exported
	// OPENAI_API_KEY has a key no other shell can see, so "no key" on its own
	// is a true statement that causes an hour of looking in settings.
	a, _, _, _ := broadcastApp(t)
	steps := a.DoctorSpeech(t.Context(), store.Scope{}, false)
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	var joined string
	for _, s := range steps {
		if s.OK {
			t.Errorf("step %q passed with no key configured", s.Name)
		}
		joined += s.String() + "\n"
	}
	for _, want := range []string{"OPENAI_API_KEY", "same shell", "Settings"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the no-key advice does not mention %q:\n%s", want, joined)
		}
	}
}

func TestTheFreeRunNeverSpends(t *testing.T) {
	// Without -full nothing is written and nothing is synthesised. An operator
	// diagnosing a bill must not be charged for asking.
	a, voice, sc, _ := broadcastApp(t)
	_ = a.DoctorSpeech(t.Context(), sc, false)
	if voice.count() != 0 {
		t.Errorf("the free run synthesised %d times", voice.count())
	}
}
