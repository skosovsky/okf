package bundle

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOKF03ReadsOneYAMLDocumentWithDirectives(t *testing.T) {
	const frontmatter = "%YAML 1.2 # retain\r\n%TAG !x! tag:example.org,2026:\r\n--- # YAML mapping\r\n" +
		"type: Note\r\nexternal: !x!code preserved\r\n"
	const text = "---\r\n" + frontmatter + "---\r\n\r\nBody.\r\n"
	profile := VersionResolution{Effective: OKFVersion03}
	doc, err := ParseDocumentForVersionContext(t.Context(), text, profile)
	if err != nil || string(doc.FrontmatterYAML()) != frontmatter {
		t.Fatalf("document frontmatter = %q, %v", doc.FrontmatterYAML(), err)
	}
	value, found := doc.Frontmatter.Get("external")
	if !found || value.Tag != "tag:example.org,2026:code" || value.Value != "preserved" {
		t.Fatalf("extension = %+v", value)
	}
	if _, err := ParseDocument(text); err == nil {
		t.Fatal("legacy parser accepted YAML 1.2 directive")
	}
	if _, err := ParseFrontmatterForVersionContext(t.Context(), "type: Note\n--- {type: Other}\n", profile); err == nil || !strings.Contains(err.Error(), "expected one YAML document") {
		t.Fatalf("second document: error = %v", err)
	}
}

func TestOKF03ReportsCompatibilityDirectivesAndPreservesTheSource(t *testing.T) {
	profile := VersionResolution{Effective: OKFVersion03}
	const raw = "# first\r\n%OTHER opaque\r\n%YAML 1.010 # kept\r\n--- # YAML mapping\r\ntype: Note\r\n"
	f, err := ParseFrontmatterForVersionContext(t.Context(), raw, profile)
	if err != nil {
		t.Fatal(err)
	}
	notices := f.YAMLDirectiveWarnings()
	if len(notices) != 2 || notices[0].Code != "yaml_unknown_directive" || notices[0].Line != 2 ||
		notices[1].Code != "yaml_future_minor_version" || notices[1].Line != 3 {
		t.Fatalf("warnings = %+v", notices)
	}
	if retained, err := f.YAMLString(); err != nil || retained != raw {
		t.Fatalf("frontmatter = %q, %v", retained, err)
	}
	for _, input := range []string{
		"%OTHER opaque\ntype: Note\n",
		"%YAML 2.0\n--- # YAML mapping\ntype: Note\n",
		"%YAML 1.3\n%YAML 1.2\n--- # YAML mapping\ntype: Note\n",
	} {
		if _, err := ParseFrontmatterForVersionContext(t.Context(), input, profile); err == nil {
			t.Fatalf("malformed or unsupported directives were accepted: %q", input)
		}
	}
}

func TestOKF03ProjectsYAMLCoreValuesAndRetainsOriginalBytes(t *testing.T) {
	const frontmatter = "type: 2026-11-27\n" +
		"sources:\n  - resource: scope\n    usage_count: 011\n" +
		"extension: {count: 011, bin: 0b10, number: 079, label: ! true}\n"
	const input = "---\n" + frontmatter + "---\n\nBody.\n"
	doc, err := ParseDocumentForVersionContext(t.Context(), input, VersionResolution{Effective: OKFVersion03})
	if err != nil {
		t.Fatal(err)
	}
	if name, valid := doc.Frontmatter.Type(); !valid || name != "2026-11-27" {
		t.Fatalf("type = %q, %v", name, valid)
	}
	states := doc.SourceStates()
	if len(states) != 1 || !states[0].UsageCount.Valid || states[0].UsageCount.Raw != "011" || states[0].UsageCount.Value != 11 {
		t.Fatalf("sources = %+v", states)
	}
	type extension struct {
		Count, Number int
		Bin, Label    string
	}
	decoded, err := DecodeFrontmatter[struct{ Extension extension }](doc.Frontmatter)
	if err != nil || decoded.Extension != (extension{11, 79, "0b10", "true"}) {
		t.Fatalf("decoded = %+v, %v", decoded, err)
	}
	if yaml, err := doc.Frontmatter.YAMLString(); err != nil || yaml != frontmatter {
		t.Fatalf("retained YAML = %q, %v", yaml, err)
	}
	legacy, err := ParseDocument(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, valid := legacy.Frontmatter.Type(); valid {
		t.Fatal("legacy parser unexpectedly resolved a native timestamp as a string")
	}
	if states := legacy.SourceStates(); len(states) != 1 || states[0].UsageCount.Value != 9 {
		t.Fatalf("legacy sources = %+v", states)
	}
	legacyRoot := legacy.Frontmatter.YAMLNode()
	if got := legacyRoot.Content[1].Tag; got != "!!timestamp" || legacyRoot.Content[1].Kind != yaml.ScalarNode {
		t.Fatalf("legacy type tag = %q", got)
	}
}
