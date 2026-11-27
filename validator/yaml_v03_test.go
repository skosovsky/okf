package validator

import (
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestValidationUsesSelectedDiscoveryOnRetainedBundle(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "nested/README.md", "# Companion\n")
	writeValidationFile(t, root, "nested/rule.md", "---\ntype: Rule\n---\nBody.\n")
	legacy, err := bundle.LoadBundleWithSpec(root, bundle.OKFVersion)
	if err != nil {
		t.Fatal(err)
	}
	current, err := bundle.LoadBundleWithSpec(root, bundle.OKFVersion03)
	if err != nil {
		t.Fatal(err)
	}

	want := ValidateBundle(legacy, &ValidatorConfig{Spec: bundle.OKFVersion})
	got := ValidateBundle(current, &ValidatorConfig{Spec: bundle.OKFVersion})
	if want.IsConformant() || want.ErrorCount() != got.ErrorCount() ||
		len(got.Diagnostics) != 1 || !reflect.DeepEqual(got.Diagnostics, want.Diagnostics) ||
		got.Diagnostics[0].File != "nested/README.md" {
		t.Fatalf("legacy diagnostics on its own capture = %+v; on 0.3 capture = %+v", want.Diagnostics, got.Diagnostics)
	}

	want = ValidateBundle(current, &ValidatorConfig{Spec: bundle.OKFVersion03})
	got = ValidateBundle(legacy, &ValidatorConfig{Spec: bundle.OKFVersion03})
	if !want.IsConformant() || !got.IsConformant() ||
		len(got.Diagnostics) != 1 || !reflect.DeepEqual(got.Diagnostics, want.Diagnostics) ||
		got.Diagnostics[0].Code != "markdown_not_concept" {
		t.Fatalf("0.3 diagnostics on its own capture = %+v; on legacy capture = %+v", want.Diagnostics, got.Diagnostics)
	}
}

func TestOKF03ChecksDuplicateYAMLKeysAcrossDocumentRoles(t *testing.T) {
	for _, name := range []string{"a.md", "README.md", "index.md"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			index := "---\nokf_version: '0.3'\n---\n# Notes\n\n* [A](a.md)\n"
			writeValidationFile(t, root, "index.md", index)
			writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
			extra := "external: {owner: one, owner: two}\n"
			switch name {
			case "index.md":
				writeValidationFile(t, root, name, strings.Replace(index, "\n---\n", "\n"+extra+"---\n", 1))
			case "a.md":
				writeValidationFile(t, root, name, "---\ntype: Note\n"+extra+"---\nBody.\n")
			default:
				writeValidationFile(t, root, name, "---\n"+extra+"---\nBody.\n")
			}
			report := ValidatePath(root, nil)
			for _, d := range report.Of(SeverityError) {
				if d.File == name && strings.Contains(d.Message, `duplicate YAML key "owner"`) {
					return
				}
			}
			t.Fatalf("want duplicate key in %s, got %+v", name, report.Diagnostics)
		})
	}
}

func TestOKF03AllowsSeparateMappingsAndExplicitMergeOverrides(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\ntype: Open vocabulary type\nbase: &base {owner: one}\nexternal: {<<: *base, owner: two}\n---\nBody.\n")
	if report := ValidatePath(root, &ValidatorConfig{Spec: "0.3"}); !report.IsConformant() {
		t.Fatalf("unexpected conformance error: %+v", report.Diagnostics)
	}
}

func TestOKF03ChecksYAMLKeyEquality(t *testing.T) {
	tests := []struct {
		name, extension string
		duplicate       bool
	}{
		{"radix", "external: {011: one, 0xB: two}", true},
		{"decimal leading zeros", "external: {011: one, 9: two}", false},
		{"large integer", "external: {18446744073709551616: one, 0x10000000000000000: two}", true},
		{"float exponent", "external: {10.00: one, 1e+1: two}", true},
		{"float precision", "external: {0.100000000000000001: one, 0.100000000000000002: two}", false},
		{"float type", "external: {10: one, 10.0: two}", false},
		{"null spelling", "external: {~: one, NULL: two}", true},
		{"boolean spelling", "external: {FALSE: one, False: two}", true},
		{"ordinary core string", `external: {0b10: one, "0b10": two}`, true},
		{"quoted scalar", `external: {true: one, "true": two}`, false},
		{"non-specific string tag", `external: {"ключ": zero, ? ! true : one, true: two}`, false},
		{"non-specific tag after anchor", "external:\n  ? &key # comment\n    ! true\n  : one\n  \"true\": two", true},
		{"explicit scalar tag", `external: {!!int "11": one, 0xB: two}`, true},
		{"sequence alias", "anchor: &key [1, 2]\nexternal: {? *key : one, ? [01, 2] : two}", true},
		{"sequence order", "external: {? [1, 2] : one, ? [2, 1] : two}", false},
		{"mapping order", "external: {? {n: 1, active: true} : one, ? {active: TRUE, n: 01} : two}", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeValidationFile(t, root, "a.md", "---\ntype: Note\n"+tt.extension+"\n---\nBody.\n")

			report := ValidatePath(root, &ValidatorConfig{Spec: "0.3"})

			if !tt.duplicate {
				if !report.IsConformant() {
					t.Fatalf("unexpected conformance error: %+v", report.Diagnostics)
				}
				return
			}
			for _, d := range report.Of(SeverityError) {
				if d.File == "a.md" && strings.Contains(d.Message, "duplicate YAML") {
					return
				}
			}
			t.Fatalf("want a duplicate key error, got %+v", report.Diagnostics)
		})
	}
}

func TestOKF03YAMLKeyEqualityLeavesLegacyBundleReadingIntact(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "a.md", "---\ntype: Note\nexternal: {011: one, 0xB: two}\n---\nBody.\n")
	for _, spec := range []string{"0.1", "0.2"} {
		report := ValidatePath(root, &ValidatorConfig{Spec: spec})
		if !report.IsConformant() {
			t.Fatalf("0.3 key equality leaked into %s: %+v", spec, report.Diagnostics)
		}
	}
}

func TestOKF03ValidatesDirectivesOnceAndAllowsIndexWithoutVersion(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "index.md", "---\n%INDEX keep\n--- # YAML mapping\nlicense: MIT\n---\n# Notes\n\n* [A](a.md)\n")
	writeValidationFile(t, root, "a.md", "---\n%YAML 1.3\n--- # YAML mapping\ntype: Note\n---\nBody.\n")
	writeValidationFile(t, root, "README.md", "---\n%OTHER keep\n--- # YAML mapping\nexternal: retained\n---\nBody.\n")

	report := ValidatePath(root, &ValidatorConfig{Spec: "0.3", Strict: true, CheckLinks: true})
	if !report.IsConformant() {
		t.Fatalf("bundle is not conformant: %+v", report.Diagnostics)
	}
	want := map[string]string{"index.md": "yaml_unknown_directive", "README.md": "yaml_unknown_directive", "a.md": "yaml_future_minor_version"}
	for _, d := range report.Of(SeverityWarning) {
		if d.Code != "yaml_unknown_directive" && d.Code != "yaml_future_minor_version" {
			continue
		}
		if want[d.File] != d.Code || d.FieldPath != "frontmatter.line[1]" {
			t.Fatalf("duplicate or unexpected warning: %+v", d)
		}
		delete(want, d.File)
	}
	if len(want) != 0 {
		t.Fatalf("missing directive warnings: %v", want)
	}
}

func TestOKF03ExplicitSelectorAcceptsEmptyVersionAndPreservesLegacyRefusal(t *testing.T) {
	for _, declaration := range []string{"null", "''", "[]"} {
		t.Run(declaration, func(t *testing.T) {
			root := t.TempDir()
			writeValidationFile(t, root, "index.md", "---\nokf_version: "+declaration+"\nlicense: MIT\n---\n# Notes\n\n* [A](a.md)\n")
			writeValidationFile(t, root, "a.md", "---\ntype: Note\n---\nBody.\n")
			current := ValidatePath(root, &ValidatorConfig{Spec: "0.3"})
			if !current.IsConformant() || current.Version.Effective != "0.3" || current.VersionDeclaration.Present {
				t.Fatalf("0.3 empty declaration: %+v", current)
			}
			legacy := ValidatePath(root, &ValidatorConfig{Spec: "0.2"})
			if legacy.IsConformant() || !legacy.VersionDeclaration.Present || legacy.VersionDeclaration.Valid {
				t.Fatalf("0.2 empty declaration: %+v", legacy)
			}
		})
	}
}
