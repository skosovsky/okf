package validator

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestValidatePathRejectsRootAndAncestorSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink assertions require Unix-style symlink support")
	}

	// Arrange.
	realRoot := t.TempDir()
	writeValidationFile(t, realRoot, "note.md", "---\ntype: Note\n---\ninside\n")
	container := t.TempDir()
	rootLink := filepath.Join(container, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatalf("create root symlink: %v", err)
	}
	ancestorLink := filepath.Join(container, "ancestor-link")
	if err := os.Symlink(filepath.Dir(realRoot), ancestorLink); err != nil {
		t.Fatalf("create ancestor symlink: %v", err)
	}
	ancestorPath := filepath.Join(ancestorLink, filepath.Base(realRoot))

	// Act + Assert.
	for _, path := range []string{rootLink, ancestorPath} {
		report := ValidatePath(path, nil)
		if report.IsConformant() || report.ErrorCount() == 0 {
			t.Fatalf("ValidatePath(%q) = %#v, want loading error", path, report)
		}
	}
}

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
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "link_target_missing", SpecRef: "toolkit#link-resolution", File: "a.md",
		FieldPath: "body.links[0].target", Severity: SeverityInfo,
	})
}

func TestMissingLinkAnchorsExposeStableDiagnosticContract(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "source.md", "---\ntype: Note\n---\nSee [missing](target.md#missing).\n")
	writeValidationFile(t, root, "target.md", "---\ntype: Note\n---\n# Present\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{CheckLinks: true})

	// Assert.
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "link_anchor_missing", SpecRef: "toolkit#anchor-resolution", File: "source.md",
		FieldPath: "body.links[0].target", Severity: SeverityWarning,
	})
}

func TestOrphanChecksExposeStableDiagnosticContract(t *testing.T) {
	t.Parallel()

	t.Run("missing index", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")

		// Act.
		report := ValidatePath(root, &ValidatorConfig{CheckOrphans: true})

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "orphan_index_missing", SpecRef: "toolkit#orphan-missing-index", File: ".",
			FieldPath: "index.md", Severity: SeverityInfo,
		})
	})

	t.Run("unlisted concept", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "index.md", "---\nokf_version: \"0.2\"\n---\n# Notes\n\n* [Listed](listed.md)\n")
		writeValidationFile(t, root, "listed.md", "---\ntype: Note\n---\nBody.\n")
		writeValidationFile(t, root, "orphan.md", "---\ntype: Note\n---\nBody.\n")

		// Act.
		report := ValidatePath(root, &ValidatorConfig{CheckOrphans: true})

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "orphan_unlisted", SpecRef: "toolkit#orphan-coverage", File: "orphan.md",
			FieldPath: "index.md.body", Severity: SeverityWarning,
		})
	})
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
	report := ValidatePath(root, &ValidatorConfig{Strict: true, Spec: bundle.LegacyOKFVersion})

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

func TestStrictLegacyRecommendedFieldsUseSemanticYAMLValues(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "aliased.md", `---
type: Note
title_value: &title_value Alias title
description_value: &description_value Alias description
tags_value: &tags_value [semantic]
timestamp_value: &timestamp_value "2026-01-02T03:04:05Z"
resource_value: &resource_value "https://example.test/aliased"
title: *title_value
description: *description_value
tags: *tags_value
timestamp: *timestamp_value
resource: *resource_value
---
Body.
`)
	writeValidationFile(t, root, "merged.md", `---
defaults: &defaults
  title: Merged title
  description: Merged description
  tags: [semantic]
  timestamp: "2026-01-02T03:04:05Z"
  resource: "https://example.test/merged"
<<: *defaults
type: Note
---
Body.
`)

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true, Spec: bundle.LegacyOKFVersion})

	// Assert.
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.File == "aliased.md" || diagnostic.File == "merged.md" {
			t.Fatalf("semantic alias/merge produced strict-v0.1 diagnostic: %#v", diagnostic)
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

func TestReservedFilesUseParserOwnedTopLevelStructure(t *testing.T) {
	t.Parallel()

	t.Run("setext headings are accepted", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
		writeValidationFile(t, root, "index.md", "Listing\n=======\n\n* [A](a.md)\n")
		writeValidationFile(t, root, "log.md", "Log\n===\n\n2026-07-29\n----------\n\n* Entry\n")

		// Act.
		report := ValidatePath(root, nil)

		// Assert.
		if !report.IsConformant() {
			t.Fatalf("IsConformant() = false, diagnostics = %#v", report.Diagnostics)
		}
	})

	tests := []struct {
		name     string
		rel      string
		contents string
		want     string
	}{
		{
			name:     "index escaped heading",
			rel:      "index.md",
			contents: "\\# Listing\n\n* [A](a.md)\n",
			want:     "index.md should contain at least one heading",
		},
		{
			name:     "index fenced code",
			rel:      "index.md",
			contents: "```md\n# Listing\n* [A](a.md)\n```\n",
			want:     "index.md should contain at least one heading",
		},
		{
			name:     "index html",
			rel:      "index.md",
			contents: "<div>\n# Listing\n* [A](a.md)\n</div>\n",
			want:     "index.md should contain at least one heading",
		},
		{
			name:     "index blockquote",
			rel:      "index.md",
			contents: "> # Listing\n> * [A](a.md)\n",
			want:     "index.md should contain at least one heading",
		},
		{
			name:     "index list container",
			rel:      "index.md",
			contents: "- container\n\n  # Listing\n\n  * [A](a.md)\n",
			want:     "index.md should contain at least one heading",
		},
		{
			name:     "log escaped heading",
			rel:      "log.md",
			contents: "\\## 2026-07-29\n\n* Entry\n",
			want:     "log.md should contain ISO-8601 date headings",
		},
		{
			name:     "log fenced code",
			rel:      "log.md",
			contents: "```md\n## 2026-07-29\n* Entry\n```\n",
			want:     "log.md should contain ISO-8601 date headings",
		},
		{
			name:     "log html",
			rel:      "log.md",
			contents: "<div>\n## 2026-07-29\n* Entry\n</div>\n",
			want:     "log.md should contain ISO-8601 date headings",
		},
		{
			name:     "log blockquote",
			rel:      "log.md",
			contents: "> ## 2026-07-29\n> * Entry\n",
			want:     "log.md should contain ISO-8601 date headings",
		},
		{
			name:     "log list container",
			rel:      "log.md",
			contents: "- container\n\n  ## 2026-07-29\n\n  * Entry\n",
			want:     "log.md should contain ISO-8601 date headings",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nbody\n")
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

func TestConventionalSectionsUseParserOwnedTopLevelHeadings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantWarned bool
	}{
		{name: "setext", body: "Schema\n======\n", wantWarned: false},
		{name: "escaped", body: "\\# Schema\n", wantWarned: true},
		{name: "fenced", body: "```md\n# Schema\n```\n", wantWarned: true},
		{name: "html", body: "<div>\n# Schema\n</div>\n", wantWarned: true},
		{name: "blockquote", body: "> # Schema\n", wantWarned: true},
		{name: "list container", body: "- item\n\n  # Schema\n", wantWarned: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "table.md", "---\ntype: BigQuery Table\n---\n"+tt.body)

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true, Spec: bundle.LegacyOKFVersion})

			// Assert.
			warned := validationDiagnosticsContain(report.Of(SeverityWarning), "should include a '# Schema' section")
			if warned != tt.wantWarned {
				t.Fatalf("Schema warning = %v, want %v; diagnostics = %#v", warned, tt.wantWarned, report.Diagnostics)
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
