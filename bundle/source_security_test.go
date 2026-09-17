package bundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type closableTestSource struct {
	closeCalls int
}

func (s *closableTestSource) Paths(context.Context) ([]string, error) { return []string{"a.md"}, nil }

func (s *closableTestSource) ReadFile(context.Context, string) ([]byte, error) {
	return []byte("---\ntype: Note\n---\nA\n"), nil
}

func (s *closableTestSource) Close() error {
	s.closeCalls++
	return nil
}

type pathOnlySource struct{ paths []string }

func (s pathOnlySource) Paths(context.Context) ([]string, error) {
	return append([]string(nil), s.paths...), nil
}

type reservedPathReadSpy struct {
	paths     []string
	readCalls int
}

func (s *reservedPathReadSpy) Paths(context.Context) ([]string, error) {
	return append([]string(nil), s.paths...), nil
}

func (s *reservedPathReadSpy) ReadFile(context.Context, string) ([]byte, error) {
	s.readCalls++
	return []byte("must not be read"), nil
}
func (pathOnlySource) ReadFile(context.Context, string) ([]byte, error) {
	return []byte("asset"), nil
}

func TestLoadRejectsInvalidGenericSourcePaths(t *testing.T) {
	// Arrange.
	tests := []string{
		"",
		"./asset.bin",
		"../asset.bin",
		"nested//asset.bin",
		`nested\\asset.bin`,
		"/asset.bin",
		".okf/transaction.json",
		"bad\x00.bin",
		"bad\x1f.bin",
		"bad\x7f.bin",
		string([]byte{'b', 'a', 'd', 0xff}),
	}

	for _, name := range tests {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			// Act.
			_, err := Load(context.Background(), pathOnlySource{paths: []string{name}})

			// Assert.
			if err == nil {
				t.Fatalf("Load() accepted invalid source path %q", name)
			}
		})
	}
}

func TestLoadRejectsReservedIndexTransactionPathsBeforeAnyRead(t *testing.T) {
	t.Parallel()

	for _, reserved := range []string{
		".okf-index-txn-stage-v1-0000-deadbeef",
		"nested/.OKF-INDEX-TXN-restore-v1-0000-deadbeef",
		"nested/.okf-index-txn-private/file.bin",
	} {
		reserved := reserved
		t.Run(reserved, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			source := &reservedPathReadSpy{
				paths: []string{"a.md", reserved},
			}

			// Act.
			loaded, err := Load(context.Background(), source)

			// Assert.
			if loaded != nil || err == nil {
				t.Fatalf("Load() = %#v, %v; want deterministic reserved-path rejection", loaded, err)
			}
			if source.readCalls != 0 {
				t.Fatalf("ReadFile calls = %d, want zero", source.readCalls)
			}
		})
	}
}

func TestFilesystemSourcesRejectReservedIndexTransactionEntries(t *testing.T) {
	t.Parallel()

	for _, reserved := range []string{
		".okf-index-txn-stage-v1-0000-deadbeef",
		"nested/.OKF-INDEX-TXN-private",
	} {
		reserved := reserved
		t.Run(reserved, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeSourceFile(t, root, "a.md", "ordinary")
			writeSourceFile(t, root, reserved, "reserved")
			source := &FileSystemSource{Root: root}
			t.Cleanup(func() { _ = source.Close() })

			// Act.
			paths, err := source.Paths(context.Background())
			loaded, loadErr := LoadBundle(root)

			// Assert.
			if paths != nil || err == nil {
				t.Fatalf("Paths() = %#v, %v; want reserved-path rejection", paths, err)
			}
			if loaded != nil || loadErr == nil {
				t.Fatalf("LoadBundle() = %#v, %v; want zero-capture rejection", loaded, loadErr)
			}
		})
	}
}

func TestValidateRevisionPathReservesInternalNamespacesExactly(t *testing.T) {
	// Arrange.
	tests := []struct {
		path  string
		valid bool
	}{
		{path: ".okf"},
		{path: ".okf/x"},
		{path: ".okf-name", valid: true},
		{path: "nested/.okf/file", valid: true},
		{path: ".okf-index-txn-stage-v1-0000-deadbeef"},
		{path: "nested/.OKF-INDEX-TXN-foreign"},
		{path: "nested/.okf-index-tx", valid: true},
		{path: "nested/.okf-index-txn", valid: true},
		{path: "nested/.okf-index-txnordinary", valid: true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			// Act.
			err := ValidateRevisionPath(tt.path)

			// Assert.
			if (err == nil) != tt.valid {
				t.Fatalf("ValidateRevisionPath(%q) error = %v, valid = %t", tt.path, err, tt.valid)
			}
		})
	}
}

func TestFileSystemSourceAcceptsNestedMetadataNamedAssetDirectory(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeSourceFile(t, root, ".okf/hidden", "metadata")
	writeSourceFile(t, root, ".okf-name", "ordinary")
	writeSourceFile(t, root, "nested/.okf/file.bin", "nested ordinary")
	source := &FileSystemSource{Root: root}
	t.Cleanup(func() { _ = source.Close() })

	// Act.
	paths, err := source.Paths(context.Background())
	data, readErr := source.ReadFile(context.Background(), "nested/.okf/file.bin")

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if got, want := fmt.Sprint(paths), "[.okf-name nested/.okf/file.bin]"; got != want {
		t.Fatalf("Paths() = %s, want %s", got, want)
	}
	if got, want := string(data), "nested ordinary"; got != want {
		t.Fatalf("ReadFile() = %q, want %q", got, want)
	}
}

func TestLoadLeavesCallerOwnedSourceOpenAndReusable(t *testing.T) {
	// Arrange.
	source := &closableTestSource{}

	// Act.
	first, firstErr := Load(context.Background(), source)
	second, secondErr := Load(context.Background(), source)

	// Assert.
	if firstErr != nil || secondErr != nil {
		t.Fatalf("Load() errors = %v, %v", firstErr, secondErr)
	}
	if first.Len() != 1 || second.Len() != 1 {
		t.Fatalf("loaded lengths = %d, %d, want 1, 1", first.Len(), second.Len())
	}
	if source.closeCalls != 0 {
		t.Fatalf("Load() Close calls = %d, want caller ownership", source.closeCalls)
	}
}

func TestFileSystemSourceCloseIsIdempotent(t *testing.T) {
	// Arrange.
	source := &FileSystemSource{Root: t.TempDir()}
	if _, err := source.Paths(context.Background()); err != nil {
		t.Fatalf("Paths() error = %v", err)
	}

	// Act.
	firstErr := source.Close()
	secondErr := source.Close()

	// Assert.
	if firstErr != nil || secondErr != nil {
		t.Fatalf("Close() errors = %v, %v", firstErr, secondErr)
	}
	if source.root != nil {
		t.Fatal("Close() retained pinned root")
	}
}

func TestFileSystemSourcePinsRootAcrossRootPathSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix-style symlink support")
	}
	root := t.TempDir()
	writeSourceFile(t, root, "note.md", "inside")
	source := &FileSystemSource{Root: root}
	t.Cleanup(func() { _ = source.Close() })
	if _, err := source.Paths(context.Background()); err != nil {
		t.Fatalf("Paths() error = %v", err)
	}

	parked := filepath.Join(t.TempDir(), "parked-root")
	if err := os.Rename(root, parked); err != nil {
		t.Fatalf("rename root: %v", err)
	}
	outside := t.TempDir()
	writeSourceFile(t, outside, "note.md", "outside")
	if err := os.Symlink(outside, root); err != nil {
		t.Fatalf("replace root with symlink: %v", err)
	}

	data, err := source.ReadFile(context.Background(), "note.md")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "inside" {
		t.Fatalf("ReadFile() = %q, want content from pinned root", got)
	}
}

func TestLoadBundleRejectsRootAndAncestorSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix-style symlink support")
	}

	// Arrange.
	realRoot := t.TempDir()
	writeSourceFile(t, realRoot, "note.md", "---\ntype: Note\n---\ninside\n")
	container := t.TempDir()
	rootLink := filepath.Join(container, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatalf("create root symlink: %v", err)
	}
	ancestorLink := filepath.Join(container, "ancestor-link")
	if err := os.Symlink(filepath.Dir(realRoot), ancestorLink); err != nil {
		t.Fatalf("create ancestor symlink: %v", err)
	}
	ancestorPath := filepath.Join(ancestorLink, filepath.Base(realRoot))

	// Act + Assert.
	for _, path := range []string{rootLink, ancestorPath} {
		if _, err := LoadBundle(path); err == nil {
			t.Fatalf("LoadBundle(%q) error = nil, want symlink rejection", path)
		}
	}
}

func TestFileSystemSourceRejectsAncestorAndFileSymlinkSwaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix-style symlink support")
	}
	root := t.TempDir()
	writeSourceFile(t, root, "nested/note.md", "inside")
	source := &FileSystemSource{Root: root}
	t.Cleanup(func() { _ = source.Close() })
	if _, err := source.Paths(context.Background()); err != nil {
		t.Fatalf("Paths() error = %v", err)
	}

	outside := t.TempDir()
	writeSourceFile(t, outside, "note.md", "outside")
	parked := filepath.Join(root, "nested-real")
	if err := os.Rename(filepath.Join(root, "nested"), parked); err != nil {
		t.Fatalf("rename nested: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "nested")); err != nil {
		t.Fatalf("replace nested with symlink: %v", err)
	}
	if _, err := source.ReadFile(context.Background(), "nested/note.md"); err == nil {
		t.Fatal("ReadFile() error = nil after ancestor symlink swap")
	}

	if err := os.Remove(filepath.Join(root, "nested")); err != nil {
		t.Fatalf("remove nested symlink: %v", err)
	}
	if err := os.Rename(parked, filepath.Join(root, "nested")); err != nil {
		t.Fatalf("restore nested: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "nested", "note.md")); err != nil {
		t.Fatalf("remove note: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "note.md"), filepath.Join(root, "nested", "note.md")); err != nil {
		t.Fatalf("replace note with symlink: %v", err)
	}
	if _, err := source.ReadFile(context.Background(), "nested/note.md"); err == nil {
		t.Fatal("ReadFile() error = nil after file symlink swap")
	}
}

func writeSourceFile(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
