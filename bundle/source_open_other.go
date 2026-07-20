//go:build !unix

package bundle

import "os"

// Non-Unix platforms do not expose Unix special files or O_NOFOLLOW. The
// Lstat/Stat identity checks in openRegularFile still reject links and every
// non-regular file before it is read.
func openFileNoFollow(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY, 0)
}
