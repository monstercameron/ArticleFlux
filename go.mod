module github.com/monstercameron/ArticleFlux

go 1.26.5

require (
	github.com/andybalholm/brotli v1.2.0
	github.com/andybalholm/cascadia v1.3.4
	github.com/chromedp/cdproto v0.0.0-20260714215040-dc233986426f
	github.com/chromedp/chromedp v0.16.0
	github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0
	github.com/mmcdole/gofeed v1.4.0
	github.com/monstercameron/GoGRPCBridge v1.1.1
	github.com/monstercameron/GoWebComponents/v5 v5.0.0-00010101000000-000000000000
	github.com/ncruces/go-sqlite3 v0.35.2
	github.com/prometheus/client_golang v1.24.1
	go.opentelemetry.io/otel v1.45.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0
	go.opentelemetry.io/otel/exporters/prometheus v0.66.0
	go.opentelemetry.io/otel/metric v1.45.0
	go.opentelemetry.io/otel/sdk v1.45.0
	go.opentelemetry.io/otel/sdk/metric v1.45.0
	go.opentelemetry.io/otel/trace v1.45.0
	golang.org/x/crypto v0.54.0
	golang.org/x/net v0.57.0
	golang.org/x/term v0.45.0
	golang.org/x/text v0.40.0
	google.golang.org/grpc v1.83.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/joho/godotenv v1.5.1 // indirect
	github.com/sashabaranov/go-openai v1.20.4 // indirect
	go.opentelemetry.io/contrib/bridges/otelslog v0.20.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.21.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.38.0 // indirect
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.38.0 // indirect
	go.opentelemetry.io/otel/log v0.21.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.21.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/mmcdole/goxpp/v2 v2.0.0 // indirect
	github.com/monstercameron/schemaflux v0.0.0
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/ncruces/go-sqlite3-wasm/v3 v3.2.35303 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/yuin/goldmark v1.7.17 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
)

// D0: v5.0.0 exists in the GWC CHANGELOG but was never tagged or published.
// Remove this once the tag is pushed.
replace github.com/monstercameron/GoWebComponents/v5 => ../GoWebComponents

// SchemaFlux is developed in a sibling checkout and is not published yet, so
// this points at the working copy rather than at a tag.
//
// It is a `replace`, not a vendored copy, for the reason the GWC line above is
// one: the two repositories are edited together, and a copy is a fork that
// nobody remembers to update. **Replace this whole directive with an ordinary
// `go get github.com/monstercameron/schemaflux@vX.Y.Z` the moment it is
// tagged** — a replace is a build that only works on a machine with the sibling
// checkout, so CI and anybody else's clone cannot build this until it goes.
//
// The API is still moving (§6 of docs/AI_SCHEMAFLOW_MIGRATION.md records three
// gaps closing in three days), so this repo deliberately depends on the
// smallest possible part of it: the Provider seam and the middleware chain.
// See internal/llm/sfprovider.
//
// **A directory replace resolves the sibling's WORKING TREE, not its HEAD**,
// which has already bitten once: an untracked, half-written file over there
// stopped every package in this repository from compiling, with an error naming
// a file nobody here had touched. It cleared when its author saved again. If it
// happens for longer than that is worth waiting for, verify against a snapshot
// of their last commit rather than editing somebody's work in progress:
//
//	git -C ../SchemaFlow archive HEAD | tar -x -C <tmp>
//	go mod edit -replace github.com/monstercameron/schemaflux=<tmp>
//
// and put this line back afterwards. Publishing the module retires the whole
// problem, which is the other reason the paragraph above has a deadline in it.
replace github.com/monstercameron/schemaflux => ../SchemaFlow
