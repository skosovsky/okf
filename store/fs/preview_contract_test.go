package fs

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/store"
)

func TestStorePreviewContract_ProjectsInformationalRelationDiagnostics(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: thing\nparts:\n  - id: canonical\n    anchor: legacy\n---\nA\n")
	writeTestFile(t, root, "b.md", "---\ntype: thing\n---\nB\n")
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, s)
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "preview-alias", Actor: "tester", BaseRevision: base.Revision(), Operations: []store.Operation{store.EnsureRelation{Source: adversarialRef(t, "a#canonical"), Type: "uses", Target: adversarialRef(t, "b")}}}

	// Act.
	preview, err := s.Preview(context.Background(), change)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range preview.Diagnostics {
		if diagnostic.Kind == store.DiagnosticRelation && diagnostic.Severity == store.DiagnosticInfo && diagnostic.Code == "anchor_alias" {
			return
		}
	}
	t.Fatalf("Preview diagnostics = %#v, want informational anchor_alias", preview.Diagnostics)
}

func TestStorePreviewContract_ReturnsDiagnosticPreviewAndRevisionDependencies(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "a.md", "---\ntype: thing\n---\nA\n")
	writeTestFile(t, root, "b.md", "---\ntype: thing\nrelations:\n  uses:\n    - target: target#part\n---\nB\n")
	writeTestFile(t, root, "target.md", "---\ntype: thing\nparts:\n  - id: part\n  - id: part\n---\nTarget\n")
	writeTestFile(t, root, "asset.bin", "revision-visible but semantically unread")
	s, err := Open(root, Config{})
	if err != nil {
		t.Fatal(err)
	}
	base := adversarialSnapshot(t, s)
	change := store.ChangeSet{Version: store.ChangeSetFormatVersion, ID: "preview-invalid", Actor: "tester", BaseRevision: base.Revision(), Operations: []store.Operation{store.EnsureRelation{Source: adversarialRef(t, "a"), Type: "uses", Target: adversarialRef(t, "b")}}}

	// Act.
	preview, err := s.Preview(context.Background(), change)

	// Assert.
	var invalid *store.InvalidChangeSet
	if !errors.As(err, &invalid) || invalid.Code != "staged_validation_failed" {
		t.Fatalf("Preview() error = %#v", err)
	}
	paths := make([]string, len(preview.Reads))
	for i, read := range preview.Reads {
		paths[i] = read.Path
	}
	if want := []string{"a.md", "asset.bin", "b.md", "target.md"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("Preview reads = %#v, want %#v", paths, want)
	}
	if len(preview.Writes) != 0 || len(preview.Deletes) != 0 || len(preview.Renames) != 0 || len(preview.Plan) != 0 {
		t.Fatalf("invalid preview exposed commit-ready state: %#v", preview)
	}
	if !containsDiagnostic(preview.Diagnostics, "missing_or_ambiguous_target_fragment", "b.md", "uses", "target#part", "b") {
		t.Fatalf("preview diagnostics lost relation context: %#v", preview.Diagnostics)
	}
	if !reflect.DeepEqual(invalid.Diagnostics, preview.Diagnostics) {
		t.Fatalf("error diagnostics = %#v, want preview diagnostics %#v", invalid.Diagnostics, preview.Diagnostics)
	}
}

func containsDiagnostic(diagnostics []store.Diagnostic, code, file, relationType, rawTarget, ref string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind == store.DiagnosticRelation && diagnostic.Severity == store.DiagnosticError && diagnostic.Code == code && diagnostic.File == file && diagnostic.RelationType == relationType && diagnostic.RawTarget == rawTarget && len(diagnostic.Refs) == 1 && diagnostic.Refs[0].String() == ref {
			return true
		}
	}
	return false
}
