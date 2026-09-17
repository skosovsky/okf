package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skosovsky/okf/store"
)

func TestOpenCloseLeavesAbsentPrivateNamespaceUntouched(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	var hooks int
	cfg := Config{
		Fault:         func(Step) error { hooks++; return nil },
		PostFault:     func(Step) error { hooks++; return nil },
		DirectorySync: func(string) error { hooks++; return nil },
	}

	// Act.
	s, openErr := OpenContext(context.Background(), root, cfg)
	closeErr := error(nil)
	if s != nil {
		closeErr = s.Close()
	}
	entries, readErr := os.ReadDir(root)

	// Assert.
	if openErr != nil || closeErr != nil || readErr != nil || len(entries) != 0 || hooks != 0 {
		t.Fatalf("Open+Close side effects: open=%v close=%v read=%v entries=%v hooks=%d", openErr, closeErr, readErr, entries, hooks)
	}
}

func TestOpenIsSideEffectFreeAndPrivateInitializationIsLazy(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "visible.md", adversarialDocument("lazy"))
	var hooks int
	var capabilityMkdirs int
	cfg := Config{
		Fault: func(step Step) error {
			hooks++
			if step == StepCapabilityMkdir {
				capabilityMkdirs++
			}
			return nil
		},
		PostFault:     func(Step) error { hooks++; return nil },
		DirectorySync: func(string) error { hooks++; return nil },
	}

	// Act.
	s, err := OpenContext(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	hooksAtOpen := hooks
	entriesAtOpen, entriesErr := os.ReadDir(root)
	_, privateAtOpen := os.Stat(filepath.Join(root, internalDirectory))
	_, snapshotErr := s.Snapshot(context.Background())
	capabilityMkdirsAfterFirst := capabilityMkdirs
	_, secondSnapshotErr := s.Snapshot(context.Background())
	_, privateAfterSnapshot := os.Stat(filepath.Join(root, internalDirectory))
	_, leaseErr := os.Stat(filepath.Join(root, internalDirectory, "lease"))

	// Assert.
	if entriesErr != nil || len(entriesAtOpen) != 1 || entriesAtOpen[0].Name() != "visible.md" || !errors.Is(privateAtOpen, os.ErrNotExist) || hooksAtOpen != 0 || hooks == 0 || snapshotErr != nil || secondSnapshotErr != nil || privateAfterSnapshot != nil || capabilityMkdirsAfterFirst == 0 || capabilityMkdirs != capabilityMkdirsAfterFirst {
		t.Fatalf("lazy open: entries=%v/%v private-at-open=%v hooks=%d/%d capability-init=%d/%d snapshots=%v/%v private-after=%v", entriesAtOpen, entriesErr, privateAtOpen, hooksAtOpen, hooks, capabilityMkdirsAfterFirst, capabilityMkdirs, snapshotErr, secondSnapshotErr, privateAfterSnapshot)
	}
	if !errors.Is(leaseErr, os.ErrNotExist) {
		t.Fatalf("lazy initialization created lease artifact: %v", leaseErr)
	}
}

func TestRootLockCancellationAndIndependentStores(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	first, err := Open(root, Config{LeaseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(root, Config{LeaseTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	lock, err := openRootLockDescriptor(first.dirFD)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockExclusive(lock); err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	pinnedBefore, err := first.dirFD.Stat()
	if err != nil {
		t.Fatal(err)
	}
	lockedBefore, err := lock.Stat()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlockFile(lock); _ = lock.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	_, snapshotErr := second.Snapshot(ctx)
	opened, openErr := OpenContext(ctx, root, Config{})
	lockedAfter, identityErr := lock.Stat()

	// Assert. Open cancellation is checked before pinning; only observations
	// contend for the root-inode lock.
	if opened != nil || !errors.Is(openErr, context.Canceled) || !errors.Is(snapshotErr, context.Canceled) || identityErr != nil || !os.SameFile(pinnedBefore, lockedBefore) || !os.SameFile(pinnedBefore, lockedAfter) {
		t.Fatalf("root lock cancellation: store=%v open=%v snapshot=%v identity=%v", opened, openErr, snapshotErr, identityErr)
	}
}

func TestRootLockTimeoutDoesNotRetainLock(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	first, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(root, Config{LeaseTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	lock, err := openRootLockDescriptor(first.dirFD)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockExclusive(lock); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, timeoutErr := second.Snapshot(context.Background())
	if err := unlockFile(lock); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	_, retryErr := second.Snapshot(context.Background())

	// Assert.
	if !errors.Is(timeoutErr, context.DeadlineExceeded) || retryErr != nil {
		t.Fatalf("root lock timeout=%v retry=%v", timeoutErr, retryErr)
	}
}

func TestRootLockPostAcquireIdentityFailurePrecedesPrivateMutation(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := errors.New("post-lock identity mismatch")
	checks := 0
	s.rootLockIdentity = func(root, lock *os.File) error {
		checks++
		if root == nil || lock == nil {
			t.Fatal("post-lock verifier received nil descriptor")
		}
		return errors.Join(store.ErrStorageCorrupt, want)
	}

	// Act.
	_, snapshotErr := s.Snapshot(context.Background())
	_, privateErr := os.Stat(filepath.Join(root, internalDirectory))
	s.rootLockIdentity = verifyRootLockIdentity
	_, retryErr := s.Snapshot(context.Background())

	// Assert. The failed verifier owns the already-acquired descriptor; retry
	// succeeding proves the failure path unlocked and closed it.
	if checks != 1 || !errors.Is(snapshotErr, want) || !errors.Is(snapshotErr, store.ErrStorageCorrupt) || !errors.Is(privateErr, os.ErrNotExist) || retryErr != nil {
		t.Fatalf("post-lock identity: checks=%d snapshot=%v private=%v retry=%v", checks, snapshotErr, privateErr, retryErr)
	}
}

func TestLazyPrivateRepairIgnoresLegacyLeaseArtifact(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	for _, dir := range knownPrivateDirectories() {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	lease := filepath.Join(root, internalDirectory, "lease")
	if err := os.WriteFile(lease, nil, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lease, 0o666); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := os.Stat(filepath.Join(root, internalDirectory))
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	_, snapshotErr := s.Snapshot(context.Background())
	legacy, legacyErr := os.Stat(lease)

	// Assert.
	if before.Mode().Perm() != 0o777 || snapshotErr != nil || legacyErr != nil || legacy.Mode().Perm() != 0o666 {
		t.Fatalf("lazy repair: before=%v snapshot=%v lease=%v/%v", before.Mode().Perm(), snapshotErr, legacy.Mode().Perm(), legacyErr)
	}
	for _, dir := range knownPrivateDirectories() {
		info, statErr := os.Stat(filepath.Join(root, dir))
		if statErr != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("private directory %s = %v/%v, want 0700", dir, info, statErr)
		}
	}
}
