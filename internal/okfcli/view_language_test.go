package okfcli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestViewCLILanguage(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("# Index\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "viewer.html")
	var stdout, stderr bytes.Buffer
	// Act
	code := Run([]string{"view", root, "--lang", "ru", "--output", output}, &stdout, &stderr)
	// Assert
	data, err := os.ReadFile(output)
	if code != 0 || err != nil || !bytes.Contains(data, []byte(`<html lang="ru">`)) {
		t.Fatalf("code %d, err %v, stderr %s", code, err, stderr.String())
	}
}

func TestViewCLIInvalidLanguageDoesNotWrite(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("# Index\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []bool{false, true} {
		dir := t.TempDir()
		output := filepath.Join(dir, "viewer.html")
		if existing {
			if err := os.WriteFile(output, []byte("previous"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		var stdout, stderr bytes.Buffer
		// Act
		code := Run([]string{"view", root, "--lang", "fr", "--output", output, "--overwrite"}, &stdout, &stderr)
		// Assert
		if code == 0 || !bytes.Contains(stderr.Bytes(), []byte("--lang")) {
			t.Fatal("unsupported language accepted")
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if existing {
			data, err := os.ReadFile(output)
			if err != nil || string(data) != "previous" || len(entries) != 1 {
				t.Fatal("existing export changed")
			}
		} else if len(entries) != 0 {
			t.Fatal("invalid language created output")
		}
	}
}
