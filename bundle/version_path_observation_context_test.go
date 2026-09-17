package bundle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestVersionPathObservationPathObservationsCancelInsideLargeSemanticScalars(t *testing.T) {
	large := strings.Repeat("segment/", 256<<10)
	tests := []struct {
		name        string
		frontmatter string
	}{
		{name: "source resource", frontmatter: "sources:\n  - resource: " + large + "leaf.md\n"},
		{name: "computation", frontmatter: "computation: " + large + "query.sql\n"},
		{name: "executor resource", frontmatter: "executor:\n  resource: " + large + "runner.md\n"},
		{name: "attester resource", frontmatter: "attester:\n  resource: " + large + "attester.md\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			frontmatter, err := ParseFrontmatter(test.frontmatter)
			if err != nil {
				t.Fatal(err)
			}
			id := mustParseConceptID(t, "large-path")
			loaded := &Bundle{
				concepts: []Concept{{ID: id, Document: NewDocument(frontmatter, "")}},
				byID:     map[string]int{id.String(): 0},
			}
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			baseline, found, baselineErr := loaded.ConceptPathValueObservationsContext(probe, id)
			checks := probe.checks.Load()
			if baselineErr != nil || !found || checks < 8 {
				t.Fatalf("background probe = (%#v, %v, %v), checks=%d", baseline, found, baselineErr, checks)
			}
			cancelInside := &cancelAfterErrChecksContext{allowed: checks / 2}

			// Act.
			got, gotFound, gotErr := loaded.ConceptPathValueObservationsContext(cancelInside, id)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || gotFound ||
				!reflect.DeepEqual(got, ConceptPathValueObservations{}) {
				t.Fatalf("canceled observation = (%#v, %v, %v), want zero/false/context.Canceled", got, gotFound, gotErr)
			}
			baseline.Values[0].FieldPath = "mutated"
			baseline.Values[0].Scalar.Raw = "mutated"
			again, againFound, againErr := loaded.ConceptPathValueObservationsContext(context.Background(), id)
			if againErr != nil || !againFound || len(again.Values) == 0 || again.Values[0].FieldPath == "mutated" ||
				again.Values[0].Scalar.Raw == "mutated" {
				t.Fatalf("caller mutation leaked: (%#v, %v, %v)", again, againFound, againErr)
			}
		})
	}
}

func TestVersionPathObservationVersionDeclarationContextCancellationParityAndErrors(t *testing.T) {
	largeCanonical := strings.Repeat("7", 2<<20) + ".2"
	tests := []struct {
		name string
		yaml string
	}{
		{name: "canonical declaration", yaml: "okf_version: \"" + largeCanonical + "\"\n"},
		{name: "malformed declaration", yaml: "okf_version: \"" + strings.Repeat("7", 2<<20) + "x.2\"\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			frontmatter, err := ParseFrontmatter(test.yaml)
			if err != nil {
				t.Fatal(err)
			}
			legacy := frontmatter.VersionDeclarationState()
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			background, backgroundErr := frontmatter.VersionDeclarationStateContext(probe)
			checks := probe.checks.Load()
			if backgroundErr != nil || !reflect.DeepEqual(background, legacy) || checks < 16 {
				t.Fatalf("Background parity = (%#v, %v), legacy=%#v checks=%d", background, backgroundErr, legacy, checks)
			}
			cancelInside := &cancelAfterErrChecksContext{allowed: checks / 2}

			// Act.
			got, gotErr := frontmatter.VersionDeclarationStateContext(cancelInside)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || got != (VersionDeclarationState{}) {
				t.Fatalf("canceled declaration = (%#v, %v), want zero/context.Canceled", got, gotErr)
			}
		})
	}
}

func TestVersionPathObservationVersionResolutionContextCancelsWithoutOutcome(t *testing.T) {
	largeCanonical := strings.Repeat("8", 2<<20) + ".3"
	tests := []struct {
		name     string
		declared string
		selector string
	}{
		{name: "declaration parsing", declared: largeCanonical},
		{name: "selector parsing", selector: largeCanonical},
		{name: "declaration selector comparison", declared: largeCanonical, selector: OKFVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			legacy, legacyErr := ResolveVersion(test.declared, test.selector)
			probe := &cancelAfterErrChecksContext{allowed: 1 << 60}
			background, backgroundErr := resolveVersionContext(probe, test.declared, test.selector)
			checks := probe.checks.Load()
			if !reflect.DeepEqual(background, legacy) || !sameVersionPathObservationError(backgroundErr, legacyErr) || checks < 16 {
				t.Fatalf("Background parity = (%#v, %v), legacy=(%#v, %v), checks=%d", background, backgroundErr, legacy, legacyErr, checks)
			}
			cancelInside := &cancelAfterErrChecksContext{allowed: checks / 2}

			// Act.
			got, gotErr := resolveVersionContext(cancelInside, test.declared, test.selector)

			// Assert.
			if !errors.Is(gotErr, context.Canceled) || got != (VersionResolution{}) {
				t.Fatalf("canceled resolution = (%#v, %v), want zero/context.Canceled", got, gotErr)
			}
		})
	}
}

func TestVersionPathObservationVersionBackgroundErrorsRemainStable(t *testing.T) {
	// Arrange.
	tests := []struct {
		declared string
		selector string
		kind     error
	}{
		{declared: "v0.2", kind: ErrInvalidVersionDeclaration},
		{selector: "9.0", kind: ErrUnsupportedVersionSelector},
		{declared: LegacyOKFVersion, selector: OKFVersion, kind: ErrVersionConflict},
	}
	for _, test := range tests {
		// Act.
		_, legacyErr := ResolveVersion(test.declared, test.selector)
		_, contextErr := resolveVersionContext(context.Background(), test.declared, test.selector)

		// Assert.
		if !errors.Is(contextErr, test.kind) || !sameVersionPathObservationError(contextErr, legacyErr) {
			t.Fatalf("errors differ: context=%v legacy=%v kind=%v", contextErr, legacyErr, test.kind)
		}
	}
}

func sameVersionPathObservationError(left, right error) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Error() == right.Error()
}
