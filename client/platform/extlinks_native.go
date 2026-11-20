//go:build !(js && wasm)

package platform

// OnExternalLinks (extlinks_wasm.go): no DOM, nothing to listen on.
func OnExternalLinks() Listener { return Listener{} }
