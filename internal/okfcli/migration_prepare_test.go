package okfcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/skosovsky/okf/bundle"
)

func TestPrepareMigrationInputsPreservesParserSelectorsAndAmbiguity(t *testing.T) {
	// Arrange.
	root := t.TempDir()
	contents := map[string]string{
		"index.md":            "---\nokf_version: '0.1'\n---\n\n# Knowledge\n",
		"numbered.md":         "# Note\n\nClaim [1].\n\n# Citations\n\n[1] [Spec](https://example.test/spec)\n",
		"bullet.md":           "# Note\n\n# Citations\n\n- https://example.test/bare\n",
		"mixed.md":            "# Note\n\n```md\n# Citations\n[1] fake\n```\n\n# Citations\n\n[1] [A](https://example.test/a)\n- [B](https://example.test/b)\n\n```md\n[9] fake citation\n```\n\nUnrecognized prose.\n",
		"duplicate.md":        "# Citations\n\n- [Same](https://example.test/same)\n- [Same](https://example.test/same)\n",
		"duplicate-number.md": "# Citations\n\n[1] https://example.test/a\n[1] https://example.test/b\n",
		"crlf.md":             "# Citations\r\n\r\n- [CRLF](https://example.test/crlf)\r\n",
		"undated.md":          "---\ntype: Knowledge\n---\n\n# Undated\n",
		"mixed-sources.md":    "---\ntype: Knowledge\ntimestamp: 2026-01-01T00:00:00Z\nsources:\n  - id: existing\n    resource: https://example.test/existing\n---\n\n# Citations\n\n[1] https://example.test/legacy\n",
		"leading-bom.md":      "\uFEFF---\ntype: Knowledge\n---\n\n# Citations\n\n[1] https://example.test/bom\n",
	}
	for name, content := range contents {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := &bundle.FileSystemSource{Root: root}
	before := make(map[string]string, len(contents))
	for key, value := range contents {
		before[key] = value
	}

	// Act.
	template, report, err := prepareMigrationInputs(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	again, againReport, err := prepareMigrationInputs(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}

	// Assert.
	if !reflect.DeepEqual(template, again) || !reflect.DeepEqual(report, againReport) || len(report.SourceSHA256) != 64 {
		t.Fatalf("prepare is not deterministic: %#v / %#v", template, report)
	}
	if report.Required.GeneratedBy != "" || report.Required.TimestampPolicy != "preserve" || report.Required.TimestampConflict != "reject" {
		t.Fatalf("preparation inferred actor or changed temporal policy: %#v", report.Required)
	}
	if len(report.GeneratedAt) != 1 || report.GeneratedAt[0].Path != "undated.md" || report.GeneratedAt[0].At != "" {
		t.Fatalf("missing historical instant not exposed as fillable input: %#v", report.GeneratedAt)
	}
	if len(template) != 4 {
		t.Fatalf("template documents = %#v", template)
	}
	for _, document := range template {
		for _, entry := range document.Entries {
			if entry.SourceID != "" || entry.Title != "" || entry.Resource != "" {
				t.Fatalf("template invented confirmed value: %#v", entry)
			}
		}
	}
	var numbered, bullet, mixed, crlf *migrationMappingTemplateDocument
	for index := range template {
		switch template[index].Path {
		case "numbered.md":
			numbered = &template[index]
		case "bullet.md":
			bullet = &template[index]
		case "mixed.md":
			mixed = &template[index]
		case "crlf.md":
			crlf = &template[index]
		}
	}
	if numbered == nil || bullet == nil || mixed == nil || crlf == nil ||
		len(numbered.Entries) != 1 || numbered.Entries[0].LegacyNumber != 1 ||
		len(bullet.Entries) != 1 || bullet.Entries[0].LegacyNumber != 0 ||
		!strings.Contains(bullet.Entries[0].LegacyEntry, "https://example.test/bare") ||
		len(mixed.Entries) != 2 || len(crlf.Entries) != 1 ||
		crlf.Entries[0].LegacyEntry != "[CRLF](https://example.test/crlf)" {
		t.Fatalf("selector projection = %#v", template)
	}
	for _, suggestion := range report.Suggestions {
		content := contents[suggestion.Path]
		if suggestion.StartByte < 0 || suggestion.EndByte > len(content) || suggestion.StartByte >= suggestion.EndByte ||
			!strings.Contains(content[suggestion.StartByte:suggestion.EndByte], suggestion.LegacyEntry) ||
			suggestion.Status != "proposal" {
			t.Fatalf("bad evidence span: %#v", suggestion)
		}
		if suggestion.Path == "bullet.md" && (suggestion.Title != "" || suggestion.Resource != "https://example.test/bare") {
			t.Fatalf("bare URL suggestion invented a title or lost resource: %#v", suggestion)
		}
	}
	var duplicateCount, duplicateNumberCount, proseCount, opaqueCount, mixedSourcesCount, leadingBOMCount int
	for _, unresolved := range report.Unresolved {
		switch unresolved.Reason {
		case "duplicate or unrepresentable parser-owned citation selectors":
			duplicateCount++
		case "duplicate_citation_number":
			duplicateNumberCount++
		case "unrecognized content in Citations section":
			proseCount++
		case "opaque content in Citations section":
			opaqueCount++
		case "sources_citations_conflict":
			mixedSourcesCount++
		case "leading_bom_unsupported":
			leadingBOMCount++
		}
	}
	if duplicateCount != 2 || duplicateNumberCount != 1 || proseCount == 0 || opaqueCount == 0 || mixedSourcesCount != 1 || leadingBOMCount != 1 {
		t.Fatalf("unresolved ambiguity/prose = %#v", report.Unresolved)
	}
	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != want {
			t.Fatalf("source changed %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".okf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only preparation opened store: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "bullet.md"), []byte(contents["bullet.md"]+"new bytes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, changed, err := prepareMigrationInputs(context.Background(), source)
	if err != nil || changed.SourceSHA256 == report.SourceSHA256 {
		t.Fatalf("source digest did not invalidate after input change: %v / %q", err, changed.SourceSHA256)
	}
}
