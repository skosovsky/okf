package validator

import (
	"strings"
	"testing"
)

func TestOKF03IndexAcceptsLinkedTableEntries(t *testing.T) {
	const table = "# Catalog\n\n## Guides\n\n| Guide | Summary |\n| --- | --- |\n| [Rule][rule] | A rule. |\n\n[rule]: rule.md\n"
	for _, tt := range []struct {
		name, version, body string
		conformant          bool
	}{
		{"table rows", "0.3", table, true},
		{"legacy table rows", "0.2", table, false},
		{"table header only", "0.3", "# Guides\n\n| [Rule](rule.md) | Summary |\n| --- | --- |\n| No link | A rule. |\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "index.md", "---\nokf_version: '"+tt.version+"'\n---\n"+tt.body)
			writeValidationFile(t, root, "rule.md", "---\ntype: Guide\n---\nRule.\n")

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true})

			// Assert.
			if report.IsConformant() != tt.conformant || tt.conformant && len(report.Diagnostics) != 0 {
				t.Fatalf("conformant = %t, diagnostics = %+v", report.IsConformant(), report.Diagnostics)
			}
		})
	}
}

func TestOKF03OrphanCoverageSkipsCompanionMarkdown(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n# Guides\n\n- [Rule](rule.md)\n")
	writeValidationFile(t, root, "rule.md", "---\ntype: Guide\n---\nRule.\n")
	writeValidationFile(t, root, "orphan.md", "---\ntype: Guide\n---\nUnlisted guide.\n")
	writeValidationFile(t, root, "README.md", "# Companion\n")
	writeValidationFile(t, root, "meta/README.md", "# Another companion\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{CheckOrphans: true})

	// Assert.
	var unlisted []string
	for _, d := range report.Diagnostics {
		switch d.Code {
		case "orphan_unlisted":
			unlisted = append(unlisted, d.File)
		case "orphan_index_missing":
			t.Fatalf("companion Markdown triggered index coverage: %+v", d)
		}
	}
	if len(unlisted) != 1 || unlisted[0] != "orphan.md" || !report.IsConformant() {
		t.Fatalf("orphan coverage: %+v", report.Diagnostics)
	}
}

func TestOKF03IndexAllowsATitleBeforeTopLevelGroups(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "---\nokf_version: '0.3'\n---\n# Catalog title\n\n# Guides\n\n- [Nested](nested/)\n")
	writeValidationFile(t, root, "nested/index.md", "# Subcatalog title\n\n# Guides\n\n- [Rule](rule.md)\n")
	writeValidationFile(t, root, "nested/rule.md", "---\ntype: Guide\n---\nRule.\n")

	report := ValidatePath(root, &ValidatorConfig{Strict: true, CheckLinks: true, CheckOrphans: true})
	if !report.IsConformant() || len(report.Diagnostics) != 0 {
		t.Fatalf("catalog with top-level title and groups: %+v", report.Diagnostics)
	}
	writeValidationFile(t, root, "nested/index.md", "# Title\n\n# Empty group\n\n# Guides\n\n- [Rule](rule.md)\n")
	report = ValidatePath(root, nil)
	assertExactValidationDiagnostic(t, report, Diagnostic{
		Code: "index_structure_invalid", SpecRef: "okf-v0.3#11.3",
		File: "nested/index.md", FieldPath: "body", Severity: SeverityError,
	})
	if report.ErrorCount() != 1 || !strings.Contains(report.Diagnostics[0].Message, "Empty group") {
		t.Fatalf("empty group diagnostics: %+v", report.Diagnostics)
	}
}

func TestInteroperabilityReservedStructureAndExtensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, index, log    string
		wantCode, wantField string
	}{
		{"title and introduction", "---\nokf_version: '0.2'\nupkeep: enforced\ncustom: retained\n---\n# Bundle title\n\nAn introduction.\n\n# Concepts\n\n* [A](a.md)\n", "", "", ""},
		{"title and H2 group", "# Bundle title\n\n## Concepts\n\n* [A](a.md)\n", "", "", ""},
		{"empty group", "# Bundle title\n\n## Empty\n\n## Concepts\n\n* [A](a.md)\n", "", "index_structure_invalid", "body"},
		{"empty first H1 group", "# Empty\n\n# Concepts\n\n* [A](a.md)\n", "", "index_structure_invalid", "body"},
		{"empty listing", "# Bundle title\n", "", "index_structure_invalid", "body"},
		{"malformed entry", "# Concepts\n\n* Plain entry\n", "", "index_structure_invalid", "body"},
		{"duplicate declaration", "---\nokf_version: '0.2'\nokf_version: '0.2'\n---\n# Concepts\n\n* [A](a.md)\n", "", "index_structure_invalid", "okf_version"},
		{"malformed declaration", "---\nokf_version: [0, 2]\ncustom: retained\n---\n# Concepts\n\n* [A](a.md)\n", "", "index_structure_invalid", "okf_version"},
		{"missing declaration", "---\ncustom: retained\n---\n# Concepts\n\n* [A](a.md)\n", "", "index_structure_invalid", "okf_version"},
		{"wrapped LF log", "# Concepts\n\n* [A](a.md)\n", "# Log\n\n## 2026-09-21\n* First\n  indented\nlazy continuation\n", "", ""},
		{"wrapped CRLF log", "# Concepts\n\n* [A](a.md)\n", strings.ReplaceAll("# Log\n\n## 2026-09-21\n* First\n  indented\nlazy continuation\n", "\n", "\r\n"), "", ""},
		{"separate log paragraph", "# Concepts\n\n* [A](a.md)\n", "# Log\n\n## 2026-09-21\n* First\n\nSeparate paragraph\n", "log_structure_invalid", "body"},
		{"nested log list", "# Concepts\n\n* [A](a.md)\n", "# Log\n\n## 2026-09-21\n* First\n  * nested\n", "log_structure_invalid", "body"},
		{"invalid log date", "# Concepts\n\n* [A](a.md)\n", "# Log\n\n## September 21\n* First\n", "log_structure_invalid", "body"},
		{"out of order log dates", "# Concepts\n\n* [A](a.md)\n", "# Log\n\n## 2026-09-20\n* Earlier\n\n## 2026-09-21\n* Later\n", "log_structure_invalid", "body"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
			writeValidationFile(t, root, "index.md", test.index)
			if test.log != "" {
				writeValidationFile(t, root, "log.md", test.log)
			}

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Spec: "0.2"})

			// Assert.
			if test.wantCode == "" {
				if !report.IsConformant() {
					t.Fatalf("diagnostics = %#v", report.Diagnostics)
				}
				return
			}
			for _, diagnostic := range report.Of(SeverityError) {
				if diagnostic.Code == test.wantCode && diagnostic.FieldPath == test.wantField {
					return
				}
			}
			t.Fatalf("diagnostics = %#v, want %s at %s", report.Diagnostics, test.wantCode, test.wantField)
		})
	}
}

func TestInteroperabilityNestedIndexFrontmatterRemainsInvalid(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "# Root\n\n* [Nested](nested/)\n")
	writeValidationFile(t, root, "nested/index.md", "---\ncustom: retained\n---\n# Nested\n\n* [A](a.md)\n")
	writeValidationFile(t, root, "nested/a.md", "---\ntype: Note\n---\nBody.\n")

	// Act.
	report := ValidatePath(root, nil)

	// Assert.
	for _, diagnostic := range report.Of(SeverityError) {
		if diagnostic.Code == "index_structure_invalid" && diagnostic.File == "nested/index.md" && diagnostic.FieldPath == "frontmatter" {
			return
		}
	}
	t.Fatalf("diagnostics = %#v, want nested frontmatter error", report.Diagnostics)
}

func TestInteroperabilityInlineCodeFootnotesDoNotCreateStrictWarnings(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "# Concepts\n\n* [A](a.md)\n")
	writeValidationFile(t, root, "a.md", "---\ntype: Note\nsources:\n  - id: actual\n    resource: https://example.test/source\n---\nLiteral `[^code]` and multiline `first\n[^multiline]`; claim [^actual].\n\n[^actual]: source\n")

	// Act.
	report := ValidatePath(root, &ValidatorConfig{Strict: true})

	// Assert.
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Code == "source_footnote_unknown" || diagnostic.Code == "source_footnote_definition_missing" {
			t.Fatalf("false footnote diagnostic: %#v", report.Diagnostics)
		}
	}
}

func TestSourceFootnoteDefinitionCanResolveThroughOKF03Frontmatter(t *testing.T) {
	for _, tt := range []struct {
		version     string
		wantMissing string
	}{
		{"0.3", "body.footnotes[dangling],body.footnotes[incomplete]"},
		{"0.2", "body.footnotes[actual],body.footnotes[dangling],body.footnotes[incomplete]"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			writeValidationFile(t, root, "index.md", "---\nokf_version: '"+tt.version+"'\n---\n# Concepts\n\n- [A](a.md)\n")
			writeValidationFile(t, root, "a.md", `---
type: Note
sources:
  - id: actual
    resource: https://example.test/source
  - id: incomplete
  - id: duplicate
    resource: https://example.test/duplicate
---
Supported claim.[^actual] Dangling claim.[^dangling] Incomplete source.[^incomplete]
Duplicate definition.[^duplicate]

[^duplicate]: One.
[^duplicate]: Two.
`)

			// Act.
			report := ValidatePath(root, &ValidatorConfig{Strict: true})

			// Assert.
			var missing, duplicates []string
			for _, diagnostic := range report.Diagnostics {
				switch diagnostic.Code {
				case "source_footnote_definition_missing":
					missing = append(missing, diagnostic.FieldPath)
				case "source_footnote_definition_duplicate":
					duplicates = append(duplicates, diagnostic.FieldPath)
				}
			}
			if strings.Join(missing, ",") != tt.wantMissing ||
				strings.Join(duplicates, ",") != "body.footnotes[duplicate]" {
				t.Fatalf("missing = %v, duplicate = %v, diagnostics = %+v", missing, duplicates, report.Diagnostics)
			}
		})
	}
}

func TestInteroperabilityActorShapesRemainAdvisory(t *testing.T) {
	t.Parallel()

	// Arrange.
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "# Concepts\n\n* [Agent](agent.md)\n* [Producer](producer.md)\n* [Human](human.md)\n* [Process](process.md)\n")
	for name, actor := range map[string]string{
		"agent": "agent:claude-opus-5", "producer": "producer/version", "human": "human:alice", "process": "process:import",
	} {
		writeValidationFile(t, root, name+".md", "---\ntype: Note\ngenerated: {by: '"+actor+"', at: '2026-09-21T00:00:00Z'}\n---\nBody.\n")
	}

	// Act.
	base := ValidatePath(root, &ValidatorConfig{Spec: "0.2"})
	strict := ValidatePath(root, &ValidatorConfig{Spec: "0.2", Strict: true})

	// Assert.
	if !base.IsConformant() || len(base.Of(SeverityWarning)) != 0 || !strict.IsConformant() {
		t.Fatalf("base=%#v strict=%#v", base.Diagnostics, strict.Diagnostics)
	}
	var actorWarnings int
	for _, diagnostic := range strict.Of(SeverityWarning) {
		if diagnostic.Code == "actor_invalid" && diagnostic.File == "agent.md" && diagnostic.FieldPath == "generated.by" {
			actorWarnings++
		} else {
			t.Fatalf("unexpected strict warning: %#v", diagnostic)
		}
	}
	if actorWarnings != 1 {
		t.Fatalf("actor warnings = %d, want 1", actorWarnings)
	}
}
