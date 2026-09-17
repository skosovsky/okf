package validator

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestVersionAwareValidationUsesSharedResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		declaration   string
		spec          string
		wantDeclared  string
		wantEffective string
		wantSource    bundle.VersionSource
		wantCompat    bundle.VersionCompatibility
		wantCode      string
	}{
		{
			name:          "absent defaults to v0.2",
			spec:          "auto",
			wantEffective: bundle.OKFVersion,
			wantSource:    bundle.VersionSourceDefault,
			wantCompat:    bundle.VersionCompatibilityNative,
		},
		{
			name:          "declared legacy",
			declaration:   bundle.LegacyOKFVersion,
			wantDeclared:  bundle.LegacyOKFVersion,
			wantEffective: bundle.LegacyOKFVersion,
			wantSource:    bundle.VersionSourceDeclared,
			wantCompat:    bundle.VersionCompatibilityLegacy,
		},
		{
			name:          "future is best effort",
			declaration:   "9.7",
			wantDeclared:  "9.7",
			wantEffective: bundle.OKFVersion,
			wantSource:    bundle.VersionSourceFutureBestEffort,
			wantCompat:    bundle.VersionCompatibilityBestEffort,
		},
		{
			name:          "selector conflict",
			declaration:   bundle.LegacyOKFVersion,
			spec:          bundle.OKFVersion,
			wantDeclared:  bundle.LegacyOKFVersion,
			wantEffective: bundle.LegacyOKFVersion,
			wantSource:    bundle.VersionSourceDeclared,
			wantCompat:    bundle.VersionCompatibilityLegacy,
			wantCode:      "version_assertion_failed",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
			if tt.declaration != "" {
				writeValidationFile(t, root, "index.md", "---\nokf_version: \""+tt.declaration+"\"\n---\n# Notes\n\n* [Concept](concept.md)\n")
			}

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Spec: tt.spec})

			// Assert.
			if report.Version.Declared != tt.wantDeclared ||
				report.Version.Effective != tt.wantEffective ||
				report.Version.Source != tt.wantSource ||
				report.Version.Compatibility != tt.wantCompat {
				t.Fatalf("Version = %#v, want declared=%q effective=%q source=%q compatibility=%q", report.Version, tt.wantDeclared, tt.wantEffective, tt.wantSource, tt.wantCompat)
			}
			if tt.wantCode == "" {
				if !report.IsConformant() {
					t.Fatalf("report = %#v, want conformant", report)
				}
			} else {
				assertExactValidationDiagnostic(t, report, Diagnostic{
					Code:          tt.wantCode,
					SpecRef:       "toolkit#version-selector-assertion",
					File:          "index.md",
					FieldPath:     "okf_version",
					Severity:      SeverityWarning,
					PolicyFailure: true,
				})
				if !report.IsConformant() || report.ErrorCount() != 0 || !report.HasPolicyFailures() || report.ExitCode() != 1 {
					t.Fatalf("selector assertion outcome = %#v, want conformant bundle with failed policy", report)
				}
			}
		})
	}
}

func TestVersionSelectorDeclarationMatrix(t *testing.T) {
	t.Parallel()

	declarations := []struct {
		name, value string
		malformed   bool
	}{
		{name: "absent"},
		{name: "legacy", value: bundle.LegacyOKFVersion},
		{name: "native", value: bundle.OKFVersion},
		{name: "malformed", value: "v0.2", malformed: true},
	}
	selectors := []string{"auto", bundle.LegacyOKFVersion, bundle.OKFVersion, "v0.2", "9.9"}

	for _, declaration := range declarations {
		declaration := declaration
		for _, selector := range selectors {
			selector := selector
			t.Run(declaration.name+"/"+selector, func(t *testing.T) {
				t.Parallel()

				// Arrange.
				root := t.TempDir()
				writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
				if declaration.value != "" {
					writeValidationFile(t, root, "index.md", "---\nokf_version: \""+declaration.value+"\"\n---\n# Notes\n\n* [Concept](concept.md)\n")
				}
				wantEffective := bundle.OKFVersion
				wantSource := bundle.VersionSourceDefault
				wantCompatibility := bundle.VersionCompatibilityNative
				wantPolicyFailure := false
				if declaration.malformed {
					wantPolicyFailure = selector == "v0.2" || selector == "9.9"
				} else {
					switch {
					case selector == "auto" && declaration.value != "":
						wantEffective = declaration.value
						wantSource = bundle.VersionSourceDeclared
					case selector == bundle.LegacyOKFVersion || selector == bundle.OKFVersion:
						if declaration.value == "" || declaration.value == selector {
							wantEffective = selector
							wantSource = bundle.VersionSourceExplicit
						} else {
							wantEffective = declaration.value
							wantSource = bundle.VersionSourceDeclared
							wantPolicyFailure = true
						}
					case selector != "auto":
						if declaration.value != "" {
							wantEffective = declaration.value
							wantSource = bundle.VersionSourceDeclared
						}
						wantPolicyFailure = true
					}
				}
				if wantEffective == bundle.LegacyOKFVersion {
					wantCompatibility = bundle.VersionCompatibilityLegacy
				}

				// Act.
				report := ValidatePath(root, &ValidatorConfig{Spec: selector})

				// Assert.
				if report.Version.Effective != wantEffective || report.Version.Source != wantSource || report.Version.Compatibility != wantCompatibility {
					t.Fatalf("Version = %#v, want effective=%q source=%q compatibility=%q", report.Version, wantEffective, wantSource, wantCompatibility)
				}
				if got := hasValidationDiagnostic(report, "version_assertion_failed", "okf_version"); got != wantPolicyFailure {
					t.Fatalf("version_assertion_failed = %v, want %v: %#v", got, wantPolicyFailure, report.Diagnostics)
				}
				if report.HasPolicyFailures() != wantPolicyFailure {
					t.Fatalf("HasPolicyFailures() = %v, want %v", report.HasPolicyFailures(), wantPolicyFailure)
				}
				if wantPolicyFailure {
					assertExactValidationDiagnostic(t, report, Diagnostic{
						Code: "version_assertion_failed", SpecRef: "toolkit#version-selector-assertion", File: "index.md",
						FieldPath: "okf_version", Severity: SeverityWarning, PolicyFailure: true,
					})
				}
				if declaration.malformed {
					assertExactValidationDiagnostic(t, report, Diagnostic{
						Code: "index_structure_invalid", SpecRef: "okf-v0.2#12", File: "index.md",
						FieldPath: "okf_version", Severity: SeverityError,
					})
					if report.IsConformant() {
						t.Fatalf("malformed declaration report = %#v, want hard reserved-file error", report)
					}
				} else if !report.IsConformant() {
					t.Fatalf("report = %#v, selector policy must not change base conformance", report)
				}
			})
		}
	}
}

func TestReportPreservesVersionDeclarationObservationSeparatelyFromTraversalResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frontmatter string
		want        bundle.VersionDeclarationState
	}{
		{
			name: "absent",
			want: bundle.VersionDeclarationState{Valid: true},
		},
		{
			name:        "native",
			frontmatter: "okf_version: \"0.2\"\n",
			want:        bundle.VersionDeclarationState{Present: true, Valid: true, Raw: "0.2", Value: "0.2"},
		},
		{
			name:        "future",
			frontmatter: "okf_version: \"9.7\"\n",
			want:        bundle.VersionDeclarationState{Present: true, Valid: true, Raw: "9.7", Value: "9.7"},
		},
		{
			name:        "malformed syntax",
			frontmatter: "okf_version: \"v0.2\"\n",
			want:        bundle.VersionDeclarationState{Present: true, Raw: "v0.2"},
		},
		{
			name:        "blank string",
			frontmatter: "okf_version: \"\"\n",
			want:        bundle.VersionDeclarationState{Present: true},
		},
		{
			name:        "numeric scalar",
			frontmatter: "okf_version: 0.2\n",
			want:        bundle.VersionDeclarationState{Present: true, Raw: "0.2"},
		},
		{
			name:        "sequence",
			frontmatter: "okf_version: [\"0.2\"]\n",
			want:        bundle.VersionDeclarationState{Present: true},
		},
		{
			name:        "mapping",
			frontmatter: "okf_version: {value: \"0.2\"}\n",
			want:        bundle.VersionDeclarationState{Present: true},
		},
		{
			name:        "terminal alias",
			frontmatter: "version: &version \"0.2\"\nokf_version: *version\n",
			want:        bundle.VersionDeclarationState{Present: true, Valid: true, Raw: "0.2", Value: "0.2"},
		},
		{
			name:        "merged malformed",
			frontmatter: "base: &base {okf_version: \"v0.2\"}\n<<: *base\n",
			want:        bundle.VersionDeclarationState{Present: true, Raw: "v0.2"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
			if tt.frontmatter == "" {
				writeValidationFile(t, root, "index.md", "# Notes\n\n* [Concept](concept.md)\n")
			} else {
				writeValidationFile(t, root, "index.md", "---\n"+tt.frontmatter+"---\n# Notes\n\n* [Concept](concept.md)\n")
			}

			// Act.
			report := ValidatePath(root, nil)

			// Assert.
			if !reflect.DeepEqual(report.VersionDeclaration, tt.want) {
				t.Fatalf("VersionDeclaration = %#v, want %#v", report.VersionDeclaration, tt.want)
			}
			if tt.want.Present && !tt.want.Valid {
				if report.Version.Effective != bundle.OKFVersion ||
					report.Version.Source != bundle.VersionSourceDefault {
					t.Fatalf("malformed traversal Version = %#v, want explicit default traversal only", report.Version)
				}
				if report.IsConformant() || len(report.Diagnostics) == 0 {
					t.Fatalf("malformed declaration diagnostics = %#v", report.Diagnostics)
				}
			}
		})
	}
}

func TestStrictV02TypeOnlyAndPathValuesRemainClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		frontmatter string
	}{
		{name: "type only"},
		{name: "absolute URL resource", frontmatter: "resource: https://example.test/reference\n"},
		{name: "bundle relative resource", frontmatter: "resource: /references/source.json\n"},
		{name: "relative resource", frontmatter: "resource: ../references/source.json\n"},
		{name: "source scope descriptor", frontmatter: "sources: [{resource: all queries in project X}]\n"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "concept.md", "---\ntype: Note\n"+tt.frontmatter+"---\nBody.\n")

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true})

			// Assert.
			if !report.IsConformant() || report.ErrorCount() != 0 || report.WarningCount() != 0 {
				t.Fatalf("report = %#v, want strict-clean v0.2 concept", report)
			}
			if hasValidationDiagnostic(report, "legacy_recommended_field_missing", "timestamp") {
				t.Fatalf("diagnostics = %#v, v0.2 must not require legacy timestamp", report.Diagnostics)
			}
		})
	}
}

func TestStrictV02MalformedOptionalFamiliesRemainWarnings(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "malformed.md", "---\n"+
		"type: Note\n"+
		"sources: nope\n"+
		"generated: []\n"+
		"verified: nope\n"+
		"status: archived\n"+
		"stale_after: tomorrow\n"+
		"---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	if !report.IsConformant() || report.ErrorCount() != 0 {
		t.Fatalf("report = %#v, malformed optionals must not become base errors", report)
	}
	for _, want := range []struct{ code, ref, path string }{
		{"sources_invalid", "okf-v0.2#5.1", "sources"},
		{"generated_invalid", "okf-v0.2#5.2", "generated"},
		{"verified_invalid", "okf-v0.2#5.2", "verified"},
		{"status_invalid", "okf-v0.2#5.4", "status"},
		{"stale_after_invalid", "okf-v0.2#5.5", "stale_after"},
	} {
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: want.code, SpecRef: want.ref, File: "malformed.md",
			FieldPath: want.path, Severity: SeverityWarning,
		})
	}
	second := ValidatePath(root, &ValidatorConfig{Strict: true})
	if !reflect.DeepEqual(report, second) {
		t.Fatalf("strict reports are not deterministic:\nfirst=%#v\nsecond=%#v", report, second)
	}
}

func TestBaseDiagnosticsExposeCodesPathsAndSpecRefs(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "missing-frontmatter.md", "Body.\n")
	writeValidationFile(t, root, "missing-type.md", "---\ntitle: Missing type\n---\nBody.\n")
	writeValidationFile(t, root, "blank-type.md", "---\ntype: \"   \"\n---\nBody.\n")
	writeValidationFile(t, root, "invalid.md", "---\ntype: [\n---\nBody.\n")
	writeValidationFile(t, root, "index.md", "# Empty\n")
	writeValidationFile(t, root, "log.md", "# Log\n\n## someday\n* Entry\n")

	// Act.
	report := ValidatePath(root, nil)

	// Assert.
	for _, want := range []struct{ code, file, path, ref string }{
		{"concept_frontmatter_missing", "missing-frontmatter.md", "frontmatter", "okf-v0.2#11.1"},
		{"concept_type_invalid", "missing-type.md", "type", "okf-v0.2#11.2"},
		{"concept_type_invalid", "blank-type.md", "type", "okf-v0.2#11.2"},
		{"concept_unparseable", "invalid.md", "frontmatter", "okf-v0.2#11.1"},
		{"index_structure_invalid", "index.md", "body", "okf-v0.2#11.3"},
		{"log_structure_invalid", "log.md", "body", "okf-v0.2#11.3"},
	} {
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: want.code, SpecRef: want.ref, File: want.file,
			FieldPath: want.path, Severity: SeverityError,
		})
	}
}

func TestDuplicateTypeAndReservedVersionRemainBaseDiagnostics(t *testing.T) {
	t.Parallel()

	t.Run("duplicate concept type", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "concept.md", "---\ntype: Note\ntype: Reference\n---\nBody.\n")

		// Act.
		report := ValidatePath(root, nil)

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "concept_type_invalid", SpecRef: "okf-v0.2#11.2", File: "concept.md",
			FieldPath: "type", Severity: SeverityError,
		})
		if len(report.Diagnostics) != 1 || !strings.Contains(report.Diagnostics[0].Message, "declared exactly once") {
			t.Fatalf("diagnostics = %#v, want one exact duplicate-type diagnostic", report.Diagnostics)
		}
	})

	for _, tt := range []struct {
		name, declarations string
	}{
		{name: "identical root declarations", declarations: "okf_version: \"0.2\"\nokf_version: \"0.2\"\n"},
		{name: "conflicting root declarations", declarations: "okf_version: \"0.1\"\nokf_version: \"0.2\"\n"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
			writeValidationFile(t, root, "index.md", "---\n"+tt.declarations+"---\n# Concepts\n\n* [Concept](concept.md)\n")

			// Act.
			report := ValidatePath(root, nil)

			// Assert.
			assertExactValidationDiagnostic(t, report, Diagnostic{
				Code: "index_structure_invalid", SpecRef: "okf-v0.2#12", File: "index.md",
				FieldPath: "okf_version", Severity: SeverityError,
			})
			if len(report.Diagnostics) != 1 || !strings.Contains(report.Diagnostics[0].Message, "declared exactly once") {
				t.Fatalf("diagnostics = %#v, want one exact duplicate-version diagnostic", report.Diagnostics)
			}
			if !report.VersionDeclaration.Present || report.VersionDeclaration.Valid ||
				report.Version.Effective != bundle.OKFVersion || report.Version.Source != bundle.VersionSourceDefault {
				t.Fatalf("version observation/resolution = %#v/%#v, want unresolved declaration with default traversal", report.VersionDeclaration, report.Version)
			}
		})
	}
}

func TestStrictV02DuplicateStandardFamiliesFailClosedWithoutLegacyFallback(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "families.md", `---
type: Note
resource: ./one.json
resource: ./two.json
tags: [one]
tags: [two]
usage_window: {from: 2026-01-01, to: 2026-01-31}
usage_window: {from: 2026-02-01, to: 2026-02-28}
sources: []
sources: [{resource: scope}]
generated: {by: process:one, at: 2026-01-01T00:00:00Z}
generated: {by: process:two, at: 2026-01-02T00:00:00Z}
verified: {by: process:one, at: 2026-01-01T00:00:00Z}
verified: {by: process:two, at: 2026-01-02T00:00:00Z}
status: stable
status: deprecated
stale_after: 2026-12-01
stale_after: 2026-12-02
timestamp: invalid-but-shadowed
---
Claim [1].

# Citations

not a numbered citation
`)
	writeValidationFile(t, root, "computation.md", `---
type: Attested Computation
runtime: sql
runtime: python
parameters: []
parameters: [{name: value, type: string, required: true}]
computation: ./one.sql
computation: ./two.sql
executor: {resource: ./one.md, receipt: [result]}
executor: {resource: ./two.md, receipt: [result]}
attester: {resource: ./one.py}
attester: {resource: ./two.py}
---
# Computation

`+"```sql"+`
select 1
`+"```"+`
`)

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	wants := []Diagnostic{
		{Code: "attester_invalid", SpecRef: "okf-v0.2#10.2", File: "computation.md", FieldPath: "attester", Severity: SeverityWarning},
		{Code: "computation_invalid", SpecRef: "okf-v0.2#6.2", File: "computation.md", FieldPath: "computation", Severity: SeverityWarning},
		{Code: "executor_invalid", SpecRef: "okf-v0.2#10.2", File: "computation.md", FieldPath: "executor", Severity: SeverityWarning},
		{Code: "parameters_invalid", SpecRef: "okf-v0.2#10.2", File: "computation.md", FieldPath: "parameters", Severity: SeverityWarning},
		{Code: "runtime_invalid", SpecRef: "okf-v0.2#10.2", File: "computation.md", FieldPath: "runtime", Severity: SeverityWarning},
		{Code: "generated_invalid", SpecRef: "okf-v0.2#5.2", File: "families.md", FieldPath: "generated", Severity: SeverityWarning},
		{Code: "path_value_invalid", SpecRef: "okf-v0.2#6.2", File: "families.md", FieldPath: "resource", Severity: SeverityWarning},
		{Code: "sources_invalid", SpecRef: "okf-v0.2#5.1", File: "families.md", FieldPath: "sources", Severity: SeverityWarning},
		{Code: "stale_after_invalid", SpecRef: "okf-v0.2#5.5", File: "families.md", FieldPath: "stale_after", Severity: SeverityWarning},
		{Code: "status_invalid", SpecRef: "okf-v0.2#5.4", File: "families.md", FieldPath: "status", Severity: SeverityWarning},
		{Code: "tags_invalid", SpecRef: "okf-v0.2#4.1", File: "families.md", FieldPath: "tags", Severity: SeverityWarning},
		{Code: "usage_window_invalid", SpecRef: "okf-v0.2#5.1", File: "families.md", FieldPath: "usage_window", Severity: SeverityWarning},
		{Code: "verified_invalid", SpecRef: "okf-v0.2#5.2", File: "families.md", FieldPath: "verified", Severity: SeverityWarning},
	}
	for _, want := range wants {
		assertExactValidationDiagnostic(t, report, want)
	}
	if !report.IsConformant() || report.ErrorCount() != 0 || report.WarningCount() != len(wants) {
		t.Fatalf("report = %#v, want exactly %d strict warnings and no errors", report, len(wants))
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "concept_unparseable" || diagnostic.Code == "timestamp_invalid" ||
			diagnostic.Code == "citation_integrity_invalid" {
			t.Fatalf("diagnostics = %#v, duplicate replacement families must suppress legacy fallbacks without parse failure", report.Diagnostics)
		}
	}
}

func TestStrictV02DuplicateMergeKeysFailClosedWithoutDonorLeakage(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "families.md", `---
type: Note
left: &left
  generated: {by: process:left, at: 2026-07-28T00:00:00Z}
  sources: []
  status: stable
  stale_after: 2026-07-28
right: &right
  generated: {by: process:right, at: 2026-07-29T00:00:00Z}
  sources: [{resource: right.md}]
  status: deprecated
  stale_after: 2026-07-29
<<: *left
<<: *right
timestamp: invalid-but-shadowed
---
Claim [1].

# Citations

not a numbered citation
`)
	writeValidationFile(t, root, "computation.md", `---
type: Attested Computation
runtime: sql
computation: ./query.sql
attester: {resource: ./check.py}
left: &left
  executor: {resource: ./one.md, receipt: [one]}
right: &right
  executor: {resource: ./two.md, receipt: [two]}
<<: *left
<<: *right
---
`)
	writeValidationFile(t, root, "query.sql", "select 1\n")
	writeValidationFile(t, root, "check.py", "def attest(): return True\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	wants := []Diagnostic{
		{Code: "executor_invalid", SpecRef: "okf-v0.2#10.2", File: "computation.md", FieldPath: "executor", Severity: SeverityWarning},
		{Code: "generated_invalid", SpecRef: "okf-v0.2#5.2", File: "families.md", FieldPath: "generated", Severity: SeverityWarning},
		{Code: "sources_invalid", SpecRef: "okf-v0.2#5.1", File: "families.md", FieldPath: "sources", Severity: SeverityWarning},
		{Code: "stale_after_invalid", SpecRef: "okf-v0.2#5.5", File: "families.md", FieldPath: "stale_after", Severity: SeverityWarning},
		{Code: "status_invalid", SpecRef: "okf-v0.2#5.4", File: "families.md", FieldPath: "status", Severity: SeverityWarning},
	}
	for _, want := range wants {
		assertExactValidationDiagnostic(t, report, want)
	}
	if !report.IsConformant() || report.ErrorCount() != 0 ||
		report.WarningCount() != len(wants) {
		t.Fatalf("report = %#v, want exactly %d merge-key warnings", report, len(wants))
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "timestamp_invalid" ||
			diagnostic.Code == "citation_integrity_invalid" ||
			strings.HasPrefix(diagnostic.FieldPath, "executor.") {
			t.Fatalf("duplicate merge donor leaked into diagnostics: %#v", report.Diagnostics)
		}
	}
}

func TestStrictV02DuplicateNestedStandardKeysHaveExactPaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "nested.md", `---
type: Attested Computation
runtime: sql
usage_window:
  from: 2026-01-01
  from: 2026-01-02
  to: 2026-01-31
sources:
  - resource: ./one.json
    resource: ./two.json
generated:
  by: process:one
  by: process:two
  at: 2026-01-01T00:00:00Z
verified:
  by: process:one
  by: process:two
  at: 2026-01-01T00:00:00Z
parameters:
  - name: one
    name: two
    type: string
    required: true
executor:
  resource: ./one.md
  resource: ./two.md
  receipt: [one]
  receipt: [two]
attester:
  resource: ./one.py
  resource: ./two.py
---
# Computation

`+"```sql"+`
select 1
`+"```"+`
`)

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	wants := []Diagnostic{
		{Code: "actor_invalid", SpecRef: "okf-v0.2#7", File: "nested.md", FieldPath: "generated.by", Severity: SeverityWarning},
		{Code: "attester_resource_missing", SpecRef: "okf-v0.2#10.2", File: "nested.md", FieldPath: "attester.resource", Severity: SeverityWarning},
		{Code: "executor_resource_missing", SpecRef: "okf-v0.2#10.2", File: "nested.md", FieldPath: "executor.resource", Severity: SeverityWarning},
		{Code: "parameter_name_missing", SpecRef: "okf-v0.2#10.2", File: "nested.md", FieldPath: "parameters[0].name", Severity: SeverityWarning},
		{Code: "receipt_invalid", SpecRef: "okf-v0.2#10.2", File: "nested.md", FieldPath: "executor.receipt", Severity: SeverityWarning},
		{Code: "source_resource_missing", SpecRef: "okf-v0.2#5.1", File: "nested.md", FieldPath: "sources[0].resource", Severity: SeverityWarning},
		{Code: "usage_window_invalid", SpecRef: "okf-v0.2#5.1", File: "nested.md", FieldPath: "usage_window.from", Severity: SeverityWarning},
		{Code: "verification_by_missing", SpecRef: "okf-v0.2#5.2", File: "nested.md", FieldPath: "verified[0].by", Severity: SeverityWarning},
	}
	for _, want := range wants {
		assertExactValidationDiagnostic(t, report, want)
	}
	if !report.IsConformant() || report.ErrorCount() != 0 || report.WarningCount() != len(wants) {
		t.Fatalf("report = %#v, want exactly %d nested-key warnings and no errors", report, len(wants))
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "concept_unparseable" {
			t.Fatalf("diagnostics = %#v, duplicate nested keys must remain strict field diagnostics", report.Diagnostics)
		}
	}
}

func TestBaseAndReservedPathsAreVersionAwareAndExact(t *testing.T) {
	t.Parallel()

	t.Run("legacy type", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n# Concepts\n\n* [Missing type](missing.md)\n")
		writeValidationFile(t, root, "missing.md", "---\ntitle: Missing\n---\nBody.\n")

		// Act.
		report := ValidatePath(root, nil)

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "concept_type_invalid", SpecRef: "okf-v0.1#11.2", File: "missing.md",
			FieldPath: "type", Severity: SeverityError,
		})
	})

	t.Run("reserved frontmatter fields", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
		writeValidationFile(t, root, "index.md", "---\nokf_version: \"0.2\"\ntitle: Invalid\n---\n# Concepts\n\n* [Concept](concept.md)\n")
		writeValidationFile(t, root, "nested/index.md", "---\ntype: Invalid\n---\n# Nested\n\n* [Concept](../concept.md)\n")
		writeValidationFile(t, root, "log.md", "---\ntype: Invalid\n---\n# Log\n")

		// Act.
		report := ValidatePath(root, nil)

		// Assert.
		for _, want := range []Diagnostic{
			{Code: "index_structure_invalid", SpecRef: "okf-v0.2#11.3", File: "index.md", FieldPath: "title", Severity: SeverityError},
			{Code: "index_structure_invalid", SpecRef: "okf-v0.2#11.3", File: "nested/index.md", FieldPath: "frontmatter", Severity: SeverityError},
			{Code: "log_structure_invalid", SpecRef: "okf-v0.2#11.3", File: "log.md", FieldPath: "frontmatter", Severity: SeverityError},
		} {
			assertExactValidationDiagnostic(t, report, want)
		}
	})

	t.Run("malformed version declaration", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
		writeValidationFile(t, root, "index.md", "---\nokf_version: \"v0.2\"\n---\n# Concepts\n\n* [Concept](concept.md)\n")

		// Act.
		report := ValidatePath(root, nil)

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "index_structure_invalid", SpecRef: "okf-v0.2#12", File: "index.md",
			FieldPath: "okf_version", Severity: SeverityError,
		})
		if report.HasPolicyFailures() || report.Version.Effective != bundle.OKFVersion || report.Version.Source != bundle.VersionSourceDefault {
			t.Fatalf("malformed declaration outcome = %#v, want base error with v0.2 default validation and no selector-policy failure", report)
		}
	})

	t.Run("non-string version declaration", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "concept.md", "---\ntype: Note\n---\nBody.\n")
		writeValidationFile(t, root, "index.md", "---\nokf_version: 0.2\n---\n# Concepts\n\n* [Concept](concept.md)\n")

		// Act.
		report := ValidatePath(root, nil)

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "index_structure_invalid", SpecRef: "okf-v0.2#12", File: "index.md",
			FieldPath: "okf_version", Severity: SeverityError,
		})
		if report.HasPolicyFailures() {
			t.Fatalf("non-string declaration report = %#v, want reserved error only", report)
		}
	})
}

func TestSelectorPolicyDoesNotShortCircuitBaseConformance(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n# Concepts\n\n* [Invalid](invalid.md)\n")
	writeValidationFile(t, root, "invalid.md", "---\ntitle: Missing type\n---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Spec: bundle.OKFVersion})

	// Assert.
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "version_assertion_failed", SpecRef: "toolkit#version-selector-assertion", File: "index.md",
		FieldPath: "okf_version", Severity: SeverityWarning, PolicyFailure: true,
	})
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "concept_type_invalid", SpecRef: "okf-v0.1#11.2", File: "invalid.md",
		FieldPath: "type", Severity: SeverityError,
	})
	if report.IsConformant() || !report.HasPolicyFailures() || report.ExitCode() != 1 {
		t.Fatalf("report outcome = %#v, want independent conformance and policy failures", report)
	}
}

func TestStrictV02SourcesAndFootnotesHaveStablePaths(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "sources.md", "---\n"+
		"type: Note\n"+
		"sources:\n"+
		"  - { id: source-a, resource: all queries in project X, usage_count: 3 }\n"+
		"  - id: source-a\n"+
		"    resource: ../references/source.md\n"+
		"    usage_count: -1\n"+
		"    usage_window: { from: 2026-06-30, to: 2026-06-01 }\n"+
		"  - { resource: null, title: 5, author: [], last_modified: yesterday }\n"+
		"---\n"+
		"Known.[^source-a] Unknown.[^orphan]\n\n"+
		"`Ignored [^inline-code]`\n\n"+
		"```text\nIgnored [^fenced-code]\n```\n\n"+
		"[^source-a]: Known source\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	if !report.IsConformant() {
		t.Fatalf("report = %#v, strict findings must be warnings", report)
	}
	for _, want := range []struct{ code, path string }{
		{"source_usage_window_missing", "sources[0].usage_count"},
		{"source_id_duplicate", "sources[1].id"},
		{"source_usage_count_invalid", "sources[1].usage_count"},
		{"usage_window_order", "sources[1].usage_window"},
		{"source_resource_missing", "sources[2].resource"},
		{"source_field_invalid", "sources[2].title"},
		{"source_field_invalid", "sources[2].author"},
		{"source_field_invalid", "sources[2].last_modified"},
		{"source_footnote_unknown", "body.footnotes[orphan]"},
		{"source_footnote_definition_missing", "body.footnotes[orphan]"},
	} {
		assertValidationDiagnostic(t, report, want.code, want.path)
	}
	if hasValidationDiagnostic(report, "source_footnote_unknown", "body.footnotes[inline-code]") ||
		hasValidationDiagnostic(report, "source_footnote_unknown", "body.footnotes[fenced-code]") {
		t.Fatalf("diagnostics = %#v, code footnotes should be ignored", report.Diagnostics)
	}
}

func TestUsageCountYAMLIntegerDomainAndEffectiveWindow(t *testing.T) {
	t.Parallel()

	valid := []string{"+1", "1_000", "0x10", "0o10", "0b10", "-0", "18446744073709551615"}
	for _, raw := range valid {
		raw := raw
		t.Run("valid_"+raw, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "count.md", "---\n"+
				"type: Note\n"+
				"usage_window: { from: 2026-01-01, to: 2026-01-31 }\n"+
				"sources: [{ resource: scope, usage_count: "+raw+" }]\n"+
				"---\nBody.\n")

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true})

			// Assert.
			if hasValidationDiagnostic(report, "source_usage_count_invalid", "sources[0].usage_count") {
				t.Fatalf("usage_count %q diagnostics = %#v, want valid YAML non-negative integer", raw, report.Diagnostics)
			}
		})
	}

	for _, raw := range []string{"-1", "1.5", "\"1\"", "1__0", "18446744073709551616"} {
		raw := raw
		t.Run("invalid_"+raw, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "count.md", "---\n"+
				"type: Note\n"+
				"usage_window: { from: 2026-01-01, to: 2026-01-31 }\n"+
				"sources: [{ resource: scope, usage_count: "+raw+" }]\n"+
				"---\nBody.\n")

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true})

			// Assert.
			assertValidationDiagnostic(t, report, "source_usage_count_invalid", "sources[0].usage_count")
		})
	}

	t.Run("malformed override does not inherit shared window", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "window.md", "---\n"+
			"type: Note\n"+
			"usage_window: { from: 2026-01-01, to: 2026-01-31 }\n"+
			"sources:\n"+
			"  - resource: scope\n"+
			"    usage_count: 1\n"+
			"    usage_window: { from: bad, to: 2026-01-31 }\n"+
			"---\nBody.\n")

		// Act.
		report := ValidatePath(root, &ValidatorConfig{Strict: true})

		// Assert.
		assertValidationDiagnostic(t, report, "usage_window_invalid", "sources[0].usage_window.from")
		assertValidationDiagnostic(t, report, "source_usage_window_missing", "sources[0].usage_count")
	})
}

func TestMalformedTypedScalarsAndEmptyVerifiedAreWarnings(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "typed.md", "---\n"+
		"type: Attested Computation\n"+
		"resource: false\n"+
		"runtime: python\n"+
		"computation: 42\n"+
		"verified: []\n"+
		"sources: [{ resource: true }]\n"+
		"executor: { resource: 7, receipt: [result] }\n"+
		"attester: { resource: false }\n"+
		"---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	for _, want := range []struct{ code, path string }{
		{"path_value_invalid", "resource"},
		{"computation_invalid", "computation"},
		{"verified_invalid", "verified"},
		{"source_resource_missing", "sources[0].resource"},
		{"executor_resource_missing", "executor.resource"},
		{"attester_resource_missing", "attester.resource"},
	} {
		assertValidationDiagnostic(t, report, want.code, want.path)
	}
}

func TestStrictV02GeneratedVerifiedAndLegacyFallback(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "events.md", "---\n"+
		"type: Note\n"+
		"timestamp: invalid-but-shadowed\n"+
		"generated: { by: team:writer, at: yesterday }\n"+
		"verified:\n"+
		"  - { by: human:reviewer, at: 2026-06-25T09:00:00Z }\n"+
		"  - { by: process:nightly }\n"+
		"---\nBody.\n")
	writeValidationFile(t, root, "bare.md", "---\n"+
		"type: Note\n"+
		"verified: { by: human:reviewer, at: 2026-06-25T09:00:00Z }\n"+
		"---\nBody.\n")
	writeValidationFile(t, root, "fallback.md", "---\n"+
		"type: Note\n"+
		"timestamp: invalid\n"+
		"---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	for _, want := range []struct{ code, path string }{
		{"actor_invalid", "generated.by"},
		{"generated_at_invalid", "generated.at"},
		{"verification_at_missing", "verified[1].at"},
	} {
		assertValidationDiagnostic(t, report, want.code, want.path)
	}
	if hasValidationDiagnostic(report, "timestamp_invalid", "timestamp") {
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.File == "events.md" && diagnostic.Code == "timestamp_invalid" {
				t.Fatalf("diagnostics = %#v, generated presence must suppress timestamp fallback", report.Diagnostics)
			}
		}
	}
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "timestamp_invalid", SpecRef: "okf-v0.2#13.1", File: "fallback.md",
		FieldPath: "timestamp", Severity: SeverityWarning,
	})
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.File == "bare.md" {
			t.Fatalf("bare verified mapping diagnostics = %#v, want normalized clean event", report.Diagnostics)
		}
	}
}

func TestStrictV02LegacyTimestampFallbackIsVersionIndependent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		index       string
		spec        string
		wantWarning bool
		wantSource  bundle.VersionSource
	}{
		{name: "no root index default", wantWarning: true, wantSource: bundle.VersionSourceDefault},
		{name: "root index without declaration", index: "---\ntitle: Root\n---\n# Notes\n", wantWarning: true, wantSource: bundle.VersionSourceDefault},
		{name: "explicit v0.2 selector", spec: bundle.OKFVersion, wantWarning: true, wantSource: bundle.VersionSourceExplicit},
		{name: "declared v0.2", index: "---\nokf_version: \"0.2\"\n---\n# Notes\n", wantWarning: true, wantSource: bundle.VersionSourceDeclared},
		{name: "matching declaration and selector", index: "---\nokf_version: \"0.2\"\n---\n# Notes\n", spec: bundle.OKFVersion, wantWarning: true, wantSource: bundle.VersionSourceExplicit},
		{name: "auto selector remains default", spec: "auto", wantWarning: true, wantSource: bundle.VersionSourceDefault},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			if tt.index != "" {
				writeValidationFile(t, root, "index.md", tt.index)
			}
			writeValidationFile(t, root, "timestamp.md", "---\ntype: Note\ntimestamp: invalid\n---\nBody.\n")

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true, Spec: tt.spec})

			// Assert.
			gotWarning := false
			for _, diagnostic := range report.Diagnostics {
				if diagnostic.File == "timestamp.md" && diagnostic.Code == "timestamp_invalid" {
					gotWarning = true
				}
			}
			if gotWarning != tt.wantWarning || report.Version.Source != tt.wantSource {
				t.Fatalf("timestamp warning/source = (%v, %q), want (%v, %q); diagnostics=%#v", gotWarning, report.Version.Source, tt.wantWarning, tt.wantSource, report.Diagnostics)
			}
		})
	}
}

func TestRFC3339ScalarTagsAreSharedAcrossLegacyAndV02(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, tag, value string
		want             bool
	}{
		{name: "string", tag: "!!str", value: "2026-07-29T00:00:00Z", want: true},
		{name: "timestamp", tag: "!!timestamp", value: "2026-07-29T00:00:00Z", want: true},
		{name: "malformed string", tag: "!!str", value: "2026-07-29"},
		{name: "integer tag", tag: "!!int", value: "20260729"},
		{name: "boolean tag", tag: "!!bool", value: "true"},
	} {
		tt := tt
		t.Run("predicate/"+tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			node := &yaml.Node{Kind: yaml.ScalarNode, Tag: tt.tag, Value: tt.value}

			// Act.
			got := validRFC3339Scalar(node)

			// Assert.
			if got != tt.want {
				t.Fatalf("validRFC3339Scalar(%s %q) = %v, want %v", tt.tag, tt.value, got, tt.want)
			}
		})
	}

	t.Run("v0.2 event and fallback fields", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "generated.md", "---\ntype: Note\ngenerated: { by: process:build, at: !!int 20260729 }\n---\nBody.\n")
		writeValidationFile(t, root, "verified.md", "---\ntype: Note\nverified: { by: human:reviewer, at: !!bool true }\n---\nBody.\n")
		writeValidationFile(t, root, "fallback.md", "---\ntype: Note\ntimestamp: !!int 20260729\n---\nBody.\n")
		writeValidationFile(t, root, "valid.md", "---\ntype: Note\ngenerated: { by: process:build, at: !!timestamp 2026-07-29T00:00:00Z }\nverified: { by: human:reviewer, at: !!str 2026-07-29T00:00:00Z }\n---\nBody.\n")

		// Act.
		report := ValidatePath(root, &ValidatorConfig{Strict: true})

		// Assert.
		for _, want := range []Diagnostic{
			{Code: "generated_at_invalid", SpecRef: "okf-v0.2#5.2", File: "generated.md", FieldPath: "generated.at", Severity: SeverityWarning},
			{Code: "verification_at_invalid", SpecRef: "okf-v0.2#5.2", File: "verified.md", FieldPath: "verified[0].at", Severity: SeverityWarning},
			{Code: "timestamp_invalid", SpecRef: "okf-v0.2#13.1", File: "fallback.md", FieldPath: "timestamp", Severity: SeverityWarning},
		} {
			assertExactValidationDiagnostic(t, report, want)
		}
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.File == "valid.md" {
				t.Fatalf("valid explicit RFC3339 tags produced diagnostic: %#v", diagnostic)
			}
		}
	})

	t.Run("legacy timestamp", func(t *testing.T) {
		t.Parallel()

		// Arrange.
		root := t.TempDir()
		writeValidationFile(t, root, "index.md", "---\nokf_version: \"0.1\"\n---\n# Notes\n\n* [Legacy](legacy.md)\n")
		writeValidationFile(t, root, "legacy.md", "---\ntype: Note\ntitle: Legacy\ndescription: Legacy\ntags: [legacy]\ntimestamp: !!int 20260729\n---\nBody.\n")

		// Act.
		report := ValidatePath(root, &ValidatorConfig{Strict: true})

		// Assert.
		assertExactValidationDiagnostic(t, report, Diagnostic{
			Code: "timestamp_invalid", SpecRef: "okf-v0.1#4.1", File: "legacy.md",
			FieldPath: "timestamp", Severity: SeverityWarning,
		})
	})
}

func TestStrictLegacyCitationsFallbackIsVersionAndPresenceAware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, declaration, spec, sources, body string
		wantWarning                            bool
		wantRef                                string
	}{
		{name: "numeric marker alone is not legacy representation", body: "Claim [1].\n"},
		{name: "present sources suppress fallback", sources: "sources: []\n", body: "Claim [1].\n"},
		{name: "declared v0.2 does not enable numeric markers", declaration: "0.2", body: "Claim [1].\n"},
		{name: "explicit v0.2 does not enable numeric markers", spec: "0.2", body: "Claim [1].\n"},
		{name: "declared v0.1 numeric marker alone", declaration: "0.1", body: "Claim [1].\n"},
		{name: "explicit v0.1 numeric marker alone", spec: "0.1", body: "Claim [1].\n"},
		{name: "declared v0.2 actual malformed citations", declaration: "0.2", body: "# Citations\n\nnot a citation\n", wantWarning: true, wantRef: "okf-v0.2#13.1"},
		{name: "explicit v0.1 actual malformed citations", spec: "0.1", body: "# Citations\n\nnot a citation\n", wantWarning: true, wantRef: "okf-v0.1#4.2"},
		{name: "code marker ignored", body: "```text\nClaim [1].\n```\n"},
		{name: "valid default fallback", body: "Claim [1].\n\n# Citations\n\n[1] https://example.com/source\n"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			if tt.declaration != "" {
				writeValidationFile(t, root, "index.md", "---\nokf_version: \""+tt.declaration+"\"\n---\n# Notes\n\n* [Concept](concept.md)\n")
			}
			writeValidationFile(t, root, "concept.md", "---\ntype: Note\n"+tt.sources+"---\n"+tt.body)

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true, Spec: tt.spec})

			// Assert.
			got := hasValidationDiagnostic(report, "citation_integrity_invalid", "body.# Citations")
			if got != tt.wantWarning {
				t.Fatalf("citation_integrity_invalid = %v, want %v: %#v", got, tt.wantWarning, report.Diagnostics)
			}
			if tt.wantWarning {
				assertExactValidationDiagnostic(t, report, Diagnostic{
					Code: "citation_integrity_invalid", SpecRef: tt.wantRef, File: "concept.md",
					FieldPath: "body.# Citations", Severity: SeverityWarning,
				})
			}
		})
	}
}

func TestStrictV02AcceptsAcyclicSemanticYAMLAliasesAndMerges(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "aliased.md", `---
type: Attested Computation
runtime: sql
tag: &tag scope
tags: [*tag]
window: &window { from: 2026-07-01, to: 2026-07-29 }
source: &source
  resource: source scope
  usage_count: 1
  usage_window: *window
sources: [*source]
event: &event { by: "process:validator", at: 2026-07-29T00:00:00Z }
generated: *event
verified: [*event]
parameter: &parameter { name: input, type: string, required: true }
parameters: [*parameter]
executor_defaults: &executor_defaults
  resource: run.md
  receipt: &receipt [result]
executor:
  <<: *executor_defaults
attester_value: &attester_value { resource: check.py }
attester: *attester_value
status_defaults: &status_defaults { status: stable }
<<: *status_defaults
---
# Computation

`+"```sql"+`
select 1
`+"```"+`
`)
	writeValidationFile(t, root, "merged-sources.md", `---
type: Note
provenance: &provenance
  sources: []
<<: *provenance
---
Claim [1].
`)

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	for _, file := range []string{"aliased.md", "merged-sources.md"} {
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.File == file {
				t.Fatalf("%s diagnostics = %#v, want semantic alias/merge strict-clean", file, report.Diagnostics)
			}
		}
	}
	if len(report.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v, want semantic aliases and merges strict-clean", report.Diagnostics)
	}
}

func TestStrictV02RejectsSemanticYAMLAliasCycleAtBaseLayer(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "cycle.md", `---
type: Note
source_cycle: &source_cycle
  <<: *source_cycle
sources:
  - <<: *source_cycle
---
Body.
`)

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	if len(report.Diagnostics) != 1 {
		t.Fatalf("diagnostics = %#v, want exactly one base-layer rejection", report.Diagnostics)
	}
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "concept_unparseable", SpecRef: "okf-v0.2#11.1", File: "cycle.md",
		FieldPath: "frontmatter", Severity: SeverityError,
	})
	if !strings.Contains(report.Diagnostics[0].Message, "yaml_alias_cycle") {
		t.Fatalf("diagnostic message = %q, want stable yaml_alias_cycle evidence", report.Diagnostics[0].Message)
	}
}

func TestStrictV02ActorValidationUsesSharedDomainPredicate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		actor     string
		wantValid bool
	}{
		{name: "human", actor: "human:id", wantValid: true},
		{name: "process", actor: "process:id", wantValid: true},
		{name: "producer version", actor: "producer/version", wantValid: true},
		{name: "version with colon", actor: "producer/v:1", wantValid: true},
		{name: "256 byte boundary", actor: "p/" + strings.Repeat("v", 254), wantValid: true},
		{name: "namespaced extra colon", actor: "human:a:b", wantValid: true},
		{name: "producer colon", actor: "producer:name/v1", wantValid: true},
		{name: "extra slash", actor: "producer/version/extra", wantValid: false},
		{name: "internal ascii space", actor: "producer/v 1", wantValid: true},
		{name: "internal unicode non-breaking space", actor: "producer/v\u00a01", wantValid: true},
		{name: "c0 control", actor: "producer/v\x1f1", wantValid: false},
		{name: "delete control", actor: "producer/v\x7f1", wantValid: false},
		{name: "invalid utf8", actor: string([]byte{'p', '/', 0xff}), wantValid: false},
		{name: "257 bytes", actor: "p/" + strings.Repeat("v", 255), wantValid: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: tt.actor}

			// Act.
			gotBundle := bundle.ValidActor(tt.actor)
			gotValidator := validActorScalar(node)

			// Assert.
			if gotBundle != tt.wantValid || gotValidator != tt.wantValid {
				t.Fatalf("validity = (bundle=%v validator=%v), want %v for actor bytes %x", gotBundle, gotValidator, tt.wantValid, []byte(tt.actor))
			}
		})
	}

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "actor.md", "---\ntype: Note\ngenerated:\n  by: \" human:id\"\n---\nBody.\n")
	writeValidationFile(t, root, "verified.md", "---\ntype: Note\nverified:\n  by: \"producer/version/extra\"\n  at: 2026-01-01T00:00:00Z\n---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "actor_invalid", SpecRef: "okf-v0.2#7", File: "actor.md",
		FieldPath: "generated.by", Severity: SeverityWarning,
	})
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "actor_invalid", SpecRef: "okf-v0.2#7", File: "verified.md",
		FieldPath: "verified[0].by", Severity: SeverityWarning,
	})
}

func TestStrictV02ReportsNormalizedSourceIDCollision(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "collision.md", `---
type: Note
sources:
  - id: Spec
    resource: https://example.test/one
    author: team:finance
  - id: spec
    resource: https://example.test/two
---
Body.
`)

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "normalized_footnote_label_collision", SpecRef: "toolkit#source-footnote-integrity",
		File: "collision.md", FieldPath: "sources[1].id", Severity: SeverityWarning,
	})
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "normalized_footnote_label_collision" &&
			!strings.Contains(diagnostic.Message, "action: disambiguate_citation_entry") {
			t.Fatalf("collision diagnostic message = %q, want exact remediation action", diagnostic.Message)
		}
		if diagnostic.Code == "actor_invalid" && strings.HasPrefix(diagnostic.FieldPath, "sources[") {
			t.Fatalf("sources[].author incorrectly used actor grammar: %#v", diagnostic)
		}
	}
}

func TestStrictV02StaleBoundaryUsesInjectedReferenceDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		reference string
		wantStale bool
	}{
		{name: "before", reference: "2026-06-29"},
		{name: "equal", reference: "2026-06-30", wantStale: true},
		{name: "after", reference: "2026-07-01", wantStale: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "freshness.md", "---\ntype: Note\nstale_after: 2026-06-30\n---\nBody.\n")
			reference, err := time.Parse(time.DateOnly, tt.reference)
			if err != nil {
				t.Fatal(err)
			}

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true, ReferenceDate: reference})

			// Assert.
			if got := hasValidationDiagnostic(report, "stale", "stale_after"); got != tt.wantStale {
				t.Fatalf("stale diagnostic = %v, want %v; diagnostics = %#v", got, tt.wantStale, report.Diagnostics)
			}
		})
	}
}

func TestStrictV02AttestedComputationVariants(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "inline.md", "---\n"+
		"type: Attested Computation\n"+
		"runtime: custom-runtime\n"+
		"parameters: [{ name: input, type: opaque, required: true }]\n"+
		"executor: { resource: ./run.md, receipt: [result] }\n"+
		"attester: { resource: /references/check.py }\n"+
		"---\n# Computation\n\n```text\ncompute(input)\n```\n")
	writeValidationFile(t, root, "file.md", "---\n"+
		"type: Attested Computation\n"+
		"runtime: python\n"+
		"computation: ../references/compute.py\n"+
		"executor: { resource: runbook scope, receipt: [result] }\n"+
		"attester: { resource: https://example.com/check.py }\n"+
		"---\nBody.\n")
	writeValidationFile(t, root, "bad.md", "---\n"+
		"type: Attested Computation\n"+
		"parameters:\n"+
		"  - { name: p, type: string, required: yes }\n"+
		"  - { name: p, required: false }\n"+
		"computation: ./query.sql\n"+
		"executor: { resource: ./run.md, receipt: [result, result, ''] }\n"+
		"attester: nope\n"+
		"---\n# Computation\n\n```sql\nselect 1\n```\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	for _, file := range []string{"inline.md", "file.md"} {
		for _, diagnostic := range report.Diagnostics {
			if diagnostic.File == file {
				t.Fatalf("%s diagnostics = %#v, want strict-clean", file, report.Diagnostics)
			}
		}
	}
	for _, want := range []struct{ code, path string }{
		{"runtime_missing", "runtime"},
		{"parameter_required_invalid", "parameters[0].required"},
		{"parameter_name_duplicate", "parameters[1].name"},
		{"parameter_type_missing", "parameters[1].type"},
		{"computation_conflict", "computation"},
		{"receipt_field_duplicate", "executor.receipt[1]"},
		{"receipt_field_invalid", "executor.receipt[2]"},
		{"attester_invalid", "attester"},
	} {
		assertValidationDiagnostic(t, report, want.code, want.path)
	}
	for _, want := range []Diagnostic{
		{Code: "runtime_missing", SpecRef: "okf-v0.2#10.2", File: "bad.md", FieldPath: "runtime", Severity: SeverityWarning},
		{Code: "parameter_name_duplicate", SpecRef: "toolkit#computation-parameter-unique", File: "bad.md", FieldPath: "parameters[1].name", Severity: SeverityWarning},
		{Code: "receipt_field_duplicate", SpecRef: "toolkit#receipt-field-unique", File: "bad.md", FieldPath: "executor.receipt[1]", Severity: SeverityWarning},
		{Code: "attester_invalid", SpecRef: "okf-v0.2#10.2", File: "bad.md", FieldPath: "attester", Severity: SeverityWarning},
	} {
		assertExactValidationDiagnostic(t, report, want)
	}
}

func TestInlineComputationUsesParserOwnedClosedFence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		computation string
		wantCode    string
		wantPath    string
	}{
		{name: "direct closed", body: "# Computation\n\n```sql\nselect 1\n```\n"},
		{name: "indented code is not a fence", body: "# Computation\n\n    select 1\n", wantCode: "computation_inline_missing"},
		{name: "nested heading", body: "## Computation\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "heading inside code", body: "```markdown\n# Computation\n```\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "unclosed", body: "# Computation\n\n```sql\nselect 1\n", wantCode: "computation_inline_malformed"},
		{name: "prose intervenes", body: "# Computation\n\nExplanation.\n\n```sql\nselect 1\n```\n"},
		{name: "lowercase heading rejected", body: "# computation\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "uppercase heading rejected", body: "# COMPUTATION\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "setext heading rejected", body: "Computation\n===\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "styled heading rejected", body: "# *Computation*\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "closing hash rejected", body: "# Computation #\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "multiple heading spaces rejected", body: "#  Computation\n\n```sql\nselect 1\n```\n", wantCode: "computation_inline_missing"},
		{name: "multiple owners", body: "# Computation\n\n```\none\n```\n# Computation\n\n```\ntwo\n```\n", wantCode: "computation_inline_multiple"},
		{name: "multiple sections one fence", body: "# Computation\n\nExplanation only.\n# Computation\n\n```\ntwo\n```\n", wantCode: "computation_inline_multiple"},
		{name: "multiple fences one section", body: "# Computation\n\n```\none\n```\n\n```\ntwo\n```\n", wantCode: "computation_inline_multiple"},
		{name: "blockquote fence is malformed inline", body: "# Computation\n\n> ```sql\n> select 1\n> ```\n", wantCode: "computation_inline_malformed"},
		{name: "list fence is malformed inline", body: "# Computation\n\n- ```sql\n  select 1\n  ```\n", wantCode: "computation_inline_malformed"},
		{name: "file mode retains unclosed malformed candidate", computation: "./query.sql", body: "# Computation\n\n```sql\nselect 1\n", wantCode: "computation_inline_malformed"},
		{name: "file mode retains nested malformed candidate", computation: "./query.sql", body: "# Computation\n\n> ```sql\n> select 1\n> ```\n", wantCode: "computation_inline_malformed"},
		{name: "file mode permits prose-only section", computation: "./query.sql", body: "# Computation\n\nExplanation only.\n"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			computation := ""
			if tt.computation != "" {
				computation = "computation: " + tt.computation + "\n"
			}
			writeValidationFile(t, root, "computation.md", "---\n"+
				"type: Attested Computation\n"+
				"runtime: custom\n"+
				computation+
				"executor: { resource: run.md, receipt: [result] }\n"+
				"attester: { resource: check.py }\n"+
				"---\n"+tt.body)

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true})

			// Assert.
			if tt.wantCode == "" {
				for _, diagnostic := range report.Diagnostics {
					if diagnostic.Code == "computation_conflict" || diagnostic.Code == "computation_inline_malformed" ||
						diagnostic.Code == "computation_inline_missing" || diagnostic.Code == "computation_inline_multiple" {
						t.Fatalf("diagnostics = %#v, want parser-owned computation fence", report.Diagnostics)
					}
				}
				return
			}
			wantPath := tt.wantPath
			if wantPath == "" {
				wantPath = "body.# Computation"
			}
			assertExactValidationDiagnostic(t, report, Diagnostic{
				Code: tt.wantCode, SpecRef: "okf-v0.2#10.3", File: "computation.md",
				FieldPath: wantPath, Severity: SeverityWarning,
			})
		})
	}
}

func TestMalformedComputationFieldDoesNotSuppressValidInlinePayload(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, field string
	}{
		{name: "blank", field: "computation: \"\"\n"},
		{name: "non-string", field: "computation: 17\n"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "computation.md",
				"---\ntype: Attested Computation\nruntime: custom\n"+tt.field+
					"executor: {resource: run.md, receipt: [result]}\n"+
					"attester: {resource: check.py}\n---\n"+
					"# Computation\n```\nSELECT 1;\n```\n",
			)

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true})

			// Assert.
			assertExactValidationDiagnostic(t, report, Diagnostic{
				Code: "computation_invalid", SpecRef: "okf-v0.2#6.2", File: "computation.md",
				FieldPath: "computation", Severity: SeverityWarning,
			})
			for _, diagnostic := range report.Diagnostics {
				if strings.HasPrefix(diagnostic.Code, "computation_inline_") || diagnostic.Code == "computation_conflict" {
					t.Fatalf("malformed field suppressed usable inline payload: %#v", report.Diagnostics)
				}
			}
		})
	}
}

func TestFutureBestEffortSkipsUnsafeOptionalPolicy(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "---\nokf_version: \"9.0\"\n---\n# Concepts\n\n* [Future](future.md)\n")
	writeValidationFile(t, root, "future.md", "---\ntype: Attested Computation\ntags: nope\nsources: nope\ngenerated: []\nverified: nope\nstatus: unknown-future-status\nstale_after: tomorrow\n---\nBody.\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	if len(report.Diagnostics) != 0 || !report.IsConformant() {
		t.Fatalf("diagnostics = %#v, future best-effort should retain only base §11 conformance", report.Diagnostics)
	}
}

func TestStrictSpecRefExactMatrix(t *testing.T) {
	t.Parallel()

	// Arrange.
	groups := map[string][]string{
		"okf-v0.2#4.1": {
			"tags_invalid",
		},
		"okf-v0.2#5.1": {
			"sources_invalid", "source_invalid", "source_resource_missing", "source_field_invalid", "usage_window_invalid",
		},
		"okf-v0.2#5.2": {
			"generated_invalid", "generated_by_missing", "generated_at_invalid", "verified_invalid",
			"verification_invalid", "verification_by_missing", "verification_at_missing", "verification_at_invalid",
		},
		"okf-v0.2#5.4":  {"status_invalid"},
		"okf-v0.2#5.5":  {"stale_after_invalid", "stale"},
		"okf-v0.2#6.2":  {"computation_invalid", "path_value_invalid"},
		"okf-v0.2#7":    {"actor_invalid"},
		"okf-v0.2#10.2": {"runtime_missing", "runtime_invalid", "parameters_invalid", "parameter_invalid", "parameter_name_missing", "parameter_type_missing", "parameter_required_invalid", "executor_invalid", "executor_resource_missing", "receipt_invalid", "receipt_field_invalid", "attester_invalid", "attester_resource_missing"},
		"okf-v0.2#10.3": {"computation_conflict", "computation_inline_malformed", "computation_inline_missing", "computation_inline_multiple"},
		"okf-v0.2#13.1": {"timestamp_invalid"},
		"toolkit#sources-id-unique": {
			"source_id_duplicate",
		},
		"toolkit#sources-usage-profile": {
			"source_usage_count_invalid", "source_usage_window_missing", "usage_window_order",
		},
		"toolkit#source-footnote-integrity": {
			"source_footnote_unknown", "source_footnote_definition_missing", "source_footnote_definition_duplicate",
		},
		"toolkit#computation-parameter-unique": {"parameter_name_duplicate"},
		"toolkit#receipt-field-unique":         {"receipt_field_duplicate"},
		"toolkit#computation-usability":        {"executor_missing", "receipt_missing", "attester_missing"},
	}

	// Act + Assert.
	for wantRef, codes := range groups {
		for _, code := range codes {
			if got := strictSpecRef(code); got != wantRef {
				t.Fatalf("strictSpecRef(%q) = %q, want %q", code, got, wantRef)
			}
		}
	}
}

func assertValidationDiagnostic(t *testing.T, report Report, code, fieldPath string) {
	t.Helper()
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code && diagnostic.FieldPath == fieldPath {
			if diagnostic.SpecRef == "" {
				t.Fatalf("diagnostic = %#v, want non-empty spec_ref", diagnostic)
			}
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want code=%q field_path=%q", report.Diagnostics, code, fieldPath)
}

func hasValidationDiagnostic(report Report, code, fieldPath string) bool {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == code && diagnostic.FieldPath == fieldPath {
			return true
		}
	}
	return false
}

func assertExactValidationDiagnostic(t *testing.T, report Report, want Diagnostic) {
	t.Helper()
	for _, got := range report.Diagnostics {
		if got.Code == want.Code && got.File == want.File && got.FieldPath == want.FieldPath {
			if got.SpecRef != want.SpecRef || got.Severity != want.Severity || got.PolicyFailure != want.PolicyFailure {
				t.Fatalf("diagnostic = %#v, want code=%q spec_ref=%q file=%q field_path=%q severity=%s policy_failure=%v", got, want.Code, want.SpecRef, want.File, want.FieldPath, want.Severity, want.PolicyFailure)
			}
			return
		}
	}
	t.Fatalf("diagnostics = %#v, missing code=%q file=%q field_path=%q", report.Diagnostics, want.Code, want.File, want.FieldPath)
}
