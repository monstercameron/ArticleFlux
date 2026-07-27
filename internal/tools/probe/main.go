// Command probe runs one derivation and reports corroboration behaviour.
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
		ranked, _ := repo.HomeRanking(ctx, sc, 200)
		fmt.Printf("home_ranking: %d rows\n", len(ranked))

		corr, dupe, slots := 0, 0, map[string]int{}
		for _, r := range ranked {
			slots[r.Slot]++
			for _, why := range r.Reasons {
				if len(why) > 0 {
					switch {
					case contains(why, "carried"), contains(why, "sources"), contains(why, "corrobor"):
						corr++
					case contains(why, "already shown"), contains(why, "close to something"):
						dupe++
					}
				}
			}
		}
		fmt.Printf("slots: %v\n", slots)
		fmt.Printf("rows citing corroboration: %d\n", corr)
		fmt.Printf("rows citing duplication:   %d\n", dupe)
		fmt.Println("\n-- first 12 reason sets --")
		for i, r := range ranked {
			if i >= 12 {
				break
			}
			fmt.Printf("  #%-3d %-7s %.3f  %v\n", r.Rank, r.Slot, r.Score, r.Reasons)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
