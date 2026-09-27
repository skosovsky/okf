package main

import (
	"path/filepath"
	"testing"
)

func TestArtifactOutsideBundle(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	inside := filepath.Join(root, "checkpoint.json")
	outside := filepath.Join(t.TempDir(), "checkpoint.json")
	// Act.
	insideErr := artifactOutsideBundle(root, inside)
	outsideErr := artifactOutsideBundle(root, outside)
	// Assert.
	if insideErr == nil || outsideErr != nil {
		t.Fatalf("inside=%v outside=%v", insideErr, outsideErr)
	}
}
