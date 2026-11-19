//go:build !windows

// Command articleflux-desktop is the Windows desktop app. GoWebComponents'
// desktop support is Windows-first (its CHANGELOG: "Windows only is targeted"),
// so on any other system this says so rather than half-working; run
// `articleflux serve -dev` and open the printed address instead.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "articleflux-desktop runs on Windows only; use `articleflux serve -dev` elsewhere")
	os.Exit(2)
}
