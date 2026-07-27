// Package platform is the only package in ArticleFlux permitted to import
// syscall/js (A26).
//
// # Why the boundary exists
//
// Everything above this package is ordinary Go: it can be read without knowing
// anything about the browser, and — the part that pays for itself — it can be
// COMPILED AND TESTED natively, without a wasm toolchain or a headless Chrome.
// `internal/tools/guards` enforces the rule mechanically, because a boundary
// that depends on everyone remembering it is a boundary that lasts until the
// first hurried afternoon.
//
// # Why this file exists and holds nothing
//
// The package is built twice, from two disjoint sets of files: `*_wasm.go`
// behind `//go:build js && wasm`, and `*_native.go` behind its negation. Every
// symbol is declared once in each, so the callers above never learn which one
// they got.
//
// That split had one casualty. The package documentation lived in
// platform_wasm.go, which the native build excludes — so `go doc
// ./client/platform` was a full description under one build and completely
// empty under the other, and the empty one is the build a person runs when they
// are trying to understand the code without setting up a wasm environment
// first. Documentation that is invisible from the cheaper of two builds is
// documentation aimed away from the newcomer.
//
// So the package doc lives here, in a file with no build constraint, and is
// therefore the same under both. Nothing else belongs in this file: a
// declaration here would have to be satisfied by both implementations, which is
// exactly the coupling the two-file split exists to avoid.
//
// # What the native half actually does
//
// It answers honestly rather than pretending. There is no browser, so there is
// no local storage, no network-status events and nothing to scroll — and the
// native implementations say so by returning the zero value, with a comment on
// each explaining why "absent" beats a plausible-looking fake. A native test of
// the login gate that passed against an in-memory storage map would be a test
// of the fake.
package platform
