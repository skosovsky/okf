package bundle

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Source is an immutable, snapshot-compatible source of bundle files. Paths
// must satisfy ValidateRevisionPath. ReadFile must return an owned byte slice;
// Load makes an additional defensive copy before retaining it.
type Source interface {
	Paths(context.Context) ([]string, error)
	ReadFile(context.Context, string) ([]byte, error)
}

// FileSystemSource adapts a directory to Source. It pins Root when first used,
// so Paths and ReadFile observe one directory even if its pathname is renamed
// or replaced. Symlinks are never listed or read, including symlinked
// directories.
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
	root, err := s.openRoot()
	if err != nil {
		return nil, err
	}

	var out []string
	if err := walkRevisionVisible(ctx, root, ".", "", &out); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (s *FileSystemSource) ReadFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	name, err := normalizeSourcePath(name)
	if err != nil {
		return nil, err
	}
	root, err := s.openRoot()
	if err != nil {
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
	return append([]byte(nil), data...), nil
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.root != nil {
		return s.root, nil
	}
	root, err := openRootWithoutSymlinks(s.Root)
	if err != nil {
		return nil, err
	}
	s.root = root
	return root, nil
}

// openRootWithoutSymlinks pins pathName by opening every absolute component
// from the volume root. Lstat is intentionally used instead of EvalSymlinks:
// a symlink is rejected, never resolved and accepted under a different name.
func openRootWithoutSymlinks(pathName string) (*os.Root, error) {
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
	root, err := os.OpenRoot(volumeRoot)
	if err != nil {
		return nil, err
	}
	if relative == "." {
		return root, nil
	}

	current := root
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
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
		next, err := current.OpenRoot(component)
		if err != nil {
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
	return current, nil
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
	dir, err := openDirectory(root, directory)
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
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
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
		rel := path.Join(prefix, entry.Name())
		if rel == ".okf" {
			if info.IsDir() {
				continue
			}
			continue
		}
		if info.IsDir() {
			if err := walkRevisionVisible(ctx, root, name, rel, out); err != nil {
				// A directory replaced by a symlink after ReadDir is ignored.
				if isSymlinkPathError(err) {
					continue
				}
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := ValidateRevisionPath(rel); err != nil {
			return err
		}
		*out = append(*out, rel)
	}
	return nil
}

func openRegularFile(root *os.Root, name string) (*os.File, error) {
	directory, base := path.Split(name)
	dir, err := openDirectory(root, strings.TrimSuffix(directory, "/"))
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
	for _, part := range strings.Split(name, "/") {
		before, err := current.Lstat(part)
		if err != nil {
			closeOpened()
			return nil, err
		}
		if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			closeOpened()
			return nil, fmt.Errorf("symlink or non-directory component %q", part)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
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
	return current, nil
}

func isSymlinkPathError(err error) bool {
	return strings.Contains(err.Error(), "symlink") || strings.Contains(err.Error(), "changed component")
}

func ioReadAllContext(ctx context.Context, file *os.File) ([]byte, error) {
	var out []byte
	buf := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := file.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func normalizeSourcePath(name string) (string, error) {
	if err := ValidateRevisionPath(name); err != nil {
		return "", fmt.Errorf("invalid bundle source path %q", name)
	}
	return name, nil
}

// ValidateRevisionPath verifies a revision-visible bundle path. Such paths are
// valid UTF-8, normalized relative slash paths with no control characters or
// backslashes. The root metadata directory and its descendants are reserved;
// a directory named .okf below an ordinary asset directory is not reserved.
//
//	path                 result
//	.okf                 rejected
//	.okf/x               rejected
//	.okf-name            accepted
//	nested/.okf/file     accepted
//
// It deliberately does not impose ConceptID rules: assets may use ordinary
// Unicode and punctuation.
func ValidateRevisionPath(name string) error {
	if name == "" || !utf8.ValidString(name) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") ||
		name == ".okf" || strings.HasPrefix(name, ".okf/") {
		return fmt.Errorf("invalid revision path %q", name)
	}
	for _, r := range name {
		if r <= 0x1f || r == 0x7f {
			return fmt.Errorf("invalid revision path %q", name)
		}
	}
	if clean := path.Clean(name); clean != name || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("invalid revision path %q", name)
	}
	return nil
}
