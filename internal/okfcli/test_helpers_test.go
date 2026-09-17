package okfcli

import (
	"os"
	"path/filepath"
	"testing"
)

func fixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	all := append([]string{"..", "..", "fixtures", "v02"}, parts...)
	path, err := filepath.Abs(filepath.Join(all...))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	return path
}

func writeFixtureFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func snapshotRegularFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotRegularFiles(%s) error = %v", root, err)
	}
	return files
}

func snapshotFilesystemTree(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			entries[relative] = "directory"
			return nil
		}
		if !entry.Type().IsRegular() {
			entries[relative] = "non-regular"
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries[relative] = "file\x00" + string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotFilesystemTree(%s) error = %v", root, err)
	}
	return entries
}

func makeTextWriterBundle(t *testing.T, version string) (string, string) {
	t.Helper()
	root := t.TempDir()
	note := filepath.Join(root, "note.md")
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \""+version+"\"\n---\n\n# Concepts\n\n* [Note](note.md)\n")
	timestamp := ""
	if version == "0.1" {
		timestamp = "timestamp: 2026-06-01T10:00:00Z\n"
	}
	writeFixtureFile(t, note, "---\ntype: Note\n"+timestamp+"---\nSee [missing](/missing.md).\n")
	return root, note
}
