//go:build !unix

package mcpserver

import "os"

// Non-Unix platforms do not expose O_NOFOLLOW/O_NONBLOCK. The surrounding
// Lstat/Stat identity and regular-file checks still reject unsafe replacements.
func openPinnedConceptFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY, 0)
}
