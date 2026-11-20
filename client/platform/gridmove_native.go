//go:build !(js && wasm)

package platform

// GridMove (gridmove_wasm.go): no layout to read.
func GridMove(containerSelector, itemSelector string, dx, dy int) bool { return false }
