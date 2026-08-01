package app

import (
	"errors"
	"net/url"
	"testing"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/smart"
)

// The observation that stops a screen claiming more than it knows.
//
// Every test here is about one sentence being sayable: "a key is stored, and
// the last attempt two minutes ago was refused". Before it, the screen could
// only say the first half, and the second half existed in a log the reader had
// to know to open.

func TestARefusalIsRecordedAndClearedBySuccess(t *testing.T) {
	// Cleared by success is the half that keeps it useful. A stale failure is
	// worse than none: an operator who fixed their key an hour ago and still
	// sees "refused" learns to ignore the field, and then it is furniture.
	var r refusal

	if kind, _ := r.last(); kind != "" {
		t.Errorf("a fresh process already reports %q", kind)
	}
	r.note(errors.New("401 Unauthorized: invalid_api_key"))
	kind, at := r.last()
	if kind != refusedKey {
		t.Errorf("class = %q, want %q", kind, refusedKey)
	}
	if at.IsZero() {
		t.Error("no time recorded")
	}
	r.note(nil)
	if kind, _ := r.last(); kind != "" {
		t.Errorf("a success left %q behind", kind)
	}
}

func TestEachRefusalClassNamesADifferentRemedy(t *testing.T) {
	// A class two people would fix the same way is a class that should be one.
	// These four are kept apart because the remedies are: replace the key, pay
	// the bill, change the model, fix the network.
	cases := []struct {
		err  string
		want string
	}{
		{"401 Unauthorized: incorrect API key provided", refusedKey},
		{"insufficient_quota: You exceeded your current quota", refusedQuota},
		{"model_not_found: the model gpt-9 does not exist", refusedModel},
		{"Post \"https://api.openai.com\": dial tcp: no such host", refusedReach},
		{"something nobody has seen before", refusedOther},
	}
	for _, c := range cases {
		if got := classifyRefusal(errors.New(c.err)); got != c.want {
			t.Errorf("%q classified as %q, want %q", c.err, got, c.want)
		}
	}
}

func TestAnEmptyArticleIsNotARefusal(t *testing.T) {
	// A two-line link post has nothing to summarise, and recording that as a
	// refusal would make every one of them look like a broken key — which is
	// the failure this whole field exists to stop, arriving from the other
	// direction.
	if got := classifyRefusal(smart.ErrNothingToSummarise); got != "" {
		t.Errorf("an empty article was classified as %q", got)
	}
	// A missing key IS one, and the most actionable of them.
	if got := classifyRefusal(llm.ErrNotConfigured); got != refusedKey {
		t.Errorf("a missing key classified as %q", got)
	}
}

func TestTheProvidersOwnWordsNeverReachTheClass(t *testing.T) {
	// The provider's message can quote the article being read aloud, and
	// §22.11 keeps request content out of anything leaving this process. The
	// class is short and fixed; the verbatim text stays in the log and in
	// `articleflux speech`.
	const leaky = "Invalid request: the text 'Cam's private reading' could not be processed"
	got := classifyRefusal(errors.New(leaky))
	if got != refusedOther {
		t.Fatalf("class = %q", got)
	}
	if len(got) > 24 {
		t.Errorf("the class is long enough to be carrying content: %q", got)
	}
}

func TestAFailedListenIsVisibleToTheScreen(t *testing.T) {
	// End to end through the real handler: a synthesis that fails has to leave
	// a mark the Smart+ screen can read, because the browser cannot see the
	// status and the reader is otherwise told only "the voice didn't start".
	a, voice, _, ids := broadcastApp(t)
	voice.fail = errors.New("401 Unauthorized: invalid_api_key")

	q := url.Values{}
	q.Set("item", ids[1])
	speak(t, a, q.Encode())

	kind, at := a.LastRefusal()
	if kind != refusedKey {
		t.Errorf("after a refused synthesis the server reports %q, want %q", kind, refusedKey)
	}
	if at.IsZero() {
		t.Error("no time recorded for the refusal")
	}

	// …and a listen that works clears it, so the screen speaks about the most
	// recent attempt rather than the worst one.
	voice.fail = nil
	speak(t, a, q.Encode())
	if kind, _ := a.LastRefusal(); kind != "" {
		t.Errorf("a successful listen left %q behind", kind)
	}
}
