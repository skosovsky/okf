package mcpserver

import (
	"strconv"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestReadConceptProjectionCollectionCapsAcceptBoundary(t *testing.T) {
	// Arrange.
	collections := readConceptProjectionCollections{
		Tags:         make([]string, maxReadConceptProjectionItems),
		Sources:      make([]bundle.ProvenanceSource, maxReadConceptProjectionItems),
		Verified:     make([]bundle.Verification, maxReadConceptProjectionItems),
		Attributions: make([]bundle.Attribution, maxReadConceptProjectionItems),
		Computation: &bundle.AttestedComputationContract{
			Parameters: make([]bundle.ComputationParameter, maxReadConceptComputationItems),
			Executor: &bundle.ExecutorContract{
				Receipt: make([]string, maxReadConceptComputationItems),
			},
		},
	}
	collections.Attributions[0].Sources = make(
		[]bundle.ProvenanceSource,
		maxReadConceptProjectionItems,
	)
	collections.Attributions[0].References = make(
		[]bundle.FootnoteReference,
		maxReadConceptProjectionItems,
	)
	collections.Attributions[0].Definitions = make(
		[]bundle.FootnoteDefinition,
		maxReadConceptProjectionItems,
	)

	// Act.
	result := validateReadConceptProjectionCapsContext(t.Context(), collections)

	// Assert.
	if result != nil {
		t.Fatalf("boundary collections returned error: %s", resultText(t, result))
	}
}

func TestReadConceptProjectionCollectionCapsRejectPlusOne(t *testing.T) {
	tests := []struct {
		name        string
		collections readConceptProjectionCollections
	}{
		{
			name: "tags",
			collections: readConceptProjectionCollections{
				Tags: make([]string, maxReadConceptProjectionItems+1),
			},
		},
		{
			name: "sources",
			collections: readConceptProjectionCollections{
				Sources: make([]bundle.ProvenanceSource, maxReadConceptProjectionItems+1),
			},
		},
		{
			name: "attributions",
			collections: readConceptProjectionCollections{
				Attributions: make([]bundle.Attribution, maxReadConceptProjectionItems+1),
			},
		},
		{
			name: "attribution sources",
			collections: readConceptProjectionCollections{
				Attributions: []bundle.Attribution{{
					Sources: make([]bundle.ProvenanceSource, maxReadConceptProjectionItems+1),
				}},
			},
		},
		{
			name: "attribution references",
			collections: readConceptProjectionCollections{
				Attributions: []bundle.Attribution{{
					References: make([]bundle.FootnoteReference, maxReadConceptProjectionItems+1),
				}},
			},
		},
		{
			name: "attribution definitions",
			collections: readConceptProjectionCollections{
				Attributions: []bundle.Attribution{{
					Definitions: make([]bundle.FootnoteDefinition, maxReadConceptProjectionItems+1),
				}},
			},
		},
		{
			name: "verified",
			collections: readConceptProjectionCollections{
				Verified: make([]bundle.Verification, maxReadConceptProjectionItems+1),
			},
		},
		{
			name: "computation parameters",
			collections: readConceptProjectionCollections{
				Computation: &bundle.AttestedComputationContract{
					Parameters: make(
						[]bundle.ComputationParameter,
						maxReadConceptComputationItems+1,
					),
				},
			},
		},
		{
			name: "executor receipt",
			collections: readConceptProjectionCollections{
				Computation: &bundle.AttestedComputationContract{
					Executor: &bundle.ExecutorContract{
						Receipt: make([]string, maxReadConceptComputationItems+1),
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			collections := test.collections

			// Act.
			result := validateReadConceptProjectionCapsContext(t.Context(), collections)

			// Assert.
			assertSchemaErrorEnvelope(t, result, "resource_limit")
		})
	}
}

func TestReadConceptHandlerRejectsProjectionCollectionPlusOne(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	var frontmatter strings.Builder
	frontmatter.WriteString("---\ntype: Note\ntags:\n")
	for index := 0; index < maxReadConceptProjectionItems+1; index++ {
		frontmatter.WriteString("  - tag-")
		frontmatter.WriteString(strconv.Itoa(index))
		frontmatter.WriteByte('\n')
	}
	frontmatter.WriteString("---\nBody.\n")
	writeTestFile(t, root, "a.md", frontmatter.String())

	// Act.
	result := callHandler(t, handleReadConcept, map[string]any{
		"bundle_path": root,
		"concept_id":  "a",
	})

	// Assert.
	assertSchemaErrorEnvelope(t, result, "resource_limit")
}
