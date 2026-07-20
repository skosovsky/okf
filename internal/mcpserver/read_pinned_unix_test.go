//go:build unix

package mcpserver

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/sys/unix"
)

// TestReadPinnedConceptRejectsFinalSpecialFileSwaps exercises the exact
// Lstat-to-open window synchronously. The timeout is an assertion that the
// reader cannot hang; cleanup releases a FIFO reader in case of regression so
// the test itself never leaves a goroutine behind.
func TestReadPinnedConceptRejectsFinalSpecialFileSwaps(t *testing.T) {
	for _, tc := range []struct {
		name string
		swap func(root, path string) (func(), error)
	}{
		{
			name: "fifo",
			swap: func(_, path string) (func(), error) {
				if err := unix.Mkfifo(path, 0o600); err != nil {
					return nil, err
				}
				return func() {
					fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
					if err == nil {
						_ = unix.Close(fd)
					}
				}, nil
			},
		},
		{
			name: "symlink",
			swap: func(root, path string) (func(), error) {
				target := filepath.Join(root, "target.md")
				if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
					return nil, err
				}
				if err := os.Symlink(target, path); err != nil {
					return nil, err
				}
				return func() {}, nil
			},
		},
		{
			name: "socket",
			swap: func(_, path string) (func(), error) {
				listener, err := net.Listen("unix", path)
				if err != nil {
					return nil, err
				}
				return func() { _ = listener.Close() }, nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempBase := ""
			if runtime.GOOS == "darwin" {
				tempBase = "/private/tmp"
			}
			root, err := os.MkdirTemp(tempBase, "okf-mcp-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nsafe\n")
			path := filepath.Join(root, "a.md")
			var release func()
			var swapErr error
			var swapMu sync.Mutex
			readPinnedConceptBeforeOpen = func() {
				swapMu.Lock()
				defer swapMu.Unlock()
				if err := os.Remove(path); err != nil {
					swapErr = err
					return
				}
				release, swapErr = tc.swap(root, path)
			}
			t.Cleanup(func() {
				readPinnedConceptBeforeOpen = nil
				swapMu.Lock()
				defer swapMu.Unlock()
				if release != nil {
					release()
				}
			})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			resultCh := make(chan *mcp.CallToolResult, 1)
			go func() {
				result, _ := handleReadConcept(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{
					"bundle_path": root,
					"concept_id":  "a",
				}}})
				resultCh <- result
			}()

			select {
			case result := <-resultCh:
				swapMu.Lock()
				err := swapErr
				swapMu.Unlock()
				if err != nil {
					t.Fatalf("swap: %v", err)
				}
				if result == nil || !result.IsError {
					t.Fatalf("read_concept result = %#v, want an error", result)
				}
			case <-ctx.Done():
				swapMu.Lock()
				if release != nil {
					release()
					release = nil
				}
				swapMu.Unlock()
				select {
				case <-resultCh:
				case <-time.After(time.Second):
					t.Fatal("read_concept leaked after special-file swap")
				}
				t.Fatal("read_concept blocked on special-file swap")
			}
		})
	}
}

func TestReadPinnedConceptHonorsCanceledContext(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "safe")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := readPinnedConcept(ctx, root, "a.md")
	if err != context.Canceled {
		t.Fatalf("readPinnedConcept() error = %v, want context.Canceled", err)
	}
}
