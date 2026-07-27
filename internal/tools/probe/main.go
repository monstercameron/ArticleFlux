// Command probe runs one derivation and reports what it did.
//
//	probe <db> [hist]
//
// With no second argument it runs the derivation and reports corroboration
// behaviour. With `hist` it also prints the pairwise-similarity distribution
// among unread candidates, which is how SameStoryThreshold gets chosen from
// data rather than guessed. That is a separate word because it is O(n²) over
// 600 items and nobody wants it on every run.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/derive"
	"github.com/monstercameron/ArticleFlux/internal/store"
)

func main() {
	ctx := context.Background()
	db, err := store.Open(store.Options{Path: os.Args[1]})
	if err != nil {
		panic(err)
	}
	defer db.Close()
	repo := store.NewReaderRepo(db)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	scopes, err := repo.ScopesToDerive(ctx, time.Now().UTC().Add(-derive.Window))
	if err != nil {
		panic(err)
	}
	svc := derive.New(repo, log)
	for _, sc := range scopes {
		if err := svc.Run(ctx, sc, time.Now().UTC()); err != nil {
			fmt.Println("run:", err)
			continue
		}
		terms(ctx, repo, sc)
		if len(os.Args) > 2 && os.Args[2] == "hist" {
			hist(ctx, repo, sc)
		}
		ranked, _ := repo.HomeRanking(ctx, sc, 200)
		fmt.Printf("home_ranking: %d rows\n", len(ranked))

		// Counted on the reason's TERM rather than by grepping its prose.
		//
		// This used to match English substrings — "carried", "already shown" — because a
		// stored reason was only a sentence. store.RankReason now carries the scoring
		// term alongside the prose, so the count is exact and survives any rewording of
		// the sentences in internal/rank.
		slots, terms, plus := map[string]int{}, map[string]int{}, 0
		for _, r := range ranked {
			slots[r.Slot]++
			if r.Tier == store.TierSmartPlus {
				plus++
			}
			for _, why := range r.Reasons {
				terms[why.Term]++
			}
		}
		fmt.Printf("slots: %v\n", slots)
		fmt.Printf("reason terms: %v\n", terms)
		fmt.Printf("smart_plus rows: %d of %d\n", plus, len(ranked))
		fmt.Println("\n-- first 12 reason sets --")
		for i, r := range ranked {
			if i >= 12 {
				break
			}
			labels := make([]string, 0, len(r.Reasons))
			for _, why := range r.Reasons {
				labels = append(labels, why.Term)
			}
			fmt.Printf("  #%-3d %-7s %-10s %.3f  %v\n", r.Rank, r.Slot, r.Tier, r.Score, labels)
		}
	}
}
