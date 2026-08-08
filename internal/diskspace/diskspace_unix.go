//go:build unix

package diskspace

import "syscall"

// free is Statfs, reading Bavail rather than Bfree.
//
// Bfree counts blocks that are unused; Bavail counts blocks an unprivileged
// process may use, which is smaller by the filesystem's reserved percentage.
// The articleflux unit runs as its own unprivileged user, so Bavail is the only
// one of the two that describes what it can write.
//
// The conversions are not decoration: Bsize is int64 on Linux and uint32 on
// Darwin, and Bavail is uint64 on both. Writing them out keeps this compiling
// on a Mac, which is where somebody will run the tests.
func free(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
