package okfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

func TestOKF03FormatPreservesExtensionValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "concept.md")
	const input = "---\r\ntype: Note\r\nmetadata: &external {ids: [42, '42']}\r\nsources: [{id: ref, resource: 'https://example.org', audit: *external}]\r\n---\r\n\r\nText.\r\n"
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code, err := cmdFmt([]string{path, "--spec", "0.3", "-w"}, &output); code != 0 || err != nil {
		t.Fatalf("format = %d, %v", code, err)
	}
	formatted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := func(raw string) map[string]any {
		t.Helper()
		doc, err := bundle.ParseDocument(raw)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := yaml.Unmarshal(doc.FrontmatterYAML(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if before, after := values(input), values(string(formatted)); !reflect.DeepEqual(before, after) {
		t.Fatalf("formatted values = %#v, want %#v", after, before)
	}
}

func TestOKF03CLIFormatRoundTripsCoreYAMLValues(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "policy.md")
	const header = "---\n%YAML 1.2\n--- # YAML mapping\ntype: 2026-11-27\nsources: {id: policy, resource: 'urn:test:policy', usage_count: 079}\n" +
		"usage_window: {from: 2026-11-01, to: 2026-11-30}\nexternal: {code: 0b10, label: ! true} # keep\n---\n"
	const indexHeader = "---\n%YAML 1.2\n--- # YAML mapping\nokf_version: '0.3'\n---\n"
	writeFixtureFile(t, filepath.Join(root, "index.md"), indexHeader+"# Notes\n\n- [Policy](policy.md)\n")
	writeFixtureFile(t, path, header+"\nRule.[^policy]\n\n[^policy]: Policy source.\n")
	parse := func() parseResponse {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := Run([]string{"parse", path, "--json"}, &stdout, &stderr); code != 0 {
			t.Fatalf("parse: %d %s", code, stderr.String())
		}
		var result parseResponse
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := parse()
	var stdout bytes.Buffer
	if code, err := cmdFmt([]string{path, "-w"}, &stdout); code != 0 || err != nil {
		t.Fatalf("format: %d %v", code, err)
	}
	after := parse()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte(header)) || !reflect.DeepEqual(before, after) {
		t.Fatalf("format changed YAML bytes or parse result: %q, %v", data, err)
	}
	if after.Type != "2026-11-27" || len(after.Sources) != 1 || after.Sources[0].UsageCount == nil ||
		*after.Sources[0].UsageCount != 79 || !after.SourcesAudit.Family.ShapeValid {
		t.Fatalf("typed sources = %+v, family = %+v", after.Sources, after.SourcesAudit.Family)
	}
	if code := Run([]string{"index", root}, &stdout, &stdout); code != 0 {
		t.Fatalf("index: %d %s", code, stdout.String())
	}
	index, err := os.ReadFile(filepath.Join(root, "index.md"))
	if err != nil || !bytes.HasPrefix(index, []byte(indexHeader)) {
		t.Fatalf("index changed YAML bytes: %q, %v", index, err)
	}
}

func TestOKF03CLIDiscoversConceptsAndIgnoresHistoricalReview(t *testing.T) {
	for _, mode := range []string{"declared", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			// Arrange.
			root := t.TempDir()
			files := map[string]string{
				"README.md": "# Read me\n",
				"policy.md": "---\ntype: Policy\ngenerated: {by: bot/v1, at: 2026-11-18T10:00:00Z}\nverified: {by: 'human:ada', at: 2026-11-18T09:00:00Z}\n---\n\nRevised policy.\n",
			}
			if mode == "declared" {
				files["index.md"] = "---\nokf_version: '0.3'\n---\n\n# Knowledge\n\n- [Policy](policy.md)\n"
			}
			for name, content := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			infoArgs := []string{"info", root, "--json"}
			validateArgs := []string{"validate", "--path", root, "--json"}
			if mode == "explicit" {
				infoArgs = append(infoArgs, "--spec", "0.3")
				validateArgs = append(validateArgs, "--spec", "0.3")
			}

			// Act.
			var info, validation, stderr bytes.Buffer
			infoCode := Run(infoArgs, &info, &stderr)
			validationCode := Run(validateArgs, &validation, &stderr)

			// Assert.
			if infoCode != 0 || validationCode != 0 || stderr.Len() != 0 {
				t.Fatalf("info=%d validate=%d stderr=%s validation=%s", infoCode, validationCode, &stderr, &validation)
			}
			var got infoResponse
			if err := json.Unmarshal(info.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Concepts != 1 || got.TrustTiers.Unverified != 1 || got.TrustTiers.HumanReviewed != 0 {
				t.Fatalf("unexpected info: %s", &info)
			}
			if !strings.Contains(validation.String(), "markdown_not_concept") {
				t.Fatalf("expected README warning: %s", &validation)
			}
		})
	}
}

func TestOKF03CLIComparesDateDeadlineAtUTCMidnight(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	for name, content := range map[string]string{
		"index.md":  "---\nokf_version: '0.3'\n---\n\n# Knowledge\n\n- [Policy](policy.md)\n",
		"policy.md": "---\ntype: Policy\nstale_after: 2026-11-18\nsources: {resource: 'urn:policy:1', last_modified: '2026-11-01T10:20:00+02:00'}\n---\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name  string
		at    string
		stale int
	}{
		{"before", "2026-11-18T00:59:59+01:00", 0},
		{"equal", "2026-11-17T19:00:00-05:00", 1},
		{"calendar date", "2026-11-18", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Act.
			var stdout, stderr bytes.Buffer
			code := Run([]string{"info", root, "--as-of", tt.at, "--json"}, &stdout, &stderr)

			// Assert.
			if code != 0 {
				t.Fatalf("info code %d: %s", code, &stderr)
			}
			var got infoResponse
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Stale != tt.stale || got.Sources != 1 {
				t.Fatalf("unexpected info: %s", &stdout)
			}
		})
	}
}

func TestOKF03InitPublishesBundleMetadata(t *testing.T) {
	// Arrange.
	root := filepath.Join(t.TempDir(), "delivery")

	// Act.
	var initOutput, infoOutput, stderr bytes.Buffer
	initCode := Run([]string{"init", root, "--spec", "0.3", "--title", "1e9999", "--description", "False", "--license", "CC-BY-4.0", "--json"}, &initOutput, &stderr)
	infoCode := Run([]string{"info", root, "--json"}, &infoOutput, &stderr)

	// Assert.
	if initCode != 0 || infoCode != 0 {
		t.Fatalf("init=%d info=%d stderr=%s", initCode, infoCode, &stderr)
	}
	var result initResult
	if err := json.Unmarshal(initOutput.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var info infoResponse
	if err := json.Unmarshal(infoOutput.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if result.OKFVersion != "0.3" || info.BundleMetadata == nil ||
		info.BundleMetadata.Title != "1e9999" || info.BundleMetadata.Description != "False" || info.BundleMetadata.License != "CC-BY-4.0" {
		t.Fatalf("init=%s info=%s", &initOutput, &infoOutput)
	}
}
