package validator

import (
	"context"
	"reflect"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestValidateReportsStructuredSemanticRelationDiagnostics(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: missing\n"+
		"  uses:\n"+
		"    - target: target#missing\n"+
		"    - target: ambiguous#duplicate\n"+
		"  writes_to: malformed\n"+
		"---\nBody.\n")
	writeValidationFile(t, root, "target.md", "---\ntype: Note\n---\nBody.\n")
	writeValidationFile(t, root, "ambiguous.md", "---\ntype: Note\nfirst:\n  id: duplicate\nsecond:\n  id: duplicate\n---\nBody.\n")
	writeValidationFile(t, root, "alias.md", "---\ntype: Note\nsection:\n  id: canonical\n  anchor: legacy\n---\nBody.\n")
	writeValidationFile(t, root, "x.md", "---\ntype: Note\nrelations:\n  uses:\n    - target: absent\n---\nBody.\n")
	writeValidationFile(t, root, "y.md", "---\ntype: Note\nrelations:\n  uses:\n    - target: absent\n---\nBody.\n")
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	a := mustValidationConceptID(t, "a")

	// Act.
	relations := b.SemanticLinksFrom(a)
	report := ValidatePath(root, &ValidatorConfig{CheckRelations: true})

	// Assert.
	if len(relations) != 0 {
		t.Fatalf("SemanticLinksFrom(a) = %#v, want no unresolved semantic relation", relations)
	}
	if report.IsConformant() {
		t.Fatalf("IsConformant() = true, diagnostics = %#v", report.Diagnostics)
	}
	for _, want := range []struct {
		code, file, source, relationType, rawTarget string
		severity                                    Severity
	}{
		{"missing_target_concept", "a.md", "a", "depends_on", "missing", SeverityError},
		{"missing_or_ambiguous_target_fragment", "a.md", "a", "uses", "target#missing", SeverityError},
		{"missing_or_ambiguous_target_fragment", "a.md", "a", "uses", "ambiguous#duplicate", SeverityError},
		{"relation_not_sequence", "a.md", "a", "writes_to", "", SeverityError},
		{"anchor_alias", "alias.md", "alias#canonical", "", "legacy", SeverityInfo},
		{"missing_target_concept", "x.md", "x", "uses", "absent", SeverityError},
		{"missing_target_concept", "y.md", "y", "uses", "absent", SeverityError},
	} {
		if !containsRelationValidationDiagnostic(report.Diagnostics, want.code, want.file, want.source, want.relationType, want.rawTarget, want.severity) {
			t.Fatalf("diagnostics = %#v, missing %#v", report.Diagnostics, want)
		}
	}
	if report.InfoCount() != 1 {
		t.Fatalf("InfoCount() = %d, want exactly the nonblocking alias diagnostic", report.InfoCount())
	}
	second := ValidatePath(root, &ValidatorConfig{CheckRelations: true})
	if !reflect.DeepEqual(report, second) {
		t.Fatalf("diagnostics are not deterministic:\nfirst=%#v\nsecond=%#v", report, second)
	}
}

func TestValidateRelationPolicyIsOptInAcrossEntryPoints(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	contents := "---\ntype: Note\nrelations:\n  uses:\n    - target: missing\n---\nBody.\n"
	writeValidationFile(t, root, "a.md", contents)
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	source := validationSource{"a.md": []byte(contents)}

	// Act.
	pathReport := ValidatePath(root, nil)
	bundleReport := ValidateBundle(b, nil)
	contextReport, err := ValidateBundleContext(context.Background(), b, nil)
	if err != nil {
		t.Fatalf("ValidateBundleContext() error = %v", err)
	}
	sourceReport, err := ValidateSource(context.Background(), source, nil)
	if err != nil {
		t.Fatalf("ValidateSource() error = %v", err)
	}
	optIn := ValidatePath(root, &ValidatorConfig{CheckRelations: true})

	// Assert.
	for name, report := range map[string]Report{
		"path": pathReport, "bundle": bundleReport, "context": contextReport, "source": sourceReport,
	} {
		if !report.IsConformant() || containsRelationValidationDiagnostic(report.Diagnostics, "missing_target_concept", "a.md", "a", "uses", "missing", SeverityError) {
			t.Fatalf("%s baseline report = %#v, want base-only conformance", name, report)
		}
	}
	if optIn.IsConformant() || !containsRelationValidationDiagnostic(optIn.Diagnostics, "missing_target_concept", "a.md", "a", "uses", "missing", SeverityError) {
		t.Fatalf("opt-in report = %#v, want semantic relation failure", optIn)
	}
}

func containsRelationValidationDiagnostic(diagnostics []Diagnostic, code, file, source, relationType, rawTarget string, severity Severity) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.File == file && diagnostic.Source.String() == source && diagnostic.RelationType == relationType && diagnostic.RawTarget == rawTarget && diagnostic.Severity == severity && len(diagnostic.Refs) == 1 && diagnostic.Refs[0].String() == source {
			return true
		}
	}
	return false
}

func TestInvalidYAMLFrontmatterStillFailsConformance(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "bad.md", "---\ntype: [\nrelations:\n  depends_on:\n    - target: missing#col\n---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{CheckLinks: true})

	// Assert.
	if report.IsConformant() {
		t.Fatalf("IsConformant() = true, want invalid YAML frontmatter to fail")
	}
	if !validationDiagnosticsContain(report.Of(SeverityError), "invalid frontmatter") {
		t.Fatalf("errors = %#v, want invalid frontmatter diagnostic", report.Of(SeverityError))
	}
}

func mustValidationConceptID(t *testing.T, raw string) bundle.ConceptID {
	t.Helper()
	id, err := bundle.ParseConceptID(raw)
	if err != nil {
		t.Fatalf("ParseConceptID(%q) error = %v", raw, err)
	}
	return id
}
