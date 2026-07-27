// A separate module on purpose.
//
// The D7 bake-off compares three readability ports, and two of the three lose.
// Keeping them in the main module's go.mod would drag wazero, a WASM-compiled
// re2, zerolog and a date-parsing library into the dependency graph of a server
// that uses none of them — permanently, to preserve a comparison that runs about
// once a year.
//
// A nested module is invisible to `go build ./...` and `go test ./...` in the
// parent, so the evidence stays runnable without the parent paying for it:
//
//	cd internal/extract/bakeoff && go test -run TestBakeoff -v
module github.com/monstercameron/ArticleFlux/internal/extract/bakeoff

go 1.26.3

require (
	github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0
	github.com/markusmobius/go-domdistiller v0.0.0-20240926050704-25b8d046ffb4
	github.com/markusmobius/go-trafilatura v1.12.2
	golang.org/x/net v0.57.0
)

require (
	github.com/RadhiFadlillah/whatlanggo v0.0.0-20240916001553-aac1f0f737fc // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/elliotchance/pie/v2 v2.9.0 // indirect
	github.com/forPelevin/gomoji v1.2.0 // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/hablullah/go-hijri v1.0.2 // indirect
	github.com/hablullah/go-juliandays v1.0.0 // indirect
	github.com/jalaali/go-jalaali v0.0.0-20210801064154-80525e88d958 // indirect
	github.com/markusmobius/go-dateparser v1.2.3 // indirect
	github.com/markusmobius/go-htmldate v1.9.1 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rs/zerolog v1.33.0 // indirect
	github.com/tetratelabs/wazero v1.8.1 // indirect
	github.com/wasilibs/go-re2 v1.7.0 // indirect
	github.com/wasilibs/wazero-helpers v0.0.0-20240620070341-3dff1577cd52 // indirect
	github.com/yosssi/gohtml v0.0.0-20201013000340-ee4748c638f4 // indirect
	golang.org/x/exp v0.0.0-20241009180824-f66d83c29e7c // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
)
