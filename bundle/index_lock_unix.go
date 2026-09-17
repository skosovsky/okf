//go:build (darwin && !ios) || (linux && !android)

package bundle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

func physicalPublicationRootIdentity(root *os.Root) (string, error) {
	file, info, err := openPinnedPublicationRootDirectory(root)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return unixPublicationRootIdentity(info)
}

func tryAcquireCrossProcessPublicationLock(
	root *os.Root,
	identity string,
) (release func(), err error) {
	file, info, err := openPinnedPublicationRootDirectory(root)
	if err != nil {
		return nil, err
	}
	currentIdentity, err := unixPublicationRootIdentity(info)
	if err != nil {
		file.Close()
		return nil, err
	}
	if currentIdentity != identity {
		file.Close()
		return nil, fmt.Errorf(
			"physical bundle root changed while acquiring publication lock: %q != %q",
			currentIdentity,
			identity,
		)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errors.Join(
				fmt.Errorf(
					"%w: physical bundle root %q is owned by another process",
					ErrPublicationOwnershipConflict,
					identity,
				),
				closeErr,
			)
		}
		return nil, errors.Join(
			fmt.Errorf("lock physical bundle root %q: %w", identity, err),
			closeErr,
		)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
			_ = file.Close()
		})
	}, nil
}

func openPinnedPublicationRootDirectory(root *os.Root) (*os.File, os.FileInfo, error) {
	before, err := root.Lstat(".")
	if err != nil {
		return nil, nil, fmt.Errorf("inspect physical bundle root: %w", err)
	}
	if !before.IsDir() || before.Mode()&fs.ModeSymlink != 0 {
		return nil, nil, errors.New("physical bundle root is not a no-follow directory")
	}
	file, err := root.Open(".")
	if err != nil {
		return nil, nil, fmt.Errorf("open physical bundle root directory: %w", err)
	}
	after, err := file.Stat()
	if err != nil ||
		!after.IsDir() ||
		after.Mode()&fs.ModeSymlink != 0 ||
		!os.SameFile(before, after) {
		file.Close()
		if err == nil {
			err = errors.New("physical bundle root changed while opening directory lock")
		}
		return nil, nil, err
	}
	return file, after, nil
}

func unixPublicationRootIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("physical bundle root identity is unavailable")
	}
	return fmt.Sprintf("unix:%d:%d", stat.Dev, stat.Ino), nil
}
