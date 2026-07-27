// Command pagescan is the bench for the subscribe ladder (plan.md §11).
//
// It answers the two questions that come up whenever a site cannot be followed,
// and that are otherwise answered by guessing:
//
//	1. what does the model actually SEE?    — the distilled outline, verbatim
//	2. does this rule work on that page?    — the extractor, on the live HTML
//
// Both matter because they fail differently. A proposal that misses the item
// list is usually a distillation that dropped the evidence — a page whose posts
// are inside a wrapper the outline collapsed, or an outline truncated before it
// reached them. A proposal that reads plausible and extracts nothing is a
// selector problem. Without this you cannot tell those apart, and the temptation
// is to reword the prompt until something works, which is how a prompt becomes
// superstition.
//
// It fetches over the network, and it is a tool rather than a test for exactly
// that reason: nothing in CI should depend on a stranger's homepage.
//
//	go run ./internal/tools/pagescan https://example.com/blog
//	go run ./internal/tools/pagescan -rule 'article.post|h2 a|h2 a@href|time@datetime' <url>
//	go run ./internal/tools/pagescan -outline=false -feeds <url>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/monstercameron/ArticleFlux/internal/discover"
	"github.com/monstercameron/ArticleFlux/internal/scrapesel"
	"github.com/monstercameron/ArticleFlux/internal/smart"
)

func main() {
	var (
		outline = flag.Bool("outline", true, "print the distilled outline the model would receive")
		feeds   = flag.Bool("feeds", false, "climb rungs 1-3 and print any feeds found")
		rule    = flag.String("rule", "",
			"item|title|link|date|summary|image — pipe-separated attrsel selectors to try")
		layout  = flag.String("layout", "", "Go time layout for a human-readable date selector")
		private = flag.Bool("allow-private", false, "permit loopback/LAN addresses (local fixtures)")
		limit   = flag.Int("limit", 6000, "truncate the printed outline to this many bytes; 0 for all")
	)
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: pagescan [flags] <url>")
		flag.PrintDefaults()
		os.Exit(2)
	}
	url := flag.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	f := discover.New(discover.Config{AllowPrivateAddresses: *private})

	if *feeds {
		_, cands, err := f.Feeds(ctx, url)
		if err != nil {
			fmt.Fprintln(os.Stderr, "feeds:", err)
		}
		fmt.Printf("== rungs 1-3: %d candidate(s)\n", len(cands))
		for _, c := range cands {
			fmt.Printf("   %-9s %-4d items  %s\n     %s\n", c.How, c.Items, c.Title, c.URL)
		}
	}

	page, err := f.Page(ctx, url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch:", err)
		os.Exit(1)
	}
	fmt.Printf("== page\n   url    %s\n   title  %s\n   html   %d bytes\n   robots %v\n",
		page.URL, page.Title, len(page.HTML), f.Allowed(ctx, page.URL))

	out := smart.Outline(page.HTML)
	fmt.Printf("== outline: %d bytes (%.1f%% of the page)\n",
		len(out), 100*float64(len(out))/float64(max(len(page.HTML), 1)))
	if *outline {
		shown := out
		if *limit > 0 && len(shown) > *limit {
			shown = shown[:*limit] + "\n… truncated; pass -limit=0 for all of it\n"
		}
		fmt.Println(shown)
	}

	if strings.TrimSpace(*rule) == "" {
		return
	}
	parts := strings.Split(*rule, "|")
	for len(parts) < 6 {
		parts = append(parts, "")
	}
	r := scrapesel.Rule{
		IndexURL:        page.URL,
		ItemSelector:    strings.TrimSpace(parts[0]),
		TitleSelector:   strings.TrimSpace(parts[1]),
		LinkSelector:    strings.TrimSpace(parts[2]),
		DateSelector:    strings.TrimSpace(parts[3]),
		DateLayout:      *layout,
		SummarySelector: strings.TrimSpace(parts[4]),
		ImageSelector:   strings.TrimSpace(parts[5]),
	}
	c, err := scrapesel.Compile(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compile:", err)
		os.Exit(1)
	}
	res, err := scrapesel.Extract(c, []byte(page.HTML), time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "extract:", err)
		os.Exit(1)
	}
	fmt.Printf("== rule: %d matched, %d usable, %d skipped\n",
		res.Matched, len(res.Items), res.Skipped)
	for _, p := range res.Problems {
		fmt.Println("   !", p)
	}
	for i, it := range res.Items {
		when := "—"
		if !it.PublishedAt.IsZero() {
			when = it.PublishedAt.Format("2006-01-02")
		}
		fmt.Printf("   %2d  %-10s %-46.46s %s\n", i+1, when, it.Title, it.URL)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
