//go:build (darwin && !ios) || (linux && !android)

package fs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// fdRenameOwnedNoReplace verifies the named source after the adversarial seam
// and consumes it with the platform's atomic NOREPLACE primitive. The returned
// identity is the descriptor-pinned source inode, never a post-rename lookup.
func (s *Store) fdRenameOwnedNoReplace(from, to string, expected fs.FileInfo, wantDir bool) (fs.FileInfo, error) {
	return s.fdRenameOwnedNoReplaceMode(from, to, expected, wantDir, true)
}

func (s *Store) fdRenameOwnedNoReplaceMode(from, to string, expected fs.FileInfo, wantDir, observe bool) (fs.FileInfo, error) {
	fromDir, fromBase, err := s.parentFD(from, false, 0)
	if err != nil {
		return nil, err
	}
	toDir, toBase, err := s.parentFD(to, true, 0o700)
	if err != nil {
		return nil, errors.Join(err, fromDir.Close())
	}
	closeParents := func(err error) error { return errors.Join(err, toDir.Close(), fromDir.Close()) }
	if observe {
		if err := s.runDescriptorBarrier("claim_before_rename"); err != nil {
			return nil, closeParents(err)
		}
	}
	if err := s.verifyParent(from, fromDir); err != nil {
		return nil, closeParents(err)
	}
	if err := s.verifyParent(to, toDir); err != nil {
		return nil, closeParents(err)
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if wantDir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(fromDir.Fd()), fromBase, flags, 0)
	if err != nil {
		return nil, closeParents(noFollowErr(err))
	}
	source := os.NewFile(uintptr(fd), fromBase)
	identity, statErr := source.Stat()
	if statErr != nil || expected == nil || identity.IsDir() != wantDir || !os.SameFile(expected, identity) {
		return nil, errors.Join(errMutationTargetChanged(from), statErr, source.Close(), closeParents(nil))
	}
	if observe {
		if err := s.runDescriptorBarrier("claim_after_source_stat"); err != nil {
			return nil, errors.Join(err, source.Close(), closeParents(nil))
		}
	}
	if err := renameNoReplaceAt(int(fromDir.Fd()), fromBase, int(toDir.Fd()), toBase); err != nil {
		return nil, errors.Join(noFollowErr(err), source.Close(), closeParents(nil))
	}
	if observe {
		if err := s.runDescriptorBarrier("claim_after_rename"); err != nil {
			return identity, errors.Join(err, source.Close(), closeParents(nil))
		}
	}
	targetFD, targetOpenErr := unix.Openat(int(toDir.Fd()), toBase, flags, 0)
	if targetOpenErr != nil {
		return identity, errors.Join(noFollowErr(targetOpenErr), source.Close(), closeParents(nil))
	}
	target := os.NewFile(uintptr(targetFD), toBase)
	targetIdentity, targetStatErr := target.Stat()
	if targetStatErr != nil || targetIdentity == nil || targetIdentity.IsDir() != wantDir || !os.SameFile(identity, targetIdentity) {
		restoreErr := renameNoReplaceAt(int(toDir.Fd()), toBase, int(fromDir.Fd()), fromBase)
		return identity, errors.Join(errArtifactClaimConflict, targetStatErr, noFollowErr(restoreErr), target.Close(), source.Close(), closeParents(nil))
	}
	return identity, errors.Join(target.Close(), source.Close(), closeParents(nil))
}

func (s *Store) fdLinkOwnedNoReplace(from, to string, expected fs.FileInfo) error {
	return s.fdLinkOwnedNoReplaceMode(from, to, expected, true)
}

func (s *Store) fdLinkOwnedNoReplaceMode(from, to string, expected fs.FileInfo, observe bool) error {
	fromDir, fromBase, err := s.parentFD(from, false, 0)
	if err != nil {
		return err
	}
	toDir, toBase, err := s.parentFD(to, true, 0o700)
	if err != nil {
		return errors.Join(err, fromDir.Close())
	}
	closeParents := func(err error) error { return errors.Join(err, toDir.Close(), fromDir.Close()) }
	if observe {
		if err := s.runDescriptorBarrier("witness_before_link"); err != nil {
			return closeParents(err)
		}
	}
	fd, err := unix.Openat(int(fromDir.Fd()), fromBase, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return closeParents(noFollowErr(err))
	}
	source := os.NewFile(uintptr(fd), fromBase)
	identity, statErr := source.Stat()
	if statErr != nil || expected == nil || !identity.Mode().IsRegular() || !os.SameFile(expected, identity) {
		return errors.Join(errMutationTargetChanged(from), statErr, source.Close(), closeParents(nil))
	}
	linkErr := unix.Linkat(int(fromDir.Fd()), fromBase, int(toDir.Fd()), toBase, 0)
	return errors.Join(noFollowErr(linkErr), source.Close(), closeParents(nil))
}

func fileIdentityKey(info fs.FileInfo) (string, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%#o", uint64(stat.Dev), uint64(stat.Ino), uint32(stat.Mode)), true
}
