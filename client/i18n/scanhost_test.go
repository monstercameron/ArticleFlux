package i18n

import (
	"os"
	"testing"
)

// The source-scanning guards in this package read directories, and one of the
// two platforms they run on cannot.
//
// # Why this exists
//
// keycoverage_test.go and srvkeys_test.go parse the Go source of client/view and
// the server packages to prove that every key referenced in code exists in the
// catalogue, and that no catalogue key is orphaned. They are structural guards,
// and they are the kind this project relies on.
//
// They are also `GOOS=js` tests, because client/view is js+wasm. Under the
// node-hosted wasm runtime on WINDOWS, os.ReadDir fails outright —
// "syscall.Open: O_DIRECTORY is not supported on Windows" — so all four of them
// failed on the only machine this project is developed on, every single run,
// while passing in CI on Linux.
//
// A guard that is permanently red where the developer works is not a guard. It
// is the same failure this repository names elsewhere about a stale error on a
// screen: somebody learns to ignore it, and then it is furniture. Worse here,
// because `go test ./client/...` is the command AGENTS.md's verify-before-done
// rule points at, and four guaranteed failures make its output useless.
//
// # Why a capability probe rather than a platform check or a string match
//
// The error does NOT wrap errors.ErrUnsupported — measured, it does not — so
// matching it would mean matching its message, which is a different fragility
// for the same problem. Checking runtime.GOOS would encode today's list of
// hosts that cannot do this.
//
// Reading the package's own directory answers the actual question. If THAT
// cannot be listed, no directory can be, and no source scan is possible here. If
// it can, then a failure to read the target directory is a real failure and is
// still fatal — which is what keeps this from hiding a broken path in CI.
func requireDirScan(t *testing.T) {
	t.Helper()
	if _, err := os.ReadDir("."); err != nil {
		t.Skipf("this host cannot list directories (%v), so the source scan these "+
			"guards depend on cannot run — they are enforced in CI, on Linux, where "+
			"it can", err)
	}
}
