package bundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootComponentOwnerCancelsBeforeFilesystemOpen(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	name := strings.Repeat("segment/", 1<<17) + "leaf"
	probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
	_, probeErr := openDirectoryContext(probe, root, name)
	checks := probe.checks.Load()
	if probeErr == nil || checks < 32 {
		t.Fatalf("probe err=%v checks=%d", probeErr, checks)
	}
	_, gotErr := openDirectoryContext(&cancelAfterErrChecksContext{allowed: checks / 2}, root, name)
	if !errors.Is(gotErr, context.Canceled) {
		t.Fatalf("error=%v", gotErr)
	}
}

func TestSourceRootOpenRootContextPreCanceledReturnsZero(t *testing.T) {
	source := &FileSystemSource{Root: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root, err := source.openRootContext(ctx)
	if root != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("openRoot=(%v,%v)", root, err)
	}
}

func TestSourceRootBackgroundRootTraversalParity(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "a", "b")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy, legacyErr := openRootWithoutSymlinks(directory)
	if legacy != nil {
		defer legacy.Close()
	}
	got, gotErr := openRootWithoutSymlinksContext(context.Background(), directory)
	if got != nil {
		defer got.Close()
	}
	if (legacyErr == nil) != (gotErr == nil) {
		t.Fatalf("errors=%v/%v", legacyErr, gotErr)
	}
}
