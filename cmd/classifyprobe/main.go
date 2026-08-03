// Command classifyprobe runs the classification pipeline over real items from a
// live database and prints what it would assign, writing nothing.
//
// # Why this exists
//
// The engine (TODO 10.1-10.5) is unit-tested and measured against a labeled
// corpus, and neither of those answers the question somebody actually asks first:
// **does it do anything sensible to MY articles.** The corpus is 302 items chosen
// partly for difficulty; a reader's own feed is the real distribution, and the
// first honest look at it belongs before the job wiring rather than after.
//
// It is read-only and it takes no scope, because it reads `items`, which is
// global (A14). It writes nothing at all — not to the database and not to
// `item_analysis` — so it can be pointed at a production file safely.
//
//	go run ./cmd/classifyprobe -db articleflux.db -n 40
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

func main() {
	dbPath := flag.String("db", "articleflux.db", "path to the database")
	limit := flag.Int("n", 40, "how many recent items to show individually")
	sample := flag.Int("sample", 1500, "how many items to summarise over")
	showAll := flag.Bool("all", false, "print every sampled item, not just -n of them")
	// A sweep rather than a single value, because the first real run showed the
	// corpus-calibrated floor behaving very differently on live data — see the
	// note in classify.DefaultStrategy. One number would have hidden that.
	sweep := flag.String("sweep", "", "comma-separated MinScore values to compare, e.g. 1,1.5,2,3")
	mine := flag.Int("mine", 0, "print the top N unseen terms from UNSORTED items")
	flag.Parse()

	if *mine > 0 {
		if err := runMine(*dbPath, *sample, *mine); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *sweep != "" {
		if err := runSweep(*dbPath, *sample, *sweep); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(*dbPath, *limit, *sample, *showAll); err != nil {
		log.Fatal(err)
	}
}

// runSweep prints the unsorted rate against MinScore over real items.
func runSweep(dbPath string, sample int, spec string) error {
	db, err := store.Open(store.Options{Path: dbPath, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	items, err := loadItems(context.Background(), store.NewReaderRepo(db), sample)
	if err != nil {
		return err
	}
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		return err
	}

	fmt.Printf("\n  MinScore  categorised  unsorted  ambiguous  distinct-cats\n")
	fmt.Printf("  --------  -----------  --------  ---------  -------------\n")
	for _, s := range strings.Split(spec, ",") {
		var floor float64
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &floor); err != nil {
			return fmt.Errorf("bad -sweep value %q", s)
		}
		st := classify.DefaultStrategy()
		st.MinScore = floor
		svc := pipeline.New(lx, st, nil)
		out, err := svc.Analyze(context.Background(), items)
		if err != nil {
			return err
		}
		unsorted, ambiguous := 0, 0
		cats := map[string]bool{}
		for _, a := range out {
			if a.Primary == "" {
				unsorted++
			} else {
				cats[a.Primary] = true
			}
			if a.Ambiguous {
				ambiguous++
			}
		}
		n := float64(len(items))
		fmt.Printf("  %8.2f  %10.1f%%  %7.1f%%  %8.1f%%  %13d\n",
			floor, 100*float64(len(items)-unsorted)/n, 100*float64(unsorted)/n,
			100*float64(ambiguous)/n, len(cats))
	}
	fmt.Println()
	return nil
}

func run(dbPath string, limit, sample int, showAll bool) error {
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("cannot see the database: %w", err)
	}

	// Read-only, and immutable so that opening it cannot create or recover a WAL
	// beside a database another process is writing. A probe that perturbs the
	// thing it is measuring is not a probe.
	db, err := store.Open(store.Options{Path: dbPath, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("opening read-only: %w", err)
	}
	defer func() { _ = db.Close() }()

	items, err := loadItems(context.Background(), store.NewReaderRepo(db), sample)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return fmt.Errorf("no items in %s", dbPath)
	}

	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		return err
	}
	svc := pipeline.New(lx, classify.DefaultStrategy(), nil)

	out, err := svc.Analyze(context.Background(), items)
	if err != nil {
		return err
	}

	printItems(os.Stdout, items, out, limit, showAll)
	printSummary(os.Stdout, items, out, lx)
	return nil
}

// loadItems reads the newest articles through the repository rather than with a
// query of its own.
//
// A probe with its own SELECT is a second place that understands the schema, and
// it is the place nobody updates when a column moves — which is what the "no SQL
// outside internal/store" guard is for. store.RecentItemIDs and store.ItemsByID
// are the same pair internal/analyze uses, so this tool exercises the path the
// real job takes instead of a private imitation of it.
func loadItems(ctx context.Context, repo *store.ReaderRepo, limit int) ([]pipeline.Item, error) {
	ids, err := repo.RecentItemIDs(ctx, limit)
	if err != nil {
		return nil, err
	}
	rows, err := repo.ItemsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]pipeline.Item, 0, len(rows))
	for _, r := range rows {
		body := r.ContentHTML
		if body == "" {
			body = r.Summary
		}
		out = append(out, pipeline.Item{
			ID:      r.ID,
			Title:   r.Title,
			URL:     r.URL,
			Summary: r.Summary,
			// Plain text, as the pipeline requires — converting once here is what
			// the real job does too.
			Body:        sanitize.Text(body),
			WordCount:   r.WordCount,
			SourceTitle: r.SourceTitle,
		})
	}
	return out, nil
}

func printItems(w io.Writer, items []pipeline.Item, out []pipeline.Analysis, limit int, showAll bool) {
	n := limit
	if showAll || n > len(items) {
		n = len(items)
	}
	fmt.Fprintf(w, "\n=== %d most recent items ===\n\n", n)
	for i := range n {
		a := out[i]
		label := a.Primary
		if label == "" {
			label = "— unsorted —"
		}
		if len(a.Secondary) > 0 {
			label += " (+" + strings.Join(a.Secondary, ", ") + ")"
		}
		flag := " "
		if a.Ambiguous {
			flag = "?"
		}
		title := items[i].Title
		if len([]rune(title)) > 74 {
			title = string([]rune(title)[:71]) + "..."
		}
		fmt.Fprintf(w, "%s %-30s %-9s %s\n", flag, label, a.Genre, title)
		if a.Primary != "" {
			fmt.Fprintf(w, "  %-30s           %s\n", "", whyLine(a))
		}
	}
}

func whyLine(a pipeline.Analysis) string {
	if len(a.Keyphrases) == 0 {
		return ""
	}
	k := a.Keyphrases
	if len(k) > 5 {
		k = k[:5]
	}
	return "· " + strings.Join(k, ", ")
}

func printSummary(w io.Writer, items []pipeline.Item, out []pipeline.Analysis, lx *classify.Lexicon) {
	byCat := map[string]int{}
	byGenre := map[string]int{}
	unsorted, ambiguous, nonEnglish := 0, 0, 0

	for _, a := range out {
		if a.Primary == "" {
			unsorted++
		} else {
			byCat[a.Primary]++
		}
		if a.Ambiguous {
			ambiguous++
		}
		if a.Lang != "" && a.Lang != pipeline.LangEnglish {
			nonEnglish++
		}
		byGenre[a.Genre]++
	}

	fmt.Fprintf(w, "\n=== over %d items ===\n\n", len(items))

	// Every figure below is a share of len(items), and an empty corpus is not a
	// hypothetical: it is what a fresh instance, or a -sample that matched
	// nothing, hands this function. Dividing there produces NaN, and a report
	// reading "unsorted NaN%" is worse than no report — it looks like a result.
	if len(items) == 0 {
		fmt.Fprintln(w, "  no items matched; nothing to summarise")
		return
	}

	type kv struct {
		k string
		n int
	}
	var cats []kv
	for k, n := range byCat {
		cats = append(cats, kv{k, n})
	}
	sort.Slice(cats, func(i, j int) bool {
		if cats[i].n != cats[j].n {
			return cats[i].n > cats[j].n
		}
		return cats[i].k < cats[j].k
	})
	for _, c := range cats {
		fmt.Fprintf(w, "  %-14s %5d  %5.1f%%  %s\n", c.k, c.n,
			100*float64(c.n)/float64(len(items)), bar(c.n, len(items)))
	}
	fmt.Fprintf(w, "  %-14s %5d  %5.1f%%  %s\n", "(unsorted)", unsorted,
		100*float64(unsorted)/float64(len(items)), bar(unsorted, len(items)))

	var genres []kv
	for k, n := range byGenre {
		genres = append(genres, kv{k, n})
	}
	sort.Slice(genres, func(i, j int) bool { return genres[i].n > genres[j].n })
	fmt.Fprintf(w, "\n  genre: ")
	for i, g := range genres {
		if i == 6 {
			break
		}
		fmt.Fprintf(w, "%s %d · ", g.k, g.n)
	}

	fmt.Fprintf(w, "\n\n  categorised   %5.1f%%\n", 100*float64(len(items)-unsorted)/float64(len(items)))
	fmt.Fprintf(w, "  unsorted      %5.1f%%\n", 100*float64(unsorted)/float64(len(items)))
	fmt.Fprintf(w, "  ambiguous     %5.1f%%   (would escalate to Smart+ as a tie-break)\n",
		100*float64(ambiguous)/float64(len(items)))
	fmt.Fprintf(w, "  non-English   %5.1f%%   (free tier declines; Smart+ handles these)\n",
		100*float64(nonEnglish)/float64(len(items)))
	fmt.Fprintf(w, "  distinct categories used: %d of %d\n", len(byCat), lx.Len())
}

func bar(n, total int) string {
	if total == 0 {
		return ""
	}
	w := n * 40 / total
	return strings.Repeat("#", w)
}

// runMine prints the vocabulary of the articles the classifier could not place.
//
// # Why this is the fix and a lower threshold is not
//
// Measured 2026-07-27: the shipped lexicon left 66% of 1,500 real items unsorted,
// and sweeping MinScore down to 0.75 still left 39%. That rules out the floor as
// the cause. The lexicon simply does not contain the words these feeds use —
// `sdr`, `lumens`, `duv`, `alternator`, `projector` — because 1,644 terms were
// written from general knowledge rather than from a reader's actual corpus.
//
// This ranks the candidates: n-grams that appear in UNSORTED items, that the
// lexicon is not already watching for, ordered by how many distinct unsorted
// items carry them. A term at the top of this list is one that would place the
// most articles.
//
// Document frequency, not total count: a word repeated forty times in one article
// is one article's worth of evidence, and ranking by raw count promotes the
// vocabulary of whichever single item happened to be longest.
func runMine(dbPath string, sample, top int) error {
	db, err := store.Open(store.Options{Path: dbPath, ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	items, err := loadItems(context.Background(), store.NewReaderRepo(db), sample)
	if err != nil {
		return err
	}
	lx, err := classify.Compile(lexicon.Categories())
	if err != nil {
		return err
	}
	svc := pipeline.New(lx, classify.DefaultStrategy(), nil)
	out, err := svc.Analyze(context.Background(), items)
	if err != nil {
		return err
	}

	docFreq := map[string]int{}
	unsorted := 0
	for i, a := range out {
		if a.Primary != "" {
			continue
		}
		unsorted++
		// Title and summary only. The body's vocabulary is mostly boilerplate and
		// navigation, and a term mined from it tends to place the site rather than
		// the article.
		seen := map[string]bool{}
		for _, ng := range ngrams(items[i].Title + ". " + items[i].Summary) {
			if seen[ng] || lx.Knows(ng) || !worthProposing(ng) {
				continue
			}
			seen[ng] = true
			docFreq[ng]++
		}
	}

	type kv struct {
		term string
		n    int
	}
	var cands []kv
	for t, n := range docFreq {
		if n < 3 {
			// Below three distinct articles a term is a coincidence, and adding it
			// is fitting the lexicon to one headline.
			continue
		}
		cands = append(cands, kv{t, n})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		return cands[i].term < cands[j].term
	})

	fmt.Printf("\n%d unsorted of %d items. Top %d unseen terms by DOCUMENT frequency:\n\n",
		unsorted, len(items), top)
	for i, c := range cands {
		if i == top {
			break
		}
		fmt.Printf("  %5d  %.1f%%  %s\n", c.n, 100*float64(c.n)/float64(unsorted), c.term)
	}
	fmt.Printf("\n(%d candidates appeared in 3+ unsorted articles)\n\n", len(cands))
	return nil
}

// ngrams generates the 1-, 2- and 3-grams a lexicon term could match, using the
// same scanner the classifier uses so that a proposed term is one that would
// actually fire.
func ngrams(text string) []string {
	toks := textvec.Scan(text)
	var out []string
	for i := range toks {
		out = append(out, toks[i].Text)
		for n := 2; n <= 3 && i+n <= len(toks); n++ {
			if toks[i+n-1].BreakBefore {
				break
			}
			parts := make([]string, 0, n)
			for j := range n {
				parts = append(parts, toks[i+j].Text)
			}
			out = append(out, strings.Join(parts, " "))
		}
	}
	return out
}

// worthProposing filters the miner's candidates down to things a person would
// actually write into a lexicon.
//
// The first run without this was 90 rows of "the", "and", "of the", "a new" —
// because the classifier's n-grams deliberately KEEP stopwords so that phrases
// like "war on drugs" stay adjacent (see textvec/scan.go). That is right for
// matching and useless for proposing, so the miner applies the interest layer's
// filter on the way out: an n-gram survives only if every one of its tokens is a
// term `textvec.Tokenize` would keep — no stopwords, no feed furniture
// ("comments", "read more", "appeared"), no bare numbers, nothing under
// MinTermLen.
//
// Requiring EVERY token to survive rather than at least one is deliberate. "of
// the iphone" contains a real term and is not a term; a candidate with a stopword
// in it is a fragment of a sentence rather than a name for anything.
func worthProposing(ngram string) bool {
	parts := strings.Fields(ngram)
	kept := textvec.Tokenize(ngram)
	if len(kept) != len(parts) || len(kept) == 0 {
		return false
	}
	for _, p := range kept {
		// Numeric leftovers: years, list positions, and the HTML entity numbers
		// that survive an un-decoded summary.
		if strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
			return false
		}
	}
	return true
}
