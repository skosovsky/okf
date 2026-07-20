//go:build (darwin && !ios) || (linux && !android)

package fs

import (
	"os"
	"syscall"
)

func testLockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func testUnlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
