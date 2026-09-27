package validator

import (
	"strings"
	"testing"
)

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
			report := ValidatePath(root, nil)

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
	base := ValidatePath(root, nil)
	strict := ValidatePath(root, &ValidatorConfig{Strict: true})

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
