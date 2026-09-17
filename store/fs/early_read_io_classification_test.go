package fs

// Early-read structural and close-error classification.

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

func TestEarlyStructuralReadKeepsCloseOperationalFirst(t *testing.T) {
	for _, test := range []struct {
		name string
		act  func(*testing.T, string, *Store, error) error
	}{
		{name: "recovery journal type", act: func(t *testing.T, root string, s *Store, closeEIO error) error {
			name := path.Join(internalDirectory, "transactions", "type.json")
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o700); err != nil {
				t.Fatal(err)
			}
			injectReadCloseError(t, s, closeEIO, "")
			_, _, err := s.readRecoveryJournal(context.Background(), name, maxJournalManifestRead)
			return err
		}},
		{name: "recovery journal size", act: func(t *testing.T, root string, s *Store, closeEIO error) error {
			name := path.Join(internalDirectory, "transactions", "size.json")
			writeRecoveryFile(t, root, name, []byte("oversized"))
			injectReadCloseError(t, s, closeEIO, "")
			_, _, err := s.readRecoveryJournal(context.Background(), name, 1)
			return err
		}},
		{name: "owned metadata identity", act: func(t *testing.T, root string, s *Store, closeEIO error) error {
			name := path.Join(temporaryDirectory, ".okf-tmp-880000001-0")
			writeRecoveryFile(t, root, name, []byte("payload"))
			expected, _ := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
			replaceRegularWithSameBytes(t, root, name)
			injectReadCloseError(t, s, closeEIO, "")
			_, err := s.readMetadataLimitOwned(context.Background(), name, maxMetadataRead, expected)
			return err
		}},
		{name: "open pinned metadata type", act: func(t *testing.T, root string, s *Store, closeEIO error) error {
			name := path.Join(temporaryDirectory, "type-dir")
			if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(name)), 0o700); err != nil {
				t.Fatal(err)
			}
			injectReadCloseError(t, s, closeEIO, "type-dir")
			_, err := s.openPinnedMetadata(name, false)
			return err
		}},
		{name: "visible pinned identity", act: func(t *testing.T, root string, s *Store, closeEIO error) error {
			name := "a.md"
			writeRecoveryFile(t, root, name, []byte("visible"))
			expected, _ := os.Lstat(filepath.Join(root, name))
			replaceRegularWithSameBytes(t, root, name)
			injectReadCloseError(t, s, closeEIO, "")
			_, err := readPinnedRegularWithClose(context.Background(), s.rootFD, name, expected, s.closeReadFile)
			return err
		}},
		{name: "provenance pinned identity", act: func(t *testing.T, root string, s *Store, closeEIO error) error {
			name := "a.md"
			writeRecoveryFile(t, root, name, []byte("provenance"))
			expected, _ := os.Lstat(filepath.Join(root, name))
			replaceRegularWithSameBytes(t, root, name)
			injectReadCloseError(t, s, closeEIO, "")
			_, err := s.streamPinnedRegularDigest(context.Background(), name, expected)
			return err
		}},
	} {
		for _, withClose := range []bool{false, true} {
			name := "structural-only"
			if withClose {
				name = "structural-and-close"
			}
			t.Run(test.name+"/"+name, func(t *testing.T) {
				// Arrange.
				root, s := adversarialStore(t, Config{})
				closeEIO := errors.New("early close EIO")
				var injected error
				if withClose {
					injected = closeEIO
				}

				// Act.
				err := test.act(t, root, s, injected)

				// Assert.
				if !errors.Is(err, store.ErrStorageCorrupt) || errors.Is(err, closeEIO) != withClose {
					t.Fatalf("error = %v, want corrupt close=%t", err, withClose)
				}
				if withClose && strings.Index(err.Error(), closeEIO.Error()) > strings.Index(err.Error(), store.ErrStorageCorrupt.Error()) {
					t.Fatalf("error = %v, want close I/O before structural corruption", err)
				}
			})
		}
	}
}

func TestEarlyReadCloseOnlyRemainsOperational(t *testing.T) {
	for _, test := range []struct {
		name string
		act  func(*testing.T, string, *Store) error
	}{
		{name: "recovery journal", act: func(t *testing.T, root string, s *Store) error {
			name := path.Join(internalDirectory, "transactions", "valid.json")
			writeRecoveryFile(t, root, name, []byte("{}"))
			_, _, err := s.readRecoveryJournal(context.Background(), name, maxJournalManifestRead)
			return err
		}},
		{name: "owned metadata", act: func(t *testing.T, root string, s *Store) error {
			name := path.Join(temporaryDirectory, ".okf-tmp-880000002-0")
			writeRecoveryFile(t, root, name, []byte("payload"))
			expected, _ := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
			_, err := s.readMetadataLimitOwned(context.Background(), name, maxMetadataRead, expected)
			return err
		}},
		{name: "visible pinned", act: func(t *testing.T, root string, s *Store) error {
			writeRecoveryFile(t, root, "a.md", []byte("visible"))
			expected, _ := os.Lstat(filepath.Join(root, "a.md"))
			_, err := readPinnedRegularWithClose(context.Background(), s.rootFD, "a.md", expected, s.closeReadFile)
			return err
		}},
		{name: "provenance pinned", act: func(t *testing.T, root string, s *Store) error {
			writeRecoveryFile(t, root, "a.md", []byte("visible"))
			expected, _ := os.Lstat(filepath.Join(root, "a.md"))
			_, err := s.streamPinnedRegularDigest(context.Background(), "a.md", expected)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root, s := adversarialStore(t, Config{})
			closeEIO := errors.New("close-only EIO")
			injectReadCloseError(t, s, closeEIO, "")

			// Act.
			err := test.act(t, root, s)

			// Assert.
			if !errors.Is(err, closeEIO) || errors.Is(err, store.ErrStorageCorrupt) {
				t.Fatalf("error = %v, want operational close-only", err)
			}
		})
	}
}
