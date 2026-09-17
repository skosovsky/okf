package mutation

import (
	"context"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/store"
	"github.com/skosovsky/okf/validator"
	"gopkg.in/yaml.v3"
)

func TestPreviewDiagnosticsNULTupleComponentsNeverCollide(t *testing.T) {
	// Arrange. Decode escaped NULs through YAML so the fixtures exercise the
	// same unrestricted scalar values that can reach diagnostic components.
	codeWithNUL := decodePlannerIdentityYAMLString(t, `"a\0b"`)
	fileWithNUL := decodePlannerIdentityYAMLString(t, `"b\0c"`)
	relationTypeWithNUL := decodePlannerIdentityYAMLString(t, `"uses\0raw"`)
	rawTargetWithNUL := decodePlannerIdentityYAMLString(t, `"raw\0target"`)
	source := ref(t, "source").ID
	validationDiagnostics := []validator.Diagnostic{
		{Code: codeWithNUL, File: "c", Severity: validator.SeverityWarning, Message: "validation"},
		{Code: "a", File: fileWithNUL, Severity: validator.SeverityWarning, Message: "validation"},
	}
	relationDiagnostics := []bundle.RelationDiagnostic{
		{Code: "relation", Source: source, RelationType: relationTypeWithNUL, RawTarget: "target", File: "a.md", Message: "relation", Severity: bundle.RelationDiagnosticInfo},
		{Code: "relation", Source: source, RelationType: "uses", RawTarget: rawTargetWithNUL, File: "a.md", Message: "relation", Severity: bundle.RelationDiagnosticInfo},
	}
	// Duplicate every value: exact identities must still coalesce while the
	// formerly delimiter-colliding identities remain distinct.
	validationDiagnostics = append(validationDiagnostics, validationDiagnostics...)
	relationDiagnostics = append(relationDiagnostics, relationDiagnostics...)

	// Act.
	got, err := previewDiagnosticsContext(
		context.Background(),
		validator.Report{Diagnostics: validationDiagnostics},
		relationDiagnostics,
	)

	// Assert.
	if err != nil {
		t.Fatal(err)
	}
	if gotCount, wantCount := len(got), 4; gotCount != wantCount {
		t.Fatalf("diagnostic count = %d, want %d: %#v", gotCount, wantCount, got)
	}
	assertPlannerDiagnosticPresent(t, got, store.Diagnostic{Kind: store.DiagnosticValidation, Severity: store.DiagnosticWarning, Code: codeWithNUL, File: "c", Message: "validation"})
	assertPlannerDiagnosticPresent(t, got, store.Diagnostic{Kind: store.DiagnosticValidation, Severity: store.DiagnosticWarning, Code: "a", File: fileWithNUL, Message: "validation"})
	assertPlannerDiagnosticPresent(t, got, store.Diagnostic{Kind: store.DiagnosticRelation, Severity: store.DiagnosticInfo, Code: "relation", File: "a.md", Message: "relation", RelationType: relationTypeWithNUL, RawTarget: "target", Refs: []bundle.RelationRef{{ID: source}}})
	assertPlannerDiagnosticPresent(t, got, store.Diagnostic{Kind: store.DiagnosticRelation, Severity: store.DiagnosticInfo, Code: "relation", File: "a.md", Message: "relation", RelationType: "uses", RawTarget: rawTargetWithNUL, Refs: []bundle.RelationRef{{ID: source}}})
}

func TestPreviewDiagnosticsNULIdentityOrderingIgnoresInputOrder(t *testing.T) {
	// Arrange.
	nul := decodePlannerIdentityYAMLString(t, `"\0"`)
	source := ref(t, "source").ID
	validations := []validator.Diagnostic{
		{Code: "a" + nul + "b", File: "c", Severity: validator.SeverityInfo, Message: "one"},
		{Code: "a", File: "b" + nul + "c", Severity: validator.SeverityInfo, Message: "one"},
		{Code: "z", File: "a.md", Severity: validator.SeverityError, Message: "last"},
	}
	relations := []bundle.RelationDiagnostic{
		{Code: "relation", Source: source, RelationType: "a" + nul + "b", RawTarget: "c", File: "a.md", Message: "one", Severity: bundle.RelationDiagnosticInfo},
		{Code: "relation", Source: source, RelationType: "a", RawTarget: "b" + nul + "c", File: "a.md", Message: "one", Severity: bundle.RelationDiagnosticInfo},
	}
	permutations := []struct {
		validations []validator.Diagnostic
		relations   []bundle.RelationDiagnostic
	}{
		{validations: validations, relations: relations},
		{validations: reversePlannerDiagnostics(validations), relations: relations},
		{validations: validations, relations: reversePlannerRelationDiagnostics(relations)},
		{validations: reversePlannerDiagnostics(validations), relations: reversePlannerRelationDiagnostics(relations)},
	}

	// Act.
	var baseline []store.Diagnostic
	for index, permutation := range permutations {
		got, err := previewDiagnosticsContext(
			context.Background(),
			validator.Report{Diagnostics: permutation.validations},
			permutation.relations,
		)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			baseline = got
			continue
		}

		// Assert.
		if !reflect.DeepEqual(got, baseline) {
			t.Fatalf("permutation %d changed canonical order:\n got %#v\nwant %#v", index, got, baseline)
		}
	}
}

func TestLengthFramedIdentitySeparatesEmbeddedNULTuples(t *testing.T) {
	// Arrange.
	left := []string{"a\x00b", "c"}
	right := []string{"a", "b\x00c"}

	// Act.
	leftFrame, leftErr := lengthFrameStringsContext(context.Background(), left)
	rightFrame, rightErr := lengthFrameStringsContext(context.Background(), right)
	repeatedFrame, repeatedErr := lengthFrameStringsContext(context.Background(), left)

	// Assert.
	if leftErr != nil || rightErr != nil || repeatedErr != nil {
		t.Fatalf("framing errors = %v / %v / %v", leftErr, rightErr, repeatedErr)
	}
	if leftFrame == rightFrame {
		t.Fatalf("distinct tuples collided: %q", leftFrame)
	}
	if leftFrame != repeatedFrame {
		t.Fatalf("framing is nondeterministic: %q versus %q", leftFrame, repeatedFrame)
	}
}

func TestDiagnosticIdentityCanonicalizesShuffledReferenceOrder(t *testing.T) {
	// Arrange.
	hashConcept, fragmentRef := plannerHashCollisionRefs(t)
	left := store.Diagnostic{
		Kind: store.DiagnosticValidation,
		Code: "same",
		Refs: []bundle.RelationRef{hashConcept, fragmentRef, hashConcept},
	}
	right := left
	right.Refs = []bundle.RelationRef{fragmentRef, hashConcept}

	// Act.
	leftKey, leftParts, leftRefs, leftErr := diagnosticKeyContext(context.Background(), left)
	rightKey, rightParts, rightRefs, rightErr := diagnosticKeyContext(context.Background(), right)

	// Assert.
	if leftErr != nil || rightErr != nil {
		t.Fatalf("diagnosticKeyContext() errors = %v / %v", leftErr, rightErr)
	}
	if leftKey != rightKey {
		t.Fatalf("equivalent ref sets have different identities: %#v versus %#v", leftKey, rightKey)
	}
	if !reflect.DeepEqual(leftParts, rightParts) || !reflect.DeepEqual(leftRefs, rightRefs) {
		t.Fatalf("canonical refs differ: (%#v, %#v) versus (%#v, %#v)", leftParts, leftRefs, rightParts, rightRefs)
	}
	wantRefs := []bundle.RelationRef{fragmentRef, hashConcept}
	if !reflect.DeepEqual(leftRefs, wantRefs) {
		t.Fatalf("canonical refs = %#v, want %#v", leftRefs, wantRefs)
	}
}

func decodePlannerIdentityYAMLString(t *testing.T, token string) string {
	t.Helper()
	var value string
	if err := yaml.Unmarshal([]byte(token), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func assertPlannerDiagnosticPresent(t *testing.T, diagnostics []store.Diagnostic, want store.Diagnostic) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if reflect.DeepEqual(diagnostic, want) {
			return
		}
	}
	t.Fatalf("diagnostic %#v not found in %#v", want, diagnostics)
}

func reversePlannerDiagnostics(values []validator.Diagnostic) []validator.Diagnostic {
	out := append([]validator.Diagnostic(nil), values...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func reversePlannerRelationDiagnostics(values []bundle.RelationDiagnostic) []bundle.RelationDiagnostic {
	out := append([]bundle.RelationDiagnostic(nil), values...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}
