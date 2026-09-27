package okfcli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewCLIExportsOfflineHTMLWithExplicitInstant(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n# Index\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\ntitle: Тест\nstale_after: 2026-09-26T12:00:00+07:00\n---\n# Привет\n"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "view.html")
	var stdout, stderr bytes.Buffer
	// Act
	code := Run([]string{"view", root, "--output", output, "--temporal-profile", "instant-0b87c52", "--as-of", "2026-09-26T12:00:00+07:00"}, &stdout, &stderr)
	// Assert
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	for _, needle := range []string{"Тест", "Привет", "instant-0b87c52", "stale", "connect-src 'none'"} {
		if !strings.Contains(html, needle) {
			t.Fatalf("output missing %q", needle)
		}
	}
	if code := Run([]string{"view", root, "--output", output}, &stdout, &stderr); code == 0 {
		t.Fatal("collision accepted")
	}
}

func TestViewCLIRejectsInvalidTimeAndConceptOverwrite(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\n---\nBody"), 0644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	// Act, Assert
	if code := Run([]string{"view", root, "--output", filepath.Join(root, "view.html"), "--temporal-profile", "instant-0b87c52", "--as-of", "2026-09-26"}, &stdout, &stderr); code == 0 {
		t.Fatal("date-only as-of accepted for instant profile")
	}
	if code := Run([]string{"view", root, "--output", filepath.Join(root, "view.html"), "--temporal-profile", "instant-0b87c52", "--as-of", "2026-09-26T12:00:00-00:00"}, &stdout, &stderr); code == 0 {
		t.Fatal("unknown UTC offset accepted for instant profile")
	}
	if code := Run([]string{"view", root, "--output", filepath.Join(root, "a.md"), "--overwrite"}, &stdout, &stderr); code == 0 {
		t.Fatal("concept overwrite accepted")
	}
	data, err := os.ReadFile(filepath.Join(root, "a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Body") {
		t.Fatal("concept changed")
	}
}

func TestViewCLIDefaultsToPinnedDateRevision(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.md"), []byte("---\nokf_version: \"0.2\"\n---\n# Index\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\nstale_after: 2026-09-26\n---\nBody"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "date.html")
	var stdout, stderr bytes.Buffer
	// Act
	code := Run([]string{"view", root, "--output", output, "--as-of", "2026-09-26"}, &stdout, &stderr)
	// Assert
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"date-3fcbb9f", "explicit-civil-date", "2026-09-26", "stale"} {
		if !strings.Contains(string(data), needle) {
			t.Fatalf("output missing %q", needle)
		}
	}
}

func TestViewCLIExplicitLegacyVersionForUndeclaredBundle(t *testing.T) {
	// Arrange
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: Note\n---\nBody"), 0644); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "legacy.html")
	var stdout, stderr bytes.Buffer
	// Act
	code := Run([]string{"view", root, "--spec", "0.1", "--output", output}, &stdout, &stderr)
	// Assert
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"spec_version\":\"0.1\"") || !strings.Contains(string(data), "\"spec_revision\":\"legacy-v0.1\"") {
		t.Fatal("explicit legacy resolution lost")
	}
}
