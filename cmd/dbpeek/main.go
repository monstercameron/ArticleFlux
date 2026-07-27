package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/ext/fts5"
)

func main() {
	b, err := os.ReadFile("migrations/0025_item_revisions.sql")
	if err != nil {
		panic(err)
	}
	s := sha256.Sum256(b)
	fmt.Println("file 0025 sha:", hex.EncodeToString(s[:]))

	db, err := driver.Open("file:articleflux.db?mode=ro", fts5.Register)
	if err != nil {
		panic(err)
	}
	var sum string
	if err := db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=24`).Scan(&sum); err != nil {
		panic(err)
	}
	fmt.Println("row 24 sha:  ", sum)
	fmt.Println("match:", sum == hex.EncodeToString(s[:]))
}
