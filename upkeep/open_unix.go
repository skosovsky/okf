//go:build darwin || linux

package upkeep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// openBounded walks each component through directory descriptors with
// O_NOFOLLOW. A path replaced with a symlink cannot redirect the read.
func openBounded(ctx context.Context, root, name string) (*os.File, string, error) {
	if err := validPath(name); err != nil {
		return nil, "", err
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	defer func() { unix.Close(fd) }()
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			return nil, "deleted", nil
		}
		if err != nil {
			return nil, "", fmt.Errorf("unsafe path component %q: %w", part, err)
		}
		unix.Close(fd)
		fd = next
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	last := parts[len(parts)-1]
	var stat unix.Stat_t
	if err := unix.Fstatat(fd, last, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil, "deleted", nil
		}
		return nil, "", err
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		buf := make([]byte, 4096)
		n, err := unix.Readlinkat(fd, last, buf)
		if err != nil {
			return nil, "", err
		}
		if n == len(buf) {
			return nil, "", errors.New("symlink target too long")
		}
		return nil, "symlink:" + string(buf[:n]), nil
	}
	fileFD, err := unix.Openat(fd, last, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, "deleted", nil
	}
	if err != nil {
		return nil, "", err
	}
	f := os.NewFile(uintptr(fileFD), name)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, "", fmt.Errorf("unsupported git path type: %q", name)
	}
	return f, "", nil
}

func repositoryDirectoryIdentity(gitDir string) (string, error) {
	var stat unix.Stat_t
	if err := unix.Stat(gitDir, &stat); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
