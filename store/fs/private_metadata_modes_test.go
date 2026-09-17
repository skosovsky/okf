package fs

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"
)

func TestPrivateMetadataDirectoriesAreRepairedToOwnerOnly(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{internalDirectory, path.Join(internalDirectory, "transactions"), path.Join(internalDirectory, "staging"), path.Join(internalDirectory, "receipts"), path.Join(internalDirectory, "capabilities"), temporaryDirectory} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, dir), 0o777); err != nil {
			t.Fatal(err)
		}
	}
	s, err := openObserved(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{internalDirectory, path.Join(internalDirectory, "transactions"), path.Join(internalDirectory, "staging"), path.Join(internalDirectory, "receipts"), path.Join(internalDirectory, "capabilities"), temporaryDirectory} {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode=%#o err=%v", dir, info.Mode().Perm(), err)
		}
	}
}
