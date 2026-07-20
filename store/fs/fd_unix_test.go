//go:build darwin || linux

package fs

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/skosovsky/okf/store"
	"golang.org/x/sys/unix"
)

func TestSnapshotAndMetadataReadsNeverBlockOnSpecialFiles(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "regular.md", "safe")
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	fifo := filepath.Join(root, "blocked.md")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo(visible) error = %v", err)
	}
	metadataFIFO := filepath.Join(root, internalDirectory, "transactions", "blocked.json")
	if err := unix.Mkfifo(metadataFIFO, 0o600); err != nil {
		t.Fatalf("Mkfifo(metadata) error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.Snapshot(context.Background())
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Snapshot() blocked on a FIFO")
	}

	done = make(chan error, 1)
	go func() {
		_, err := readMetadata(context.Background(), s.rootFD, path.Join(internalDirectory, "transactions", "blocked.json"))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, store.ErrStorageCorrupt) {
			t.Fatalf("readMetadata() error = %v, want storage corruption", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readMetadata() blocked on a FIFO")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(canceled) error = %v, want context.Canceled", err)
	}
}

func TestReadPinnedRegularRejectsSpecialFileSwapWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "note.md", "safe")
	opened, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	expected, err := opened.Lstat("note.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "note.md"), filepath.Join(root, "held.md")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "note.md"), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := readPinnedRegular(context.Background(), opened, "note.md", expected)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("readPinnedRegular() error = nil after FIFO swap")
		}
	case <-time.After(time.Second):
		t.Fatal("readPinnedRegular() blocked after FIFO swap")
	}
}

// TestDescriptorBarrierRejectsSwappedWriteParents is deliberately synchronous:
// the hook runs after openat has pinned the parent and before create/rename.
// It models the exact Lstat-then-swap window without timing or sleeps.
func TestDescriptorBarrierRejectsSwappedWriteParents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ancestor string
		write    func(*Store) error
	}{
		{"content", "content", func(s *Store) error { return s.writeFile("content/document.md", []byte("safe")) }},
		{"journal", ".okf/transactions", func(s *Store) error { return s.writeJournal(t.Context(), ".okf/transactions/swap.json", []byte("{}")) }},
		{"receipt", ".okf/receipts", func(s *Store) error { return s.writePrivateDurableAt(".okf/receipts/swap.json", []byte("{}")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, tc.ancestor), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "attacker"), 0o755); err != nil {
				t.Fatal(err)
			}
			s, err := Open(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			swapped := false
			s.descriptorBarrier = func(operation string) error {
				if operation != "create" || swapped {
					return nil
				}
				swapped = true
				old := filepath.Join(root, tc.ancestor+".held")
				if err := os.Rename(filepath.Join(root, tc.ancestor), old); err != nil {
					return err
				}
				// All targets stay inside root: containment alone is insufficient.
				target := "attacker"
				if tc.ancestor != "content" {
					target = "../../attacker"
				}
				return os.Symlink(target, filepath.Join(root, tc.ancestor))
			}

			err = tc.write(s)
			if !errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("write error = %v, want storage corruption", err)
			}
			if _, err := os.Stat(filepath.Join(root, "attacker", "document.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("attacker content write: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "attacker", "swap.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("attacker metadata write: %v", err)
			}
		})
	}
}

func TestOpenRejectsSplitReadAndMutationRoots(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "safe")
	held := root + ".held"
	openRootAfterReadCapability = func() {
		if err := os.Rename(root, held); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o755); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, root, "a.md", "attacker")
	}
	t.Cleanup(func() { openRootAfterReadCapability = nil })

	// Act.
	s, err := Open(root, Config{})

	// Assert.
	if s != nil || !errors.Is(err, store.ErrStorageCorrupt) {
		t.Fatalf("Open() store=%v err=%v, want storage corruption", s, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, internalDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement root mutated: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(held, internalDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("original root mutated after failed open: %v", statErr)
	}
}

func TestOpenRejectsAncestorSymlinkSwapBetweenPinnedAndReadCapabilities(t *testing.T) {
	// Arrange.
	parent := t.TempDir()
	ancestor := filepath.Join(parent, "ancestor")
	root := filepath.Join(ancestor, "bundle")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "a.md", "safe")
	attacker := t.TempDir()
	if err := os.Mkdir(filepath.Join(attacker, "bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(attacker, "bundle"), "a.md", "attacker")
	held := ancestor + ".held"
	openRootAfterReadCapability = func() {
		if err := os.Rename(ancestor, held); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(attacker, ancestor); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { openRootAfterReadCapability = nil })

	// Act.
	s, err := Open(root, Config{})

	// Assert.
	if s != nil || !errors.Is(err, store.ErrStorageCorrupt) {
		t.Fatalf("Open() store=%v err=%v, want storage corruption", s, err)
	}
	if _, statErr := os.Stat(filepath.Join(attacker, "bundle", internalDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("attacker root mutated: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(held, "bundle", internalDirectory)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("pinned root mutated after failed open: %v", statErr)
	}
}

func TestDescriptorBarrierRejectsSwappedRenameAndRemoveParents(t *testing.T) {
	for _, operation := range []string{"rename", "remove"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "content"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "attacker"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "content", "source"), []byte("safe"), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.descriptorBarrier = func(got string) error {
				if got != operation {
					return nil
				}
				if err := os.Rename(filepath.Join(root, "content"), filepath.Join(root, "content.held")); err != nil {
					return err
				}
				return os.Symlink("attacker", filepath.Join(root, "content"))
			}
			if operation == "rename" {
				err = s.fdRename("content/source", "content/destination")
			} else {
				err = s.fdRemove("content/source")
			}
			if !errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("%s error = %v, want storage corruption", operation, err)
			}
			if _, err := os.Stat(filepath.Join(root, "attacker", "source")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("attacker affected: %v", err)
			}
		})
	}
}
