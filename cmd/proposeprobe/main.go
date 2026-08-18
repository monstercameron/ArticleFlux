// Command proposeprobe shows what the category-discovery pass WOULD propose,
// writing nothing.
//
// The same argument classifyprobe makes: the gate is unit-tested against
// synthetic clusters, and neither that nor a benchmark answers the question
// somebody actually asks before turning this on — what does it do to MY feed.
//
//	go run ./cmd/proposeprobe -db copy.db
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/propose"
	"github.com/monstercameron/ArticleFlux/internal/relabel"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

func main() {
	dbPath := flag.String("db", "articleflux.db", "path to a COPY of the database")
	limit := flag.Int("n", relabel.DiscoveryCorpusLimit, "how much of the pile to cluster")
	flag.Parse()

	ctx := context.Background()
	db, err := store.Open(store.Options{Path: *dbPath})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Migrate(ctx); err != nil {
		log.Fatal(err)
	}
	repo := store.NewReaderRepo(db)

	scopes, err := repo.ScopesWithSubscriptions(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, sc := range scopes {
		total, err := repo.UncategorisedCount(ctx, sc)
		if err != nil {
			log.Fatal(err)
		}
		docs, err := repo.UncategorisedForDiscovery(ctx, sc, *limit)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("\nuser %s — eligible pile %d (clustering %d)\n", sc.UserID, total, len(docs))
		if len(docs) == 0 {
			continue
		}

		pdocs := make([]propose.Doc, 0, len(docs))
		for _, d := range docs {
			pdocs = append(pdocs, propose.Doc{
				ItemID: d.ItemID, SourceID: d.SourceID,
				PublishedAt: d.PublishedAt, Vector: d.Vector,
			})
		}

		start := time.Now()
		res := propose.Propose(pdocs, nil, nil, propose.Defaults())
		fmt.Printf("clustered %d of %d in %v\n", res.Clustered, res.Scanned, time.Since(start).Round(time.Millisecond))

		by := map[string]int{}
		for _, r := range res.Rejections {
			by[r.Reason]++
		}
		keys := make([]string, 0, len(by))
		for k := range by {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Print("refused: ")
		for _, k := range keys {
			fmt.Printf("%s=%d  ", k, by[k])
		}
		fmt.Println()

		fmt.Printf("WOULD CREATE %d:\n", len(res.Proposals))
		for _, p := range res.Proposals {
			fmt.Printf("  %-28s  %4d items  %2d sources  cohesion %.2f  top-source %.0f%%  recent %.0f%%\n",
				propose.SuggestName(p.Terms), len(p.Members), p.Sources,
				p.Cohesion, p.TopSourceShare()*100, p.RecentShare()*100)
			fmt.Printf("      terms: %v\n", p.Terms)
		}

		// The near-misses are the interesting part: what the gate refused and by
		// how much says whether the thresholds are right for this corpus.
		sort.SliceStable(res.Rejections, func(i, j int) bool {
			return res.Rejections[i].Members > res.Rejections[j].Members
		})
		fmt.Println("largest refusals:")
		for i, r := range res.Rejections {
			if i >= 8 {
				break
			}
			fmt.Printf("  %-28s  %4d items  %s %s\n", r.Label, r.Members, r.Reason, r.Collided)
		}
	}
}
