package bundle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"testing/fstest"
)

func TestSourceFromFSFiltersMetadataAndReturnsOwnedSortedData(t *testing.T) {
	// Arrange.
	fsys := fstest.MapFS{
		"z.md":                 &fstest.MapFile{Data: []byte("z")},
		"a.md":                 &fstest.MapFile{Data: []byte("a")},
		"nested/asset.bin":     &fstest.MapFile{Data: []byte("asset")},
		"nested/.okf/keep.bin": &fstest.MapFile{Data: []byte("ordinary nested path")},
		".okf/receipt.json":    &fstest.MapFile{Data: []byte("private")},
		"link.md":              &fstest.MapFile{Mode: fs.ModeSymlink},
		"device":               &fstest.MapFile{Mode: fs.ModeDevice},
	}
	source := SourceFromFS(fsys)

	// Act.
	paths, err := source.Paths(context.Background())
	data, readErr := source.ReadFile(context.Background(), "a.md")
	if err != nil || readErr != nil {
		t.Fatalf("source errors = %v, %v", err, readErr)
	}
	data[0] = 'X'
	again, againErr := source.ReadFile(context.Background(), "a.md")

	// Assert.
	if againErr != nil {
		t.Fatalf("second ReadFile() error = %v", againErr)
	}
	want := []string{"a.md", "nested/.okf/keep.bin", "nested/asset.bin", "z.md"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("Paths() = %v, want %v", paths, want)
	}
	if got := string(again); got != "a" {
		t.Fatalf("ReadFile() retained caller mutation: %q", got)
	}
	paths[0] = "changed"
	secondPaths, secondErr := source.Paths(context.Background())
	if secondErr != nil || !reflect.DeepEqual(secondPaths, want) {
		t.Fatalf("Paths() exposed ownership: %v, %v", secondPaths, secondErr)
	}
}

func TestSourceFromFSRejectsUnsafeAndNonRegularReads(t *testing.T) {
	// Arrange.
	source := SourceFromFS(fstest.MapFS{
		"regular.md": &fstest.MapFile{Data: []byte("ok")},
		"link.md":    &fstest.MapFile{Mode: fs.ModeSymlink},
	})

	// Act + Assert.
	for _, name := range []string{"", "../regular.md", "/regular.md", `nested\\regular.md`, ".okf/receipt"} {
		if _, err := source.ReadFile(context.Background(), name); err == nil {
			t.Fatalf("ReadFile(%q) accepted unsafe path", name)
		}
	}
	if _, err := source.ReadFile(context.Background(), "link.md"); !errors.Is(err, ErrNotRegularFile) {
		t.Fatalf("ReadFile(link.md) error = %v, want ErrNotRegularFile", err)
	}
}

func TestSourceFromFSPropagatesCancellationAndFilesystemErrors(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := SourceFromFS(fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("a")}})
	failure := errors.New("read failure")
	failing := SourceFromFS(sourceFromFSFailure{MapFS: fstest.MapFS{"a.md": &fstest.MapFile{Data: []byte("a")}}, err: failure})

	// Act.
	_, pathsErr := cancelled.Paths(ctx)
	_, readErr := cancelled.ReadFile(ctx, "a.md")
	_, walkErr := failing.Paths(context.Background())
	_, fsErr := failing.ReadFile(context.Background(), "a.md")

	// Assert.
	if !errors.Is(pathsErr, context.Canceled) || !errors.Is(readErr, context.Canceled) {
		t.Fatalf("cancellation errors = %v, %v", pathsErr, readErr)
	}
	if !errors.Is(walkErr, failure) || !errors.Is(fsErr, failure) {
		t.Fatalf("filesystem errors = Paths:%v ReadFile:%v, want wrapped filesystem error", walkErr, fsErr)
	}
}

func TestSourceFromFSRejectsSymlinkedComponents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix-style symlink support")
	}

	// Arrange.
	root := t.TempDir()
	outside := t.TempDir()
	writeSourceFile(t, root, "nested/inside.md", "inside")
	writeSourceFile(t, outside, "hidden.md", "outside")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatalf("create ancestor symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "hidden.md"), filepath.Join(root, "leaf.md")); err != nil {
		t.Fatalf("create leaf symlink: %v", err)
	}
	source := SourceFromFS(os.DirFS(root))

	// Act.
	paths, pathsErr := source.Paths(context.Background())
	inside, insideErr := source.ReadFile(context.Background(), "nested/inside.md")
	_, ancestorErr := source.ReadFile(context.Background(), "linked/hidden.md")
	_, leafErr := source.ReadFile(context.Background(), "leaf.md")

	// Assert.
	if pathsErr != nil || insideErr != nil {
		t.Fatalf("source errors = %v, %v", pathsErr, insideErr)
	}
	if got, want := paths, []string{"nested/inside.md"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Paths() = %v, want %v", got, want)
	}
	if got := string(inside); got != "inside" {
		t.Fatalf("ReadFile(nested/inside.md) = %q, want inside", got)
	}
	if !errors.Is(ancestorErr, ErrNotRegularFile) || !errors.Is(leafErr, ErrNotRegularFile) {
		t.Fatalf("symlink errors = ancestor:%v leaf:%v, want ErrNotRegularFile", ancestorErr, leafErr)
	}
}

func TestSourceFromFSChecksContextDuringComponentWalk(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	source := SourceFromFS(cancelOnReadDirFS{
		MapFS:  fstest.MapFS{"nested/file.md": &fstest.MapFile{Data: []byte("inside")}},
		cancel: cancel,
	})

	// Act.
	_, err := source.ReadFile(ctx, "nested/file.md")

	// Assert.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadFile() error = %v, want context cancellation", err)
	}
}

func TestSourceReadsCancelDuringChunkedCopy(t *testing.T) {
	t.Run("SourceFromFS", func(t *testing.T) {
		// Arrange.
		ctx, cancel := context.WithCancel(context.Background())
		content := make([]byte, 128<<10)
		source := SourceFromFS(cancelAfterFileReadFS{
			MapFS:  fstest.MapFS{"large.bin": &fstest.MapFile{Data: content}},
			cancel: cancel,
		})

		// Act.
		data, err := source.ReadFile(ctx, "large.bin")

		// Assert.
		if !errors.Is(err, context.Canceled) || data != nil {
			t.Fatalf("ReadFile() = %d bytes, %v; want nil, context.Canceled", len(data), err)
		}
	})

	t.Run("FileSystemSource", func(t *testing.T) {
		// Arrange.
		root := t.TempDir()
		writeSourceFile(t, root, "large.bin", string(make([]byte, 256<<10)))
		source := &FileSystemSource{Root: root}
		defer source.Close()
		ctx := &cancelAfterContextChecks{Context: context.Background(), remaining: 3}

		// Act.
		data, err := source.ReadFile(ctx, "large.bin")

		// Assert.
		if !errors.Is(err, context.Canceled) || data != nil {
			t.Fatalf("ReadFile() = %d bytes, %v; want nil, context.Canceled", len(data), err)
		}
	})
}

func TestSourceFromFSAcceptsUnknownDirEntryTypes(t *testing.T) {
	// Arrange. Regular files and directories may report Type()==0; Info is the
	// authoritative classification for this adapter.
	source := SourceFromFS(unknownTypeFS{MapFS: fstest.MapFS{
		"nested/file.md": &fstest.MapFile{Data: []byte("inside")},
	}})

	// Act.
	data, err := source.ReadFile(context.Background(), "nested/file.md")

	// Assert.
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); got != "inside" {
		t.Fatalf("ReadFile() = %q, want inside", got)
	}
}

func TestSourceFromFSWrapsMissingPermissionAndReadErrors(t *testing.T) {
	// Arrange.
	permission := &fs.PathError{Op: "readdir", Path: ".", Err: fs.ErrPermission}
	read := errors.New("read failure")
	missing := SourceFromFS(fstest.MapFS{"present.md": &fstest.MapFile{Data: []byte("present")}})
	denied := SourceFromFS(sourceFromFSFailure{err: permission})
	broken := SourceFromFS(sourceFromFSReadFailure{
		MapFS: fstest.MapFS{"present.md": &fstest.MapFile{Data: []byte("present")}},
		err:   read,
	})

	// Act.
	_, missingErr := missing.ReadFile(context.Background(), "missing.md")
	_, permissionErr := denied.ReadFile(context.Background(), "present.md")
	_, readErr := broken.ReadFile(context.Background(), "present.md")

	// Assert.
	if !errors.Is(missingErr, fs.ErrNotExist) {
		t.Fatalf("missing error = %v, want fs.ErrNotExist", missingErr)
	}
	if !errors.Is(permissionErr, fs.ErrPermission) {
		t.Fatalf("permission error = %v, want fs.ErrPermission", permissionErr)
	}
	if !errors.Is(readErr, read) {
		t.Fatalf("read error = %v, want wrapped read error", readErr)
	}
}

type sourceFromFSFailure struct {
	fstest.MapFS
	err error
}

func (s sourceFromFSFailure) Open(string) (fs.File, error)    { return nil, s.err }
func (s sourceFromFSFailure) ReadFile(string) ([]byte, error) { return nil, s.err }
func (s sourceFromFSFailure) ReadDir(string) ([]fs.DirEntry, error) {
	return nil, s.err
}

type sourceFromFSReadFailure struct {
	fstest.MapFS
	err error
}

func (s sourceFromFSReadFailure) Open(string) (fs.File, error) { return nil, s.err }

type cancelOnReadDirFS struct {
	fstest.MapFS
	cancel context.CancelFunc
}

type cancelAfterFileReadFS struct {
	fstest.MapFS
	cancel context.CancelFunc
}

func (s cancelAfterFileReadFS) Open(name string) (fs.File, error) {
	file, err := s.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return &cancelAfterFileRead{File: file, cancel: s.cancel}, nil
}

type cancelAfterFileRead struct {
	fs.File
	cancel context.CancelFunc
}

func (f *cancelAfterFileRead) Read(data []byte) (int, error) {
	n, err := f.File.Read(data)
	if n > 0 {
		f.cancel()
	}
	return n, err
}

type cancelAfterContextChecks struct {
	context.Context
	remaining int
}

func (c *cancelAfterContextChecks) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return c.Context.Err()
}

func (s cancelOnReadDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := s.MapFS.ReadDir(name)
	s.cancel()
	return entries, err
}

type unknownTypeFS struct{ fstest.MapFS }

func (s unknownTypeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := s.MapFS.ReadDir(name)
	if err != nil {
		return nil, err
	}
	unknown := make([]fs.DirEntry, len(entries))
	for index, entry := range entries {
		unknown[index] = unknownTypeEntry{DirEntry: entry}
	}
	return unknown, nil
}

type unknownTypeEntry struct{ fs.DirEntry }

func (unknownTypeEntry) Type() fs.FileMode { return 0 }
