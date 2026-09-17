package store

import (
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestProjectRelationDiagnosticsKeepsDelimiterCollidingTuplesDistinct(t *testing.T) {
	// Arrange.
	source, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	first := bundle.RelationDiagnostic{
		Code:         "code",
		File:         "file\x00tail",
		RelationType: "uses",
		RawTarget:    "target",
		Message:      "message",
		Source:       source,
	}
	second := first
	second.Code = "code\x00file"
	second.File = "tail"
	firstLegacyKey := first.Code + "\x00" + first.File
	secondLegacyKey := second.Code + "\x00" + second.File
	if firstLegacyKey != secondLegacyKey {
		t.Fatal("adversarial fixture does not collide under delimiter concatenation")
	}

	// Act.
	got := ProjectRelationDiagnostics([]bundle.RelationDiagnostic{second, first, first})

	// Assert.
	if len(got) != 2 {
		t.Fatalf("diagnostic count = %d, want 2 distinct tuples with one exact duplicate removed", len(got))
	}
	if got[0].Code != first.Code || got[0].File != first.File {
		t.Fatalf("first diagnostic = %#v, want code %q file %q", got[0], first.Code, first.File)
	}
	if got[1].Code != second.Code || got[1].File != second.File {
		t.Fatalf("second diagnostic = %#v, want code %q file %q", got[1], second.Code, second.File)
	}
}

func TestProjectRelationDiagnosticsKeepsStructuralRefTuplesDistinct(t *testing.T) {
	// Arrange.
	embeddedDelimiter, err := bundle.NewConceptID([]string{"source#part"})
	if err != nil {
		t.Fatal(err)
	}
	separateFragment, err := bundle.ParseConceptID("source")
	if err != nil {
		t.Fatal(err)
	}
	first := bundle.RelationDiagnostic{Code: "code", Message: "message", Source: embeddedDelimiter}
	second := bundle.RelationDiagnostic{Code: "code", Message: "message", Source: separateFragment, SourceFragment: "part"}
	firstRef := bundle.RelationRef{ID: first.Source, Fragment: first.SourceFragment}
	secondRef := bundle.RelationRef{ID: second.Source, Fragment: second.SourceFragment}
	if firstRef.String() == secondRef.String() {
		t.Fatal("escaped relation-ref grammar did not distinguish the structural tuples")
	}

	// Act.
	got := ProjectRelationDiagnostics([]bundle.RelationDiagnostic{second, first, first})

	// Assert.
	if len(got) != 2 {
		t.Fatalf("diagnostic count = %d, want 2 distinct ref tuples with one exact duplicate removed", len(got))
	}
	if got[0].Refs[0].ID.String() != "source" || got[0].Refs[0].Fragment != "part" {
		t.Fatalf("first diagnostic ref = %#v, want separate fragment tuple", got[0].Refs[0])
	}
	if got[1].Refs[0].ID.String() != "source#part" || got[1].Refs[0].Fragment != "" {
		t.Fatalf("second diagnostic ref = %#v, want embedded delimiter tuple", got[1].Refs[0])
	}
}
