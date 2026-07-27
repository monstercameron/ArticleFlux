// Package articlefluxv1 is the generated wire contract. Do not edit it by hand.
//
// Every other file in this directory ends in `.pb.go` and is produced by
// `buf generate` from the `.proto` files in /proto/articleflux/v1. Editing one
// works until the next person runs the generator, at which point the change
// disappears without a conflict, a warning, or any trace of having existed. The
// source of truth is the .proto; change that and run:
//
//	./scripts/make.ps1 gen     (or: make gen)
//
// # Why this file is hand-written when the rest is not
//
// Because the generator does not emit a package comment, so `go doc
// ./internal/pb/articleflux/v1` — which is where a reader lands the first time
// they follow a type like `*pb.Item` — printed nothing at all. The one place in
// the tree where a newcomer most needs to be told "this is generated, and here
// is what generates it" was the one place saying nothing.
//
// It is safe to keep here: `buf generate` writes `*.pb.go` and leaves
// everything else alone.
//
// # What lives here, and what deliberately does not
//
// These types are the CONTRACT between the Go server and the wasm client, and
// nothing more. They carry no behaviour: no validation, no defaulting, no
// business rules. Those belong in internal/reader (the service layer) and
// internal/store (the repository), so that the REST sync API and the offline
// pack channel cannot quietly acquire different rules from the tunnel — which
// is the whole reason the service layer exists.
//
// The wire contract is additive-only within v1. A field may be added; a field
// may not change meaning, change type, or be renumbered, because an old client
// and a new server have to keep understanding each other across a deploy the
// reader did not ask for.
package articlefluxv1
