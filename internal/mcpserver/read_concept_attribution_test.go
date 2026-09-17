package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestReadConceptProjectionAttributionsAreTypedClosedAndNonNull(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeTestFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n- [A](a.md)\n")
	writeTestFile(t, root, "a.md", "---\ntype: Note\n---\nNo provenance.\n")

	// Act.
	result := callHandler(t, handleReadConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "a",
	})
	if result.IsError {
		t.Fatalf("read_concept returned error: %s", resultText(t, result))
	}
	projected := result.StructuredContent.(readConceptStructured)
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// Assert.
	if projected.Projection.Attributions == nil || len(projected.Projection.Attributions) != 0 {
		t.Fatalf("attributions = %#v, want non-nil empty array", projected.Projection.Attributions)
	}
	if got := wire["projection"].(map[string]any)["attributions"]; got == nil {
		t.Fatal("wire attributions is null, want []")
	}
	if err := validateContract("read_concept.output", wire); err != nil {
		t.Fatalf("typed read_concept output violates contract: %v", err)
	}

	wire["projection"].(map[string]any)["attributions"] = []any{map[string]any{
		"id":            "source",
		"normalized_id": "source",
		"sources":       []any{},
		"references":    []any{},
		"definitions":   []any{},
		"unknown":       true,
	}}
	if err := validateContract("read_concept.output", wire); err == nil {
		t.Fatal("closed attribution schema accepted an unknown property")
	}
}

func TestProjectDocumentAttributionsRetainKeyedUnknownAndDuplicateObservations(t *testing.T) {
	t.Parallel()

	// Arrange.
	document, err := bundle.ParseDocument(`---
type: Note
sources:
  - {id: spec, resource: urn:test:z}
  - {id: Spec, resource: urn:test:a}
  - {id: source-only, resource: urn:test:only}
---
Known.[^spec] Repeated.[^Spec] Unknown.[^missing]

[^spec]: first definition
[^Spec]: second definition
[^missing]: unknown definition
`)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}

	// Act.
	projection, result := projectDocumentContext(t.Context(), document)
	if result != nil {
		t.Fatalf("projectDocument() returned error: %s", resultText(t, result))
	}

	// Assert.
	if len(projection.Attributions) != 3 {
		t.Fatalf("attributions = %#v, want missing/source-only/spec groups", projection.Attributions)
	}
	byNormalizedID := make(map[string]attributionDTO, len(projection.Attributions))
	for _, attribution := range projection.Attributions {
		byNormalizedID[attribution.NormalizedID] = attribution
		if attribution.Sources == nil || attribution.References == nil || attribution.Definitions == nil {
			t.Fatalf("attribution contains null-capable arrays: %#v", attribution)
		}
	}

	known := byNormalizedID["spec"]
	if known.ID != "Spec" ||
		len(known.Sources) != 2 ||
		known.Sources[0].ID != "Spec" ||
		known.Sources[1].ID != "spec" ||
		len(known.References) != 2 ||
		known.References[0].ID != "Spec" ||
		known.References[1].ID != "spec" ||
		len(known.Definitions) != 2 ||
		known.Definitions[0].ID != "Spec" ||
		known.Definitions[1].ID != "spec" {
		t.Fatalf("duplicate keyed observations were collapsed or rewritten: %#v", known)
	}

	unknown := byNormalizedID["missing"]
	if unknown.ID != "missing" ||
		len(unknown.Sources) != 0 ||
		len(unknown.References) != 1 ||
		len(unknown.Definitions) != 1 {
		t.Fatalf("unknown keyed attribution was hidden: %#v", unknown)
	}

	sourceOnly := byNormalizedID["source-only"]
	if len(sourceOnly.Sources) != 1 ||
		len(sourceOnly.References) != 0 ||
		len(sourceOnly.Definitions) != 0 {
		t.Fatalf("source-only attribution was hidden: %#v", sourceOnly)
	}
}
