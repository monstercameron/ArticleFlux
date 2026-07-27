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
	var items, analysed int
	must(db.Read.QueryRowContext(ctx, `SELECT count(*) FROM items`).Scan(&items))
	must(db.Read.QueryRowContext(ctx, `SELECT count(*) FROM item_analysis`).Scan(&analysed))
	fmt.Printf("items in database : %d\n", items)
	fmt.Printf("analysis rows     : %d  (%.1f%%)\n\n", analysed, 100*float64(analysed)/float64(items))

	rows, err := db.Read.QueryContext(ctx, `
		SELECT genre, count(*) FROM item_analysis WHERE genre IS NOT NULL
		 GROUP BY genre ORDER BY count(*) DESC LIMIT 6`)
	must(err)
	defer rows.Close()
	fmt.Print("genres stored     : ")
	for rows.Next() {
		var g string
		var n int
		must(rows.Scan(&g, &n))
		fmt.Printf("%s %d · ", g, n)
	}
	fmt.Println("\n")

	// A real sample: the newest analysed articles and what they were filed as.
	sample, err := db.Read.QueryContext(ctx, `
		SELECT i.title, a.category_scores
		  FROM item_analysis a JOIN items i ON i.id = a.item_id
		 ORDER BY i.published_at DESC LIMIT 14`)
	must(err)
	defer sample.Close()
	fmt.Println("newest analysed articles:")
	for sample.Next() {
		var title, scores string
		must(sample.Scan(&title, &scores))
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
