//go:build unix

package bundle

import (
	"os"

	"golang.org/x/sys/unix"
)

func openFileNoFollow(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
}
