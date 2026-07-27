package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

// terms prints the derived vocabulary, separating phrases from single words so the
// brand/product signal can be judged on its own.
func terms(ctx context.Context, repo *store.ReaderRepo, sc store.Scope) {
	v, err := repo.TermAffinity(ctx, sc)
	if err != nil {
		fmt.Println("terms:", err)
		return
	}
	type tw struct {
		t string
		w float64
	}
	var words, phrases []tw
	for k, w := range v {
		if strings.Contains(k, " ") {
			phrases = append(phrases, tw{k, w})
		} else {
			words = append(words, tw{k, w})
		}
	}
	byW := func(s []tw) { sort.Slice(s, func(i, j int) bool { return s[i].w > s[j].w }) }
	byW(words)
	byW(phrases)

	fmt.Printf("\nvocabulary: %d terms (%d single words, %d phrases)\n",
		len(v), len(words), len(phrases))
	fmt.Print("top 20 words:   ")
	for i := 0; i < 20 && i < len(words); i++ {
		fmt.Printf("%s(%.2f) ", words[i].t, words[i].w)
	}
	fmt.Print("\ntop 25 PHRASES: ")
	for i := 0; i < 25 && i < len(phrases); i++ {
		fmt.Printf("%q(%.2f) ", phrases[i].t, phrases[i].w)
	}
	fmt.Println()
}
