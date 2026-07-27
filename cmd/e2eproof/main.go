// Command e2eproof runs the real analysis path against a real database copy.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/analyze"
	"github.com/monstercameron/ArticleFlux/internal/classify"
	"github.com/monstercameron/ArticleFlux/internal/classify/lexicon"
	"github.com/monstercameron/ArticleFlux/internal/jobs"
	"github.com/monstercameron/ArticleFlux/internal/pipeline"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

func main() {
	path := flag.String("db", "", "database to analyse (a COPY)")
	rounds := flag.Int("rounds", 8, "backfill sweeps to run")
	seed := flag.String("seedfrom", "", "copy real articles out of this database first")
	seedN := flag.Int("seedn", 1200, "how many articles to copy")
	flag.Parse()

	db, err := store.Open(store.Options{Path: *path})
	must(err)
	defer db.Close()
	n, err := db.Migrate(context.Background())
	must(err)
	fmt.Printf("migrations: %d applied/verified\n", n)

	repo := store.NewReaderRepo(db)
	if *seed != "" {
		must(seedFrom(context.Background(), *seed, repo, *seedN))
	}
	lx := classify.MustCompile(lexicon.Categories())
	svc := analyze.New(repo, pipeline.New(lx, classify.DefaultStrategy(), nil), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pool := jobs.New(repo, jobs.Options{Workers: 4, Idle: 20 * time.Millisecond})
	pool.Handle(store.JobAnalyze, svc.Handle)
	pool.Start(ctx)
	defer pool.Stop()

	total := 0
	for i := range *rounds {
		queued, err := svc.Backfill(ctx, 250)
		must(err)
		total += queued
		fmt.Printf("sweep %d: queued %d\n", i+1, queued)
		if queued == 0 && i > 0 {
			break
		}
		drain(ctx, repo)
	}
	drain(ctx, repo)
	fmt.Printf("\nqueued %d items in total\n\n", total)

	report(ctx, db)
}

func drain(ctx context.Context, repo *store.ReaderRepo) {
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		d, err := repo.QueueDepth(ctx)
		must(err)
		if d[store.JobAnalyze].Queued == 0 && d[store.JobAnalyze].Running == 0 {
			time.Sleep(150 * time.Millisecond)
			d, _ = repo.QueueDepth(ctx)
			if d[store.JobAnalyze].Queued == 0 && d[store.JobAnalyze].Running == 0 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func report(ctx context.Context, db *store.DB) {
	proof, err := db.LoadAnalysisProof(ctx, 14)
	must(err)
	fmt.Printf("items in database : %d\n", proof.Items)
	percent := 0.0
	if proof.Items > 0 {
		percent = 100 * float64(proof.Analysed) / float64(proof.Items)
	}
	fmt.Printf("analysis rows     : %d  (%.1f%%)\n\n", proof.Analysed, percent)
	fmt.Print("genres stored     : ")
	for _, genre := range proof.Genres {
		g := genre.Name
		n := genre.Count
		fmt.Printf("%s %d · ", g, n)
	}
	fmt.Println()

	// A real sample: the newest analysed articles and what they were filed as.
	fmt.Println("newest analysed articles:")
	for _, item := range proof.Recent {
		title := item.Title
		scores := item.CategoryScores
		fmt.Printf("  %-12s %s\n", top(scores), trunc(title, 68))
	}
}

func top(scoresJSON string) string {
	var m map[string]float64
	if err := jsonUnmarshal(scoresJSON, &m); err != nil || len(m) == 0 {
		return "[unsorted]"
	}
	type kv struct {
		k string
		v float64
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	if all[0].v < classify.DefaultStrategy().MinScore {
		return "[unsorted]"
	}
	return all[0].k
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

func must(err error) {
	if err != nil {
		fmt.Println("ERROR:", err)
		os.Exit(1)
	}
}
