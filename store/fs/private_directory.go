package fs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

// ensurePrivateDirectories repairs each private path component through a
// pinned no-follow descriptor. Namespace identity is rebound after metadata
// durability, so a concurrent replacement is never chmodded or accepted.
func (s *Store) ensurePrivateDirectories(ctx context.Context, dir string) error {
	parts := strings.Split(path.Clean(dir), "/")
	for i := range parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := strings.Join(parts[:i+1], "/")
		if err := s.repairPrivateDirectory(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) repairPrivateDirectory(ctx context.Context, name string) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	expected, err := s.rootFD.Lstat(name)
	if err != nil {
		return err
	}
	if expected == nil || !expected.IsDir() || expected.Mode()&os.ModeSymlink != 0 {
		return metadataCorrupt(fmt.Errorf("fs store: unsafe private metadata directory %q", name))
	}
	f, err := openRootReadNoFollow(s.rootFD, name, true)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, f.Close())
		}
	}()
	pinned, err := f.Stat()
	if err != nil {
		return err
	}
	if pinned == nil || !pinned.IsDir() || !os.SameFile(expected, pinned) {
		return metadataCorrupt(fmt.Errorf("fs store: private metadata directory identity changed before repair: %q", name))
	}
	if err := s.runDescriptorBarrier("private_directory_after_open"); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("private_directory_after_open:" + name); err != nil {
		return err
	}

	if pinned.Mode().Perm() != 0o700 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.fail(StepPrivateDirectoryChmod); err != nil {
			return err
		}
		if err := f.Chmod(0o700); err != nil {
			return err
		}
		if err := s.postFault(StepPrivateDirectoryChmod); err != nil {
			return err
		}
		if err := s.runDescriptorBarrier("private_directory_after_chmod"); err != nil {
			return err
		}
		if err := s.runDescriptorBarrier("private_directory_after_chmod:" + name); err != nil {
			return err
		}
		pinned, err = f.Stat()
		if err != nil {
			return err
		}
		if pinned == nil || !pinned.IsDir() || !os.SameFile(expected, pinned) || pinned.Mode().Perm() != 0o700 {
			return metadataCorrupt(fmt.Errorf("fs store: private metadata directory identity changed after chmod: %q", name))
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fail(StepPrivateDirectorySync); err != nil {
		return err
	}
	if err := s.syncFile(f); err != nil {
		return err
	}
	if err := s.postFault(StepPrivateDirectorySync); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("private_directory_after_sync"); err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("private_directory_after_sync:" + name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rebound, err := s.rootFD.Lstat(name)
	if err != nil {
		return err
	}
	if rebound == nil || !rebound.IsDir() || !os.SameFile(pinned, rebound) {
		return metadataCorrupt(fmt.Errorf("fs store: private metadata directory identity changed after sync: %q", name))
	}

	if err := s.fail(StepPrivateDirectoryClose); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := s.postFault(StepPrivateDirectoryClose); err != nil {
		return err
	}
	return s.syncPrivateDirectoryParentContext(ctx, name)
}

func (s *Store) syncPrivateDirectoryParent(name string) error {
	return s.syncPrivateDirectoryParentContext(context.Background(), name)
}

func (s *Store) syncPrivateDirectoryParentContext(ctx context.Context, name string) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.fail(StepPrivateDirectoryParentSync); err != nil {
		return err
	}
	parent := path.Dir(name)
	expected, err := s.rootFD.Lstat(parent)
	if err != nil {
		return err
	}
	if expected == nil || !expected.IsDir() || expected.Mode()&os.ModeSymlink != 0 {
		return metadataCorrupt(fmt.Errorf("fs store: unsafe private metadata parent %q", parent))
	}
	f, err := openRootReadNoFollow(s.rootFD, parent, true)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, f.Close()) }()
	pinned, err := f.Stat()
	if err != nil {
		return err
	}
	if pinned == nil || !pinned.IsDir() || !os.SameFile(expected, pinned) {
		return metadataCorrupt(fmt.Errorf("fs store: private metadata parent identity changed: %q", parent))
	}
	if s.config.DirectorySync != nil {
		if err := s.config.DirectorySync(parent); err != nil {
			return err
		}
	}
	if err := s.syncFile(f); err != nil {
		return err
	}
	rebound, err := s.rootFD.Lstat(parent)
	if err != nil {
		return err
	}
	if rebound == nil || !rebound.IsDir() || !os.SameFile(pinned, rebound) {
		return metadataCorrupt(fmt.Errorf("fs store: private metadata parent identity changed after sync: %q", parent))
	}
	return s.postFault(StepPrivateDirectoryParentSync)
}
