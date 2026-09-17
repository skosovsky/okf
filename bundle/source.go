package bundle

import (
	"context"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
)

// Source is an immutable, snapshot-compatible source of bundle files. Paths
// must satisfy ValidateRevisionPath. Paths and ReadFile must return caller-owned
// slices; Load makes an additional defensive copy before retaining file bytes.
type Source interface {
	Paths(context.Context) ([]string, error)
	ReadFile(context.Context, string) ([]byte, error)
}

// SourceFromFS adapts fsys to Source without taking ownership of it. The
// caller is responsible for supplying a stable filesystem snapshot for the
// complete Paths/ReadFile lifetime; a changing fs.FS can otherwise expose an
// inconsistent source. Paths are deterministic slash paths for revision-visible
// regular files only; .okf and symlinks are excluded. A basename beginning
// with .okf-index-txn- or .okf-document-txn- in any ASCII case is reserved
// transaction evidence:
// Paths rejects it instead of hiding or capturing it. ReadFile rejects a
// symlink in any path component rather than resolving it. Paths and ReadFile
// each return caller-owned defensive copies. Source remains the core contract
// for loaders and mutations; this is only an io/fs adapter.
func SourceFromFS(fsys iofs.FS) Source { return fsSource{fsys: fsys} }

type fsSource struct{ fsys iofs.FS }

func (s fsSource) Paths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.fsys == nil {
		return nil, fmt.Errorf("bundle: nil fs")
	}
	paths := make([]string, 0)
	err := iofs.WalkDir(s.fsys, ".", func(name string, entry iofs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return fmt.Errorf("bundle source walk %q: %w", name, walkErr)
		}
		if name == "." {
			return nil
		}
		if name == ".okf" || strings.HasPrefix(name, ".okf/") {
			if entry.IsDir() {
				return iofs.SkipDir
			}
			return nil
		}
		if isReservedTransactionPath(name) {
			return reservedIndexTransactionPathError(name)
		}
		if entry.Type()&iofs.ModeSymlink != 0 {
			if entry.IsDir() {
				return iofs.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("bundle source stat %q: %w", name, err)
		}
		if info.Mode()&iofs.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		if err := ValidateRevisionPathContext(ctx, name); err != nil {
			return fmt.Errorf("bundle source path %q: %w", name, err)
		}
		paths = append(paths, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sortCompareContext(ctx, paths, compareStringsContext); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s fsSource) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.fsys == nil {
		return nil, fmt.Errorf("bundle: nil fs")
	}
	if err := ValidateRevisionPathContext(ctx, name); err != nil {
		return nil, fmt.Errorf("invalid bundle source path %q: %w", name, err)
	}
	if err := fsNoFollowRegularFile(ctx, s.fsys, name); err != nil {
		return nil, fmt.Errorf("stat bundle source path %q: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := s.fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open bundle source path %q: %w", name, err)
	}
	defer file.Close()
	data, err := ioReadAllContext(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("read bundle source path %q: %w", name, err)
	}
	return data, nil
}

// fsNoFollowRegularFile proves every component from . to name without using
// fs.Stat(name), which is permitted to resolve symlinks. Each ReadDir runs on
// a parent already proved to be a real directory. This is necessarily a
// snapshot check: SourceFromFS requires fsys to remain stable for the complete
// Paths/ReadFile lifetime.
func fsNoFollowRegularFile(ctx context.Context, fsys iofs.FS, name string) error {
	directory := "."
	components, err := sourcePathComponentsContext(ctx, name)
	if err != nil {
		return err
	}
	for index, component := range components {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := iofs.ReadDir(fsys, directory)
		if err != nil {
			return fmt.Errorf("read directory %q: %w", directory, err)
		}

		var found iofs.DirEntry
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			equal, err := equalStringContext(ctx, entry.Name(), component)
			if err != nil {
				return err
			}
			if equal {
				found = entry
				break
			}
		}
		if found == nil {
			return fmt.Errorf("component %q: %w", component, iofs.ErrNotExist)
		}
		if found.Type()&iofs.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink component %q", ErrNotRegularFile, component)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := found.Info()
		if err != nil {
			return fmt.Errorf("stat component %q: %w", component, err)
		}
		if info.Mode()&iofs.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink component %q", ErrNotRegularFile, component)
		}
		if index == len(components)-1 {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: path %q", ErrNotRegularFile, name)
			}
			return nil
		}
		if !info.IsDir() {
			return fmt.Errorf("%w: non-directory component %q", ErrNotRegularFile, component)
		}
		directory = path.Join(directory, component)
	}
	return fmt.Errorf("%w: path %q", ErrNotRegularFile, name)
}

func sourcePathComponentsContext(ctx context.Context, name string) ([]string, error) {
	var out []string
	start := 0
	for index := 0; index <= len(name); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if index == len(name) || name[index] == '/' {
			owned, err := stringFromStringContext(ctx, name[start:index])
			if err != nil {
				return nil, err
			}
			out = append(out, owned)
			start = index + 1
		}
	}
	return out, ctx.Err()
}

// FileSystemSource adapts a directory to Source. It pins Root when first used,
// so Paths and ReadFile observe one directory even if its pathname is renamed
// or replaced. Symlinks are never listed or read, including symlinked
// directories.
//
// Paths rejects every case-folded reserved transaction basename at every
// depth. Such entries are internal crash evidence and must be recovered or
// removed before the directory can become revision-visible.
//
// Close releases the pinned root. Source callers own its lifecycle; Load does
// not close supplied sources. Close is safe to call repeatedly.
type FileSystemSource struct {
	Root string

	mu   sync.Mutex
	root *os.Root
}

func (s *FileSystemSource) Paths(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := s.openRootContext(ctx)
	if err != nil {
		return nil, err
	}

	var out []string
	if err := walkRevisionVisible(ctx, root, ".", "", &out); err != nil {
		return nil, err
	}
	if err := sortCompareContext(ctx, out, compareStringsContext); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *FileSystemSource) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := normalizeSourcePathContext(ctx, name)
	if err != nil {
		return nil, err
	}
	root, err := s.openRootContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openRegularFile(root, name)
	if err != nil {
		return nil, fmt.Errorf("invalid bundle source path %q: %w", name, err)
	}
	defer file.Close()
	data, err := ioReadAllContext(ctx, file)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Close releases the descriptor backing the pinned root.
func (s *FileSystemSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root == nil {
		return nil
	}
	err := s.root.Close()
	s.root = nil
	return err
}

func (s *FileSystemSource) openRoot() (*os.Root, error) {
	return s.openRootContext(context.Background())
}

func (s *FileSystemSource) openRootContext(ctx context.Context) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root != nil {
		return s.root, nil
	}
	root, err := openRootWithoutSymlinksContext(ctx, s.Root)
	if err != nil {
		return nil, err
	}
	s.root = root
	return root, ctx.Err()
}

// openRootWithoutSymlinks pins pathName by opening every absolute component
// from the volume root. Lstat is intentionally used instead of EvalSymlinks:
// a symlink is rejected, never resolved and accepted under a different name.
func openRootWithoutSymlinks(pathName string) (*os.Root, error) {
	return openRootWithoutSymlinksContext(context.Background(), pathName)
}

func openRootWithoutSymlinksContext(ctx context.Context, pathName string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(pathName)
	if err != nil {
		return nil, err
	}
	abs = canonicalVolumePath(abs)
	volumeRoot := filepath.VolumeName(abs) + string(filepath.Separator)
	relative, err := filepath.Rel(volumeRoot, abs)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, err
	}
	if relative == "." {
		return root, nil
	}

	current := root
	components, err := filesystemPathComponentsContext(ctx, relative)
	if err != nil {
		root.Close()
		return nil, err
	}
	for _, component := range components {
		if err := ctx.Err(); err != nil {
			current.Close()
			return nil, err
		}
		before, err := current.Lstat(component)
		if err != nil {
			current.Close()
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, fmt.Errorf("%w: symlink component %s", ErrNotDirectory, component)
		}
		if !before.IsDir() {
			current.Close()
			return nil, fmt.Errorf("%w: %s", ErrNotDirectory, pathName)
		}
		if err := ctx.Err(); err != nil {
			current.Close()
			return nil, err
		}
		next, err := current.OpenRoot(component)
		if err != nil {
			current.Close()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			next.Close()
			current.Close()
			return nil, err
		}
		after, err := next.Lstat(".")
		if err != nil || !os.SameFile(before, after) {
			next.Close()
			current.Close()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("%w: bundle root changed while opening", ErrNotDirectory)
		}
		current.Close()
		current = next
	}
	if err := ctx.Err(); err != nil {
		current.Close()
		return nil, err
	}
	return current, nil
}

func filesystemPathComponentsContext(ctx context.Context, value string) ([]string, error) {
	separator := byte(filepath.Separator)
	var out []string
	start := 0
	for index := 0; index <= len(value); index++ {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if index == len(value) || value[index] == separator {
			if index > start {
				owned, err := stringFromStringContext(ctx, value[start:index])
				if err != nil {
					return nil, err
				}
				out = append(out, owned)
			}
			start = index + 1
		}
	}
	return out, ctx.Err()
}

// canonicalVolumePath accounts for Darwin's kernel-owned /var, /tmp and /etc
// aliases. They are fixed aliases of the startup volume, not user-controlled
// path components; treating them as ordinary symlinks would make all t.TempDir
// and standard temporary paths unusable. No arbitrary symlink is resolved.
func canonicalVolumePath(abs string) string {
	if runtime.GOOS != "darwin" {
		return abs
	}
	for alias, physical := range map[string]string{
		"/etc": "/private/etc",
		"/tmp": "/private/tmp",
		"/var": "/private/var",
	} {
		if abs == alias {
			return physical
		}
		if strings.HasPrefix(abs, alias+string(filepath.Separator)) {
			return physical + strings.TrimPrefix(abs, alias)
		}
	}
	return abs
}

func walkRevisionVisible(ctx context.Context, root *os.Root, directory, prefix string, out *[]string) error {
	dir, err := openDirectoryContext(ctx, root, directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	dirFile, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer dirFile.Close()
	entries, err := dirFile.ReadDir(-1)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sortCompareContext(ctx, entries, func(ctx context.Context, left, right os.DirEntry) (int, error) {
		return compareStringsContext(ctx, left.Name(), right.Name())
	}); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := path.Join(prefix, entry.Name())
		if isReservedTransactionPath(rel) {
			return reservedIndexTransactionPathError(rel)
		}
		// DirEntry.Type is only a hint on some file systems. Lstat is the
		// authority here: do not let an unknown entry type turn a FIFO, socket
		// or device into a revision-visible file.
		info, err := dir.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := path.Join(directory, entry.Name())
		if rel == ".okf" {
			if info.IsDir() {
				continue
			}
			continue
		}
		if info.IsDir() {
			if err := walkRevisionVisible(ctx, root, name, rel, out); err != nil {
				// A directory replaced by a symlink after ReadDir is ignored.
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				if isSymlinkPathError(err) {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return ctxErr
					}
					continue
				}
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := ValidateRevisionPathContext(ctx, rel); err != nil {
			return err
		}
		*out = append(*out, rel)
	}
	return nil
}

func openRegularFile(root *os.Root, name string) (*os.File, error) {
	directory, base := path.Split(name)
	dir, err := openDirectoryContext(context.Background(), root, strings.TrimSuffix(directory, "/"))
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	before, err := dir.Lstat(base)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("symlink")
	}
	if !before.Mode().IsRegular() {
		return nil, ErrNotRegularFile
	}
	// O_NONBLOCK ensures a rename race cannot make this call wait on a FIFO or
	// device. O_NOFOLLOW is a second, kernel-enforced symlink barrier.
	file, err := openFileNoFollow(dir, base)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		file.Close()
		return nil, fmt.Errorf("path changed while opening")
	}
	return file, nil
}

func openDirectory(root *os.Root, name string) (*os.Root, error) {
	return openDirectoryContext(context.Background(), root, name)
}

func openDirectoryContext(ctx context.Context, root *os.Root, name string) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current := root
	var opened []*os.Root
	closeOpened := func() {
		for i := len(opened) - 1; i >= 0; i-- {
			_ = opened[i].Close()
		}
	}
	if name == "" || name == "." {
		return current.OpenRoot(".")
	}
	parts, err := sourcePathComponentsContext(ctx, name)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		if err := ctx.Err(); err != nil {
			closeOpened()
			return nil, err
		}
		before, err := current.Lstat(part)
		if err != nil {
			closeOpened()
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			closeOpened()
			return nil, fmt.Errorf("symlink or non-directory component %q", part)
		}
		if err := ctx.Err(); err != nil {
			closeOpened()
			return nil, err
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			closeOpened()
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			next.Close()
			closeOpened()
			return nil, err
		}
		after, err := next.Lstat(".")
		if err != nil || !os.SameFile(before, after) {
			next.Close()
			closeOpened()
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("symlink or changed component %q", part)
		}
		opened = append(opened, next)
		current = next
	}
	// The caller owns only the last root. Its parents can be closed: child
	// descriptors remain valid after their parent descriptors are closed.
	for i := 0; i+1 < len(opened); i++ {
		_ = opened[i].Close()
	}
	if err := ctx.Err(); err != nil {
		current.Close()
		return nil, err
	}
	return current, nil
}

func isSymlinkPathError(err error) bool {
	return strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "changed component")
}

func ioReadAllContext(ctx context.Context, reader io.Reader) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := reader.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == io.EOF {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func normalizeSourcePath(name string) (string, error) {
	return normalizeSourcePathContext(context.Background(), name)
}

func normalizeSourcePathContext(ctx context.Context, name string) (string, error) {
	if err := ValidateRevisionPathContext(ctx, name); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("invalid bundle source path %q", name)
	}
	owned, err := stringFromStringContext(ctx, name)
	if err != nil {
		return "", err
	}
	return owned, ctx.Err()
}

// ValidateRevisionPath verifies a revision-visible bundle path. Such paths are
// valid UTF-8, normalized relative slash paths with no control characters or
// backslashes. The root metadata directory and its descendants are reserved.
// Every basename beginning with a transaction prefix (ASCII-case-
// insensitively) is also reserved at every depth for self-authenticating
// publication artifacts. A directory named .okf below an ordinary asset
// directory is not reserved.
//
//	path                 result
//	.okf                 rejected
//	.okf/x               rejected
//	.okf-name            accepted
//	nested/.okf/file     accepted
//	nested/.okf-index-txn-stage-v1-... rejected
//	nested/.okf-document-txn-v2-... rejected
//
// It deliberately does not impose ConceptID rules: assets may use ordinary
// Unicode and punctuation.
func ValidateRevisionPath(name string) error {
	return ValidateRevisionPathContext(context.Background(), name)
}

// ValidateRevisionPathContext is the cancellation-aware form of
// ValidateRevisionPath.
func ValidateRevisionPathContext(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("invalid revision path %q", name)
	}
	start := 0
	components := make([]string, 0, 8)
	for index := 0; index <= len(name); {
		if index%(64<<10) == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if index == len(name) || name[index] == '/' {
			component := name[start:index]
			if component == "" || component == "." || component == ".." || strings.Contains(component, "\\") {
				return fmt.Errorf("invalid revision path %q", name)
			}
			if len(components) == 0 && component == ".okf" || hasASCIIFoldPrefix(component, indexTransactionPrefix) || hasASCIIFoldPrefix(component, documentTransactionPrefix) || hasASCIIFoldPrefix(component, publicationPrivateClaimPrefix) {
				return fmt.Errorf("invalid revision path %q", name)
			}
			components = append(components, component)
			start = index + 1
			index++
			continue
		}
		r, size := utf8.DecodeRuneInString(name[index:])
		if r == utf8.RuneError && size == 1 || r <= 0x1f || r == 0x7f || r == '\\' || index == 0 && r == '/' {
			return fmt.Errorf("invalid revision path %q", name)
		}
		index += size
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func isIndexTransactionReservedPath(name string) bool {
	for _, component := range strings.Split(name, "/") {
		if hasASCIIFoldPrefix(component, indexTransactionPrefix) {
			return true
		}
	}
	return false
}

func isReservedTransactionPath(name string) bool {
	if isIndexTransactionReservedPath(name) || isDocumentTransactionReservedPath(name) {
		return true
	}
	for _, component := range strings.Split(name, "/") {
		if hasASCIIFoldPrefix(component, publicationPrivateClaimPrefix) {
			return true
		}
	}
	return false
}

func reservedIndexTransactionPathError(name string) error {
	return fmt.Errorf("reserved index transaction path %q", name)
}
