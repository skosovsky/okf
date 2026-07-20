//go:build (!darwin && !linux) || ios || android

package fs

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
)

func TestOpenContextReturnsUnsupportedPlatform(t *testing.T) {
	// Arrange.
	ctx := context.Background()

	// Act.
	got, err := OpenContext(ctx, t.TempDir(), DefaultConfig())

	// Assert.
	if got != nil {
		t.Fatalf("OpenContext() store = %#v, want nil", got)
	}
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("OpenContext() error = %v, want ErrUnsupportedPlatform", err)
	}
	var detail *UnsupportedPlatformError
	if !errors.As(err, &detail) || detail.GOOS != runtime.GOOS || detail.GOARCH != runtime.GOARCH {
		t.Fatalf("OpenContext() detail = %#v, want %s/%s", detail, runtime.GOOS, runtime.GOARCH)
	}
}

func TestOpenReturnsUnsupportedPlatformWithoutTouchingRoot(t *testing.T) {
	// Arrange. The path must remain nonexistent; platform rejection precedes
	// path resolution and filesystem initialization.
	root := t.TempDir() + "/does-not-exist"

	// Act.
	got, err := Open(root, DefaultConfig())

	// Assert.
	if got != nil {
		t.Fatalf("Open() store = %#v, want nil", got)
	}
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedPlatform", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Open() touched root: os.Stat() error = %v, want os.ErrNotExist", statErr)
	}
}

func TestOpenContextCancellationPrecedesPlatformCheck(t *testing.T) {
	// Arrange.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	got, err := OpenContext(ctx, ".", DefaultConfig())

	// Assert.
	if got != nil {
		t.Fatalf("OpenContext() store = %#v, want nil", got)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext() error = %v, want context.Canceled", err)
	}
}
