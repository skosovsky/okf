package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func documentPublicationIdentities(t *testing.T, root string) map[string]os.FileInfo {
	t.Helper()
	identities := make(map[string]os.FileInfo)
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		info, err := os.Lstat(filename)
		if err != nil {
			return err
		}
		identities[filepath.ToSlash(relative)] = info
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return identities
}

func documentAssertPublicationIdentities(
	t *testing.T,
	root string,
	before map[string]os.FileInfo,
) {
	t.Helper()
	for relative, want := range before {
		got, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil || !os.SameFile(want, got) {
			t.Fatalf("identity %s changed: before=%v after=%v error=%v", relative, want, got, err)
		}
	}
}
