// Command appicon writes the application's icons into web/icons.
//
//	go run ./internal/tools/appicon
//
// A tool rather than a `go:generate` line, for the same reason `pagescan` is one:
// the thing it produces is committed, so regenerating it is something a person
// does on purpose after changing a token — not something a build does silently on
// every run. `internal/appicon`'s test is what says the committed files are still
// what this writes.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/monstercameron/ArticleFlux/internal/appicon"
)

func main() {
	dir := flag.String("out", filepath.Join("web", "icons"),
		"where to write the icons; the default is where the manifest expects them")
	check := flag.Bool("check", false,
		"do not write; report whether what is on disk matches what would be written")
	flag.Parse()

	icons := appicon.Render()

	if *check {
		bad := 0
		for _, ic := range icons {
			path := filepath.Join(*dir, ic.Name)
			got, err := os.ReadFile(path)
			switch {
			case err != nil:
				fmt.Printf("missing  %s (%v)\n", path, err)
				bad++
			case string(got) != string(ic.PNG):
				fmt.Printf("stale    %s — %d bytes on disk, %d rendered\n",
					path, len(got), len(ic.PNG))
				bad++
			default:
				fmt.Printf("ok       %s\n", path)
			}
		}
		if bad > 0 {
			fmt.Fprintf(os.Stderr, "\n%d icon(s) differ. Run: go run ./internal/tools/appicon\n", bad)
			os.Exit(1)
		}
		return
	}

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, ic := range icons {
		path := filepath.Join(*dir, ic.Name)
		if err := os.WriteFile(path, ic.PNG, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("wrote %s  (%d×%d, %d bytes)\n", path, ic.Size, ic.Size, len(ic.PNG))
	}
}
