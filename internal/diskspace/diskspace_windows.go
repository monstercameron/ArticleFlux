//go:build windows

package diskspace

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// free is GetDiskFreeSpaceExW, taking the FIRST out-parameter.
//
// The call returns three numbers and only the first is the right one:
// lpFreeBytesAvailableToCaller respects a per-user disk quota, while
// lpTotalNumberOfFreeBytes does not. On a box with no quota they are equal, and
// on a box with one the second would promise room this process cannot use.
//
// Windows is not a deployment target — the droplet is Ubuntu — but it is where
// this repository is developed, and a readiness check that reported "not
// supported" on the machine somebody is testing it on would never be exercised
// before it shipped.
func free(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var availableToCaller, total, totalFree uint64
	r1, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&availableToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r1 == 0 {
		return 0, callErr
	}
	return availableToCaller, nil
}
