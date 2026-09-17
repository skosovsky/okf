//go:build (!darwin && !linux && !windows) || ios || android

package bundle

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestOpenDocumentSessionMapsUnsupportedLockToDocumentConflict(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	target := filepath.Join(root, "concept.md")
	documentSessionWriteFile(t, target, documentSessionConcept("concept", "before"))
	before := documentSessionCaptureTree(t, root)

	// Act.
	session, err := OpenDocumentSessionContext(context.Background(), target, "")
	if session != nil {
		_ = session.Close()
	}

	// Assert.
	if session != nil {
		t.Fatal("OpenDocumentSessionContext() returned a session")
	}
	if !errors.Is(err, ErrDocumentConflict) {
		t.Fatalf("OpenDocumentSessionContext() error = %v, want ErrDocumentConflict", err)
	}
	if !errors.Is(err, ErrPublicationCapabilityUnsupported) {
		t.Fatalf("OpenDocumentSessionContext() error = %v, want ErrPublicationCapabilityUnsupported", err)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, root))
	documentSessionAssertNoPublicationArtifacts(t, root)
}

func TestRegenerateIndexesRejectsUnsupportedCapabilityBeforeMutation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeIndexDoc(t, root, "nested/a.md", "Note", "A", "Alpha.")
	before := documentSessionCaptureTree(t, root)

	// Act.
	written, err := RegenerateIndexes(root)

	// Assert.
	if written != nil || !errors.Is(err, ErrPublicationCapabilityUnsupported) {
		t.Fatalf("RegenerateIndexes() = %#v, %v; want capability failure", written, err)
	}
	documentSessionAssertTreeSnapshotEqual(t, before, documentSessionCaptureTree(t, root))
	documentSessionAssertNoPublicationArtifacts(t, root)
}
