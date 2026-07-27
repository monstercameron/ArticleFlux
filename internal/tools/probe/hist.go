package main

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/sanitize"
	"github.com/monstercameron/ArticleFlux/internal/store"
	"github.com/monstercameron/ArticleFlux/internal/textvec"
)

// hist reports the pairwise-similarity distribution among unread candidates, so the
// SameStoryThreshold can be chosen from the data rather than guessed.
func hist(ctx context.Context, repo *store.ReaderRepo, sc store.Scope) {
	cands, _, err := repo.ListItems(ctx, sc, store.ListQuery{UnreadOnly: true, Limit: 600})
	if err != nil {
		fmt.Println("hist:", err)
		return
	}
	corpus := textvec.NewCorpus()
	type c struct {
		id, src string
		pub     time.Time
		vec     textvec.Vector
	}
	texts := make([]string, 0, len(cands))
	for _, it := range cands {
		text := it.Title
		if it.Summary != "" {
			text += " " + sanitize.Text(it.Summary)
		}
		texts = append(texts, text)
		corpus.Add(text)
	}
	items := make([]c, 0, len(cands))
	for i, it := range cands {
		pub, _ := time.Parse(time.RFC3339Nano, it.PublishedAt)
		items = append(items, c{it.ID, it.SourceID, pub, corpus.TFIDF(texts[i])})
	}

	buckets := map[string]int{}
	var sims []float64
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			if items[i].src == items[j].src {
				continue
			}
			d := items[i].pub.Sub(items[j].pub)
			if d < 0 {
				d = -d
			}
			if d > 72*time.Hour {
				continue
			}
			s := textvec.Cosine(items[i].vec, items[j].vec)
			if s <= 0.05 {
				continue
			}
			sims = append(sims, s)
			switch {
			case s >= 0.7:
				buckets["0.70+"]++
			case s >= 0.6:
				buckets["0.60-0.70"]++
			case s >= 0.5:
				buckets["0.50-0.60"]++
			case s >= 0.45:
				buckets["0.45-0.50"]++
			case s >= 0.4:
				buckets["0.40-0.45"]++
			case s >= 0.3:
				buckets["0.30-0.40"]++
			case s >= 0.2:
				buckets["0.20-0.30"]++
			default:
				buckets["0.05-0.20"]++
			}
		}
	}
	sort.Float64s(sims)
	fmt.Printf("\n-- cross-source pair similarities (%d candidates, %d pairs > 0.05) --\n",
		len(items), len(sims))
	for _, k := range []string{"0.05-0.20", "0.20-0.30", "0.30-0.40", "0.40-0.45",
		"0.45-0.50", "0.50-0.60", "0.60-0.70", "0.70+"} {
		fmt.Printf("  %-10s %d\n", k, buckets[k])
	}
	if len(sims) > 0 {
		fmt.Printf("  max=%.3f  p99=%.3f\n", sims[len(sims)-1], sims[int(float64(len(sims))*0.99)])
	}
}
