//go:build (!darwin && !linux) || ios || android

package fs

// This file keeps package fs compile-safe on targets where the descriptor-
// relative durability backend is unavailable. OpenContext rejects the target
// before any of these defensive compile stubs can be reached.

import (
	"io/fs"
	"os"
	"runtime"
)

func platformOpenError() error {
	return &UnsupportedPlatformError{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func lockExclusive(*os.File) error { return platformOpenError() }

func unlockFile(*os.File) error { return platformOpenError() }

func rootLockRetryable(error) bool { return false }

func openRootLockDescriptor(*os.File) (*os.File, error) { return nil, platformOpenError() }

func openRootCapabilities(string) (*os.Root, *os.File, error) {
	return nil, nil, platformOpenError()
}

func openRootReadNoFollow(*os.Root, string, bool) (*os.File, error) {
	return nil, platformOpenError()
}

func (s *Store) runDescriptorBarrier(operation string) error {
	if s.descriptorBarrier != nil {
		return s.descriptorBarrier(operation)
	}
	return nil
}

func (s *Store) fdOpen(string, int, os.FileMode) (*os.File, error) {
	return nil, platformOpenError()
}

func (s *Store) fdLstat(string) (fs.FileInfo, error) {
	return nil, platformOpenError()
}

func (s *Store) fdRemoveClaimOwned(claimsZonePath, fs.FileInfo, bool) (bool, error) {
	return false, platformOpenError()
}

func (s *Store) fdRemoveClaimOwnedMode(claimsZonePath, fs.FileInfo, bool, bool) (bool, error) {
	return false, platformOpenError()
}

func (s *Store) fdRenameGuarded(string, string, fs.FileInfo, mutationTargetIdentity) (fs.FileInfo, error) {
	return nil, platformOpenError()
}

func (s *Store) fdSyncDir(string) error { return platformOpenError() }

func (s *Store) fdChmod(string, os.FileMode) error { return platformOpenError() }

func (s *Store) fdMkdirAll(string, os.FileMode) ([]string, error) {
	return nil, platformOpenError()
}
