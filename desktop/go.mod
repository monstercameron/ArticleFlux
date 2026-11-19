// The Windows desktop shell (plan.md A45 / §4a, docs/DESKTOP.md).
//
// A module of its own, beside the server rather than inside it, for the reason
// GoWebComponents ships its own Wails adapter as a separate module: the native
// window toolkit is a large dependency graph that the server, the wasm client
// and the CI ratchets have no use for. Nothing in the root module imports this
// one, so `go build ./...`, `go test ./...`, govulncheck and the wasm size
// ratchet at the root are exactly what they were before it existed.
module github.com/monstercameron/ArticleFlux/desktop

go 1.27.0

toolchain go1.27.3

require (
	github.com/monstercameron/ArticleFlux v0.0.0
	github.com/wailsapp/wails/v3 v3.0.0-beta.17
	golang.org/x/sys v0.48.0
)

require (
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/chromedp/cdproto v0.0.0-20260714215040-dc233986426f // indirect
	github.com/chromedp/chromedp v0.16.0 // indirect
	github.com/chromedp/sysutil v1.1.0 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-json-experiment/json v0.0.0-20260623181947-01eb4420fa68 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0 // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/gobwas/ws v1.4.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.29.0 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mmcdole/gofeed v1.4.0 // indirect
	github.com/mmcdole/goxpp/v2 v2.0.0 // indirect
	github.com/monstercameron/GoGRPCBridge v1.1.1 // indirect
	github.com/monstercameron/GoWebComponents/v7 v7.0.8 // indirect
	github.com/monstercameron/schemaflux/v2 v2.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/ncruces/go-sqlite3 v0.35.2 // indirect
	github.com/ncruces/go-sqlite3-wasm/v3 v3.2.35303 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/prometheus/client_golang v1.24.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.70.1 // indirect
	github.com/prometheus/otlptranslator v1.0.0 // indirect
	github.com/prometheus/procfs v0.21.1 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/bridges/otelslog v0.20.0 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.21.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.44.0 // indirect
	go.opentelemetry.io/otel/exporters/prometheus v0.66.0 // indirect
	go.opentelemetry.io/otel/log v0.21.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/sdk v1.45.0 // indirect
	go.opentelemetry.io/otel/sdk/log v0.21.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260803160001-6ac0973c030d // indirect
	google.golang.org/grpc v1.83.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// The server, from this checkout. A nested module may import its parent's
// internal/ packages because the import path is still under
// github.com/monstercameron/ArticleFlux — the replace is what makes the parent
// resolve to the tree beside it rather than to a published version that does not
// exist.
replace github.com/monstercameron/ArticleFlux => ../

// Wails v3.0.0-beta.17 plus the Windows menu patch GoWebComponents v7.0.8 pins
// (third_party/wails at 0965e9db in the GWC repository; CHANGELOG "v6.1.0" →
// "Fixed": menu resource ownership, radio reselection, context dispatch). The
// same revision GWC's own desktop adapter is tested against, fetched through the
// module proxy rather than a sibling checkout, so this builds from this
// repository alone.
replace github.com/wailsapp/wails/v3 => github.com/monstercameron/wails/v3 v3.0.0-beta.17.0.20260908235539-0965e9db574e
