package bundle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestSemanticFamilyObservationRestrictedFamilyMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		field      string
		valid      string
		alternate  string
		wrong      string
		shape      YAMLValueShape
		wrongShape YAMLValueShape
	}{
		{
			field: "usage_window", valid: "{from: 2026-01-01, to: 2026-01-31}",
			alternate: "{from: 2026-02-01, to: 2026-02-28}",
			wrong:     "wrong", shape: YAMLShapeMapping, wrongShape: YAMLShapeScalar,
		},
		{
			field:     "generated",
			valid:     "{by: 'process:test', at: 2026-01-01T00:00:00Z}",
			alternate: "{by: 'process:other', at: 2026-01-02T00:00:00Z}",
			wrong:     "wrong", shape: YAMLShapeMapping, wrongShape: YAMLShapeScalar,
		},
		{
			field:     "verified",
			valid:     "{by: 'process:test', at: 2026-01-01T00:00:00Z}",
			alternate: "{by: 'process:other', at: 2026-01-02T00:00:00Z}",
			wrong:     "wrong", shape: YAMLShapeMapping, wrongShape: YAMLShapeScalar,
		},
		{
			field: "sources", valid: "[]", alternate: "[{resource: source.md}]",
			wrong: "wrong", shape: YAMLShapeSequence, wrongShape: YAMLShapeScalar,
		},
		{
			field: "status", valid: "stable", alternate: "deprecated",
			wrong: "{value: wrong}", shape: YAMLShapeScalar, wrongShape: YAMLShapeMapping,
		},
		{
			field: "parameters", valid: "[]",
			alternate: "[{name: value, type: string, required: true}]",
			wrong:     "wrong", shape: YAMLShapeSequence, wrongShape: YAMLShapeScalar,
		},
		{
			field: "executor", valid: "{resource: run.md, receipt: [result]}",
			alternate: "{resource: other.md, receipt: [other]}",
			wrong:     "wrong", shape: YAMLShapeMapping, wrongShape: YAMLShapeScalar,
		},
		{
			field: "attester", valid: "{resource: check.py}",
			alternate: "{resource: other.py}",
			wrong:     "wrong", shape: YAMLShapeMapping, wrongShape: YAMLShapeScalar,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.field, func(t *testing.T) {
			t.Parallel()

			t.Run("absent", func(t *testing.T) {
				t.Parallel()

				// Arrange.
				frontmatter := NewFrontmatter()

				// Act.
				got, err := frontmatter.SemanticFamilyObservation(test.field)

				// Assert.
				want := SemanticFamilyObservation{
					Shape:         YAMLShapeAbsent,
					ResolvedShape: YAMLShapeAbsent,
					ShapeValid:    true,
				}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("SemanticFamilyObservation(%q) = (%#v, %v), want %#v",
						test.field, got, err, want)
				}
			})

			t.Run("valid", func(t *testing.T) {
				t.Parallel()
				assertSemanticFamilyCase(
					t,
					test.field+": "+test.valid+"\n",
					test.field,
					func(got SemanticFamilyObservation) bool {
						return got.Present && !got.Ambiguous &&
							got.Shape == test.shape && got.ResolvedShape == test.shape &&
							got.ShapeValid && got.Raw != "" && got.ResolvedRaw != "" &&
							!got.ViaAlias && !got.ViaMerge
					},
				)
			})

			t.Run("wrong shape", func(t *testing.T) {
				t.Parallel()
				assertSemanticFamilyCase(
					t,
					test.field+": "+test.wrong+"\n",
					test.field,
					func(got SemanticFamilyObservation) bool {
						return got.Present && !got.Ambiguous &&
							got.Shape == test.wrongShape &&
							got.ResolvedShape == test.wrongShape &&
							!got.ShapeValid && got.Raw != "" && got.ResolvedRaw != ""
					},
				)
			})

			t.Run("direct duplicate", func(t *testing.T) {
				t.Parallel()
				assertSemanticFamilyCase(
					t,
					test.field+": "+test.valid+"\n"+
						test.field+": "+test.alternate+"\n",
					test.field,
					isDirectAmbiguousFamily,
				)
			})

			t.Run("duplicate merge", func(t *testing.T) {
				t.Parallel()
				assertSemanticFamilyCase(
					t,
					"left: &left {"+test.field+": "+test.valid+"}\n"+
						"right: &right {"+test.field+": "+test.alternate+"}\n"+
						"<<: *left\n<<: *right\n",
					test.field,
					func(got SemanticFamilyObservation) bool {
						return isAmbiguousFamily(got) && got.ViaAlias && got.ViaMerge
					},
				)
			})

			t.Run("alias", func(t *testing.T) {
				t.Parallel()
				assertSemanticFamilyCase(
					t,
					"family_value: &family_value "+test.valid+"\n"+
						test.field+": *family_value\n",
					test.field,
					func(got SemanticFamilyObservation) bool {
						return got.Present && !got.Ambiguous &&
							got.Shape == YAMLShapeAlias &&
							got.ResolvedShape == test.shape && got.ShapeValid &&
							got.Raw == "*family_value" && got.ResolvedRaw != "" &&
							got.ViaAlias && !got.ViaMerge
					},
				)
			})

		})
	}
}

func TestSemanticFamilyObservationAcceptsBothVerifiedShapes(t *testing.T) {
	t.Parallel()

	for name, value := range map[string]string{
		"mapping":  "{by: 'process:test', at: 2026-01-01T00:00:00Z}",
		"sequence": "[{by: 'process:test', at: 2026-01-01T00:00:00Z}]",
	} {
		name, value := name, value
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			frontmatter, err := ParseFrontmatter("verified: " + value + "\n")
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			got, err := frontmatter.SemanticFamilyObservation("verified")

			// Assert.
			if err != nil || !got.Present || got.Ambiguous || !got.ShapeValid {
				t.Fatalf("verified %s = (%#v, %v)", name, got, err)
			}
		})
	}
}

func TestSemanticFamilyObservationRejectsUnknownBeforeTraversalAndCancelsFailClosed(t *testing.T) {
	t.Parallel()

	// Arrange.
	frontmatter, err := ParseFrontmatter("x-extension: retained\n")
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	// Act.
	unknown, unknownErr := frontmatter.SemanticFamilyObservation("x-extension")
	unknownCanceled, unknownCanceledErr := frontmatter.SemanticFamilyObservationContext(
		canceled,
		"x-extension",
	)
	knownCanceled, knownCanceledErr := frontmatter.SemanticFamilyObservationContext(
		canceled,
		"sources",
	)

	// Assert.
	if unknown != (SemanticFamilyObservation{}) ||
		!errors.Is(unknownErr, ErrUnknownSemanticFamily) {
		t.Fatalf("unknown = (%#v, %v)", unknown, unknownErr)
	}
	if unknownCanceled != (SemanticFamilyObservation{}) ||
		!errors.Is(unknownCanceledErr, ErrUnknownSemanticFamily) {
		t.Fatalf("unknown canceled precedence = (%#v, %v)", unknownCanceled, unknownCanceledErr)
	}
	if knownCanceled != (SemanticFamilyObservation{}) ||
		!errors.Is(knownCanceledErr, context.Canceled) {
		t.Fatalf("known canceled = (%#v, %v)", knownCanceled, knownCanceledErr)
	}
}

func TestSemanticFamilyObservationContextReturnsNoPartialProjection(t *testing.T) {
	t.Parallel()

	// Arrange.
	var source strings.Builder
	source.WriteString("sources:\n")
	for index := 0; index < 512; index++ {
		fmt.Fprintf(&source, "  - {id: source-%04d, resource: source.md}\n", index)
	}
	frontmatter, err := ParseFrontmatter(source.String())
	if err != nil {
		t.Fatal(err)
	}
	want, err := frontmatter.SemanticFamilyObservation("sources")
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	got, err := frontmatter.SemanticFamilyObservationContext(
		&cancelAfterErrChecksContext{allowed: 4},
		"sources",
	)
	again, againErr := frontmatter.SemanticFamilyObservation("sources")

	// Assert.
	if got != (SemanticFamilyObservation{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-canceled = (%#v, %v)", got, err)
	}
	if againErr != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("repeat = (%#v, %v), want %#v", again, againErr, want)
	}
}

func assertSemanticFamilyCase(
	t *testing.T,
	source string,
	field string,
	valid func(SemanticFamilyObservation) bool,
) {
	t.Helper()

	// Arrange.
	frontmatter, err := ParseFrontmatter(source)
	if err != nil {
		t.Fatal(err)
	}

	// Act.
	got, gotErr := frontmatter.SemanticFamilyObservation(field)
	contextGot, contextErr := frontmatter.SemanticFamilyObservationContext(
		context.Background(),
		field,
	)

	// Assert.
	if gotErr != nil || contextErr != nil || !reflect.DeepEqual(got, contextGot) ||
		!valid(got) {
		t.Fatalf("observation %q = (%#v, %v), context=(%#v, %v)",
			field, got, gotErr, contextGot, contextErr)
	}
}

func isDirectAmbiguousFamily(got SemanticFamilyObservation) bool {
	return isAmbiguousFamily(got) && got.Shape == YAMLShapeAmbiguous &&
		!got.ViaAlias && !got.ViaMerge
}

func isAmbiguousFamily(got SemanticFamilyObservation) bool {
	return got.Present && got.Ambiguous &&
		(got.Shape == YAMLShapeAmbiguous || got.Shape == YAMLShapeAlias) &&
		got.ResolvedShape == YAMLShapeAmbiguous &&
		!got.ShapeValid && got.Raw == "" && got.ResolvedRaw == ""
}
