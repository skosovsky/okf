package validator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokenLinksAreReportedAsInfo(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nSee [missing](/does/not/exist.md).\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{CheckLinks: true})

	// Assert.
	if !report.IsConformant() {
		t.Fatalf("IsConformant() = false, diagnostics = %#v", report.Diagnostics)
	}
	if !validationDiagnosticsContain(report.Of(SeverityInfo), "does/not/exist") {
		t.Fatalf("info diagnostics = %#v, want broken link info", report.Of(SeverityInfo))
	}
}

func TestAppendixABundleIsConformant(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := appendixAValidationBundle(t)

	// Act.
	report := ValidatePath(root, nil)

	// Assert.
	if !report.IsConformant() {
		t.Fatalf("IsConformant() = false, diagnostics = %#v", report.Diagnostics)
	}
	if got := report.ErrorCount(); got != 0 {
		t.Fatalf("ErrorCount() = %d, want 0", got)
	}
}

func TestMissingTypeIsConformanceError(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "bad.md", "---\ntitle: No Type\n---\nbody\n")

	// Act.
	report := ValidatePath(root, nil)

	// Assert.
	if report.IsConformant() {
		t.Fatalf("IsConformant() = true, want false")
	}
	if !validationDiagnosticsContain(report.Of(SeverityError), "type") {
		t.Fatalf("error diagnostics = %#v, want type error", report.Of(SeverityError))
	}
}

func TestRecommendedFieldsAreWarnings(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "minimal.md", "---\ntype: Note\n---\nbody\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	if !report.IsConformant() {
		t.Fatalf("IsConformant() = false, diagnostics = %#v", report.Diagnostics)
	}
	for _, field := range []string{"title", "description", "tags", "timestamp"} {
		if !validationDiagnosticsContain(report.Of(SeverityWarning), "missing recommended frontmatter field '"+field+"'") {
			t.Fatalf("warning diagnostics = %#v, want missing %s warning", report.Of(SeverityWarning), field)
		}
	}
}

func TestLoadBundleParseErrorsFailValidation(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "bad.md", "---\ntype: Note\n")

	// Act.
	report := ValidatePath(root, nil)

	// Assert.
	if report.IsConformant() {
		t.Fatalf("IsConformant() = true, want false")
	}
}

func TestValidateReservedFileErrors(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\ntype: Note\ntitle: A\ndescription: A\ntimestamp: 2026-05-22\n---\nbody\n")
	writeValidationFile(t, root, "nested/index.md", "---\ntype: Listing\n---\n\n# Listing\n")
	writeValidationFile(t, root, "log.md", "# Log\n\n## May 22\n* bad date\n")

	// Act.
	report := ValidatePath(root, nil)

	// Assert.
	errors := report.Of(SeverityError)
	if !validationDiagnosticsContain(errors, "index.md should not contain frontmatter") {
		t.Fatalf("errors = %#v, want index frontmatter error", errors)
	}
	if !validationDiagnosticsContain(errors, "log date heading") {
		t.Fatalf("errors = %#v, want log date error", errors)
	}
	if report.IsConformant() {
		t.Fatalf("IsConformant() = true, diagnostics = %#v", report.Diagnostics)
	}
}

func TestValidateReservedFileStructureDetails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rel      string
		contents string
		want     string
	}{
		{
			name:     "index heading without entries",
			rel:      "index.md",
			contents: "# Listing\n",
			want:     "index.md section has no entries",
		},
		{
			name:     "index entry without link",
			rel:      "index.md",
			contents: "# Listing\n\n* Plain entry\n",
			want:     "index.md list entry should contain a Markdown link",
		},
		{
			name:     "log empty date",
			rel:      "log.md",
			contents: "# Log\n\n## 2026-06-21\n",
			want:     "log date heading has no entries",
		},
		{
			name:     "log out of order",
			rel:      "log.md",
			contents: "# Log\n\n## 2026-06-20\n* older\n\n## 2026-06-21\n* newer\n",
			want:     "log date headings should be newest first",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "a.md", "---\ntype: Note\ntitle: A\ndescription: A\nresource: https://example.com/a\ntags: [a]\ntimestamp: 2026-06-21T00:00:00Z\n---\nbody\n")
			writeValidationFile(t, root, tt.rel, tt.contents)

			// Act.
			report := ValidatePath(root, nil)

			// Assert.
			if report.IsConformant() {
				t.Fatalf("IsConformant() = true, diagnostics = %#v", report.Diagnostics)
			}
			if !validationDiagnosticsContain(report.Of(SeverityError), tt.want) {
				t.Fatalf("errors = %#v, want %q", report.Of(SeverityError), tt.want)
			}
		})
	}
}

func appendixAValidationBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeValidationFile(t, root, "datasets/sales.md", "---\n"+
		"type: BigQuery Dataset\n"+
		"title: Sales\n"+
		"description: All sales-related tables for the retail business.\n"+
		"resource: https://console.cloud.google.com/bigquery?p=acme&d=sales\n"+
		"tags: [sales]\n"+
		"timestamp: 2026-05-28T00:00:00Z\n"+
		"---\n\n"+
		"The sales dataset contains transactional tables, including\n"+
		"[orders](/tables/orders.md) and [customers](/tables/customers.md).\n")
	writeValidationFile(t, root, "tables/orders.md", "---\n"+
		"type: BigQuery Table\n"+
		"title: Orders\n"+
		"description: One row per completed customer order.\n"+
		"resource: https://console.cloud.google.com/bigquery?p=acme&d=sales&t=orders\n"+
		"tags: [sales, orders]\n"+
		"timestamp: 2026-05-28T00:00:00Z\n"+
		"---\n\n"+
		"# Schema\n\n"+
		"Part of the [sales dataset](/datasets/sales.md). FK to [customers](/tables/customers.md).\n")
	writeValidationFile(t, root, "tables/customers.md", "---\n"+
		"type: BigQuery Table\n"+
		"title: Customers\n"+
		"description: One row per customer.\n"+
		"timestamp: 2026-05-28T00:00:00Z\n"+
		"---\n\n"+
		"Linked from [orders](/tables/orders.md).\n")
	return root
}

func writeValidationFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func validationDiagnosticsContain(diagnostics []Diagnostic, fragment string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, fragment) {
			return true
		}
	}
	return false
}
