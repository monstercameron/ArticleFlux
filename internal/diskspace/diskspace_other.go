//go:build !unix && !windows

package diskspace

// free has no implementation here.
//
// Reported rather than guessed. A stub returning a large number would make the
// readiness check pass on a platform where it has measured nothing, which is
// the one outcome worse than not answering — and `App.diskReady` treats
// ErrUnsupported as "no information" and falls back to the write probe, which
// needs no platform support at all.
func free(string) (uint64, error) { return 0, ErrUnsupported }
