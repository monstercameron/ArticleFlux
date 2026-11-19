//go:build unix

package host

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// acquire takes an advisory flock. The shell only ships for Windows; this
// exists so the host package — which has no Windows-only logic of its own —
// still builds and tests on the CI's Linux runners if anybody points one at it.
func acquire(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the instance lock %q: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("locking %q: %w", path, err)
	}
	return f.Close, nil
}
