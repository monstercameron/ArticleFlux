//go:build !(js && wasm)

package platform

// The native half of the file bridge. Both are no-ops, for the reason every stub
// in this package is: there is no chooser to open and no document to download
// into, and a native fake would produce tests that pass against a fiction.
//
// PickFile never calls back, which is the same shape a cancelled chooser has in
// the browser — so a caller written correctly for one is correct on the other.

func PickFile(accept string, onFile func(name string, data []byte)) {}

func SaveFile(name, mime string, data []byte) {}
