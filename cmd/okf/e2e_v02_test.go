package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBinaryE2EV02(t *testing.T) {
	// Arrange: exercise the built command, never the in-process CLI entry point.
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs() error = %v", err)
	}
	binary := filepath.Join(t.TempDir(), "okf")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/okf")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build error = %v; output = %s", err, output)
	}
	corpus := loadBinaryCorpus(t, repositoryRoot, "canonical_synthetic_v02", "verified_bare")
	bundle := corpus["canonical_synthetic_v02"]

	t.Run("JSON command wiring", func(t *testing.T) {
		tests := []struct {
			name string
			args []string
		}{
			{name: "version", args: []string{"version", "--json"}},
			{name: "validate", args: []string{"validate", "--path", bundle, "--spec", "0.2", "--strict", "--json"}},
			{name: "info", args: []string{"info", bundle, "--spec", "0.2", "--as-of", "2099-01-01", "--json"}},
			{name: "parse", args: []string{"parse", corpus["verified_bare"], "--spec", "0.2", "--json"}},
			{name: "graph", args: []string{"graph", bundle, "--format", "json-ld"}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				// Act.
				code, stdout, stderr := runBinary(t, binary, test.args...)

				// Assert.
				if code != 0 || stderr != "" {
					t.Fatalf("code/stderr = %d/%q; stdout=%q", code, stderr, stdout)
				}
				var response any
				if err := json.Unmarshal([]byte(stdout), &response); err != nil {
					t.Fatalf("JSON response error = %v; stdout=%q", err, stdout)
				}
			})
		}
	})

	t.Run("format preview and index write boundary", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		indexPath := filepath.Join(root, "index.md")
		notePath := filepath.Join(root, "note.md")
		writeBinaryFile(t, indexPath, "---\nokf_version: \"0.2\"\n---\n\nOld index.\n")
		writeBinaryFile(t, notePath, "---\ntype: Note\nx-keep: true\n---\nBody.\n")
		indexBefore := readBinaryFile(t, indexPath)
		noteBefore := readBinaryFile(t, notePath)

		// Act.
		fmtCode, fmtOut, fmtErr := runBinary(t, binary, "fmt", notePath, "--spec", "0.2")
		noteAfterFmt := readBinaryFile(t, notePath)
		indexCode, indexOut, indexErr := runBinary(t, binary, "index", root, "--spec", "0.2")
		indexAfter := readBinaryFile(t, indexPath)
		noteAfterIndex := readBinaryFile(t, notePath)

		// Assert.
		if fmtCode != 0 || fmtErr != "" || fmtOut == "" || !bytes.Equal(noteBefore, noteAfterFmt) {
			t.Fatalf("fmt code/stdout/stderr/unchanged = %d/%q/%q/%t", fmtCode, fmtOut, fmtErr, bytes.Equal(noteBefore, noteAfterFmt))
		}
		if indexCode != 0 || indexErr != "" || indexOut == "" || bytes.Equal(indexBefore, indexAfter) ||
			!bytes.Equal(noteBefore, noteAfterIndex) {
			t.Fatalf("index code/stdout/stderr/indexChanged/noteUnchanged = %d/%q/%q/%t/%t",
				indexCode, indexOut, indexErr, !bytes.Equal(indexBefore, indexAfter), bytes.Equal(noteBefore, noteAfterIndex))
		}
	})

	t.Run("migration preview apply and planned noop", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		indexPath := filepath.Join(root, "index.md")
		writeBinaryFile(t, indexPath, "---\nokf_version: \"0.1\"\n---\n\n# Knowledge\n\n- [Note](note.md)\n")
		writeBinaryFile(t, filepath.Join(root, "note.md"), "---\ntype: Knowledge\nx-keep: true\n---\nBody.\n")
		before := snapshotBinaryTree(t, root)
		args := []string{"migrate", root, "--from", "auto", "--to", "0.2", "--format", "json"}

		// Act.
		previewCode, previewOut, previewErr := runBinary(t, binary, args...)
		afterPreview := snapshotBinaryTree(t, root)
		_, previewStoreErr := os.Stat(filepath.Join(root, ".okf"))
		applyCode, applyOut, applyErr := runBinary(t, binary, append(args, "--write")...)
		noopCode, noopOut, noopErr := runBinary(t, binary, append(args, "--write")...)

		// Assert.
		var preview, apply, noop migrationProcessReport
		decodeBinaryJSON(t, previewOut, &preview)
		decodeBinaryJSON(t, applyOut, &apply)
		decodeBinaryJSON(t, noopOut, &noop)
		if previewCode != 0 || previewErr != "" || preview.Applied || preview.Outcome != "preview" ||
			!reflect.DeepEqual(before, afterPreview) || !os.IsNotExist(previewStoreErr) {
			t.Fatalf("preview code/stderr/report/tree/store = %d/%q/%#v/%t/%v",
				previewCode, previewErr, preview, reflect.DeepEqual(before, afterPreview), previewStoreErr)
		}
		if applyCode != 0 || applyErr != "" || !apply.Applied || apply.Noop || apply.Receipt == nil {
			t.Fatalf("apply code/stderr/report = %d/%q/%#v", applyCode, applyErr, apply)
		}
		if noopCode != 0 || noopErr != "" || !noop.Applied || !noop.Noop || noop.Outcome != "applied" ||
			noop.PlanDigest == "" || noop.ResolutionDigest == "" || noop.Receipt == nil ||
			len(noop.Receipt.ChangedFiles) != 0 || len(noop.Receipt.ChangedRefs) != 0 {
			t.Fatalf("noop code/stderr/report = %d/%q/%#v", noopCode, noopErr, noop)
		}
		if !bytes.Contains(readBinaryFile(t, indexPath), []byte(`okf_version: "0.2"`)) {
			t.Fatalf("apply did not write target version")
		}
	})

	t.Run("migration write through Darwin tmp alias", func(t *testing.T) {
		if runtime.GOOS != "darwin" {
			t.Skip("Darwin system alias contract")
		}

		// Arrange.
		root, err := os.MkdirTemp("/tmp", "okf-cli-alias-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(root) })
		writeBinaryFile(t, filepath.Join(root, "index.md"), "---\nokf_version: \"0.2\"\n---\n\n# Knowledge\n\n- [Note](note.md)\n")
		writeBinaryFile(t, filepath.Join(root, "note.md"), "---\ntype: Knowledge\n---\nBody.\n")

		// Act.
		code, stdout, stderr := runBinary(
			t,
			binary,
			"migrate", root, "--to", "0.2", "--write", "--format", "json",
		)

		// Assert.
		var report migrationProcessReport
		decodeBinaryJSON(t, stdout, &report)
		if code != 0 || stderr != "" || !report.Applied || !report.Noop || report.Receipt == nil {
			t.Fatalf("tmp migration code/stderr/report = %d/%q/%#v", code, stderr, report)
		}
		if _, err := os.Stat(filepath.Join(root, ".okf", "receipts")); err != nil {
			t.Fatalf("tmp migration receipt directory: %v", err)
		}
	})

	t.Run("fatal input fails closed", func(t *testing.T) {
		// Arrange.
		path := filepath.Join(t.TempDir(), "malformed.md")
		writeBinaryFile(t, path, "---\ntype: [\n---\nBody.\n")

		// Act.
		code, stdout, stderr := runBinary(t, binary, "parse", path, "--spec", "0.2", "--json")

		// Assert.
		if code != 1 || stdout != "" || strings.TrimSpace(stderr) == "" ||
			strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") != 0 {
			t.Fatalf("code/stdout/stderr = %d/%q/%q", code, stdout, stderr)
		}
	})
}

type migrationProcessReport struct {
	Outcome          string `json:"outcome"`
	Applied          bool   `json:"applied"`
	Noop             bool   `json:"noop"`
	PlanDigest       string `json:"plan_digest"`
	ResolutionDigest string `json:"resolution_digest"`
	Receipt          *struct {
		ChangedFiles []any `json:"changed_files"`
		ChangedRefs  []any `json:"changed_refs"`
	} `json:"receipt"`
}

func runBinary(t *testing.T, binary string, args ...string) (int, string, string) {
	t.Helper()
	command := exec.Command(binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("exec %v error = %v", args, err)
	}
	return exit.ExitCode(), stdout.String(), stderr.String()
}

func loadBinaryCorpus(t *testing.T, repositoryRoot string, keys ...string) map[string]string {
	t.Helper()
	fixtureRoot := filepath.Join(repositoryRoot, "fixtures", "v02")
	manifestPath := filepath.Join(fixtureRoot, "corpus.yaml")
	var manifest struct {
		Cases map[string]string `yaml:"cases"`
	}
	data := readBinaryFile(t, manifestPath)
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("yaml.Unmarshal(%s) error = %v", manifestPath, err)
	}
	resolved := make(map[string]string, len(keys))
	for _, key := range keys {
		relative := manifest.Cases[key]
		clean := filepath.Clean(filepath.FromSlash(relative))
		if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, "\\") ||
			clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("%s has invalid case %q=%q", manifestPath, key, relative)
		}
		path := filepath.Join(fixtureRoot, clean)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s case %q path %q: %v", manifestPath, key, relative, err)
		}
		resolved[key] = path
	}
	return resolved
}

func writeBinaryFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("os.WriteFile(%s) error = %v", path, err)
	}
}

func readBinaryFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%s) error = %v", path, err)
	}
	return data
}

func decodeBinaryJSON(t *testing.T, output string, destination any) {
	t.Helper()
	if err := json.Unmarshal([]byte(output), destination); err != nil {
		t.Fatalf("JSON response error = %v; stdout=%q", err, output)
	}
}

func snapshotBinaryTree(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			entries[filepath.ToSlash(relative)] = "non-regular"
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries[filepath.ToSlash(relative)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshotBinaryTree(%s) error = %v", root, err)
	}
	return entries
}
