package okfcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFmtPreservesUnknownKeysAndDoesNotWriteByDefault(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	path := filepath.Join(root, "concept.md")
	original := "---\ntype: Note\nx-producer:\n  keep: [all, unknown, data]\n---\n\nBody.\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"fmt", "--spec", "0.2", path}, &stdout, &stderr)
	after, err := os.ReadFile(path)

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(fmt) code/stderr = %d/%q", code, stderr.String())
	}
	if err != nil {
		t.Fatalf("os.ReadFile() error = %v", err)
	}
	if string(after) != original {
		t.Fatalf("fmt without --write changed file:\ngot  %q\nwant %q", after, original)
	}
	for _, forbidden := range []string{"generated:", "okf_version:"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("fmt stdout = %q, unexpectedly contains %q", stdout.String(), forbidden)
		}
	}
	if !strings.Contains(stdout.String(), "x-producer:") {
		t.Fatalf("fmt stdout = %q, missing unknown key", stdout.String())
	}
}
