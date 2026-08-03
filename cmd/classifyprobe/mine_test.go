package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

// The miner's two decisions — what counts as a candidate n-gram, and which
// candidates are worth putting in front of a person — are the reason this
// command exists. The lexicon left 66% of 1,500 real items unsorted because its
// 1,644 terms were written from general knowledge rather than from a reader's
// corpus, and these functions are how that gets fixed. A regression in either
// one produces a plausible-looking list of proposals that is quietly useless,
// which is exactly the failure the first run had.

// --- worthProposing -----------------------------------------------------------

func TestWorthProposingRejectsStopwordFragments(t *testing.T) {
	// The first run without this filter was 90 rows of exactly these, because
	// the classifier's n-grams deliberately KEEP stopwords so phrases like
	// "war on drugs" stay adjacent. Right for matching, useless for proposing.
	for _, ngram := range []string{
		"the", "and", "of the", "a new", "in a", "on the", "for the",
	} {
		if worthProposing(ngram) {
			t.Errorf("%q would be proposed as a lexicon term", ngram)
		}
	}
}

// Every token must survive, not merely one. "of the iphone" contains a real term
// and is not a term; a candidate with a stopword in it is a fragment of a
// sentence rather than a name for anything.
func TestWorthProposingRequiresEveryTokenToSurvive(t *testing.T) {
	if worthProposing("of the iphone") {
		t.Error("a fragment containing one real term was proposed")
	}
	if !worthProposing("iphone") {
		t.Error("a real single term was rejected")
	}
}

func TestWorthProposingRejectsBareNumbers(t *testing.T) {
	// Years, list positions, and the HTML entity numbers that survive an
	// un-decoded summary.
	for _, ngram := range []string{"2026", "1", "8217", "12 34"} {
		if worthProposing(ngram) {
			t.Errorf("%q is a number and would be proposed", ngram)
		}
	}
}

func TestWorthProposingRejectsNothing(t *testing.T) {
	for _, ngram := range []string{"", "   ", "\t"} {
		if worthProposing(ngram) {
			t.Errorf("%q was proposed", ngram)
		}
	}
}

// The candidates the whole exercise is for: real vocabulary the shipped lexicon
// did not contain.
func TestWorthProposingAcceptsRealVocabulary(t *testing.T) {
	for _, ngram := range []string{
		"sdr", "lumens", "alternator", "projector",
		"solid state drive", "electric vehicle",
	} {
		if !worthProposing(ngram) {
			t.Errorf("%q is the kind of term this miner exists to surface, and was filtered out", ngram)
		}
	}
}

// A term containing a digit is not a bare number — "h264" and "hdr10" name
// things. Only an ALL-digit token is a leftover.
func TestWorthProposingKeepsTermsThatMerelyContainDigits(t *testing.T) {
	for _, ngram := range []string{"h264", "rs232", "hdr10"} {
		if !worthProposing(ngram) {
			t.Errorf("%q contains digits but names something, and was filtered out", ngram)
		}
	}
}

// A REAL GAP IN THE MINER, pinned here because it is invisible from its output:
// a term shorter than textvec's MinTermLen can never be proposed, however often
// it appears in unsorted items.
//
// That is the correct rule for the interest layer — two-letter tokens are mostly
// noise, and this filter is deliberately the interest layer's own. But the
// miner's whole purpose is to find the vocabulary the shipped lexicon is missing,
// and "AI", "EV" and "4K" are three of the most common things a 2026 feed talks
// about. They will never appear in a proposal list, so an unsorted item whose
// only distinguishing term is "AI" stays unsorted and the miner reports nothing
// that would fix it.
//
// Not changed here: lowering MinTermLen affects matching everywhere, which is a
// product decision about the classifier rather than a fix to this probe. The
// options if it is ever worth addressing are a short-term allowlist or a separate
// floor for proposals. This test exists so the limitation is on the record and so
// that anyone who does change the floor sees what it was protecting.
func TestWorthProposingCannotSurfaceTwoLetterTerms(t *testing.T) {
	for _, ngram := range []string{"ai", "ev", "4k"} {
		if worthProposing(ngram) {
			t.Errorf("%q is now proposable — if MinTermLen changed, that is a product "+
				"decision worth confirming rather than an accident", ngram)
		}
	}
	// Three characters is where it starts working, which is the boundary.
	for _, ngram := range []string{"sdr", "usb"} {
		if !worthProposing(ngram) {
			t.Errorf("%q is three characters and should survive", ngram)
		}
	}
}

// --- ngrams -------------------------------------------------------------------

func TestNgramsEmitsUnigramsBigramsAndTrigrams(t *testing.T) {
	got := ngrams("solid state drive prices")
	has := func(s string) bool {
		for _, g := range got {
			if g == s {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"solid",             // unigram
		"solid state",       // bigram
		"solid state drive", // trigram
	} {
		if !has(want) {
			t.Errorf("ngrams did not produce %q: %v", want, got)
		}
	}
	// Nothing longer than three: a four-word phrase is a sentence, not a name.
	for _, g := range got {
		if len(strings.Fields(g)) > 3 {
			t.Errorf("ngrams produced a %d-word phrase: %q", len(strings.Fields(g)), g)
		}
	}
}

// N-grams must not span a break. "war on drugs" stays adjacent because it is one
// clause; a phrase built across a sentence boundary names nothing and would be
// proposed as if it did.
func TestNgramsDoNotSpanABreak(t *testing.T) {
	got := ngrams("first sentence. second sentence")
	for _, g := range got {
		if strings.Contains(g, "sentence second") {
			t.Errorf("an n-gram spanned a sentence break: %q", g)
		}
	}
}

func TestNgramsOnEmptyInputProducesNothing(t *testing.T) {
	if got := ngrams(""); len(got) != 0 {
		t.Errorf("ngrams(\"\") = %v", got)
	}
}

func TestNgramsOnASingleTokenProducesOnlyIt(t *testing.T) {
	got := ngrams("alternator")
	if len(got) != 1 || got[0] != "alternator" {
		t.Errorf("ngrams of one token = %v", got)
	}
}

// --- the report's formatting --------------------------------------------------

// bar renders a distribution. A division by zero here would crash the probe on
// the empty corpus, which is the first thing somebody runs it against.
func TestBarHandlesAnEmptyTotal(t *testing.T) {
	if got := bar(0, 0); got != "" {
		t.Errorf("bar(0, 0) = %q, want empty", got)
	}
}

func TestBarScalesToFortyColumns(t *testing.T) {
	if got := bar(1, 1); len(got) != 40 {
		t.Errorf("a full bar is %d columns, want 40", len(got))
	}
	if got := bar(1, 2); len(got) != 20 {
		t.Errorf("a half bar is %d columns, want 20", len(got))
	}
	if got := bar(0, 10); got != "" {
		t.Errorf("an empty bar is %q", got)
	}
	// Never wider than the terminal allowance, even if the counts disagree.
	if got := bar(5, 2); len(got) > 100 {
		t.Errorf("bar overflowed to %d columns", len(got))
	}
}

func TestWhyLineIsEmptyWithoutKeyphrases(t *testing.T) {
	if got := whyLine(pipeline.Analysis{}); got != "" {
		t.Errorf("whyLine with no keyphrases = %q", got)
	}
}

// The line goes next to a headline in a terminal, so it is capped at five
// phrases — an uncapped one wraps and the table stops being readable.
func TestWhyLineCapsAtFiveKeyphrases(t *testing.T) {
	a := pipeline.Analysis{Keyphrases: []string{"one", "two", "three", "four", "five", "six", "seven"}}
	got := whyLine(a)
	if strings.Contains(got, "six") || strings.Contains(got, "seven") {
		t.Errorf("whyLine did not cap the list: %q", got)
	}
	if n := strings.Count(got, ",") + 1; n != 5 {
		t.Errorf("whyLine showed %d phrases, want 5: %q", n, got)
	}
	if !strings.HasPrefix(got, "· ") {
		t.Errorf("whyLine lost its marker: %q", got)
	}
}

func TestWhyLineShowsEverythingUnderTheCap(t *testing.T) {
	got := whyLine(pipeline.Analysis{Keyphrases: []string{"sdr", "lumens"}})
	if got != "· sdr, lumens" {
		t.Errorf("whyLine = %q", got)
	}
}

// --- the report ---------------------------------------------------------------
//
// printItems and printSummary are the whole visible output of this command. The
// numbers in them are what somebody decides a lexicon change against, so a
// report that is merely plausible is worse than one that is obviously broken.

func TestPrintSummaryReportsTheSharesAndTheTotals(t *testing.T) {
	items := make([]pipeline.Item, 4)
	analyses := []pipeline.Analysis{
		{Primary: "technology", Genre: "news", Lang: pipeline.LangEnglish},
		{Primary: "technology", Genre: "news", Lang: pipeline.LangEnglish},
		{Primary: "science", Genre: "analysis", Lang: pipeline.LangEnglish, Ambiguous: true},
		{Primary: "", Genre: "news", Lang: "fr"}, // unsorted and non-English
	}

	var buf bytes.Buffer
	printSummary(&buf, items, analyses, nil)
	got := buf.String()

	for _, want := range []string{
		"over 4 items",
		"technology",
		"science",
		"(unsorted)",
		"categorised",
		"ambiguous",
		"non-English",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary omits %q:\n%s", want, got)
		}
	}
	// One of four unsorted is 25%, and the shares are the number the whole
	// exercise turns on — the shipped lexicon left 66% of 1,500 items unsorted.
	if !strings.Contains(got, "25.0%") {
		t.Errorf("the unsorted share is not 25%%:\n%s", got)
	}
}

// The categories are ordered by count, because the top of this list is what a
// person acts on. Ties break by name so two runs over the same corpus do not
// print a different order and read as a change.
func TestPrintSummaryOrdersCategoriesByCountThenName(t *testing.T) {
	items := make([]pipeline.Item, 6)
	analyses := []pipeline.Analysis{
		{Primary: "zebra"}, {Primary: "zebra"}, {Primary: "zebra"},
		{Primary: "alpha"}, {Primary: "alpha"},
		{Primary: "beta"},
	}
	var buf bytes.Buffer
	printSummary(&buf, items, analyses, nil)
	got := buf.String()

	z, a, b := strings.Index(got, "zebra"), strings.Index(got, "alpha"), strings.Index(got, "beta")
	if z < 0 || a < 0 || b < 0 {
		t.Fatalf("a category is missing:\n%s", got)
	}
	if !(z < a && a < b) {
		t.Errorf("categories are not ordered by count:\n%s", got)
	}
}

// The empty corpus — a fresh instance, or a -sample that matched nothing. Every
// figure is a share of len(items), so this used to divide by zero and print a
// report full of NaN, which looks like a result rather than like an absence.
func TestPrintSummaryOnAnEmptyCorpusSaysSoRatherThanPrintingNaN(t *testing.T) {
	var buf bytes.Buffer
	printSummary(&buf, nil, nil, nil)
	got := buf.String()

	if strings.Contains(got, "NaN") {
		t.Errorf("an empty corpus produced NaN figures:\n%s", got)
	}
	if !strings.Contains(got, "nothing to summarise") {
		t.Errorf("an empty corpus did not say it was empty:\n%s", got)
	}
}

func TestPrintItemsShowsTheLabelGenreAndTitle(t *testing.T) {
	items := []pipeline.Item{
		{Title: "Speculative decoding without a draft model"},
		{Title: "A field guide to Postgres lock modes"},
	}
	analyses := []pipeline.Analysis{
		{Primary: "technology", Genre: "news", Keyphrases: []string{"decoding", "draft model"}},
		{Primary: "", Genre: "tutorial"},
	}

	var buf bytes.Buffer
	printItems(&buf, items, analyses, 10, false)
	got := buf.String()

	if !strings.Contains(got, "technology") || !strings.Contains(got, "Speculative decoding") {
		t.Errorf("the first row is wrong:\n%s", got)
	}
	// An unsorted item has to be visibly unsorted rather than blank, or the
	// column reads as a rendering bug.
	if !strings.Contains(got, "unsorted") {
		t.Errorf("an unlabelled item was printed with an empty label:\n%s", got)
	}
	// The keyphrases are the "why", and they belong under the row they explain.
	if !strings.Contains(got, "decoding") {
		t.Errorf("the why-line is missing:\n%s", got)
	}
}

// A secondary label is additional information, not a replacement — the row has
// to show both or the probe understates what the classifier did.
func TestPrintItemsShowsSecondaryLabels(t *testing.T) {
	var buf bytes.Buffer
	printItems(&buf,
		[]pipeline.Item{{Title: "t"}},
		[]pipeline.Analysis{{Primary: "technology", Secondary: []string{"science"}}},
		1, false)
	got := buf.String()
	if !strings.Contains(got, "technology") || !strings.Contains(got, "science") {
		t.Errorf("a secondary label was dropped:\n%s", got)
	}
}

// Ambiguous items are the ones that would escalate to Smart+ as a tie-break, so
// they carry a marker a person can scan a column for.
func TestPrintItemsFlagsAmbiguousRows(t *testing.T) {
	var buf bytes.Buffer
	printItems(&buf,
		[]pipeline.Item{{Title: "t"}},
		[]pipeline.Analysis{{Primary: "technology", Ambiguous: true}},
		1, false)
	if !strings.Contains(buf.String(), "?") {
		t.Errorf("an ambiguous row carries no marker:\n%s", buf.String())
	}
}

// A limit larger than the corpus must print the corpus, not read past the end of
// the slice — which is a panic in a tool somebody runs against a small database.
func TestPrintItemsDoesNotReadPastTheEndOfTheCorpus(t *testing.T) {
	var buf bytes.Buffer
	printItems(&buf,
		[]pipeline.Item{{Title: "only one"}},
		[]pipeline.Analysis{{Primary: "technology"}},
		500, false)
	if !strings.Contains(buf.String(), "only one") {
		t.Errorf("the one item was not printed:\n%s", buf.String())
	}
}

func TestPrintItemsOnAnEmptyCorpusPrintsNoRows(t *testing.T) {
	var buf bytes.Buffer
	printItems(&buf, nil, nil, 10, false)
	if strings.Contains(buf.String(), "unsorted") {
		t.Errorf("an empty corpus produced rows:\n%s", buf.String())
	}
}

// Long titles are truncated so the table stays one row per item. A wrapped line
// makes the columns unreadable, which is the only thing this output is for.
func TestPrintItemsTruncatesALongTitle(t *testing.T) {
	long := strings.Repeat("x", 300)
	var buf bytes.Buffer
	printItems(&buf,
		[]pipeline.Item{{Title: long}},
		[]pipeline.Analysis{{Primary: "technology"}},
		1, false)
	got := buf.String()
	if strings.Contains(got, long) {
		t.Error("a 300-character title was printed in full")
	}
	if !strings.Contains(got, "...") {
		t.Errorf("the truncation is not marked:\n%s", got)
	}
}

// Multi-byte titles must not be cut mid-rune — the truncation counts runes, and
// a byte-wise cut puts a replacement character in the table.
func TestPrintItemsTruncatesOnRuneBoundaries(t *testing.T) {
	var buf bytes.Buffer
	printItems(&buf,
		[]pipeline.Item{{Title: strings.Repeat("é", 200)}},
		[]pipeline.Analysis{{Primary: "technology"}},
		1, false)
	if strings.Contains(buf.String(), "\uFFFD") {
		t.Error("a multi-byte title was cut mid-rune")
	}
}

// --- loadItems and run ----------------------------------------------------------
//
// loadItems reads through the REPOSITORY rather than with a query of its own,
// and that is the property worth pinning: a probe with its own SELECT is a
// second place that understands the schema, and it is the place nobody updates
// when a column moves. It uses the same RecentItemIDs/ItemsByID pair
// internal/analyze does, so this tool exercises the path the real job takes
// instead of a private imitation of it.

// seedProbeDB builds a database with real items in it and returns its path.
func seedProbeDB(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.db")
	db, err := store.Open(store.Options{Path: path})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repo := store.NewReaderRepo(db)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := repo.CreateTenantAndUser(ctx, store.NewTenant{
		TenantID: "t1", Name: "Test", UserID: "u1", Username: "cam",
		Hash: "x", Role: "superadmin", Now: now,
	}); err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	sc := store.Scope{TenantID: "t1", UserID: "u1", Role: "superadmin"}
	if _, _, err := repo.Subscribe(ctx, sc, store.NewSubscription{
		NaturalKey: "feed:a", FeedURL: "https://a.example/feed", Title: "Alpha Journal",
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	feeds, err := repo.ListFeeds(ctx, sc)
	if err != nil || len(feeds) == 0 {
		t.Fatalf("list feeds: %v", err)
	}

	var items []store.IngestItem
	for i := range n {
		items = append(items, store.IngestItem{
			GUID:  "g" + strconv.Itoa(i),
			Title: "Speculative decoding without a draft model " + strconv.Itoa(i),
			// HTML on purpose: loadItems must hand the pipeline PLAIN TEXT, and
			// tags reaching the classifier would be scored as vocabulary.
			ContentHTML: "<p>The <b>solid state drive</b> prices fell again.</p>",
			Summary:     "n-gram proposals and verification in one batch",
			PublishedAt: time.Now().Add(-time.Duration(i+1) * time.Hour),
			WordCount:   12,
		})
	}
	if _, err := repo.IngestItems(ctx, feeds[0].SourceID, items); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

func TestLoadItemsReadsThroughTheRepository(t *testing.T) {
	path := seedProbeDB(t, 5)
	db, err := store.Open(store.Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	got, err := loadItems(context.Background(), store.NewReaderRepo(db), 10)
	if err != nil {
		t.Fatalf("loadItems: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("loaded %d items, want 5", len(got))
	}
	for _, it := range got {
		if it.ID == "" || it.Title == "" {
			t.Errorf("an item came back hollow: %+v", it)
		}
		// The pipeline requires plain text. Markup reaching the classifier
		// would be scored as vocabulary — "p" and "b" as terms.
		if strings.Contains(it.Body, "<") {
			t.Errorf("markup survived into the body handed to the pipeline: %q", it.Body)
		}
		if !strings.Contains(it.Body, "solid state drive") {
			t.Errorf("the body lost its text: %q", it.Body)
		}
		// SourceTitle is deliberately NOT asserted here. It comes from the
		// global `sources` row, which a FETCH populates — a subscription's own
		// title is a different column. This fixture never fetches, so an empty
		// one is the honest state rather than a lost field.
	}
}

// The limit is what -sample controls, and it has to bound the read rather than
// be applied after loading everything — the corpus this runs against is the
// reason the flag exists.
func TestLoadItemsHonoursTheLimit(t *testing.T) {
	path := seedProbeDB(t, 8)
	db, err := store.Open(store.Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	got, err := loadItems(context.Background(), store.NewReaderRepo(db), 3)
	if err != nil {
		t.Fatalf("loadItems: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("loaded %d items for a limit of 3", len(got))
	}
}

func TestLoadItemsOnAnEmptyCorpusReturnsNothing(t *testing.T) {
	path := seedProbeDB(t, 0)
	db, err := store.Open(store.Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	got, err := loadItems(context.Background(), store.NewReaderRepo(db), 10)
	if err != nil {
		t.Fatalf("loadItems on an empty corpus: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("loaded %d items from an empty corpus", len(got))
	}
}

// A probe that perturbs the thing it is measuring is not a probe: run opens the
// database READ-ONLY and immutable, so it cannot create or recover a WAL beside
// one another process is writing.
func TestRunLeavesTheDatabaseAlone(t *testing.T) {
	path := seedProbeDB(t, 4)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	if err := run(path, 10, 10, false); err != nil {
		t.Fatalf("run: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Error("the probe modified the database it was measuring")
	}
	// Deliberately NOT asserting that no -wal/-shm appeared. A WAL-mode database
	// needs its shared-memory file to be read at all, so a read-only open can
	// create one where none existed; only immutable=1 would avoid that, and that
	// is a promise nobody can keep about a file another process is writing. The
	// guarantee is about the CONTENT of the database, which is what is checked
	// above. See store.Options.ReadOnly.
}

func TestRunReportsAMissingDatabase(t *testing.T) {
	err := run(filepath.Join(t.TempDir(), "nope.db"), 10, 10, false)
	if err == nil {
		t.Fatal("running against a database that does not exist reported success")
	}
	if !strings.Contains(err.Error(), "cannot see the database") {
		t.Errorf("the error does not say what was wrong: %v", err)
	}
}

// An empty corpus is a real state — a fresh instance, or a -sample that matched
// nothing — and it must be an honest error rather than a report full of zeroes
// that reads like a measurement.
func TestRunRefusesAnEmptyCorpus(t *testing.T) {
	err := run(seedProbeDB(t, 0), 10, 10, false)
	if err == nil {
		t.Fatal("running against an empty corpus reported success")
	}
	if !strings.Contains(err.Error(), "no items") {
		t.Errorf("the error does not say the corpus was empty: %v", err)
	}
}
