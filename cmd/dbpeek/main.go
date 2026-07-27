package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/monstercameron/ArticleFlux/internal/store"
)

func main() {
	b, err := os.ReadFile("migrations/0025_item_revisions.sql")
	if err != nil {
		panic(err)
	}
	s := sha256.Sum256(b)
	fmt.Println("file 0025 sha:", hex.EncodeToString(s[:]))

	sum, err := store.MigrationChecksum(context.Background(), "articleflux.db", 24)
	if err != nil {
		panic(err)
	}
	fmt.Println("row 24 sha:  ", sum)
	fmt.Println("match:", sum == hex.EncodeToString(s[:]))
}
