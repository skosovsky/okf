//go:build darwin || linux

package fs

// This file is deliberately the only mutation backend.  os.Root prevents an
// escape, but it may still follow a symlink which is swapped after Lstat.  A
// directory descriptor is a capability for the directory we inspected; all
// writes below are therefore relative to that capability.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/skosovsky/okf/store"
	"golang.org/x/sys/unix"
)

// openRootAfterReadCapability is an internal test seam between pinning the
// mutation inode and opening the pathname-based read capability.
var openRootAfterReadCapability func()

// openRootCapabilities walks every requested absolute path component from the
// filesystem root with O_NOFOLLOW. The pinned descriptor is acquired before
// os.OpenRoot, whose pathname traversal is then verified against that inode.
// Thus a Store never combines reads from A with writes to B.
func openRootCapabilities(name string) (*os.Root, *os.File, error) {
	name = normalizeDarwinVolumeAlias(name)
	dir, err := openPinnedRoot(name)
	if err != nil {
		return nil, nil, err
	}
	closeDir := func(err error) (*os.Root, *os.File, error) {
		return nil, nil, errors.Join(fmt.Errorf("fs store: root capabilities changed: %w", store.ErrStorageCorrupt), err, dir.Close())
	}
	if openRootAfterReadCapability != nil {
		openRootAfterReadCapability()
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		return closeDir(err)
	}
	closeBoth := func(err error) (*os.Root, *os.File, error) {
		return nil, nil, errors.Join(fmt.Errorf("fs store: root capabilities changed: %w", store.ErrStorageCorrupt), err, root.Close(), dir.Close())
	}
	readDir, err := openRootReadNoFollow(root, ".", true)
	if err != nil {
		return closeBoth(err)
	}
	readInfo, err := readDir.Stat()
	closeErr := readDir.Close()
	if err != nil {
		return closeBoth(err)
	}
	if closeErr != nil {
		return closeBoth(closeErr)
	}
	writeInfo, err := dir.Stat()
	if err != nil {
		return closeBoth(err)
	}
	if !os.SameFile(readInfo, writeInfo) {
		return closeBoth(errors.New("read and mutation roots are different inodes"))
	}
	return root, dir, nil
}

func openPinnedRoot(name string) (*os.File, error) {
	if !filepath.IsAbs(name) {
		return nil, fmt.Errorf("fs store: root is not absolute: %q", name)
	}
	fd, err := unix.Open(string(os.PathSeparator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(filepath.Clean(name), string(os.PathSeparator)), string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return nil, errors.Join(noFollowErr(openErr), unix.Close(fd))
		}
		if closeErr := unix.Close(fd); closeErr != nil {
			return nil, errors.Join(closeErr, unix.Close(next))
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), name), nil
}

// Darwin's /var is an OS-owned fixed alias for /private/var. It is the only
// pathname normalization allowed before the descriptor walk.
func normalizeDarwinVolumeAlias(name string) string {
	if runtime.GOOS != "darwin" || (name != "/var" && !strings.HasPrefix(name, "/var/")) {
		return name
	}
	return "/private" + name
}

func noFollowErr(err error) error {
	if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
		return fmt.Errorf("fs store: unsafe symlink path: %w", storeCorrupt(err))
	}
	return err
}

// parentFD opens every component with O_NOFOLLOW and returns the descriptor of
// the parent plus the basename.  When makeDirs is true, absent components are
// created through the already pinned parent descriptor.
func (s *Store) parentFD(name string, makeDirs bool, mode os.FileMode) (*os.File, string, error) {
	clean := path.Clean(name)
	if clean == "." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return nil, "", fmt.Errorf("fs store: unsafe path %q", name)
	}
	parts := strings.Split(clean, "/")
	current, err := unix.Dup(int(s.dirFD.Fd()))
	if err != nil {
		return nil, "", err
	}
	d := os.NewFile(uintptr(current), "okf-root")
	for _, component := range parts[:len(parts)-1] {
		fd, openErr := unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) && makeDirs {
			if err := unix.Mkdirat(current, component, uint32(mode.Perm())); err != nil && !errors.Is(err, unix.EEXIST) {
				return nil, "", errors.Join(noFollowErr(err), d.Close())
			}
			fd, openErr = unix.Openat(current, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if openErr != nil {
			return nil, "", errors.Join(noFollowErr(openErr), d.Close())
		}
		if closeErr := d.Close(); closeErr != nil {
			return nil, "", errors.Join(closeErr, unix.Close(fd))
		}
		current = fd
		d = os.NewFile(uintptr(current), component)
	}
	return d, parts[len(parts)-1], nil
}

func (s *Store) fdOpen(name string, flags int, mode os.FileMode) (*os.File, error) {
	d, base, err := s.parentFD(name, flags&os.O_CREATE != 0, 0o755)
	if err != nil {
		return nil, err
	}
	if flags&os.O_CREATE != 0 {
		if err := s.runDescriptorBarrier("create"); err != nil {
			return nil, errors.Join(err, d.Close())
		}
		if err := s.verifyParent(name, d); err != nil {
			return nil, errors.Join(err, d.Close())
		}
	}
	// Non-blocking is harmless for regular files and prevents a malicious FIFO
	// or device swap from stalling a read/write capability before its inode can
	// be verified.
	uflags := unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if flags&os.O_RDWR != 0 {
		uflags |= unix.O_RDWR
	} else if flags&os.O_WRONLY != 0 {
		uflags |= unix.O_WRONLY
	} else {
		uflags |= unix.O_RDONLY
	}
	if flags&os.O_CREATE != 0 {
		uflags |= unix.O_CREAT
	}
	if flags&os.O_EXCL != 0 {
		uflags |= unix.O_EXCL
	}
	if flags&os.O_TRUNC != 0 {
		uflags |= unix.O_TRUNC
	}
	fd, err := unix.Openat(int(d.Fd()), base, uflags, uint32(mode.Perm()))
	if err != nil {
		return nil, errors.Join(noFollowErr(err), d.Close())
	}
	f := os.NewFile(uintptr(fd), base)
	if err := d.Close(); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// openRootReadNoFollow is for read-only operations which still use os.Root
// rather than Store's mutation descriptor. It must never wait while opening a
// special file that replaced the path after Lstat.
func openRootReadNoFollow(root *os.Root, name string, wantDirectory bool) (*os.File, error) {
	flags := os.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if wantDirectory {
		flags |= unix.O_DIRECTORY
	}
	return root.OpenFile(name, flags, 0)
}

func (s *Store) runDescriptorBarrier(operation string) error {
	if s.descriptorBarrier != nil {
		return s.descriptorBarrier(operation)
	}
	return nil
}

// verifyParent is defence in depth for an ancestor rename occurring between
// preparation and the syscall.  The descriptor remains safe either way, but
// rejecting the changed name gives callers the explicit unsafe-path result
// instead of silently publishing under a moved directory.
func (s *Store) verifyParent(name string, expected *os.File) error {
	actual, _, err := s.parentFD(name, false, 0)
	if err != nil {
		return noFollowErr(err)
	}
	a, err := actual.Stat()
	if err != nil {
		return errors.Join(err, actual.Close())
	}
	b, err := expected.Stat()
	if err != nil {
		return errors.Join(err, actual.Close())
	}
	closeErr := actual.Close()
	if !os.SameFile(a, b) {
		return errors.Join(fmt.Errorf("fs store: unsafe changed parent: %w", store.ErrStorageCorrupt), closeErr)
	}
	return closeErr
}

func (s *Store) fdLstat(name string) (fs.FileInfo, error) {
	d, base, err := s.parentFD(name, false, 0)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(int(d.Fd()), base, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, errors.Join(err, d.Close())
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil, errors.Join(noFollowErr(unix.ELOOP), d.Close())
	}
	// We only need the mode in the mutation path; opening the target through
	// O_NOFOLLOW makes this a conservative FileInfo implementation unnecessary.
	f, err := s.fdOpen(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}
	info, statErr := f.Stat()
	return info, errors.Join(statErr, f.Close(), d.Close())
}

func (s *Store) fdRemove(name string) error {
	d, base, err := s.parentFD(name, false, 0)
	if err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("remove"); err != nil {
		return errors.Join(err, d.Close())
	}
	if err := s.verifyParent(name, d); err != nil {
		return errors.Join(err, d.Close())
	}
	return errors.Join(noFollowErr(unix.Unlinkat(int(d.Fd()), base, 0)), d.Close())
}

func (s *Store) fdRemoveDir(name string) error {
	d, base, err := s.parentFD(name, false, 0)
	if err != nil {
		return err
	}
	if err := s.runDescriptorBarrier("remove_dir"); err != nil {
		return errors.Join(err, d.Close())
	}
	if err := s.verifyParent(name, d); err != nil {
		return errors.Join(err, d.Close())
	}
	return errors.Join(noFollowErr(unix.Unlinkat(int(d.Fd()), base, unix.AT_REMOVEDIR)), d.Close())
}

func (s *Store) fdRename(from, to string) error {
	fd, fb, err := s.parentFD(from, false, 0)
	if err != nil {
		return err
	}
	td, tb, err := s.parentFD(to, true, 0o755)
	if err != nil {
		return errors.Join(err, fd.Close())
	}
	if err := s.runDescriptorBarrier("rename"); err != nil {
		return errors.Join(err, td.Close(), fd.Close())
	}
	if err := s.verifyParent(from, fd); err != nil {
		return errors.Join(err, td.Close(), fd.Close())
	}
	if err := s.verifyParent(to, td); err != nil {
		return errors.Join(err, td.Close(), fd.Close())
	}
	return errors.Join(noFollowErr(unix.Renameat(int(fd.Fd()), fb, int(td.Fd()), tb)), td.Close(), fd.Close())
}

func (s *Store) fdSyncDir(name string) error {
	if name == "." || name == "" {
		return s.dirFD.Sync()
	}
	d, base, err := s.parentFD(name, false, 0)
	if err != nil {
		return err
	}
	fd, err := unix.Openat(int(d.Fd()), base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.Join(noFollowErr(err), d.Close())
	}
	f := os.NewFile(uintptr(fd), base)
	// A close can surface delayed filesystem writeback errors.  Directory sync
	// is the publication boundary, so preserve both errors instead of letting a
	// defer silently discard the close result.
	syncErr := f.Sync()
	closeErr := f.Close()
	parentCloseErr := d.Close()
	return errors.Join(syncErr, closeErr, parentCloseErr)
}

func (s *Store) fdChmod(name string, mode os.FileMode) error {
	f, err := s.fdOpen(name, os.O_RDWR, mode)
	if err != nil {
		return err
	}
	chmodErr := f.Chmod(mode)
	closeErr := f.Close()
	return errors.Join(chmodErr, closeErr)
}

func (s *Store) fdMkdirAll(name string, mode os.FileMode) ([]string, error) {
	if name == "." || name == "" {
		return nil, nil
	}
	parts := strings.Split(path.Clean(name), "/")
	fd, err := unix.Dup(int(s.dirFD.Fd()))
	if err != nil {
		return nil, err
	}
	d := os.NewFile(uintptr(fd), "okf-root")
	created := make([]string, 0, len(parts))
	for i, component := range parts {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) {
			mkdirErr := unix.Mkdirat(fd, component, uint32(mode.Perm()))
			if mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return nil, errors.Join(noFollowErr(mkdirErr), d.Close())
			}
			if mkdirErr == nil {
				created = append(created, strings.Join(parts[:i+1], "/"))
			}
			next, openErr = unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if openErr != nil {
			return nil, errors.Join(noFollowErr(openErr), d.Close())
		}
		if closeErr := d.Close(); closeErr != nil {
			return nil, errors.Join(closeErr, unix.Close(next))
		}
		fd = next
		d = os.NewFile(uintptr(fd), component)
	}
	return created, d.Close()
}

// storeCorrupt keeps unsafe descriptor traversal distinguishable to callers.
func storeCorrupt(err error) error { return errors.Join(store.ErrStorageCorrupt, err) }
