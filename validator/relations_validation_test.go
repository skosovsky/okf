package validator

import (
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestValidateIgnoresSemanticRelationTargets(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\n"+
		"type: Note\n"+
		"relations:\n"+
		"  depends_on:\n"+
		"    - target: missing#col\n"+
		"  writes_to: malformed\n"+
		"---\nBody.\n")
	b, err := bundle.LoadBundle(root)
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	a := mustValidationConceptID(t, "a")

	// Act.
	relations := b.SemanticLinksFrom(a)
	report := ValidatePath(root, &ValidatorConfig{CheckLinks: true})

	// Assert.
	if len(relations) != 1 || relations[0].TargetExists {
		t.Fatalf("SemanticLinksFrom(a) = %#v, want one missing semantic relation", relations)
	}
	if !report.IsConformant() {
		t.Fatalf("IsConformant() = false, diagnostics = %#v", report.Diagnostics)
	}
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want semantic relations ignored by validation", report.Diagnostics)
	}
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
