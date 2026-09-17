package okfcli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIndexPreservesFutureRootVersionAndKeepsNestedIndexUnstamped(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("os.Mkdir() error = %v", err)
	}
	writeFixtureFile(t, filepath.Join(root, "index.md"), "---\nokf_version: \"9.9\"\n---\n\nOld index.\n")
	writeFixtureFile(t, filepath.Join(root, "root.md"), "---\ntype: Note\n---\n\nRoot.\n")
	writeFixtureFile(t, filepath.Join(root, "nested", "child.md"), "---\ntype: Note\n---\n\nChild.\n")
	var stdout, stderr bytes.Buffer

	// Act.
	code := Run([]string{"index", root}, &stdout, &stderr)
	rootIndex, rootErr := os.ReadFile(filepath.Join(root, "index.md"))
	nestedIndex, nestedErr := os.ReadFile(filepath.Join(root, "nested", "index.md"))

	// Assert.
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("Run(index) code/stderr = %d/%q", code, stderr.String())
	}
	if rootErr != nil || nestedErr != nil {
		t.Fatalf("ReadFile root/nested error = %v/%v", rootErr, nestedErr)
	}
	if !strings.Contains(string(rootIndex), `okf_version: "9.9"`) {
		t.Fatalf("root index = %q, missing preserved future version", rootIndex)
	}
	if strings.Contains(string(nestedIndex), "okf_version:") || strings.HasPrefix(string(nestedIndex), "---") {
		t.Fatalf("nested index = %q, unexpectedly stamped", nestedIndex)
	}
}

func TestIndexSelectorUsesSingleTransactionalDomainContract(t *testing.T) {
	tests := []struct {
		name       string
		declared   string
		selector   string
		wantCode   int
		wantChange bool
	}{
		{name: "legacy auto", declared: "0.1", selector: "auto", wantChange: true},
		{name: "legacy explicit", declared: "0.1", selector: "0.1", wantChange: true},
		{name: "legacy conflict", declared: "0.1", selector: "0.2", wantCode: 1},
		{name: "native auto", declared: "0.2", selector: "auto", wantChange: true},
		{name: "native explicit", declared: "0.2", selector: "0.2", wantChange: true},
		{name: "native conflict", declared: "0.2", selector: "0.1", wantCode: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
				t.Fatalf("os.Mkdir() error = %v", err)
			}
			rootIndex := filepath.Join(root, "index.md")
			nestedIndex := filepath.Join(root, "nested", "index.md")
			writeFixtureFile(t, rootIndex,
				"---\nokf_version: \""+test.declared+"\"\n---\n\n# Root before\n")
			writeFixtureFile(t, nestedIndex, "# Nested before\n")
			writeFixtureFile(t, filepath.Join(root, "nested", "note.md"),
				"---\ntype: Note\ntitle: Note\n---\nBody.\n")
			before := snapshotFilesystemTree(t, root)
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run(
				[]string{"index", root, "--spec", test.selector},
				&stdout,
				&stderr,
			)
			after := snapshotFilesystemTree(t, root)

			// Assert.
			if code != test.wantCode {
				t.Fatalf("Run(index --spec %s) code/stderr = %d/%q, want %d",
					test.selector, code, stderr.String(), test.wantCode)
			}
			if test.wantChange {
				if stderr.Len() != 0 || stdout.Len() == 0 || reflect.DeepEqual(before, after) {
					t.Fatalf("successful index code/stdout/stderr/changed = %d/%q/%q/%t",
						code, stdout.String(), stderr.String(), !reflect.DeepEqual(before, after))
				}
				updatedRoot, err := os.ReadFile(rootIndex)
				if err != nil || !bytes.Contains(
					updatedRoot,
					[]byte(`okf_version: "`+test.declared+`"`),
				) {
					t.Fatalf("root version read/content = %v/%q", err, updatedRoot)
				}
				updatedNested, err := os.ReadFile(nestedIndex)
				if err != nil || bytes.Contains(updatedNested, []byte("okf_version:")) {
					t.Fatalf("nested index read/content = %v/%q", err, updatedNested)
				}
				return
			}
			if stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "selector conflicts with declaration") ||
				!reflect.DeepEqual(before, after) {
				t.Fatalf("conflicting index stdout/stderr/unchanged = %q/%q/%t",
					stdout.String(), stderr.String(), reflect.DeepEqual(before, after))
			}
		})
	}
}

func TestIndexFutureSelectorAutoPreservesAndExplicitSelectorsRejectWithoutWrites(t *testing.T) {
	for _, selector := range []string{"auto", "0.1", "0.2"} {
		t.Run(selector, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			rootIndex := filepath.Join(root, "index.md")
			writeFixtureFile(t, rootIndex,
				"---\nokf_version: \"9.9\"\n---\n\n# Root before\n")
			writeFixtureFile(t, filepath.Join(root, "note.md"),
				"---\ntype: Note\ntitle: Note\n---\nBody.\n")
			before := snapshotFilesystemTree(t, root)
			var stdout, stderr bytes.Buffer

			// Act.
			code := Run([]string{"index", root, "--spec", selector}, &stdout, &stderr)
			after := snapshotFilesystemTree(t, root)

			// Assert.
			if selector == "auto" {
				updated, err := os.ReadFile(rootIndex)
				if code != 0 || stderr.Len() != 0 || err != nil ||
					!bytes.Contains(updated, []byte(`okf_version: "9.9"`)) ||
					reflect.DeepEqual(before, after) {
					t.Fatalf("future auto code/stderr/read/changed/content = %d/%q/%v/%t/%q",
						code, stderr.String(), err, !reflect.DeepEqual(before, after), updated)
				}
				return
			}
			if code != 1 || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "selector conflicts with declaration") ||
				!reflect.DeepEqual(before, after) {
				t.Fatalf("future explicit code/stdout/stderr/unchanged = %d/%q/%q/%t",
					code, stdout.String(), stderr.String(), reflect.DeepEqual(before, after))
			}
		})
	}
}

func TestIndexMalformedDeclarationAndSymlinkDestinationFailBeforeAnyWrite(t *testing.T) {
	t.Run("malformed declaration", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
			t.Fatalf("os.Mkdir() error = %v", err)
		}
		writeFixtureFile(t, filepath.Join(root, "index.md"),
			"---\nokf_version: []\n---\n\n# Root before\n")
		writeFixtureFile(t, filepath.Join(root, "nested", "index.md"), "# Nested before\n")
		writeFixtureFile(t, filepath.Join(root, "nested", "note.md"),
			"---\ntype: Note\ntitle: Note\n---\nBody.\n")
		before := snapshotFilesystemTree(t, root)
		var stdout, stderr bytes.Buffer

		// Act.
		code := Run([]string{"index", root}, &stdout, &stderr)
		after := snapshotFilesystemTree(t, root)

		// Assert.
		if code != 1 || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "invalid OKF version declaration") ||
			!reflect.DeepEqual(before, after) {
			t.Fatalf("malformed index code/stdout/stderr/unchanged = %d/%q/%q/%t",
				code, stdout.String(), stderr.String(), reflect.DeepEqual(before, after))
		}
	})

	t.Run("symlink destination", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		external := filepath.Join(t.TempDir(), "external.md")
		externalBefore := []byte("external bytes\n")
		writeFixtureFile(t, external, string(externalBefore))
		if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(nested) error = %v", err)
		}
		if err := os.MkdirAll(filepath.Join(root, "other"), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(other) error = %v", err)
		}
		writeFixtureFile(t, filepath.Join(root, "index.md"),
			"---\nokf_version: \"0.2\"\n---\n\n# Root before\n")
		writeFixtureFile(t, filepath.Join(root, "nested", "note.md"),
			"---\ntype: Note\ntitle: Nested\n---\nBody.\n")
		writeFixtureFile(t, filepath.Join(root, "other", "note.md"),
			"---\ntype: Note\ntitle: Other\n---\nBody.\n")
		if err := os.Symlink(external, filepath.Join(root, "nested", "index.md")); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		before := snapshotFilesystemTree(t, root)
		var stdout, stderr bytes.Buffer

		// Act.
		code := Run([]string{"index", root, "--spec", "0.2"}, &stdout, &stderr)
		after := snapshotFilesystemTree(t, root)
		externalAfter, readErr := os.ReadFile(external)

		// Assert.
		if code != 1 || stdout.Len() != 0 ||
			!strings.Contains(stderr.String(), "destination is a symlink") ||
			!reflect.DeepEqual(before, after) ||
			readErr != nil || !bytes.Equal(externalAfter, externalBefore) {
			t.Fatalf("symlink index code/stdout/stderr/unchanged/external/read = %d/%q/%q/%t/%q/%v",
				code, stdout.String(), stderr.String(), reflect.DeepEqual(before, after),
				externalAfter, readErr)
		}
	})
}

func TestConcurrentIndexCLIInvocationsPublishOneCompleteGeneration(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "index.md"),
		"---\nokf_version: \"0.2\"\n---\n\n# Root before\n")
	for _, directory := range []string{"x/a", "x/b"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatalf("os.MkdirAll(%s) error = %v", directory, err)
		}
	}
	for path, title := range map[string]string{
		"x/a/one.md":   "One",
		"x/a/two.md":   "Two",
		"x/b/three.md": "Three",
		"x/b/four.md":  "Four",
	} {
		writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(path)),
			"---\ntype: Note\ntitle: "+title+"\n---\nBody.\n")
	}
	type runResult struct {
		code   int
		stdout string
		stderr string
	}
	start := make(chan struct{})
	results := make(chan runResult, 2)
	run := func() {
		<-start
		var stdout, stderr bytes.Buffer
		code := Run([]string{"index", root, "--spec", "0.2"}, &stdout, &stderr)
		results <- runResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
	}
	go run()
	go run()

	// Act.
	close(start)
	first := <-results
	second := <-results

	// Assert.
	successes := 0
	for index, result := range []runResult{first, second} {
		switch result.code {
		case 0:
			successes++
			if result.stderr != "" || result.stdout == "" {
				t.Fatalf("concurrent result[%d] success stdout/stderr = %q/%q",
					index, result.stdout, result.stderr)
			}
		case 1:
			if result.stdout != "" || result.stderr == "" {
				t.Fatalf("concurrent result[%d] conflict stdout/stderr = %q/%q",
					index, result.stdout, result.stderr)
			}
		default:
			t.Fatalf("concurrent result[%d] code = %d", index, result.code)
		}
	}
	if successes == 0 {
		t.Fatalf("both concurrent index commands failed: %#v / %#v", first, second)
	}
	for path, fragments := range map[string][]string{
		"index.md":     {`okf_version: "0.2"`, "[x](x/index.md)"},
		"x/index.md":   {"[a](a/index.md)", "[b](b/index.md)"},
		"x/a/index.md": {"[One](one.md)", "[Two](two.md)"},
		"x/b/index.md": {"[Four](four.md)", "[Three](three.md)"},
	} {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("os.ReadFile(%s) error = %v", path, err)
		}
		for _, fragment := range fragments {
			if !bytes.Contains(content, []byte(fragment)) {
				t.Fatalf("%s missing %q:\n%s", path, fragment, content)
			}
		}
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasPrefix(entry.Name(), ".okf-index-txn-") {
			return fmt.Errorf("orphan index transaction artifact: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
