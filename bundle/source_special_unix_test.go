//go:build unix

package bundle

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFileSystemSourceSkipsAndRejectsSpecialFiles(t *testing.T) {
	// Unix-domain socket paths have a small kernel limit (104 bytes on
	// Darwin), so the usual testing temp path can be too long.
	tempBase := ""
	if runtime.GOOS == "darwin" {
		tempBase = "/private/tmp"
	}
	root, err := os.MkdirTemp(tempBase, "okf-src-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	writeSourceFile(t, root, "regular.md", "regular")
	fifo := filepath.Join(root, "blocked.md")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}
	socket := filepath.Join(root, "socket.md")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	source := &FileSystemSource{Root: root}
	t.Cleanup(func() { _ = source.Close() })
	paths, err := source.Paths(context.Background())
	if err != nil {
		t.Fatalf("Paths() error = %v", err)
	}
	if got, want := paths, []string{"regular.md"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("Paths() = %#v, want %#v", got, want)
	}

	for _, name := range []string{"blocked.md", "socket.md"} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				_, err := source.ReadFile(context.Background(), name)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrNotRegularFile) {
					t.Fatalf("ReadFile() error = %v, want ErrNotRegularFile", err)
				}
			case <-time.After(time.Second):
				t.Fatalf("ReadFile(%q) blocked on a special file", name)
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := source.ReadFile(ctx, name)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("ReadFile(canceled) error = %v, want context.Canceled", err)
			}
		})
	}
}
