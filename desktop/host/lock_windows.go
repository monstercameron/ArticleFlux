//go:build windows

package host

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// acquire opens the lock file with no sharing at all.
//
// A share mode of zero is the whole mechanism: Windows refuses every other
// open of the file — by this program or any other — until the handle is
// closed, and closes it for us when the process exits. No LockFileEx range,
// no contents, nothing to clean up.
func acquire(path string) (func() error, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, // no sharing: this is the lock
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("opening the instance lock %q: %w", path, err)
	}
	f := os.NewFile(uintptr(h), path)
	return f.Close, nil
}
