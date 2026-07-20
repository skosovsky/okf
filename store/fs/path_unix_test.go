//go:build !windows

package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotRejectsInvalidUTF8VisibleFilenameBeforePublication(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "valid.md", "# valid\n")
	bad := filepath.Join(root, string([]byte{'b', 'a', 'd', 0xff, '.', 'b', 'i', 'n'}))
	if err := os.WriteFile(bad, []byte("bad"), 0o600); err != nil {
		t.Skipf("filesystem does not accept invalid UTF-8 names: %v", err)
	}
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	_, snapshotErr := s.Snapshot(context.Background())
	entries, entriesErr := os.ReadDir(root)

	// Assert.
	if snapshotErr == nil {
		t.Fatal("Snapshot() accepted invalid UTF-8 revision-visible filename")
	}
	if entriesErr != nil {
		t.Fatal(entriesErr)
	}
	if len(entries) != 2 {
		t.Fatalf("root entries after rejected snapshot = %#v", entries)
	}
	if _, err := os.Stat(filepath.Join(root, internalDirectory, "transactions")); !os.IsNotExist(err) {
		t.Fatalf("journal exists after rejected snapshot: %v", err)
	}
}

func TestDecodeJournalRejectsInvalidUTF8BeforeJSONReplacement(t *testing.T) {
	// Arrange. encoding/json would otherwise decode this key as U+FFFD.
	raw := []byte{'{', '"', 'f', 0xff, '"', ':', '1', '}'}

	// Act.
	_, err := decodeJournal(raw)

	// Assert.
	if err == nil {
		t.Fatal("decodeJournal() accepted invalid UTF-8")
	}
}
