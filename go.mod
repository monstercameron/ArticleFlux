module github.com/monstercameron/Tidings

go 1.26.3

require (
	github.com/mmcdole/gofeed v1.4.0
	github.com/monstercameron/GoGRPCBridge v1.1.1
	github.com/monstercameron/GoWebComponents/v5 v5.0.0-00010101000000-000000000000
	github.com/ncruces/go-sqlite3 v0.35.0
	golang.org/x/crypto v0.54.0
	golang.org/x/net v0.56.0
	golang.org/x/text v0.40.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/fxamacker/cbor/v2 v2.9.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/mmcdole/goxpp/v2 v2.0.0 // indirect
	github.com/ncruces/go-sqlite3-wasm/v3 v3.1.35302 // indirect
	github.com/ncruces/julianday v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	github.com/yuin/goldmark v1.7.13 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
)

// D0: v5.0.0 exists in the GWC CHANGELOG but was never tagged or published.
// Remove this once the tag is pushed.
replace github.com/monstercameron/GoWebComponents/v5 => ../GoWebComponents
