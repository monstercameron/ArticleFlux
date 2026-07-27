//go:build !(js && wasm)

package platform

// The native half of the broadcast's sound. Silence, like every other stub here:
// a process with no browser has no AudioContext, and faking one would produce
// tests that pass against a fiction.

func Sting() {}

func Bed(src string) {}

// BlobURL and RevokeBlobURL have no native meaning: there is no document to
// hold the bytes and nothing that could play them. Empty is the honest answer,
// and the caller treats it as "no track" rather than as a failure.
func BlobURL(mime string, data []byte) string { return "" }

func RevokeBlobURL(url string) {}

func BedDuck(under bool) {}
