//go:build unix

package mcpserver

import (
	"os"

	"golang.org/x/sys/unix"
)

// openPinnedConceptFile opens the final component relative to the already
// pinned directory descriptor. O_NONBLOCK makes a FIFO/device swap fail
// without waiting; O_NOFOLLOW makes a symlink swap fail in the kernel.
func openPinnedConceptFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}
