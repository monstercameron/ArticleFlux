package smart

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/client/i18n"
	"github.com/monstercameron/ArticleFlux/internal/llm"
	"github.com/monstercameron/schemaflux/v2"
)

// On-the-fly UI translation (plan.md §22.16c): per-key caching, only the
// missing keys sent, the checks that keep a hole off the screen, and the
// boundary on what leaves.

// echoing answers every batch with "TR:" + the English it was sent.
func echoing(t *testing.T) interface{ CallCount() int } {
	t.Helper()
	return answering(t, func(_ int, r schemaflux.Request) (string, error) {
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})
}

// sentKeys collects every flat key the provider was asked about.
func sentKeys(t *testing.T, reqs []schemaflux.Request) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, r := range reqs {
		for k, v := range batchFromInput(t, r.System+r.User) {
			out[k] = v
		}
	}
	return out
}

func TestAWarmCatalogCostsNothing(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	prov := echoing(t)

	if _, err := tr.Catalog(ctx, "fr", false); err != nil {
		t.Fatal(err)
	}
	first := prov.CallCount()
	if first == 0 {
		t.Fatal("a cold catalog made no calls")
	}

	// Every way of asking again: the whole-catalog RPC, the per-key read, and a
	// background fill request. None may reach the model.
	if _, err := tr.Catalog(ctx, "fr", false); err != nil {
		t.Fatal(err)
	}
	got, pending, err := tr.Strings(ctx, "fr", nil)
	if err != nil || pending != 0 || len(got) == 0 {
		t.Fatalf("Strings on a warm cache: %d entries, %d pending, %v", len(got), pending, err)
	}
	running, err := tr.StartFill(ctx, "fr")
	if err != nil || running {
		t.Fatalf("StartFill on a complete locale: running=%v err=%v, want nothing to do", running, err)
	}
	if n := prov.CallCount(); n != first {
		t.Errorf("a warm cache made %d more provider calls", n-first)
	}
	if !tr.CachedLocales(ctx)["fr"] {
		t.Error("a complete locale is not reported as cached")
	}
}

func TestOnlyTheKeysANewBuildChangedAreSent(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	_ = echoing(t)
	if _, err := tr.Catalog(ctx, "de", false); err != nil {
		t.Fatal(err)
	}

	// Simulate a new build: three keys' English "changed", so their recorded
	// hashes no longer match.
	row, err := tr.readCache(ctx, "de")
	if err != nil {
		t.Fatal(err)
	}
	changed := []string{"login.submit", "list.readingTime", "smart.langFailed"}
	for _, k := range changed {
		row.Hashes[k] = "an-older-english"
	}
	if err := tr.writeCache(ctx, row); err != nil {
		t.Fatal(err)
	}
	_, pending, _ := tr.Strings(ctx, "de", changed)
	if pending != len(changed) {
		t.Fatalf("pending = %d, want %d", pending, len(changed))
	}

	prov := answering(t, func(_ int, r schemaflux.Request) (string, error) {
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})
	if _, err := tr.Catalog(ctx, "de", false); err != nil {
		t.Fatal(err)
	}
	if prov.CallCount() != 1 {
		t.Fatalf("re-translating three keys took %d calls", prov.CallCount())
	}
	got := sentKeys(t, prov.Requests())
	want := map[string]bool{"login.submit": true, "list.readingTime#one": true,
		"list.readingTime#other": true, "smart.langFailed": true}
	if len(got) != len(want) {
		t.Errorf("sent %d messages, want %d: %v", len(got), len(want), got)
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("%s was not re-sent", k)
		}
	}
}

func TestPolishIsAskedForEveryFormItsRuntimeSelects(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	prov := answering(t, func(_ int, r schemaflux.Request) (string, error) {
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})
	if _, err := tr.Catalog(context.Background(), "pl", false); err != nil {
		t.Fatal(err)
	}
	got := sentKeys(t, prov.Requests())
	for _, cat := range []string{"one", "few", "many", "other"} {
		if _, ok := got["list.readingTime#"+cat]; !ok {
			t.Errorf("Polish was not asked for list.readingTime#%s", cat)
		}
	}
	// And a form English lacks is sent with the English `other` as its source.
	en, _ := i18n.English("list.readingTime")
	if got["list.readingTime#few"] != en.Plural["other"] {
		t.Errorf("#few was sent as %q, want the English other %q", got["list.readingTime#few"], en.Plural["other"])
	}
	if _, ok := got["list.readingTime#two"]; ok {
		t.Error("Polish was asked for a dual it does not have")
	}
}

func TestABrokenTranslationIsRejectedAndNotPaidForTwice(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	prov := answering(t, func(_ int, r schemaflux.Request) (string, error) {
		batch := batchFromInput(t, r.System+r.User)
		if _, ok := batch["smart.langFailed"]; ok {
			// Lost its {err}, and wrapped in markup.
			batch["smart.langFailed"] = "<b>Fehler</b>"
			return echoEntriesJSON(map[string]string{"smart.langFailed": "<b>Fehler</b>"}), nil
		}
		return echoEntriesJSON(batch), nil
	})
	entries, err := tr.Catalog(ctx, "sv", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Key == "smart.langFailed" {
			t.Fatalf("a translation that lost its placeholder was served: %+v", e)
		}
	}
	row, _ := tr.readCache(ctx, "sv")
	if row.Rejected["smart.langFailed"] == "" {
		t.Error("the rejection was not recorded")
	}
	before := prov.CallCount()
	if _, err := tr.Catalog(ctx, "sv", false); err != nil {
		t.Fatal(err)
	}
	if prov.CallCount() != before {
		t.Error("a key already rejected for this English was paid for again")
	}
	// Force is the reader's lever and does retry it.
	if _, err := tr.Catalog(ctx, "sv", true); err != nil {
		t.Fatal(err)
	}
	if prov.CallCount() == before {
		t.Error("force did not re-ask")
	}
}

// TestUIEgressIsThisBuildsEnglishAndNothingElse is the §18.8 statement for UI
// translation: what leaves is the application's own compiled-in English, keyed
// by catalog key — no reader data exists on this path to leak, and this proves
// none was added.
func TestUIEgressIsThisBuildsEnglishAndNothingElse(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	prov := answering(t, func(_ int, r schemaflux.Request) (string, error) {
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})
	if _, err := tr.Catalog(context.Background(), "ar", false); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]string{}
	for _, e := range i18n.Export(i18n.DefaultLocale) {
		for k, v := range flattenFor(e, "ar") {
			allowed[k] = v
		}
	}
	for _, r := range prov.Requests() {
		for k, v := range batchFromInput(t, r.System+r.User) {
			want, ok := allowed[k]
			if !ok {
				t.Errorf("sent a key that is not in this build's catalog: %q", k)
				continue
			}
			if v != want {
				t.Errorf("%s was sent as %q, which is not this build's English %q", k, v, want)
			}
		}
	}
}

func TestNoKeyMeansNoEgress(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: false}, settings)
	prov := echoing(t)
	ctx := context.Background()
	if _, err := tr.Catalog(ctx, "fr", false); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("Catalog with no key: %v", err)
	}
	if _, err := tr.StartFill(ctx, "fr"); !errors.Is(err, llm.ErrNotConfigured) {
		t.Errorf("StartFill with no key: %v", err)
	}
	if _, _, err := tr.Strings(ctx, "fr", nil); err != nil {
		t.Errorf("Strings with no key must still answer from the cache: %v", err)
	}
	if prov.CallCount() != 0 {
		t.Errorf("%d provider calls with no key configured", prov.CallCount())
	}
}

// TestTheSpendCeilingStopsAFillWithoutLosingWork: the ceiling itself is
// enforced in internal/llm's funnel (TestTheSpendCeilingBindsTheTypedOperations
// Too); this pins what the translator does when it is told no. Nothing is
// marked rejected — the keys were never answered — so raising the ceiling and
// asking again picks up exactly where it stopped.
func TestTheSpendCeilingStopsAFillWithoutLosingWork(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	prov := answering(t, func(call int, r schemaflux.Request) (string, error) {
		if call >= 2 {
			return "", llm.ErrOverBudget
		}
		return echoEntriesJSON(batchFromInput(t, r.System+r.User)), nil
	})
	_, err := tr.Catalog(ctx, "it", false)
	if !errors.Is(err, llm.ErrOverBudget) {
		t.Fatalf("err = %v, want ErrOverBudget surfaced", err)
	}
	if prov.CallCount() != 3 {
		t.Errorf("%d calls; the fill should stop at the first refusal", prov.CallCount())
	}
	row, rerr := tr.readCache(ctx, "it")
	if rerr != nil {
		t.Fatalf("the two paid batches were not kept: %v", rerr)
	}
	if len(row.Rejected) != 0 {
		t.Errorf("%d keys marked rejected by a budget refusal", len(row.Rejected))
	}
	got, pending, _ := tr.Strings(ctx, "it", nil)
	if len(got) == 0 || pending == 0 {
		t.Errorf("after a stopped fill: %d valid, %d pending — want both non-zero", len(got), pending)
	}
}

func TestABackgroundFillLandsInTheCache(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	_ = echoing(t)

	running, err := tr.StartFill(ctx, "es")
	if err != nil || !running {
		t.Fatalf("StartFill: running=%v err=%v", running, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for tr.Filling("es") {
		if time.Now().After(deadline) {
			t.Fatal("the background fill never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, pending, err := tr.Strings(ctx, "es", []string{"login.submit", "list.readingTime", "not.a.key"})
	if err != nil || pending != 0 || len(got) != 2 {
		t.Fatalf("after the fill: %d entries, %d pending, %v", len(got), pending, err)
	}
	for _, e := range got {
		src, _ := i18n.English(e.Key)
		if e.SourceHash != i18n.SourceHash(src) {
			t.Errorf("%s carries source hash %q, want this build's", e.Key, e.SourceHash)
		}
		if !strings.HasPrefix(e.Text+e.Plural["other"], "TR:") {
			t.Errorf("%s = %+v", e.Key, e)
		}
	}
}

func TestALegacyRowIsHonouredAndUpgraded(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	prov := echoing(t)
	seedCache(t, settings, cached{
		SourceHash: hashOf(flatten(i18n.Export(i18n.DefaultLocale))), Locale: "nl",
		Entries: map[string]string{"login.submit": "Inloggen"},
	})
	got, err := tr.Catalog(ctx, "nl", false)
	if err != nil || len(got) != 1 || got[0].Text != "Inloggen" {
		t.Fatalf("legacy row: %+v, %v", got, err)
	}
	if prov.CallCount() != 0 {
		t.Errorf("a legacy row for the current English was re-billed: %d calls", prov.CallCount())
	}
}

// G18 of the second UX audit: a background fill that stops on a provider error
// is remembered, so the next read can say it stopped; starting another attempt
// clears the mark; a fill that finishes leaves none.
func TestAFillThatStoppedOnAnErrorIsRemembered(t *testing.T) {
	settings := newSettings(t)
	tr := NewTranslator(&fakeLLM{configured: true}, settings)
	ctx := context.Background()
	_ = answering(t, func(int, schemaflux.Request) (string, error) {
		return "", errors.New("provider returned 401: Incorrect API key provided")
	})
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for tr.Filling("es") {
			if time.Now().After(deadline) {
				t.Fatal("the background fill never finished")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	if tr.FillFailed("es") {
		t.Fatal("a fill that never ran is marked failed")
	}
	if running, err := tr.StartFill(ctx, "es"); err != nil || !running {
		t.Fatalf("StartFill: running=%v err=%v", running, err)
	}
	wait()
	if !tr.FillFailed("es") {
		t.Error("a fill that stopped on a provider error is not remembered")
	}
	if tr.FillFailed("fr") || tr.FillFailed("not-a-language") {
		t.Error("the mark leaked onto another language")
	}

	// Another attempt clears it while it runs, and a clean finish leaves none.
	_ = echoing(t)
	if running, err := tr.StartFill(ctx, "es"); err != nil || !running {
		t.Fatalf("second StartFill: running=%v err=%v", running, err)
	}
	wait()
	if tr.FillFailed("es") {
		t.Error("the mark survived a fill that finished cleanly")
	}
}
