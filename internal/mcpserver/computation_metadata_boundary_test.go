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

func TestMCPAttestedComputationMetadataRejectsUnsafeTextBeforeBundleAccess(t *testing.T) {
	srv, err := mcptest.NewServer(t, mustServerTools(t)...)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	defer srv.Close()

	type fieldCase struct {
		name     string
		value    string
		wantCode string
		mutate   func(map[string]any, string)
	}
	cases := []fieldCase{
		{
			name: "runtime/NUL", value: "wasm\x00runtime", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) { contract["runtime"] = value },
		},
		{
			name: "parameter-name/control", value: "input\x01name", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) {
				contract["parameters"].([]any)[0].(map[string]any)["name"] = value
			},
		},
		{
			name: "parameter-type/DEL", value: "string\x7f", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) {
				contract["parameters"].([]any)[0].(map[string]any)["type"] = value
			},
		},
		{
			name: "computation-path/NUL", value: "references/query\x00.sql", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) {
				delete(contract, "inline_computation")
				delete(contract, "inline_language")
				contract["mode"] = "file"
				contract["computation_path"] = value
			},
		},
		{
			name: "inline-language/control", value: "w\x1fat", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) { contract["inline_language"] = value },
		},
		{
			name: "executor-resource/NUL", value: "urn:runner:\x00", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) {
				contract["executor"].(map[string]any)["resource"] = value
			},
		},
		{
			name: "executor-receipt/control", value: "dig\x02est", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) {
				contract["executor"].(map[string]any)["receipt"] = []any{value}
			},
		},
		{
			name: "attester-resource/DEL", value: "urn:attester:\x7f", wantCode: "invalid_request",
			mutate: func(contract map[string]any, value string) {
				contract["attester"].(map[string]any)["resource"] = value
			},
		},
		{
			name: "nested-metadata/oversize", value: strings.Repeat("x", 4097), wantCode: "resource_limit",
			mutate: func(contract map[string]any, value string) {
				contract["executor"].(map[string]any)["receipt"] = []any{value}
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
	validContract := func() map[string]any {
		return map[string]any{
			"mode": "inline", "runtime": "wasm",
			"parameters": []any{map[string]any{
				"name": "input", "type": "string", "required": true,
			}},
			"inline_computation": "(module)",
			"inline_language":    "wat",
			"executor": map[string]any{
				"resource": "urn:test:runner", "receipt": []any{"digest"},
			},
			"attester": map[string]any{"resource": "urn:test:attester"},
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
			t.Fatalf("%s changed bundle tree:\nbefore=%#v\nafter=%#v", name, before, after)
		}
		if _, statErr := os.Lstat(filepath.Join(root, ".okf")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s opened transactional store: %v", name, statErr)
		}
	}

	for _, test := range cases {
		t.Run("patch/"+test.name, func(t *testing.T) {
			// Arrange.
			root := newRoot(t)
			contract := validContract()
			test.mutate(contract, test.value)
			operation := map[string]any{
				"kind": "put_attested_computation", "concept_id": "a",
				"attested_computation": contract,
			}
			base := map[string]any{
				"bundle_path": root,
				"actor":       "mcp:metadata-boundary",
				"operations":  []any{operation},
			}

			// Act and assert.
			assertRejectedBeforeAccess(
				t,
				root,
				"preview_concept_patch",
				base,
				test.wantCode,
			)
			apply := cloneArguments(base)
			apply["expected_revision"] = "sha256:" + strings.Repeat("a", 64)
			apply["expected_plan_digest"] = "sha256:" + strings.Repeat("b", 64)
			assertRejectedBeforeAccess(
				t,
				root,
				"apply_concept_patch",
				apply,
				test.wantCode,
			)
		})

		t.Run("migration/"+test.name, func(t *testing.T) {
			// Arrange.
			root := newRoot(t)
			contract := validContract()
			test.mutate(contract, test.value)
			computation := map[string]any{
				"concept_id": "query",
				"contract":   contract,
			}
			if path, ok := contract["computation_path"].(string); ok {
				computation["asset"] = map[string]any{
					"path": path, "content": "SELECT 1;\n",
				}
			}
			base := map[string]any{
				"bundle_path":       root,
				"from":              "auto",
				"generated_by":      "process:migration",
				"timestamp_policy":  "preserve",
				"citation_mappings": []any{},
				"computations":      []any{computation},
			}

			// Act and assert.
			assertRejectedBeforeAccess(
				t,
				root,
				"preview_v02_migration",
				base,
				test.wantCode,
			)
			apply := cloneArguments(base)
			apply["expected_source"] = testMigrationSourceValue(
				mutation.MigrationVersionV02,
				string(mutation.MigrationTransitionTargetNoop),
			)
			assertRejectedBeforeAccess(
				t,
				root,
				"apply_v02_migration",
				apply,
				test.wantCode,
			)
		})
	}
}
