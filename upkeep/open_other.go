//go:build !darwin && !linux

package upkeep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// os.Root confines reads to the repository even when a path changes during
// traversal. Symlink components are rejected before opening the final file.
func openBounded(ctx context.Context, root, name string) (*os.File, string, error) {
	if err := validPath(name); err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	parts := strings.Split(name, "/")
	for i := range parts {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		partial := filepath.FromSlash(strings.Join(parts[:i+1], "/"))
		info, err := r.Lstat(partial)
		if errors.Is(err, os.ErrNotExist) {
			return nil, "deleted", nil
		}
		if err != nil {
			return nil, "", err
		}
		if i < len(parts)-1 {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return nil, "", fmt.Errorf("unsafe path component %q", partial)
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := r.Readlink(partial)
			if err != nil {
				return nil, "", err
			}
			return nil, "symlink:" + target, nil
		}
		if !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("unsupported git path type: %q", name)
		}
		f, err := r.Open(partial)
		if err != nil {
			return nil, "", err
		}
		opened, err := f.Stat()
		if err != nil || !opened.Mode().IsRegular() {
			f.Close()
			return nil, "", fmt.Errorf("unsafe git path %q", name)
		}
		return f, "", nil
	}
	return nil, "", errors.New("empty path")
}

func repositoryDirectoryIdentity(gitDir string) (string, error) {
	if _, err := os.Stat(gitDir); err != nil {
		return "", err
	}
	return gitDir, nil
}
