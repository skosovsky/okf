package mcpserver

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/skosovsky/okf/bundle"
	"github.com/skosovsky/okf/mutation"
)

func TestMCPMigrationMappingMetadataRejectsUnsafeTextBeforeSourceAccess(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	type mappingCase struct {
		name     string
		wantCode string
		mutate   func(map[string]any)
	}
	citation := func(fields map[string]any) []any {
		entry := map[string]any{"legacy_number": 1, "source_id": "source"}
		for name, value := range fields {
			entry[name] = value
		}
		return []any{map[string]any{
			"path": "a.md", "entries": []any{entry},
		}}
	}
	cases := []mappingCase{
		{
			name: "source-id/C0", wantCode: "invalid_request",
			mutate: func(arguments map[string]any) {
				arguments["citation_mappings"] = citation(map[string]any{
					"source_id": "source\x01",
				})
			},
		},
		{
			name: "title/NUL", wantCode: "invalid_request",
			mutate: func(arguments map[string]any) {
				arguments["citation_mappings"] = citation(map[string]any{
					"title": "Source\x00title",
				})
			},
		},
		{
			name: "resource/DEL", wantCode: "invalid_request",
			mutate: func(arguments map[string]any) {
				arguments["citation_mappings"] = citation(map[string]any{
					"resource": "urn:test:source\x7f",
				})
			},
		},
		{
			name: "source-id/ASCII-over-limit", wantCode: "resource_limit",
			mutate: func(arguments map[string]any) {
				arguments["citation_mappings"] = citation(map[string]any{
					"source_id": strings.Repeat("s", 4097),
				})
			},
		},
		{
			name: "title/multibyte-over-byte-limit", wantCode: "resource_limit",
			mutate: func(arguments map[string]any) {
				arguments["citation_mappings"] = citation(map[string]any{
					"title": strings.Repeat("é", 2049),
				})
			},
		},
	}
	newRoot := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		writeTestFile(t, root, "poison.md",
			"---\ntype: Note\n"+nestedYAMLValue(bundle.MaxYAMLPhysicalDepth+1)+"---\nPoison.\n",
		)
		return root
	}
	baseArguments := func(root string) map[string]any {
		return map[string]any{
			"bundle_path":       root,
			"from":              "auto",
			"generated_by":      "process:migration",
			"timestamp_policy":  "preserve",
			"citation_mappings": []any{},
		}
	}
	assertRejectedBeforeAccess := func(
		t *testing.T,
		root string,
		name string,
		arguments map[string]any,
		wantCode string,
	) {
		t.Helper()
		before := protocolTreeSnapshot(t, root)
		result := callMCPTool(t, srv, name, arguments)
		assertSchemaErrorEnvelope(t, result, wantCode)
		if after := protocolTreeSnapshot(t, root); !reflect.DeepEqual(after, before) {
			t.Fatalf("%s changed poison tree:\nbefore=%#v\nafter=%#v", name, before, after)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s opened transactional store: %v", name, statErr)
		}
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange.
			root := newRoot(t)
			previewArguments := baseArguments(root)
			test.mutate(previewArguments)

			// Act and assert.
			assertRejectedBeforeAccess(
				t,
				root,
				"preview_v02_migration",
				previewArguments,
				test.wantCode,
			)
			applyArguments := baseArguments(root)
			test.mutate(applyArguments)
			applyArguments["expected_source"] = testMigrationSourceValue(
				mutation.MigrationVersionV02,
				string(mutation.MigrationTransitionTargetNoop),
			)
			assertRejectedBeforeAccess(
				t,
				root,
				"apply_v02_migration",
				applyArguments,
				test.wantCode,
			)
		})
	}

	t.Run("valid Unicode and open punctuation remain exact", func(t *testing.T) {
		// Arrange.
		arguments := migrationArguments{
			TimestampPolicy:         "preserve",
			TimestampConflictPolicy: "reject",
			CitationMappings: []migrationCitationInput{{
				Path: "notes/résumé.md",
				Entries: []migrationCitationEntryInput{{
					LegacyEntry: "Résumé\r\n\t— “quoted”!? №1",
					SourceID:    "источник №1",
					Title:       "Доклад: «Практика»",
					Resource:    "urn:пример:источник?редакция=№1",
				}},
			}},
		}

		// Act.
		input, validationErr, result := canonicalMigrationInput(arguments)

		// Assert.
		if result != nil {
			t.Fatalf("valid Unicode/open punctuation rejected: %s", resultText(t, result))
		}
		if validationErr != nil {
			t.Fatalf("valid Unicode/open punctuation failed validation: %v", validationErr)
		}
		got := input.Citations[0].Entries[0]
		if got.LegacyEntry != arguments.CitationMappings[0].Entries[0].LegacyEntry ||
			got.SourceID != arguments.CitationMappings[0].Entries[0].SourceID ||
			got.Title != arguments.CitationMappings[0].Entries[0].Title ||
			got.Resource != arguments.CitationMappings[0].Entries[0].Resource {
			t.Fatalf("mapping metadata was normalized: %#v", got)
		}
	})
}
