package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/ArticleFlux/internal/smart"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// Why read-to-me is silent, answered where the answer actually is.
//
// # The gap this closes
//
// The slideshow can say four things about a silent narrator, and only one of
// them is ever true here: "the voice didn't start". It cannot say more, and not
// through carelessness — an <audio> element reports a MediaError with a DECODE
// code and no HTTP status, so the client genuinely cannot tell a refused key
// from a request that never arrived. Meanwhile the server knows exactly what
// happened and is forbidden from saying: the provider's message can quote the
// article being read aloud, and §22.11 keeps request content out of anything
// that leaves this process.
//
// The result was a reader looking at four green prerequisites and a silent
// screen, with the real answer in a log they had to know to open. That is not a
// diagnosis, it is homework.
//
// # Why "ready" was never a lie, and never enough
//
// `An OpenAI key on the server: ready` is `strings.TrimSpace(key) != ""`. It
// says a key is STORED. It cannot say the key works without spending money to
// find out, so it does not claim to — and an expired key, a revoked key, a
// project with no credit and a model the account cannot reach all read as
// ready, because from where that check stands they are.
//
// So this is the check that DOES spend, run deliberately, from a terminal, by
// the person who owns the account. It runs the same chain /speech runs and
// prints what each step actually said.

// DoctorStep is one check, and what it found.
type DoctorStep struct {
	Name string
	// OK is whether this step passed. A step that could not run at all — no key
	// to test with — is not OK and says why rather than being skipped silently.
	OK bool
	// Detail is what to do about it, or what was found. Never the credential.
	Detail string
	// Err is the provider's own message, verbatim. Safe HERE and nowhere else:
	// this is a terminal on the operator's machine, not an HTTP response, and
	// the whole point is to show what the browser is not allowed to see.
	Err error
}

func (s DoctorStep) String() string {
	mark := "FAIL"
	if s.OK {
		mark = "ok  "
	}
	out := fmt.Sprintf("%s  %-28s %s", mark, s.Name, s.Detail)
	if s.Err != nil {
		out += "\n         " + s.Err.Error()
	}
	return out
}

// DoctorSpeech runs the listening chain and reports where it breaks.
//
// `full` decides whether it SPENDS. Without it every check is free — a stored
// key, the account's model list, and whether the models this instance is
// configured to use are in it — which is enough to find the great majority of
// failures. With it, one real segment is written and one real sentence is
// synthesised, which is the only way to prove the whole path end to end.
func (a *App) DoctorSpeech(ctx context.Context, sc store.Scope, full bool) []DoctorStep {
	var out []DoctorStep
	add := func(s DoctorStep) { out = append(out, s) }

	// --- 1. is there a key, and WHERE did it come from ----------------------
	//
	// The source is half the answer and the half that gets missed. The
	// resolution is stored-setting first, environment second — so a server
	// started from a shell that exported OPENAI_API_KEY has a key that no
	// check run from a DIFFERENT shell can see. A tool that reported "no key"
	// there would send its operator to look at settings that were never the
	// problem, which is exactly the loop this command exists to end.
	//
	// Resolved through the app's own function rather than a second copy of it,
	// for the same reason: two implementations of "is there a key" is how a
	// screen comes to say ready while the request answers 501.
	key, source := a.smartKeyAndSource(ctx)
	if key == "" {
		add(DoctorStep{Name: "smart+ key", Detail: "none stored, and OPENAI_API_KEY is not set in THIS shell"})
		add(DoctorStep{Name: "  where to look",
			Detail: "if the server was started from a shell that exported OPENAI_API_KEY, it has a key " +
				"this check cannot see — run this from that same shell, or paste the key into " +
				"Settings → Smart+ so it is stored and survives a restart"})
		return out
	}
	// Length and source only. Never the value, and never a prefix long enough
	// to be useful to somebody reading a terminal over a shoulder.
	add(DoctorStep{Name: "smart+ key", OK: true,
		Detail: fmt.Sprintf("%d characters, from %s", len(key), source)})

	// --- 2. does the provider accept it -------------------------------------
	//
	// The models list is free and it is the whole answer for an expired key, a
	// revoked key, a project with no credit or a wrong organisation — every one
	// of which reads as "ready" to the prerequisite check.
	client := llm.New(func(context.Context) string { return key })
	models, err := client.Models(ctx)
	if err != nil {
		add(DoctorStep{Name: "provider accepts the key",
			Detail: "the key is stored but the provider refused it", Err: err})
		return out
	}
	add(DoctorStep{Name: "provider accepts the key", OK: true,
		Detail: fmt.Sprintf("%d models available to this account", len(models))})

	// --- 3. can this instance reach the models it is configured to use -------
	//
	// The one nobody thinks to check. A model id that the account cannot use
	// fails every call with a provider error, while the key is valid, the
	// switches are on, and the prerequisite screen is entirely green.
	have := make(map[string]bool, len(models))
	for _, m := range models {
		have[m] = true
	}
	for _, tier := range []store.ModelTier{store.ModelTierSmall, store.ModelTierMid, store.ModelTierLarge} {
		want, terr := a.settings.ModelForTier(ctx, tier)
		if terr != nil || strings.TrimSpace(want) == "" {
			continue
		}
		if have[want] {
			add(DoctorStep{Name: "model " + string(tier), OK: true, Detail: want})
			continue
		}
		add(DoctorStep{Name: "model " + string(tier),
			Detail: want + " is set but this account cannot reach it — Settings → Smart+"})
	}

	// --- 4. the voice's own model -------------------------------------------
	//
	// Separate from the tiers above because it is a different endpoint with its
	// own model namespace: a speech model is not in the responses model list,
	// so its absence there proves nothing and its presence would be luck.
	add(DoctorStep{Name: "voice endpoint", OK: a.speak != nil && a.speak.Configured(ctx),
		Detail: doctorVoiceDetail(a, ctx)})

	if !full {
		add(DoctorStep{Name: "end to end", Detail: "not run — pass -full to write and synthesise one real segment (this spends)"})
		return out
	}

	// --- 5. the whole chain, for real ---------------------------------------
	it, derr := a.doctorItem(ctx, sc)
	if derr != nil {
		add(DoctorStep{Name: "end to end", Detail: "no article to test with", Err: derr})
		return out
	}

	text, werr := a.podcastFor(ctx, it, store.Item{}, smart.DefaultVibe, nil, false)
	if strings.TrimSpace(text) == "" {
		// The BROADCAST's fallback, because that is what this check is
		// diagnosing — the same rendering writeBeat would have used. See
		// speechAnnounced.
		//
		// Applied BEFORE the step is reported, not after: it used to run below
		// the switch, where nothing read it again, so the fallback was computed
		// and thrown away and the step said "0 words" about a segment the
		// reader would have heard in full.
		text = speechAnnounced(it)
	}
	switch {
	case errors.Is(werr, smart.ErrNothingToSummarise):
		add(DoctorStep{Name: "write a segment",
			Detail: "this article has no text to work from; the reader would hear the article itself"})
	case werr != nil:
		add(DoctorStep{Name: "write a segment", Detail: "the model refused", Err: werr})
		return out
	default:
		add(DoctorStep{Name: "write a segment", OK: true,
			Detail: fmt.Sprintf("%d words", len(strings.Fields(text)))})
	}

	// One short sentence rather than the segment: this proves the endpoint, the
	// key and the voice, and there is no reason to pay for a minute of audio to
	// learn it.
	audio, serr := a.speak.Speak(ctx, "doctor:"+it.ID, doctorProbeLine, "", "")
	if serr != nil {
		add(DoctorStep{Name: "synthesise it", Detail: "the voice endpoint refused", Err: serr})
		return out
	}
	add(DoctorStep{Name: "synthesise it", OK: true, Detail: fmt.Sprintf("%d bytes of audio", len(audio))})
	return out
}

// doctorProbeLine is what gets synthesised for the end-to-end check. Short, and
// about the check itself, so a recording that somehow reaches a listener is not
// a fragment of their own reading read back at them.
const doctorProbeLine = "Listening is configured correctly on this server."

func doctorVoiceDetail(a *App, ctx context.Context) string {
	if a.speak == nil {
		return "no voice client on this instance"
	}
	if !a.speak.Configured(ctx) {
		return "no key — the same one Smart+ uses, so fixing that fixes this"
	}
	return "configured"
}

// doctorItem picks something to test with: the newest article this reader can
// see that actually has text in it.
func (a *App) doctorItem(ctx context.Context, sc store.Scope) (store.Item, error) {
	items, _, err := a.repo.ListItems(ctx, sc, store.ListQuery{Limit: 25})
	if err != nil {
		return store.Item{}, err
	}
	for _, s := range items {
		it, gerr := a.repo.GetItem(ctx, sc, s.ID)
		if gerr != nil {
			continue
		}
		if strings.TrimSpace(speechBody(it)) != "" {
			return it, nil
		}
	}
	return store.Item{}, errors.New("no article with usable text in the last 25")
}

// smartKeyAndSource resolves the credential the way the rest of the process
// does, and says where it came from.
//
// The source matters because it decides what the operator should do next: a
// stored key is changed in Settings, an environment key is changed in the shell
// that starts the server, and "neither, here" is not the same statement as
// "neither, anywhere".
func (a *App) smartKeyAndSource(ctx context.Context) (key, source string) {
	if a == nil {
		return "", ""
	}
	if a.settings != nil {
		if k, err := a.settings.SystemSecret(ctx, store.KeyOpenAIAPIKey); err == nil {
			if k = strings.TrimSpace(k); k != "" {
				return k, "Settings → Smart+"
			}
		} else if !errors.Is(err, store.ErrNoSetting) {
			// An undecryptable row is a different problem from an absent one,
			// and the remedy — paste the key again — silently abandons whatever
			// the old one was protecting. Worth saying rather than folding into
			// "no key".
			return "", "a stored key exists but could not be decrypted"
		}
	}
	if a.smartKey != nil {
		if k := strings.TrimSpace(a.smartKey(ctx)); k != "" {
			return k, "OPENAI_API_KEY in this shell"
		}
	}
	return "", ""
}
