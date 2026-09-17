package fs

// Metadata and directory I/O classification during recovery.

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestMetadataIOWinsOverCancellation(t *testing.T) {
	for _, role := range []string{"binding", "sentinel"} {
		t.Run(role, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			name := path.Join(claimDirectory, "r5.binding.json")
			if role == "sentinel" {
				name = path.Join(internalDirectory, "staging", "r5", directorySentinelName)
			}
			absolute := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(absolute, []byte(`{"version":1}`), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			injected := errors.New("metadata EIO")
			s.descriptorBarrier = func(operation string) error {
				if operation == "metadata_read:"+name {
					cancel()
					return injected
				}
				return nil
			}

			// Act.
			_, readErr := s.readMetadataLimitObserved(ctx, name, maxJournalManifestRead)

			// Assert.
			if !errors.Is(readErr, injected) || errors.Is(readErr, context.Canceled) || errors.Is(readErr, store.ErrStorageCorrupt) {
				t.Fatalf("read=%v, want exact operational EIO", readErr)
			}
		})
	}
}

func TestProvenanceIOAndStructuralClassification(t *testing.T) {
	for _, kind := range []string{"io", "digest"} {
		t.Run(kind, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			s, err := Open(root, Config{})
			if err != nil {
				t.Fatal(err)
			}
			registerStoreCleanup(t, s)
			data := []byte("visible")
			if err := os.WriteFile(filepath.Join(root, "index.md"), data, 0o640); err != nil {
				t.Fatal(err)
			}
			base := []journalBaseFile{{Path: "index.md", Size: int64(len(data)), Digest: sha256Digest(data)}}
			result := []journalFile{{Path: "index.md", Size: int64(len(data)), Digest: sha256Digest(data)}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			injected := errors.New("provenance EIO")
			if kind == "io" {
				s.descriptorBarrier = func(operation string) error {
					if operation == "provenance_read:index.md" {
						cancel()
						return injected
					}
					return nil
				}
			} else {
				base[0].Digest = "sha256:" + strings.Repeat("0", 64)
				result[0].Digest = base[0].Digest
			}

			// Act.
			provenanceErr := s.validateCurrentJournalProvenance(ctx, base, result)

			// Assert.
			if kind == "io" {
				if !errors.Is(provenanceErr, injected) || errors.Is(provenanceErr, context.Canceled) || errors.Is(provenanceErr, store.ErrStorageCorrupt) {
					t.Fatalf("provenance=%v, want exact operational EIO", provenanceErr)
				}
			} else if !errors.Is(provenanceErr, store.ErrStorageCorrupt) {
				t.Fatalf("provenance=%v, want structural corruption", provenanceErr)
			}
		})
	}
}

func TestOrphanDirectoryIOIsOperational(t *testing.T) {
	for _, cut := range []string{"orphan_temporary_readdir", "orphan_stage_readdir", "orphan_payload_readdir"} {
		t.Run(cut, func(t *testing.T) {
			// Arrange.
			_, s, _, _, _, _ := stageNamespaceCleanupFixture(t, 0)
			ctx, cancel := context.WithCancel(context.Background())
			injected := errors.New("orphan ReadDir EIO")
			s.descriptorBarrier = func(operation string) error {
				if operation == cut {
					cancel()
					return injected
				}
				return nil
			}

			// Act.
			var cleanupErr error
			if cut == "orphan_temporary_readdir" {
				cleanupErr = s.cleanupOrphanTemps(ctx)
			} else {
				cleanupErr = s.cleanupOrphanStages(ctx)
			}

			// Assert.
			if !errors.Is(cleanupErr, injected) || errors.Is(cleanupErr, context.Canceled) || errors.Is(cleanupErr, store.ErrStorageCorrupt) {
				t.Fatalf("cleanup=%v, want exact operational ReadDir error", cleanupErr)
			}
		})
	}
}

func TestTransactionReadDirIOIsOperational(t *testing.T) {
	// Arrange.
	_, s, _, _ := stageRecoveryJournal(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	injected := errors.New("transaction ReadDir EIO")
	s.descriptorBarrier = func(operation string) error {
		if operation == "recovery_transactions_readdir" {
			cancel()
			return injected
		}
		return nil
	}

	// Act.
	recoverErr := s.recoverForTest(ctx)

	// Assert.
	if !errors.Is(recoverErr, injected) || errors.Is(recoverErr, context.Canceled) || errors.Is(recoverErr, store.ErrStorageCorrupt) {
		t.Fatalf("recover=%v, want exact operational ReadDir error", recoverErr)
	}
}

func TestPrivateDirectorySyncIOIsOperational(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	absolute := filepath.Join(root, filepath.FromSlash(claimDirectory))
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	injected := errors.New("private fsync EIO")
	s.fileSync = func(*os.File) error { cancel(); return injected }

	// Act.
	repairErr := s.repairPrivateDirectory(ctx, claimDirectory)

	// Assert.
	if !errors.Is(repairErr, injected) || errors.Is(repairErr, context.Canceled) || errors.Is(repairErr, store.ErrStorageCorrupt) {
		t.Fatalf("repair=%v, want exact operational fsync error", repairErr)
	}
}

func TestMetadataStructuralControlsRemainCorruption(t *testing.T) {
	// Arrange.
	root, s := adversarialStore(t, Config{})
	target := path.Join(claimDirectory, "r5-target")
	symlink := path.Join(claimDirectory, "r5-symlink")
	absoluteTarget := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(absoluteTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absoluteTarget, []byte("metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path.Base(target), filepath.Join(root, filepath.FromSlash(symlink))); err != nil {
		t.Fatal(err)
	}

	// Act.
	_, readErr := s.readMetadataLimitObserved(context.Background(), symlink, maxJournalManifestRead)

	// Assert.
	if !errors.Is(readErr, store.ErrStorageCorrupt) {
		t.Fatalf("read=%v, want structural corruption", readErr)
	}
}
