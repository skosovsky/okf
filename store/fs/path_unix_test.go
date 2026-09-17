//go:build (darwin && !ios) || (linux && !android)

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
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

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
	visible := make(map[string]bool, 2)
	for _, entry := range entries {
		if entry.Name() != internalDirectory {
			visible[entry.Name()] = true
		}
	}
	if len(visible) != 2 || !visible["valid.md"] || !visible[filepath.Base(bad)] {
		t.Fatalf("revision-visible root entries after rejected snapshot = %#v, want valid and invalid source files", visible)
	}
	transactions, err := os.ReadDir(filepath.Join(root, internalDirectory, "transactions"))
	if err != nil {
		t.Fatalf("ReadDir(transactions) error = %v", err)
	}
	if len(transactions) != 0 {
		t.Fatalf("journals after rejected snapshot = %#v, want none", transactions)
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
