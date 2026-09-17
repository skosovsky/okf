package bundle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestConceptPathValueObservationsContextPreservesExactShapeAndScalarProvenance(t *testing.T) {
	t.Parallel()

	// Arrange.
	id := mustParseConceptID(t, "observed")
	frontmatter, err := ParseFrontmatter(`
resource_defaults: &resource_defaults
  resource: inherited.sql
sources:
  - resource: direct.sql
  - resource: 17
  - title: missing-resource
  - malformed-parent
  - <<: *resource_defaults
computation: ""
executor: malformed-parent
attester:
  resource: https://example.com/attestation.json
`)
	if err != nil {
		t.Fatal(err)
	}
	loaded := &Bundle{
		concepts: []Concept{{
			ID:       id,
			Document: NewDocument(frontmatter, ""),
		}},
		byID: map[string]int{id.String(): 0},
	}
	want := ConceptPathValueObservations{
		SourcesPresent:  true,
		SourcesSequence: true,
		Values: []ConceptPathValueObservation{
			{
				Field:         PathFieldSourceResource,
				FieldPath:     "sources[0].resource",
				ParentPresent: true,
				ParentMapping: true,
				Scalar:        ScalarValueState{Present: true, Valid: true, Raw: "direct.sql", Value: "direct.sql"},
			},
			{
				Field:         PathFieldSourceResource,
				FieldPath:     "sources[1].resource",
				ParentPresent: true,
				ParentMapping: true,
				Scalar:        ScalarValueState{Present: true, Raw: "17"},
			},
			{
				Field:         PathFieldSourceResource,
				FieldPath:     "sources[2].resource",
				ParentPresent: true,
				ParentMapping: true,
			},
			{
				Field:         PathFieldSourceResource,
				FieldPath:     "sources[3].resource",
				ParentPresent: true,
			},
			{
				Field:         PathFieldSourceResource,
				FieldPath:     "sources[4].resource",
				ParentPresent: true,
				ParentMapping: true,
				Scalar:        ScalarValueState{Present: true, Valid: true, Raw: "inherited.sql", Value: "inherited.sql"},
			},
			{
				Field:     PathFieldComputation,
				FieldPath: "computation",
				Scalar:    ScalarValueState{Present: true},
			},
			{
				Field:         PathFieldExecutorResource,
				FieldPath:     "executor.resource",
				ParentPresent: true,
			},
			{
				Field:         PathFieldAttesterResource,
				FieldPath:     "attester.resource",
				ParentPresent: true,
				ParentMapping: true,
				Scalar: ScalarValueState{
					Present: true,
					Valid:   true,
					Raw:     "https://example.com/attestation.json",
					Value:   "https://example.com/attestation.json",
				},
			},
		},
	}

	// Act.
	got, found, err := loaded.ConceptPathValueObservationsContext(context.Background(), id)

	// Assert.
	if err != nil || !found {
		t.Fatalf("ConceptPathValueObservationsContext() = (%#v, %v, %v)", got, found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ConceptPathValueObservationsContext() = %#v, want %#v", got, want)
	}

	got.Values[0].FieldPath = "mutated"
	got.Values[0].Scalar.Raw = "mutated"
	got.Values[0].Scalar.Value = "mutated"
	again, found, err := loaded.ConceptPathValueObservationsContext(context.Background(), id)
	if err != nil || !found || !reflect.DeepEqual(again, want) {
		t.Fatalf("caller mutation reached retained frontmatter: got (%#v, %v, %v)", again, found, err)
	}
}

func TestConceptPathValueObservationsContextPreservesMalformedSourcesFamily(t *testing.T) {
	t.Parallel()

	// Arrange.
	id := mustParseConceptID(t, "malformed-sources")
	frontmatter, err := ParseFrontmatter(`
sources: malformed-sequence
executor: {}
`)
	if err != nil {
		t.Fatal(err)
	}
	loaded := &Bundle{
		concepts: []Concept{{ID: id, Document: NewDocument(frontmatter, "")}},
		byID:     map[string]int{id.String(): 0},
	}

	// Act.
	got, found, err := loaded.ConceptPathValueObservationsContext(context.Background(), id)

	// Assert.
	if err != nil || !found {
		t.Fatalf("ConceptPathValueObservationsContext() = (%#v, %v, %v)", got, found, err)
	}
	if !got.SourcesPresent || got.SourcesSequence {
		t.Fatalf("sources shape = (present=%v, sequence=%v)", got.SourcesPresent, got.SourcesSequence)
	}
	want := []ConceptPathValueObservation{
		{Field: PathFieldComputation, FieldPath: "computation"},
		{
			Field:         PathFieldExecutorResource,
			FieldPath:     "executor.resource",
			ParentPresent: true,
			ParentMapping: true,
		},
		{Field: PathFieldAttesterResource, FieldPath: "attester.resource"},
	}
	if !reflect.DeepEqual(got.Values, want) {
		t.Fatalf("Values = %#v, want %#v", got.Values, want)
	}
}

func TestConceptPathValueObservationsContextHonorsCancellationAndUnknownParity(t *testing.T) {
	t.Parallel()

	// Arrange.
	id := mustParseConceptID(t, "known")
	unknown := mustParseConceptID(t, "unknown")
	loaded := &Bundle{
		concepts: []Concept{{ID: id, Document: NewDocument(NewFrontmatter(), "")}},
		byID:     map[string]int{id.String(): 0},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act and assert.
	if got, found, err := loaded.ConceptPathValueObservationsContext(canceled, id); !reflect.DeepEqual(got, ConceptPathValueObservations{}) || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled observation = (%#v, %v, %v)", got, found, err)
	}
	if got, found, err := loaded.ConceptPathValueObservationsContext(context.Background(), unknown); !reflect.DeepEqual(got, ConceptPathValueObservations{}) || found || err != nil {
		t.Fatalf("unknown observation = (%#v, %v, %v)", got, found, err)
	}
	var nilBundle *Bundle
	if got, found, err := nilBundle.ConceptPathValueObservationsContext(context.Background(), id); !reflect.DeepEqual(got, ConceptPathValueObservations{}) || found || err != nil {
		t.Fatalf("nil bundle observation = (%#v, %v, %v)", got, found, err)
	}
}

func TestConceptPathValueObservationsContextCancelsDuringHighCardinalitySources(t *testing.T) {
	t.Parallel()

	// Arrange.
	const sourceCount = 4096
	var yamlText strings.Builder
	yamlText.WriteString("sources:\n")
	for index := range sourceCount {
		fmt.Fprintf(&yamlText, "  - resource: source-%04d.sql\n", index)
	}
	frontmatter, err := ParseFrontmatter(yamlText.String())
	if err != nil {
		t.Fatal(err)
	}
	id := mustParseConceptID(t, "many-sources")
	loaded := &Bundle{
		concepts: []Concept{{ID: id, Document: NewDocument(frontmatter, "")}},
		byID:     map[string]int{id.String(): 0},
	}
	ctx := &cancelAfterErrChecksContext{allowed: 1024}

	// Act.
	got, found, err := loaded.ConceptPathValueObservationsContext(ctx, id)

	// Assert.
	if !reflect.DeepEqual(got, ConceptPathValueObservations{}) || found || !errors.Is(err, context.Canceled) {
		t.Fatalf("high-cardinality observation = (%#v, %v, %v), want zero canceled result", got, found, err)
	}
	if checks := ctx.checks.Load(); checks <= 3 || checks >= 2*sourceCount {
		t.Fatalf("cancellation checks = %d, want cancellation during source traversal", checks)
	}
}
